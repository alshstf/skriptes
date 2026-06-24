package metadata

import (
	"reflect"
	"testing"
)

func TestBuildExternalQuery(t *testing.T) {
	// Переводная книга: есть src_title + src_author_normalized → ищем по ОРИГИНАЛУ
	// (иначе OL/GB по русскому переводу + кириллице дают 0).
	t.Run("translation_with_src", func(t *testing.T) {
		q := buildExternalQuery(externalQueryFields{
			id: 1, title: "Еще один великолепный МИФ", lang: "ru",
			authors:       []string{"Асприн Роберт"},
			srcTitle:      "Another Fine Myth",
			srcAuthorNorm: "robert asprin",
			srcLang:       "en",
		})
		if q.Title != "Another Fine Myth" || q.Lang != "en" {
			t.Fatalf("title/lang = %q/%q, want оригинал (Another Fine Myth/en)", q.Title, q.Lang)
		}
		if !reflect.DeepEqual(q.Authors, []string{"robert asprin"}) {
			t.Fatalf("authors = %v, want [robert asprin]", q.Authors)
		}
	})

	// src_title есть, src_author пуст → автор из транслитерации первого автора.
	t.Run("translation_translit_author", func(t *testing.T) {
		q := buildExternalQuery(externalQueryFields{
			id: 2, title: "Перевод", authors: []string{"Асприн Роберт"},
			srcTitle: "Original",
		})
		if q.Title != "Original" {
			t.Fatalf("title = %q, want Original", q.Title)
		}
		if len(q.Authors) != 1 || q.Authors[0] == "" || q.Authors[0] == "Асприн Роберт" {
			t.Fatalf("authors = %v, want транслитерированные (латиница)", q.Authors)
		}
	})

	// Без src_title → локализованные поля (как было).
	t.Run("no_src_localized", func(t *testing.T) {
		q := buildExternalQuery(externalQueryFields{
			id: 3, title: "Дюна", lang: "ru", authors: []string{"Герберт Фрэнк"},
		})
		if q.Title != "Дюна" || q.Lang != "ru" {
			t.Fatalf("want localized (Дюна/ru), got %q/%q", q.Title, q.Lang)
		}
		if !reflect.DeepEqual(q.Authors, []string{"Герберт Фрэнк"}) {
			t.Fatalf("authors = %v, want [Герберт Фрэнк]", q.Authors)
		}
	})
}
