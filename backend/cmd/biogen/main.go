// biogen — офлайн-генератор датасета био-таймлайна (internal/bio).
//
// Подкоманды (каждая — свой набор флагов, `biogen <cmd> -h`):
//
//	fetch   -qid Q991 [-out work/]          → work/sources/Q991.json (Wikidata-скелет + текст Википедии)
//	extract -qid Q991 [-out work/] [-engine claude-cli|api] [-model] [-effort]
//	                                         → work/draft/Q991.json + raw ответ. По умолчанию — headless Claude Code
//	                                           (`claude -p`, токены по подписке, ключ не нужен); -engine api — Anthropic API
//	                                           (нужен ANTHROPIC_API_KEY).
//	verify  -qid Q991 [-out work/]          → work/verified/Q991.json + work/review/Q991.md
//	run     -qid Q991[,Q7243] [-out work/]  → fetch + extract + verify подряд
//	eval    -generated work/verified [-golden internal/bio/data/authors]
//	check   [-dir internal/bio/data]        → валидация датасета (то же, что делает загрузчик)
//
// Приложение внешних вызовов не делает и ключей не требует: извлечение —
// офлайн, через Claude Code по подписке или (опционально) через API. Принятый
// файл кладут в internal/bio/data/authors/<QID>.json руками после ревью.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/skriptes/skriptes/backend/internal/bio"
	"github.com/skriptes/skriptes/backend/internal/bio/gen"
)

func main() {
	os.Exit(run())
}

// run — код выхода; отдельной функцией, чтобы defer cancel() отработал до os.Exit.
func run() int {
	if len(os.Args) < 2 {
		usage()
		return 2
	}
	// Без общего таймаута: пачка из десятков авторов идёт часами; лимит — на
	// один вызов модели (CLIExtractor.Timeout / таймаут SDK-клиента).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var err error
	switch os.Args[1] {
	case "fetch":
		err = cmdFetch(ctx, os.Args[2:])
	case "extract":
		err = cmdExtract(ctx, os.Args[2:])
	case "verify":
		err = cmdVerify(os.Args[2:])
	case "run":
		err = cmdRun(ctx, os.Args[2:])
	case "eval":
		err = cmdEval(os.Args[2:])
	case "check":
		err = cmdCheck(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "biogen:", err)
		return 1
	}
	return 0
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: biogen <fetch|extract|verify|run|eval|check> [flags]")
}

// qids — список через запятую или @файл (QID в начале каждой строки, остальное
// после табуляции/пробела — комментарий; пустые и # — пропуск).
func qids(s string) []string {
	var out []string
	if strings.HasPrefix(s, "@") {
		raw, err := os.ReadFile(filepath.Clean(s[1:])) //nolint:gosec // путь из флага CLI владельца
		if err != nil {
			fmt.Fprintln(os.Stderr, "biogen:", err)
			return nil
		}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			out = append(out, strings.FieldsFunc(line, func(r rune) bool { return r == '\t' || r == ' ' || r == ',' })[0])
		}
		return out
	}
	for _, q := range strings.Split(s, ",") {
		if q = strings.TrimSpace(q); q != "" {
			out = append(out, q)
		}
	}
	return out
}

func httpClient() *http.Client { return &http.Client{Timeout: 90 * time.Second} }

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(filepath.Clean(path)) //nolint:gosec // путь из флагов CLI владельца, не из сети
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func cmdFetch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	qid := fs.String("qid", "", "QID автора (или список через запятую)")
	out := fs.String("out", "work", "рабочая папка")
	langs := fs.String("langs", "", "языки статей через запятую (ru,en,de); пусто — ru, en при короткой ru, язык оригинала (не ru/en) из Wikidata")
	_ = fs.Parse(args)
	if *qid == "" {
		return errors.New("нужен -qid")
	}
	f := gen.NewFetcher(httpClient())
	f.Langs = qids(*langs)
	for _, q := range qids(*qid) {
		src, err := f.Fetch(ctx, q)
		if err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
		p := filepath.Join(*out, "sources", q+".json")
		if err := writeJSON(p, src); err != nil {
			return err
		}
		fmt.Printf("%s: %s, фактов %d, статей %d → %s\n", q, src.Label, len(src.Facts), len(src.Articles), p)
	}
	return nil
}

// extractor — движок извлечения. claude-cli: `claude -p` (модель и effort —
// умолчания CLI, если не заданы); api: Anthropic SDK, нужен ключ.
func extractor(engine, binary, model, effort string) (gen.Engine, error) {
	switch engine {
	case "claude-cli", "":
		return &gen.CLIExtractor{Binary: binary, Model: model, Effort: effort}, nil
	case "api":
		if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("ANTHROPIC_AUTH_TOKEN") == "" {
			return nil, errors.New("engine api: нужен ANTHROPIC_API_KEY (или профиль ant auth login)")
		}
		if model == "" {
			model = gen.DefaultModel
		}
		if effort == "" {
			effort = "high"
		}
		return &gen.Extractor{
			Client: anthropic.NewClient(option.WithRequestTimeout(10 * time.Minute)),
			Model:  model,
			Effort: anthropic.OutputConfigEffort(effort),
		}, nil
	}
	return nil, fmt.Errorf("неизвестный -engine %q (claude-cli|api)", engine)
}

func engineFlags(fs *flag.FlagSet) (engine, binary, model, effort *string) {
	engine = fs.String("engine", "claude-cli", "движок извлечения: claude-cli (headless Claude Code, по подписке) | api (Anthropic API, ключ)")
	binary = fs.String("claude", "claude", "бинарь claude для -engine claude-cli")
	model = fs.String("model", "", "модель (пусто — умолчание движка; api: "+gen.DefaultModel+")")
	effort = fs.String("effort", "", "effort: low|medium|high|xhigh|max (пусто — умолчание движка; api: high)")
	return
}

func extractOne(ctx context.Context, x gen.Engine, out, q string) error {
	var src gen.Sources
	if err := readJSON(filepath.Join(out, "sources", q+".json"), &src); err != nil {
		return fmt.Errorf("%s: сначала fetch: %w", q, err)
	}
	var prev *bio.Author
	var pa bio.Author
	// Путь относительный: из backend/ автор датасета извлекается как регенерация
	// (модели подаётся прежний файл), из папки прогона — с нуля. Для сравнения
	// моделей запускать не из backend/ — иначе модель повторит готовый файл.
	if pp := filepath.Join("internal", "bio", "data", "authors", q+".json"); readJSON(pp, &pa) == nil {
		prev = &pa
		fmt.Printf("%s: регенерация — модели подан прежний файл %s\n", q, pp)
	}
	a, raw, err := x.Extract(ctx, &src, prev)
	if raw != "" {
		_ = os.MkdirAll(filepath.Join(out, "raw"), 0o750)
		_ = os.WriteFile(filepath.Join(out, "raw", q+".json"), []byte(raw), 0o600)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", q, err)
	}
	if gen.NoEvents(a) {
		// Разовый сбой модели (главы и работы есть, вех нет) — ошибка, run повторит.
		return fmt.Errorf("%s: модель вернула ответ без вех", q)
	}
	p := filepath.Join(out, "draft", q+".json")
	if err := writeJSON(p, a); err != nil {
		return err
	}
	fmt.Printf("%s: вех %d, работ %d, глав %d → %s\n", q, len(a.Events), len(a.Works), len(a.Chapters), p)
	return nil
}

func cmdExtract(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("extract", flag.ExitOnError)
	qid := fs.String("qid", "", "QID автора (или список)")
	out := fs.String("out", "work", "рабочая папка")
	engine, binary, model, effort := engineFlags(fs)
	_ = fs.Parse(args)
	if *qid == "" {
		return errors.New("нужен -qid")
	}
	x, err := extractor(*engine, *binary, *model, *effort)
	if err != nil {
		return err
	}
	for _, q := range qids(*qid) {
		if err := extractOne(ctx, x, *out, q); err != nil {
			return err
		}
	}
	return nil
}

func verifyOne(out, q string) error {
	var src gen.Sources
	if err := readJSON(filepath.Join(out, "sources", q+".json"), &src); err != nil {
		return fmt.Errorf("%s: нет источников: %w", q, err)
	}
	var a bio.Author
	if err := readJSON(filepath.Join(out, "draft", q+".json"), &a); err != nil {
		return fmt.Errorf("%s: нет черновика: %w", q, err)
	}
	v, flags := gen.Verify(&a, &src)
	if err := writeJSON(filepath.Join(out, "verified", q+".json"), v); err != nil {
		return err
	}
	rp := filepath.Join(out, "review", q+".md")
	if err := os.MkdirAll(filepath.Dir(rp), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(rp, []byte(gen.ReviewReport(v, flags, &src)), 0o600); err != nil {
		return err
	}
	dropped := 0
	for _, f := range flags {
		if f.Dropped {
			dropped++
		}
	}
	fmt.Printf("%s: принято %d вех, выброшено %d, замечаний %d → %s\n", q, len(v.Events), dropped, len(flags)-dropped, rp)
	return nil
}

func cmdVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	qid := fs.String("qid", "", "QID автора (или список)")
	out := fs.String("out", "work", "рабочая папка")
	_ = fs.Parse(args)
	if *qid == "" {
		return errors.New("нужен -qid")
	}
	for _, q := range qids(*qid) {
		if err := verifyOne(*out, q); err != nil {
			return err
		}
	}
	return nil
}

func cmdRun(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	qid := fs.String("qid", "", "QID автора (или список)")
	out := fs.String("out", "work", "рабочая папка")
	langs := fs.String("langs", "", "языки статей через запятую (ru,en,de); пусто — ru, en при короткой ru, язык оригинала (не ru/en) из Wikidata")
	retries := fs.Int("retries", 2, "повторов извлечения после сбоя (лимит подписки, сеть)")
	retryWait := fs.Duration("retry-wait", 10*time.Minute, "пауза перед повтором")
	engine, binary, model, effort := engineFlags(fs)
	_ = fs.Parse(args)
	if *qid == "" {
		return errors.New("нужен -qid")
	}
	x, err := extractor(*engine, *binary, *model, *effort)
	if err != nil {
		return err
	}
	f := gen.NewFetcher(httpClient())
	f.Langs = qids(*langs)
	// Пачка: ошибка одного автора не останавливает остальных (пишется в
	// <out>/failed.txt), готовые (есть verified/<QID>.json) пропускаются —
	// повторный запуск той же команды доделывает хвост. Сбой извлечения
	// (лимит подписки, сеть) повторяется после паузы.
	var failed []string
	for _, q := range qids(*qid) {
		if _, err := os.Stat(filepath.Join(*out, "verified", q+".json")); err == nil {
			fmt.Printf("%s: уже есть verified, пропуск\n", q)
			continue
		}
		err := runOne(ctx, f, x, *out, q, *retries, *retryWait)
		if err != nil {
			fmt.Fprintf(os.Stderr, "biogen: %s: %v\n", q, err)
			failed = append(failed, q+"\t"+err.Error())
			_ = os.MkdirAll(*out, 0o750)
			_ = os.WriteFile(filepath.Join(*out, "failed.txt"), []byte(strings.Join(failed, "\n")+"\n"), 0o600)
		}
		if ctx.Err() != nil {
			break
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("не удалось %d из %d, см. %s", len(failed), len(qids(*qid)), filepath.Join(*out, "failed.txt"))
	}
	return nil
}

func runOne(ctx context.Context, f *gen.Fetcher, x gen.Engine, out, q string, retries int, wait time.Duration) error {
	src, err := f.Fetch(ctx, q)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	if err := writeJSON(filepath.Join(out, "sources", q+".json"), src); err != nil {
		return err
	}
	if !gen.HasBirthDate(src) {
		// Не ошибка: повтор раунда ничего не изменит, а модель стоит денег.
		fmt.Printf("%s: пропуск — в Wikidata нет даты рождения (%q), таймлайн не строится\n", q, src.Born)
		return nil
	}
	for attempt := 0; ; attempt++ {
		err = extractOne(ctx, x, out, q)
		if err == nil || attempt >= retries || ctx.Err() != nil {
			break
		}
		fmt.Fprintf(os.Stderr, "biogen: %s: extract: %v — повтор через %s (%d/%d)\n", q, err, wait, attempt+1, retries)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	return verifyOne(out, q)
}

func cmdEval(args []string) error {
	fs := flag.NewFlagSet("eval", flag.ExitOnError)
	generated := fs.String("generated", filepath.Join("work", "verified"), "папка сгенерированных файлов")
	golden := fs.String("golden", filepath.Join("internal", "bio", "data", "authors"), "папка золотого набора")
	_ = fs.Parse(args)
	entries, err := os.ReadDir(*generated)
	if err != nil {
		return err
	}
	var results []gen.EvalResult
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var g, gold bio.Author
		if err := readJSON(filepath.Join(*generated, e.Name()), &g); err != nil {
			return err
		}
		if err := readJSON(filepath.Join(*golden, e.Name()), &gold); err != nil {
			fmt.Printf("%s: нет золотого файла, пропуск\n", e.Name())
			continue
		}
		results = append(results, gen.Eval(&gold, &g))
	}
	fmt.Print(gen.Report(results))
	return nil
}

func cmdCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	dir := fs.String("dir", filepath.Join("internal", "bio", "data"), "папка датасета")
	_ = fs.Parse(args)
	ds, err := bio.LoadFS(os.DirFS(filepath.Dir(*dir)), filepath.Base(*dir))
	if err != nil {
		return err
	}
	warns := 0
	for qid, a := range ds.Authors {
		for _, p := range bio.Validate(a) {
			if p.Severity == "warn" {
				warns++
				fmt.Printf("%s: %s\n", qid, p)
			}
		}
	}
	fmt.Printf("ок: авторов %d, регионов фона %d, версия %s, предупреждений %d\n", len(ds.Authors), len(ds.Context), ds.Version, warns)
	return nil
}
