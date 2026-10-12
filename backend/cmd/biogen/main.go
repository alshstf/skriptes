// biogen — офлайн-генератор датасета био-таймлайна (internal/bio).
//
// Подкоманды (каждая — свой набор флагов, `biogen <cmd> -h`):
//
//	fetch   -qid Q991 [-out work/]          → work/sources/Q991.json (Wikidata-скелет + текст Википедии)
//	extract -qid Q991 [-out work/] [-model] → work/draft/Q991.json + raw ответ (нужен ANTHROPIC_API_KEY)
//	verify  -qid Q991 [-out work/]          → work/verified/Q991.json + work/review/Q991.md
//	run     -qid Q991[,Q7243] [-out work/]  → fetch + extract + verify подряд
//	eval    -generated work/verified [-golden internal/bio/data/authors]
//	check   [-dir internal/bio/data]        → валидация датасета (то же, что делает загрузчик)
//
// Ключ API — только здесь, в окружении владельца; приложение внешних вызовов
// не делает. Принятый файл кладут в internal/bio/data/authors/<QID>.json
// руками после ревью.
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
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

func qids(s string) []string {
	var out []string
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
	_ = fs.Parse(args)
	if *qid == "" {
		return errors.New("нужен -qid")
	}
	f := gen.NewFetcher(httpClient())
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

func extractor(model, effort string) (*gen.Extractor, error) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("ANTHROPIC_AUTH_TOKEN") == "" {
		return nil, errors.New("нужен ANTHROPIC_API_KEY (или профиль ant auth login)")
	}
	return &gen.Extractor{
		Client: anthropic.NewClient(option.WithRequestTimeout(10 * time.Minute)),
		Model:  model,
		Effort: anthropic.OutputConfigEffort(effort),
	}, nil
}

func extractOne(ctx context.Context, x *gen.Extractor, out, q string) error {
	var src gen.Sources
	if err := readJSON(filepath.Join(out, "sources", q+".json"), &src); err != nil {
		return fmt.Errorf("%s: сначала fetch: %w", q, err)
	}
	var prev *bio.Author
	var pa bio.Author
	if err := readJSON(filepath.Join("internal", "bio", "data", "authors", q+".json"), &pa); err == nil {
		prev = &pa
	}
	a, raw, err := x.Extract(ctx, &src, prev)
	if raw != "" {
		_ = os.MkdirAll(filepath.Join(out, "raw"), 0o750)
		_ = os.WriteFile(filepath.Join(out, "raw", q+".json"), []byte(raw), 0o600)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", q, err)
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
	model := fs.String("model", gen.DefaultModel, "модель")
	effort := fs.String("effort", "high", "effort: low|medium|high|xhigh|max")
	_ = fs.Parse(args)
	if *qid == "" {
		return errors.New("нужен -qid")
	}
	x, err := extractor(*model, *effort)
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
	model := fs.String("model", gen.DefaultModel, "модель")
	effort := fs.String("effort", "high", "effort")
	_ = fs.Parse(args)
	if *qid == "" {
		return errors.New("нужен -qid")
	}
	x, err := extractor(*model, *effort)
	if err != nil {
		return err
	}
	f := gen.NewFetcher(httpClient())
	for _, q := range qids(*qid) {
		src, err := f.Fetch(ctx, q)
		if err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
		if err := writeJSON(filepath.Join(*out, "sources", q+".json"), src); err != nil {
			return err
		}
		if err := extractOne(ctx, x, *out, q); err != nil {
			return err
		}
		if err := verifyOne(*out, q); err != nil {
			return err
		}
	}
	return nil
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
