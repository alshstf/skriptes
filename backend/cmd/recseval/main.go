// recseval — харнесс оценки рекомендаций (план recsys/01, задача 1.5):
// leave-last-out по пользователям инстанса. У каждого пользователя прячутся
// k последних «понравившихся» работ (оценка ≥ 4 или дочитано), профиль
// строится по остальным сигналам, и каждый рекомендатель должен найти
// спрятанное: hit@10, hit@50, MRR, покрытие каталога, новизна (доля
// рекомендаций вне топ-5 % известности). Только чтение: ни БД, ни индекс не
// меняются — можно гонять на проде (docker compose exec -T backend recseval).
//
//	recseval [-k 3] [-n 50] [-users 1,2] [-min-positives 4] [-seed 1] [-out results.jsonl] [-sanity sanity.csv]
//
// DSN и Meili — из окружения бекенда (SKRIPTES_DATABASE_URL, SKRIPTES_MEILI_*).
// -out — строка JSON на пару (пользователь, рекомендатель): спрятанное, ранги,
// список; личные данные (id пользователей) — файл в репо не класть
// (~/projects/plans/skriptes/recsys/results/, в .gitignore).
// -sanity — CSV `user_id,expected_work_id,label`: без прятанья, по полному
// профилю — на каком месте ожидаемая работа у каждого рекомендателя.
// Итог — markdown-таблица в stdout для recsys/eval-log.md.
package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/meilisearch/meilisearch-go"
	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/config"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/skriptes/skriptes/backend/internal/recsys"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type result struct {
	UserID      int64   `json:"user_id"`
	Recommender string  `json:"recommender"`
	Hidden      []int64 `json:"hidden"`
	Ranks       []int   `json:"ranks"`
	Recs        []int64 `json:"recs"`
}

func run() error {
	k := flag.Int("k", 3, "сколько последних положительных работ прятать")
	n := flag.Int("n", 50, "длина списка рекомендаций")
	usersFlag := flag.String("users", "", "id пользователей через запятую (пусто — все с достаточным числом сигналов)")
	minPos := flag.Int("min-positives", 0, "минимум положительных работ у пользователя (0 = k+1)")
	seed := flag.Int64("seed", 1, "seed бейзлайна «случайное»")
	out := flag.String("out", "", "JSONL с результатами по пользователям")
	sanity := flag.String("sanity", "", "CSV user_id,expected_work_id,label — sanity-прогон без прятанья")
	flag.Parse()
	if *minPos <= 0 {
		*minPos = *k + 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	meili := meilisearch.New(cfg.MeiliURL, meilisearch.WithAPIKey(cfg.MeiliAPIKey))
	bs := books.New(pool, meili, nil)

	recs := []recsys.Recommender{
		recsys.PopularityRecommender{Top: bs.TopPopularWorkIDs},
		recsys.SameAuthorRecommender{Q: pool},
		recsys.RandomRecommender{Q: pool, Seed: *seed},
	}

	catalog, err := bs.VisibleWorksCount(ctx)
	if err != nil {
		return err
	}
	// «Популярный топ» для новизны — верхние 5 % каталога по известности.
	popular := map[int64]struct{}{}
	topN := int(catalog / 20)
	for offset := 0; offset < topN; offset += 1000 {
		page, err := bs.TopPopularWorkIDs(ctx, offset, min(1000, topN-offset))
		if err != nil {
			return err
		}
		if len(page) == 0 {
			break
		}
		for _, id := range page {
			popular[id] = struct{}{}
		}
	}

	if *sanity != "" {
		return runSanity(ctx, pool, recs, *sanity, *n)
	}

	users, err := pickUsers(ctx, pool, *usersFlag)
	if err != nil {
		return err
	}
	var w *bufio.Writer
	if *out != "" {
		f, err := os.Create(filepath.Clean(*out)) //nolint:gosec // путь из флага CLI владельца
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		w = bufio.NewWriter(f)
		defer func() { _ = w.Flush() }()
	}
	metrics := map[string]*recsys.Metrics{}
	for _, r := range recs {
		metrics[r.Name()] = recsys.NewMetrics()
	}
	evaluated, skipped := 0, 0
	for _, uid := range users {
		signals, err := recsys.LoadSignals(ctx, pool, uid)
		if err != nil {
			return fmt.Errorf("user %d: %w", uid, err)
		}
		positives := 0
		for _, s := range signals {
			if _, ok := recsys.PositiveAt(s); ok {
				positives++
			}
		}
		if positives < *minPos {
			skipped++
			continue
		}
		hidden, rest := recsys.LeaveLastOut(signals, *k)
		p, err := recsys.ProfileFromSignals(ctx, pool, uid, rest, time.Now(), recsys.DefaultWeights)
		if err != nil {
			return fmt.Errorf("user %d: %w", uid, err)
		}
		evaluated++
		for _, r := range recs {
			list, err := r.Recommend(ctx, p, *n)
			if err != nil {
				return fmt.Errorf("user %d %s: %w", uid, r.Name(), err)
			}
			metrics[r.Name()].Add(list, hidden, popular)
			if w != nil {
				ranks := make([]int, len(hidden))
				for i, h := range hidden {
					ranks[i] = recsys.Rank(list, h)
				}
				if err := json.NewEncoder(w).Encode(result{UserID: uid, Recommender: r.Name(), Hidden: hidden, Ranks: ranks, Recs: list}); err != nil {
					return err
				}
			}
		}
	}

	fmt.Printf("Каталог: %d видимых работ; популярный топ (5 %%): %d. Пользователей: %d оценено, %d пропущено (меньше %d положительных). k=%d, n=%d.\n\n",
		catalog, len(popular), evaluated, skipped, *minPos, *k, *n)
	fmt.Println("| Рекомендатель | Пользователей | Спрятано | hit@10 | hit@50 | MRR | Покрытие | Новизна |")
	fmt.Println("|---|---|---|---|---|---|---|---|")
	for _, r := range recs {
		s := metrics[r.Name()].Summary(catalog)
		fmt.Printf("| %s | %d | %d | %.2f | %.2f | %.3f | %.4f | %.2f |\n",
			r.Name(), s.Users, s.Hidden, s.Hit10, s.Hit50, s.MRR, s.Coverage, s.Novelty)
	}
	return nil
}

// pickUsers — явный список или все пользователи инстанса.
func pickUsers(ctx context.Context, pool recsys.Querier, flagValue string) ([]int64, error) {
	if flagValue != "" {
		var out []int64
		for _, s := range strings.Split(flagValue, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("-users: %q", s)
			}
			out = append(out, id)
		}
		return out, nil
	}
	rows, err := pool.Query(ctx, `SELECT id FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// runSanity — пары «пользователь → ожидаемая работа» по полному профилю.
func runSanity(ctx context.Context, pool recsys.Querier, recs []recsys.Recommender, path string, n int) error {
	f, err := os.Open(filepath.Clean(path)) //nolint:gosec // путь из флага CLI владельца
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.Comment = '#'
	fmt.Printf("| Пользователь | Ожидаю | Метка | %s |\n|---|---|---|%s|\n", strings.ReplaceAll(recsys.Names(recs), ", ", " | "), strings.Repeat("---|", len(recs)))
	profiles := map[int64]*recsys.Profile{}
	lists := map[[2]any][]int64{}
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if len(rec) < 2 {
			continue
		}
		uid, err1 := strconv.ParseInt(strings.TrimSpace(rec[0]), 10, 64)
		wid, err2 := strconv.ParseInt(strings.TrimSpace(rec[1]), 10, 64)
		if err1 != nil || err2 != nil {
			continue // заголовок или мусор
		}
		label := ""
		if len(rec) > 2 {
			label = rec[2]
		}
		p, ok := profiles[uid]
		if !ok {
			if p, err = recsys.BuildProfile(ctx, pool, uid, time.Now(), recsys.DefaultWeights); err != nil {
				return err
			}
			profiles[uid] = p
		}
		cells := make([]string, len(recs))
		for i, rc := range recs {
			key := [2]any{uid, rc.Name()}
			list, ok := lists[key]
			if !ok {
				if list, err = rc.Recommend(ctx, p, n); err != nil {
					return err
				}
				lists[key] = list
			}
			if rank := recsys.Rank(list, wid); rank > 0 {
				cells[i] = strconv.Itoa(rank)
			} else {
				cells[i] = "—"
			}
		}
		fmt.Printf("| %d | %d | %s | %s |\n", uid, wid, label, strings.Join(cells, " | "))
	}
	return nil
}
