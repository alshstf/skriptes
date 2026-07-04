package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// FetchRenown — число sitelinks Wikidata (в скольких языковых разделах
// Википедии есть статья о книге) — классический прокси мировой известности:
// ГП №1 — 104, «Мастер и Маргарита» — 78, «Азазель» Акунина — 10. Реализует
// RenownProvider (источник "wikidata" воркера renown_backfill).
//
// Двухшаговый: QID → sitelinks. Если QID уже известен (q.WikidataQID из
// works.ext_ids, резолвил Tier-2 группировки) — дорогой резолв по названию
// пропускается; иначе ResolveWorkKey (wbsearchentities + валидация по автору
// P50, precision > recall).
func (p *WikidataAdaptationsProvider) FetchRenown(ctx context.Context, q WorkQuery) (RenownResult, error) {
	qid := q.WikidataQID
	if qid == "" {
		var err error
		qid, err = p.ResolveWorkKey(ctx, q)
		if err != nil {
			return RenownResult{}, err // ErrNotFound пробрасывается как есть
		}
	}
	n, err := p.fetchSitelinksCount(ctx, qid)
	if err != nil {
		return RenownResult{}, err
	}
	if n <= 0 {
		return RenownResult{}, ErrNotFound
	}
	return RenownResult{Sitelinks: n}, nil
}

// fetchSitelinksCount — wbgetentities&props=sitelinks → число ключей sitelinks
// сущности. С 2026 Wikimedia требует User-Agent практически обязательно
// (без него глобальный лимит 10 req/min против 200 с осмысленным UA).
func (p *WikidataAdaptationsProvider) fetchSitelinksCount(ctx context.Context, qid string) (int, error) {
	v := url.Values{}
	v.Set("action", "wbgetentities")
	v.Set("ids", qid)
	v.Set("props", "sitelinks")
	v.Set("format", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.searchURL+"?"+v.Encode(), nil)
	if err != nil {
		return 0, fmt.Errorf("build wbgetentities: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", wdUserAgent)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("wbgetentities: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("wbgetentities: status %d", resp.StatusCode)
	}
	var body struct {
		Entities map[string]struct {
			Sitelinks map[string]json.RawMessage `json:"sitelinks"`
		} `json:"entities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, fmt.Errorf("decode wbgetentities: %w", err)
	}
	ent, ok := body.Entities[qid]
	if !ok {
		return 0, ErrNotFound
	}
	return len(ent.Sitelinks), nil
}
