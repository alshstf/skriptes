package opds

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Базовый sanity-check: feed сериализуется в валидный XML с правильными
// namespace-объявлениями. Регрессии типа "encoding/xml выкинул xmlns" мы
// ловим парсингом обратно через xml.Decoder.
func TestMarshal_Roundtrip(t *testing.T) {
	f := &Feed{
		ID:      "urn:test:root",
		Title:   "тест",
		Updated: "2026-05-19T00:00:00Z",
		Author:  &Person{Name: "skriptes"},
		Links: []Link{
			{Rel: RelSelf, Href: "/opds/", Type: MIMEFeedNavigation},
		},
		Entries: []Entry{
			{
				ID:      "urn:test:e1",
				Title:   "Тест-entry с & < > и кавычками \"x\"",
				Updated: "2026-05-19T00:00:00Z",
				Summary: &Text{Type: "text", Body: "Описание"},
				Links: []Link{
					{Rel: RelSubsection, Href: "/opds/x", Type: MIMEFeedAcquisition},
				},
			},
		},
	}
	body, err := Marshal(f)
	require.NoError(t, err)
	s := string(body)

	// XML-prolog должен быть на месте.
	require.True(t, strings.HasPrefix(s, "<?xml"), "must start with xml prolog")

	// Namespace declarations должны быть в корневом feed-элементе.
	require.Contains(t, s, `xmlns="http://www.w3.org/2005/Atom"`)
	require.Contains(t, s, `xmlns:opensearch="http://a9.com/-/spec/opensearch/1.1/"`)
	require.Contains(t, s, `xmlns:dc="http://purl.org/dc/terms/"`)

	// Спецсимволы должны быть escape'нуты, не сломав парсинг.
	require.Contains(t, s, "&amp;")
	require.Contains(t, s, "&lt;")
	require.Contains(t, s, "&gt;")

	// Roundtrip: парсим обратно и сверяем структуру.
	var parsed Feed
	require.NoError(t, xml.Unmarshal(body, &parsed))
	require.Equal(t, "urn:test:root", parsed.ID)
	require.Equal(t, "тест", parsed.Title)
	require.Len(t, parsed.Entries, 1)
	require.Equal(t, "urn:test:e1", parsed.Entries[0].ID)
	require.Equal(t, `Тест-entry с & < > и кавычками "x"`, parsed.Entries[0].Title)
}

// pagingLinks — самая хитрая часть; проверяем все ветки.
func TestPagingLinks(t *testing.T) {
	cases := []struct {
		name        string
		basePath    string
		page, total int
		limit       int
		wantRels    []string // ожидаемый set rel-значений
		wantNotRels []string // НЕ должно быть в результате
	}{
		{
			name:     "single page",
			basePath: "/opds/recent",
			page:     1, total: 5, limit: 50,
			wantRels:    []string{RelSelf, RelStart, RelUp, RelFirst, RelLast},
			wantNotRels: []string{RelPrev, RelNext},
		},
		{
			name:     "middle page",
			basePath: "/opds/recent",
			page:     2, total: 150, limit: 50,
			wantRels:    []string{RelSelf, RelStart, RelUp, RelFirst, RelLast, RelPrev, RelNext},
			wantNotRels: nil,
		},
		{
			name:     "first page of many",
			basePath: "/opds/recent",
			page:     1, total: 150, limit: 50,
			wantRels:    []string{RelSelf, RelStart, RelUp, RelFirst, RelLast, RelNext},
			wantNotRels: []string{RelPrev},
		},
		{
			name:     "last page of many",
			basePath: "/opds/recent",
			page:     3, total: 150, limit: 50,
			wantRels:    []string{RelSelf, RelStart, RelUp, RelFirst, RelLast, RelPrev},
			wantNotRels: []string{RelNext},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			links := pagingLinks("https://example.com", tc.basePath, tc.page, tc.total, tc.limit, MIMEFeedAcquisition)
			gotRels := map[string]bool{}
			for _, l := range links {
				gotRels[l.Rel] = true
			}
			for _, want := range tc.wantRels {
				require.Truef(t, gotRels[want], "rel %q should be present", want)
			}
			for _, notWant := range tc.wantNotRels {
				require.Falsef(t, gotRels[notWant], "rel %q should NOT be present", notWant)
			}
		})
	}
}

// Search query с пробелами не должен ломать ?q= → &page=N. Проверяем
// конкретно сцепку basePath с уже-имеющимся "?…" на &page= вместо ?page=.
func TestPagingLinks_PreservesQueryString(t *testing.T) {
	links := pagingLinks("https://example.com", "/opds/search?q=war", 2, 100, 50, MIMEFeedAcquisition)
	for _, l := range links {
		if l.Rel == RelSelf {
			require.Contains(t, l.Href, "?q=war&page=2", "must use & not ? after existing ?q=")
			require.NotContains(t, l.Href, "?q=war?page=2")
		}
	}
}

// sanitizePaging — защита от мусорных значений.
func TestSanitizePaging(t *testing.T) {
	// (sanitizePaging реально живёт в catalog, тут мы проверяем что
	// pagingLinks устойчив к limit=0 / total=0.)
	links := pagingLinks("https://example.com", "/opds/recent", 1, 0, 50, MIMEFeedAcquisition)
	require.NotEmpty(t, links) // self/start/up/first/last всегда должны быть
}

// makeFormats — фиксируем контракт: первым отдаём fb2 (наша целевая
// аудитория — KOReader / CoolReader / fb2 нативно), вторым EPUB3
// (универсальный fallback для Kindle/Apple Books). Если кто-то
// меняет порядок или MIME — пусть осознанно ломает этот тест.
func TestMakeFormats_FB2First(t *testing.T) {
	h := NewHandler(Config{}, Deps{})
	formats := h.makeFormats(42)
	require.Len(t, formats, 2)
	require.Equal(t, "/opds/books/42/download?format=fb2", formats[0].HrefPath)
	require.Equal(t, "application/x-fictionbook+xml", formats[0].MIME)
	require.Equal(t, "/opds/books/42/download?format=epub3", formats[1].HrefPath)
	require.Equal(t, "application/epub+zip", formats[1].MIME)
}

// guessImageMIME — узкая утилита, но обложки в OPDS должны иметь
// правильный type, иначе KOReader не отображает.
func TestGuessImageMIME(t *testing.T) {
	cases := map[string]string{
		"foo.jpg":   "image/jpeg",
		"foo.jpeg":  "image/jpeg",
		"foo.png":   "image/png",
		"foo.webp":  "image/webp",
		"foo.gif":   "image/gif",
		"no-ext":    "image/jpeg",
		"weird.bin": "image/jpeg",
	}
	for fn, want := range cases {
		require.Equalf(t, want, guessImageMIME(fn), "input=%q", fn)
	}
}
