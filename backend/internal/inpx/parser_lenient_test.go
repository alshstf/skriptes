package inpx

import (
	"strings"
	"testing"
)

// Нечисловые SERNO/SIZE/LIBRATE не обрывают разбор: поле пустое, исходник — в Extra.
func TestParseRecord_LenientNumbers(t *testing.T) {
	fields := []string{"Автор,Имя,", "periodic", "Журнал", "Журнал «Х»", "1995 01", "162829", "12a",
		"162829", "0", "fb2", "2005-01-01", "ru", "пять", ""}
	rec, err := ParseRecord([]byte(strings.Join(fields, "\x04")), DefaultSchema)
	if err != nil {
		t.Fatalf("ParseRecord: %v", err)
	}
	if rec.SerNo != 0 || rec.Size != 0 || rec.Rating != 0 {
		t.Fatalf("числа должны стать пустыми: SerNo=%d Size=%d Rating=%d", rec.SerNo, rec.Size, rec.Rating)
	}
	want := map[string]string{FieldSerNo: "1995 01", FieldSize: "12a", FieldLibRate: "пять"}
	for k, v := range want {
		if rec.Extra[k] != v {
			t.Errorf("Extra[%s] = %q, want %q", k, rec.Extra[k], v)
		}
	}
	if rec.LibID != "162829" || rec.Series != "Журнал «Х»" {
		t.Errorf("остальные поля разобраны неверно: %+v", rec)
	}

	var n int
	err = ParseInp(strings.NewReader(strings.Join(fields, "\x04")+"\r\n"+strings.Replace(strings.Join(fields, "\x04"), "162829", "162830", 2)+"\r\n"),
		DefaultSchema, func(Record) error { n++; return nil })
	if err != nil || n != 2 {
		t.Fatalf("ParseInp: err=%v, records=%d, want 2", err, n)
	}
}
