package kosync

import (
	"crypto/md5" // #nosec G501 -- идентификатор документа KOReader, не криптография
	"encoding/hex"
	"io"
)

// Документ в синхронизации KOReader — «частичный MD5» файла (util.partialMD5):
// MD5 от кусков по 1024 байта со смещений 256·4^k (256, 1 К, 4 К, 16 К … 1 Г) до
// конца файла; последний кусок может быть короче. Readest считает так же.

const sampleSize = 1024

// sampleOffsets — смещения кусков: 1024 << 2i для i = -1…10.
var sampleOffsets = func() []int64 {
	out := []int64{256}
	for i := 0; i <= 10; i++ {
		out = append(out, int64(1024)<<(2*i))
	}
	return out
}()

// PartialMD5 — документ KOReader для файла с произвольным доступом.
func PartialMD5(r io.ReaderAt, size int64) string {
	h := md5.New() // #nosec G401 -- см. импорт
	buf := make([]byte, sampleSize)
	for _, off := range sampleOffsets {
		if off >= size {
			break
		}
		// Короткий кусок у конца файла — не повод останавливаться: следующее
		// смещение может быть ещё внутри файла (так читает KOReader).
		n, _ := r.ReadAt(buf, off)
		if n > 0 {
			h.Write(buf[:n])
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Tracker считает частичный MD5 на лету, пока файл отдаётся клиенту: куски
// идут по возрастанию смещений, поэтому хватает одного прохода. Первые два
// куска ([256, 1280) и [1024, 2048)) пересекаются — первые 2048 байт храним целиком.
type Tracker struct {
	pos     int64
	head    []byte           // первые 2048 байт
	samples map[int64][]byte // куски со смещений ≥ 4096
}

// NewTracker — пустой счётчик; пишите в него отдаваемые байты (io.Writer).
func NewTracker() *Tracker {
	return &Tracker{samples: map[int64][]byte{}}
}

// Write принимает очередную порцию отдаваемых байтов.
func (t *Tracker) Write(p []byte) (int, error) {
	start, end := t.pos, t.pos+int64(len(p))
	if start < 2048 {
		take := min(end, 2048) - start
		t.head = append(t.head, p[:take]...)
	}
	for _, off := range sampleOffsets[2:] { // 4096, 16384, …
		if end <= off {
			break
		}
		got := t.samples[off]
		if len(got) >= sampleSize || start >= off+sampleSize {
			continue
		}
		from := off + int64(len(got)) // позиция следующего нужного байта
		if from < start {
			continue // часть куска пропущена — так не бывает при последовательной записи
		}
		to := min(off+sampleSize, end)
		t.samples[off] = append(got, p[from-start:to-start]...)
	}
	t.pos = end
	return len(p), nil
}

// Sum — частичный MD5 всего записанного (вызывать после последнего Write).
func (t *Tracker) Sum() string {
	size := t.pos
	h := md5.New() // #nosec G401 -- см. импорт
	for _, off := range sampleOffsets {
		if off >= size {
			break
		}
		if off < 2048 {
			end := min(off+sampleSize, size, int64(len(t.head)))
			h.Write(t.head[off:end])
			continue
		}
		h.Write(t.samples[off])
	}
	return hex.EncodeToString(h.Sum(nil))
}
