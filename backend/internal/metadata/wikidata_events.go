package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Событие биографии автора (до записи в author_events). Источник — Wikidata
// («структурный скелет», CC0; замер 2026-07: 13–19 датированных фактов у
// классиков) и, со следующего PR, Wikipedia-текст. См. план
// cryptic-roaming-turing (био-таймлайн v2).
type AuthorEvent struct {
	Source        string // "wikidata" | "wikipedia"
	ExtKey        string // идемпотентность: wd '{prop}:{QID|-}:{год}'
	Type          string // типология (см. eventWeight)
	YearFrom      int
	YearTo        *int       // период (брак, residence): год окончания
	DateFrom      *time.Time // при precision month/day
	DatePrecision string     // 'year' | 'month' | 'day'
	Title         string     // короткая русская формулировка
	Quote         string     // сырое предложение (wikipedia-источник, PR-2)
	Place         string
	URL           string // атрибуция
	Weight        int
}

// Типология событий и веса «интересности» (референс — летописи/«Полка»;
// сильнее всего работают war/persecution/loss и контрастные связи).
// Веса питают критерий показа таймлайна («нетривиальное» = weight ≥ 2)
// и сортировку при капе. birth/death — якоря оси (вес 0, показываются всегда).
const (
	EventBirth        = "birth"
	EventDeath        = "death"
	EventWar          = "war"
	EventPersecution  = "persecution" // арест/ссылка/заключение/травля
	EventLoss         = "loss"        // смерти близких
	EventIsolation    = "isolation"
	EventPoverty      = "poverty"
	EventSpiritual    = "spiritual"
	EventLove         = "love" // брак/венчание
	EventChild        = "child"
	EventIllness      = "illness"
	EventRelocation   = "relocation" // переезд/эмиграция (часто период)
	EventCareer       = "career"
	EventCreationMode = "creation_mode"
	EventEducation    = "education"
	EventResidence    = "residence"
	EventAward        = "award"
	EventOther        = "other"
)

func eventWeight(t string) int {
	switch t {
	case EventWar, EventPersecution, EventLoss:
		return 5
	case EventIsolation, EventPoverty, EventSpiritual:
		return 4
	case EventLove, EventChild, EventIllness, EventRelocation:
		return 3
	case EventCareer, EventCreationMode:
		return 2
	case EventEducation, EventResidence, EventAward, EventOther:
		return 1
	default: // birth/death — якоря
		return 0
	}
}

// maxAwardEvents — кап наград: у современников P166 заливает таймлайн
// (замер: Кинг 41, Пратчетт 18 — стена мелких премий вместо биографии).
const maxAwardEvents = 3

// WikidataEventsProvider — «структурный скелет» биографии из Wikidata одним
// UNION-SPARQL-запросом по QID автора: рождение/смерть (+места, точность),
// браки-периоды (P26+P580/P582), рождения детей (P40→P569), смерти
// родителей/сиблингов (P22/P25/P3373→P570, фильтр по годам жизни автора),
// residence/учёба-периоды, награды (P166+P585), significant events
// (P793/P1344/P2632 — бонус, у большинства пусты; драматургию середины жизни
// добирает Wikipedia-экстрактор в PR-2).
type WikidataEventsProvider struct {
	httpClient *http.Client
	sparqlURL  string
	apiURL     string // wbsearchentities (резолв QID по имени, фолбэк)
	gate       *rateGate
}

func NewWikidataEventsProvider(httpClient *http.Client) *WikidataEventsProvider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 90 * time.Second}
	}
	p := &WikidataEventsProvider{
		httpClient: httpClient,
		sparqlURL:  "https://query.wikidata.org/sparql",
		apiURL:     "https://www.wikidata.org/w/api.php",
		gate:       &rateGate{},
	}
	p.gate.setRPM(20) // вежливый потолок к WDQS (зеркало adaptations)
	return p
}

// WithEndpoints — для тестов.
func (p *WikidataEventsProvider) WithEndpoints(sparql, api string) *WikidataEventsProvider {
	if sparql != "" {
		p.sparqlURL = sparql
	}
	if api != "" {
		p.apiURL = api
	}
	return p
}

func (p *WikidataEventsProvider) Name() string { return "wikidata" }

// eventsSPARQL — один UNION-запрос на автора. ?prop BIND'ится строкой в
// каждой ветке — маппинг prop→тип события живёт в Go (rowToEvent).
const eventsSPARQL = `
SELECT ?prop ?date ?prec ?end ?who ?whoLabel ?placeLabel WHERE {
  {
    BIND("P569" AS ?prop)
    wd:%[1]s p:P569/psv:P569 ?dv . ?dv wikibase:timeValue ?date ; wikibase:timePrecision ?prec .
    OPTIONAL { wd:%[1]s wdt:P19 ?place . }
  } UNION {
    BIND("P570" AS ?prop)
    wd:%[1]s p:P570/psv:P570 ?dv . ?dv wikibase:timeValue ?date ; wikibase:timePrecision ?prec .
    OPTIONAL { wd:%[1]s wdt:P20 ?place . }
  } UNION {
    BIND("P26" AS ?prop)
    wd:%[1]s p:P26 ?st . ?st ps:P26 ?who .
    OPTIONAL { ?st pq:P580 ?date . }
    OPTIONAL { ?st pq:P582 ?end . }
  } UNION {
    BIND("P40" AS ?prop)
    wd:%[1]s wdt:P40 ?who . ?who p:P569/psv:P569 ?dv . ?dv wikibase:timeValue ?date ; wikibase:timePrecision ?prec .
  } UNION {
    BIND("P570rel" AS ?prop)
    { wd:%[1]s wdt:P22 ?who } UNION { wd:%[1]s wdt:P25 ?who } UNION { wd:%[1]s wdt:P3373 ?who }
    ?who p:P570/psv:P570 ?dv . ?dv wikibase:timeValue ?date ; wikibase:timePrecision ?prec .
  } UNION {
    BIND("P551" AS ?prop)
    wd:%[1]s p:P551 ?st . ?st ps:P551 ?place .
    OPTIONAL { ?st pq:P580 ?date . }
    OPTIONAL { ?st pq:P582 ?end . }
  } UNION {
    BIND("P69" AS ?prop)
    wd:%[1]s p:P69 ?st . ?st ps:P69 ?who .
    OPTIONAL { ?st pq:P580 ?date . }
    OPTIONAL { ?st pq:P582 ?end . }
  } UNION {
    BIND("P166" AS ?prop)
    wd:%[1]s p:P166 ?st . ?st ps:P166 ?who . ?st pq:P585 ?date .
  } UNION {
    BIND("P793" AS ?prop)
    wd:%[1]s p:P793 ?st . ?st ps:P793 ?who .
    OPTIONAL { ?st pq:P585 ?date . }
    OPTIONAL { ?st pq:P580 ?date . }
  } UNION {
    BIND("P1344" AS ?prop)
    wd:%[1]s p:P1344 ?st . ?st ps:P1344 ?who .
    OPTIONAL { ?st pq:P585 ?date . }
    OPTIONAL { ?st pq:P580 ?date . }
    OPTIONAL { ?who wdt:P580 ?date . }
  } UNION {
    BIND("P2632" AS ?prop)
    wd:%[1]s p:P2632 ?st . ?st ps:P2632 ?who .
    OPTIONAL { ?st pq:P580 ?date . }
    OPTIONAL { ?st pq:P582 ?end . }
  }
  SERVICE wikibase:label { bd:serviceParam wikibase:language "ru,en".
    ?who rdfs:label ?whoLabel .
    ?place rdfs:label ?placeLabel .
  }
}
LIMIT 400
`

// FetchAuthorEvents — события по QID. 404/пусто → ErrNotFound не бывает
// (SPARQL всегда 200 с пустыми bindings); не-200 → statusErr (грабля №20).
func (p *WikidataEventsProvider) FetchAuthorEvents(ctx context.Context, qid string) ([]AuthorEvent, error) {
	if err := p.gate.wait(ctx); err != nil {
		return nil, fmt.Errorf("%w: wdqs gate: %v", ErrUpstream, err)
	}
	form := url.Values{}
	form.Set("query", fmt.Sprintf(eventsSPARQL, qid))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.sparqlURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build sparql request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/sparql-results+json")
	req.Header.Set("User-Agent", wdUserAgent)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: sparql: %v", ErrUpstream, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, statusErr(resp.StatusCode)
	}

	var body struct {
		Results struct {
			Bindings []map[string]struct {
				Value string `json:"value"`
			} `json:"bindings"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode sparql: %w", err)
	}

	return assembleWikidataEvents(qid, body.Results.Bindings), nil
}

// assembleWikidataEvents — сырые SPARQL-строки → события: парсинг дат/точности,
// дедуп по ext_key, lifespan-фильтр смертей родных, русские формулировки,
// кап наград. Отделено от HTTP для юнит-тестов.
func assembleWikidataEvents(qid string, rows []map[string]struct {
	Value string `json:"value"`
}) []AuthorEvent {
	entityURL := "https://www.wikidata.org/wiki/" + qid

	type raw struct {
		prop, who, whoLabel, place string
		date, end                  string
		prec                       int
	}
	var raws []raw
	for _, b := range rows {
		r := raw{
			prop:     b["prop"].Value,
			who:      extractQID(b["who"].Value),
			whoLabel: b["whoLabel"].Value,
			place:    b["placeLabel"].Value,
			date:     b["date"].Value,
			end:      b["end"].Value,
		}
		if v := b["prec"].Value; v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				r.prec = n
			}
		}
		if r.prop == "" || r.date == "" {
			continue // события без даты для таймлайна бесполезны
		}
		raws = append(raws, r)
	}

	// Годы жизни автора — для фильтра «смерть родного — событие ЕГО жизни».
	birthYear, deathYear := 0, 0
	for _, r := range raws {
		if y := sparqlYear(r.date); y != 0 {
			switch r.prop {
			case "P569":
				birthYear = y
			case "P570":
				deathYear = y
			}
		}
	}

	seen := map[string]bool{}
	var out []AuthorEvent
	awards := 0
	for _, r := range raws {
		y := sparqlYear(r.date)
		if y == 0 {
			continue
		}
		// Смерть родителя/сиблинга — событие жизни автора, только пока автор жив.
		if r.prop == "P570rel" {
			if (birthYear != 0 && y < birthYear) || (deathYear != 0 && y > deathYear) {
				continue
			}
		}
		if r.prop == "P166" {
			if awards >= maxAwardEvents {
				continue
			}
		}

		ev := AuthorEvent{
			Source:        "wikidata",
			ExtKey:        fmt.Sprintf("%s:%s:%d", r.prop, orDash(r.who), y),
			YearFrom:      y,
			DatePrecision: sparqlPrecision(r.prec, r.date),
			Place:         r.place,
			URL:           entityURL,
		}
		if seen[ev.ExtKey] {
			continue
		}
		if t, err := time.Parse(time.RFC3339, r.date); err == nil && ev.DatePrecision != "year" {
			ev.DateFrom = &t
		}
		if ey := sparqlYear(r.end); ey != 0 && ey != y {
			ev.YearTo = &ey
		}

		who := cleanLabel(r.whoLabel, r.who)
		switch r.prop {
		case "P569":
			ev.Type, ev.Title = EventBirth, "Родился"
			if r.place != "" {
				ev.Title = "Родился — " + r.place
			}
		case "P570":
			ev.Type, ev.Title = EventDeath, "Умер"
			if r.place != "" {
				ev.Title = "Умер — " + r.place
			}
		case "P26":
			ev.Type, ev.Title = EventLove, "Брак"
			if who != "" {
				ev.Title = "Брак: " + who
			}
		case "P40":
			ev.Type, ev.Title = EventChild, "Родился ребёнок"
			if who != "" {
				ev.Title = "Родился ребёнок: " + who
			}
		case "P570rel":
			ev.Type, ev.Title = EventLoss, "Смерть близкого"
			if who != "" {
				ev.Title = "Смерть близкого: " + who
			}
		case "P551":
			ev.Type, ev.Title = EventResidence, "Место жизни"
			if r.place != "" {
				ev.Title = "Жил: " + r.place
				ev.Place = r.place
			}
		case "P69":
			ev.Type, ev.Title = EventEducation, "Учёба"
			if who != "" {
				ev.Title = "Учёба: " + who
			}
		case "P166":
			ev.Type, ev.Title = EventAward, "Награда"
			if who != "" {
				ev.Title = "Награда: " + who
			}
			awards++
		case "P2632":
			ev.Type, ev.Title = EventPersecution, "Заключение"
			if who != "" {
				ev.Title = "Заключение: " + who
			}
		case "P793", "P1344":
			ev.Type, ev.Title = classifySignificant(who)
		default:
			continue
		}
		ev.Weight = eventWeight(ev.Type)
		seen[ev.ExtKey] = true
		out = append(out, ev)
	}
	return out
}

// classifySignificant — P793 (significant event) / P1344 (participant in):
// тип по ключевым словам лейбла (война даёт war/5, арест — persecution/5,
// прочее — other/1: не всё «значимое» Wikidata значимо для читателя).
func classifySignificant(label string) (string, string) {
	l := strings.ToLower(label)
	switch {
	case strings.Contains(l, "войн") || strings.Contains(l, "war") ||
		strings.Contains(l, "битв") || strings.Contains(l, "battle") ||
		strings.Contains(l, "фронт") || strings.Contains(l, "оборон"):
		return EventWar, "Война: " + label
	case strings.Contains(l, "арест") || strings.Contains(l, "arrest") ||
		strings.Contains(l, "ссылк") || strings.Contains(l, "exile") ||
		strings.Contains(l, "каторг") || strings.Contains(l, "тюрьм") ||
		strings.Contains(l, "prison") || strings.Contains(l, "лагер"):
		return EventPersecution, label
	case label == "":
		return EventOther, "Событие"
	default:
		return EventOther, label
	}
}

// sparqlYear — год из xsd:dateTime Wikidata ("1867-02-15T00:00:00Z");
// отрицательные/пустые → 0.
func sparqlYear(s string) int {
	if len(s) < 4 || s[0] == '-' {
		return 0
	}
	y, err := strconv.Atoi(s[:4])
	if err != nil || y == 0 {
		return 0
	}
	return y
}

// sparqlPrecision — wikibase:timePrecision (11=день, 10=месяц, 9=год) →
// date_precision. Даты из квалификаторов приходят без precision (prec 0) —
// эвристика: полночь 1 января = «известен только год».
func sparqlPrecision(prec int, date string) string {
	switch prec {
	case 11:
		return "day"
	case 10:
		return "month"
	case 9:
		return "year"
	}
	if strings.Contains(date, "-01-01T00:00:00") {
		return "year"
	}
	if len(date) >= 10 {
		return "day"
	}
	return "year"
}

// cleanLabel — лейбл сущности; Wikidata без метки отдаёт сам QID («Q12345») —
// такой «лейбл» бесполезен читателю, прячем.
func cleanLabel(label, qid string) string {
	label = strings.TrimSpace(label)
	if label == "" || label == qid {
		return ""
	}
	if strings.HasPrefix(label, "Q") {
		if _, err := strconv.Atoi(label[1:]); err == nil {
			return ""
		}
	}
	return label
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// ResolveAuthorQID — фолбэк-резолв QID по имени (когда bio-путь QID ещё не
// персистил): wbsearchentities → первый кандидат, прошедший occupation-гейт
// (P106 «писатель»; защита от казуса Q46405≠Пратчетт). gate nil → первый хит.
func (p *WikidataEventsProvider) ResolveAuthorQID(ctx context.Context, fullName string, gate func(ctx context.Context, qid string) (OccupationVerdict, error)) (string, error) {
	if strings.TrimSpace(fullName) == "" {
		return "", ErrNotFound
	}
	if err := p.gate.wait(ctx); err != nil {
		return "", fmt.Errorf("%w: wdqs gate: %v", ErrUpstream, err)
	}
	q := url.Values{}
	q.Set("action", "wbsearchentities")
	q.Set("search", fullName)
	q.Set("language", "ru")
	q.Set("uselang", "ru")
	q.Set("type", "item")
	q.Set("limit", "5")
	q.Set("format", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiURL+"?"+q.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("build wbsearch request: %w", err)
	}
	req.Header.Set("User-Agent", wdUserAgent)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: wbsearch: %v", ErrUpstream, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", statusErr(resp.StatusCode)
	}
	var body struct {
		Search []struct {
			ID string `json:"id"`
		} `json:"search"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode wbsearch: %w", err)
	}
	for _, hit := range body.Search {
		if hit.ID == "" {
			continue
		}
		if gate == nil {
			return hit.ID, nil
		}
		v, gerr := gate(ctx, hit.ID)
		if gerr != nil {
			continue // транзиент гейта — пробуем следующего кандидата
		}
		if v == OccupationWriter {
			return hit.ID, nil
		}
		// NonWriter/Unknown: для СОБЫТИЙ (не bio) осторожнее — берём только
		// подтверждённого писателя, иначе рискуем чужой биографией.
	}
	return "", ErrNotFound
}
