package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/textnorm"
)

// SuggestAuthors — typeahead по авторам.
//
// Стратегия:
//   - ПОДСТРОЧНОЕ совпадение по normalized_name (CITEXT), регистр-нечувствительно:
//     "достоев" → "Достоевский …", но и "роберт" → "Гэлбрейт Роберт" (имя — не
//     первое слово). Префиксные совпадения ранжируются выше (см. ORDER BY).
//   - без различия «ё/э/й» и «е/е/и» (#278, #516): «семенов» находит
//     «Семёнов», «бредбери» — «Брэдбери»; свёртка с обеих сторон
//     (textnorm.FoldName / NameFoldSQL); GIN trigram index по тому же
//     выражению (authors_name_fold_trgm, миграция 0059) ускоряет ILIKE '%…%'
//     на запросах ≥3 символов; на коротких (1-2 символа) планировщик может
//     выбрать seq scan, но при ~50-100K авторов это всё ещё <50 мс.
//   - строгих совпадений меньше limit — добор ПОХОЖИХ имён (опечатки, #516,
//     fuzzy_names.go): после строгих, меньше правок и известнее — выше.
//   - сортировка: сначала префиксные совпадения, затем по числу книг
//     (популярные наверху), потом по нормализованному имени для стабильности.
//   - видимость (грабля №14, #289): счётчик — по живым книгам, не скрытым
//     жанрами/языками/«Скрывать сборники» (как в списке и на карточке); авторы
//     без видимых книг не подсказываются — иначе «Вазов Иван (170)» при скрытом
//     болгарском вёл на карточку с 0 книг.
func (s *Service) SuggestAuthors(ctx context.Context, query string, limit int, excludeGenres, excludeLangs []string, hideCompilations bool) ([]AuthorSuggest, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return []AuthorSuggest{}, nil
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	exClause, exArgs := bookExclusionClause(3, excludeGenres, excludeLangs, hideCompilations)
	args := append([]any{textnorm.FoldName(escapeLike(q)), limit}, exArgs...)
	// Кириллица — по свёрнутому имени, латиница — по латинскому имени из fb2
	// переводов (миграция 0046): «doyle» находит «Дойль Артур Конан». Голый
	// latin_name, без COALESCE: выражение обязано совпадать с индексным
	// (authors_latin_name_trgm), иначе OR валит запрос в полный проход по
	// всем авторам (165 мс на нажатие против 5 мс); NULL и так не совпадает.
	whereCyr, args := wordConditions(authorNameFold, q, args)
	whereLat, args := wordConditions("a.latin_name", q, args)
	where := "((" + whereCyr + ") OR (" + whereLat + "))"

	// Кандидаты — дёшево (без видимости, по известности authors.renown), видимые
	// книги считаются только для них: раньше COUNT шёл по каждому совпадению (на
	// «тол» — 2,5 тыс. авторов, 230 мс на нажатие; теперь 34 мс, #309). Слова
	// запроса — каждое отдельно, поэтому «лев толстой» находит «Толстой Лев
	// Николаевич»; совпадение фразы целиком — выше.
	rows, err := s.pool.Query(ctx, `
		WITH cand AS (
			SELECT a.id, a.last_name, a.first_name, a.middle_name, COALESCE(a.name_note, '') AS note,
			       a.normalized_name::text AS nn,
			       (`+authorNameFold+` ILIKE $1 || '%' ESCAPE '\') AS prefix,
			       (`+authorNameFold+` ILIKE '%' || $1 || '%' ESCAPE '\') AS phrase
			FROM authors a
			WHERE NOT a.instance_hidden AND `+where+`
			ORDER BY prefix DESC, phrase DESC, a.renown DESC, a.id
			LIMIT `+suggestCandidates+`
		)
		SELECT id, last_name, first_name, middle_name, note, cnt FROM (
			SELECT cand.*,
			       (SELECT COUNT(DISTINCT COALESCE(b.work_id, -b.id)) FROM book_authors ba
			        JOIN books b ON b.id = ba.book_id
			        WHERE ba.author_id = cand.id AND b.deleted = false`+exClause+`) AS cnt
			FROM cand
		) x
		WHERE cnt > 0
		ORDER BY prefix DESC, phrase DESC, cnt DESC, nn
		LIMIT $2
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query author suggestions: %w", err)
	}
	out, err := scanAuthorSuggests(rows, limit)
	if err != nil || len(out) >= limit {
		return out, err
	}

	// Добор похожих (#516): порядок — fuzzyAuthors, видимые книги — как у строгих.
	strictIDs := make([]int64, len(out))
	for i, a := range out {
		strictIDs[i] = a.ID
	}
	hits, err := s.fuzzyAuthors(ctx, fuzzyWords(q), strictIDs, suggestFuzzyCandidates)
	if err != nil || len(hits) == 0 {
		return out, err
	}
	exClause, exArgs = bookExclusionClause(3, excludeGenres, excludeLangs, hideCompilations)
	rows, err = s.pool.Query(ctx, `
		SELECT a.id, a.last_name, a.first_name, a.middle_name, COALESCE(a.name_note, ''), v.cnt
		FROM unnest($1::bigint[]) WITH ORDINALITY u(id, ord)
		JOIN authors a ON a.id = u.id
		CROSS JOIN LATERAL (
			SELECT COUNT(DISTINCT COALESCE(b.work_id, -b.id)) AS cnt FROM book_authors ba
			JOIN books b ON b.id = ba.book_id
			WHERE ba.author_id = a.id AND b.deleted = false`+exClause+`
		) v
		WHERE v.cnt > 0
		ORDER BY u.ord
		LIMIT $2
	`, append([]any{hitIDs(hits), limit - len(out)}, exArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("query fuzzy author suggestions: %w", err)
	}
	more, err := scanAuthorSuggests(rows, limit-len(out))
	if err != nil {
		return nil, err
	}
	return append(out, more...), nil
}

// scanAuthorSuggests — строки (id, ФИО, уточнение, видимых работ) подсказки.
func scanAuthorSuggests(rows pgx.Rows, limit int) ([]AuthorSuggest, error) {
	defer rows.Close()
	out := make([]AuthorSuggest, 0, limit)
	for rows.Next() {
		var (
			id                  int64
			last, first, middle string
			note                string
			cnt                 int
		)
		if err := rows.Scan(&id, &last, &first, &middle, &note, &cnt); err != nil {
			return nil, err
		}
		out = append(out, AuthorSuggest{
			ID:        id,
			FullName:  fullName(last, first, middle),
			Note:      books.DisplayNote(note),
			BookCount: cnt,
		})
	}
	return out, rows.Err()
}

// SuggestSeries — typeahead по сериям.
// Принцип тот же, что и для авторов: ПОДСТРОЧНОЕ ILIKE на свёрнутом
// normalized_title + trigram GIN index (так "страйк" находит «Корморан Страйк»
// — не первое слово); префиксные совпадения ранжируются выше; строгих меньше
// limit — добор похожих названий (#516), с большим числом книг — выше. AuthorName заполняется LEFT JOIN
// если серия привязана к одному автору (это поле опционально в схеме).
// Видимость — как у SuggestAuthors (#289): пустые и целиком скрытые серии не
// подсказываются.
func (s *Service) SuggestSeries(ctx context.Context, query string, limit int, excludeGenres, excludeLangs []string, hideCompilations bool) ([]SeriesSuggest, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return []SeriesSuggest{}, nil
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	exClause, exArgs := bookExclusionClause(3, excludeGenres, excludeLangs, hideCompilations)
	args := append([]any{textnorm.FoldName(escapeLike(q)), limit}, exArgs...)
	where, args := wordConditions(seriesNameFold, q, args)
	// Переименованную правкой серию (#379) ищем и по новому названию: ключ
	// normalized_title остаётся прежним (по нему серию узнаёт импорт). Отдельной
	// веткой UNION, не через OR: OR с подзапросом валил запрос в полный проход
	// по всем сериям (170 мс на нажатие); правок названий — единицы.
	renamed, args := wordConditions(textnorm.NameFoldSQL("lower(s.title)"), q, args)

	// Как у авторов: дешёвые кандидаты, видимые книги — только для них (#309).
	rows, err := s.pool.Query(ctx, `
		WITH m AS (
			SELECT s.id FROM series s WHERE `+where+`
			UNION
			SELECT s.id FROM metadata_overrides o JOIN series s ON s.id = o.target_id
			WHERE o.target_kind = 'series' AND o.field = 'title' AND `+renamed+`
		), cand AS (
			SELECT s.id, s.title, s.author_id, s.normalized_title::text AS nt,
			       (`+seriesNameFold+` ILIKE $1 || '%' ESCAPE '\') AS prefix,
			       (`+seriesNameFold+` ILIKE '%' || $1 || '%' ESCAPE '\') AS phrase
			FROM series s
			JOIN m ON m.id = s.id
			ORDER BY prefix DESC, phrase DESC,
			         (SELECT count(*) FROM books b WHERE b.series_id = s.id) DESC, s.id
			LIMIT `+suggestCandidates+`
		)
		SELECT id, title, author_name, cnt FROM (
			SELECT cand.id, cand.title, cand.prefix, cand.phrase, cand.nt,
			       COALESCE(NULLIF(TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name)), ''), '') AS author_name,
			       (SELECT COUNT(DISTINCT COALESCE(b.work_id, -b.id)) FROM books b
			        WHERE b.series_id = cand.id AND b.deleted = false`+exClause+`) AS cnt
			FROM cand
			LEFT JOIN authors a ON a.id = cand.author_id
		) x
		WHERE cnt > 0
		ORDER BY prefix DESC, phrase DESC, cnt DESC, nt
		LIMIT $2
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query series suggestions: %w", err)
	}
	out, err := scanSeriesSuggests(rows, limit)
	if err != nil || len(out) >= limit {
		return out, err
	}

	// Добор похожих названий (#516); внутри одного числа правок — больше книг выше.
	strictIDs := make([]int64, len(out))
	for i, x := range out {
		strictIDs[i] = x.ID
	}
	hits, err := s.fuzzySeries(ctx, fuzzyWords(q), strictIDs, suggestFuzzyCandidates)
	if err != nil || len(hits) == 0 {
		return out, err
	}
	exClause, exArgs = bookExclusionClause(3, excludeGenres, excludeLangs, hideCompilations)
	rows, err = s.pool.Query(ctx, `
		SELECT s.id, s.title,
		       COALESCE(NULLIF(TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name)), ''), ''),
		       v.cnt
		FROM unnest($1::bigint[]) WITH ORDINALITY u(id, ord)
		JOIN series s ON s.id = u.id
		LEFT JOIN authors a ON a.id = s.author_id
		CROSS JOIN LATERAL (
			SELECT COUNT(DISTINCT COALESCE(b.work_id, -b.id)) AS cnt FROM books b
			WHERE b.series_id = s.id AND b.deleted = false`+exClause+`
		) v
		WHERE v.cnt > 0
		ORDER BY u.ord
		LIMIT $2
	`, append([]any{hitIDs(hits), limit - len(out)}, exArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("query fuzzy series suggestions: %w", err)
	}
	more, err := scanSeriesSuggests(rows, limit-len(out))
	if err != nil {
		return nil, err
	}
	return append(out, more...), nil
}

// scanSeriesSuggests — строки (id, название, автор, видимых работ) подсказки.
func scanSeriesSuggests(rows pgx.Rows, limit int) ([]SeriesSuggest, error) {
	defer rows.Close()
	out := make([]SeriesSuggest, 0, limit)
	for rows.Next() {
		var (
			id     int64
			title  string
			author string
			cnt    int
		)
		if err := rows.Scan(&id, &title, &author, &cnt); err != nil {
			return nil, err
		}
		out = append(out, SeriesSuggest{
			ID:         id,
			Title:      title,
			AuthorName: author,
			BookCount:  cnt,
		})
	}
	return out, rows.Err()
}

// suggestCandidates — сколько совпадений подсказки берём в точный подсчёт
// видимых книг; suggestFuzzyCandidates — то же для похожих (#516).
const (
	suggestCandidates      = "50"
	suggestFuzzyCandidates = 50
)

// wordConditions — условие «каждое слово запроса есть в поле» (порядок слов
// любой: «лев толстой» ~ «толстой лев николаевич»); слова дописываются в args.
// Не больше 6 слов — дальше подсказка и так ничего не уточнит.
func wordConditions(field, query string, args []any) (string, []any) {
	words := strings.Fields(textnorm.FoldName(escapeLike(query)))
	if len(words) > 6 {
		words = words[:6]
	}
	conds := make([]string, 0, len(words))
	for _, w := range words {
		args = append(args, w)
		conds = append(conds, fmt.Sprintf(`%s ILIKE '%%' || $%d || '%%' ESCAPE '\'`, field, len(args)))
	}
	return strings.Join(conds, " AND "), args
}

// escapeLike экранирует спецсимволы LIKE (%, _ и сам \) — запрос пользователя
// ищется как текст, а не как шаблон (#309).
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
