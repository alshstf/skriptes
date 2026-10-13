package timeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skriptes/skriptes/backend/internal/bio"
)

const versionKey = "bio_dataset_version"

// ErrNotFound — у автора/работы нет таймлайна (нет QID или QID нет в датасете).
var ErrNotFound = errors.New("bio timeline: not found")

// Service — чтение таймлайна и сев датасета.
type Service struct {
	pool   *pgxpool.Pool
	ds     *bio.Dataset
	logger *slog.Logger
}

// New — сервис поверх пула и загруженного датасета (bio.Load()).
func New(pool *pgxpool.Pool, ds *bio.Dataset, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, ds: ds, logger: logger}
}

// Version — версия встроенного датасета.
func (s *Service) Version() string { return s.ds.Version }

// stripEvidence — в PG и наружу основания не отдаём (служебное поле генератора).
func stripEvidence(a *bio.Author) *bio.Author {
	c := *a
	c.Events = make([]bio.Event, len(a.Events))
	for i, e := range a.Events {
		e.Evidence = nil
		c.Events[i] = e
	}
	return &c
}

// Seed — идемпотентный сев: если app_settings.bio_dataset_version уже равна
// версии встроенного датасета — no-op; иначе в транзакции upsert всех авторов,
// удаление исчезнувших QID и запись версии. Скрытия (author_event_hides) не
// трогаются — переживают пересев.
func (s *Service) Seed(ctx context.Context) error {
	var current string
	err := s.pool.QueryRow(ctx, `SELECT value::text FROM app_settings WHERE key = $1`, versionKey).Scan(&current)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("bio seed: read version: %w", err)
	}
	if current == quoted(s.ds.Version) {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("bio seed: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qids := make([]string, 0, len(s.ds.Authors))
	for qid, a := range s.ds.Authors {
		data, err := json.Marshal(stripEvidence(a))
		if err != nil {
			return fmt.Errorf("bio seed: marshal %s: %w", qid, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO author_bio (qid, name, data, version, updated_at)
			VALUES ($1, $2, $3, $4, now())
			ON CONFLICT (qid) DO UPDATE SET name = EXCLUDED.name, data = EXCLUDED.data,
			    version = EXCLUDED.version, updated_at = now()`,
			qid, a.Name, data, s.ds.Version); err != nil {
			return fmt.Errorf("bio seed: upsert %s: %w", qid, err)
		}
		qids = append(qids, qid)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM author_bio WHERE NOT (qid = ANY($1))`, qids); err != nil {
		return fmt.Errorf("bio seed: prune: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at) VALUES ($1, to_jsonb($2::text), now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		versionKey, s.ds.Version); err != nil {
		return fmt.Errorf("bio seed: write version: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("bio seed: commit: %w", err)
	}
	s.logger.Info("bio: dataset seeded", "version", s.ds.Version, "authors", len(qids))
	return nil
}

func quoted(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// loadAuthor — запись датасета из PG + скрытые ключи.
func (s *Service) loadAuthor(ctx context.Context, qid string) (*bio.Author, map[string]bool, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT data FROM author_bio WHERE qid = $1`, qid).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("bio: load %s: %w", qid, err)
	}
	var a bio.Author
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, nil, fmt.Errorf("bio: decode %s: %w", qid, err)
	}
	rows, err := s.pool.Query(ctx, `SELECT key FROM author_event_hides WHERE qid = $1`, qid)
	if err != nil {
		return nil, nil, fmt.Errorf("bio: hides %s: %w", qid, err)
	}
	defer rows.Close()
	hidden := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, nil, err
		}
		hidden[k] = true
	}
	return &a, hidden, rows.Err()
}

// SetHidden — скрыть/вернуть веху (админ). Ключ должен существовать в датасете.
func (s *Service) SetHidden(ctx context.Context, qid, key string, hidden bool, by int64) error {
	a, ok := s.ds.Authors[qid]
	if !ok {
		return ErrNotFound
	}
	found := false
	for _, e := range a.Events {
		if e.Key == key {
			found = true
			break
		}
	}
	if !found {
		return ErrNotFound
	}
	if hidden {
		_, err := s.pool.Exec(ctx, `
			INSERT INTO author_event_hides (qid, key, hidden_at, hidden_by) VALUES ($1, $2, now(), $3)
			ON CONFLICT (qid, key) DO NOTHING`, qid, key, by)
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM author_event_hides WHERE qid = $1 AND key = $2`, qid, key)
	return err
}

// Stats — сводка для админки.
type Stats struct {
	DatasetVersion      string    `json:"dataset_version"`
	SeededVersion       string    `json:"seeded_version"`
	AuthorsInDataset    int       `json:"authors_in_dataset"`
	AuthorsWithQID      int       `json:"authors_with_qid"`
	AuthorsWithTimeline int       `json:"authors_with_timeline"`
	HiddenEvents        int       `json:"hidden_events"`
	SeededAt            time.Time `json:"seeded_at"`
}

// Stats — покрытие: авторов каталога с QID, из них с таймлайном.
func (s *Service) Stats(ctx context.Context) (Stats, error) {
	st := Stats{DatasetVersion: s.ds.Version, AuthorsInDataset: len(s.ds.Authors)}
	err := s.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM authors WHERE ext_ids ? 'wd_qid'),
		  (SELECT count(*) FROM authors a JOIN author_bio b ON b.qid = a.ext_ids->>'wd_qid'),
		  (SELECT count(*) FROM author_event_hides),
		  COALESCE((SELECT value #>> '{}' FROM app_settings WHERE key = $1), ''),
		  COALESCE((SELECT max(updated_at) FROM author_bio), to_timestamp(0))`, versionKey).
		Scan(&st.AuthorsWithQID, &st.AuthorsWithTimeline, &st.HiddenEvents, &st.SeededVersion, &st.SeededAt)
	if err != nil {
		return st, fmt.Errorf("bio stats: %w", err)
	}
	return st, nil
}
