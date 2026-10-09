package kosync

import (
	"bytes"
	"crypto/md5" // #nosec G501 -- эталон алгоритма KOReader
	"encoding/hex"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

// reference — util.partialMD5 KOReader дословно: seek(1024 << 2i), read(1024),
// nil (конец файла) — стоп.
func reference(data []byte) string {
	h := md5.New() // #nosec G401
	for i := -1; i <= 10; i++ {
		var off int
		if i < 0 {
			off = 1024 >> 2
		} else {
			off = 1024 << (2 * i)
		}
		if off >= len(data) {
			break
		}
		end := min(off+1024, len(data))
		h.Write(data[off:end])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestPartialMD5(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	for _, size := range []int{0, 100, 256, 300, 1279, 1280, 2047, 2048, 4095, 4096, 5000, 70_000, 300_000, 1_100_000, 5_000_000} {
		data := make([]byte, size)
		rnd.Read(data)
		want := reference(data)
		require.Equal(t, want, PartialMD5(bytes.NewReader(data), int64(size)), "ReaderAt, %d байт", size)
		for _, chunk := range []int{1, 7, 1000, 4096, 65536} {
			if size > 400_000 && chunk < 1000 {
				continue
			}
			tr := NewTracker()
			for off := 0; off < size; off += chunk {
				_, _ = tr.Write(data[off:min(off+chunk, size)])
			}
			require.Equal(t, want, tr.Sum(), "поток, %d байт порциями по %d", size, chunk)
		}
	}
}
