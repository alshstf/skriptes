package gen

import (
	"testing"

	"github.com/skriptes/skriptes/backend/internal/bio"
)

func TestHasBirthDate(t *testing.T) {
	for born, want := range map[string]bool{
		"1963-11-21": true, "1891-05": true, "1799": true,
		"": false, "-0900": false, "около 1800": false,
	} {
		if got := HasBirthDate(&Sources{Born: born}); got != want {
			t.Errorf("born %q → %v, want %v", born, got, want)
		}
	}
}

func TestNoEvents(t *testing.T) {
	anchors := []bio.Event{{Type: bio.EventBirth}, {Type: bio.EventDeath}}
	if !NoEvents(&bio.Author{}) || !NoEvents(&bio.Author{Events: anchors}) {
		t.Fatal("ответ без вех жизни не распознан")
	}
	if NoEvents(&bio.Author{Events: append(anchors, bio.Event{Type: bio.EventLoss})}) {
		t.Fatal("веха утраты — не пустой ответ")
	}
}
