package metadata

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Дневная квота хоста (#501). У Google Books бесплатная квота Books API ~1000
// запросов в сутки на проект — одна на всех: рейтинг, обложки, аннотации,
// ленивые запросы с карточки. Раньше кап был только у воркера рейтинга и считал
// только свои запросы, и воркер обложек с Google Books выбрал бы квоту за
// ~17 минут. Теперь квота стоит в общем транспорте источников (breakerTransport):
// каждый запрос к хосту берёт единицу из счётчика UTC-суток; счётчик — в БД
// (app_settings, ключ quota:<host>), поэтому он общий для всех клиентов процесса
// и переживает рестарт. Квота выбрана — запрос не уходит в сеть, а возвращается
// SourcePausedError до полуночи UTC: воркеры пропускают книгу молча, как при
// паузе источника, и вернутся к ней завтра. Картинки (books.google.com) — другой
// хост и в квоту API не входят.

// GoogleBooksHost — хост Books API (квота считается по нему).
const GoogleBooksHost = "www.googleapis.com"

// DailyQuota — лимит запросов к хосту за UTC-сутки.
type DailyQuota struct {
	host   string
	pool   *pgxpool.Pool
	logger *slog.Logger
	limit  atomic.Int64
	now    func() time.Time

	mu        sync.Mutex
	exhausted string // UTC-сутки, на которые квота уже выбрана (без похода в БД)
}

// NewDailyQuota — квота хоста; limit <= 0 — без ограничения.
func NewDailyQuota(pool *pgxpool.Pool, host string, limit int, logger *slog.Logger) *DailyQuota {
	if logger == nil {
		logger = slog.Default()
	}
	q := &DailyQuota{host: host, pool: pool, logger: logger, now: time.Now}
	q.limit.Store(int64(limit))
	return q
}

// SetLimit — новый лимит (из админки); 0 — без ограничения.
func (q *DailyQuota) SetLimit(limit int) {
	q.limit.Store(int64(limit))
	q.mu.Lock()
	q.exhausted = ""
	q.mu.Unlock()
}

func (q *DailyQuota) key() string { return "quota:" + q.host }

func (q *DailyQuota) day() (string, time.Time) {
	now := q.now().UTC()
	return now.Format("2006-01-02"), time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
}

// Take — взять единицу квоты на запрос. false — квота на сегодня выбрана, until —
// полночь UTC. Ошибка БД квоту не закрывает (запрос важнее учёта).
func (q *DailyQuota) Take(ctx context.Context) (bool, time.Time) {
	limit := q.limit.Load()
	today, until := q.day()
	if limit <= 0 || q.pool == nil {
		return true, until
	}
	q.mu.Lock()
	cached := q.exhausted == today
	q.mu.Unlock()
	if cached {
		return false, until
	}
	// Счётчик суток растёт, только пока он ниже лимита; новые сутки — с единицы.
	var taken int
	if err := q.pool.QueryRow(ctx, `
		WITH up AS (
		    INSERT INTO app_settings (key, value, updated_at)
		    VALUES ($1, jsonb_build_object('day', $2::text, 'used', 1), now())
		    ON CONFLICT (key) DO UPDATE SET
		        value = CASE WHEN app_settings.value->>'day' IS DISTINCT FROM $2::text
		                     THEN jsonb_build_object('day', $2::text, 'used', 1)
		                     ELSE jsonb_set(app_settings.value, '{used}',
		                                    to_jsonb((app_settings.value->>'used')::int + 1)) END,
		        updated_at = now()
		    WHERE app_settings.value->>'day' IS DISTINCT FROM $2::text
		       OR (app_settings.value->>'used')::int < $3
		    RETURNING 1
		)
		SELECT count(*) FROM up`, q.key(), today, limit).Scan(&taken); err != nil {
		q.logger.Warn("daily quota: take failed — request allowed", "host", q.host, "err", err)
		return true, until
	}
	if taken > 0 {
		return true, until
	}
	q.mu.Lock()
	first := q.exhausted != today
	q.exhausted = today
	q.mu.Unlock()
	if first {
		q.logger.Warn("daily quota exhausted — requests paused until UTC midnight",
			"host", q.host, "limit", limit, "until", until.Format(time.RFC3339))
	}
	return false, until
}

// Exhausted — квота на сегодня уже выбрана (счётчик не трогает). Воркеры не
// считают такой источник «пора спросить» при выборке кандидатов.
func (q *DailyQuota) Exhausted(ctx context.Context) bool {
	limit := q.limit.Load()
	if limit <= 0 || q.pool == nil {
		return false
	}
	today, _ := q.day()
	q.mu.Lock()
	cached := q.exhausted == today
	q.mu.Unlock()
	if cached {
		return true
	}
	var full bool
	if err := q.pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT value->>'day' = $2 AND (value->>'used')::int >= $3
		                 FROM app_settings WHERE key = $1), false)`,
		q.key(), today, limit).Scan(&full); err != nil {
		return false
	}
	if full {
		q.mu.Lock()
		q.exhausted = today
		q.mu.Unlock()
	}
	return full
}

// Used — сколько запросов к хосту сделано за текущие UTC-сутки (для админки).
func (q *DailyQuota) Used(ctx context.Context) int {
	if q.pool == nil {
		return 0
	}
	today, _ := q.day()
	var n int
	_ = q.pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT CASE WHEN value->>'day' = $2 THEN (value->>'used')::int ELSE 0 END
		                 FROM app_settings WHERE key = $1), 0)`, q.key(), today).Scan(&n)
	return n
}

// Квоты хостов процесса: транспорт источников берёт квоту по хосту запроса.
var hostQuotas sync.Map // host → *DailyQuota

// SetHostDailyQuota — включить квоту хоста для всех клиентов обогащения (nil — снять).
func SetHostDailyQuota(host string, q *DailyQuota) {
	if q == nil {
		hostQuotas.Delete(host)
		return
	}
	hostQuotas.Store(host, q)
}

// HostDailyQuota — квота хоста или nil.
func HostDailyQuota(host string) *DailyQuota {
	if v, ok := hostQuotas.Load(host); ok {
		return v.(*DailyQuota)
	}
	return nil
}
