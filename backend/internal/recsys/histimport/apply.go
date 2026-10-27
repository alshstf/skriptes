package histimport

import (
	"context"
	"crypto/sha1" //nolint:gosec // ключ места подчёркивания, не защита
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/recsys"
)

// Применение импорта и откат (recsys/04, 4.4). Всё, что записал пакет, помечено
// import_batch_id; своё пользователя не перетираем (оценка, «прочитано», полка
// уже есть — пропуск, в отчёте «kept»). Правила записи:
//   - оценка 1–5 → book_ratings (rated_at — дата прочтения, иначе сейчас);
//   - прочитано (полка, дата или оценка — «оценил ⇒ прочитал», как SetRating) →
//     reads.completed_at на представительном издании работы, если работа ещё не
//     прочитана;
//   - «хочу прочитать» → полка to_read, если работа не прочитана и ещё не там;
//   - подчёркивания → annotations kind=highlight с cfi «import:<sha1>» (без
//     места в тексте: ридер показывает их списком, не в тексте), fraction NULL;
//   - книги, которых нет в библиотеке (watch), → список желаний wishlist
//     «название автор» (#597): появятся при импорте INPX — Главная скажет
//     «Появилось то, что вы искали» (recsys.Matcher).

// ImportCFIPrefix — «место» подчёркивания из импорта: в тексте его нет.
const ImportCFIPrefix = "import:"

// Choice — книга файла (индекс в Result.Items) → выбранная работа каталога.
type Choice struct {
	Index  int   `json:"i"`
	WorkID int64 `json:"work_id"`
}

// Report — итог применения (те же числа — в import_batches).
type Report struct {
	BatchID    int64 `json:"batch_id"`
	Applied    int   `json:"applied"`
	Ratings    int   `json:"ratings"`
	Reads      int   `json:"reads"`
	ToRead     int   `json:"to_read"`
	Highlights int   `json:"highlights"`
	Kept       int   `json:"kept"`
	Wishlist   int   `json:"wishlist"` // ненайденных под наблюдением (новых в списке желаний)
}

// Batch — пакет импорта в списке профиля.
type Batch struct {
	Report
	Source    Source     `json:"source"`
	FileName  string     `json:"file_name"`
	Items     int        `json:"items"`
	AppliedAt time.Time  `json:"applied_at"`
	UndoneAt  *time.Time `json:"undone_at,omitempty"`
}

var (
	// ErrBadChoice — выбор ссылается на строку вне файла или скрытую/несуществующую работу.
	ErrBadChoice = errors.New("неверный выбор: книги нет в файле или работы нет в библиотеке")
	// ErrBatchNotFound — нет такого пакета у пользователя (или он уже отменён).
	ErrBatchNotFound = errors.New("импорт не найден или уже отменён")
)

const (
	maxExcerpt = 2000 // как history.SaveAnnotation
	maxNote    = 10000
	toReadName = "Хочу прочитать" // как history.toReadName
)

// Apply записывает выбранные книги одной транзакцией и создаёт пакет; watch —
// книги файла (индексы), которых нет в библиотеке, — в список желаний.
func Apply(ctx context.Context, pool *pgxpool.Pool, userID int64, res *Result, fileName string, choices []Choice, watch []int) (Report, error) {
	var rep Report
	works := make(map[int64]struct{}, len(choices))
	for _, c := range choices {
		if c.Index < 0 || c.Index >= len(res.Items) || c.WorkID <= 0 {
			return rep, ErrBadChoice
		}
		works[c.WorkID] = struct{}{}
	}
	for _, i := range watch {
		if i < 0 || i >= len(res.Items) {
			return rep, ErrBadChoice
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return rep, fmt.Errorf("histimport: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Представительное издание работы — живое, не скрытое; где пользователь
	// уже читал — предпочтительнее (как SetRating).
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT ON (b.work_id) b.work_id, b.id
		FROM books b JOIN works w ON w.id = b.work_id
		WHERE b.work_id = ANY($1) AND NOT b.deleted AND NOT b.instance_hidden AND NOT w.instance_hidden
		ORDER BY b.work_id, (EXISTS (SELECT 1 FROM reads r WHERE r.user_id = $2 AND r.book_id = b.id)) DESC, b.id`,
		keys(works), userID)
	if err != nil {
		return rep, fmt.Errorf("histimport: издания: %w", err)
	}
	edition := map[int64]int64{}
	if err := forEach(rows, func(r pgx.Rows) error {
		var w, b int64
		if err := r.Scan(&w, &b); err != nil {
			return err
		}
		edition[w] = b
		return nil
	}); err != nil {
		return rep, err
	}
	if len(edition) != len(works) {
		return rep, ErrBadChoice
	}

	if err := tx.QueryRow(ctx, `
		INSERT INTO import_batches (user_id, source, file_name, items) VALUES ($1, $2, $3, $4) RETURNING id`,
		userID, string(res.Source), clipText(fileName, 300), len(res.Items)).Scan(&rep.BatchID); err != nil {
		return rep, fmt.Errorf("histimport: пакет: %w", err)
	}
	now := time.Now()
	for _, c := range choices {
		it := res.Items[c.Index]
		book := edition[c.WorkID]
		rep.Applied++
		when := now
		if it.ReadAt != nil {
			when = *it.ReadAt
		}
		if it.Rating > 0 {
			tag, err := tx.Exec(ctx, `
				INSERT INTO book_ratings (user_id, work_id, rating, rated_at, import_batch_id)
				VALUES ($1, $2, $3, $4, $5) ON CONFLICT (user_id, work_id) DO NOTHING`,
				userID, c.WorkID, it.Rating, when, rep.BatchID)
			if err != nil {
				return rep, fmt.Errorf("histimport: оценка: %w", err)
			}
			tally(tag.RowsAffected(), &rep.Ratings, &rep.Kept)
		}
		read := it.Read() || it.Rating > 0
		if read {
			// Работа уже прочитана (любое издание) — своё не трогаем; иначе
			// отметка на представительном издании, его строка reads — та же
			// (прогресс в ней сохраняется).
			tag, err := tx.Exec(ctx, `
				INSERT INTO reads (user_id, book_id, completed_at, updated_at, import_batch_id)
				SELECT $1, $2, $3, now(), $4
				WHERE NOT EXISTS (
					SELECT 1 FROM reads r JOIN books b ON b.id = r.book_id
					WHERE r.user_id = $1 AND b.work_id = $5 AND r.completed_at IS NOT NULL)
				ON CONFLICT (user_id, book_id) DO UPDATE
				SET completed_at = EXCLUDED.completed_at, import_batch_id = EXCLUDED.import_batch_id, updated_at = now()
				WHERE reads.completed_at IS NULL`,
				userID, book, when, rep.BatchID, c.WorkID)
			if err != nil {
				return rep, fmt.Errorf("histimport: прочитано: %w", err)
			}
			tally(tag.RowsAffected(), &rep.Reads, &rep.Kept)
		}
		if it.Shelf == ShelfToRead && !read {
			tag, err := tx.Exec(ctx, `
				WITH shelf AS (
					INSERT INTO user_collections (user_id, name, kind) VALUES ($1, $4, 'to_read')
					ON CONFLICT (user_id) WHERE kind = 'to_read' DO UPDATE SET name = user_collections.name
					RETURNING id
				)
				INSERT INTO user_collection_books (collection_id, book_id, import_batch_id)
				SELECT shelf.id, $2, $3 FROM shelf
				WHERE NOT EXISTS (
					SELECT 1 FROM user_collection_books cb JOIN books b ON b.id = cb.book_id
					WHERE cb.collection_id = shelf.id AND b.work_id = $5)
				  AND NOT EXISTS (
					SELECT 1 FROM reads r JOIN books b ON b.id = r.book_id
					WHERE r.user_id = $1 AND b.work_id = $5 AND r.completed_at IS NOT NULL)
				ON CONFLICT (collection_id, book_id) DO NOTHING`,
				userID, book, rep.BatchID, toReadName, c.WorkID)
			if err != nil {
				return rep, fmt.Errorf("histimport: хочу прочитать: %w", err)
			}
			tally(tag.RowsAffected(), &rep.ToRead, &rep.Kept)
		}
		for _, h := range it.Highlights {
			text, note := clipText(h.Text, maxExcerpt), clipText(h.Note, maxNote)
			if text == "" && note == "" {
				continue
			}
			at := now
			if h.At != nil {
				at = *h.At
			}
			tag, err := tx.Exec(ctx, `
				INSERT INTO annotations (user_id, book_id, kind, cfi, excerpt, note, created_at, updated_at, import_batch_id)
				VALUES ($1, $2, 'highlight', $3, $4, $5, $6, $6, $7)
				ON CONFLICT (user_id, book_id, kind, cfi) DO NOTHING`,
				userID, book, highlightCFI(text, note), text, note, at, rep.BatchID)
			if err != nil {
				return rep, fmt.Errorf("histimport: подчёркивание: %w", err)
			}
			if tag.RowsAffected() > 0 {
				rep.Highlights++
			}
		}
	}
	for _, i := range watch {
		q := wishQuery(res.Items[i])
		norm := recsys.NormalizeQuery(q)
		if len([]rune(norm)) < recsys.MissMinLen {
			continue
		}
		// Желание уже есть (прошлый импорт) — не дублируем и не перехватываем.
		tag, err := tx.Exec(ctx, `
			INSERT INTO wishlist (user_id, q, norm, import_batch_id) VALUES ($1, $2, $3, $4)
			ON CONFLICT (user_id, norm) DO NOTHING`, userID, q, norm, rep.BatchID)
		if err != nil {
			return rep, fmt.Errorf("histimport: список желаний: %w", err)
		}
		rep.Wishlist += int(tag.RowsAffected())
	}
	if _, err := tx.Exec(ctx, `
		UPDATE import_batches SET applied = $2, ratings = $3, reads = $4, to_read = $5, highlights = $6, kept = $7, wishlist = $8
		WHERE id = $1`, rep.BatchID, rep.Applied, rep.Ratings, rep.Reads, rep.ToRead, rep.Highlights, rep.Kept, rep.Wishlist); err != nil {
		return rep, fmt.Errorf("histimport: итоги пакета: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return rep, fmt.Errorf("histimport: commit: %w", err)
	}
	return rep, nil
}

// wishQuery — запрос желания, как его искал бы человек: «название автор»
// (первый автор файла).
func wishQuery(it Item) string {
	q := clipText(it.Title, 300)
	if len(it.Authors) > 0 {
		q += " " + clipText(it.Authors[0], 200)
	}
	return strings.TrimSpace(q)
}

func tally(affected int64, done, kept *int) {
	if affected > 0 {
		*done++
	} else {
		*kept++
	}
}

// highlightCFI — «место» подчёркивания из импорта: по тексту, чтобы повторный
// импорт того же файла не плодил дубли (UNIQUE user, book, kind, cfi).
func highlightCFI(text, note string) string {
	sum := sha1.Sum([]byte(text + "\x00" + note)) //nolint:gosec // ключ, не защита
	return ImportCFIPrefix + hex.EncodeToString(sum[:8])
}

func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// Undo откатывает пакет: удаляет то, что он записал и что пользователь с тех пор
// не подтвердил сам (метка import_batch_id ещё стоит). «Прочитано» снимается;
// строка чтения удаляется, только если в ней больше ничего нет (прогресс,
// «приобретено» — остаются).
func Undo(ctx context.Context, pool *pgxpool.Pool, userID, batchID int64) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("histimport: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		UPDATE import_batches SET undone_at = now()
		WHERE id = $1 AND user_id = $2 AND undone_at IS NULL`, batchID, userID)
	if err != nil {
		return fmt.Errorf("histimport: откат: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBatchNotFound
	}
	// «Прочитано» — двумя запросами: в одном (CTE) DELETE не видит UPDATE.
	rows, err := tx.Query(ctx, `
		UPDATE reads SET completed_at = NULL, import_batch_id = NULL, updated_at = now()
		WHERE user_id = $1 AND import_batch_id = $2
		RETURNING book_id`, userID, batchID)
	if err != nil {
		return fmt.Errorf("histimport: откат прочитанного: %w", err)
	}
	var unread []int64
	if err := forEach(rows, func(r pgx.Rows) error {
		var id int64
		if err := r.Scan(&id); err != nil {
			return err
		}
		unread = append(unread, id)
		return nil
	}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM reads WHERE user_id = $1 AND book_id = ANY($2) AND completed_at IS NULL
		  AND last_pos IS NULL AND fraction IS NULL AND acquired_at IS NULL`, userID, unread); err != nil {
		return fmt.Errorf("histimport: откат прочитанного: %w", err)
	}
	for _, q := range []string{
		`DELETE FROM book_ratings WHERE user_id = $1 AND import_batch_id = $2`,
		`DELETE FROM annotations WHERE user_id = $1 AND import_batch_id = $2`,
		`DELETE FROM wishlist WHERE user_id = $1 AND import_batch_id = $2`,
		`DELETE FROM user_collection_books cb USING user_collections c
		WHERE cb.collection_id = c.id AND c.user_id = $1 AND cb.import_batch_id = $2`,
	} {
		if _, err := tx.Exec(ctx, q, userID, batchID); err != nil {
			return fmt.Errorf("histimport: откат: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("histimport: commit: %w", err)
	}
	return nil
}

// Batches — пакеты пользователя, новые сверху.
func Batches(ctx context.Context, q recsys.Querier, userID int64) ([]Batch, error) {
	rows, err := q.Query(ctx, `
		SELECT id, source, file_name, items, applied, ratings, reads, to_read, highlights, kept, wishlist, applied_at, undone_at
		FROM import_batches WHERE user_id = $1 ORDER BY applied_at DESC, id DESC LIMIT 100`, userID)
	if err != nil {
		return nil, fmt.Errorf("histimport: пакеты: %w", err)
	}
	out := []Batch{}
	err = forEach(rows, func(r pgx.Rows) error {
		var b Batch
		if err := r.Scan(&b.BatchID, &b.Source, &b.FileName, &b.Items, &b.Applied, &b.Ratings, &b.Reads, &b.ToRead,
			&b.Highlights, &b.Kept, &b.Wishlist, &b.AppliedAt, &b.UndoneAt); err != nil {
			return err
		}
		out = append(out, b)
		return nil
	})
	return out, err
}
