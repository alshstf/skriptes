package catalog

// SetAuthorWorksCapForTest уменьшает предел работ карточки автора на время
// теста; возвращает функцию восстановления.
func SetAuthorWorksCapForTest(n int) (restore func()) {
	old := authorWorksCap
	authorWorksCap = n
	return func() { authorWorksCap = old }
}
