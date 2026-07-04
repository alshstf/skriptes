package metadata

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestClampOLRPM — OL-RPM прижимается к документированному лимиту. Политика OL
// (2026-05): 1 req/s анонимно / 3 req/s с UA → cap 60/мин (консервативно 1 req/s).
func TestClampOLRPM(t *testing.T) {
	require.Equal(t, 60, olRPMCap, "cap = 1 req/s по политике OL 2026-05")
	require.Equal(t, olRPMCap, clampOLRPM(0))        // 0/без-лимита → cap
	require.Equal(t, olRPMCap, clampOLRPM(100))      // выше cap → cap
	require.Equal(t, olRPMCap, clampOLRPM(olRPMCap)) // на границе
	require.Equal(t, 10, clampOLRPM(10))             // вежливее cap — оставляем
}

// TestClampFantlabRPM — лимиты api.fantlab.ru не документированы, держим
// вежливый потолок.
func TestClampFantlabRPM(t *testing.T) {
	require.Equal(t, fantlabRPMCap, clampFantlabRPM(0))
	require.Equal(t, fantlabRPMCap, clampFantlabRPM(1000))
	require.Equal(t, 10, clampFantlabRPM(10))
}
