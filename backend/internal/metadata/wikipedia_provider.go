package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
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

	// candidateCheck — политика приёма кандидата (candidate_policy.go, см.
	// resolveTitle). nil = выключена (принимается всё, что прошло гейт имени).
	// Инъектируется WithCandidateCheck из main (NewCandidateCheck поверх
	// WikidataAdaptationsProvider.CandidateFacts). Функция, а не прямая зависимость
	// на Wikidata-провайдер: разрыв связности + тестируемость.
	candidateCheck CandidateCheck

	// titles — найденная статья (или «не найдено») по автору и разделу: путь фото
	// идёт следом за путём био и повторил бы поиск, QID и проверку кандидата.
	titles *ttlCache[titleResult]
}

type titleResult struct {
	title    string
	notFound bool
}

// wikiUserAgent — Wikimedia требует осмысленный User-Agent на REST API,
// иначе блокирует или отдаёт пустые ответы без явной ошибки. Формат
// рекомендован https://meta.wikimedia.org/wiki/User-Agent_policy.
const wikiUserAgent = "skriptes/0.1 (https://github.com/alshstf/skriptes; metadata-enricher)"

func NewWikipediaProvider(httpClient *http.Client) *WikipediaProvider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &WikipediaProvider{httpClient: httpClient, titles: newTTLCache[titleResult](lookupCacheTTL, lookupCacheSize)}
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

// WithCandidateCheck включает политику приёма кандидата: после гейта имени
// статья проходит проверку по фактам Wikidata (профессия, годы, книги) и профилю
// книг автора. nil (по умолчанию) = выключена. Провязка в main.
func (p *WikipediaProvider) WithCandidateCheck(fn CandidateCheck) *WikipediaProvider {
	p.candidateCheck = fn
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
			traceRequestError(ctx, "wikipedia", lang, err)
			failed = keepTransient(failed, err)
			continue
		}
		if text != "" {
			return text, nil
		}
	}
	return "", notFoundOr(failed)
}

// traceRequestError — сбой запроса (не «не найдено») в трассу.
func traceRequestError(ctx context.Context, source, lang string, err error) {
	if err != nil && !errors.Is(err, ErrNotFound) {
		traceStep(ctx, TraceStep{Source: source, Lang: lang, Stage: "request", Outcome: TraceError, Value: err.Error()})
	}
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
	q = latinFor(ctx, "wikipedia", lang, q)
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
		traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "extract", Outcome: TraceReject, Input: title, Value: "missing page"})
		return "", ErrNotFound
	}
	page := body.Query.Pages[0]
	if _, ok := page.PageProps["disambiguation"]; ok {
		if !q.Strict() {
			traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "disambiguation", Outcome: TraceInfo, Input: page.Title, Value: "retry as namesake"})
			return p.intro(ctx, lang, asNamesake(q))
		}
		traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "disambiguation", Outcome: TraceReject, Input: page.Title})
		return "", ErrNotFound
	}
	// Редирект мог увести на другого человека («Флинт, Александра» →
	// «Флит, Александр») — имя проверяем и у итоговой статьи (articleIsAuthor), а
	// псевдоним принимаем, если полный текст статьи называет автора.
	if !articleIsAuthor(q, page.Title, page.Extract) {
		ok, err := p.redirectNamesAuthor(ctx, lang, title, page.Title, q)
		if err != nil {
			return "", err
		}
		if !ok {
			traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "article_is_author", Outcome: TraceReject, Input: page.Title, Value: articleLead(page.Extract)})
			return "", ErrNotFound
		}
	}
	text := strings.TrimSpace(page.Extract)
	if text == "" {
		traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "extract", Outcome: TraceReject, Input: page.Title, Value: "empty extract"})
		return "", nil
	}
	traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "accept", Outcome: TracePass, Value: page.Title})
	return text, nil
}

func (p *WikipediaProvider) FetchAuthorPhoto(ctx context.Context, q AuthorQuery) (*CoverImage, error) {
	var failed error
	for _, lang := range p.langs(q.Lang) {
		src, err := p.photoSource(ctx, lang, q)
		if err != nil {
			failed = keepTransient(failed, err)
			continue
		}
		if src == "" {
			continue
		}
		img, err := p.downloadImage(ctx, src)
		if err != nil {
			traceRequestError(ctx, "wikipedia", lang, err)
			failed = keepTransient(failed, err)
			continue
		}
		return img, nil
	}
	return nil, notFoundOr(failed)
}

// AuthorPhotoSource — адрес фото автора без скачивания (сухой прогон, #280):
// тот же выбор статьи и языка, что у FetchAuthorPhoto.
func (p *WikipediaProvider) AuthorPhotoSource(ctx context.Context, q AuthorQuery) (string, error) {
	var failed error
	for _, lang := range p.langs(q.Lang) {
		src, err := p.photoSource(ctx, lang, q)
		if err != nil {
			failed = keepTransient(failed, err)
			continue
		}
		if src != "" {
			return src, nil
		}
	}
	return "", notFoundOr(failed)
}

// photoSource — адрес миниатюры статьи об авторе в одном языке; "" — у статьи
// нет картинки.
func (p *WikipediaProvider) photoSource(ctx context.Context, lang string, q AuthorQuery) (string, error) {
	s, err := p.summary(ctx, lang, q)
	if err != nil {
		traceRequestError(ctx, "wikipedia", lang, err)
		return "", err
	}
	if s.Thumbnail.Source == "" {
		traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "thumbnail", Outcome: TraceReject, Input: s.Title, Value: "no thumbnail"})
		return "", nil
	}
	traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "accept", Outcome: TracePass, Input: s.Title, Value: s.Thumbnail.Source})
	return s.Thumbnail.Source, nil
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

// redirectNamesAuthor — название, найденное по имени автора, оказалось
// перенаправлением на статью под другим именем, и начало статьи автора не называет
// (псевдоним: «Йовил, Джек» → «Ньюман, Ким», «Сайер, Ги» → «Мумину, Ги»). Такую
// статью принимаем, если автора называет её полный текст: псевдоним упоминают
// дальше начала. Редирект на однофамильца («Флинт» → «Флит») автора не называет.
func (p *WikipediaProvider) redirectNamesAuthor(ctx context.Context, lang, from, to string, q AuthorQuery) (bool, error) {
	if from == "" || to == "" || from == to {
		return false, nil
	}
	v := url.Values{}
	v.Set("action", "query")
	v.Set("prop", "extracts")
	v.Set("explaintext", "1")
	v.Set("titles", to)
	v.Set("format", "json")
	v.Set("formatversion", "2")
	var body struct {
		Query struct {
			Pages []struct {
				Extract string `json:"extract"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := p.apiGet(ctx, lang, v, &body); err != nil {
		return false, err
	}
	if len(body.Query.Pages) == 0 || !mentionsAuthor(body.Query.Pages[0].Extract, q) {
		return false, nil
	}
	traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "pseudonym", Outcome: TracePass, Input: from, Value: to})
	return true, nil
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
	q = latinFor(ctx, "wikipedia", lang, q)
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
			traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "disambiguation", Outcome: TraceInfo, Input: s.Title, Value: "retry as namesake"})
			return p.summary(ctx, lang, asNamesake(q))
		}
		traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "disambiguation", Outcome: TraceReject, Input: s.Title})
		return nil, ErrNotFound
	}
	// summary идёт по редиректу — имя проверяем и у итоговой статьи (#280).
	if !articleIsAuthor(q, s.Title, s.Extract) {
		ok, err := p.redirectNamesAuthor(ctx, lang, title, s.Title, q)
		if err != nil {
			return nil, err
		}
		if !ok {
			traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "article_is_author", Outcome: TraceReject, Input: s.Title, Value: articleLead(s.Extract)})
			return nil, ErrNotFound
		}
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
	key := lang + "|" + q.cacheKey()
	if r, ok := p.titles.get(key); ok {
		if r.notFound {
			traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "cached", Outcome: TraceReject, Value: "not found a moment ago"})
			return "", ErrNotFound
		}
		traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "cached", Outcome: TraceInfo, Value: r.title})
		return r.title, nil
	}
	title, err := p.resolveTitleUncached(ctx, lang, q)
	switch {
	case err == nil:
		p.titles.put(key, titleResult{title: title})
	case errors.Is(err, ErrNotFound):
		p.titles.put(key, titleResult{notFound: true})
	}
	return title, err
}

// resolveTitleUncached — поиск статьи без кэша (см. resolveTitle).
func (p *WikipediaProvider) resolveTitleUncached(ctx context.Context, lang string, q AuthorQuery) (string, error) {
	if q.Strict() {
		title, match, err := p.resolveStrictTitle(ctx, lang, q)
		if err != nil {
			return "", err
		}
		return p.acceptCandidate(ctx, lang, q, title, match)
	}
	title, match, namesakes, err := p.resolveByName(ctx, lang, q)
	if errors.Is(err, errNamesakes) {
		// Имени соответствуют несколько статей без уточнения — у автора есть тёзки:
		// первый результат поиска был бы просто самым известным из них (#280).
		title, match, err = p.resolveStrictTitle(ctx, lang, asNamesake(q))
		if err != nil {
			return "", err
		}
		return p.acceptCandidate(ctx, lang, q, title, match)
	}
	if err != nil {
		return "", err
	}
	got, err := p.acceptCandidate(ctx, lang, q, title, match)
	if errors.Is(err, ErrNotFound) && namesakes {
		// Основная статья (без уточнения) не подошла, а рядом есть статьи об
		// одноимённых людях с уточнением — наш автор может быть среди них:
		// строгий путь по книгам («Иванов, Юрий Иванович» футболист → «… (писатель)»).
		traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "namesakes", Outcome: TraceInfo, Input: title, Value: "primary rejected — retry as namesake"})
		title, match, err = p.resolveStrictTitle(ctx, lang, asNamesake(q))
		if err != nil {
			return "", err
		}
		return p.acceptCandidate(ctx, lang, q, title, match)
	}
	return got, err
}

// acceptCandidate — политика приёма (candidate_policy.go): гейт имени пропускает
// тёзку с тем же ФИО — решают профессия, годы, книги кандидата в Wikidata и
// профиль книг автора. Сбой запроса (429, сеть) — временная ошибка, а не «принять»:
// автор перепроверится позже (#280, грабля №20).
func (p *WikipediaProvider) acceptCandidate(ctx context.Context, lang string, q AuthorQuery, title string, match MatchKind) (string, error) {
	if p.candidateCheck == nil {
		if match == MatchLoose {
			return "", ErrNotFound // нестрогое имя без проверки книгой не принимаем
		}
		return title, nil
	}
	qid, err := p.resolvePageQID(ctx, lang, title)
	if err != nil {
		return "", fmt.Errorf("%w: resolve wikidata qid: %w", ErrUpstream, err)
	}
	traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "qid", Outcome: TraceInfo, Input: title, Value: qid})
	ok, err := p.candidateCheck(ctx, q, "wikipedia", lang, title, qid, match)
	if err != nil {
		return "", fmt.Errorf("%w: candidate check: %w", ErrUpstream, err)
	}
	if !ok {
		return "", ErrNotFound
	}
	return title, nil
}

// latinFor — запрос для раздела на латинице (queryForLang) с шагом трассы, если
// имя заменено на латинское.
func latinFor(ctx context.Context, source, lang string, q AuthorQuery) AuthorQuery {
	l := queryForLang(q, lang)
	if l.FullName != q.FullName {
		traceStep(ctx, TraceStep{Source: source, Lang: lang, Stage: "latin", Outcome: TraceInfo, Input: q.FullName, Value: l.FullName})
	}
	return l
}

// searchLimit — сколько результатов поиска смотреть на каждую форму имени.
const searchLimit = 5

// errNamesakes — имени автора соответствуют несколько разных статей.
var errNamesakes = errors.New("several articles match the author name")

// searchForms — формы имени для поиска статьи (case study #280): полное «Фамилия
// Имя Отчество» (у русских авторов на него есть редиректы, и оно точнее всего),
// «Фамилия Имя» (второго имени иностранца в названии статьи нет: по «Виндж Вернор
// Стефан» статья «Виндж, Вернор» не находится) и «Имя Фамилия» — так названы
// статьи под псевдонимами и о китайских авторах («Софи Кинселла», «Ли Чайлд»,
// «Лю Цысинь» — в каталоге «Цысинь Лю»).
func searchForms(q AuthorQuery) []string {
	last := strings.TrimSpace(q.LastName)
	first := strings.TrimSpace(q.FirstName)
	middle := strings.TrimSpace(q.MiddleName)
	if last == "" || first == "" {
		return []string{strings.TrimSpace(q.FullName)}
	}
	forms := make([]string, 0, 3)
	if middle != "" {
		forms = append(forms, last+" "+first+" "+middle)
	}
	return append(forms, last+" "+first, first+" "+last)
}

// resolveByName — статья по имени автора: формы имени по очереди, до searchLimit
// результатов на форму, гейт имени на каждый. Решает первая форма, давшая
// совпадения: одно — это оно, несколько — тёзки (errNamesakes). Раньше брался
// только первый результат по полному имени — «Генри О» находил «Генри Лайон Олди»,
// а «Кинселла Софи» не находил ничего.
func (p *WikipediaProvider) resolveByName(ctx context.Context, lang string, q AuthorQuery) (string, MatchKind, bool, error) {
	seen := false
	var all []string
	for _, form := range searchForms(q) {
		titles, err := p.opensearch(ctx, lang, form, searchLimit)
		if err != nil {
			return "", MatchName, false, err
		}
		all = append(all, titles...)
		var matched, primary []string
		for _, t := range titles {
			if !authorNameMatches(q, t) || slices.Contains(matched, t) || isDisambiguationTitle(t) {
				continue
			}
			matched = append(matched, t)
			if _, qual := splitQualifier(t); qual == "" {
				primary = append(primary, t)
			}
		}
		switch {
		case len(matched) == 1:
			traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "name_gate", Outcome: TracePass, Input: form, Value: matched[0]})
			return matched[0], MatchName, false, nil
		case len(primary) == 1:
			// Основная статья без уточнения и одноимённые с уточнением («Тургенев, Иван
			// Сергеевич» и «… (учёный)»): по соглашению Википедии без уточнения —
			// главный носитель имени. Берём её; не подойдёт — строгий путь.
			traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "name_gate", Outcome: TracePass, Input: form,
				Value: primary[0] + " (+" + strconv.Itoa(len(matched)-1) + " с уточнением)"})
			return primary[0], MatchName, true, nil
		case len(matched) > 1:
			traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "namesakes", Outcome: TraceInfo, Input: form, Value: strings.Join(matched, " | ")})
			return "", MatchName, true, errNamesakes
		case len(titles) > 0:
			seen = true
			traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "name_gate", Outcome: TraceReject, Input: form, Value: titles[0]})
		}
	}
	// Ни одна статья не прошла гейт имени — может, имя передано иначе (Ширли/Шерли,
	// Гуидо/Гвидо, Глик/Глейк). Единственный такой кандидат идёт дальше с пометкой:
	// политика примет его только с книгой автора в Wikidata.
	var loose []string
	for _, t := range all {
		if looseNameMatches(q, t) && !slices.Contains(loose, t) {
			loose = append(loose, t)
		}
	}
	if len(loose) == 1 {
		traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "name_loose", Outcome: TraceInfo, Input: q.FullName, Value: loose[0]})
		return loose[0], MatchLoose, false, nil
	}
	if !seen {
		traceStep(ctx, TraceStep{Source: "wikipedia", Lang: lang, Stage: "opensearch", Outcome: TraceReject, Input: q.FullName})
	}
	return "", MatchName, false, ErrNotFound
}

// isDisambiguationTitle — название страницы неоднозначности по уточнению
// («Достоевский, Фёдор Михайлович (значения)»): не кандидат и не тёзка.
func isDisambiguationTitle(t string) bool {
	_, qual := splitQualifier(t)
	q := strings.ToLower(qual)
	return q == "значения" || q == "disambiguation"
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
