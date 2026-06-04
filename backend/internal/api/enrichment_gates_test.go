package api

import (
	"testing"

	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/settings"
)

// TestBookEnrichTargets — матрица «что дозагружать» для обложки/аннотации:
// данных нет И тип не выключен в админке. «Выкл» (gate) глушит даже при
// отсутствующих данных; присутствие данных глушит независимо от гейта.
func TestBookEnrichTargets(t *testing.T) {
	cases := []struct {
		name          string
		gates         settings.EnrichmentGates
		book          books.Book
		wantCover     bool
		wantAnnotatio bool
	}{
		{
			name:          "обоих нет, ничего не выключено → оба",
			gates:         settings.EnrichmentGates{},
			book:          books.Book{},
			wantCover:     true,
			wantAnnotatio: true,
		},
		{
			name:          "обоих нет, обложка выключена → только аннотация",
			gates:         settings.EnrichmentGates{CoverDisabled: true},
			book:          books.Book{},
			wantCover:     false,
			wantAnnotatio: true,
		},
		{
			name:          "обоих нет, аннотация выключена → только обложка",
			gates:         settings.EnrichmentGates{AnnotationDisabled: true},
			book:          books.Book{},
			wantCover:     true,
			wantAnnotatio: false,
		},
		{
			name:          "обоих нет, оба выключены → ничего",
			gates:         settings.EnrichmentGates{CoverDisabled: true, AnnotationDisabled: true},
			book:          books.Book{},
			wantCover:     false,
			wantAnnotatio: false,
		},
		{
			name:          "обложка есть, аннотации нет → только аннотация",
			gates:         settings.EnrichmentGates{},
			book:          books.Book{CoverPath: "abc.jpg"},
			wantCover:     false,
			wantAnnotatio: true,
		},
		{
			name:          "оба на месте → ничего (гейт неважен)",
			gates:         settings.EnrichmentGates{CoverDisabled: true, AnnotationDisabled: true},
			book:          books.Book{CoverPath: "abc.jpg", Annotation: "текст"},
			wantCover:     false,
			wantAnnotatio: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cover, ann := bookEnrichTargets(c.gates, c.book)
			if cover != c.wantCover || ann != c.wantAnnotatio {
				t.Fatalf("bookEnrichTargets = (cover=%v, ann=%v), want (cover=%v, ann=%v)",
					cover, ann, c.wantCover, c.wantAnnotatio)
			}
		})
	}
}

// TestAuthorEnrichWanted — гейт «Выкл» + single-shot (EnrichmentFetched) +
// «всё на месте».
func TestAuthorEnrichWanted(t *testing.T) {
	cases := []struct {
		name  string
		gates settings.EnrichmentGates
		a     catalog.Author
		want  bool
	}{
		{"ничего нет, не выключено → да", settings.EnrichmentGates{}, catalog.Author{}, true},
		{"выключено → нет", settings.EnrichmentGates{AuthorDisabled: true}, catalog.Author{}, false},
		{"попытка уже была → нет", settings.EnrichmentGates{}, catalog.Author{EnrichmentFetched: true}, false},
		{"только фото отсутствует → да", settings.EnrichmentGates{}, catalog.Author{Bio: "текст"}, true},
		{"всё на месте → нет", settings.EnrichmentGates{}, catalog.Author{PhotoPath: "p.jpg", Bio: "текст"}, false},
		{"выключено приоритетнее отсутствующих данных", settings.EnrichmentGates{AuthorDisabled: true}, catalog.Author{Bio: "текст"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := authorEnrichWanted(c.gates, c.a); got != c.want {
				t.Fatalf("authorEnrichWanted = %v, want %v", got, c.want)
			}
		})
	}
}

// TestAdaptationEnrichWanted — гейт «Выкл» для экранизаций.
func TestAdaptationEnrichWanted(t *testing.T) {
	if !adaptationEnrichWanted(settings.EnrichmentGates{}) {
		t.Fatal("по умолчанию экранизации должны обогащаться")
	}
	if adaptationEnrichWanted(settings.EnrichmentGates{AdaptationDisabled: true}) {
		t.Fatal("при «Выкл» экранизации не должны обогащаться")
	}
}
