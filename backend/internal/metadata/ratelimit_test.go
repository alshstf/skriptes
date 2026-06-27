package metadata

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestClampOLRPM — OL-RPM прижимается к документированному лимиту (~20/мин); дефолт
// 60 (1 req/s) втрое превышал его и OL троттлил → reset/таймауты.
func TestClampOLRPM(t *testing.T) {
	require.Equal(t, olRPMCap, clampOLRPM(60))       // дефолт → cap
	require.Equal(t, olRPMCap, clampOLRPM(0))        // 0/без-лимита → cap
	require.Equal(t, olRPMCap, clampOLRPM(100))      // выше cap → cap
	require.Equal(t, olRPMCap, clampOLRPM(olRPMCap)) // на границе
	require.Equal(t, 10, clampOLRPM(10))             // вежливее cap — оставляем
}
