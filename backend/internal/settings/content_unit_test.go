package settings

import (
	"reflect"
	"testing"
)

func TestContentConfig_Hides(t *testing.T) {
	c := ContentConfig{HiddenGenres: []string{"erotica"}, HiddenLanguages: []string{"bg"}}

	if !c.Hides([]string{"sf", "erotica"}, "ru") {
		t.Error("книга со скрытым жанром должна скрываться")
	}
	if !c.Hides([]string{"sf"}, "bg") {
		t.Error("книга на скрытом языке должна скрываться")
	}
	if c.Hides([]string{"sf"}, "ru") {
		t.Error("книга без скрытых атрибутов видна")
	}
	if c.Hides(nil, "") {
		t.Error("пустые атрибуты — видно")
	}

	def := DefaultContentConfig()
	if def.Hides([]string{"erotica"}, "bg") {
		t.Error("дефолт ничего не скрывает")
	}
}

func TestCleanCodes(t *testing.T) {
	got := cleanCodes([]string{"b", "", "a", "b", "a"})
	want := []string{"a", "b"} // дедуп + сортировка + без пустых
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanCodes = %v, want %v", got, want)
	}
	// Не-nil даже для пустого входа (важно для JSON `[]` вместо `null`).
	if got := cleanCodes(nil); got == nil || len(got) != 0 {
		t.Fatalf("cleanCodes(nil) должен быть пустым не-nil срезом, got %#v", got)
	}
}

func TestUnionCodes(t *testing.T) {
	got := unionCodes([]string{"a", "b"}, []string{"b", "c"})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unionCodes = %v, want %v", got, want)
	}
}

func TestContentResolver_DefaultAdmin(t *testing.T) {
	// Без БД: свежий резолвер отдаёт дефолт (ничего не скрыто), hard-block
	// никого не блокирует.
	r := NewContentResolver(nil)
	if got := r.Admin(); len(got.HiddenGenres) != 0 || len(got.HiddenLanguages) != 0 {
		t.Fatalf("дефолтный Admin() должен быть пустым, got %#v", got)
	}
	if r.AdminHides([]string{"erotica"}, "bg") {
		t.Error("дефолтный резолвер ничего не блокирует")
	}
}
