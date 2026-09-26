package importer

import (
	"context"

	"github.com/skriptes/skriptes/backend/internal/inpx"
)

// cacheSet — набор in-memory кэшей внутри одного запуска Run.
// Авторы дедуплицируются по нормализованному имени, серии — по
// (norm_title, author_id), жанры — по fb2 коду, архивы — по имени.
//
// Кэши обнуляются с каждым новым Run — в норме одного запуска не хватает,
// чтобы съесть RAM (на 500K книг ожидается ~50K уникальных авторов и
// ~10K серий, что укладывается в десятки МБ).
//
// Двухуровневый: id, полученный внутри транзакции записи, сначала лежит в staged и
// попадает в общий кэш только после Commit (commitStaged). Каждая запись — своя
// транзакция; при откате вставленные в ней строки исчезают, и id из отката в общем
// кэше — ссылка на несуществующую строку: все следующие книги того же автора/серии
// падали бы на FK (прод-кейс 2026-09-26: 3 откаченные записи дали ещё 19 FK-ошибок,
// 22 книги не попали в каталог).
type cacheSet struct {
	cacheMaps
	staged cacheMaps
}

type cacheMaps struct {
	author  map[string]int64     // normalized name → id
	series  map[seriesKey]int64  // (norm title, author id) → id
	genre   map[string]int64     // fb2 code → id
	archive map[archiveKey]int64 // (collection_id, filename) → id
}

type seriesKey struct {
	norm     string
	authorID int64 // 0 если без автора
}

type archiveKey struct {
	collectionID int64
	filename     string
}

func newCacheMaps(author, series, genre, archive int) cacheMaps {
	return cacheMaps{
		author:  make(map[string]int64, author),
		series:  make(map[seriesKey]int64, series),
		genre:   make(map[string]int64, genre),
		archive: make(map[archiveKey]int64, archive),
	}
}

func newCaches() *cacheSet {
	return &cacheSet{
		cacheMaps: newCacheMaps(1024, 256, 256, 64),
		staged:    newCacheMaps(8, 2, 8, 1),
	}
}

// commitStaged переносит id из закоммиченной транзакции записи в общий кэш.
func (c *cacheSet) commitStaged() {
	for k, v := range c.staged.author {
		c.author[k] = v
	}
	for k, v := range c.staged.series {
		c.series[k] = v
	}
	for k, v := range c.staged.genre {
		c.genre[k] = v
	}
	for k, v := range c.staged.archive {
		c.archive[k] = v
	}
	c.dropStaged()
}

// dropStaged забывает id из откаченной транзакции записи.
func (c *cacheSet) dropStaged() {
	clear(c.staged.author)
	clear(c.staged.series)
	clear(c.staged.genre)
	clear(c.staged.archive)
}

func (c *cacheSet) ensureAuthor(ctx context.Context, q querier, a inpx.Author) (int64, error) {
	key := normalizedAuthorName(a)
	if id, ok := c.author[key]; ok {
		return id, nil
	}
	if id, ok := c.staged.author[key]; ok {
		return id, nil
	}
	id, err := upsertAuthor(ctx, q, a)
	if err != nil {
		return 0, err
	}
	c.staged.author[key] = id
	return id, nil
}

func (c *cacheSet) ensureSeries(ctx context.Context, q querier, title string, authorID int64) (int64, error) {
	key := seriesKey{norm: normalize(title), authorID: authorID}
	if id, ok := c.series[key]; ok {
		return id, nil
	}
	if id, ok := c.staged.series[key]; ok {
		return id, nil
	}
	id, err := upsertSeries(ctx, q, title, authorID)
	if err != nil {
		return 0, err
	}
	c.staged.series[key] = id
	return id, nil
}

func (c *cacheSet) ensureGenre(ctx context.Context, q querier, code string) (int64, error) {
	if id, ok := c.genre[code]; ok {
		return id, nil
	}
	if id, ok := c.staged.genre[code]; ok {
		return id, nil
	}
	id, err := upsertGenre(ctx, q, code)
	if err != nil {
		return 0, err
	}
	c.staged.genre[code] = id
	return id, nil
}

func (c *cacheSet) ensureArchive(ctx context.Context, q querier, collectionID int64, filename string) (int64, error) {
	key := archiveKey{collectionID: collectionID, filename: filename}
	if id, ok := c.archive[key]; ok {
		return id, nil
	}
	if id, ok := c.staged.archive[key]; ok {
		return id, nil
	}
	id, err := upsertArchive(ctx, q, collectionID, filename)
	if err != nil {
		return 0, err
	}
	c.staged.archive[key] = id
	return id, nil
}
