package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestContemporaries — современники (#505): пересечение взрослых жизней ≥ 5
// лет, самые известные первыми; без года смерти жив только родившийся с 1900;
// служебные, скрытые и без известности не берутся.
func TestContemporaries(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	svc := catalog.New(pool)

	mk := func(last string, born int, died *int, renown int, extra string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO authors (last_name, normalized_name, born_year, died_year, renown)
			VALUES ($1, lower($1), $2, $3, $4) RETURNING id`, last, born, died, renown).Scan(&id))
		if extra != "" {
			_, err := pool.Exec(ctx, `UPDATE authors SET `+extra+` WHERE id = $1`, id)
			require.NoError(t, err)
		}
		return id
	}
	y := func(v int) *int { return &v }
	pushkin := mk("Пушкин", 1799, y(1837), 1800, "")
	gogol := mk("Гоголь", 1809, y(1852), 1900, "")
	goethe := mk("Гёте", 1749, y(1832), 1600, "")
	lermontov := mk("Лермонтов", 1814, y(1841), 1500, "") // общих взрослых лет — 8
	mk("Достоевский", 1821, y(1881), 1700, "")            // в 1837 ему 16 — не современник
	mk("Безвестный", 1800, y(1850), 0, "")                // без известности
	mk("Служебный", 1800, y(1850), 900, "is_service = true")
	mk("Скрытый", 1800, y(1850), 900, "instance_hidden = true")
	mk("Неизвестно когда умер", 1790, nil, 950, "") // до 1900 без года смерти — не «жив»

	got, err := svc.Contemporaries(ctx, pushkin)
	require.NoError(t, err)
	var ids []int64
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	require.Equal(t, []int64{gogol, goethe, lermontov}, ids)
	require.Equal(t, 1809, got[0].BornYear)
	require.Equal(t, 1852, *got[0].DiedYear)

	// Живой автор: без года смерти — по сей день.
	living := mk("Живой", 1950, nil, 500, "")
	peer := mk("Ровесник", 1945, nil, 400, "") // тоже жив
	got, err = svc.Contemporaries(ctx, living)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, peer, got[0].ID)
	require.Nil(t, got[0].DiedYear)

	// У автора нет годов жизни — пусто.
	unknown := mk("Без дат", 0, nil, 100, "born_year = NULL")
	got, err = svc.Contemporaries(ctx, unknown)
	require.NoError(t, err)
	require.Empty(t, got)
}
