package metadata

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Политика приёма кандидата — статьи (или автора OpenLibrary), найденной по имени
// и прошедшей гейт имени (case study #280, отчёт
// ~/projects/plans/skriptes/author-bio-case-study-report.md, планка владельца
// 2026-10-02: ≈5 % чужих среди принятых, верных не меньше прежнего).
//
// Прежний слой 2 смотрел только на профессию: «не писатель» — отказ, остальное —
// приём. На размеченной выборке это давало 12,5 % чужих: смежные профессии
// (учёный, журналист, юрист) и кандидаты без профессии в Wikidata принимались без
// вопросов, а мемуаристы-генералы и политики отвергались. Порядок решений:
//
//  1. подтверждение книгой — принять: в Wikidata есть работа кандидата (P50) с
//     названием книги автора, или кандидата нашёл строгий путь (по книге или
//     уточнению);
//  2. противоречие — отвергнуть: кандидат родился позже книг автора (−12 лет);
//     умер до 2000 года, а автор пишет сетевую литературу; у автора больше
//     половины книг — сетевая литература (статей о таких авторах почти не бывает);
//     в названии статьи другое отчество;
//  3. профессия: писатель — принять; смежная или нет в Wikidata — только с
//     подтверждением (совпало отчество или тема книг соответствует профессии);
//     не писатель — если тема книг соответствует профессии, эпоха сходится, а
//     отчество совпало или его у автора нет (мемуары военных, политиков…).

// CandidateFacts — что Wikidata знает о кандидате.
type CandidateFacts struct {
	QID         string
	Occupations []string // метки P106 (ru, иначе en)
	Writer      bool     // есть профессия класса writer/author (P279*)
	Adjacent    bool     // есть смежная пишущая: учёный, журналист, юрист… (writerBaseClasses без writer/author)
	Born, Died  int      // годы (0 — неизвестно)
	Works       []string // названия работ с автором P50 (ru, иначе en)
}

// occupationClass — writer / adjacent / non-writer / unknown.
func (f CandidateFacts) occupationClass() string {
	switch {
	case len(f.Occupations) == 0 && !f.Writer && !f.Adjacent:
		return "unknown"
	case f.Writer:
		return "writer"
	case f.Adjacent:
		return "adjacent"
	default:
		return "non-writer"
	}
}

// CandidateFactsFunc — источник фактов о кандидате по QID ("" — нет QID: пустые факты).
type CandidateFactsFunc func(ctx context.Context, qid string) (CandidateFacts, error)

// CandidateCheck — решение о кандидате; confirmed — кандидата нашёл строгий путь
// (по книге или уточнению). Ошибка — сбой источника (временный).
type CandidateCheck func(ctx context.Context, q AuthorQuery, source, lang, title, qid string, confirmed bool) (bool, error)

// NewCandidateCheck — политика приёма поверх источника фактов. Факты кэшируются
// по QID на candidateFactsTTL: цепочки био и фото ищут статью независимо, и без
// кэша каждый автор спрашивал бы Wikidata дважды.
func NewCandidateCheck(facts CandidateFactsFunc) CandidateCheck {
	cache := newFactsCache(candidateFactsTTL, candidateFactsCacheSize)
	return func(ctx context.Context, q AuthorQuery, source, lang, title, qid string, confirmed bool) (bool, error) {
		f := CandidateFacts{QID: qid}
		if qid != "" {
			var ok bool
			if f, ok = cache.get(qid); !ok {
				var err error
				if f, err = facts(ctx, qid); err != nil {
					return false, err
				}
				cache.put(qid, f)
			}
		}
		traceOccupationFacts(ctx, source, lang, f)
		ok, why := decideCandidate(q, title, f, confirmed)
		outcome := TraceReject
		if ok {
			outcome = TracePass
		}
		traceStep(ctx, TraceStep{Source: source, Lang: lang, Stage: "policy", Outcome: outcome, Input: qid, Value: why})
		return ok, nil
	}
}

// traceOccupationFacts — профессии кандидата в трассу (как прежде: «*» — пишущая) и
// класс профессии.
func traceOccupationFacts(ctx context.Context, source, lang string, f CandidateFacts) {
	if !traceOn(ctx) || f.QID == "" {
		return
	}
	cls := f.occupationClass()
	outcome := TraceInfo
	switch cls {
	case "writer":
		outcome = TracePass
	case "non-writer":
		outcome = TraceReject
	}
	traceStep(ctx, TraceStep{Source: source, Lang: lang, Stage: "occupation", Outcome: outcome, Input: f.QID,
		Value: cls + ": " + strings.Join(f.Occupations, ", ")})
}

// decideCandidate — решение политики (см. doc файла) и его причина для трассы.
func decideCandidate(q AuthorQuery, title string, f CandidateFacts, confirmed bool) (bool, string) {
	if confirmed {
		return true, "confirmed by strict path"
	}
	if worksAnchor(f.Works, q.BookTitles) {
		return true, "book in wikidata (P50)"
	}
	switch {
	case f.Born > 0 && q.MinBookYear > 0 && f.Born > q.MinBookYear-12:
		return false, fmt.Sprintf("born %d, books from %d", f.Born, q.MinBookYear)
	case f.Died > 0 && f.Died < 2000 && q.NetShare >= 0.3:
		return false, fmt.Sprintf("died %d, network literature", f.Died)
	case q.NetShare >= 0.5:
		return false, "network literature without book confirmation"
	case patronymicConflict(title, q.MiddleName):
		return false, "other patronymic"
	}
	patOK := patronymicMatches(title, q.MiddleName)
	topic := topicMatches(f.Occupations, q.Genres)
	confirm := patOK || topic
	switch f.occupationClass() {
	case "writer":
		return true, "writer"
	case "adjacent", "unknown":
		if confirm {
			return true, f.occupationClass() + " confirmed by " + confirmWhy(patOK, topic)
		}
		return false, f.occupationClass() + " without confirmation"
	default: // non-writer
		eraOK := f.Died == 0 || q.MinBookYear == 0 || f.Died >= q.MinBookYear-3
		if topic && eraOK && (patOK || strings.TrimSpace(q.MiddleName) == "") {
			return true, "non-writer, topic matches books"
		}
		return false, "non-writer"
	}
}

func confirmWhy(patOK, topic bool) string {
	switch {
	case patOK && topic:
		return "patronymic, topic"
	case patOK:
		return "patronymic"
	default:
		return "topic"
	}
}

// worksAnchor — среди работ кандидата в Wikidata есть книга автора: то же название
// (без различия регистра, ё/е, кавычек) или одно содержит другое (≥ 8 букв).
func worksAnchor(works, titles []string) bool {
	for _, w := range works {
		nw := normTitleLoose(w)
		if len([]rune(nw)) < 4 {
			continue
		}
		for _, t := range titles {
			nt := normTitleLoose(t)
			n := len([]rune(nt))
			if n < 4 {
				continue
			}
			if nw == nt || (n >= 8 && (strings.Contains(nw, nt) || strings.Contains(nt, nw))) {
				return true
			}
		}
	}
	return false
}

func normTitleLoose(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ё", "е")
	return strings.Trim(s, " .«»\"'„“”")
}

var patronymicRe = regexp.MustCompile(`(вич|вна|ична|инична|ич|оглы|кызы)$`)

// titleNameTokens — слова названия статьи без уточнения в скобках, ё=е.
func titleNameTokens(title string) []string {
	base, _ := splitQualifier(title)
	f := strings.FieldsFunc(strings.ReplaceAll(strings.ToLower(base), "ё", "е"), func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == '-' || r == ' '
	})
	return f
}

// patronymicConflict — у автора есть отчество, в названии статьи есть отчество, и
// оно не наше («Кузнецов Виктор Иванович» → «Кузнецов, Виктор Васильевич»).
func patronymicConflict(title, middle string) bool {
	mid := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(middle)), "ё", "е")
	if mid == "" || !patronymicRe.MatchString(mid) {
		return false
	}
	toks := titleNameTokens(title)
	var pats []string
	for i, t := range toks {
		if i > 0 && len([]rune(t)) >= 6 && patronymicRe.MatchString(t) {
			pats = append(pats, t)
		}
	}
	if len(pats) == 0 {
		return false
	}
	for _, p := range pats {
		if levenshtein(p, mid) <= 1 {
			return false
		}
	}
	return true
}

// patronymicMatches — отчество автора есть в названии статьи (с точностью до буквы).
func patronymicMatches(title, middle string) bool {
	mid := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(middle)), "ё", "е")
	if len([]rune(mid)) < 4 {
		return false
	}
	for _, t := range strings.FieldsFunc(strings.ReplaceAll(strings.ToLower(title), "ё", "е"), func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == '(' || r == ')' || r == ' '
	}) {
		if levenshtein(t, mid) <= 1 {
			return true
		}
	}
	return false
}

// topicRule — тема книг (префиксы кодов жанров fb2) ↔ профессии (метки ru/en):
// человек пишет о своём деле — генерал о войне, психолог о психологии.
type topicRule struct {
	genres []string
	occ    *regexp.Regexp
}

var topicRules = []topicRule{
	{[]string{"military", "nonf_military", "prose_military", "sci_history", "nonf_biography"},
		regexp.MustCompile(`офицер|военн|генерал|военачальник|адмирал|лётчик|летчик|разведчи|маршал|командир|моряк|партизан|полковник|officer|military|general|admiral|pilot|aviator|intelligence|soldier|sailor`)},
	{[]string{"sci_politics", "nonf_publicism", "sci_history", "nonf_biography", "sci_social_studies", "sci_state", "sci_juris"},
		regexp.MustCompile(`политик|государственн|дипломат|депутат|министр|чиновник|общественн|юрист|адвокат|правовед|революционер|советник|politician|statesman|diplomat|lawyer|civil servant|activist|revolutionary`)},
	{[]string{"religion", "sci_religion"},
		regexp.MustCompile(`священ|монах|богослов|епископ|митрополит|архиеп|игумен|протоиерей|раввин|проповедник|святой|priest|monk|bishop|rabbi|preacher|cleric|saint`)},
	{[]string{"sci_business", "popular_business", "marketing", "sci_economy", "org_behavior", "banking", "economics"},
		regexp.MustCompile(`предпринимател|бизнесмен|экономист|менеджер|финансист|банкир|инвестор|маркетолог|businessperson|entrepreneur|economist|manager|banker|investor|executive`)},
	{[]string{"home_sport", "sport", "nonf_biography"},
		regexp.MustCompile(`спортсмен|тренер|футбол|хоккеи|боксёр|шахматист|биатлон|гонщик|теннис|альпинист|баскетбол|athlete|coach|player|boxer|chess|racing driver|mountaineer`)},
	{[]string{"cine", "theatre", "music", "nonf_biography", "art_criticism"},
		regexp.MustCompile(`актёр|актер|актрис|режиссёр|режиссер|музыкант|певец|певица|композитор|дирижёр|продюсер|actor|actress|director|musician|singer|composer|conductor|producer`)},
	{[]string{"sci_medicine", "home_health", "sci_medicine_alternative", "sci_psychology"},
		regexp.MustCompile(`врач|медик|хирург|терапевт|педиатр|физиолог|психиатр|психотерапевт|невролог|фармаколог|physician|surgeon|pediatrician|psychiatrist|psychotherapist|neurologist`)},
	{[]string{"sci_psychology", "psy_", "sci_pedagogy", "home_"},
		regexp.MustCompile(`психолог|психотерапевт|педагог|коуч|консультант|учитель|psychologist|educator|coach|consultant|teacher`)},
	{[]string{"sci_tech", "sci_build", "sci_transport", "sci_radio", "comp_", "military_weapon", "sci_phys", "sci_chem", "sci_math", "sci_biology", "sci_geo", "sci_cosmos"},
		regexp.MustCompile(`инженер|конструктор|изобретател|программист|физик|химик|математик|биолог|геолог|астроном|космонавт|engineer|inventor|programmer|physicist|chemist|mathematician|biologist|geologist|astronomer|astronaut|cosmonaut`)},
	{[]string{"home_cooking"}, regexp.MustCompile(`повар|кулинар|шеф|chef|cook`)},
	{[]string{"adv_geo", "travel"}, regexp.MustCompile(`путешественник|исследователь|альпинист|мореплаватель|explorer|traveler|traveller`)},
	{[]string{"art", "visual_arts", "sci_culture", "design"}, regexp.MustCompile(`художник|архитектор|фотограф|дизайнер|скульптор|искусствовед|painter|architect|photographer|designer|sculptor|art historian`)},
}

// topicMatches — профессия кандидата соответствует теме книг автора.
func topicMatches(occupations, genres []string) bool {
	if len(occupations) == 0 || len(genres) == 0 {
		return false
	}
	occ := strings.ToLower(strings.Join(occupations, " | "))
	for _, r := range topicRules {
		if !r.occ.MatchString(occ) {
			continue
		}
		for _, g := range genres {
			for _, prefix := range r.genres {
				if strings.HasPrefix(g, prefix) {
					return true
				}
			}
		}
	}
	return false
}

const (
	candidateFactsTTL       = 10 * time.Minute
	candidateFactsCacheSize = 2000
)

// factsCache — факты о кандидатах по QID с истечением; при переполнении
// выбрасываются истёкшие, а если их нет — всё (кэш короткоживущий).
type factsCache struct {
	mu    sync.Mutex
	ttl   time.Duration
	size  int
	items map[string]cachedFacts
}

type cachedFacts struct {
	f  CandidateFacts
	at time.Time
}

func newFactsCache(ttl time.Duration, size int) *factsCache {
	return &factsCache{ttl: ttl, size: size, items: map[string]cachedFacts{}}
}

func (c *factsCache) get(qid string) (CandidateFacts, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.items[qid]
	if !ok || time.Since(it.at) > c.ttl {
		return CandidateFacts{}, false
	}
	return it.f, true
}

func (c *factsCache) put(qid string, f CandidateFacts) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= c.size {
		for k, it := range c.items {
			if time.Since(it.at) > c.ttl {
				delete(c.items, k)
			}
		}
		if len(c.items) >= c.size {
			c.items = map[string]cachedFacts{}
		}
	}
	c.items[qid] = cachedFacts{f: f, at: time.Now()}
}
