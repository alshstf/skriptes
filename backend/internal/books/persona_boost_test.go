package books

import (
	"testing"

	"github.com/skriptes/skriptes/backend/internal/history"
	"github.com/stretchr/testify/require"
)

func emptyPersona() history.PersonaProfile {
	return history.PersonaProfile{
		FavoriteAuthors: map[int64]struct{}{},
		FavoriteSeries:  map[int64]struct{}{},
		FavoriteBooks:   map[int64]struct{}{},
		BookActivity:    map[int64]float64{},
		FavoriteWorks:   map[int64]struct{}{},
		WorkActivity:    map[int64]float64{},
		AuthorActivity:  map[int64]float64{},
		SeriesActivity:  map[int64]float64{},
		GenreActivity:   map[string]float64{},
	}
}

// #399, прод 2026-10-06: подсказка «кармен». «Кто такая Кармен Сандиего?» — 7 жанров,
// по трём из них у пользователя много чтения; «Кармен» Мериме — точное совпадение
// названия и известность 974. Сумма жанровых пределов (+0.74) ставила «Сандиего» первой.
func TestApplyPersonaBoost_GenresCountOnce(t *testing.T) {
	p := emptyPersona()
	for code, w := range map[string]float64{
		"sf": 47, "sf_humor": 29, "child_sf": 27, "child_adv": 6, "sf_detective": 1,
		"prose_classic": 8, "love": 2, "love_history": 1,
	} {
		p.GenreActivity[code] = w
	}
	scored := []scoredItem{
		{item: ListItem{ID: 511485, Genres: []string{"child_adv", "child_det", "child_sf", "children", "sf", "sf_detective", "sf_humor"}}, base: 0.984},
		{item: ListItem{ID: 126725, Genres: []string{"love", "love_history", "prose_classic", "story"}}, base: 1.0, pop: popularityBoost(974)},
	}
	applyPersonaBoost(scored, p, true)
	require.InDelta(t, genreActivityCap, scored[0].personal, 1e-9, "семь жанров — один жанровый бонус")
	require.LessOrEqual(t, scored[1].personal, genreActivityCap)

	sortByFinalScore(scored)
	require.EqualValues(t, 126725, scored[0].item.ID, "точное совпадение известной книги не перебивается жанрами")
}

// Жанр — тай-брейкер: среди близких матчей поднимает книгу любимого жанра, но
// слабее известности классики.
func TestApplyPersonaBoost_GenreIsTieBreaker(t *testing.T) {
	p := emptyPersona()
	p.GenreActivity["sf"] = 50
	scored := []scoredItem{
		{item: ListItem{ID: 1, Genres: []string{"detective"}}, base: 0.95},
		{item: ListItem{ID: 2, Genres: []string{"sf"}}, base: 0.94},
	}
	applyPersonaBoost(scored, p, true)
	sortByFinalScore(scored)
	require.EqualValues(t, 2, scored[0].item.ID, "близкий матч любимого жанра выше")
	require.Less(t, genreActivityCap, popularityBoost(1000), "жанр слабее известности классики")
}

// Соавторы не складываются: антология с двумя подписанными авторами получает один
// бонус подписки и активность самого активного автора.
func TestApplyPersonaBoost_AuthorsCountOnce(t *testing.T) {
	p := emptyPersona()
	p.FavoriteAuthors[10] = struct{}{}
	p.FavoriteAuthors[11] = struct{}{}
	p.AuthorActivity[10] = 100
	p.AuthorActivity[11] = 4
	scored := []scoredItem{{item: ListItem{ID: 1, AuthorIDs: []int64{10, 11, 12}}}}
	applyPersonaBoost(scored, p, true)
	require.InDelta(t, bonusFavoriteAuthor+authorActivityCap, scored[0].personal, 1e-9)
}

// В индексе works id хита — id работы; id работ и изданий пересекаются (на проде у
// 67 из 72 открытых владельцем изданий номер совпал с чужой работой). Книжные
// сигналы берутся по работе, а для индекса изданий — по изданию.
func TestApplyPersonaBoost_WorkVsEditionIDs(t *testing.T) {
	p := emptyPersona()
	p.BookActivity[5] = 3 // открыто издание 5
	p.FavoriteBooks[5] = struct{}{}
	p.WorkActivity[9] = 3 // издание 5 принадлежит работе 9
	p.FavoriteWorks[9] = struct{}{}

	works := []scoredItem{{item: ListItem{ID: 5}}, {item: ListItem{ID: 9}}}
	applyPersonaBoost(works, p, true)
	require.Zero(t, works[0].personal, "работа 5 — чужая: её номер просто совпал с изданием")
	require.InDelta(t, bonusFavoriteBook+3*bookActivityScale, works[1].personal, 1e-9)

	editions := []scoredItem{{item: ListItem{ID: 5}}, {item: ListItem{ID: 9}}}
	applyPersonaBoost(editions, p, false)
	require.InDelta(t, bonusFavoriteBook+3*bookActivityScale, editions[0].personal, 1e-9)
	require.Zero(t, editions[1].personal)
}
