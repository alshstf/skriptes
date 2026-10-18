package gen

import (
	"regexp"

	"github.com/skriptes/skriptes/backend/internal/bio"
)

var lifeDateRe = regexp.MustCompile(`^\d{4}(-\d{2}(-\d{2})?)?$`)

// HasBirthDate — в Wikidata есть дата рождения в формате датасета. Без неё
// файл не пройдёт bio.Validate (born обязателен), так что модель не зовём:
// коллективные псевдонимы (Макс Фрай, Эрин Хантер) и античные авторы
// (Гомер, «-0900») — пачка 2026-10-18.
func HasBirthDate(src *Sources) bool { return lifeDateRe.MatchString(src.Born) }

// NoEvents — модель вернула ответ без вех жизни (только рождение и смерть или
// ничего): разовый сбой извлечения, не «биографии нет» — у Перумова и Павлова
// (пачка 2026-10-18) повтор дал 11 и 13 вех.
func NoEvents(a *bio.Author) bool {
	for _, e := range a.Events {
		if e.Type != bio.EventBirth && e.Type != bio.EventDeath {
			return false
		}
	}
	return true
}
