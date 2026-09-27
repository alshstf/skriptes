package importer

import "testing"

func TestIsGenericSeriesTitle(t *testing.T) {
	for title, want := range map[string]bool{
		"рассказы":                   true,
		"повести и рассказы":         true,
		"мемуары, дневники, письма":  true,
		"сборник рассказов":          true,
		"избранные произведения":     true,
		"рассказы.":                  true,
		"short stories":              true,
		"и":                          false,
		"":                           false,
		"мини-шарм":                  false,
		"антология фантастики":       false,
		"рассказы журнала «esquire»": false,
		"ранние рассказы":            false,
		"шекли, роберт. сборники":    false,
	} {
		if got := isGenericSeriesTitle(title); got != want {
			t.Errorf("isGenericSeriesTitle(%q) = %v, want %v", title, got, want)
		}
	}
}
