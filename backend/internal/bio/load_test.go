package bio

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// testdata — эталонный Достоевский (Q991) и регионы фона; сам датасет живёт
// в alshstf/skriptes-bio и проверяется там (biogen check в CI того репо).
func loadTestdata(t *testing.T) *Dataset {
	t.Helper()
	ds, err := LoadFS(os.DirFS("."), "testdata")
	if err != nil {
		t.Fatal(err)
	}
	return ds
}

func TestLoadTestdata(t *testing.T) {
	ds := loadTestdata(t)
	if _, ok := ds.Authors["Q991"]; !ok {
		t.Fatal("нет Достоевского (Q991)")
	}
	if len(ds.Version) != 16 || len(ds.Context) == 0 {
		t.Fatalf("version %q, регионов %d", ds.Version, len(ds.Context))
	}
}

func TestPackRoundTrip(t *testing.T) {
	ds := loadTestdata(t)
	path := filepath.Join(t.TempDir(), "bio.pack.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := WritePack(f, ds); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	pack, err := OpenPack(path)
	if err != nil || len(pack.Version) != 16 {
		t.Fatalf("open: %v, version %q", err, pack.Version)
	}
	full, err := pack.Read(true)
	if err != nil {
		t.Fatal(err)
	}
	a := full.Authors["Q991"]
	if a == nil || len(a.Events) != len(ds.Authors["Q991"].Events) || full.Version != pack.Version {
		t.Fatalf("автор после пакета: %+v", full.Authors)
	}
	for _, e := range a.Events {
		if e.Evidence != nil {
			t.Fatalf("evidence в пакете: %s", e.Key)
		}
	}
	if ds.Authors["Q991"].Events[1].Evidence == nil {
		t.Fatal("WritePack испортил исходный датасет")
	}
	ctxOnly, err := pack.Read(false)
	if err != nil || len(ctxOnly.Authors) != 0 || len(ctxOnly.Context) != len(ds.Context) {
		t.Fatalf("только фон: err=%v авторов %d регионов %d", err, len(ctxOnly.Authors), len(ctxOnly.Context))
	}
	if _, err := ReadPack(strings.NewReader("не gzip"), true); err == nil {
		t.Fatal("мусор принят за пакет")
	}
	if _, err := OpenPack(filepath.Join(t.TempDir(), "нет.gz")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("нет файла: %v", err)
	}
}

func TestLoadFSRejectsInvalid(t *testing.T) {
	fsys := fstest.MapFS{
		"data/authors/Q1.json": {Data: []byte(`{"qid":"Q1","name":"X","born":"1900","events":[{"key":"loss-1950","date":{"from":"1950","precision":"year"},"type":"loss","tier":"major","short":"Смерть матери в 1950 году"}]}`)},
		"data/context/ru.json": {Data: []byte(`{"region":"ru","title":"Россия","events":[]}`)},
	}
	_, err := LoadFS(fsys, "data")
	if err == nil || !strings.Contains(err.Error(), "год в формулировке") {
		t.Fatalf("ожидалась ошибка про год в short, получено %v", err)
	}
	// Имя файла ≠ qid.
	fsys["data/authors/Q1.json"] = &fstest.MapFile{Data: []byte(`{"qid":"Q2","name":"X","born":"1900","events":[]}`)}
	if _, err := LoadFS(fsys, "data"); err == nil || !strings.Contains(err.Error(), "имя файла") {
		t.Fatalf("ожидалась ошибка про имя файла, получено %v", err)
	}
	// Неизвестный регион фона.
	fsys["data/authors/Q1.json"] = &fstest.MapFile{Data: []byte(`{"qid":"Q1","name":"X","born":"1900","context":["mars"],"events":[]}`)}
	if _, err := LoadFS(fsys, "data"); err == nil || !strings.Contains(err.Error(), "региона") {
		t.Fatalf("ожидалась ошибка про регион, получено %v", err)
	}
	// Версия стабильна и меняется от содержимого.
	fsys["data/authors/Q1.json"] = &fstest.MapFile{Data: []byte(`{"qid":"Q1","name":"X","born":"1900","events":[]}`)}
	ds1, err := LoadFS(fsys, "data")
	if err != nil {
		t.Fatal(err)
	}
	ds2, _ := LoadFS(fsys, "data")
	if ds1.Version != ds2.Version {
		t.Fatal("версия нестабильна")
	}
	fsys["data/authors/Q1.json"] = &fstest.MapFile{Data: []byte(`{"qid":"Q1","name":"Y","born":"1900","events":[]}`)}
	ds3, _ := LoadFS(fsys, "data")
	if ds3.Version == ds1.Version {
		t.Fatal("версия не меняется от содержимого")
	}
}
