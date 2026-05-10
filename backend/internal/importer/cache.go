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
type cacheSet struct {
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

func newCaches() *cacheSet {
	return &cacheSet{
		author:  make(map[string]int64, 1024),
		series:  make(map[seriesKey]int64, 256),
		genre:   make(map[string]int64, 256),
		archive: make(map[archiveKey]int64, 64),
	}
}

func (c *cacheSet) ensureAuthor(ctx context.Context, q querier, a inpx.Author) (int64, error) {
	key := normalizedAuthorName(a)
	if id, ok := c.author[key]; ok {
		return id, nil
	}
	id, err := upsertAuthor(ctx, q, a)
	if err != nil {
		return 0, err
	}
	c.author[key] = id
	return id, nil
}

func (c *cacheSet) ensureSeries(ctx context.Context, q querier, title string, authorID int64) (int64, error) {
	key := seriesKey{norm: normalize(title), authorID: authorID}
	if id, ok := c.series[key]; ok {
		return id, nil
	}
	id, err := upsertSeries(ctx, q, title, authorID)
	if err != nil {
		return 0, err
	}
	c.series[key] = id
	return id, nil
}

func (c *cacheSet) ensureGenre(ctx context.Context, q querier, code string) (int64, error) {
	if id, ok := c.genre[code]; ok {
		return id, nil
	}
	id, err := upsertGenre(ctx, q, code)
	if err != nil {
		return 0, err
	}
	c.genre[code] = id
	return id, nil
}

func (c *cacheSet) ensureArchive(ctx context.Context, q querier, collectionID int64, filename string) (int64, error) {
	key := archiveKey{collectionID: collectionID, filename: filename}
	if id, ok := c.archive[key]; ok {
		return id, nil
	}
	id, err := upsertArchive(ctx, q, collectionID, filename)
	if err != nil {
		return 0, err
	}
	c.archive[key] = id
	return id, nil
}
