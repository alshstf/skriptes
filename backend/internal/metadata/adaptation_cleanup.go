package metadata

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// screenKinds — виды экранизаций, которые хранятся (#295, screenKind).
var screenKinds = []string{"film", "tv_series", "miniseries", "anime"}

// CleanupNonScreenAdaptations — разовая чистка book_adaptations до правил #295:
// удаляет не-экранизации (kind other — оперы, игры, песни; 37 % на проде) и
// записи с голым QID вместо названия. Книги, где среди удалённого мог оказаться
// настоящий фильм или сериал (у записи «другого» есть ссылка на Кинопоиск или
// IMDb — прежний код брал первую метку P31 и путал сериалы; запись без
// названия — сервис подписей спрашивал только ru и en), получают сброшенный
// маркер adaptations_fetched_at: воркер «Экранизации» или открытие карточки
// перепросят их по новым правилам. Возвращает работы с удалёнными записями
// (известность и works-индекс) и число книг к перепросу.
func CleanupNonScreenAdaptations(ctx context.Context, pool *pgxpool.Pool) (works []int64, refetch int64, err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		UPDATE books SET adaptations_fetched_at = NULL
		WHERE id IN (
		    SELECT book_id FROM book_adaptations
		    WHERE (kind <> ALL($1) AND (ext_url LIKE '%kinopoisk.ru%' OR ext_url LIKE '%imdb.com%'))
		       OR title ~ '^Q[0-9]+$')`, screenKinds)
	if err != nil {
		return nil, 0, fmt.Errorf("reset adaptations marker: %w", err)
	}
	refetch = tag.RowsAffected()
	rows, err := tx.Query(ctx, `
		WITH del AS (
		    DELETE FROM book_adaptations
		    WHERE kind <> ALL($1) OR title ~ '^Q[0-9]+$'
		    RETURNING book_id
		)
		SELECT DISTINCT b.work_id FROM del JOIN books b ON b.id = del.book_id WHERE b.work_id IS NOT NULL`, screenKinds)
	if err != nil {
		return nil, 0, fmt.Errorf("delete non-screen adaptations: %w", err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, 0, err
		}
		works = append(works, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, 0, err
	}
	return works, refetch, nil
}

// RecheckAdaptationsByAuthorQID — разовый перепроход промахов после проверки
// автора по QID (#570): до неё кандидата-книгу в Wikidata подтверждали буквами
// имени, и автор с другой передачей имени («Херберт Фрэнк» против «Фрэнк
// Герберт») оставался без экранизаций навсегда — маркер single-shot. Сбрасывает
// adaptations_fetched_at у живых книг без экранизаций, у которых хотя бы один
// автор с QID, — только у известных работ (QID или sitelinks Wikidata, ≥ 50
// оценок Фантлаба или ≥ 2 изданий): у них 99 % найденных экранизаций, а у
// остальных их почти нет (прод 2026-10-23: 57 тыс. книг из 227 тыс. под первые
// два условия). Перепросят воркер «Экранизации» и открытие карточки. Возвращает
// число книг к перепросу.
func RecheckAdaptationsByAuthorQID(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE books b SET adaptations_fetched_at = NULL
		FROM works w
		WHERE w.id = b.work_id AND b.deleted = false
		  AND b.adaptations_fetched_at IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM book_adaptations x WHERE x.book_id = b.id)
		  AND EXISTS (SELECT 1 FROM book_authors ba JOIN authors a ON a.id = ba.author_id
		              WHERE ba.book_id = b.id AND a.ext_ids ? 'wd_qid')
		  AND (w.ext_ids ? 'wd_qid' OR w.wd_sitelinks IS NOT NULL
		       OR w.fantlab_marks >= 50 OR w.edition_count >= 2)`)
	if err != nil {
		return 0, fmt.Errorf("reset adaptations marker: %w", err)
	}
	return tag.RowsAffected(), nil
}
