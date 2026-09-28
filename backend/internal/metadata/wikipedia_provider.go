package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// WikipediaProvider — био+фото авторов через Wikipedia REST API.
//
// Алгоритм:
//  1. opensearch на ru.wikipedia.org / en.wikipedia.org с полным именем
//     → получаем точное название страницы (например "Достоевский,_Фёдор_Михайлович").
//  2. /api/rest_v1/page/summary/{title} → JSON c "extract" (био) и
//     "thumbnail.source" (URL картинки).
//
// Сначала пробуем язык q.Lang (с дефолтом ru), потом — английский.
// Это даёт нормальный хит-rate для русских и переводных авторов.
type WikipediaProvider struct {
	httpClient *http.Client
	apiRoot    string // override для тестов; продакшен — пустая (используем https://{lang}.wikipedia.org)

	// occupationGate — слой 2 точности (см. resolveTitle). nil = выключен.
	// Инъектируется WithOccupationGate из main (реализация —
	// WikidataAdaptationsProvider.OccupationVerdict). Отдельная функция, а не
	// прямая зависимость на Wikidata-провайдер: разрыв связности + тестируемость.
	occupationGate func(ctx context.Context, qid string) (OccupationVerdict, error)
}

// wikiUserAgent — Wikimedia требует осмысленный User-Agent на REST API,
// иначе блокирует или отдаёт пустые ответы без явной ошибки. Формат
// рекомендован https://meta.wikimedia.org/wiki/User-Agent_policy.
const wikiUserAgent = "skriptes/0.1 (https://github.com/alshstf/skriptes; metadata-enricher)"

func NewWikipediaProvider(httpClient *http.Client) *WikipediaProvider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &WikipediaProvider{httpClient: httpClient}
}

// WithAPIRoot переопределяет корень API (для httptest-серверов).
// Формат: "http://127.0.0.1:1234" — без trailing slash; провайдер
// сам добавит "/w/api.php" и "/api/rest_v1/page/summary/...".
//
// При непустом apiRoot язык игнорируется — мы пользуемся одним
// сервером для всех "языков" в тестах.
func (p *WikipediaProvider) WithAPIRoot(root string) *WikipediaProvider {
	p.apiRoot = root
	return p
}

// WithOccupationGate включает слой 2 точности: после имя-гейта резолв автора
// дополнительно спрашивает Wikidata о профессии (P106) кандидата и отвергает
// явных не-писателей. nil-функция (по умолчанию) = гейт выключен. Реализация —
// WikidataAdaptationsProvider.OccupationVerdict; провязка в main.
func (p *WikipediaProvider) WithOccupationGate(fn func(ctx context.Context, qid string) (OccupationVerdict, error)) *WikipediaProvider {
	p.occupationGate = fn
	return p
}

func (p *WikipediaProvider) Name() string { return "wikipedia" }

// FetchAuthorBio — полный intro-раздел статьи через extracts API
// (action=query&prop=extracts&exintro=1&explaintext=1). summary endpoint
// возвращает только первые 1-2 предложения; для нормальной биографии
// нужен весь preamble — обычно 500-2000 символов, "родился, учился,
// написал, умер".
//
// Сначала пробуем родной язык автора (или ru по умолчанию), потом en.
func (p *WikipediaProvider) FetchAuthorBio(ctx context.Context, q AuthorQuery) (string, error) {
	var failed error
	for _, lang := range p.langs(q.Lang) {
		text, err := p.intro(ctx, lang, q)
		if err != nil {
			failed = keepTransient(failed, err)
			continue
		}
		if text != "" {
			return text, nil
		}
	}
	return "", notFoundOr(failed)
}

// keepTransient запоминает первую ошибку, которая не «не найдено».
func keepTransient(prev, err error) error {
	if prev == nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return prev
}

// notFoundOr — итог перебора языков: если где-то был сбой (429, сеть,
// проверка профессии), это не «не найдено» — иначе автор навсегда останется
// без био (грабля №20, #280).
func notFoundOr(failed error) error {
	if failed == nil {
		return ErrNotFound
	}
	if errors.Is(failed, ErrUpstream) {
		return failed
	}
	return fmt.Errorf("%w: %w", ErrUpstream, failed)
}

// intro — полный текст intro-раздела через MediaWiki action API.
//
//	GET /w/api.php?action=query&prop=extracts&exintro=1&explaintext=1
//	    &exsectionformat=plain&titles={Title}&format=json
//
// Returns plain-text без HTML, заголовков и сносок. exintro=1 ограничивает
// первой секцией статьи (до первого ==Heading==), что для биографических
// статей даёт идеальный preamble.
func (p *WikipediaProvider) intro(ctx context.Context, lang string, q AuthorQuery) (string, error) {
	title, err := p.resolveTitle(ctx, lang, q)
	if err != nil {
		return "", err
	}
	if title == "" {
		return "", ErrNotFound
	}

	v := url.Values{}
	v.Set("action", "query")
	// pageprops.disambiguation — страница неоднозначности («Козлов, Василий»):
	// у её QID нет профессии, гейт P106 её пропускал, и в био уходил список
	// тёзок (#280). В пути фото то же отсекает summary().
	v.Set("prop", "extracts|pageprops")
	v.Set("ppprop", "disambiguation")
	v.Set("exintro", "1")
	v.Set("explaintext", "1")
	v.Set("exsectionformat", "plain")
	v.Set("redirects", "1")
	v.Set("titles", title)
	v.Set("format", "json")
	v.Set("formatversion", "2") // v2 — pages как массив, удобнее парсить

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL(lang)+"/w/api.php?"+v.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("build extracts: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", wikiUserAgent)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("wikipedia extracts: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", statusErr(resp.StatusCode)
	}

	var body struct {
		Query struct {
			Pages []struct {
				Title     string            `json:"title"`
				Missing   bool              `json:"missing"`
				Extract   string            `json:"extract"`
				PageProps map[string]string `json:"pageprops"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode extracts: %w", err)
	}
	if len(body.Query.Pages) == 0 || body.Query.Pages[0].Missing {
		return "", ErrNotFound
	}
	page := body.Query.Pages[0]
	if _, ok := page.PageProps["disambiguation"]; ok {
		if !q.Strict() {
			return p.intro(ctx, lang, asNamesake(q))
		}
		return "", ErrNotFound
	}
	// Редирект мог увести на другого человека («Флинт, Александра» →
	// «Флит, Александр») — имя проверяем и у итоговой статьи (articleIsAuthor).
	if !articleIsAuthor(q, page.Title, page.Extract) {
		return "", ErrNotFound
	}
	return strings.TrimSpace(page.Extract), nil
}

func (p *WikipediaProvider) FetchAuthorPhoto(ctx context.Context, q AuthorQuery) (*CoverImage, error) {
	var failed error
	for _, lang := range p.langs(q.Lang) {
		s, err := p.summary(ctx, lang, q)
		if err != nil {
			failed = keepTransient(failed, err)
			continue
		}
		if s.Thumbnail.Source == "" {
			continue
		}
		img, err := p.downloadImage(ctx, s.Thumbnail.Source)
		if err != nil {
			failed = keepTransient(failed, err)
			continue
		}
		return img, nil
	}
	return nil, notFoundOr(failed)
}

// articleIsAuthor — итоговая статья (после редиректа) о нашем авторе:
//   - имя совпадает с названием статьи;
//   - или его называет начало статьи — там полное имя героя, в том числе
//     латиницей: «Ри́чард Мэ́тисон (англ. Richard Burton Matheson…)» для
//     «Матесон Ричард» (сверка между алфавитами допускает одну букву);
//   - или статья под псевдонимом называет автора («Акунин, Борис» — «настоящее
//     имя — Григорий Шалвович Чхартишвили»).
func articleIsAuthor(q AuthorQuery, title, extract string) bool {
	if title == "" {
		return true
	}
	return authorNameMatches(q, title) || authorNameMatches(q, articleLead(extract)) || mentionsAuthor(extract, q)
}

// articleLead — начало статьи, где Википедия называет героя полным именем.
func articleLead(extract string) string {
	const leadRunes = 200
	r := []rune(extract)
	if len(r) > leadRunes {
		r = r[:leadRunes]
	}
	return string(r)
}

// asNamesake — по имени автора в Википедии нашлась страница неоднозначности
// («Дюма, Александр»: отец и сын) — значит, у него есть тёзки, и статью ищем как
// для тёзки: по уточнению и книгам (resolveStrictTitle). Без этого автор с
// известным тёзкой оставался без био и фото.
func asNamesake(q AuthorQuery) AuthorQuery {
	q.Namesakes = true
	return q
}

// summary — opensearch для точного титла + summary endpoint.
func (p *WikipediaProvider) summary(ctx context.Context, lang string, q AuthorQuery) (*wikiSummary, error) {
	title, err := p.resolveTitle(ctx, lang, q)
	if err != nil {
		return nil, err
	}
	if title == "" {
		return nil, ErrNotFound
	}

	summaryURL := p.baseURL(lang) + "/api/rest_v1/page/summary/" + url.PathEscape(title)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, summaryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build summary request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", wikiUserAgent)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wikipedia summary: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, statusErr(resp.StatusCode)
	}
	var s wikiSummary
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return nil, fmt.Errorf("decode summary: %w", err)
	}
	// disambiguation-страницы (type="disambiguation") нам бесполезны —
	// extract там обычно общий типа "может означать...".
	if s.Type == "disambiguation" {
		if !q.Strict() {
			return p.summary(ctx, lang, asNamesake(q))
		}
		return nil, ErrNotFound
	}
	// summary идёт по редиректу — имя проверяем и у итоговой статьи (#280).
	if !articleIsAuthor(q, s.Title, s.Extract) {
		return nil, ErrNotFound
	}
	return &s, nil
}

// resolveTitle — через opensearch получает первый match И проверяет, что он
// правдоподобно совпадает с искомым автором (совпадает имя, а не только
// фамилия). Без проверки opensearch по «Гарднер Лиза» вернул бы «Иван Гарднер»
// (однофамилец) — мы бы показали чужие био/фото. Лучше «не нашли».
// У автора с тёзками или уточнением (q.Strict) первый результат по имени не
// годится вовсе — там resolveStrictTitle (подтверждение уточнением или книгой).
func (p *WikipediaProvider) resolveTitle(ctx context.Context, lang string, q AuthorQuery) (string, error) {
	var title string
	if q.Strict() {
		t, err := p.resolveStrictTitle(ctx, lang, q)
		if err != nil {
			return "", err
		}
		title = t
	} else {
		titles, err := p.opensearch(ctx, lang, q.FullName, 1)
		if err != nil {
			return "", err
		}
		if len(titles) == 0 {
			return "", ErrNotFound
		}
		// Гейт по имени: первый хит opensearch матчит только фамилию — проверяем,
		// что совпадает и имя. Не совпало → считаем «не нашли» (см. doc выше).
		if !authorNameMatches(q, titles[0]) {
			return "", ErrNotFound
		}
		title = titles[0]
	}
	// Слой 2 (опционально): проверка профессии P106. Имя-гейт пропускает
	// однофамильцев-не-писателей (полное совпадение ФИО у писателя и его тёзки
	// другой профессии). Резолвим страницу → Wikidata QID → occupationGate.
	// Отвергаем ТОЛЬКО при явном не-писателе; нет QID / профессия не размечена —
	// оставляем (precision-preserving: не режем валидных без размеченной
	// профессии). Сбой запроса (429, сеть) — временная ошибка, а не «принять»:
	// автор перепроверится позже (#280, грабля №20).
	if p.occupationGate != nil {
		qid, err := p.resolvePageQID(ctx, lang, title)
		if err != nil {
			return "", fmt.Errorf("%w: resolve wikidata qid: %w", ErrUpstream, err)
		}
		if qid != "" {
			verdict, err := p.occupationGate(ctx, qid)
			if err != nil {
				return "", fmt.Errorf("%w: occupation: %w", ErrUpstream, err)
			}
			if verdict == OccupationNonWriter {
				return "", ErrNotFound
			}
		}
	}
	return title, nil
}

// opensearch — названия статей по префиксу/похожести (namespace 0).
func (p *WikipediaProvider) opensearch(ctx context.Context, lang, search string, limit int) ([]string, error) {
	v := url.Values{}
	v.Set("action", "opensearch")
	v.Set("search", search)
	v.Set("limit", strconv.Itoa(limit))
	v.Set("namespace", "0") // только статьи, не категории
	v.Set("format", "json")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL(lang)+"/w/api.php?"+v.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build opensearch: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", wikiUserAgent)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wikipedia opensearch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, statusErr(resp.StatusCode)
	}
	// opensearch отдаёт массив [string, []string, []string, []string]:
	// [запрос, [титулы], [сниппеты], [ссылки]]. Декодим как json.RawMessage'ы.
	var arr []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&arr); err != nil {
		return nil, fmt.Errorf("decode opensearch: %w", err)
	}
	if len(arr) < 2 {
		return nil, nil
	}
	var titles []string
	if err := json.Unmarshal(arr[1], &titles); err != nil {
		return nil, fmt.Errorf("decode titles: %w", err)
	}
	return titles, nil
}

// resolvePageQID — Wikidata QID статьи через MediaWiki pageprops
// (prop=pageprops&ppprop=wikibase_item). Нужен слою 2, чтобы спросить о
// профессии сущности. Пустой QID (у страницы нет Wikidata-связи) — не ошибка,
// вызывающий трактует как «не проверить».
func (p *WikipediaProvider) resolvePageQID(ctx context.Context, lang, title string) (string, error) {
	v := url.Values{}
	v.Set("action", "query")
	v.Set("prop", "pageprops")
	v.Set("ppprop", "wikibase_item")
	v.Set("redirects", "1")
	v.Set("titles", title)
	v.Set("format", "json")
	v.Set("formatversion", "2")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL(lang)+"/w/api.php?"+v.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("build pageprops: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", wikiUserAgent)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("wikipedia pageprops: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", statusErr(resp.StatusCode)
	}
	var body struct {
		Query struct {
			Pages []struct {
				PageProps struct {
					WikibaseItem string `json:"wikibase_item"`
				} `json:"pageprops"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode pageprops: %w", err)
	}
	if len(body.Query.Pages) == 0 {
		return "", nil
	}
	return body.Query.Pages[0].PageProps.WikibaseItem, nil
}

func (p *WikipediaProvider) downloadImage(ctx context.Context, src string) (*CoverImage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, fmt.Errorf("build image request: %w", err)
	}
	req.Header.Set("User-Agent", wikiUserAgent)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wikipedia image: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, statusErr(resp.StatusCode)
	}
	mime := resp.Header.Get("Content-Type")
	if mime == "" || !strings.HasPrefix(mime, "image/") {
		mime = "image/jpeg"
	}
	return &CoverImage{
		Reader:   resp.Body,
		Mime:     mime,
		SourceID: "wp:" + src,
	}, nil
}

// langs — в каком порядке пробовать языковые Wikipedia.
// Логика: сначала "родной" (q.Lang), потом противоположный из ru/en.
// Если q.Lang пустой — ru приоритетнее (наш каталог преимущественно
// русскоязычный).
func (p *WikipediaProvider) langs(qLang string) []string {
	pref := strings.ToLower(qLang)
	switch pref {
	case "en":
		return []string{"en", "ru"}
	case "ru":
		return []string{"ru", "en"}
	default:
		return []string{"ru", "en"}
	}
}

// baseURL — корень API для конкретного языка. При apiRoot != "" (тест)
// возвращаем его как есть.
func (p *WikipediaProvider) baseURL(lang string) string {
	if p.apiRoot != "" {
		return p.apiRoot
	}
	return "https://" + lang + ".wikipedia.org"
}

type wikiSummary struct {
	Title     string `json:"title"`
	Type      string `json:"type"` // "standard" / "disambiguation" / ...
	Extract   string `json:"extract"`
	Thumbnail struct {
		Source string `json:"source"`
	} `json:"thumbnail"`
}
