package awards

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrUnknownAward — премии нет в белом списке.
var ErrUnknownAward = errors.New("unknown award")

// Service — чтение лауреатов для API.
type Service struct {
	pool *pgxpool.Pool
}

// NewService — сервис премий.
func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// Summary — премия в списке раздела: сколько лауреатов и сколько из них в каталоге.
type Summary struct {
	Award
	Wins      int `json:"wins"`
	InCatalog int `json:"in_catalog"`
	FirstYear int `json:"first_year,omitempty"`
	LastYear  int `json:"last_year,omitempty"`
}

// List — премии белого списка в порядке раздела; ещё не загруженные — с нулями.
// У кинопремий в счёт идут только экранизации книг каталога — других не показываем.
func (s *Service) List(ctx context.Context) ([]Summary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT award, count(*), count(*) FILTER (WHERE work_id IS NOT NULL OR author_id IS NOT NULL),
		       COALESCE(min(year) FILTER (WHERE award <> ALL($1) OR work_id IS NOT NULL), 0),
		       COALESCE(max(year) FILTER (WHERE award <> ALL($1) OR work_id IS NOT NULL), 0)
		FROM award_wins GROUP BY award`, filmAwardKeys())
	if err != nil {
		return nil, fmt.Errorf("list awards: %w", err)
	}
	defer rows.Close()
	type agg struct{ wins, in, first, last int }
	by := map[string]agg{}
	for rows.Next() {
		var key string
		var a agg
		if err := rows.Scan(&key, &a.wins, &a.in, &a.first, &a.last); err != nil {
			return nil, err
		}
		by[key] = a
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(Catalog))
	for _, a := range Catalog {
		g := by[a.Key]
		if a.Film {
			g.wins = g.in
		}
		out = append(out, Summary{Award: a, Wins: g.wins, InCatalog: g.in, FirstYear: g.first, LastYear: g.last})
	}
	return out, nil
}

// Win — лауреат премии.
type Win struct {
	ID         int64  `json:"id"`
	Year       int    `json:"year"`
	Nomination string `json:"nomination,omitempty"`
	Kind       string `json:"kind"` // work | author
	Title      string `json:"title,omitempty"`
	OrigTitle  string `json:"orig_title,omitempty"`
	Author     string `json:"author"`
	WorkID     *int64 `json:"work_id,omitempty"`
	AuthorID   *int64 `json:"author_id,omitempty"`
	Source     string `json:"source"` // fantlab | wikidata | manual
	SourceURL  string `json:"source_url,omitempty"`
}

// Wins — все лауреаты премии: свежие годы сверху, внутри года — порядок номинаций у
// источника. У кинопремии — только экранизации книг каталога.
func (s *Service) Wins(ctx context.Context, key string) (Award, []Win, error) {
	a, ok := ByKey(key)
	if !ok {
		return Award{}, nil, ErrUnknownAward
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, year, nomination, kind, title, orig_title, author, work_id, author_id, source, source_link
		FROM award_wins WHERE award = $1 AND (NOT $2 OR work_id IS NOT NULL)
		ORDER BY year DESC, nomination_order, id`, key, a.Film)
	if err != nil {
		return Award{}, nil, fmt.Errorf("award wins: %w", err)
	}
	defer rows.Close()
	out := []Win{}
	for rows.Next() {
		var w Win
		var link string
		if err := rows.Scan(&w.ID, &w.Year, &w.Nomination, &w.Kind, &w.Title, &w.OrigTitle, &w.Author, &w.WorkID,
			&w.AuthorID, &w.Source, &link); err != nil {
			return Award{}, nil, err
		}
		w.SourceURL = sourceURL(w.Source, link)
		out = append(out, w)
	}
	return a, out, rows.Err()
}

// sourceURL — страница у источника: Фантлаб (work123, autor45), Wikidata (QID),
// ручной список (адрес страницы премии).
func sourceURL(source, link string) string {
	switch {
	case link == "":
		return ""
	case source == sourceWikidata:
		return "https://www.wikidata.org/wiki/" + link
	case source == sourceManual:
		return link
	default:
		return "https://fantlab.ru/" + link
	}
}

// Badge — премия на карточке книги или автора.
type Badge struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	Year       int    `json:"year"`
	Nomination string `json:"nomination,omitempty"`
	// Film — название фильма или сериала, если премия — кинопремия экранизации.
	Film string `json:"film,omitempty"`
}

// WorkAwards — премии работы (в порядке белого списка, затем по годам).
func (s *Service) WorkAwards(ctx context.Context, workID int64) ([]Badge, error) {
	return s.badges(ctx, `work_id = $1`, workID)
}

// AuthorAwards — премии, врученные автору (не произведению).
func (s *Service) AuthorAwards(ctx context.Context, authorID int64) ([]Badge, error) {
	return s.badges(ctx, `author_id = $1 AND kind = 'author'`, authorID)
}

func (s *Service) badges(ctx context.Context, where string, id int64) ([]Badge, error) {
	rows, err := s.pool.Query(ctx, `SELECT award, year, nomination, title FROM award_wins WHERE `+where+` ORDER BY year, id`, id)
	if err != nil {
		return nil, fmt.Errorf("award badges: %w", err)
	}
	defer rows.Close()
	order := make(map[string]int, len(Catalog))
	for i, a := range Catalog {
		order[a.Key] = i
	}
	out := []Badge{}
	for rows.Next() {
		var b Badge
		var title string
		if err := rows.Scan(&b.Key, &b.Year, &b.Nomination, &title); err != nil {
			return nil, err
		}
		a, ok := ByKey(b.Key)
		if !ok {
			continue // премию убрали из белого списка — до ближайшей синхронизации не показываем
		}
		b.Name = a.Name
		if a.Film {
			b.Film = title
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return order[out[i].Key] < order[out[j].Key] })
	return out, nil
}

// ── Фоновая синхронизация ────────────────────────────────────────

// syncedKey — когда лауреаты последний раз загружались с источника (app_settings.updated_at).
const syncedKey = "awards_synced_at"

// SyncInterval — как часто перезагружать лауреатов (премии вручаются раз в год).
const SyncInterval = 7 * 24 * time.Hour

// Run — фоновый цикл: через startDelay и дальше каждые tick — загрузка с
// источника, если с прошлой прошло SyncInterval или изменился белый список
// (catalogVersion), иначе только пересопоставление с каталогом (каталог меняется
// с импортом).
func (s *Syncer) Run(ctx context.Context, startDelay, tick time.Duration) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(startDelay):
	}
	s.step(ctx)
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.step(ctx)
		}
	}
}

// catalogVersion — отпечаток белого списка и ручного списка: изменились — загрузить
// лауреатов сразу, не дожидаясь недели (новые премии после обновления).
func catalogVersion() string {
	h := sha256.New()
	for _, a := range Catalog {
		a.Description, a.Site = "", "" // тексты о премии на лауреатов не влияют
		_, _ = fmt.Fprintf(h, "%+v\n", a)
	}
	_, _ = h.Write(manualJSON)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func (s *Syncer) step(ctx context.Context) {
	start := time.Now()
	var last time.Time
	var version string
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(max(updated_at), 'epoch'), COALESCE(max(value->>'v'), '')
		FROM app_settings WHERE key = $1`, syncedKey).Scan(&last, &version)
	if err != nil {
		s.logger.Warn("awards: read sync time failed", "err", err)
		return
	}
	if time.Since(last) < SyncInterval && version == catalogVersion() {
		if err := s.Match(ctx); err != nil && ctx.Err() == nil {
			s.logger.Warn("awards: match failed", "err", err)
		}
		return
	}
	total, matched, err := s.SyncAll(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Warn("awards: sync failed", "err", err)
		}
		return
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at) VALUES ($1, jsonb_build_object('v', $2::text), now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, syncedKey, catalogVersion()); err != nil {
		s.logger.Warn("awards: store sync time failed", "err", err)
	}
	s.logger.Info("awards synced", "wins", total, "in_catalog", matched, "took", time.Since(start).Round(time.Second))
}
