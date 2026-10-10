package metadata

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Год работы (#288). fb2 <title-info><date> на проде — единственный источник,
// и он врёт: «Хаджи-Мурат» 2020, «Белые ночи» 1986; у 7,4 тыс. книг год написания
// позже года издания. Сверка выборки с Фантлабом (2026-10-04, 30 переводов и 31
// оригинал): fb2-год совпадает в 83–87 %, ошибается почти только позже настоящего
// (переиздание/перевод) — поэтому «у переводов fb2 не брать» отвергнуто (выбросило
// бы верные годы), а принято «самый ранний правдоподобный год»:
//
//   - fb2-год издания правдоподобен, если он в [1000, текущий]; год позже года
//     этого издания заменяется годом издания (верхняя граница: это чаще дата
//     электронной книги, чем другой век; plausibleFb2Year при извлечении,
//     CleanImplausibleBookYears — для накопленного);
//   - год работы = самый ранний из правдоподобных fb2-годов её изданий и внешнего
//     года (works.external_year: Фантлаб — год первой публикации); если самый
//     ранний год издания работы ещё раньше, он и есть потолок («год написания /
//     первого издания»);
//   - ручная правка года (metadata_overrides) неприкосновенна.
//
// Опечатки и заглушки (#465: «Сами боги» Азимова — 1073 из одного французского
// издания; ~30 работ с годом 1000–1449 на проде 2026-10):
//
//   - год раньше рождения основного автора + 10 — не год написания (годы жизни —
//     authors.born_year, author_lifetime.go). Только для авторов, родившихся с
//     1500 года: у средневековых дата рождения в Wikidata бывает с точностью до
//     века, а их настоящие ранние годы правило задело бы;
//   - год Фантлаба (первая публикация) сильнее fb2-года, который раньше него больше
//     чем на fantlabYearLead лет и сам раньше minEditionYear — заведомая опечатка
//     (1014, 1073, 1282). Без второго условия правило ломало посмертные публикации:
//     «История Петра I» Пушкина написана в 1835-м, а Фантлаб даёт 1938 (прод 1.38.0);
//   - год издания раньше minEditionYear — заглушка (книгопечатания не было), а
//     fb2-год, равный такому году издания, — тоже, кроме старинной литературы и
//     фольклора (ancientGenreSQL).

const minPlausibleYear = 1000

// ancientBooksSQL — издания старинной литературы и фольклора: их год издания
// раньше minEditionYear — не заглушка, а год текста, продублированный издателем
// («Филострато» Боккаччо — 1335). Набором, а не EXISTS на строку: план с
// коррелированным подзапросом на проде упирался в минуты.
// Только издания работ из lo — пересчёт пары работ (его зовёт группировка) не
// сканирует все жанры.
const ancientBooksSQL = `SELECT DISTINCT bg.book_id
		FROM lo JOIN books ab ON ab.work_id = lo.work_id
		JOIN book_genres bg ON bg.book_id = ab.id JOIN genres g ON g.id = bg.genre_id
		WHERE g.fb2_code LIKE 'antique%' OR g.fb2_code LIKE 'folk%' OR g.fb2_code = 'epic'`

const (
	minEditionYear      = 1450 // раньше — заглушка, а не год издания
	minLifetimeBornYear = 1500 // правило «не раньше рождения» — для авторов не раньше этого года рождения
	bornWritingAge      = 10   // год работы не раньше рождения автора + столько лет
	fantlabYearLead     = 100  // насколько fb2-год раньше года Фантлаба, чтобы считаться опечаткой
)

// plausibleYear — год в [1000, текущий], иначе 0.
func plausibleYear(y int) int {
	if y < minPlausibleYear || y > time.Now().Year() {
		return 0
	}
	return y
}

// plausibleFb2Year — fb2-год написания издания: вне [1000, текущий] — 0; позже
// года издания (edition 0 — неизвестен) — год издания, верхняя граница.
func plausibleFb2Year(written, edition int) int {
	written = plausibleYear(written)
	if written > 0 && plausibleYear(edition) > 0 && written > edition {
		return edition
	}
	return written
}

// recomputeWorkYears — works.written_year/source по правилу файла для работ ids
// (allWorks — для всех). Возвращает работы, у которых год изменился.
func recomputeWorkYears(ctx context.Context, ex pgxExec, ids []int64, allWorks bool) ([]int64, error) {
	rows, err := ex.Query(ctx, `
		WITH lo AS (
		    -- Нижняя граница года работы: рождение основного автора + bornWritingAge
		    -- (только для родившихся с minLifetimeBornYear), иначе minPlausibleYear.
		    SELECT w.id AS work_id,
		           GREATEST($3, COALESCE(CASE WHEN a.born_year >= $6 THEN a.born_year + $7 END, $3)) AS y
		    FROM works w LEFT JOIN authors a ON a.id = w.primary_author_id
		    WHERE ($2 OR w.id = ANY($1))
		), anc AS (`+ancientBooksSQL+`
		), ed AS (
		    SELECT b.work_id,
		           min(b.written_year) FILTER (WHERE b.written_year BETWEEN lo.y AND $4 AND CASE WHEN b.written_year = b.edition_year AND b.edition_year < $5 THEN anc.book_id IS NOT NULL ELSE true END) AS wy,
		           (array_agg(b.written_year_source ORDER BY b.written_year)
		              FILTER (WHERE b.written_year BETWEEN lo.y AND $4 AND CASE WHEN b.written_year = b.edition_year AND b.edition_year < $5 THEN anc.book_id IS NOT NULL ELSE true END))[1] AS wsrc,
		           min(b.edition_year) FILTER (WHERE b.edition_year BETWEEN GREATEST(lo.y, $5) AND $4) AS ey
		    FROM books b JOIN lo ON lo.work_id = b.work_id
		    LEFT JOIN anc ON anc.book_id = b.id
		    WHERE b.deleted = false AND b.work_id IS NOT NULL
		    GROUP BY b.work_id
		), calc AS (
		    SELECT w.id,
		           -- fb2-год с потолком по самому раннему изданию.
		           CASE WHEN ed.wy IS NOT NULL AND ed.ey IS NOT NULL AND ed.ey < ed.wy THEN ed.ey ELSE ed.wy END AS by,
		           CASE WHEN ed.wy IS NOT NULL AND ed.ey IS NOT NULL AND ed.ey < ed.wy THEN 'edition_year' ELSE ed.wsrc END AS bsrc,
		           CASE WHEN w.external_year >= lo.y THEN w.external_year END AS xy, w.external_year_source AS xsrc
		    FROM works w JOIN lo ON lo.work_id = w.id LEFT JOIN ed ON ed.work_id = w.id
		    WHERE NOT EXISTS (SELECT 1 FROM metadata_overrides o
		                      WHERE o.target_kind = 'work' AND o.target_id = w.id AND o.field = 'written_year')
		), pick AS (
		    -- Внешний год, если раньше fb2-года; год Фантлаба — и если fb2-год раньше
		    -- него больше чем на fantlabYearLead лет и раньше minEditionYear (опечатка
		    -- fb2, #465; посмертная публикация — не опечатка).
		    SELECT id, by, bsrc, xy, xsrc,
		           xy IS NOT NULL AND (by IS NULL OR xy < by OR (xsrc = 'fantlab' AND by < xy - $8 AND by < $5)) AS ext
		    FROM calc
		), fin AS (
		    SELECT id,
		           CASE WHEN ext THEN xy ELSE by END AS y,
		           CASE WHEN ext THEN xsrc ELSE bsrc END AS src
		    FROM pick
		)
		UPDATE works w SET written_year = fin.y, written_year_source = fin.src
		FROM fin
		WHERE w.id = fin.id
		  AND (w.written_year IS DISTINCT FROM fin.y OR w.written_year_source IS DISTINCT FROM fin.src)
		RETURNING w.id`, ids, allWorks, minPlausibleYear, time.Now().Year(),
		minEditionYear, minLifetimeBornYear, bornWritingAge, fantlabYearLead)
	if err != nil {
		return nil, fmt.Errorf("recompute work years: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// CleanImplausibleBookYears — разовая чистка накопленных fb2-годов изданий по
// правилу plausibleFb2Year (на проде: 7,4 тыс. позже года издания — к году
// издания, 3 в будущем — пусто) и пересчёт года всех работ. Возвращает работы с
// изменившимся годом.
func CleanImplausibleBookYears(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	if _, err := pool.Exec(ctx, `
		UPDATE books SET written_year = NULL, written_year_source = NULL
		WHERE written_year_source = 'fb2_title' AND (written_year < $1 OR written_year > $2)`,
		minPlausibleYear, time.Now().Year()); err != nil {
		return nil, fmt.Errorf("clean implausible book years: %w", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE books SET written_year = edition_year, written_year_source = 'edition_year'
		WHERE written_year_source = 'fb2_title'
		  AND edition_year BETWEEN $1 AND $2 AND written_year > edition_year`,
		minPlausibleYear, time.Now().Year()); err != nil {
		return nil, fmt.Errorf("cap book years by edition: %w", err)
	}
	return recomputeWorkYears(ctx, pool, nil, true)
}

// CleanExternalBookYears — разовая чистка внешних годов изданий (1.39.3, прод
// 2026-10-11): год OpenLibrary, найденный по кириллическому названию (год позднего
// русского издания: «Айвенго» 2007), и любой внешний год позже года издания книги.
// Год книги — пустой, найденная попытка стёрта: следующий проход пропустит
// OpenLibrary (кириллица) или отклонит год, и книгу спросит Wikidata. Затем год
// всех работ — по правилам файла (воркер года до 1.39.3 его не пересчитывал).
// Возвращает работы с изменившимся годом и число очищенных изданий.
func CleanExternalBookYears(ctx context.Context, pool *pgxpool.Pool) ([]int64, int64, error) {
	tag, err := pool.Exec(ctx, `
		WITH bad AS (
		    SELECT id FROM books
		    WHERE (written_year_source = 'openlibrary'
		           AND (CASE WHEN COALESCE(src_title, '') <> '' THEN src_title ELSE title END) ~ '[Ѐ-ӿ]')
		       OR (written_year_source IN ('openlibrary', 'wikidata')
		           AND edition_year BETWEEN $1 AND $2 AND written_year > edition_year)
		), del AS (
		    DELETE FROM book_year_lookups l USING bad
		    WHERE l.book_id = bad.id AND l.outcome = 'found'
		)
		UPDATE books b SET written_year = NULL, written_year_source = NULL
		FROM bad WHERE b.id = bad.id`, minEditionYear, time.Now().Year())
	if err != nil {
		return nil, 0, fmt.Errorf("clean external book years: %w", err)
	}
	changed, err := recomputeWorkYears(ctx, pool, nil, true)
	return changed, tag.RowsAffected(), err
}

// CleanOpenLibraryYears — разовая чистка после того, как OpenLibrary убран из
// источников года (1.39.4): годы изданий от OpenLibrary — пустые, его попытки
// стёрты (книги снова спросит Wikidata), год всех работ — по правилам файла.
// Возвращает работы с изменившимся годом и число очищенных изданий.
func CleanOpenLibraryYears(ctx context.Context, pool *pgxpool.Pool) ([]int64, int64, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE books SET written_year = NULL, written_year_source = NULL
		WHERE written_year_source = 'openlibrary'`)
	if err != nil {
		return nil, 0, fmt.Errorf("clean openlibrary book years: %w", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM book_year_lookups WHERE source = 'openlibrary'`); err != nil {
		return nil, 0, fmt.Errorf("drop openlibrary year lookups: %w", err)
	}
	changed, err := recomputeWorkYears(ctx, pool, nil, true)
	return changed, tag.RowsAffected(), err
}

// RecomputeWorkYears — пересчёт года работ ids (после записи внешнего года).
func RecomputeWorkYears(ctx context.Context, pool *pgxpool.Pool, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	return recomputeWorkYears(ctx, pool, ids, false)
}
