package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Счётчики фильтров /authors (#389, A3): сколько авторов даст каждое значение
// фильтра при остальных выбранных фильтрах. Считаются по хранимой таблице
// author_facets (миграция 0052): подсчёт прямо по книгам шёл 2 с без фильтров и
// до 34 с с фильтром (прод 2026-10-07), по таблице — один хэш-джойн.
//
// Таблица — без личных скрытий (как хранимые book_count/rating_score): автор
// учтён в жанре, даже если его книги этого жанра у пользователя скрыты; сам
// список авторов скрытия соблюдает.

// AuthorFacetCounts — счётчики значений фильтров (число авторов).
type AuthorFacetCounts struct {
	Genres      map[string]int `json:"genres"`
	Categories  map[string]int `json:"genre_categories"` // по коду категории (cat:sf)
	Langs       map[string]int `json:"langs"`
	SrcLangs    map[string]int `json:"src_langs"`
	Adaptations int            `json:"adaptations"`
}

// facetKinds — какие строки author_facets относятся к фасету фильтра.
var facetKinds = map[string][]string{
	FacetGenre:       {"genre", "gcat"},
	FacetLang:        {"lang"},
	FacetSrcLang:     {"src"},
	FacetAdaptations: {"adapt"},
}

// facetActive — выбран ли фильтр фасета.
func facetActive(p AuthorListParams, facet string) bool {
	switch facet {
	case FacetGenre:
		return len(p.Genres) > 0
	case FacetLang:
		return len(p.Langs) > 0
	case FacetSrcLang:
		return len(p.SrcLangs) > 0
	case FacetAdaptations:
		return p.HasAdaptations
	}
	return false
}

// AuthorFacets — счётчики фильтров для текущих параметров списка. Фасет с
// выбранным фильтром считается без собственного фильтра (выбор жанра не
// обнуляет соседние жанры), остальные — одним запросом со всеми фильтрами.
func (s *Service) AuthorFacets(ctx context.Context, p AuthorListParams) (AuthorFacetCounts, error) {
	key := facetCacheKey(p)
	if c, ok := authorFacetCache.get(key); ok {
		return c, nil
	}
	out := AuthorFacetCounts{
		Genres: map[string]int{}, Categories: map[string]int{}, Langs: map[string]int{}, SrcLangs: map[string]int{},
	}
	var shared []string
	for _, f := range []string{FacetGenre, FacetLang, FacetSrcLang, FacetAdaptations} {
		if facetActive(p, f) {
			if err := s.countFacets(ctx, p, f, facetKinds[f], &out); err != nil {
				return AuthorFacetCounts{}, err
			}
		} else {
			shared = append(shared, facetKinds[f]...)
		}
	}
	if len(shared) > 0 {
		if err := s.countFacets(ctx, p, "", shared, &out); err != nil {
			return AuthorFacetCounts{}, err
		}
	}
	authorFacetCache.put(key, out)
	return out, nil
}

// countFacets — счётчики значений kinds у авторов, прошедших фильтры (кроме skip).
func (s *Service) countFacets(ctx context.Context, p AuthorListParams, skip string, kinds []string, out *AuthorFacetCounts) error {
	wb := newAuthorWhere(p)
	where := wb.filters(skip)
	kindsN := wb.add(kinds)
	rows, err := s.pool.Query(ctx, `
		WITH fa AS MATERIALIZED (SELECT a.id FROM authors a WHERE `+strings.Join(where, " AND ")+`)
		SELECT f.kind, f.value, count(*)::int
		FROM author_facets f JOIN fa ON fa.id = f.author_id
		WHERE f.kind = ANY($`+fmt.Sprint(kindsN)+`::text[])
		GROUP BY f.kind, f.value`, wb.args...)
	if err != nil {
		return fmt.Errorf("author facets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			kind, value string
			n           int
		)
		if err := rows.Scan(&kind, &value, &n); err != nil {
			return err
		}
		switch kind {
		case "genre":
			out.Genres[value] = n
		case "gcat":
			out.Categories[value] = n
		case "lang":
			out.Langs[value] = n
		case "src":
			out.SrcLangs[value] = n
		case "adapt":
			out.Adaptations = n
		}
	}
	return rows.Err()
}

// RecomputeAuthorFacets пересобирает author_facets (пишет только разницу) и
// возвращает число изменённых строк. ~7 с на проде (800 тыс. строк) — зовут на
// старте, после импорта и раз в несколько часов, а не с каждым пересчётом агрегатов.
func RecomputeAuthorFacets(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("author facets: acquire conn: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, authorFacetsLockID); err != nil {
		return 0, fmt.Errorf("author facets: advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, authorFacetsLockID)
	}()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE author_facets_new (author_id BIGINT, kind TEXT, value TEXT) ON COMMIT DROP`); err != nil {
		return 0, fmt.Errorf("author facets: temp table: %w", err)
	}
	// Те же книги, что у фильтров списка: живые, не сборники (aggExclusion);
	// язык оригинала — уровня работы, как фильтр «Язык оригинала»: src_lang
	// изданий работы, а если его нет ни у одного — язык издания.
	if _, err := tx.Exec(ctx, `
		INSERT INTO author_facets_new (author_id, kind, value)
		WITH live AS (
		    SELECT ba.author_id, b.id AS book_id, b.work_id, lower(btrim(b.lang)) AS lang
		    FROM book_authors ba
		    JOIN authors a ON a.id = ba.author_id AND NOT a.is_service
		    JOIN books b ON b.id = ba.book_id AND NOT b.deleted
		    LEFT JOIN works w ON w.id = b.work_id
		    WHERE COALESCE(w.kind, '') = ''
		),
		wsrc AS (
		    SELECT DISTINCT b.work_id, lower(btrim(b.src_lang)) AS l
		    FROM books b
		    WHERE NOT b.deleted AND b.work_id IS NOT NULL AND b.src_lang IS NOT NULL AND btrim(b.src_lang) <> ''
		)
		SELECT l.author_id, 'genre', g.fb2_code FROM live l
		    JOIN book_genres bg ON bg.book_id = l.book_id JOIN genres g ON g.id = bg.genre_id
		UNION
		SELECT l.author_id, 'gcat', pg.fb2_code FROM live l
		    JOIN book_genres bg ON bg.book_id = l.book_id JOIN genres g ON g.id = bg.genre_id
		    JOIN genres pg ON pg.id = g.parent_id
		UNION
		SELECT author_id, 'lang', lang FROM live WHERE lang IS NOT NULL AND lang <> ''
		UNION
		SELECT l.author_id, 'src', ws.l FROM live l JOIN wsrc ws ON ws.work_id = l.work_id
		UNION
		SELECT l.author_id, 'src', l.lang FROM live l
		    WHERE l.lang IS NOT NULL AND l.lang <> '' AND NOT EXISTS (SELECT 1 FROM wsrc ws WHERE ws.work_id = l.work_id)
		UNION
		SELECT l.author_id, 'adapt', '' FROM live l
		    WHERE EXISTS (SELECT 1 FROM book_adaptations ad WHERE ad.book_id = l.book_id)`); err != nil {
		return 0, fmt.Errorf("author facets: collect: %w", err)
	}
	del, err := tx.Exec(ctx, `
		DELETE FROM author_facets f
		WHERE NOT EXISTS (SELECT 1 FROM author_facets_new n
		                  WHERE n.author_id = f.author_id AND n.kind = f.kind AND n.value = f.value)`)
	if err != nil {
		return 0, fmt.Errorf("author facets: delete: %w", err)
	}
	ins, err := tx.Exec(ctx, `
		INSERT INTO author_facets (author_id, kind, value)
		SELECT author_id, kind, value FROM author_facets_new
		ON CONFLICT DO NOTHING`)
	if err != nil {
		return 0, fmt.Errorf("author facets: insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	n := del.RowsAffected() + ins.RowsAffected()
	if n > 0 {
		authorFacetCache.clear()
	}
	return n, nil
}

// authorFacetsLockID — свой ключ pg_advisory_lock (не authorsBulkLockID:
// author_facets — отдельная таблица, строки authors не трогаем).
const authorFacetsLockID = 0x61757468666163

// ── кэш счётчиков ──────────────────────────────────────────────

// authorFacetCacheTTL — сколько держать счётчики одного набора фильтров: таблица
// меняется только пересчётом (он же чистит кэш), а самое частое — список без
// фильтров у всех пользователей.
const authorFacetCacheTTL = 10 * time.Minute

// authorFacetCacheMax — потолок записей; при переполнении кэш сбрасывается.
const authorFacetCacheMax = 512

type facetCacheEntry struct {
	at time.Time
	v  AuthorFacetCounts
}

type facetCache struct {
	mu sync.Mutex
	m  map[string]facetCacheEntry
}

var authorFacetCache = &facetCache{m: map[string]facetCacheEntry{}}

func (c *facetCache) get(key string) (AuthorFacetCounts, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || time.Since(e.at) > authorFacetCacheTTL {
		return AuthorFacetCounts{}, false
	}
	return e.v, true
}

func (c *facetCache) put(key string, v AuthorFacetCounts) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= authorFacetCacheMax {
		c.m = map[string]facetCacheEntry{}
	}
	c.m[key] = facetCacheEntry{at: time.Now(), v: v}
}

func (c *facetCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m = map[string]facetCacheEntry{}
}

// facetCacheKey — всё, что влияет на счётчики: фильтры и скрытия (через базовую
// видимость автора); пользователь — только для «Мои подписки».
func facetCacheKey(p AuthorListParams) string {
	k := p
	k.Sort, k.Limit, k.Offset = "", 0, 0
	if !k.FavoritesOnly {
		k.UserID = 0
	}
	b, _ := json.Marshal(k)
	return string(b)
}
