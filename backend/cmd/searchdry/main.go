// searchdry — сухой прогон персонализированной пересортировки поиска (грабля 13
// CLAUDE.md; план recsys/03, задача 3.5). По каждому запросу из JSONL —
// страница поиска (ListWorks, окно 20) и подсказки Cmd+K (SuggestWorks, 5) без
// негативов и с каждым синтетическим негативом по ожидаемой работе: блок
// автора, блок цикла, «не интересно» из ленты, «бросил». Печатает, куда
// сдвинулась ожидаемая работа. Только чтение: соединение с БД —
// default_transaction_read_only, индекс не меняется — можно гонять на проде
// (docker compose exec -T backend searchdry …).
//
//	searchdry -queries queries.jsonl [-out results.jsonl] [-user 1] [-limit 20] [-suggest 5] [-parallel 4]
//	searchdry -summarize results.jsonl        # только сводка по готовому JSONL, без прогона
//
// Строка queries.jsonl: {"q":"Три мушкетёра","work_id":123,"author_id":45,"series_id":7,"label":"Дюма"}.
// Без work_id ожидаемая работа — первый хит страницы без негативов (автор и
// цикл — из выдачи): так гоняют запросы владельца. -user — положить в основу
// реальный профиль пользователя (подписки, история, его негативы; нужны таблицы
// 1.61.0). -out — строка JSON на (запрос, режим, вариант): позиция ожидаемой
// работы и id выдачи; на проде файл с запросами владельца — в
// ~/projects/plans/skriptes/recsys/results/ (в .gitignore). Итог —
// markdown-таблица в stdout для recsys/eval-log.md.
//
// DSN и Meili — из окружения бекенда (SKRIPTES_DATABASE_URL, SKRIPTES_MEILI_*).
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meilisearch/meilisearch-go"
	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/config"
	"github.com/skriptes/skriptes/backend/internal/history"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type query struct {
	Q        string `json:"q"`
	WorkID   int64  `json:"work_id,omitempty"`
	AuthorID int64  `json:"author_id,omitempty"`
	SeriesID int64  `json:"series_id,omitempty"`
	Label    string `json:"label,omitempty"`
}

// Варианты негативов по ожидаемой работе.
const (
	variantNone          = "none"
	variantBlockedAuthor = "blocked_author"
	variantBlockedSeries = "blocked_series"
	variantNotInterested = "not_interested"
	variantDropped       = "dropped"
)

var variants = []string{variantNone, variantBlockedAuthor, variantBlockedSeries, variantNotInterested, variantDropped}

const (
	modeList    = "list"    // страница поиска (/works?q=), окно 20
	modeSuggest = "suggest" // Cmd+K и hero-подсказки, 5 строк
)

type result struct {
	Q       string  `json:"q"`
	Label   string  `json:"label,omitempty"`
	WorkID  int64   `json:"work_id"`
	Mode    string  `json:"mode"`
	Variant string  `json:"variant"`
	Pos     int     `json:"pos"` // -1 — нет в выдаче
	Top     int64   `json:"top"`
	IDs     []int64 `json:"ids"`
}

// persona — books.PersonaSource с подставным профилем: один на воркер,
// переключается между вариантами перед каждым вызовом.
type persona struct{ p history.PersonaProfile }

func (s *persona) PersonaProfile(context.Context, int64) (history.PersonaProfile, error) {
	return s.p, nil
}

func run() error {
	queriesPath := flag.String("queries", "", "JSONL с запросами (обязателен)")
	out := flag.String("out", "", "JSONL с результатами по (запрос, режим, вариант)")
	user := flag.Int64("user", 0, "id пользователя — положить в основу его реальный профиль (0 — пустой)")
	limit := flag.Int("limit", 20, "окно страницы поиска")
	suggestN := flag.Int("suggest", 5, "строк подсказок")
	parallel := flag.Int("parallel", 4, "параллельных воркеров")
	summarize := flag.String("summarize", "", "JSONL результатов прежнего прогона — только сводка, без прогона")
	flag.Parse()
	if *summarize != "" {
		rs, err := readResults(*summarize)
		if err != nil {
			return err
		}
		printSummary(rs, *suggestN)
		return nil
	}
	if *queriesPath == "" {
		return errors.New("-queries обязателен")
	}
	if *parallel < 1 {
		*parallel = 1
	}
	queries, err := readQueries(*queriesPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pcfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("parse dsn: %w", err)
	}
	// Только чтение, на уровне соединения: любая запись упадёт с ошибкой.
	pcfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET default_transaction_read_only = on")
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	defer pool.Close()
	meili := meilisearch.New(cfg.MeiliURL, meilisearch.WithAPIKey(cfg.MeiliAPIKey))

	base := history.PersonaProfile{}
	userID := int64(1) // любой > 0: профиль подставной, id только включает пересортировку
	if *user > 0 {
		base, err = history.New(pool).PersonaProfile(ctx, *user)
		if err != nil {
			return fmt.Errorf("профиль пользователя %d: %w", *user, err)
		}
		userID = *user
		fmt.Fprintf(os.Stderr, "профиль пользователя %d: подписок на авторов %d, активных работ %d, негативов %d\n",
			*user, len(base.FavoriteAuthors), len(base.WorkActivity),
			len(base.BlockedAuthors)+len(base.BlockedSeries)+len(base.NotInterestedWorks)+len(base.DroppedWorks))
	}

	results := make([][]result, len(queries))
	jobs := make(chan int)
	var wg sync.WaitGroup
	var done int
	var mu sync.Mutex
	errCh := make(chan error, *parallel)
	for w := 0; w < *parallel; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			src := &persona{}
			svc := books.New(pool, meili, src)
			for i := range jobs {
				rs, err := runQuery(ctx, svc, src, base, userID, queries[i], *limit, *suggestN)
				if err != nil {
					select {
					case errCh <- fmt.Errorf("%q: %w", queries[i].Q, err):
					default:
					}
					continue
				}
				mu.Lock()
				results[i] = rs
				done++
				if done%100 == 0 {
					fmt.Fprintf(os.Stderr, "%d / %d\n", done, len(queries))
				}
				mu.Unlock()
			}
		}()
	}
	for i := range queries {
		select {
		case jobs <- i:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	select {
	case err := <-errCh:
		fmt.Fprintln(os.Stderr, "предупреждение:", err)
	default:
	}

	var flat []result
	for _, rs := range results {
		flat = append(flat, rs...)
	}
	if *out != "" {
		if err := writeResults(*out, flat); err != nil {
			return err
		}
	}
	printSummary(flat, *suggestN)
	return nil
}

func readQueries(path string) ([]query, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []query
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var q query
		if err := json.Unmarshal([]byte(line), &q); err != nil {
			return nil, fmt.Errorf("queries: %q: %w", line, err)
		}
		if strings.TrimSpace(q.Q) == "" {
			continue
		}
		out = append(out, q)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("queries: пусто")
	}
	return out, nil
}

// withNegatives — профиль варианта: позитивы и реальные негативы основы плюс
// синтетический негатив по ожидаемой работе.
func withNegatives(base history.PersonaProfile, variant string, q query) history.PersonaProfile {
	p := base
	p.BlockedAuthors = cloneSet(base.BlockedAuthors)
	p.BlockedSeries = cloneSet(base.BlockedSeries)
	p.NotInterestedWorks = cloneSet(base.NotInterestedWorks)
	p.DroppedWorks = cloneSet(base.DroppedWorks)
	switch variant {
	case variantBlockedAuthor:
		p.BlockedAuthors[q.AuthorID] = struct{}{}
	case variantBlockedSeries:
		p.BlockedSeries[q.SeriesID] = struct{}{}
	case variantNotInterested:
		p.NotInterestedWorks[q.WorkID] = struct{}{}
	case variantDropped:
		p.DroppedWorks[q.WorkID] = struct{}{}
	}
	return p
}

func cloneSet(m map[int64]struct{}) map[int64]struct{} {
	out := make(map[int64]struct{}, len(m)+1)
	for k := range m {
		out[k] = struct{}{}
	}
	return out
}

func runQuery(ctx context.Context, svc *books.Service, src *persona, base history.PersonaProfile, userID int64, q query, limit, suggestN int) ([]result, error) {
	var out []result
	for _, mode := range []string{modeList, modeSuggest} {
		for _, variant := range variants {
			if variant == variantBlockedAuthor && q.AuthorID == 0 && q.WorkID != 0 ||
				variant == variantBlockedSeries && q.SeriesID == 0 && q.WorkID != 0 {
				continue
			}
			src.p = withNegatives(base, variant, q)
			var items []books.ListItem
			switch mode {
			case modeList:
				res, err := svc.ListWorks(ctx, books.ListParams{Query: q.Q, Limit: limit, UserID: userID})
				if err != nil {
					return nil, err
				}
				items = res.Items
			case modeSuggest:
				var err error
				items, err = svc.SuggestWorks(ctx, q.Q, suggestN, userID, nil, nil, false)
				if err != nil {
					return nil, err
				}
			}
			// Без ожидаемой работы — первый хит без негативов (запросы владельца).
			if variant == variantNone && q.WorkID == 0 && mode == modeList && len(items) > 0 {
				top := items[0]
				q.WorkID = top.ID
				if len(top.AuthorIDs) > 0 {
					q.AuthorID = top.AuthorIDs[0]
				}
				if top.SeriesID != nil {
					q.SeriesID = *top.SeriesID
				}
			}
			if variant == variantBlockedAuthor && q.AuthorID == 0 || variant == variantBlockedSeries && q.SeriesID == 0 {
				continue
			}
			r := result{Q: q.Q, Label: q.Label, WorkID: q.WorkID, Mode: mode, Variant: variant, Pos: -1}
			for i, it := range items {
				r.IDs = append(r.IDs, it.ID)
				if it.ID == q.WorkID {
					r.Pos = i
				}
			}
			if len(items) > 0 {
				r.Top = items[0].ID
			}
			out = append(out, r)
		}
	}
	return out, nil
}

func readResults(path string) ([]result, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []result
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r result
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("results: %q: %w", line, err)
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

func writeResults(path string, rs []result) error {
	f, err := os.Create(filepath.Clean(path)) //nolint:gosec // путь из флага CLI владельца
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, r := range rs {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return w.Flush()
}

type bucket struct {
	n, wasTop, keptTop int
	shift              [4]int // 0, 1, 2, 3
	over               int    // > 3 — нарушение предела
	lost               int    // пропала из выдачи
	sumShift           int
}

// queryKind — тип запроса из метки: текст после последнего « / » («title»,
// «author» у выгрузки search-dryrun-queries.sql), иначе «-». Запрос по имени
// автора сдвигов почти не даёт (работы автора стоят первыми и не
// пересортировываются), поэтому сводка — отдельно по типам.
func queryKind(label string) string {
	if i := strings.LastIndex(label, " / "); i >= 0 {
		return label[i+3:]
	}
	return "-"
}

// printSummary — markdown для eval-log: по режиму, типу запроса и варианту —
// сколько запросов (ожидаемая работа была в выдаче без негативов), как часто
// первая осталась первой, распределение сдвига и выпадение из выдачи.
func printSummary(rs []result, suggestN int) {
	// Ключ — запрос + ожидаемая работа + режим: один и тот же неточный запрос
	// («Страх и») бывает у разных авторов с разными ожидаемыми работами.
	key := func(r result) string { return fmt.Sprintf("%s|%d|%s", r.Q, r.WorkID, r.Mode) }
	before := map[string]int{} // → pos без негативов
	for _, r := range rs {
		if r.Variant == variantNone {
			before[key(r)] = r.Pos
		}
	}
	buckets := map[string]*bucket{}
	for _, r := range rs {
		if r.Variant == variantNone {
			continue
		}
		pb, ok := before[key(r)]
		if !ok || pb < 0 {
			continue
		}
		key := r.Mode + "|" + queryKind(r.Label) + "|" + r.Variant
		b := buckets[key]
		if b == nil {
			b = &bucket{}
			buckets[key] = b
		}
		b.n++
		if pb == 0 {
			b.wasTop++
			if r.Pos == 0 {
				b.keptTop++
			}
		}
		switch d := r.Pos - pb; {
		case r.Pos < 0:
			b.lost++
		case d <= 3:
			b.shift[max(d, 0)]++
			b.sumShift += d
		default:
			b.over++
			b.sumShift += d
		}
	}
	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("| режим | запрос | негатив | запросов | была #1 → осталась #1 | сдвиг 0 / 1 / 2 / 3 | > 3 | выпала из выдачи | средний сдвиг |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, k := range keys {
		b := buckets[k]
		parts := strings.SplitN(k, "|", 3)
		mode, kind, variant := parts[0], parts[1], parts[2]
		if mode == modeSuggest {
			mode = fmt.Sprintf("suggest (%d)", suggestN)
		}
		found := b.n - b.lost
		mean := 0.0
		if found > 0 {
			mean = float64(b.sumShift) / float64(found)
		}
		fmt.Printf("| %s | %s | %s | %d | %d → %d | %d / %d / %d / %d | %d | %d | %.2f |\n",
			mode, kind, variant, b.n, b.wasTop, b.keptTop, b.shift[0], b.shift[1], b.shift[2], b.shift[3], b.over, b.lost, mean)
	}
}
