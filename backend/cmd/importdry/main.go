// Command importdry — сухой прогон импорта истории чтения (план recsys/04,
// приёмка 4.3): разбирает файл выгрузки и сопоставляет его с каталогом тем же
// histimport.Matcher, что и профиль → «Импорт», ничего не записывая (соединение
// только на чтение). Печатает доли high/medium/none, время и списки для глаз:
// каждое «уверенно» — файл → каталог (ложные high ищутся здесь), спорные — с
// кандидатами, ненайденные.
//
//	importdry -file goodreads_library_export.csv [-source goodreads] [-user 1]
//
// На проде — одноразовым контейнером образа backend (env и сеть сервиса), как
// cmd/searchdry: ~/projects/plans/skriptes/tools/import-dryrun.sh.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meilisearch/meilisearch-go"
	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/config"
	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/skriptes/skriptes/backend/internal/recsys/histimport"
	"github.com/skriptes/skriptes/backend/internal/settings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	file := flag.String("file", "", "файл выгрузки (обязателен)")
	source := flag.String("source", "", "kindle | calibre | goodreads | csv (пусто — по содержимому)")
	user := flag.Int64("user", 0, "id пользователя — его скрытые жанры и языки (0 — без исключений)")
	noSearch := flag.Bool("no-search", false, "без запасного поиска по названию (Meili)")
	flag.Parse()
	if *file == "" {
		return errors.New("-file обязателен")
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

	f, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	t0 := time.Now()
	res, err := histimport.Parse(f, histimport.Source(*source))
	if err != nil {
		return err
	}
	parsed := time.Since(t0)

	m := &histimport.Matcher{DB: pool}
	if !*noSearch {
		m.Search = books.New(pool, meilisearch.New(cfg.MeiliURL, meilisearch.WithAPIKey(cfg.MeiliAPIKey)), nil)
	}
	var ex recsys.Exclusions
	if *user > 0 {
		ex.Genres, ex.Langs, ex.HideCompilations = settings.NewContentResolver(settings.New(pool)).Exclusions(ctx, *user)
	}
	t1 := time.Now()
	matches, err := m.Match(ctx, res.Items, ex)
	if err != nil {
		return err
	}
	matched := time.Since(t1)

	n := map[histimport.Confidence]int{}
	for _, mt := range matches {
		n[mt.Confidence]++
	}
	total := len(res.Items)
	pct := func(k histimport.Confidence) string {
		return fmt.Sprintf("%d (%.0f %%)", n[k], 100*float64(n[k])/float64(max(total, 1)))
	}
	fmt.Printf("# Сухой прогон импорта: %s (%s)\n\n", *file, res.Source)
	fmt.Printf("книг в файле %d, пропущено строк %d; разбор %s, сопоставление %s\n\n", total, res.SkippedTotal,
		parsed.Round(time.Millisecond), matched.Round(time.Millisecond))
	fmt.Printf("| уверенно | на выбор | не нашлось |\n|---|---|---|\n| %s | %s | %s |\n\n",
		pct(histimport.ConfidenceHigh), pct(histimport.ConfidenceMedium), pct(histimport.ConfidenceNone))

	section := func(title string, conf histimport.Confidence) {
		fmt.Printf("## %s\n\n", title)
		for i, mt := range matches {
			if mt.Confidence != conf {
				continue
			}
			it := res.Items[i]
			fmt.Printf("- %s — %s\n", it.Title, strings.Join(it.Authors, ", "))
			for _, c := range mt.Candidates {
				year := ""
				if c.Year != nil {
					year = fmt.Sprintf(", %d", *c.Year)
				}
				fmt.Printf("  - → [%d] %s — %s%s\n", c.WorkID, c.Title, strings.Join(c.Authors, ", "), year)
			}
		}
		fmt.Println()
	}
	section("Уверенно (проверить глазами: ложных быть не должно)", histimport.ConfidenceHigh)
	section("На выбор", histimport.ConfidenceMedium)
	section("Не нашлось", histimport.ConfidenceNone)
	if res.SkippedTotal > 0 {
		fmt.Println("## Пропущено")
		for _, s := range res.Skipped {
			fmt.Printf("- строка %d: %s\n", s.Line, s.Reason)
		}
	}
	return nil
}
