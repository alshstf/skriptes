package metadata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// srcLangMockServer — httptest-мок Wikidata: wbsearchentities отдаёт один QID,
// SPARQL различает валидацию по автору (P50 → authorLabel) и запрос P407
// (→ code). codes подставляются в ответ P407-запроса.
func srcLangMockServer(t *testing.T, authorLabel string, codes []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/w/api.php"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"search": []map[string]string{{"id": "Q42"}},
			})
		case strings.HasSuffix(r.URL.Path, "/sparql"):
			_ = r.ParseForm()
			q := r.Form.Get("query")
			w.Header().Set("Content-Type", "application/sparql-results+json")
			if strings.Contains(q, "P407") {
				bindings := make([]map[string]map[string]string, 0, len(codes))
				for _, c := range codes {
					bindings = append(bindings, map[string]map[string]string{"code": {"value": c}})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"results": map[string]any{"bindings": bindings},
				})
				return
			}
			// Валидация по автору (P50).
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": map[string]any{"bindings": []map[string]map[string]string{
					{"authorLabel": {"value": authorLabel}},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

// TestFetchSrcLang — precision-гейт P407: ровно один ISO-код → принимаем
// (нормализованным); ноль или несколько (мультиязычное произведение, кейс
// «Войны и мира» ru+fr) → ErrNotFound.
func TestFetchSrcLang(t *testing.T) {
	q := BookQuery{Title: "Le Petit Prince", Authors: []string{"Сент-Экзюпери Антуан"}, Lang: "ru"}

	cases := []struct {
		name    string
		codes   []string
		want    string
		wantErr bool
	}{
		{"single code accepted", []string{"fr"}, "fr", false},
		{"code normalized", []string{"FR"}, "fr", false},
		{"no P407", nil, "", true},
		{"ambiguous multi-code rejected", []string{"ru", "fr"}, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := srcLangMockServer(t, "Antoine de Saint-Exupéry Сент-Экзюпери Антуан", c.codes)
			defer srv.Close()
			p := NewWikidataAdaptationsProvider(nil).WithEndpoints(srv.URL+"/w/api.php", srv.URL+"/sparql", "")
			got, err := p.FetchSrcLang(context.Background(), q)
			if c.wantErr {
				require.ErrorIs(t, err, ErrNotFound)
				return
			}
			require.NoError(t, err)
			require.Equal(t, c.want, got)
		})
	}
}

// Кандидат не сопоставлен (автор не совпал) → ErrNotFound, P407 не спрашиваем.
func TestFetchSrcLang_AuthorMismatch(t *testing.T) {
	srv := srcLangMockServer(t, "Совсем Другой Человек", []string{"fr"})
	defer srv.Close()
	p := NewWikidataAdaptationsProvider(nil).WithEndpoints(srv.URL+"/w/api.php", srv.URL+"/sparql", "")
	_, err := p.FetchSrcLang(context.Background(),
		BookQuery{Title: "Le Petit Prince", Authors: []string{"Сент-Экзюпери Антуан"}, Lang: "ru"})
	require.ErrorIs(t, err, ErrNotFound)
}
