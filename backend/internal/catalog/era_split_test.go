package catalog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDetectEraSplit(t *testing.T) {
	ws := func(years ...int) []workYear {
		out := make([]workYear, len(years))
		for i, y := range years {
			out[i] = workYear{workID: int64(i + 1), year: y}
		}
		return out
	}
	// Берг Николай: поэт XIX века и автор сетевой литературы (#356).
	s := detectEraSplit(ws(1850, 1855, 1863, 1873, 2010, 2011, 2011, 2012, 2015, 2015, 2018, 2021, 2025, 0))
	require.NotNil(t, s)
	require.Equal(t, 1850, s.Older.From)
	require.Equal(t, 1873, s.Older.To)
	require.Equal(t, []int64{1, 2, 3, 4}, s.Older.WorkIDs)
	require.Equal(t, 2010, s.Newer.From)
	require.Len(t, s.Newer.WorkIDs, 9)

	require.Nil(t, detectEraSplit(ws(1825, 1830, 1833, 1836, 1837)), "одна жизнь")
	require.Nil(t, detectEraSplit(ws(1820, 1825, 1830, 1833, 1835, 1836, 1837, 1828, 1829, 1831, 1832, 1990, 1991, 1834, 1826, 1827, 1822, 1823, 1824, 1821, 1838, 1839)),
		"две ошибки дат на двадцать работ — не повод")
	require.Nil(t, detectEraSplit(ws(1850, 2010, 2011)), "мало работ")
	require.Nil(t, detectEraSplit(ws(1900, 1910, 1960, 1970)), "разрыв меньше 80 лет")
}
