package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

// Поиск автора с тёзками (AuthorQuery.Strict, грабля №22). Поля «уточнение» ни у
// Википедии, ни у OpenLibrary нет, первый результат поиска по имени — просто
// самый известный из тёзок. Поэтому кандидат принимается, только если его
// подтвердило что-то кроме имени:
//  1. уточнение как уточнение статьи Википедии — ruwiki различает тёзок так же:
//     «Антоний [Блум]» → статья «Антоний (Блум)»;
//  2. кандидат из поиска по имени, чьё уточнение в названии совпадает с нашим
//     по основе слова («писатель-маринист» ~ «(писатель)»);
//  3. одна из книг автора: статья, найденная полнотекстовым поиском по фамилии и
//     названию книги (Википедия), или автор книги с этим названием (OpenLibrary).
//
// Не подтвердилось ничем — ErrNotFound: пустая карточка лучше чужой биографии.

// strictBookTitles — сколько названий книг пробовать для якоря.
const strictBookTitles = 4

// resolveStrictTitle — статья Википедии об авторе с тёзками (см. doc файла).
// Кроме подтверждения (MatchConfirmed) возвращает статью с точным названием для
// однословного имени (MatchName): древние и восточные авторы («Тукарам»,
// «Алкуин», «Терпандр») по книге не находятся, а статья о них называется так же;
// политика примет её, только если это человек с пишущей профессией или темой книг.
func (p *WikipediaProvider) resolveStrictTitle(ctx context.Context, lang string, q AuthorQuery) (string, MatchKind, error) {
	note := strings.TrimSpace(q.Note)
	if note != "" && !numericNote(note) {
		for _, base := range wikiTitleBases(q) {
			title, ok, err := p.articleTitle(ctx, lang, base+" ("+note+")")
			if err != nil {
				return "", MatchName, err
			}
			if ok {
				traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "strict.note_title", Outcome: TracePass, Input: base + " (" + note + ")", Value: title})
				return title, MatchConfirmed, nil
			}
		}
		titles, err := p.opensearch(ctx, lang, q.FullName, 10)
		if err != nil {
			return "", MatchName, err
		}
		for _, t := range titles {
			if _, qual := splitQualifier(t); qual != "" && noteMatchesQualifier(note, qual) && authorNameMatches(q, t) {
				traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "strict.note_qualifier", Outcome: TracePass, Input: note, Value: t})
				return t, MatchConfirmed, nil
			}
		}
		traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "strict.note", Outcome: TraceInfo, Input: note, Value: strings.Join(titles, " | ")})
	}
	for i, book := range q.BookTitles {
		if i >= strictBookTitles {
			break
		}
		hits, err := p.searchText(ctx, lang, fmt.Sprintf("%q %q", q.LastName, book), 5)
		if err != nil {
			return "", MatchName, err
		}
		for _, h := range hits {
			// Имя — по основе названия: «Старый пруд (Басё)» — статья о хайку,
			// а не о поэте, хотя «Басё» в уточнении есть (выборка с прода, #280).
			if base, _ := splitQualifier(h); authorNameMatches(q, base) {
				traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "strict.book", Outcome: TracePass, Input: book, Value: h})
				return h, MatchConfirmed, nil
			}
		}
		traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "strict.book", Outcome: TraceInfo, Input: book, Value: strings.Join(hits, " | ")})
	}
	if note == "" && !q.Namesakes && strings.TrimSpace(q.FirstName) == "" && strings.TrimSpace(q.LastName) != "" {
		title, ok, err := p.articleTitle(ctx, lang, strings.TrimSpace(q.LastName))
		if err != nil {
			return "", MatchName, err
		}
		if ok && authorNameMatches(q, title) {
			traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "strict.exact_title", Outcome: TraceInfo, Input: q.LastName, Value: title})
			return title, MatchName, nil
		}
	}
	traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "strict", Outcome: TraceReject, Input: strictWhy(q),
		Value: fmt.Sprintf("note=%q books=%d", note, len(q.BookTitles))})
	return "", MatchName, ErrNotFound
}

// strictWhy — почему автор пошёл строгим путём (для трассы).
func strictWhy(q AuthorQuery) string {
	var why []string
	if strings.TrimSpace(q.Note) != "" {
		why = append(why, "note")
	}
	if q.Namesakes {
		why = append(why, "namesakes")
	}
	if strings.TrimSpace(q.LastName) != "" && strings.TrimSpace(q.FirstName) == "" {
		why = append(why, "one-word name")
	}
	return strings.Join(why, ",")
}

// wikiTitleBases — как ruwiki называет статьи о людях: «Фамилия, Имя Отчество»,
// «Фамилия, Имя», а у монашеских и одиночных имён — просто «Антоний».
func wikiTitleBases(q AuthorQuery) []string {
	last := strings.TrimSpace(q.LastName)
	first := strings.TrimSpace(q.FirstName)
	middle := strings.TrimSpace(q.MiddleName)
	if last == "" {
		return nil
	}
	var out []string
	if first != "" && middle != "" {
		out = append(out, last+", "+first+" "+middle)
	}
	if first != "" {
		out = append(out, last+", "+first)
	}
	if first == "" {
		out = append(out, last)
	}
	return out
}

// articleTitle — есть ли статья с таким названием (с учётом перенаправлений) и
// не страница ли это неоднозначности; возвращает итоговое название.
func (p *WikipediaProvider) articleTitle(ctx context.Context, lang, title string) (string, bool, error) {
	v := url.Values{}
	v.Set("action", "query")
	v.Set("titles", title)
	v.Set("redirects", "1")
	v.Set("prop", "pageprops")
	v.Set("ppprop", "disambiguation")
	v.Set("format", "json")
	v.Set("formatversion", "2")
	var body struct {
		Query struct {
			Pages []struct {
				Title     string          `json:"title"`
				Missing   bool            `json:"missing"`
				Invalid   bool            `json:"invalid"`
				PageProps json.RawMessage `json:"pageprops"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := p.apiGet(ctx, lang, v, &body); err != nil {
		return "", false, err
	}
	for _, pg := range body.Query.Pages {
		if pg.Missing || pg.Invalid || pg.Title == "" {
			continue
		}
		if strings.Contains(string(pg.PageProps), "disambiguation") {
			continue
		}
		return pg.Title, true, nil
	}
	return "", false, nil
}

// searchText — полнотекстовый поиск статей (list=search), названия хитов.
func (p *WikipediaProvider) searchText(ctx context.Context, lang, query string, limit int) ([]string, error) {
	v := url.Values{}
	v.Set("action", "query")
	v.Set("list", "search")
	v.Set("srsearch", query)
	v.Set("srnamespace", "0")
	v.Set("srlimit", fmt.Sprint(limit))
	v.Set("srprop", "")
	v.Set("format", "json")
	v.Set("formatversion", "2")
	var body struct {
		Query struct {
			Search []struct {
				Title string `json:"title"`
			} `json:"search"`
		} `json:"query"`
	}
	if err := p.apiGet(ctx, lang, v, &body); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(body.Query.Search))
	for _, h := range body.Query.Search {
		out = append(out, h.Title)
	}
	return out, nil
}

// apiGet — GET к /w/api.php с декодированием JSON.
func (p *WikipediaProvider) apiGet(ctx context.Context, lang string, v url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL(lang)+"/w/api.php?"+v.Encode(), nil)
	if err != nil {
		return fmt.Errorf("build wikipedia request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", wikiUserAgent)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("wikipedia api: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return statusErr(resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode wikipedia response: %w", err)
	}
	return nil
}

// strictAuthorKey — OLID автора с тёзками: автор книги с одним из названий
// книг нашего автора, прошедший имя-гейт. OpenLibrary уточнений не знает,
// поэтому здесь работает только якорь по книгам.
func (p *OpenLibraryProvider) strictAuthorKey(ctx context.Context, q AuthorQuery) (string, error) {
	base := p.workBaseURL()
	for i, book := range q.BookTitles {
		if i >= strictBookTitles {
			break
		}
		v := url.Values{}
		v.Set("title", book)
		v.Set("fields", "author_key,author_name")
		v.Set("limit", "10")
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/search.json?"+v.Encode(), nil)
		if err != nil {
			return "", fmt.Errorf("build ol book search: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		resp, err := p.httpClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("ol book search: %w", err)
		}
		var sr struct {
			Docs []struct {
				AuthorKey  []string `json:"author_key"`
				AuthorName []string `json:"author_name"`
			} `json:"docs"`
		}
		status := resp.StatusCode
		if status == http.StatusOK {
			err = json.NewDecoder(resp.Body).Decode(&sr)
		}
		_ = resp.Body.Close()
		if status != http.StatusOK {
			return "", statusErr(status)
		}
		if err != nil {
			return "", fmt.Errorf("decode ol book search: %w", err)
		}
		for _, d := range sr.Docs {
			for j, name := range d.AuthorName {
				if j < len(d.AuthorKey) && authorNameMatches(q, name) {
					key := strings.TrimPrefix(d.AuthorKey[j], "/authors/")
					traceStep(ctx, TraceStep{Source: "openlibrary", Stage: "strict.book", Outcome: TracePass, Input: book, Value: name + " " + key})
					return key, nil
				}
			}
		}
		traceStep(ctx, TraceStep{Source: "openlibrary", Stage: "strict.book", Outcome: TraceInfo, Input: book, Value: fmt.Sprintf("%d docs", len(sr.Docs))})
	}
	traceStep(ctx, TraceStep{Source: "openlibrary", Stage: "strict", Outcome: TraceReject, Input: strictWhy(q), Value: fmt.Sprintf("books=%d", len(q.BookTitles))})
	return "", ErrNotFound
}

// numericNote — уточнение-номер вида «#17465» (что он значит в librusec, не
// подтверждено — во внешних источниках его не ищем).
func numericNote(note string) bool {
	return len(note) > 1 && note[0] == '#' && strings.Trim(note[1:], "0123456789") == ""
}

// splitQualifier — «Блинов, Николай Николаевич (писатель)» → (база, «писатель»).
func splitQualifier(title string) (base, qual string) {
	title = strings.TrimSpace(title)
	if !strings.HasSuffix(title, ")") {
		return title, ""
	}
	i := strings.LastIndex(title, " (")
	if i < 0 {
		return title, ""
	}
	return strings.TrimSpace(title[:i]), strings.TrimSpace(title[i+2 : len(title)-1])
}

// noteMatchesQualifier — уточнение из INPX и уточнение статьи говорят об одном:
// общее слово или общая основа от 5 букв («писатель-маринист» ~ «писатель»,
// «журналист» ~ «журналистка»). Короткие слова — только целиком.
func noteMatchesQualifier(note, qual string) bool {
	nw, qw := letterWords(note), letterWords(qual)
	for _, a := range nw {
		for _, b := range qw {
			if a == b {
				return true
			}
			ra, rb := []rune(a), []rune(b)
			if len(ra) >= 5 && len(rb) >= 5 && string(ra[:5]) == string(rb[:5]) {
				return true
			}
		}
	}
	return false
}

func letterWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(strings.ReplaceAll(s, "ё", "е")), func(r rune) bool {
		return !unicode.IsLetter(r)
	})
}
