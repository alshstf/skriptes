package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestAuthorFacets — счётчики фильтров /authors (#389): число авторов на
// значение; выбранный фильтр не сужает собственный фасет, остальные — сужают;
// служебные авторы не в счёт; пересборка пишет только разницу.
func TestAuthorFacets(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	f := seedAuthorsList(t, ctx, pool)
	// Категория «Фантастика» над sf и sf_horror.
	_, err := pool.Exec(ctx, `
		WITH cat AS (INSERT INTO genres (fb2_code, name_ru) VALUES ('cat:sf', 'Фантастика') RETURNING id)
		UPDATE genres SET parent_id = (SELECT id FROM cat) WHERE fb2_code IN ('sf', 'sf_horror')`)
	require.NoError(t, err)
	n, err := catalog.RecomputeAuthorFacets(ctx, pool)
	require.NoError(t, err)
	require.Positive(t, n)
	n, err = catalog.RecomputeAuthorFacets(ctx, pool)
	require.NoError(t, err)
	require.Zero(t, n, "без изменений — без записи")

	svc := catalog.New(pool)
	all, err := svc.AuthorFacets(ctx, catalog.AuthorListParams{})
	require.NoError(t, err)
	require.Equal(t, map[string]int{"sf_horror": 1, "sf": 2, "prose_classic": 1}, all.Genres,
		"Кинг и Азимов — фантастика, служебный автор не в счёт")
	require.Equal(t, map[string]int{"cat:sf": 2}, all.Categories, "Кинг в категории один раз, хоть и в двух жанрах")
	require.Equal(t, map[string]int{"en": 2, "ru": 2}, all.Langs)
	require.Equal(t, map[string]int{"en": 2, "ru": 1}, all.SrcLangs, "у Кинга оригинал работы — английский")
	require.Equal(t, 1, all.Adaptations)

	ru, err := svc.AuthorFacets(ctx, catalog.AuthorListParams{Langs: []string{"ru"}})
	require.NoError(t, err)
	require.Equal(t, map[string]int{"en": 2, "ru": 2}, ru.Langs, "выбранный язык не сужает выбор языка")
	require.Equal(t, map[string]int{"sf_horror": 1, "sf": 1, "prose_classic": 1}, ru.Genres, "среди авторов с русскими изданиями — Кинг и Толстой")

	sf, err := svc.AuthorFacets(ctx, catalog.AuthorListParams{Genres: []string{"sf"}, HasAdaptations: true})
	require.NoError(t, err)
	require.Equal(t, map[string]int{"sf_horror": 1, "sf": 1}, sf.Genres, "экранизации сужают жанры: остался Кинг")
	require.Equal(t, 1, sf.Adaptations, "экранизации без собственного фильтра — среди фантастов")
	require.Equal(t, map[string]int{"en": 1, "ru": 1}, sf.Langs)

	fav, err := svc.AuthorFacets(ctx, catalog.AuthorListParams{UserID: f.userID, FavoritesOnly: true})
	require.NoError(t, err)
	require.Equal(t, map[string]int{"sf_horror": 1, "sf": 1}, fav.Genres, "подписки пользователя — только Кинг")
}
