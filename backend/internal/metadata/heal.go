package metadata

import (
	"context"
)

// cachedFileExists — есть ли файл с таким именем хоть в одном бакете
// (covers/posters/author-photos). Без Touch — для проверок целостности.
func (e *Enricher) cachedFileExists(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range []*CoverCache{e.cache, e.posterCache, e.photoCache} {
		if c != nil && fileExists(c.Path(name)) {
			return true
		}
	}
	return false
}

// HealDanglingAssets зануляет указатели на ОТСУТСТВУЮЩИЕ файлы постеров
// экранизаций (book_adaptations.poster_path) и фото авторов
// (authors.photo_path). Такое бывает после старой очистки/эвикции кэша, когда
// эти нерегенерируемые ассеты лежали вместе с обложками книг и были снесены, а
// указатели остались висячими → битые `?` в UI.
//
// Помимо зануления указателя сбрасывает маркер попытки обогащения
// (adaptations_fetched_at / metadata_fetched_at), чтобы фоновое/ленивое
// дозаполнение вернуло ассет (перекачало постер/фото). Идемпотентно; зовётся
// на старте в горутине.
func (e *Enricher) HealDanglingAssets(ctx context.Context) {
	e.healPhotos(ctx)
	e.healPosters(ctx)
}

func (e *Enricher) healPhotos(ctx context.Context) {
	rows, err := e.pool.Query(ctx,
		`SELECT id, photo_path FROM authors WHERE photo_path IS NOT NULL AND photo_path <> ''`)
	if err != nil {
		e.logger.Warn("heal: query author photos failed", "err", err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		var pp string
		if err := rows.Scan(&id, &pp); err != nil {
			rows.Close()
			e.logger.Warn("heal: scan author photo failed", "err", err)
			return
		}
		if !e.cachedFileExists(pp) {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		e.logger.Warn("heal: iterate author photos failed", "err", err)
		return
	}
	if len(ids) == 0 {
		return
	}
	// Зануляем фото и сбрасываем маркер — ленивый/фоновый путь перекачает.
	if _, err := e.pool.Exec(ctx,
		`UPDATE authors SET photo_path = NULL, metadata_fetched_at = NULL WHERE id = ANY($1)`, ids); err != nil {
		e.logger.Warn("heal: null dangling author photos failed", "err", err)
		return
	}
	e.logger.Info("heal: dangling author photos cleared", "count", len(ids))
}

func (e *Enricher) healPosters(ctx context.Context) {
	rows, err := e.pool.Query(ctx,
		`SELECT book_id, poster_path FROM book_adaptations WHERE poster_path IS NOT NULL AND poster_path <> ''`)
	if err != nil {
		e.logger.Warn("heal: query adaptation posters failed", "err", err)
		return
	}
	var missing []string
	bookSet := map[int64]struct{}{}
	for rows.Next() {
		var bookID int64
		var pp string
		if err := rows.Scan(&bookID, &pp); err != nil {
			rows.Close()
			e.logger.Warn("heal: scan adaptation poster failed", "err", err)
			return
		}
		if !e.cachedFileExists(pp) {
			missing = append(missing, pp)
			bookSet[bookID] = struct{}{}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		e.logger.Warn("heal: iterate adaptation posters failed", "err", err)
		return
	}
	if len(missing) == 0 {
		return
	}
	if _, err := e.pool.Exec(ctx,
		`UPDATE book_adaptations SET poster_path = NULL WHERE poster_path = ANY($1)`, missing); err != nil {
		e.logger.Warn("heal: null dangling posters failed", "err", err)
		return
	}
	books := make([]int64, 0, len(bookSet))
	for id := range bookSet {
		books = append(books, id)
	}
	// Сброс маркера → дозаполнение экранизаций перекачает постеры (saveAdaptations
	// при ON CONFLICT дописывает poster_path в существующие строки).
	if _, err := e.pool.Exec(ctx,
		`UPDATE books SET adaptations_fetched_at = NULL WHERE id = ANY($1)`, books); err != nil {
		e.logger.Warn("heal: reset adaptations marker failed", "err", err)
	}
	e.logger.Info("heal: dangling adaptation posters cleared", "files", len(missing), "books", len(books))
}
