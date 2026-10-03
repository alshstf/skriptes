package metadata

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// queryRower — минимальный QueryRow (удовлетворяют и *pgxpool.Pool, и pgx.Tx).
type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// dominantLang возвращает самый частый нормализованный код языка по живым
// книгам — «язык библиотеки». Пусто, если книг с языком нет. lower(btrim(...))
// зеркалит нормализацию импорта (грабля №14), чтобы 'RU'/'ru-RU' не дробились.
func dominantLang(ctx context.Context, ex queryRower) (string, error) {
	var lang string
	err := ex.QueryRow(ctx, `
		SELECT lower(btrim(lang)) AS l
		FROM books
		WHERE deleted = false AND lang IS NOT NULL AND btrim(lang) <> ''
		GROUP BY l
		ORDER BY count(*) DESC, l
		LIMIT 1
	`).Scan(&lang)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return lang, err
}

// recomputeWorkTitles переписывает works.title/normalized_title на заголовок
// издания работы в языке domLang — ТОЛЬКО для работ, у которых такое издание
// ЕСТЬ; остальные не трогаются.
//
// Зачем: при слиянии «перевод + оригинал» каноникой могло стать иноязычное
// издание, и works.title оставался, например, английским («Another Fine Myth»),
// хотя в библиотеке книга известна по русскому переводу. Карточка (COALESCE(
// w.title, b.title)) и works-индекс брали этот английский заголовок → рассинхрон
// со списком (b.title представителя) и провал поиска по русскому названию.
//
// Какое из изданий в domLang: название, под которым вышло БОЛЬШЕ всего изданий
// (без различия «ё»/«е»), а не «новейшее с обложкой» — иначе работа из семи
// «Золотых телят» называлась «Собрание сочинений в 2 томах. Том 2. Золотой
// теленок» (прод 2026-09: 167 таких работ, #306). При равенстве — текущее
// название работы (не прыгает между проходами), затем самое частое написание,
// обложка, новейшее издание, id.
//
// ids == nil → все работы. Возвращает id работ, чьё название реально изменилось
// (для таргетного ресинка works-индекса). Идемпотентно (UPDATE ... IS DISTINCT).
func recomputeWorkTitles(ctx context.Context, ex interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}, domLang string, ids []int64) ([]int64, error) {
	if domLang == "" {
		return nil, nil
	}
	// ed: издания работы в domLang. Фильтр lang = domLang ⇒ работы без издания в
	// этом языке в pick не попадают и UPDATE их не трогает (сохраняют текущее
	// каноническое название — не ломаем работы, у которых перевода на язык
	// библиотеки просто нет). $2::bigint[] IS NULL (nil-slice от pgx) → без
	// фильтра по ids = все работы.
	const q = `
		WITH ed AS (
			SELECT b.id, b.work_id, b.title, b.normalized_title,
			       translate(b.normalized_title::text, 'ё', 'е') AS folded,
			       (b.cover_path IS NOT NULL AND b.cover_path <> '') AS has_cover,
			       b.edition_year,
			       count(*) OVER (PARTITION BY b.work_id, translate(b.normalized_title::text, 'ё', 'е')) AS n_folded,
			       count(*) OVER (PARTITION BY b.work_id, b.normalized_title::text) AS n_exact
			FROM books b
			WHERE b.deleted = false AND b.work_id IS NOT NULL
			  AND lower(btrim(b.lang)) = $1
			  AND ($2::bigint[] IS NULL OR b.work_id = ANY($2))
		), pick AS (
			SELECT DISTINCT ON (ed.work_id)
			       ed.work_id AS wid, ed.title AS title, ed.normalized_title AS ntitle
			FROM ed JOIN works cur ON cur.id = ed.work_id
			ORDER BY ed.work_id,
			         ed.n_folded DESC,
			         (ed.folded = translate(cur.normalized_title::text, 'ё', 'е')) DESC,
			         ed.n_exact DESC,
			         ed.has_cover DESC,
			         ed.edition_year DESC NULLS LAST,
			         ed.id
		)
		UPDATE works w
		SET title = pick.title, normalized_title = pick.ntitle, updated_at = now()
		FROM pick
		WHERE w.id = pick.wid
		  AND (w.title IS DISTINCT FROM pick.title
		       OR w.normalized_title IS DISTINCT FROM pick.ntitle)
		  -- Не перетираем ручной оверрайд названия (грабля №19, metadata/overrides.go).
		  AND NOT EXISTS (SELECT 1 FROM metadata_overrides o
		                  WHERE o.target_kind='work' AND o.target_id=w.id AND o.field='title')
		RETURNING w.id
	`
	return scanInt64s(ctx, ex, q, domLang, ids)
}

// LocalizeWorkTitles пересчитывает works.title у всех работ: работы с изданием
// на языке библиотеки — самое частое название таких изданий (recomputeWorkTitles),
// работы из одного издания на другом языке — название этого издания
// (syncSingletonWorkTitles). Возвращает id изменённых работ (для ресинка
// works-индекса) и язык библиотеки. Идемпотентно. Зовут разовый шаг старта и
// шаги после импорта: импорт переписывает название и авторов издания, но не
// работы — работа 59910 называлась «Big Money» при единственном издании «Дневники
// 1939-1945» Бунина (#285). Затронутые группировкой работы пересчитывает она сама
// (WorkGrouper.apply).
func LocalizeWorkTitles(ctx context.Context, pool *pgxpool.Pool) ([]int64, string, error) {
	dom, err := dominantLang(ctx, pool)
	if err != nil {
		return nil, "", err
	}
	var changed []int64
	if dom != "" {
		if changed, err = recomputeWorkTitles(ctx, pool, dom, nil); err != nil {
			return nil, dom, err
		}
	}
	singles, err := syncSingletonWorkTitles(ctx, pool)
	if err != nil {
		return nil, dom, err
	}
	return append(changed, singles...), dom, nil
}

// syncSingletonWorkTitles — у работы из одного живого издания название
// работы = название издания (кроме ручной правки названия, грабля №19).
func syncSingletonWorkTitles(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	return scanInt64s(ctx, pool, `
		WITH single AS (
			SELECT b.work_id AS wid, min(b.title) AS title, min(b.normalized_title::text) AS ntitle
			FROM books b
			WHERE b.deleted = false AND b.work_id IS NOT NULL
			GROUP BY b.work_id
			HAVING count(*) = 1
		)
		UPDATE works w
		SET title = single.title, normalized_title = single.ntitle, updated_at = now()
		FROM single
		WHERE w.id = single.wid
		  AND (w.title IS DISTINCT FROM single.title OR w.normalized_title::text IS DISTINCT FROM single.ntitle)
		  AND NOT EXISTS (SELECT 1 FROM metadata_overrides o
		                  WHERE o.target_kind = 'work' AND o.target_id = w.id AND o.field = 'title')
		RETURNING w.id`)
}
