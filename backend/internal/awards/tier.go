package awards

// Ранг премии для известности книг и авторов (#420, вариант A — решение
// владельца 2026-10-10). Главные — премии, которые знают за пределами жанра и
// страны. Веса бонуса — в importer/popularity.go.
var majorAwards = map[string]bool{
	"nobel": true, "booker": true, "pulitzer": true, "goncourt": true, "nba": true,
	"russian-booker": true, "hugo": true, "nebula": true,
}

// Ранги PopularityTier.
const (
	TierNone  = 0 // кинопремия или ключ вне белого списка
	TierAward = 1
	TierMajor = 2
)

// PopularityTier — ранг премии key для известности. Кинопремии экранизаций —
// TierNone: экранизация и так даёт книге свой бонус.
func PopularityTier(key string) int {
	for _, a := range Catalog {
		if a.Key != key {
			continue
		}
		switch {
		case a.Film:
			return TierNone
		case majorAwards[key]:
			return TierMajor
		default:
			return TierAward
		}
	}
	return TierNone
}
