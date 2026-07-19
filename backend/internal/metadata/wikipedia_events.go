package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Wikipedia-вехи био-таймлайна (PR-2 плана cryptic-roaming-turing): rule-based
// «предложение с годом» по биографической зоне статьи. Wikidata даёт
// «ЗАГС-скелет» (13–19 фактов у классиков), но арест/каторга/травля там
// отсутствуют даже у Достоевского — драматургию середины жизни добирает текст
// Википедии (замер 2026-07: 40–90 настоящих вех у топ-классика после фильтров).
// Цитаты — CC BY-SA, атрибуция обязательна (url ведёт на статью).

// FetchAuthorMilestones — вехи из статьи {lang}.wikipedia.org/{title}:
// полный plain-text одной выборкой (prop=extracts, explaintext;
// exsectionformat=wiki оставляет заголовки строками «== Биография ==» —
// биозона скоупится по ним без отдельного запроса prop=sections), дальше
// чистый пайплайн extractWikiMilestones. birth/death — годы жизни автора
// из Wikidata-скелета (lifespan-фильтр).
func (p *WikipediaProvider) FetchAuthorMilestones(ctx context.Context, lang, title string, birth, death int) ([]AuthorEvent, error) {
	v := url.Values{}
	v.Set("action", "query")
	v.Set("prop", "extracts")
	v.Set("explaintext", "1")
	v.Set("exsectionformat", "wiki")
	v.Set("redirects", "1")
	v.Set("titles", title)
	v.Set("format", "json")
	v.Set("formatversion", "2")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL(lang)+"/w/api.php?"+v.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build fulltext: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", wikiUserAgent)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: wikipedia fulltext: %v", ErrUpstream, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, statusErr(resp.StatusCode)
	}

	var body struct {
		// ⚠️ MediaWiki action API отдаёт транзиентные сбои (readonly-окно,
		// internal_api_error_DBQueryError, maxlag) как HTTP 200 + error-JSON
		// БЕЗ query — по статусу их не отличить. Без этой проверки транзиент
		// схлопнулся бы в ErrNotFound → single-shot маркер events_fetched_at
		// встал бы навсегда (грабля №20).
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Query struct {
			Pages []struct {
				Missing bool   `json:"missing"`
				Extract string `json:"extract"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode fulltext: %w", err)
	}
	if body.Error.Code != "" {
		return nil, fmt.Errorf("%w: wikipedia api error: %s", ErrUpstream, body.Error.Code)
	}
	if len(body.Query.Pages) == 0 || body.Query.Pages[0].Missing {
		return nil, ErrNotFound
	}

	articleURL := p.baseURL(lang) + "/wiki/" + url.PathEscape(strings.ReplaceAll(title, " ", "_"))
	return extractWikiMilestones(lang, articleURL, body.Query.Pages[0].Extract, birth, death), nil
}

// --- чистый пайплайн (тестируется без HTTP) ---

var (
	wikiHeadingRe = regexp.MustCompile(`^(==+)\s*(.*?)\s*==+\s*$`)
	// Био-зона: заголовки 2-го уровня о жизни (не «Библиография»/«Память»).
	bioHeadingRe = regexp.MustCompile(`(?i)биограф|жизн|детств|юност|молодые годы|последние годы|творч|biography|life|childhood|youth|career`)
	yearRe       = regexp.MustCompile(`\b(1[0-9]{3}|20[0-2][0-9])\b`)

	// Библиографический мусор: издания, тома, страницы (ловит списки
	// литературы, просочившиеся в extract).
	biblioRe = regexp.MustCompile(`ISBN|\bТ\.\s*\d|\bС\.\s*\d|\d+\s*с\.($|\s)|[МЛ]\.\s*:|СПб\.\s*:| : [^,]{1,40},\s*1?\d{3}`)
	// Посмертное/потомки: события не жизни автора (память, музеи, внуки).
	posthumousRe = regexp.MustCompile(`(?i)внук|правнук|потомк|памятник|музе[йея]|посмертн|некролог|надгроб|перезахорон|названа? в честь|named after|monument|posthumous|obituary`)
)

// classifyMilestone — тип события по ключевым словам предложения. Порядок —
// по убыванию веса (арест в военном году → persecution, не war). Не распознано
// → other (вес 1: показывается, но критерий «не скучно» не набивает).
var milestoneClasses = []struct {
	typ string
	re  *regexp.Regexp
}{
	{EventPersecution, regexp.MustCompile(`(?i)арест|каторг|ссылк|ссыльн|тюрь|тюрем|лагер|травл|гонени|обыск|цензур|запрет|высла|выслан|заключ[её]н|arrest|prison|exile|persecut|censor|labor camp`)},
	{EventWar, regexp.MustCompile(`(?i)войн|фронт|битв|сражени|оборон|мобилиз|призван в арми|ополчени|\bwar\b|front|battle|enlist|military service|drafted`)},
	{EventLoss, regexp.MustCompile(`(?i)умер|умерл|скончал|смерть|погиб|похорон|утрат|died|death of|funeral|lost his|lost her`)},
	{EventIsolation, regexp.MustCompile(`(?i)карантин|затвор|уединени|изоляц|quarantine|seclusion|isolation`)},
	{EventPoverty, regexp.MustCompile(`(?i)нищет|нищен|долг(и|ов|ах)|бедност|безденеж|разор[еи]|poverty|debt|bankrupt|destitut`)},
	// ⚠️ \w в Go-regexp — ASCII-only, кириллицу НЕ матчит: только \S*/явные классы.
	{EventSpiritual, regexp.MustCompile(`(?i)духовн\S* кризис|кризис вер|отлуч[её]н|религиозн\S* обращени|обратился к вере|постриг|excommunicat|religious crisis|conversion`)},
	{EventIllness, regexp.MustCompile(`(?i)болезн|заболел|болел|туберкул[её]з|чахотк|эпилепс|инсульт|операци|госпитал|лечени|ill(ness)?\b|disease|tuberculosis|epilep|hospital|surgery`)},
	{EventLove, regexp.MustCompile(`(?i)женил|женитьб|вышла замуж|замужеств|брак|венчан|обвенчал|помолв|влюб|роман с|marri|wedding|engag|fell in love`)},
	{EventChild, regexp.MustCompile(`(?i)родил(ся|ась|ись)\s+(сын|доч|перв|втор)|рождени[ея] (сына|дочер|ребёнка)|(son|daughter) was born|birth of (his|her|their)`)},
	{EventRelocation, regexp.MustCompile(`(?i)переехал|переселил|перебрал(ся|ась)|эмигрир|вернул(ся|ась) в|поселил|обосновал|moved to|emigrat|settled in|returned to`)},
	{EventCreationMode, regexp.MustCompile(`(?i)начал (писать|работу над)|заверш(ил|ает) (работу|роман)|закончил (роман|повесть|работу)|написа(л|на) за|диктов|задумал|began (writing|work)|finished (writing|the novel)|completed the`)},
	{EventCareer, regexp.MustCompile(`(?i)опубликов|напечат|издан|вышел (роман|сборник)|вышла (книга|повесть)|премьер|успех|слав[аеу]|признани|редактор|редакци|основал|возглавил|publish|released|success|fame|founded|editor`)},
	{EventEducation, regexp.MustCompile(`(?i)поступил|окончил|университет|гимнази|лицей|училищ|учил(ся|ась)|graduat|enrolled|university|school|studied`)},
	{EventAward, regexp.MustCompile(`(?i)преми|наград|орден|лауреат|prize|award|medal|honou?r`)},
}

func classifyMilestone(s string) string {
	for _, c := range milestoneClasses {
		if c.re.MatchString(s) {
			return c.typ
		}
	}
	return EventOther
}

// extractWikiMilestones — plain-text статьи → вехи. Фильтры по порядку
// (замер: lifespan убивает ~30% шума ВКЛЮЧАЯ годы сюжета «Война и мир»→1812):
// биозона → предложения с годом → lifespan [рожд..смерть+2] → библиография →
// потомки/посмертное → длина 20–400 → классификация → дедуп (год, тип).
func extractWikiMilestones(lang, articleURL, text string, birth, death int) []AuthorEvent {
	zone := bioZone(text)
	seen := map[string]bool{}
	var out []AuthorEvent
	for _, sent := range splitSentences(zone) {
		runes := []rune(sent)
		if len(runes) < 20 || len(runes) > 400 {
			continue
		}
		year := milestoneYear(sent, birth, death)
		if year == 0 {
			continue
		}
		if biblioRe.MatchString(sent) || posthumousRe.MatchString(sent) {
			continue
		}
		typ := classifyMilestone(quotedRe.ReplaceAllString(sent, "«…»"))
		// Дубли якорей: «родился в 1828», «умер в 1910» — уже в Wikidata-скелете.
		if (year == birth && strings.Contains(strings.ToLower(sent), "родил")) ||
			(year == death && typ == EventLoss) {
			continue
		}
		// Один (год, тип) — одна веха: первое предложение выигрывает
		// (в биозоне оно обычно самое фактологичное).
		dupKey := fmt.Sprintf("%d:%s", year, typ)
		if seen[dupKey] {
			continue
		}
		seen[dupKey] = true

		sum := sha256.Sum256([]byte(strings.Join(strings.Fields(strings.ToLower(sent)), " ")))
		out = append(out, AuthorEvent{
			Source:        "wikipedia",
			ExtKey:        lang + ":" + hex.EncodeToString(sum[:8]),
			Type:          typ,
			YearFrom:      year,
			DatePrecision: "year",
			Title:         clipSentence(sent, 90),
			Quote:         sent,
			URL:           articleURL,
			Weight:        eventWeight(typ),
		})
	}
	return out
}

// bioZone — секции 2-го уровня с «биографическими» заголовками; статья без
// таковых (короткая, без структуры) → первые 60% текста (хвост статей —
// библиография/память/ссылки, там шум).
func bioZone(text string) string {
	lines := strings.Split(text, "\n")
	var bio []string
	inBio, sawBioHeading := false, false
	for _, line := range lines {
		if m := wikiHeadingRe.FindStringSubmatch(line); m != nil {
			if len(m[1]) == 2 { // уровень 2 («== X ==») переключает зону
				inBio = bioHeadingRe.MatchString(m[2])
				sawBioHeading = sawBioHeading || inBio
			}
			continue // сами заголовки в текст не идут
		}
		if inBio {
			bio = append(bio, line)
		}
	}
	if sawBioHeading {
		return strings.Join(bio, "\n")
	}
	runes := []rune(text)
	cut := len(runes) * 60 / 100
	// не рвать посреди абзаца
	if idx := strings.LastIndex(string(runes[:cut]), "\n"); idx > 0 {
		return string(runes[:cut])[:idx]
	}
	return string(runes[:cut])
}

// milestoneYear — первый год предложения в границах жизни [рожд..смерть+2]
// (+2 — посмертные публикации релевантны читателю; смерть неизвестна → без
// верхней границы). Годы вне границ (сюжет, «Война и мир»→1812, века) → 0.
// Перед поиском вырезаются скобки — даты жизни родни «(1794—1837)» не годы
// событий; декады «в конце 1840-х» — не год (живой прогон Толстого).
func milestoneYear(sent string, birth, death int) int {
	clean := parensRe.ReplaceAllString(sent, "")
	for _, loc := range yearRe.FindAllStringIndex(clean, 4) {
		if strings.HasPrefix(clean[loc[1]:], "-х") || strings.HasPrefix(clean[loc[1]:], "‑х") {
			continue // «1840-х» — декада
		}
		y, _ := strconv.Atoi(clean[loc[0]:loc[1]])
		if birth > 0 && y < birth {
			continue
		}
		if death > 0 && y > death+2 {
			continue
		}
		return y
	}
	return 0
}

var (
	parensRe = regexp.MustCompile(`\([^)]*\)`)
	// Названия произведений в кавычках сбивают классификатор («Война и мир» →
	// war w5 у половины творческих вех Толстого) — тип определяется по тексту
	// ВНЕ кавычек; цитата события остаётся полной.
	quotedRe = regexp.MustCompile(`«[^»]*»|"[^"]*"`)
)

// sentenceAbbrevRe — фрагмент НЕ кончается предложением, если перед точкой
// инициал («А.») или частое сокращение («т.», «г.», «ул.»).
var sentenceAbbrevRe = regexp.MustCompile(`(^|[\s(«"])([А-ЯЁA-Z]|г|гг|в|вв|т|тт|св|им|ул|пр|д|кв|с|стр|кн|акад|проф|н|э|см|др|тыс|млн|руб|коп|N|St|Mr|Mrs|Dr|Jr|Sr|vs)\.$`)

// splitSentences — предложения из plain-text: сплит по [.!?…]+пробел, склейка
// назад фрагментов, оборванных на инициале/сокращении. Абзацы независимы.
func splitSentences(text string) []string {
	var out []string
	for _, para := range strings.Split(text, "\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		parts := sentenceEndRe.Split(para, -1)
		ends := sentenceEndRe.FindAllString(para, -1)
		var cur strings.Builder
		for i, part := range parts {
			cur.WriteString(part)
			if i < len(ends) {
				cur.WriteString(strings.TrimRight(ends[i], " "))
			}
			s := cur.String()
			if sentenceAbbrevRe.MatchString(strings.TrimRight(s, ".!?… ")+".") && i < len(parts)-1 {
				cur.WriteString(" ")
				continue // инициал/сокращение — предложение не кончилось
			}
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
			cur.Reset()
		}
	}
	return out
}

var sentenceEndRe = regexp.MustCompile(`[.!?…]+\s+`)

// clipSentence — короткая формулировка из предложения: ≤ max рун по границе
// слова (LLM-полировка в PR-5 заменит на настоящий summary).
func clipSentence(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return strings.TrimRight(s, ".")
	}
	clipped := string(runes[:maxRunes])
	if idx := strings.LastIndex(clipped, " "); idx > maxRunes/2 {
		clipped = clipped[:idx]
	}
	return strings.TrimRight(clipped, ",;:— .") + "…"
}

// mergeAuthorEvents — Wikidata-скелет + Wikipedia-вехи: дедуп wd↔wiki по
// (год, тип) — типизированный источник приоритетен, цитата wiki-дубля
// дозаписывается в wd-событие (upsert COALESCE'ит quote); затем общий кап:
// сортировка вес↓ (стабильно по году), хвост весов 0–1 отсекается, якоря
// birth/death не отсеиваются никогда.
const maxTimelineEvents = 60

func mergeAuthorEvents(wd, wiki []AuthorEvent) []AuthorEvent {
	wdByYearType := map[string]int{}
	for i, ev := range wd {
		wdByYearType[fmt.Sprintf("%d:%s", ev.YearFrom, ev.Type)] = i
	}
	merged := append([]AuthorEvent{}, wd...)
	for _, ev := range wiki {
		if i, dup := wdByYearType[fmt.Sprintf("%d:%s", ev.YearFrom, ev.Type)]; dup {
			if merged[i].Quote == "" {
				merged[i].Quote = ev.Quote
			}
			continue
		}
		merged = append(merged, ev)
	}
	if len(merged) <= maxTimelineEvents {
		return merged
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Weight != merged[j].Weight {
			return merged[i].Weight > merged[j].Weight
		}
		return merged[i].YearFrom < merged[j].YearFrom
	})
	out := merged[:0:0]
	for _, ev := range merged {
		if ev.Type == EventBirth || ev.Type == EventDeath {
			out = append(out, ev) // якоря вне капа
			continue
		}
		if len(out) < maxTimelineEvents {
			out = append(out, ev)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].YearFrom < out[j].YearFrom })
	return out
}
