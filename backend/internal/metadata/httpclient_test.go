package metadata

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestEnricherHTTPClientSetsUserAgent — клиент проставляет осмысленный UA
// (иначе OpenLibrary троттлит анонимный Go-UA → таймауты).
func TestEnricherHTTPClientSetsUserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
	}))
	defer srv.Close()

	resp, err := NewEnricherHTTPClient(5 * time.Second).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got != enricherUserAgent {
		t.Fatalf("User-Agent = %q, want %q", got, enricherUserAgent)
	}
}

// TestEnricherHTTPClientKeepsExplicitUA — явно выставленный UA не перетирается.
func TestEnricherHTTPClientKeepsExplicitUA(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("User-Agent", "custom/1.0")
	resp, err := NewEnricherHTTPClient(5 * time.Second).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got != "custom/1.0" {
		t.Fatalf("explicit UA overwritten: got %q", got)
	}
}

// TestNewCoverBackfillerClampsOLRPM — OL-гейт клампится к olCoverRPMCap (док-лимит
// covers API 20/мин), GB — нет. Покрывает и «60 → 18», и «0/unlimited → 18».
func TestNewCoverBackfillerClampsOLRPM(t *testing.T) {
	cases := []struct{ inOL, wantOL int }{
		{60, olCoverRPMCap}, // выше лимита → клампим
		{0, olCoverRPMCap},  // «без лимита» → тоже клампим (иначе 403)
		{10, 10},            // в пределах — как есть
	}
	for _, c := range cases {
		b := NewCoverBackfiller(nil, nil, nil, nil,
			CoverBackfillConfig{OpenLibraryRPM: c.inOL, GoogleBooksRPM: 60}, nil)
		want := time.Minute / time.Duration(c.wantOL)
		if b.olGate.interval != want {
			t.Errorf("OL rpm in=%d: interval=%v, want %v (=%d rpm)", c.inOL, b.olGate.interval, want, c.wantOL)
		}
		if b.gbGate.interval != time.Minute/60 {
			t.Errorf("GB interval=%v, want %v", b.gbGate.interval, time.Minute/60)
		}
	}
}
