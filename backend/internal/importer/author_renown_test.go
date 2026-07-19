package importer

import "testing"

// Инварианты формулы известности автора (см. doc в author_renown.go).
// Меняешь веса/порог — обнови ожидания И бампни гейт author_renown_computed_v<N>.
func TestComputeAuthorRenown(t *testing.T) {
	// Нулевой/пустой корпус — ноль (автор уходит в алфавитный хвост).
	if got := computeAuthorRenown(0, 0); got != 0 {
		t.Fatalf("пустой корпус: got %d, want 0", got)
	}
	if got := computeAuthorRenown(0, 50); got != 0 {
		t.Fatalf("без maxPop бонус за широту не начисляется: got %d, want 0", got)
	}

	// Монотонность по обоим аргументам.
	if computeAuthorRenown(2000, 1) <= computeAuthorRenown(1500, 1) {
		t.Fatal("монотонность по maxPop нарушена")
	}
	if computeAuthorRenown(1500, 25) <= computeAuthorRenown(1500, 1) {
		t.Fatal("монотонность по числу значимых работ нарушена")
	}

	// MAX-семантика: плодовитый самиздат (50 работ по ~160 от LIBRATE) НЕ
	// обгоняет автора одного настоящего хита.
	samizdat := computeAuthorRenown(160, 50)
	oneHit := computeAuthorRenown(2000, 1)
	if samizdat >= oneHit {
		t.Fatalf("самиздат (%d) обогнал одиночный хит (%d)", samizdat, oneHit)
	}

	// Широта корпуса компенсирует умеренную разницу топовых работ: классик с
	// 25 значимыми работами против одиночки с чуть более громким хитом.
	classic := computeAuthorRenown(1500, 25)
	louderOneHit := computeAuthorRenown(1700, 1)
	if classic <= louderOneHit {
		t.Fatalf("широта корпуса не компенсирует: классик %d <= одиночка %d", classic, louderOneHit)
	}
}
