package metadata

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ── lookupTTL.isDue (pure) ──────────────────────────────────────

func TestLookupTTL_isDue(t *testing.T) {
	now := time.Now()
	day := 24 * time.Hour
	final := retryTTL(90, 24)
	require.Equal(t, lookupTTL{notFound: 90 * day, error: 24 * time.Hour}, final)

	require.True(t, final.isDue(lookupRow{}, now), "нет строки → спрашиваем")
	require.False(t, final.isDue(lookupRow{outcome: "found", checkedAt: now.Add(-1000 * day)}, now),
		"found окончательный при found=0")
	require.False(t, final.isDue(lookupRow{outcome: "not_found", checkedAt: now.Add(-10 * day)}, now))
	require.True(t, final.isDue(lookupRow{outcome: "not_found", checkedAt: now.Add(-100 * day)}, now))
	require.False(t, final.isDue(lookupRow{outcome: "error", checkedAt: now.Add(-time.Hour)}, now))
	require.True(t, final.isDue(lookupRow{outcome: "error", checkedAt: now.Add(-48 * time.Hour)}, now))
	require.True(t, final.isDue(lookupRow{outcome: "weird", checkedAt: now}, now), "незнакомый исход → спрашиваем")

	refresh := final
	refresh.found = 180 * day
	require.False(t, refresh.isDue(lookupRow{outcome: "found", checkedAt: now.Add(-30 * day)}, now))
	require.True(t, refresh.isDue(lookupRow{outcome: "found", checkedAt: now.Add(-200 * day)}, now))
}

// ── dueCond ≡ isDue (testcontainers PG) ─────────────────────────

// TestDueCond_MatchesIsDue — SQL-предикат выборки кандидатов обязан давать ровно
// то же, что Go-правило isDue: книга «пора», если хотя бы один источник из
// списка пора спросить. Перебираем все пары состояний двух источников, разные
// сроки и разные наборы включённых источников.
func TestDueCond_MatchesIsDue(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPGForPrewarm(t, ctx)

	var collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))

	// Состояние строки учёта: исход + давность. Сроки в тесте далеко от границ
	// (90 дней / 24 часа / 180 дней), чтобы часы Go и БД не спорили.
	type state struct {
		outcome string // "" — строки нет
		age     time.Duration
	}
	day := 24 * time.Hour
	states := []state{
		{},
		{"found", day}, {"found", 200 * day},
		{"not_found", 10 * day}, {"not_found", 100 * day},
		{"error", time.Hour}, {"error", 48 * time.Hour},
		{"weird", time.Hour},
	}

	rows := map[int64]map[string]lookupRow{} // книга → источник → строка
	now := time.Now()
	i := 0
	for _, sa := range states {
		for _, sb := range states {
			i++
			var id int64
			require.NoError(t, pool.QueryRow(ctx, `
				INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title)
				VALUES ($1,$2,$3,'f','fb2','t','t') RETURNING id`,
				collID, archID, fmt.Sprintf("L%d", i)).Scan(&id))
			rows[id] = map[string]lookupRow{}
			for src, st := range map[string]state{"a": sa, "b": sb} {
				if st.outcome == "" {
					continue
				}
				_, err := pool.Exec(ctx, `
					INSERT INTO book_cover_lookups (book_id, source, outcome, checked_at)
					VALUES ($1, $2, $3, now() - $4::interval)`, id, src, st.outcome, st.age)
				require.NoError(t, err)
				rows[id][src] = lookupRow{outcome: st.outcome, checkedAt: now.Add(-st.age)}
			}
		}
	}

	ttls := map[string]lookupTTL{
		"found окончательный": retryTTL(90, 24),
		"found освежается":    {found: 180 * day, notFound: 90 * day, error: 24 * time.Hour},
	}
	sourceSets := [][]string{nil, {"a"}, {"b"}, {"a", "b"}, {"c"}}

	q := `SELECT b.id FROM books b WHERE ` + dueCond("book_cover_lookups", "book_id", "b.id", 1) + ` ORDER BY b.id`
	for name, ttl := range ttls {
		for _, sources := range sourceSets {
			var want []int64
			for id, bySrc := range rows {
				for _, src := range sources {
					if ttl.isDue(bySrc[src], now) {
						want = append(want, id)
						break
					}
				}
			}
			sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })

			res, err := pool.Query(ctx, q, dueArgs(sources, ttl)...)
			require.NoError(t, err)
			var got []int64
			for res.Next() {
				var id int64
				require.NoError(t, res.Scan(&id))
				got = append(got, id)
			}
			require.NoError(t, res.Err())
			res.Close()
			require.Equal(t, want, got, "ttl=%s sources=%v", name, sources)
		}
	}
}
