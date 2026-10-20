package recsys

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/textnorm"
)

// Лог поисковых запросов (план recsys/01, задача 1.4). Приватность: строки
// видит только их пользователь; админу — агрегат Misses без user_id; срок
// хранения — Purge (SKRIPTES_SEARCH_LOG_DAYS). Ненайденное (hits = 0) — список
// желаний: после импорта Matcher (searchmatch.go) ищет появившиеся работы.

const (
	// collapseWindow — список книг запрашивает выдачу на каждое изменение
	// строки поиска: «пикн» → «пикник» → «пикник на обочине». Запрос, который
	// является продолжением (или откатом) предыдущего в этом окне, заменяет
	// его, а не добавляет строку — в логе остаётся итог набора.
	collapseWindow = 60 * time.Second
	// MinQueryLen — короче не логируем (и в «ненайденное» не берём).
	MinQueryLen = 3
	// MissMinLen — ненайденный запрос короче этого — не кандидат на «появилось».
	MissMinLen = 4
	// matchesRetention — записи «появилось» старше — чистятся вместе с логом.
	matchesRetention = 180 * 24 * time.Hour
)

// NormalizeQuery — ключ сравнения запросов: нижний регистр, ё→е, одна форма
// пробелов, без пунктуации по краям слов («Пикник на обочине!» = «пикник на обочине»).
func NormalizeQuery(q string) string {
	q = strings.ToLower(textnorm.FoldYo(q))
	fields := strings.Fields(q)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimFunc(f, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if f != "" {
			out = append(out, f)
		}
	}
	return strings.Join(out, " ")
}

// SearchLog — запись и чтение лога.
type SearchLog struct {
	pool *pgxpool.Pool
	// RetentionDays — срок хранения; 0 — лог выключен (Record — no-op).
	RetentionDays int
}

func NewSearchLog(pool *pgxpool.Pool, retentionDays int) *SearchLog {
	return &SearchLog{pool: pool, RetentionDays: retentionDays}
}

// Enabled — лог ведётся.
func (l *SearchLog) Enabled() bool { return l != nil && l.RetentionDays > 0 }

// Record — строка лога; возвращает её id (0 — не записано: лог выключен или
// запрос слишком короткий). Продолжение/откат предыдущего запроса в окне
// collapseWindow обновляет ту же строку (см. collapseWindow).
func (l *SearchLog) Record(ctx context.Context, userID int64, q string, filters map[string]any, hits int) (int64, error) {
	if !l.Enabled() {
		return 0, nil
	}
	q = strings.TrimSpace(q)
	norm := NormalizeQuery(q)
	if len([]rune(norm)) < MinQueryLen {
		return 0, nil
	}
	fj, err := json.Marshal(filters)
	if err != nil || filters == nil {
		fj = []byte("{}")
	}
	var (
		lastID   int64
		lastNorm string
	)
	err = l.pool.QueryRow(ctx, `
		SELECT id, norm FROM search_log
		WHERE user_id = $1 AND at > now() - $2::interval
		ORDER BY at DESC LIMIT 1`, userID, collapseWindow.String()).Scan(&lastID, &lastNorm)
	switch {
	case err == nil && (strings.HasPrefix(norm, lastNorm) || strings.HasPrefix(lastNorm, norm)):
		_, err = l.pool.Exec(ctx, `
			UPDATE search_log SET q = $2, norm = $3, filters = $4, hits = $5, at = now(), clicked_work_id = NULL
			WHERE id = $1`, lastID, q, norm, fj, hits)
		if err != nil {
			return 0, fmt.Errorf("search log update: %w", err)
		}
		return lastID, nil
	case err != nil && err != pgx.ErrNoRows:
		return 0, fmt.Errorf("search log last: %w", err)
	}
	var id int64
	if err := l.pool.QueryRow(ctx, `
		INSERT INTO search_log (user_id, q, norm, filters, hits) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		userID, q, norm, fj, hits).Scan(&id); err != nil {
		return 0, fmt.Errorf("search log insert: %w", err)
	}
	return id, nil
}

// Click — переход из выдачи: какую работу открыли по этому запросу. Чужую
// строку не трогает (no-op).
func (l *SearchLog) Click(ctx context.Context, userID, logID, workID int64) error {
	if !l.Enabled() {
		return nil
	}
	_, err := l.pool.Exec(ctx, `UPDATE search_log SET clicked_work_id = $3 WHERE id = $1 AND user_id = $2`, logID, userID, workID)
	if err != nil {
		return fmt.Errorf("search log click: %w", err)
	}
	return nil
}

// Entry — строка лога для страницы «Поиск» в профиле.
type Entry struct {
	ID           int64     `json:"id"`
	Q            string    `json:"q"`
	Hits         int       `json:"hits"`
	ClickedWork  int64     `json:"clicked_work_id,omitempty"`
	ClickedTitle string    `json:"clicked_title,omitempty"`
	At           time.Time `json:"at"`
}

// List — история пользователя, новые сверху; total — всего строк.
func (l *SearchLog) List(ctx context.Context, userID int64, limit, offset int) ([]Entry, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var total int
	if err := l.pool.QueryRow(ctx, `SELECT count(*) FROM search_log WHERE user_id = $1`, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("search log count: %w", err)
	}
	rows, err := l.pool.Query(ctx, `
		SELECT s.id, s.q, s.hits, COALESCE(s.clicked_work_id, 0), COALESCE(w.title, ''), s.at
		FROM search_log s
		LEFT JOIN works w ON w.id = s.clicked_work_id
		WHERE s.user_id = $1
		ORDER BY s.at DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("search log list: %w", err)
	}
	defer rows.Close()
	out := make([]Entry, 0)
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Q, &e.Hits, &e.ClickedWork, &e.ClickedTitle, &e.At); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// Delete — одна строка пользователя (чужая — no-op).
func (l *SearchLog) Delete(ctx context.Context, userID, logID int64) error {
	if _, err := l.pool.Exec(ctx, `DELETE FROM search_log WHERE id = $1 AND user_id = $2`, logID, userID); err != nil {
		return fmt.Errorf("search log delete: %w", err)
	}
	return nil
}

// DeleteAll — вся история пользователя и его «появилось».
func (l *SearchLog) DeleteAll(ctx context.Context, userID int64) error {
	if _, err := l.pool.Exec(ctx, `DELETE FROM search_log WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("search log delete all: %w", err)
	}
	if _, err := l.pool.Exec(ctx, `DELETE FROM search_matches WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("search matches delete all: %w", err)
	}
	return nil
}

// Purge — срок хранения: лог старше RetentionDays (при 0 — весь), записи
// «появилось» старше matchesRetention. Возвращает число удалённых строк лога.
func (l *SearchLog) Purge(ctx context.Context) (int64, error) {
	days := l.RetentionDays
	if days < 0 {
		days = 0
	}
	tag, err := l.pool.Exec(ctx, `DELETE FROM search_log WHERE at < now() - make_interval(days => $1)`, days)
	if err != nil {
		return 0, fmt.Errorf("search log purge: %w", err)
	}
	if _, err := l.pool.Exec(ctx, `DELETE FROM search_matches WHERE matched_at < now() - $1::interval`, matchesRetention.String()); err != nil {
		return 0, fmt.Errorf("search matches purge: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Miss — агрегат «ищут и не находят» для админа: текст (самый частый вариант
// написания), сколько раз и сколько разных пользователей. Без user_id.
type Miss struct {
	Q     string `json:"q"`
	Count int    `json:"count"`
	Users int    `json:"users"`
}

// Misses — ненайденные запросы за days дней, по убыванию частоты.
func (l *SearchLog) Misses(ctx context.Context, days, limit int) ([]Miss, error) {
	if days <= 0 {
		days = 90
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := l.pool.Query(ctx, `
		SELECT (array_agg(q ORDER BY at DESC))[1], count(*), count(DISTINCT user_id)
		FROM search_log
		WHERE hits = 0 AND at > now() - make_interval(days => $1) AND length(norm) >= $3
		GROUP BY norm
		ORDER BY count(*) DESC, max(at) DESC
		LIMIT $2`, days, limit, MissMinLen)
	if err != nil {
		return nil, fmt.Errorf("search misses: %w", err)
	}
	defer rows.Close()
	out := make([]Miss, 0)
	for rows.Next() {
		var m Miss
		if err := rows.Scan(&m.Q, &m.Count, &m.Users); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
