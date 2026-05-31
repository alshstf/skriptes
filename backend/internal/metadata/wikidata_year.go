package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// FetchYear — год первого издания произведения из Wikidata (P577 —
// publication date). Реализует YearProvider. Резолвит книгу в QID тем же
// путём, что и адаптации (wbsearchentities по названию + валидация по
// автору через P50), затем берёт минимальный год среди значений P577 —
// это год первого издания произведения.
func (p *WikidataAdaptationsProvider) FetchYear(ctx context.Context, q BookQuery) (int, error) {
	qid, err := p.resolveBookQID(ctx, q)
	if err != nil {
		return 0, err // ErrNotFound пробрасывается как «книга не сопоставлена»
	}
	query := fmt.Sprintf(`SELECT (MIN(YEAR(?date)) AS ?year) WHERE { wd:%s wdt:P577 ?date . }`, qid)
	year, err := p.runSPARQLYear(ctx, query)
	if err != nil {
		return 0, err
	}
	if year < 1000 || year > 2100 {
		return 0, ErrNotFound
	}
	return year, nil
}

// runSPARQLYear — выполняет SPARQL, ожидающий единственное поле ?year
// (агрегат MIN(YEAR(...))). Возвращает ErrNotFound, если года нет.
func (p *WikidataAdaptationsProvider) runSPARQLYear(ctx context.Context, query string) (int, error) {
	body, err := p.doSPARQL(ctx, query)
	if err != nil {
		return 0, err
	}
	defer func() { _ = body.Close() }()

	var resp struct {
		Results struct {
			Bindings []map[string]struct {
				Value string `json:"value"`
			} `json:"bindings"`
		} `json:"results"`
	}
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return 0, fmt.Errorf("decode sparql year: %w", err)
	}
	for _, b := range resp.Results.Bindings {
		if v, ok := b["year"]; ok && v.Value != "" {
			if y, err := strconv.Atoi(v.Value); err == nil {
				return y, nil
			}
		}
	}
	return 0, ErrNotFound
}
