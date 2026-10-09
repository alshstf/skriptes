package metadata

import (
	"context"
	"fmt"
	"log/slog"

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
