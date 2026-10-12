package bio

import (
	"strings"
	"testing"
	"testing/fstest"
)

// Встроенный датасет обязан грузиться без ошибок — иначе релиз с битым файлом.
func TestLoadEmbedded(t *testing.T) {
	ds, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.Authors) == 0 {
		t.Fatal("датасет пуст")
	}
	if _, ok := ds.Authors["Q991"]; !ok {
		t.Fatal("нет Достоевского (Q991)")
	}
	if len(ds.Version) != 16 {
		t.Fatalf("version %q", ds.Version)
	}
	for qid, a := range ds.Authors {
		for _, p := range Validate(a) {
			if p.Severity == "warn" {
				t.Logf("%s: %s", qid, p)
			}
		}
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
