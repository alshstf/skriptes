package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// FantlabProvider — счётчики известности с fantlab.ru (крупнейшая русская база
// фантастики). API полуофициальное (api.fantlab.ru, v0.9, чтение без auth,
// дока — github.com/FantLab/FantLab-API); rate limits не документированы →
// вежливый кламп на стороне воркера (renown_backfill).
//
// Один запрос /search-works?q=<название> = резолв + сигнал: каждый матч сразу
// несёт markcount (число оценок — наш сигнал известности), rusname/altname и
// кириллическое имя автора. Матчинг нативно русский — без транслита и
// src_title-плясок (в отличие от OL/GB, где переводные книги ищутся по оригиналу).
type FantlabProvider struct {
	httpClient *http.Client
	searchURL  string
}

const fantlabSearchURL = "https://api.fantlab.ru/search-works"

// NewFantlabProvider — провайдер с общим UA-транспортом (metadata/httpclient.go).
func NewFantlabProvider(httpClient *http.Client) *FantlabProvider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &FantlabProvider{httpClient: httpClient, searchURL: fantlabSearchURL}
}

// WithEndpoint — подмена URL поиска (для тестов на httptest-сервере).
func (p *FantlabProvider) WithEndpoint(searchURL string) *FantlabProvider {
	if searchURL != "" {
		p.searchURL = searchURL
	}
	return p
}

// Name — строка source в work_renown_lookups.
func (p *FantlabProvider) Name() string { return "fantlab" }

// fantlabMatch — один матч /search-works (реальная форма ответа снята с API
// 2026-07-04; парсим только нужные поля, схема v0.9 может дополняться).
type fantlabMatch struct {
	WorkID          int64  `json:"work_id"`
	RusName         string `json:"rusname"`
	AltName         string `json:"altname"`
	Name            string `json:"name"`
	AutorRusName    string `json:"autor_rusname"`
	AllAutorRusName string `json:"all_autor_rusname"`
	MarkCount       int    `json:"markcount"`
	WorkTypeID      int    `json:"work_type_id"` // тип произведения (справочник fantlabKind)
}

// fantlabKind — маппинг fantlab work_type_id → works.kind. Справочник снят с
// живого API 2026-07-05 (у Фантлаба строгая курируемая типизация — их метка
// надёжнее нашей эвристики):
//   - 3 «сборник» → collection; 17 «антология» (sic, "antology"), 56 «серия
//     антологий» → anthology;
//   - 1 роман, 44 повесть, 45 рассказ, 21 микрорассказ, 5 стихотворение,
//     8 сказка, 41 комикс → "novel": уверенно ОБЫЧНОЕ произведение — снимает
//     ошибочную эвристику (works.kind → NULL);
//   - остальное (4 цикл — это серия, не сборник; 7 прочее; 11/12 эссе/статья;
//     46/49/52 очерк/отрывок/рецензия; незнакомые id) → "" — не решаем.
func fantlabKind(workTypeID int) string {
	switch workTypeID {
	case 3:
		return "collection"
	case 17, 56:
		return "anthology"
	case 1, 44, 45, 21, 5, 8, 41:
		return "novel"
	default:
		return ""
	}
}

// FetchRenown — число оценок произведения (markcount). Precision > recall
// (грабля №13): матч принимается только при совпадении нормализованного
// названия (rusname/altname/name) И автора (гейт authorNameMatches —
// autor_rusname кириллический, наши фамилии тоже). Реализует RenownProvider.
func (p *FantlabProvider) FetchRenown(ctx context.Context, q WorkQuery) (RenownResult, error) {
	title := strings.TrimSpace(q.Title)
	if title == "" {
		return RenownResult{}, ErrNotFound
	}
	v := url.Values{}
	v.Set("q", title)
	v.Set("page", "1")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.searchURL+"?"+v.Encode(), nil)
	if err != nil {
		return RenownResult{}, fmt.Errorf("build fantlab search: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return RenownResult{}, fmt.Errorf("fantlab search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return RenownResult{}, fmt.Errorf("fantlab search: status %d", resp.StatusCode)
	}
	var sr struct {
		Matches []fantlabMatch `json:"matches"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return RenownResult{}, fmt.Errorf("decode fantlab search: %w", err)
	}

	want := normalizeRenownTitle(title)
	gate := AuthorQuery{LastName: q.LastName, FirstName: q.FirstName}
	for _, m := range sr.Matches {
		if m.MarkCount <= 0 || !fantlabTitleMatches(want, m) {
			continue
		}
		if !anyAuthorMatches(gate, fantlabAuthorCandidates(m)) {
			continue
		}
		return RenownResult{Ratings: m.MarkCount, Kind: fantlabKind(m.WorkTypeID)}, nil
	}
	return RenownResult{}, ErrNotFound
}

// fantlabTitleMatches — нормализованное название совпадает с русским/
// альтернативным/оригинальным названием матча.
func fantlabTitleMatches(want string, m fantlabMatch) bool {
	for _, cand := range []string{m.RusName, m.AltName, m.Name} {
		if cand != "" && normalizeRenownTitle(cand) == want {
			return true
		}
	}
	return false
}

// fantlabAuthorCandidates — кириллические имена авторов матча для гейта
// (autor_rusname — primary; all_autor_rusname может нести список через запятую).
func fantlabAuthorCandidates(m fantlabMatch) []string {
	out := make([]string, 0, 4)
	if m.AutorRusName != "" {
		out = append(out, m.AutorRusName)
	}
	for _, part := range strings.Split(m.AllAutorRusName, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// normalizeRenownTitle — lower + trim + схлопывание пробелов + ё→е: ровно
// столько, чтобы пережить разницу регистра/пробелов между нашим каталогом и
// внешним источником, не ослабляя гейт точности.
func normalizeRenownTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ё", "е")
	return strings.Join(strings.Fields(s), " ")
}
