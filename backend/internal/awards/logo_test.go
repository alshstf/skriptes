package awards

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
)

// 1×1 PNG.
var pngPixel = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89, 0, 0, 0, 0x0a, 0x49, 0x44, 0x41, 0x54,
	0x78, 0x9c, 0x63, 0, 1, 0, 0, 5, 0, 1, 0x0d, 0x0a, 0x2d, 0xb4, 0, 0, 0, 0, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82}

func TestLogoURL(t *testing.T) {
	for key := range logoOverrides {
		if _, ok := ByKey(key); !ok {
			t.Errorf("логотип для премии вне каталога: %q", key)
		}
	}
	for _, a := range Catalog {
		if a.Key == "sword-without-name" {
			if a.Logo {
				t.Errorf("у «Меча без имени» картинки нет — монограмма")
			}
			continue
		}
		if !a.Logo {
			t.Errorf("у премии %q нет картинки", a.Key)
		}
	}
}

// Картинка скачивается один раз и дальше берётся из кэша; не картинка — отказ,
// и повтор не раньше logoRetryAfter; премия без картинки — ErrNoLogo.
func TestLogoCache(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/bad" {
			_, _ = w.Write([]byte("<html>нет</html>"))
			return
		}
		_, _ = w.Write(pngPixel)
	}))
	defer srv.Close()
	c := NewLogoCache(t.TempDir())
	c.client = srv.Client()
	c.source = func(a Award) string {
		switch a.Key {
		case "hugo":
			return srv.URL + "/hugo"
		case "nebula":
			return srv.URL + "/bad"
		}
		return ""
	}
	ctx := context.Background()

	path, err := c.Path(ctx, "hugo")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); len(b) != len(pngPixel) {
		t.Fatalf("в кэше не та картинка: %d байт", len(b))
	}
	if _, err := c.Path(ctx, "hugo"); err != nil || hits.Load() != 1 {
		t.Fatalf("второй запрос — из кэша: err=%v hits=%d", err, hits.Load())
	}

	if _, err := c.Path(ctx, "nebula"); !errors.Is(err, ErrNoLogo) {
		t.Fatalf("не картинка — ErrNoLogo, got %v", err)
	}
	if _, err := c.Path(ctx, "nebula"); !errors.Is(err, ErrNoLogo) || hits.Load() != 2 {
		t.Fatalf("повтор неудачи — без запроса: err=%v hits=%d", err, hits.Load())
	}
	if _, err := c.Path(ctx, "locus"); !errors.Is(err, ErrNoLogo) {
		t.Fatalf("без адреса — ErrNoLogo, got %v", err)
	}
	if _, err := c.Path(ctx, "nope"); !errors.Is(err, ErrUnknownAward) {
		t.Fatalf("неизвестная премия, got %v", err)
	}
}
