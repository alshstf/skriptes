package metadata

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Перепроверка биографий и фото авторов (#280). 42 тыс. био скачаны в июне,
// до гейтов профессии, тёзок, неоднозначности и точного совпадения фамилий; по
// выборке аудита чужие — около 28%. Сбрасывать всё нельзя: карточки и список
// авторов опустели бы на сутки. Вместо этого каждый автор с био или фото
// заново проходит поиск с текущими гейтами:
//   - нашлось то же — не трогаем;
//   - нашлось другое — заменяем;
//   - гейты больше не пропускают — очищаем;
//   - сбой источника — не трогаем, повтор в следующем проходе.
// Изменения пишутся в author_meta_recheck (что было, что стало). Идём от самых
// известных авторов: первыми исправляются видимые карточки. Прежние файлы фото
// не удаляются — нужны для отката.

// AuthorRecheckStats — итог перепроверки.
type AuthorRecheckStats struct {
	Checked   int // авторов пройдено (без сбоев)
	Deferred  int // авторов со сбоем источника — повторятся
	BioKept   int
	BioNew    int // заменено или добавлено
	BioClear  int
	PhotoKept int
	PhotoNew  int
	PhotoClr  int
}

// AuthorRechecker — проход перепроверки. since — начало перепроверки: авторы с
// metadata_fetched_at раньше него ещё не перепроверены (после перепроверки
// маркер ставится в now()).
type AuthorRechecker struct {
	pool     *pgxpool.Pool
	enricher *Enricher
	logger   *slog.Logger
	gate     *rateGate
}

func NewAuthorRechecker(pool *pgxpool.Pool, enricher *Enricher, rpm int, logger *slog.Logger) *AuthorRechecker {
	if logger == nil {
		logger = slog.Default()
	}
	r := &AuthorRechecker{pool: pool, enricher: enricher, logger: logger, gate: &rateGate{}}
	r.gate.setRPM(rpm)
	return r
}

// Pass — один проход по всем ещё не перепроверенным авторам. Возвращает итог;
// Deferred > 0 — есть авторы со сбоем источника, нужен следующий проход.
func (r *AuthorRechecker) Pass(ctx context.Context, since time.Time) (AuthorRecheckStats, error) {
	var st AuthorRecheckStats
	var lastRenown int64 = 1 << 62
	var lastID int64
	for ctx.Err() == nil {
		batch, err := r.batch(ctx, since, lastRenown, lastID)
		if err != nil {
			return st, err
		}
		if len(batch) == 0 {
			break
		}
		for _, a := range batch {
			if ctx.Err() != nil {
				break
			}
			if err := r.gate.wait(ctx); err != nil {
				return st, err
			}
			r.checkOne(ctx, a.cand, &st)
			lastRenown, lastID = a.renown, a.cand.id
		}
	}
	return st, ctx.Err()
}

type recheckCandidate struct {
	cand   authorCandidate
	renown int64
}

func (r *AuthorRechecker) batch(ctx context.Context, since time.Time, lastRenown, lastID int64) ([]recheckCandidate, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, last_name, first_name, middle_name,
		       TRIM(CONCAT_WS(' ', last_name, first_name, middle_name)), renown
		FROM authors
		WHERE (COALESCE(bio, '') <> '' OR COALESCE(photo_path, '') <> '')
		  AND (metadata_fetched_at IS NULL OR metadata_fetched_at < $1)
		  AND (renown, id) < ($2, $3)
		ORDER BY renown DESC, id DESC
		LIMIT 200`, since, lastRenown, lastID)
	if err != nil {
		return nil, fmt.Errorf("recheck candidates: %w", err)
	}
	defer rows.Close()
	var out []recheckCandidate
	for rows.Next() {
		var c recheckCandidate
		if err := rows.Scan(&c.cand.id, &c.cand.lastName, &c.cand.firstName, &c.cand.middleName,
			&c.cand.fullName, &c.renown); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *AuthorRechecker) checkOne(ctx context.Context, a authorCandidate, st *AuthorRecheckStats) {
	taskCtx, cancel := context.WithTimeout(ctx, authorBackfillTaskTimeout)
	defer cancel()
	q := r.enricher.withNamesakeContext(taskCtx, AuthorQuery{
		ID: a.id, LastName: a.lastName, FirstName: a.firstName, MiddleName: a.middleName, FullName: a.fullName,
	})
	var oldBio, oldPhoto string
	if err := r.pool.QueryRow(taskCtx, `SELECT COALESCE(bio, ''), COALESCE(photo_path, '') FROM authors WHERE id = $1`,
		a.id).Scan(&oldBio, &oldPhoto); err != nil {
		r.logger.Warn("author recheck: read author failed", "author_id", a.id, "err", err)
		st.Deferred++
		return
	}
	// Без источников судить не о чем — поле не трогаем (иначе пустые провайдеры
	// «очистили» бы всё).
	bio, bioTransient := oldBio, false
	if len(r.enricher.authorBioProviders) > 0 {
		bio, bioTransient = r.enricher.fetchAuthorBio(taskCtx, q)
	}
	photo, photoTransient := oldPhoto, false
	if len(r.enricher.authorPhotoProviders) > 0 && r.enricher.photoCache != nil {
		photo, photoTransient = r.enricher.fetchAuthorPhoto(taskCtx, q)
	}
	if bioTransient || photoTransient {
		st.Deferred++ // маркер не трогаем — автор повторится в следующем проходе
		return
	}
	switch bio {
	case oldBio:
		st.BioKept++
	case "":
		st.BioClear++
	default:
		st.BioNew++
	}
	switch photo {
	case oldPhoto:
		st.PhotoKept++
	case "":
		st.PhotoClr++
	default:
		st.PhotoNew++
	}
	if err := r.apply(ctx, a.id, oldBio, bio, oldPhoto, photo); err != nil {
		r.logger.Warn("author recheck: write failed", "author_id", a.id, "err", err)
		st.Deferred++
		return
	}
	st.Checked++
}

// apply пишет результат и журнал одной транзакцией и ставит маркер.
func (r *AuthorRechecker) apply(ctx context.Context, id int64, oldBio, bio, oldPhoto, photo string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, f := range []struct{ field, col, old, new string }{
		{"bio", "bio", oldBio, bio},
		{"photo", "photo_path", oldPhoto, photo},
	} {
		if f.old == f.new {
			continue
		}
		action := "replaced"
		switch {
		case f.new == "":
			action = "cleared"
		case f.old == "":
			action = "added"
		}
		if _, err := tx.Exec(ctx, `UPDATE authors SET `+f.col+` = NULLIF($2, '') WHERE id = $1`, id, f.new); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO author_meta_recheck (author_id, field, action, old_value, new_value)
			VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''))`, id, f.field, action, f.old, f.new); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE authors SET metadata_fetched_at = now() WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// fetchAuthorBio — био по провайдерам без учёта уже сохранённого и без записи.
// transient — хоть один источник сбоил, а ни один не нашёл.
func (e *Enricher) fetchAuthorBio(ctx context.Context, q AuthorQuery) (string, bool) {
	transient := false
	for _, p := range e.authorBioProviders {
		text, err := p.FetchAuthorBio(ctx, q)
		observeLookup("author_bio", p.Name(), err, text != "")
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			transient = true
			continue
		}
		if text != "" {
			return text, false
		}
	}
	return "", transient
}

// fetchAuthorPhoto — фото по провайдерам, сохранённое в кэш (имя файла), без
// записи в автора. transient — как у fetchAuthorBio; сбой сохранения в кэш —
// тоже временный.
func (e *Enricher) fetchAuthorPhoto(ctx context.Context, q AuthorQuery) (string, bool) {
	transient := false
	for _, p := range e.authorPhotoProviders {
		img, err := p.FetchAuthorPhoto(ctx, q)
		observeLookup("author_photo", p.Name(), err, img != nil && img.Reader != nil)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			transient = true
			continue
		}
		if img == nil || img.Reader == nil {
			continue
		}
		name, err := e.photoCache.Save(img.Reader, img.Mime)
		_ = img.Reader.Close()
		if err != nil {
			transient = true
			continue
		}
		return name, false
	}
	return "", transient
}
