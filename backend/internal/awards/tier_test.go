package awards

import "testing"

func TestPopularityTier(t *testing.T) {
	for key := range majorAwards {
		if PopularityTier(key) != TierMajor {
			t.Errorf("главная премия %q не в каталоге или не главная", key)
		}
	}
	for key, want := range map[string]int{
		"hugo": TierMajor, "locus": TierAward, "aelita": TierAward,
		"oscar": TierNone, "emmy": TierNone, "unknown": TierNone,
	} {
		if got := PopularityTier(key); got != want {
			t.Errorf("PopularityTier(%q) = %d, want %d", key, got, want)
		}
	}
}
