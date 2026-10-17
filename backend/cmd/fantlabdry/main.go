// Command fantlabdry — сухой прогон добора года написания из библиографий Фантлаба
// (#533) на выгрузке каталога: тот же metadata.FantlabProvider.AuthorYears, что
// у воркера, без записи в базу. Ответы источников кэшируются на диск — повтор
// прогона бесплатный.
//
// Вход — выгрузки prodq.sh (разделитель «|»):
//
//	-authors: id|last_name|first_name|wd_qid|renown
//	-works:   author_id|work_id|title|written_year|written_year_source|visible(t/f)|src_title1 ~ src_title2
//
// Выход: сводка в stdout, -out — предложения (work_id|title|year|fl_work_id|via),
// -diff — расхождения с уже известным годом (для ручного разбора).
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/skriptes/skriptes/backend/internal/metadata"
)

type author struct {
	id          int64
	last, first string
	qid         string
	renown      int64
}

type work struct {
	q       metadata.FantlabWorkQuery
	year    int
	source  string
	visible bool
}

func main() {
	authorsPath := flag.String("authors", "", "выгрузка авторов")
	worksPath := flag.String("works", "", "выгрузка работ")
	cacheDir := flag.String("cache", "fantlabdry-cache", "кэш ответов источников")
	outPath := flag.String("out", "", "предложения (tsv)")
	diffPath := flag.String("diff", "", "расхождения с известным годом (tsv)")
	perAuthorPath := flag.String("per-author", "", "итог по авторам (tsv): кто найден, сколько работ без года датировано")
	rpm := flag.Int("rpm", 30, "запросов к Фантлабу в минуту (кэш не считается)")
	flag.Parse()
	if *authorsPath == "" || *worksPath == "" {
		log.Fatal("нужны -authors и -works")
	}
	authors := readAuthors(*authorsPath)
	works := readWorks(*worksPath)
	client := &http.Client{Timeout: 90 * time.Second, Transport: &cachingTransport{dir: *cacheDir, next: http.DefaultTransport}}
	ctx := context.Background()

	// FantLab ID по Wikidata P7433 — пачками.
	var qids []string
	for _, a := range authors {
		if a.qid != "" {
			qids = append(qids, a.qid)
		}
	}
	wd := metadata.NewWikidataAdaptationsProvider(client)
	flIDs := map[string]int64{}
	for i := 0; i < len(qids); i += 200 {
		m, err := wd.FantlabAuthorIDs(ctx, qids[i:min(i+200, len(qids))])
		if err != nil {
			log.Fatalf("wikidata P7433: %v", err)
		}
		for k, v := range m {
			flIDs[k] = v
		}
	}

	fl := metadata.NewFantlabProvider(client)
	interval := time.Minute / time.Duration(max(1, *rpm))
	var last time.Time
	wait := func(context.Context) error {
		if d := time.Until(last.Add(interval)); d > 0 {
			time.Sleep(d)
		}
		last = time.Now()
		return nil
	}

	out := newTSV(*outPath)
	defer out.close()
	diff := newTSV(*diffPath)
	defer diff.close()
	diff.printf("автор|работа|наш год|источник|год Фантлаба|разница\n")
	perAuthor := newTSV(*perAuthorPath)
	defer perAuthor.close()
	perAuthor.printf("автор|известность|итог|работ|видимых без года|датировано\n")

	var st struct {
		authors, viaWD, viaSearch, notFound, errs   int
		undated, undatedHit, dated, datedHit, equal int
		pm1, flEarlier, flLater                     int
	}
	sort.Slice(authors, func(i, j int) bool { return authors[i].renown > authors[j].renown })
	for i, a := range authors {
		ws := works[a.id]
		if len(ws) == 0 {
			continue
		}
		st.authors++
		qs := make([]metadata.FantlabWorkQuery, len(ws))
		byID := map[int64]work{}
		undated := 0
		for j, w := range ws {
			qs[j] = w.q
			byID[w.q.ID] = w
			if w.visible && w.year == 0 {
				undated++
			}
		}
		st.undated += undated
		res, err := fl.AuthorYears(ctx, metadata.FantlabAuthorQuery{LastName: a.last, FirstName: a.first, FantlabID: flIDs[a.qid]}, qs, wait)
		switch {
		case errors.Is(err, metadata.ErrNotFound):
			st.notFound++
			perAuthor.printf("%s %s|%d|not_found|%d|%d|0\n", a.last, a.first, a.renown, len(ws), undated)
			continue
		case err != nil:
			st.errs++
			log.Printf("%s %s: %v", a.last, a.first, err)
			perAuthor.printf("%s %s|%d|error|%d|%d|0\n", a.last, a.first, a.renown, len(ws), undated)
			continue
		case res.Via == "wikidata":
			st.viaWD++
		default:
			st.viaSearch++
		}
		hit := 0
		for _, m := range res.Matched {
			w := byID[m.WorkID]
			if m.Year == 0 {
				continue
			}
			if w.year == 0 {
				if w.visible {
					st.undatedHit++
					hit++
				}
				out.printf("%d|%s|%d|%d|%s|%s %s\n", m.WorkID, w.q.Title, m.Year, m.FlWorkID, res.Via, a.last, a.first)
				continue
			}
			st.datedHit++
			d := m.Year - w.year
			switch {
			case d == 0:
				st.equal++
			case d == 1 || d == -1:
				st.pm1++
			case d < 0:
				st.flEarlier++
			default:
				st.flLater++
			}
			if d != 0 {
				diff.printf("%s %s|%s|%d|%s|%d|%+d\n", a.last, a.first, w.q.Title, w.year, w.source, m.Year, d)
			}
		}
		perAuthor.printf("%s %s|%d|%s|%d|%d|%d\n", a.last, a.first, a.renown, res.Via, len(ws), undated, hit)
		if (i+1)%50 == 0 {
			log.Printf("%d/%d авторов, датировано %d из %d", i+1, len(authors), st.undatedHit, st.undated)
		}
	}
	for _, w := range works {
		for _, x := range w {
			if x.year > 0 {
				st.dated++
			}
		}
	}
	fmt.Printf("авторов с работами: %d; Фантлаб найден: по P7433 %d, поиском %d; не найден %d; ошибок %d\n",
		st.authors, st.viaWD, st.viaSearch, st.notFound, st.errs)
	fmt.Printf("видимых без года: %d → датированы %d (%.0f %%)\n", st.undated, st.undatedHit, pct(st.undatedHit, st.undated))
	fmt.Printf("сверка с известным годом (%d найдено из %d датированных): совпал %d (%.0f %%), ±1 — %d (%.0f %%), Фантлаб раньше %d, позже %d\n",
		st.datedHit, st.dated, st.equal, pct(st.equal, st.datedHit), st.pm1, pct(st.pm1, st.datedHit), st.flEarlier, st.flLater)
}

// tsv — выходной файл; пустой путь — вывод отключён. Ошибка записи фатальна:
// обрезанный отчёт хуже, чем никакого.
type tsv struct {
	f *os.File
	w *bufio.Writer
}

func newTSV(path string) *tsv {
	if path == "" {
		return &tsv{}
	}
	f := must(os.Create(filepath.Clean(path))) //nolint:gosec // путь из флага CLI владельца
	return &tsv{f: f, w: bufio.NewWriter(f)}
}

func (t *tsv) printf(format string, args ...any) {
	if t.w == nil {
		return
	}
	if _, err := fmt.Fprintf(t.w, format, args...); err != nil {
		log.Fatal(err)
	}
}

func (t *tsv) close() {
	if t.w == nil {
		return
	}
	if err := t.w.Flush(); err != nil {
		log.Fatal(err)
	}
	if err := t.f.Close(); err != nil {
		log.Fatal(err)
	}
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

func readAuthors(path string) []author {
	var out []author
	for _, p := range readRows(path, 5) {
		out = append(out, author{id: atoi64(p[0]), last: p[1], first: p[2], qid: p[3], renown: atoi64(p[4])})
	}
	return out
}

func readWorks(path string) map[int64][]work {
	out := map[int64][]work{}
	for _, p := range readRows(path, 7) {
		w := work{q: metadata.FantlabWorkQuery{ID: atoi64(p[1]), Title: p[2]}, year: int(atoi64(p[3])), source: p[4], visible: p[5] == "t"}
		for _, s := range strings.Split(p[6], " ~ ") {
			if s = strings.TrimSpace(s); s != "" {
				w.q.SrcTitles = append(w.q.SrcTitles, s)
			}
		}
		out[atoi64(p[0])] = append(out[atoi64(p[0])], w)
	}
	return out
}

func readRows(path string, n int) [][]string {
	f := must(os.Open(filepath.Clean(path))) //nolint:gosec // путь из флага CLI владельца
	defer func() { _ = f.Close() }()
	var rows [][]string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		if p := strings.SplitN(sc.Text(), "|", n); len(p) == n {
			rows = append(rows, p)
		}
	}
	return rows
}

func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

func must[T any](v T, err error) T {
	if err != nil {
		log.Fatal(err)
	}
	return v
}

// cachingTransport — дисковый кэш ответов 200/404 по методу, URL и телу запроса.
type cachingTransport struct {
	dir  string
	next http.RoundTripper
}

func (t *cachingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	sum := sha256.Sum256(append([]byte(r.Method+" "+r.URL.String()+"\n"), body...))
	path := filepath.Join(t.dir, hex.EncodeToString(sum[:]))
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 { //nolint:gosec // путь — хэш запроса в каталоге кэша
		code := http.StatusOK
		if b[0] == '4' {
			code, b = http.StatusNotFound, nil
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(bytes.NewReader(b)), Header: http.Header{}, Request: r}, nil
	}
	resp, err := t.next.RoundTrip(r)
	if err != nil || (resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound) {
		return resp, err
	}
	b, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	_ = os.MkdirAll(t.dir, 0o750)
	keep := b
	if resp.StatusCode == http.StatusNotFound {
		keep = []byte("404")
	}
	_ = os.WriteFile(path, keep, 0o600) //nolint:gosec // путь — хэш запроса в каталоге кэша
	resp.Body = io.NopCloser(bytes.NewReader(b))
	return resp, nil
}
