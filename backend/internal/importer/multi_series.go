package importer

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
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
	multi := map[string]bool{}
	for title, byAuthor := range booksBySeries {
		if len(byAuthor) >= multiSeriesMinAuthors && !hasDominantAuthor(byAuthor) {
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
		multi[title] = true
	}
	return multi, rows.Err()
}

// hasDominantAuthor — у одного автора не меньше половины книг серии.
func hasDominantAuthor(byAuthor map[string]int) bool {
	total, top := 0, 0
	for _, n := range byAuthor {
		total += n
		top = max(top, n)
	}
	return top*2 >= total
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
