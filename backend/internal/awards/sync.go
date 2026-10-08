package awards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/metadata"
)

// Лауреаты с Фантлаба: GET /award/{id}?include_contests=1 — все конкурсы премии с
// лауреатами по номинациям (contest_works, cw_winner). Один запрос на премию.
// Другие источники — wikidata.go и manual.go.

const fantlabAPI = "https://api.fantlab.ru"

// Syncer загружает лауреатов премий белого списка и сопоставляет их с каталогом.
type Syncer struct {
	pool      *pgxpool.Pool
	client    *http.Client
	baseURL   string // API Фантлаба
	sparqlURL string // Wikidata Query Service
	logger    *slog.Logger
	pause     time.Duration
	// onWorks — работы, у которых изменился набор премий (новые связи, снятые,
	// удалённые лауреаты): их документы в works-индексе переписываются (#447).
	onWorks func(context.Context, []int64) error
	changed map[int64]bool
}

// WithWorksChanged — колбэк для работ с изменившимся набором премий (переиндексация).
func (s *Syncer) WithWorksChanged(fn func(context.Context, []int64) error) *Syncer {
	s.onWorks = fn
	return s
}

// noteWork — запомнить работу для переиндексации.
func (s *Syncer) noteWork(id *int64) {
	if id == nil {
		return
	}
	if s.changed == nil {
		s.changed = map[int64]bool{}
	}
	s.changed[*id] = true
}

// flushWorks — отдать накопленные работы колбэку (сбой — в лог, повторит следующий шаг).
func (s *Syncer) flushWorks(ctx context.Context) {
	if s.onWorks == nil || len(s.changed) == 0 {
		s.changed = nil
		return
	}
	ids := make([]int64, 0, len(s.changed))
	for id := range s.changed {
		ids = append(ids, id)
	}
	if err := s.onWorks(ctx, ids); err != nil {
		s.logger.Warn("awards: reindex works failed", "works", len(ids), "err", err)
		return
	}
	s.changed = nil
}

// NewSyncer — загрузчик: HTTP-клиент с прерывателем по хосту (грабля №20).
func NewSyncer(pool *pgxpool.Pool, logger *slog.Logger) *Syncer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Syncer{pool: pool, client: metadata.SourceHTTPClient(60 * time.Second), baseURL: fantlabAPI,
		sparqlURL: sparqlEndpoint, logger: logger, pause: time.Second}
}

// WithEndpoint — другой адрес API Фантлаба и SPARQL (тесты): base и base+"/sparql".
func (s *Syncer) WithEndpoint(base string, client *http.Client) *Syncer {
	s.baseURL = strings.TrimRight(base, "/")
	s.sparqlURL = s.baseURL + "/sparql"
	if client != nil {
		s.client = client
	}
	s.pause = 0
	return s
}

type flAward struct {
	Contests []struct {
		NameYear     int `json:"nameyear"`
		ContestWorks []struct {
			ContestWorkID int64  `json:"contest_work_id"`
			Winner        int    `json:"cw_winner"`
			LinkType      string `json:"cw_link_type"`
			LinkID        int64  `json:"cw_link_id"`
			NominationID  *int   `json:"nomination_id"`
			NominationNo  int    `json:"nomination_number"`
			NominationRu  string `json:"nomination_rusname"`
			Nomination    string `json:"nomination_name"`
			AutorRusname  string `json:"autor_rusname"`
			WorkRusname   string `json:"work_rusname"`
			CwRusname     string `json:"cw_rusname"`
			CwName        string `json:"cw_name"`
		} `json:"contest_works"`
	} `json:"contests"`
}

// win — лауреат до записи в базу.
type win struct {
	year       int
	nomination string
	nomOrder   int
	kind       string // work | author
	title      string
	origTitle  string
	author     string
	ref        string
	link       string // страница у источника: work123 / autor45 (Фантлаб), QID (Wikidata), адрес (manual)
}

// fetch — лауреаты премии из её источника.
func (s *Syncer) fetch(ctx context.Context, a Award) ([]win, error) {
	switch a.source() {
	case sourceManual:
		return manualWins(a)
	case sourceWikidata:
		return s.fetchWikidata(ctx, a)
	default:
		return s.fetchFantlab(ctx, a)
	}
}

func (s *Syncer) fetchFantlab(ctx context.Context, a Award) ([]win, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/award/%d?include_contests=1&sort=contest", s.baseURL, a.FantlabID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fantlab award %d: %w", a.FantlabID, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fantlab award %d: status %d", a.FantlabID, resp.StatusCode)
	}
	var d flAward
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, fmt.Errorf("fantlab award %d: decode: %w", a.FantlabID, err)
	}
	var out []win
	for _, c := range d.Contests {
		for _, w := range c.ContestWorks {
			if w.Winner != 1 || c.NameYear <= 0 {
				continue
			}
			nom := 0
			if w.NominationID != nil {
				nom = *w.NominationID
			}
			if !a.allows(c.NameYear, nom) {
				continue
			}
			x := win{year: c.NameYear, nomination: firstNonEmpty(w.NominationRu, w.Nomination), nomOrder: w.NominationNo,
				ref: strconv.FormatInt(w.ContestWorkID, 10)}
			if w.LinkID > 0 {
				x.link = w.LinkType + strconv.FormatInt(w.LinkID, 10)
			}
			switch {
			case w.LinkType == "work":
				x.kind, x.title, x.author = "work", strings.TrimSpace(w.WorkRusname), strings.TrimSpace(w.AutorRusname)
				if x.title == "" {
					continue
				}
				if orig := quoted(w.CwName); orig != "" && orig != x.title {
					x.origTitle = orig
				}
			case w.LinkType == "autor" && a.AuthorLevel:
				x.kind, x.author = "author", firstNonEmpty(w.AutorRusname, w.CwRusname, w.CwName)
				if x.author == "" {
					continue
				}
			default:
				continue
			}
			out = append(out, x)
		}
	}
	return out, nil
}

// quoted — название из «Автор "Название"» (cw_name: автор и название в оригинале).
func quoted(s string) string {
	i, j := strings.IndexByte(s, '"'), strings.LastIndexByte(s, '"')
	if i < 0 || j <= i {
		return ""
	}
	return strings.TrimSpace(s[i+1 : j])
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x = strings.TrimSpace(x); x != "" {
			return x
		}
	}
	return ""
}

// SyncAll загружает все премии белого списка и заново сопоставляет лауреатов с
// каталогом. Сбой одной премии не останавливает остальные (её прежние данные остаются).
func (s *Syncer) SyncAll(ctx context.Context) (total, matched int, err error) {
	failed := 0
	for i, a := range Catalog {
		if i > 0 && s.pause > 0 && a.source() != sourceManual {
			select {
			case <-ctx.Done():
				return 0, 0, ctx.Err()
			case <-time.After(s.pause):
			}
		}
		wins, err := s.fetch(ctx, a)
		if err != nil {
			failed++
			if !errors.Is(err, metadata.ErrSourcePaused) { // пауза источника — без строки на каждую премию
				s.logger.Warn("awards: fetch failed", "award", a.Key, "err", err)
			}
			continue
		}
		if err := s.store(ctx, a, wins); err != nil {
			return 0, 0, err
		}
	}
	if failed == len(Catalog) {
		return 0, 0, fmt.Errorf("awards: all fetches failed")
	}
	if err := s.Match(ctx); err != nil {
		return 0, 0, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE work_id IS NOT NULL OR author_id IS NOT NULL)
		FROM award_wins`).Scan(&total, &matched); err != nil {
		return 0, 0, err
	}
	return total, matched, nil
}

// store — лауреаты премии: новые и изменённые записываются, исчезнувшие у
// источника (или отсечённые белым списком) удаляются.
func (s *Syncer) store(ctx context.Context, a Award, wins []win) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	refs := make([]string, 0, len(wins))
	for _, w := range wins {
		refs = append(refs, w.ref)
		if _, err := tx.Exec(ctx, `
			INSERT INTO award_wins (award, year, nomination, nomination_order, kind, title, orig_title, author,
			                        source, source_ref, source_link)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $11, $9, $10)
			ON CONFLICT (source, source_ref) DO UPDATE SET
				award = EXCLUDED.award, year = EXCLUDED.year, nomination = EXCLUDED.nomination,
				nomination_order = EXCLUDED.nomination_order, kind = EXCLUDED.kind, title = EXCLUDED.title,
				orig_title = EXCLUDED.orig_title, author = EXCLUDED.author, source_link = EXCLUDED.source_link
			WHERE (award_wins.award, award_wins.year, award_wins.nomination, award_wins.nomination_order, award_wins.kind,
			       award_wins.title, award_wins.orig_title, award_wins.author, award_wins.source_link)
			      IS DISTINCT FROM (EXCLUDED.award, EXCLUDED.year, EXCLUDED.nomination, EXCLUDED.nomination_order, EXCLUDED.kind,
			       EXCLUDED.title, EXCLUDED.orig_title, EXCLUDED.author, EXCLUDED.source_link)`,
			a.Key, w.year, w.nomination, w.nomOrder, w.kind, w.title, w.origTitle, w.author, w.ref, w.link, a.source()); err != nil {
			return fmt.Errorf("store award win: %w", err)
		}
	}
	rows, err := tx.Query(ctx, `DELETE FROM award_wins WHERE award = $1 AND NOT (source = $3 AND source_ref = ANY($2))
		RETURNING work_id`, a.Key, refs, a.source())
	if err != nil {
		return fmt.Errorf("prune award wins: %w", err)
	}
	var gone []*int64
	for rows.Next() {
		var id *int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		gone = append(gone, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("prune award wins: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, id := range gone {
		s.noteWork(id)
	}
	return nil
}
