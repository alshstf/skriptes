package genres

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// aliases.json — коды жанров, которые означают жанр нашего словаря (код в
// нижнем регистре → наш код), #286:
//   - алиасы из genres.json клиента FLibrary 2.7.0 (тот же жанр под другим
//     кодом: adv_all = adventure, painting = visual_arts, popadancy = popadanec);
//   - «…: прочее» FLibrary, у которых наш общий код уже так называется
//     (humor_all → humor «Юмор: прочее», det_all → detective);
//   - коды-слова и дефисные коды litnet в выпусках librusec 2026 («Биографии и
//     мемуары» → nonf_biography, urban-fantasy → sf_fantasy_city, litrpg →
//     sf_litrpg) — решение владельца 2026-09-28: туда, где соответствие
//     очевидно; wuxia, action, rusreal и подобные не сводим.
//
// Импорт сводит код при записи (CanonicalCodes), существующие данные переносит
// MergeAliases.
//
//go:embed aliases.json
var rawAliases []byte

var aliases = func() map[string]string {
	m := map[string]string{}
	if err := json.Unmarshal(rawAliases, &m); err != nil {
		panic(fmt.Sprintf("genres: parse aliases.json: %v", err))
	}
	return m
}()

// Canonical — код жанра нашего словаря для code (сам code, если это не алиас).
func Canonical(code string) string {
	if c, ok := aliases[strings.ToLower(strings.TrimSpace(code))]; ok {
		return c
	}
	return code
}

// CanonicalCodes — Canonical для каждого кода, без повторов, порядок сохранён.
func CanonicalCodes(codes []string) []string {
	out := make([]string, 0, len(codes))
	seen := make(map[string]bool, len(codes))
	for _, c := range codes {
		c = Canonical(c)
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// MergeAliases переносит данные с жанров-алиасов на канонические и удаляет
// строки алиасов: книги, избранное пользователей, скрытые жанры (общие и
// пользовательские настройки content), ручные правки жанров. Каждый алиас — в
// своей транзакции. Идемпотентно. Возвращает работы, у которых сменились жанры
// (для ресинка works-индекса), и число слитых кодов. Канонические строки должен
// уже создать Seed.
func MergeAliases(ctx context.Context, pool *pgxpool.Pool) ([]int64, int, error) {
	rows, err := pool.Query(ctx, `SELECT id, fb2_code FROM genres`)
	if err != nil {
		return nil, 0, fmt.Errorf("list genres: %w", err)
	}
	type genre struct {
		id   int64
		code string
	}
	all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (genre, error) {
		var g genre
		err := r.Scan(&g.id, &g.code)
		return g, err
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list genres: %w", err)
	}
	idByCode := make(map[string]int64, len(all))
	for _, g := range all {
		idByCode[g.code] = g.id
	}
	works := map[int64]struct{}{}
	merged := 0
	for _, g := range all {
		canon := Canonical(g.code)
		if canon == g.code {
			continue
		}
		canonID, ok := idByCode[canon]
		if !ok {
			return nil, merged, fmt.Errorf("canonical genre %q for %q is not seeded", canon, g.code)
		}
		ids, err := mergeAlias(ctx, pool, g.id, g.code, canonID, canon)
		if err != nil {
			return nil, merged, fmt.Errorf("merge genre %q into %q: %w", g.code, canon, err)
		}
		for _, id := range ids {
			works[id] = struct{}{}
		}
		merged++
	}
	out := make([]int64, 0, len(works))
	for id := range works {
		out = append(out, id)
	}
	return out, merged, nil
}

func mergeAlias(ctx context.Context, pool *pgxpool.Pool, aliasID int64, alias string, canonID int64, canon string) ([]int64, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT DISTINCT b.work_id FROM book_genres bg JOIN books b ON b.id = bg.book_id
		WHERE bg.genre_id = $1 AND b.work_id IS NOT NULL`, aliasID)
	if err != nil {
		return nil, err
	}
	works, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, err
	}
	for _, q := range []string{
		`INSERT INTO book_genres (book_id, genre_id) SELECT book_id, $2 FROM book_genres WHERE genre_id = $1 ON CONFLICT DO NOTHING`,
		`INSERT INTO user_favorite_genres (user_id, genre_id, added_at)
		 SELECT user_id, $2, added_at FROM user_favorite_genres WHERE genre_id = $1 ON CONFLICT DO NOTHING`,
	} {
		if _, err := tx.Exec(ctx, q, aliasID, canonID); err != nil {
			return nil, err
		}
	}
	// Коды в JSON: скрытые жанры (content) и ручные правки жанров.
	for _, t := range []struct{ sel, upd string }{
		{`SELECT key, value FROM app_settings WHERE key = 'content'`, `UPDATE app_settings SET value = $2 WHERE key = $1`},
		{`SELECT user_id::text, value FROM user_settings WHERE key = 'content'`, `UPDATE user_settings SET value = $2 WHERE user_id = $1::bigint AND key = 'content'`},
		{`SELECT target_kind || ':' || target_id, override_value FROM metadata_overrides WHERE field = 'genres'`,
			`UPDATE metadata_overrides SET override_value = $2 WHERE field = 'genres' AND target_kind || ':' || target_id = $1`},
		{`SELECT target_kind || ':' || target_id, original_value FROM metadata_overrides WHERE field = 'genres'`,
			`UPDATE metadata_overrides SET original_value = $2 WHERE field = 'genres' AND target_kind || ':' || target_id = $1`},
	} {
		if err := replaceCodeInJSON(ctx, tx, t.sel, t.upd, alias, canon); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM genres WHERE id = $1`, aliasID); err != nil {
		return nil, err
	}
	return works, tx.Commit(ctx)
}

// replaceCodeInJSON заменяет строку alias на canon во всех значениях JSON
// выборки sel (ключ строки, значение) и пишет изменённые через upd.
func replaceCodeInJSON(ctx context.Context, tx pgx.Tx, sel, upd, alias, canon string) error {
	rows, err := tx.Query(ctx, sel)
	if err != nil {
		return err
	}
	type row struct {
		key   string
		value []byte
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		err := r.Scan(&x.key, &x.value)
		return x, err
	})
	if err != nil {
		return err
	}
	for _, x := range list {
		var v any
		if err := json.Unmarshal(x.value, &v); err != nil {
			continue // не наш формат — не трогаем
		}
		nv, changed := replaceString(v, alias, canon)
		if !changed {
			continue
		}
		raw, err := json.Marshal(dedupStrings(nv))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, upd, x.key, raw); err != nil {
			return err
		}
	}
	return nil
}

func replaceString(v any, from, to string) (any, bool) {
	switch t := v.(type) {
	case string:
		if t == from {
			return to, true
		}
	case []any:
		changed := false
		for i := range t {
			var c bool
			t[i], c = replaceString(t[i], from, to)
			changed = changed || c
		}
		return t, changed
	case map[string]any:
		changed := false
		for k := range t {
			var c bool
			t[k], c = replaceString(t[k], from, to)
			changed = changed || c
		}
		return t, changed
	}
	return v, false
}

// dedupStrings убирает повторы строк в массивах (алиас и канонический код могли
// стоять в одном списке).
func dedupStrings(v any) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, 0, len(t))
		seen := map[string]bool{}
		for _, e := range t {
			if s, ok := e.(string); ok {
				if seen[s] {
					continue
				}
				seen[s] = true
			}
			out = append(out, dedupStrings(e))
		}
		return out
	case map[string]any:
		for k := range t {
			t[k] = dedupStrings(t[k])
		}
	}
	return v
}
