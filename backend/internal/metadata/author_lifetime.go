package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Годы жизни автора (#465): QID и годы рождения/смерти статьи, которую поиск
// био принял для автора. Политика приёма (candidate_policy.go) уже запрашивает
// эти факты у Wikidata и раньше их выбрасывала — теперь они сохраняются. Нужны
// правилу года работы (work_years.go: год раньше рождения + 10 — опечатка fb2)
// и био-таймлайну.

// RecordingCandidateCheck — политика приёма check, которая у принятого кандидата
// с QID записывает автору QID и годы жизни. facts — тот же (кэширующий)
// источник, что у check: повторного запроса нет.
func RecordingCandidateCheck(check CandidateCheck, facts CandidateFactsFunc, pool *pgxpool.Pool, logger *slog.Logger) CandidateCheck {
	return func(ctx context.Context, q AuthorQuery, source, lang, title, qid string, match MatchKind) (bool, error) {
		ok, err := check(ctx, q, source, lang, title, qid, match)
		if err != nil || !ok || qid == "" || q.ID <= 0 {
			return ok, err
		}
		f, ferr := facts(ctx, qid)
		if ferr != nil {
			return ok, nil // годы — попутно, приём от них не зависит
		}
		if err := recordAuthorLifetime(ctx, pool, q.ID, qid, f.Born, f.Died); err != nil {
			logger.Warn("metadata: record author lifetime failed", "author_id", q.ID, "err", err)
		}
		return ok, nil
	}
}

func recordAuthorLifetime(ctx context.Context, pool *pgxpool.Pool, authorID int64, qid string, born, died int) error {
	_, err := pool.Exec(ctx, `
		UPDATE authors
		SET ext_ids = ext_ids || jsonb_build_object('wd_qid', $2::text),
		    born_year = NULLIF($3, 0), died_year = NULLIF($4, 0)
		WHERE id = $1
		  AND (ext_ids->>'wd_qid' IS DISTINCT FROM $2 OR born_year IS DISTINCT FROM NULLIF($3, 0)
		       OR died_year IS DISTINCT FROM NULLIF($4, 0))`, authorID, qid, born, died)
	return err
}

// ResolveAuthorLifetimes — годы жизни авторов ids без годов: тот же поиск статьи,
// что у био (через RecordingCandidateCheck), без записи био. Возвращает
// авторов, у которых годы появились; сбой источника — выход (догон повторится).
func (e *Enricher) ResolveAuthorLifetimes(ctx context.Context, ids []int64) ([]int64, error) {
	if len(ids) == 0 || len(e.authorBioProviders) == 0 {
		return nil, nil
	}
	type author struct {
		id                        int64
		last, first, middle, full string
	}
	rows, err := e.pool.Query(ctx, `
		SELECT id, last_name, COALESCE(first_name, ''), COALESCE(middle_name, ''),
		       TRIM(CONCAT_WS(' ', last_name, first_name, middle_name))
		FROM authors WHERE id = ANY($1) AND born_year IS NULL AND NOT is_service ORDER BY id`, ids)
	if err != nil {
		return nil, fmt.Errorf("lifetimes: authors: %w", err)
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (author, error) {
		var a author
		err := r.Scan(&a.id, &a.last, &a.first, &a.middle, &a.full)
		return a, err
	})
	if err != nil {
		return nil, fmt.Errorf("lifetimes: authors: %w", err)
	}
	for _, a := range list {
		taskCtx, cancel := context.WithTimeout(ctx, authorBackfillTaskTimeout)
		q := e.withNamesakeContext(taskCtx, AuthorQuery{
			ID: a.id, LastName: a.last, FirstName: a.first, MiddleName: a.middle, FullName: a.full,
		})
		_, transient := e.fetchAuthorBio(taskCtx, q)
		cancel()
		if transient {
			return nil, fmt.Errorf("%w: lifetimes: author %d", ErrUpstream, a.id)
		}
	}
	return scanInt64s(ctx, e.pool, `SELECT id FROM authors WHERE id = ANY($1) AND born_year IS NOT NULL`, ids)
}

// SuspectYearAuthors — основные авторы работ с годом раньше minLifetimeBornYear,
// у которых годы жизни ещё не известны: им годы ищутся в первую очередь (#465).
func SuspectYearAuthors(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	return scanInt64s(ctx, pool, `
		SELECT DISTINCT a.id FROM works w JOIN authors a ON a.id = w.primary_author_id
		WHERE w.written_year < $1 AND a.born_year IS NULL AND NOT a.is_service`, minLifetimeBornYear)
}

// RecomputeAllWorkYears — год всех работ по текущим правилам (после их смены).
// Возвращает работы с изменившимся годом.
func RecomputeAllWorkYears(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	return recomputeWorkYears(ctx, pool, nil, true)
}

// RecomputeYearsBeforeBirth — год работ, который раньше рождения основного
// автора + bornWritingAge (годы жизни появились после расчёта года). Возвращает
// работы с изменившимся годом.
func RecomputeYearsBeforeBirth(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	ids, err := scanInt64s(ctx, pool, `
		SELECT w.id FROM works w JOIN authors a ON a.id = w.primary_author_id
		WHERE a.born_year >= $1 AND w.written_year < a.born_year + $2`, minLifetimeBornYear, bornWritingAge)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	return recomputeWorkYears(ctx, pool, ids, false)
}

// QID-бэкфилл для авторов с био (био-таймлайн, план v3). Био у 8,5 тыс. авторов
// на проде скачаны до #465 — QID и годы жизни у них не записаны (на 2026-10-13
// QID есть у 29). Датасет таймлайна ключуется QID, без него секции не
// показываются. Догон — тот же поиск статьи, что у био (ResolveAuthorLifetimes:
// политика приёма, годы и QID пишутся, био не трогается), от самых известных,
// в темпе RPM воркера био. Позиция (keyset-курсор по (renown, id)) сохраняется
// в app_settings после каждой пачки: рестарт и повтор после сбоя продолжают с
// того же места, уже просмотренные, но не нашедшиеся авторы второй раз не
// берутся. Сбой источника (429, пауза прерывателя, сеть) — пауза и повтор того
// же автора; после qidBackfillMaxRetries попыток автор откладывается (QID
// довнесёт перепроверка био), qidBackfillMaxFailures сбоев подряд — источник
// лежит, проход прерывается (ErrUpstream), вызывающий повторяет его позже.

const (
	qidBackfillCursorKey   = "author_qid_backfill_v1_cursor"
	qidBackfillFailWait    = 2 * time.Minute // пауза после сбоя источника перед повтором автора
	qidBackfillMaxRetries  = 3               // попыток на автора, дальше он отложен
	qidBackfillMaxFailures = 10              // сбоев подряд — проход прерывается до повтора
)

// QIDBackfillCursor — позиция прохода: последняя обработанная пара (renown, id).
type QIDBackfillCursor struct {
	Renown int64
	ID     int64
}

// NextQIDBackfillBatch — следующие limit авторов с био и без QID после курсора,
// по убыванию известности.
func NextQIDBackfillBatch(ctx context.Context, pool *pgxpool.Pool, cur QIDBackfillCursor, limit int) ([]QIDBackfillCursor, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, renown FROM authors
		WHERE bio IS NOT NULL AND NOT (ext_ids ? 'wd_qid') AND born_year IS NULL AND NOT is_service
		  AND (renown, id) < ($1, $2)
		ORDER BY renown DESC, id DESC LIMIT $3`, cur.Renown, cur.ID, limit)
	if err != nil {
		return nil, fmt.Errorf("qid backfill: batch: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (QIDBackfillCursor, error) {
		var c QIDBackfillCursor
		err := r.Scan(&c.ID, &c.Renown)
		return c, err
	})
}

// qidBackfillTop — курсор перед самым известным автором (начало прохода).
var qidBackfillTop = QIDBackfillCursor{Renown: 1 << 62, ID: 1 << 62}

// loadQIDBackfillCursor — сохранённая позиция прохода или начало, если её нет.
func loadQIDBackfillCursor(ctx context.Context, pool *pgxpool.Pool) (QIDBackfillCursor, error) {
	var raw []byte
	err := pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, qidBackfillCursorKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return qidBackfillTop, nil
	}
	if err != nil {
		return qidBackfillTop, fmt.Errorf("qid backfill: cursor: %w", err)
	}
	var cur struct {
		Renown int64 `json:"renown"`
		ID     int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &cur); err != nil || cur.ID == 0 {
		return qidBackfillTop, nil // битое значение — проход с начала
	}
	return QIDBackfillCursor{Renown: cur.Renown, ID: cur.ID}, nil
}

func saveQIDBackfillCursor(ctx context.Context, pool *pgxpool.Pool, cur QIDBackfillCursor) error {
	_, err := pool.Exec(ctx, `INSERT INTO app_settings (key, value, updated_at)
		VALUES ($1, jsonb_build_object('renown', $2::bigint, 'id', $3::bigint), now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		qidBackfillCursorKey, cur.Renown, cur.ID)
	return err
}

func clearQIDBackfillCursor(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `DELETE FROM app_settings WHERE key = $1`, qidBackfillCursorKey)
	return err
}

// qidFailures — реакция прохода на сбои источника: сколько раз подряд сбоил
// текущий автор и сколько сбоев подряд всего (по разным авторам).
type qidFailures struct{ author, total int }

type qidFailAction int

const (
	qidRetryAuthor qidFailAction = iota // пауза и тот же автор ещё раз
	qidSkipAuthor                       // автор отложен, дальше по списку
	qidAbortPass                        // источник лежит — проход прервать
)

func (f *qidFailures) fail() qidFailAction {
	f.author++
	f.total++
	switch {
	case f.total >= qidBackfillMaxFailures:
		return qidAbortPass
	case f.author >= qidBackfillMaxRetries:
		f.author = 0
		return qidSkipAuthor
	default:
		return qidRetryAuthor
	}
}

func (f *qidFailures) ok() { f.author, f.total = 0, 0 }

// sleepCtx — пауза, прерываемая остановкой.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// BackfillAuthorQIDs — проход с сохранённой позиции до конца списка; возвращает,
// сколько авторов просмотрено и у скольких появились годы/QID. Проход дошёл до
// конца — курсор снимается, вызывающий ставит флаг; ErrUpstream — прерван из-за
// сбоев источника, позиция сохранена, вызывающий повторяет позже.
func (e *Enricher) BackfillAuthorQIDs(ctx context.Context, rpm int, logger *slog.Logger) (scanned, resolved int, err error) {
	if logger == nil {
		logger = slog.Default()
	}
	if rpm <= 0 {
		rpm = 30 // без лимита Википедию не дёргаем
	}
	gate := &rateGate{}
	gate.setRPM(rpm)
	cur, err := loadQIDBackfillCursor(ctx, e.pool)
	if err != nil {
		return 0, 0, err
	}
	if cur != qidBackfillTop {
		logger.Info("author qid backfill: resuming", "renown", cur.Renown, "author_id", cur.ID)
	}
	var fails qidFailures
	deferred := 0
	for {
		batch, err := NextQIDBackfillBatch(ctx, e.pool, cur, 200)
		if err != nil {
			return scanned, resolved, err
		}
		if len(batch) == 0 {
			if err := clearQIDBackfillCursor(ctx, e.pool); err != nil {
				logger.Warn("author qid backfill: clear cursor failed", "err", err)
			}
			if deferred > 0 {
				logger.Info("author qid backfill: authors deferred to bio recheck", "deferred", deferred)
			}
			return scanned, resolved, nil
		}
		for i := 0; i < len(batch); {
			a := batch[i]
			if err := gate.wait(ctx); err != nil {
				return scanned, resolved, err
			}
			got, err := e.ResolveAuthorLifetimes(ctx, []int64{a.ID})
			if err != nil {
				if ctx.Err() != nil {
					return scanned, resolved, ctx.Err()
				}
				switch fails.fail() {
				case qidAbortPass:
					if serr := saveQIDBackfillCursor(ctx, e.pool, cur); serr != nil {
						logger.Warn("author qid backfill: save cursor failed", "err", serr)
					}
					return scanned, resolved, fmt.Errorf("%w: %d сбоев подряд, последний: %v", ErrUpstream, fails.total, err)
				case qidSkipAuthor:
					logger.Info("author qid backfill: author deferred after repeated source failures", "author_id", a.ID, "err", err)
					deferred++
					cur = a
					i++
				default:
					logger.Info("author qid backfill: source failed, retrying author after pause", "author_id", a.ID, "err", err)
				}
				if err := sleepCtx(ctx, qidBackfillFailWait); err != nil {
					return scanned, resolved, err
				}
				continue
			}
			fails.ok()
			scanned++
			resolved += len(got)
			cur = a
			i++
			if scanned%500 == 0 {
				logger.Info("author qid backfill: progress", "scanned", scanned, "resolved", resolved)
			}
		}
		if err := saveQIDBackfillCursor(ctx, e.pool, cur); err != nil {
			logger.Warn("author qid backfill: save cursor failed", "err", err)
		}
	}
}
