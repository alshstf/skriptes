package importer

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/authorkind"
	"github.com/skriptes/skriptes/backend/internal/inpx"
)

// multiSeriesMinAuthors — с какого числа разных первых авторов серия считается
// межавторской/издательской (решение владельца: 3; план
// inpx-2026-09-authors-series). librusec с выпуска 2026-09 проставляет книгам
// издательские серии («Мини-Шарм», «Любовный роман (Центрполиграф)»), а серия у
// нас заводилась по паре «название + первый автор» — одна издательская серия
// дробилась на тысячи «циклов», по одному в карточке каждого автора.
const multiSeriesMinAuthors = 3

// planMultiSeries — проход по INPX до импорта: названия серий, под которыми
// книги ≥ multiSeriesMinAuthors разных первых авторов и ни у одного из них нет
// половины книг, плюс уже помеченные такими в базе (признак липкий — иначе
// серия, у которой в следующем выпуске окажется двое авторов, снова
// развалилась бы на «циклы»).
//
// Доминирующий автор — это его цикл с редкими чужими книгами (продолжения,
// ошибки атрибуции), а не издательская серия: прогон на librusec 2026-09 —
// «Ниро Вульф» (Стаут, 309 из 313), «Колесо времени» (Джордан, 83 из 88); таких
// названий 941 из 6 200 с ≥3 авторами.
//
// Названия из одних жанровых слов («Рассказы», «Повести и рассказы») не
// становятся межавторскими ни из INPX, ни из базы — см. isGenericSeriesTitle.
func (im *Importer) planMultiSeries(ctx context.Context, ix *inpx.Inpx) (map[string]bool, error) {
	booksBySeries := map[string]map[string]int{} // название → первый автор → книг
	err := ix.Each(func(_ inpx.InpFile, rec inpx.Record) error {
		if rec.Series == "" || len(rec.Authors) == 0 {
			return nil
		}
		title := normalize(rec.Series)
		if title == "" {
			return nil
		}
		byAuthor := booksBySeries[title]
		if byAuthor == nil {
			byAuthor = map[string]int{}
			booksBySeries[title] = byAuthor
		}
		byAuthor[authorKey(rec.Authors[0])]++
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan series authors: %w", err)
	}
	service, err := im.serviceAuthorKeys(ctx)
	if err != nil {
		return nil, err
	}
	isService := func(key string) bool {
		if v, ok := service[key]; ok {
			return v
		}
		name, _, _ := strings.Cut(key, "\x00")
		return authorkind.IsServiceName(name)
	}
	multi := map[string]bool{}
	for title, byAuthor := range booksBySeries {
		if len(byAuthor) >= multiSeriesMinAuthors && !hasDominantAuthor(byAuthor, isService) && !isGenericSeriesTitle(title) {
			multi[title] = true
		}
	}
	rows, err := im.deps.Pool.Query(ctx, `SELECT normalized_title::text FROM series WHERE kind = 'multi'`)
	if err != nil {
		return nil, fmt.Errorf("load multi series: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			return nil, err
		}
		if !isGenericSeriesTitle(title) {
			multi[title] = true
		}
	}
	return multi, rows.Err()
}

// genericSeriesWords — жанровые слова, из которых состоит «серия» вида
// «Рассказы», «Повести и рассказы», «Мемуары, дневники, письма» (решение
// владельца 2026-09-27). В INPX и fb2 у серии нет идентификатора, только
// название, а такие названия у разных авторов совпадают сами собой: в
// librusec 2026-09 серия «Рассказы» у 276 авторов (у Чехова 51 книга) — это
// «рассказы этого автора», а не одна издательская серия.
var genericSeriesWords = map[string]bool{
	"рассказы": true, "рассказ": true, "рассказов": true,
	"повести": true, "повесть": true, "повестей": true,
	"романы": true, "роман": true, "романов": true,
	"сказки": true, "сказка": true, "сказок": true,
	"стихи": true, "стихов": true, "стихотворения": true, "стихотворение": true, "стихотворений": true,
	"поэмы": true, "поэма": true, "поэм": true,
	"пьесы": true, "пьеса": true, "пьес": true,
	"статьи": true, "статья": true, "статей": true,
	"очерки": true, "очерк": true, "очерков": true,
	"новеллы": true, "новелла": true, "новелл": true,
	"эссе": true, "миниатюры": true, "басни": true, "притчи": true, "фельетоны": true, "юморески": true,
	"публицистика": true, "проза": true, "поэзия": true, "драматургия": true,
	"мемуары": true, "воспоминания": true, "дневники": true, "письма": true, "интервью": true,
	"сборник": true, "сборники": true, "избранное": true, "избранные": true,
	"произведения": true, "сочинения": true,
	"stories": true, "short": true, "novels": true, "poems": true, "essays": true,
}

// isGenericSeriesTitle — нормализованное название серии целиком из
// genericSeriesWords (через пробел, запятую, «и»). Такая серия не становится
// межавторской: у каждого автора своя, как было до kind='multi'.
func isGenericSeriesTitle(title string) bool {
	words := strings.FieldsFunc(title, func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == '.' || r == ';'
	})
	generic := false
	for _, w := range words {
		if w == "и" {
			continue
		}
		if !genericSeriesWords[w] {
			return false
		}
		generic = true
	}
	return generic
}

// hasDominantAuthor — у одного автора не меньше половины книг серии.
// Служебный автор («Журнал «Вокруг света»», «Анекдоты | Автор неизвестен»)
// доминирующим не бывает: серия журнала или сборника с чужими книгами — это
// издательская серия, а не чей-то цикл (прод 2026-09: «Вокруг света (журнал)» —
// 418 из 452 у журнала и 18 обрывков-«циклов» у остальных авторов, #298). Его
// книги при этом входят в общее число.
func hasDominantAuthor(byAuthor map[string]int, isService func(key string) bool) bool {
	total, top := 0, 0
	for key, n := range byAuthor {
		total += n
		if !isService(key) {
			top = max(top, n)
		}
	}
	return top*2 >= total
}

// serviceAuthorKeys — решения о служебности авторов, уже принятые в базе
// (authorKey → is_service): метки эвристики и ручные в обе стороны (админ снял
// метку — автор снова может быть доминирующим). Авторов, которых в базе нет
// (свежая база, новые имена выпуска), planMultiSeries проверяет правилом
// authorkind напрямую.
func (im *Importer) serviceAuthorKeys(ctx context.Context) (map[string]bool, error) {
	rows, err := im.deps.Pool.Query(ctx, `
		SELECT normalized_name::text, lower(btrim(COALESCE(name_note, ''))), is_service
		FROM authors
		WHERE is_service OR is_service_source = 'manual'`)
	if err != nil {
		return nil, fmt.Errorf("load service authors: %w", err)
	}
	defer rows.Close()
	res := map[string]bool{}
	for rows.Next() {
		var name, note string
		var svc bool
		if err := rows.Scan(&name, &note, &svc); err != nil {
			return nil, err
		}
		res[name+"\x00"+note] = svc // формат authorKey
	}
	return res, rows.Err()
}

// moveSeriesSubscriptions — подписки на прежние «циклы» автора с названием
// межавторской серии, из которых импорт увёл все книги в общую серию, —
// переносятся на общую. Пустые «циклы» потом удалит deleteEmptySeries.
func moveSeriesSubscriptions(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	const fragments = `
		FROM series frag
		JOIN series m ON m.kind = 'multi' AND m.author_id IS NULL
		             AND m.normalized_title = frag.normalized_title AND m.id <> frag.id
		WHERE frag.author_id IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM books b WHERE b.series_id = frag.id)`
	tag, err := pool.Exec(ctx, `
		INSERT INTO favorite_series (user_id, series_id)
		SELECT f.user_id, m.id
		FROM favorite_series f
		JOIN (SELECT frag.id AS frag_id, m.id `+fragments+`) x ON x.frag_id = f.series_id
		JOIN series m ON m.id = x.id
		ON CONFLICT DO NOTHING`)
	if err != nil {
		return 0, fmt.Errorf("copy series subscriptions: %w", err)
	}
	if _, err := pool.Exec(ctx, `
		DELETE FROM favorite_series f
		WHERE f.series_id IN (SELECT frag.id `+fragments+`)`); err != nil {
		return 0, fmt.Errorf("drop fragment subscriptions: %w", err)
	}
	return tag.RowsAffected(), nil
}
