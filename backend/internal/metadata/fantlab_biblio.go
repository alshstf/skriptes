package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Год написания из библиографии автора на Фантлабе (#533). Один запрос
// /autor/{id}?biblio_blocks=1 отдаёт все произведения автора с годом первой
// публикации (иногда — и годом написания); сопоставление названий — локально,
// внутри автора. На сухом прогоне 2026-10-17 (37 авторов из топа): год получили
// 68 % недатированных работ, у уже датированных год Фантлаба совпал с нашим в
// 81 % (±1 год — ещё 6 %). Исследование и план —
// ~/projects/plans/skriptes/written-year-{sources-research,enrichment-plan}.md.

const fantlabAPIBase = "https://api.fantlab.ru"

// fantlabBiblioWork — произведение библиографии Фантлаба.
type fantlabBiblioWork struct {
	WorkID int64
	Names  []string // русское, оригинальное и альтернативные названия
	Year   int      // год написания, иначе первой публикации; 0 — неизвестен
	TypeID int
}

// fantlabBiblioSkipTypes — типы, которые не сопоставляем: циклы и сборники
// (их год — год первой части, у нас это тома-сборники), отрывки, рецензии,
// интервью; 7 — тип без названия в API. Эссе, статьи, очерки — берём: это
// тексты самого автора (у Борхеса их много).
var fantlabBiblioSkipTypes = map[int]bool{
	3: true, 4: true, 7: true, 17: true, 48: true, 49: true, 51: true, 52: true, 56: true,
}

// fantlabBiblioBlockTypes — сборники, циклы, антологии: сами не сопоставляются,
// но их название с ДРУГИМ годом делает совпадение неоднозначным — наша работа
// с таким названием скорее том-сборник по циклу: «Шерлок Холмс (Том 1)» — не
// пьеса «Sherlock Holmes» (альт. «Шерлок Холмс», 1899), а цикл (1887).
var fantlabBiblioBlockTypes = map[int]bool{3: true, 4: true, 17: true, 56: true}

// parseFantlabBiblio — произведения из ответа /autor/{id}?biblio_blocks=1: все
// объекты с work_id и work_name (вложены в блоки и циклы, один и тот же
// work_id встречается несколько раз — берём первое вхождение).
func parseFantlabBiblio(r io.Reader) ([]fantlabBiblioWork, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("decode fantlab biblio: %w", err)
	}
	seen := map[int64]bool{}
	var out []fantlabBiblioWork
	var walk func(any)
	walk = func(x any) {
		switch v := x.(type) {
		case map[string]any:
			if id := jsonInt(v["work_id"]); id > 0 {
				if name, ok := v["work_name"].(string); ok && !seen[int64(id)] {
					seen[int64(id)] = true
					w := fantlabBiblioWork{WorkID: int64(id), TypeID: jsonInt(v["work_type_id"])}
					w.Names = append(w.Names, name)
					if orig, ok := v["work_name_orig"].(string); ok && orig != "" {
						w.Names = append(w.Names, orig)
					}
					if alt, ok := v["work_name_alt"].(string); ok {
						for _, a := range strings.Split(alt, ";") {
							if a = strings.TrimSpace(a); a != "" {
								w.Names = append(w.Names, a)
							}
						}
					}
					w.Year = plausibleYear(jsonInt(v["work_year_of_write"]))
					if w.Year == 0 {
						w.Year = plausibleYear(jsonInt(v["work_year"]))
					}
					out = append(out, w)
				}
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(root)
	return out, nil
}

// jsonInt — целое из json.Number или строки («1922», «1922-1923» → 1922); 0 —
// нет числа.
func jsonInt(v any) int {
	var s string
	switch x := v.(type) {
	case json.Number:
		s = x.String()
	case string:
		s = x
	default:
		return 0
	}
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, err := strconv.Atoi(s[:end])
	if err != nil {
		return 0
	}
	return n
}

var (
	biblioQuotes = strings.NewReplacer("«", "", "»", "", "\"", "", "'", "", "“", "", "”", "", "„", "", "`", "", "´", "", "’", "", "…", "")
	biblioParens = regexp.MustCompile(`\([^)]*\)`)
)

// biblioTitleKey — ключ сопоставления названий: нижний регистр, ё→е, без кавычек,
// пунктуация → пробел, пробелы схлопнуты.
func biblioTitleKey(s string) string {
	s = strings.ReplaceAll(strings.ToLower(biblioQuotes.Replace(s)), "ё", "е")
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			return r
		}
		return ' '
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// biblioTitleVariants — ключи названия: целиком и без скобок («Красная корона.
// (Historia morbi)» ~ «Красная корона»). По точке НЕ режем: «К берегу удачи.
// Кривой домишко…» — том-сборник, склеился бы с первым романом. Ключи короче
// трёх символов не годятся.
func biblioTitleVariants(s string) []string {
	var out []string
	for _, k := range []string{biblioTitleKey(s), biblioTitleKey(biblioParens.ReplaceAllString(s, " "))} {
		if len([]rune(k)) >= 3 && (len(out) == 0 || out[0] != k) {
			out = append(out, k)
		}
	}
	return out
}

// FantlabWorkQuery — наша работа для сопоставления: название и названия
// оригинала её изданий (src_title переводов).
type FantlabWorkQuery struct {
	ID        int64
	Title     string
	SrcTitles []string
}

// FantlabYearMatch — работа, однозначно найденная в библиографии.
type FantlabYearMatch struct {
	WorkID   int64 // наша
	Year     int   // 0 — найдена, но у Фантлаба нет года
	FlWorkID int64 // 0 — одно название у нескольких записей (id неоднозначен)
}

// matchFantlabBiblio — наши работы, однозначно найденные в библиографии:
// ключи названия (и src_title изданий) совпали с названиями ровно одного
// произведения Фантлаба (подходящего типа). Несколько произведений с одним
// названием и разными годами — не решаем (точность важнее полноты).
func matchFantlabBiblio(works []FantlabWorkQuery, biblio []fantlabBiblioWork) []FantlabYearMatch {
	idx, block := map[string][]int{}, map[string][]int{}
	for i, w := range biblio {
		into := idx
		switch {
		case fantlabBiblioBlockTypes[w.TypeID]:
			into = block
		case fantlabBiblioSkipTypes[w.TypeID]:
			continue
		}
		seen := map[string]bool{}
		for _, n := range w.Names {
			for _, k := range biblioTitleVariants(n) {
				if !seen[k] {
					seen[k] = true
					into[k] = append(into[k], i)
				}
			}
		}
	}
	var out []FantlabYearMatch
	for _, w := range works {
		hits, blockers := map[int]bool{}, map[int]bool{}
		for _, t := range append([]string{w.Title}, w.SrcTitles...) {
			for _, k := range biblioTitleVariants(t) {
				for _, i := range idx[k] {
					hits[i] = true
				}
				for _, i := range block[k] {
					blockers[i] = true
				}
			}
		}
		if len(hits) == 0 {
			continue
		}
		years := map[int]bool{}
		var pick int
		for i := range hits {
			pick = i
			if biblio[i].Year > 0 {
				years[biblio[i].Year] = true
			}
		}
		blocked := false
		for i := range blockers {
			if y := biblio[i].Year; y > 0 && !years[y] {
				blocked = true
			}
		}
		switch {
		case len(years) > 1, blocked:
			continue // разные произведения с одним названием (или том-сборник цикла)
		case len(hits) > 1:
			// Одно название у нескольких записей с одним годом (или без года) —
			// работа найдена, но FantLab-id неоднозначен.
			out = append(out, FantlabYearMatch{WorkID: w.ID, Year: firstYear(years)})
		default:
			out = append(out, FantlabYearMatch{WorkID: w.ID, Year: biblio[pick].Year, FlWorkID: biblio[pick].WorkID})
		}
	}
	return out
}

func firstYear(m map[int]bool) int {
	for y := range m {
		return y
	}
	return 0
}

// FantlabAuthorQuery — наш автор: имя для поиска и FantLab ID, если известен
// (Wikidata P7433).
type FantlabAuthorQuery struct {
	LastName, FirstName string
	FantlabID           int64
}

// FantlabAuthorYears — итог по автору: какая страница Фантлаба (via:
// "wikidata" — по P7433, "search" — поиском по имени с гейтом) и найденные
// работы.
type FantlabAuthorYears struct {
	FantlabID int64
	Via       string
	Matched   []FantlabYearMatch
}

// fantlabSearchGate — сколько наших работ должно найтись в библиографии
// кандидата поиска, чтобы считать его нашим автором: поиск по имени неточен,
// тёзки и чужие совпадения отсекает только пересечение названий.
const fantlabSearchGate = 3

// fantlabSearchCandidates — сколько кандидатов поиска проверять библиографией.
const fantlabSearchCandidates = 2

// AuthorYears — годы работ автора по его библиографии на Фантлабе. wait
// вызывается перед каждым запросом (лимит запросов воркера). ErrNotFound —
// автора на Фантлабе нет, страница закрыта или поиск не прошёл гейт;
// ErrUpstream — сбой источника (повторить позже).
func (p *FantlabProvider) AuthorYears(ctx context.Context, a FantlabAuthorQuery, works []FantlabWorkQuery, wait func(context.Context) error) (FantlabAuthorYears, error) {
	if wait == nil {
		wait = func(context.Context) error { return nil }
	}
	biblio := func(id int64) ([]fantlabBiblioWork, error) {
		if err := wait(ctx); err != nil {
			return nil, err
		}
		return p.FantlabAuthorBiblio(ctx, id)
	}
	if a.FantlabID > 0 {
		b, err := biblio(a.FantlabID)
		if err != nil {
			return FantlabAuthorYears{}, err
		}
		return FantlabAuthorYears{FantlabID: a.FantlabID, Via: "wikidata", Matched: matchFantlabBiblio(works, b)}, nil
	}
	last := biblioTitleKey(a.LastName)
	if len([]rune(last)) < 2 || strings.TrimSpace(a.FirstName) == "" || len(works) < fantlabSearchGate {
		return FantlabAuthorYears{}, ErrNotFound
	}
	if err := wait(ctx); err != nil {
		return FantlabAuthorYears{}, err
	}
	hits, err := p.FantlabSearchAuthors(ctx, strings.TrimSpace(a.FirstName+" "+a.LastName))
	if err != nil {
		return FantlabAuthorYears{}, err
	}
	tried := 0
	for _, h := range hits {
		if tried == fantlabSearchCandidates {
			break
		}
		if !strings.Contains(biblioTitleKey(h.RusName), last) {
			continue
		}
		tried++
		b, err := biblio(h.ID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return FantlabAuthorYears{}, err
		}
		if m := matchFantlabBiblio(works, b); len(m) >= fantlabSearchGate {
			return FantlabAuthorYears{FantlabID: h.ID, Via: "search", Matched: m}, nil
		}
	}
	return FantlabAuthorYears{}, ErrNotFound
}

// fantlabAuthorHit — кандидат поиска /search-autors.
type fantlabAuthorHit struct {
	ID      int64
	RusName string
}

// FantlabAuthorBiblio — библиография автора (ErrNotFound — страницы нет или она
// закрыта: у части классиков вне фантастики API отдаёт 404).
func (p *FantlabProvider) FantlabAuthorBiblio(ctx context.Context, authorID int64) ([]fantlabBiblioWork, error) {
	body, err := p.getJSON(ctx, fmt.Sprintf("%s/autor/%d?biblio_blocks=1", p.apiBase(), authorID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	return parseFantlabBiblio(body)
}

// FantlabSearchAuthors — кандидаты поиска автора по имени. Поиск видит только
// открытые страницы и бывает неточен («Незнанский» → Мария Семёнова), поэтому
// результат — только кандидаты: принимает их гейт по совпадению названий.
func (p *FantlabProvider) FantlabSearchAuthors(ctx context.Context, name string) ([]fantlabAuthorHit, error) {
	body, err := p.getJSON(ctx, p.apiBase()+"/search-autors?"+url.Values{"q": {name}}.Encode())
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	var sr struct {
		Matches []struct {
			AutorID json.Number `json:"autor_id"`
			RusName string      `json:"rusname"`
		} `json:"matches"`
	}
	if err := json.NewDecoder(body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("decode fantlab author search: %w", err)
	}
	out := make([]fantlabAuthorHit, 0, len(sr.Matches))
	for _, m := range sr.Matches {
		if id, err := m.AutorID.Int64(); err == nil && id > 0 {
			out = append(out, fantlabAuthorHit{ID: id, RusName: m.RusName})
		}
	}
	return out, nil
}

func (p *FantlabProvider) apiBase() string {
	if p.biblioBase != "" {
		return p.biblioBase
	}
	return fantlabAPIBase
}

// WithAPIBase — подмена базы API библиографий (для тестов на httptest-сервере).
func (p *FantlabProvider) WithAPIBase(base string) *FantlabProvider {
	p.biblioBase = strings.TrimRight(base, "/")
	return p
}

func (p *FantlabProvider) getJSON(ctx context.Context, u string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build fantlab request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fantlab: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, statusErr(resp.StatusCode)
	}
	return resp.Body, nil
}

// FantlabAuthorIDs — FantLab author ID (Wikidata P7433) по QID авторов, одним
// SPARQL на пачку. Нет свойства — нет ключа в ответе.
func (p *WikidataAdaptationsProvider) FantlabAuthorIDs(ctx context.Context, qids []string) (map[string]int64, error) {
	out := map[string]int64{}
	var vals []string
	for _, q := range qids {
		if qidPattern.MatchString(q) {
			vals = append(vals, "wd:"+q)
		}
	}
	if len(vals) == 0 {
		return out, nil
	}
	body, err := p.doSPARQL(ctx, `SELECT ?a ?fl WHERE { VALUES ?a { `+strings.Join(vals, " ")+` } ?a wdt:P7433 ?fl . }`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	var resp struct {
		Results struct {
			Bindings []struct {
				A  struct{ Value string } `json:"a"`
				Fl struct{ Value string } `json:"fl"`
			} `json:"bindings"`
		} `json:"results"`
	}
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode fantlab author ids: %w", err)
	}
	for _, b := range resp.Results.Bindings {
		qid := b.A.Value[strings.LastIndex(b.A.Value, "/")+1:]
		if id, err := strconv.ParseInt(b.Fl.Value, 10, 64); err == nil && id > 0 {
			out[qid] = id
		}
	}
	return out, nil
}

var qidPattern = regexp.MustCompile(`^Q[1-9][0-9]*$`)
