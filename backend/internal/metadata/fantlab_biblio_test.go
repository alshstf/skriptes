package metadata

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Форма ответа /autor/{id}?biblio_blocks=1 (поля сняты с живого API 2026-10-17;
// названия и годы — вымышленные).
const testBiblioJSON = `{
  "autor_id": 186,
  "cycles_blocks": {"1": {"list": [
    {"work_id": 10, "work_name": "Записки", "work_type_id": 4, "work_year": 1922,
     "children": [
       {"work_id": 11, "work_name": "Красная корона. (Historia morbi)", "work_type_id": 45, "work_year": 1922},
       {"work_id": 12, "work_name": "Богема", "work_type_id": 45, "work_year": null, "work_year_of_write": 1924}
     ]},
    {"work_id": 11, "work_name": "Красная корона. (Historia morbi)", "work_type_id": 45, "work_year": 1922},
    {"work_id": 13, "work_name": "Самовар", "work_name_orig": "The Samovar", "work_name_alt": "Сказка про самовар; Самоварчик",
     "work_type_id": 8, "work_year": "1918"},
    {"work_id": 14, "work_name": "Двойник", "work_type_id": 45, "work_year": 1930},
    {"work_id": 15, "work_name": "Двойник", "work_type_id": 1, "work_year": 1950},
    {"work_id": 16, "work_name": "Избранное", "work_type_id": 3, "work_year": 1960},
    {"work_id": 17, "work_name": "Без года", "work_type_id": 1, "work_year": null},
    {"work_id": 18, "work_name": "", "work_name_orig": "Detective", "work_name_alt": "Сыщик", "work_type_id": 18, "work_year": 1922, "work_year_of_write": 1899},
    {"work_id": 19, "work_name": "Сыщик", "work_type_id": 4, "work_year": 1887},
    {"work_id": 20, "work_name": "Дорога", "work_type_id": 1, "work_year": 1965},
    {"work_id": 21, "work_name": "Дорога", "work_type_id": 4, "work_year": 1965},
    {"work_id": 22, "work_name": "Тень", "work_name_orig": "Shadow", "work_type_id": 45, "work_year": null}
  ]},
  "z_block": {"list": [
    {"work_id": 22, "work_name": "Тень", "work_name_alt": "Тень на стене", "work_type_id": 45, "work_year": 1931}
  ]}}
}`

func TestParseFantlabBiblio(t *testing.T) {
	ws, err := parseFantlabBiblio(strings.NewReader(testBiblioJSON))
	require.NoError(t, err)
	byID := map[int64]fantlabBiblioWork{}
	for _, w := range ws {
		byID[w.WorkID] = w
	}
	require.Len(t, byID, 13, "повтор work_id 11 — один раз")
	require.Equal(t, []string{"Тень", "Shadow", "Тень на стене"}, byID[22].Names, "вхождения одного work_id сливаются")
	require.Equal(t, 1931, byID[22].Year, "год — из того вхождения, где он есть")
	// Порядок и итог не зависят от обхода map: разбор стабилен.
	for range 20 {
		again, err := parseFantlabBiblio(strings.NewReader(testBiblioJSON))
		require.NoError(t, err)
		require.Equal(t, ws, again)
	}
	require.Equal(t, 1924, byID[12].Year, "год написания, когда нет года публикации")
	require.Equal(t, 1918, byID[13].Year, "год строкой")
	require.Equal(t, []string{"Самовар", "The Samovar", "Сказка про самовар", "Самоварчик"}, byID[13].Names)
	require.Zero(t, byID[17].Year)
}

func TestMatchFantlabBiblio(t *testing.T) {
	ws, err := parseFantlabBiblio(strings.NewReader(testBiblioJSON))
	require.NoError(t, err)
	got := matchFantlabBiblio([]FantlabWorkQuery{
		{ID: 1, Title: "Красная корона"},                                      // без скобок
		{ID: 2, Title: "«Самоварчик»"},                                        // альтернативное, кавычки
		{ID: 3, Title: "Самовар-перевод", SrcTitles: []string{"The Samovar"}}, // по оригиналу
		{ID: 4, Title: "Двойник"},                                             // два произведения, разные годы — не решаем
		{ID: 5, Title: "Избранное"},                                           // сборник — не сопоставляем
		{ID: 6, Title: "Записки"},                                             // цикл — не сопоставляем
		{ID: 7, Title: "Без года"},                                            // найдено, года нет
		{ID: 8, Title: "Чужая книга"},
		{ID: 9, Title: "Сыщик (Том 1)"}, // том цикла, не пьеса с альт. названием
		{ID: 10, Title: "Дорога"},       // роман и цикл одного года — роман
	}, ws)
	require.Equal(t, []FantlabYearMatch{
		{WorkID: 1, Year: 1922, FlWorkID: 11},
		{WorkID: 2, Year: 1918, FlWorkID: 13},
		{WorkID: 3, Year: 1918, FlWorkID: 13},
		{WorkID: 7, Year: 0, FlWorkID: 17},
		{WorkID: 10, Year: 1965, FlWorkID: 20},
	}, got)
}

func TestMatchFantlabKinds(t *testing.T) {
	ws, err := parseFantlabBiblio(strings.NewReader(testBiblioJSON))
	require.NoError(t, err)
	got := matchFantlabKinds([]FantlabWorkQuery{
		{ID: 1, Title: "Записки"},             // только цикл → том собрания
		{ID: 2, Title: "Избранное (сборник)"}, // только сборник
		{ID: 3, Title: "Дорога"},              // роман с названием цикла — роман
		{ID: 4, Title: "Сыщик (Том 1)"},       // цикл и пьеса (альт. название) — не решаем
		{ID: 5, Title: "Красная корона"},      // произведение
		{ID: 6, Title: "Чужая книга"},
	}, ws)
	require.Equal(t, []FantlabKindMatch{{WorkID: 1, Kind: "omnibus"}, {WorkID: 2, Kind: "collection"}}, got)
}

func TestFantlabAuthorYears(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/autor/186":
			_, _ = w.Write([]byte(testBiblioJSON))
		case r.URL.Path == "/autor/999":
			http.NotFound(w, r) // закрытая страница
		case r.URL.Path == "/search-autors" && strings.Contains(r.URL.Query().Get("q"), "Булгаков"):
			_, _ = w.Write([]byte(`{"matches":[{"autor_id":999,"rusname":"Михаил Булгаков-тёзка"},{"autor_id":186,"rusname":"Михаил Булгаков"}]}`))
		case r.URL.Path == "/search-autors":
			_, _ = w.Write([]byte(`{"matches":[{"autor_id":186,"rusname":"Мария Семёнова"}]}`))
		default:
			http.Error(w, "boom", http.StatusBadGateway)
		}
	}))
	defer srv.Close()
	p := NewFantlabProvider(srv.Client()).WithAPIBase(srv.URL)
	ctx := context.Background()
	works := []FantlabWorkQuery{{ID: 1, Title: "Красная корона"}, {ID: 2, Title: "Самовар"}, {ID: 3, Title: "Богема"}}
	waits := 0
	wait := func(context.Context) error { waits++; return nil }

	// По FantLab ID из Wikidata — сразу библиография, без гейта.
	res, err := p.AuthorYears(ctx, FantlabAuthorQuery{FantlabID: 186}, works[:1], wait)
	require.NoError(t, err)
	require.Equal(t, "wikidata", res.Via)
	require.Len(t, res.Matched, 1)
	require.Equal(t, 1, waits)

	// Поиском: первый кандидат — закрытая страница (404), второй проходит гейт.
	res, err = p.AuthorYears(ctx, FantlabAuthorQuery{LastName: "Булгаков", FirstName: "Михаил"}, works, wait)
	require.NoError(t, err)
	require.Equal(t, FantlabAuthorYears{FantlabID: 186, Via: "search", Matched: []FantlabYearMatch{
		{WorkID: 1, Year: 1922, FlWorkID: 11}, {WorkID: 2, Year: 1918, FlWorkID: 13}, {WorkID: 3, Year: 1924, FlWorkID: 12},
	}}, res)

	// Чужой автор в выдаче поиска (фамилия не совпала) — не наш.
	_, err = p.AuthorYears(ctx, FantlabAuthorQuery{LastName: "Незнанский", FirstName: "Фридрих"}, works, wait)
	require.ErrorIs(t, err, ErrNotFound)
	// Гейт: меньше трёх наших работ нашлось — не принимаем.
	_, err = p.AuthorYears(ctx, FantlabAuthorQuery{LastName: "Булгаков", FirstName: "Михаил"},
		[]FantlabWorkQuery{{ID: 1, Title: "Красная корона"}, {ID: 9, Title: "Чужое"}, {ID: 10, Title: "Ещё чужое"}}, wait)
	require.ErrorIs(t, err, ErrNotFound)
	// Закрытая страница по P7433 — not found; сбой источника — upstream.
	_, err = p.AuthorYears(ctx, FantlabAuthorQuery{FantlabID: 999}, works, wait)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = p.AuthorYears(ctx, FantlabAuthorQuery{FantlabID: 5}, works, wait)
	require.True(t, errors.Is(err, ErrUpstream), "502 — сбой, повторить позже")
}
