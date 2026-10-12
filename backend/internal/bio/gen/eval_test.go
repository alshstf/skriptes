package gen

import (
	"strings"
	"testing"

	"github.com/skriptes/skriptes/backend/internal/bio"
)

func TestEval(t *testing.T) {
	golden := author(
		ev("loss-1868", "1868-05-24", bio.EventLoss, "Смерть дочери Сони", ""),
		ev("persecution-1849", "1849-04-23", bio.EventPersecution, "Арест", ""),
		ev("love-1867", "1867-02-15", bio.EventLove, "Венчание", ""),
		ev("birth-1821", "1821-11-11", bio.EventBirth, "Рождение", ""),
	)
	generated := author(
		ev("loss-1868", "1868-05", bio.EventLoss, "Смерть дочери", ""),          // попадание
		ev("persecution-1850", "1850", bio.EventPersecution, "Арест", ""),       // ошибка даты (+1)
		ev("career-1880", "1880-06-08", bio.EventCareer, "Пушкинская речь", ""), // лишнее major
	)
	r := Eval(golden, generated)
	if r.GoldenMajor != 3 || r.HitMajor != 1 {
		t.Fatalf("golden=%d hit=%d", r.GoldenMajor, r.HitMajor)
	}
	if len(r.DateErrors) != 1 || len(r.MissedMajor) != 1 || len(r.ExtraMajor) != 1 {
		t.Fatalf("%+v", r)
	}
	rep := Report([]EvalResult{r})
	if !strings.Contains(rep, "recall 33%") || !strings.Contains(rep, "Венчание") {
		t.Fatalf("report:\n%s", rep)
	}
}
