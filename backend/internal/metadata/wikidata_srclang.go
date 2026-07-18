package metadata

import (
	"context"
	"encoding/json"
	"fmt"
)

// FetchSrcLang — язык оригинала произведения из Wikidata (P407 «язык
// произведения») как ISO 639-1 код (P218 у сущности языка). Реализует
// SrcLangProvider. Резолвит книгу в QID тем же путём, что адаптации/год
// (wbsearchentities по названию + валидация по автору через P50).
//
// ⚠️ P407 шумный: у мультиязычных произведений бывает НЕСКОЛЬКО значений
// («Война и мир» — ru И fr: в тексте много французского), а сам он означает
// «язык произведения или названия», не строго оригинал. Precision-гейт:
// принимаем ТОЛЬКО когда P407 даёт РОВНО ОДИН distinct ISO-код — иначе
// ErrNotFound (не гадаем, лучше пусто, чем неверный «оригинал»). Языки без
// P218 (нет ISO 639-1) в выдачу не попадают by construction.
//
// Второй гейт — «оригинал ≠ язык издания» — живёт в воркере
// (src_lang_backfill.go): это политика записи, не выборки.
func (p *WikidataAdaptationsProvider) FetchSrcLang(ctx context.Context, q BookQuery) (string, error) {
	qid, err := p.resolveBookQID(ctx, q)
	if err != nil {
		return "", err // ErrNotFound — книга не сопоставлена
	}
	query := fmt.Sprintf(
		`SELECT DISTINCT ?code WHERE { wd:%s wdt:P407 ?lang . ?lang wdt:P218 ?code . } LIMIT 5`, qid)
	codes, err := p.runSPARQLValues(ctx, query, "code")
	if err != nil {
		return "", err
	}
	if len(codes) != 1 {
		// 0 — P407 не размечен; ≥2 — неоднозначно (мультиязычное произведение).
		return "", ErrNotFound
	}
	code := normalizeLangCode(codes[0])
	if code == "" {
		return "", ErrNotFound
	}
	return code, nil
}

// runSPARQLValues — исполняет SPARQL и собирает все значения одного поля.
// Общий помощник для запросов вида SELECT DISTINCT ?x WHERE {...}.
func (p *WikidataAdaptationsProvider) runSPARQLValues(ctx context.Context, query, field string) ([]string, error) {
	body, err := p.doSPARQL(ctx, query)
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("decode sparql %s: %w", field, err)
	}
	out := make([]string, 0, len(resp.Results.Bindings))
	for _, b := range resp.Results.Bindings {
		if v, ok := b[field]; ok && v.Value != "" {
			out = append(out, v.Value)
		}
	}
	return out, nil
}
