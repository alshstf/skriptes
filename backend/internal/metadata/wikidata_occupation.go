package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// OccupationVerdict — вердикт проверки профессии (P106) кандидата-автора,
// найденного по имени. Слой 2 точности обогащения авторов поверх
// authorNameMatches: имя-гейт пропускает однофамильцев ("Гарднер" —
// писательница vs. богослов), P106 добивает — писатель ли это ВООБЩЕ.
//
// Философия precision > recall, но осторожная: отвергаем ТОЛЬКО при явном
// не-писателе (есть профессии, среди них нет писательской). Нет P106 /
// сущность без Wikidata-связи / ошибка запроса → Unknown (не отвергаем), иначе
// потеряли бы валидных авторов без размеченной профессии.
type OccupationVerdict int

const (
	// OccupationUnknown — не смогли определить (нет P106, нет QID, ошибка сети).
	// НЕ основание отвергнуть кандидата — падаем обратно на имя-гейт.
	OccupationUnknown OccupationVerdict = iota
	// OccupationWriter — среди P106 есть писательская профессия (writer/author
	// или их подкласс: novelist/poet/playwright/… через P279*). Принимаем.
	OccupationWriter
	// OccupationNonWriter — P106 есть, но НИ ОДНА не писательская. Явный
	// однофамилец-не-писатель — отвергаем.
	OccupationNonWriter
)

func (v OccupationVerdict) String() string {
	switch v {
	case OccupationWriter:
		return "writer"
	case OccupationNonWriter:
		return "non-writer"
	default:
		return "unknown"
	}
}

// writerBaseClasses — корневые классы «пишущих» профессий для P279*-обхода:
// writer (Q36180) и author (Q482980) — их подклассы (novelist, poet,
// playwright, screenwriter, essayist…) обход ловит сам. Плюс те, кто пишет
// нехудожественные книги каталога: scientist Q901, researcher Q1650915,
// scholar Q2248623, academic Q3400985, university teacher Q1622272, historian
// Q201788, philosopher Q4964182, journalist Q1930187, translator Q333634,
// critic Q6430706, editor Q1607826, publicist Q1086863, jurist Q185351. Без них гейт считал
// «не писателями» математика Гутера, историка Каткова, искусствоведа Бессонову
// (выборка с прода 2026-09, #280) и отвергал их верные биографии. Отвергаем
// по-прежнему, когда ни одной такой профессии нет: спортсмены, актёры, певцы.
const writerBaseClasses = "wd:Q36180 wd:Q482980 wd:Q901 wd:Q1650915 wd:Q2248623 wd:Q3400985 wd:Q1622272 " +
	"wd:Q201788 wd:Q4964182 wd:Q1930187 wd:Q333634 wd:Q6430706 wd:Q1607826 wd:Q1086863 wd:Q185351"

// OccupationVerdict — по QID сущности определяет, писатель ли это. Один SPARQL:
// считаем ВСЕ занятости (P106) и писательские (P106, чей класс через P279*
// доходит до writer/author). Реализует сигнатуру гейта, инъектируемого в
// WikipediaProvider.WithOccupationGate.
func (p *WikidataAdaptationsProvider) OccupationVerdict(ctx context.Context, qid string) (OccupationVerdict, error) {
	if qid == "" {
		return OccupationUnknown, nil
	}
	query := fmt.Sprintf(`SELECT (COUNT(DISTINCT ?occ) AS ?total) (COUNT(DISTINCT ?w) AS ?writer) WHERE {
  OPTIONAL { wd:%[1]s wdt:P106 ?occ . }
  OPTIONAL { wd:%[1]s wdt:P106 ?w . ?w wdt:P279* ?base . VALUES ?base { %[2]s } }
}`, qid, writerBaseClasses)

	total, writer, err := p.runSPARQLOccupationCounts(ctx, query)
	if err != nil {
		return OccupationUnknown, err
	}
	if traceOn(ctx) && total > 0 {
		p.traceOccupationLabels(ctx, qid)
	}
	switch {
	case writer > 0:
		return OccupationWriter, nil
	case total > 0:
		return OccupationNonWriter, nil
	default:
		return OccupationUnknown, nil
	}
}

// runSPARQLOccupationCounts — исполняет агрегатный SPARQL и достаёт два
// счётчика (?total, ?writer) из единственной строки результата. COUNT в SPARQL
// приходит строкой ("3", datatype xsd:integer) — парсим strconv.Atoi.
func (p *WikidataAdaptationsProvider) runSPARQLOccupationCounts(ctx context.Context, query string) (total, writer int, err error) {
	body, err := p.doSPARQL(ctx, query)
	if err != nil {
		return 0, 0, err
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
		return 0, 0, fmt.Errorf("decode occupation counts: %w", err)
	}
	if len(resp.Results.Bindings) == 0 {
		return 0, 0, nil // сущности нет / нет строк — трактуем как Unknown
	}
	b := resp.Results.Bindings[0]
	if v, ok := b["total"]; ok {
		total, _ = strconv.Atoi(v.Value)
	}
	if v, ok := b["writer"]; ok {
		writer, _ = strconv.Atoi(v.Value)
	}
	return total, writer, nil
}

// traceOccupationLabels — для разбора ошибок (#280): какие именно профессии у
// кандидата и какие из них засчитаны пишущими («*»). Только при включённой
// трассе — обычный путь этих запросов не делает. Два лёгких запроса: подписи
// профессий и «пишущие» (тем же обходом P279*, что у вердикта); совмещённый в
// один запрос с сервисом подписей WDQS не успевал ответить. Сбой не мешает
// решению: в трассу уходит текст ошибки.
func (p *WikidataAdaptationsProvider) traceOccupationLabels(ctx context.Context, qid string) {
	step := TraceStep{Source: "wikidata", Stage: "occupations", Outcome: TraceInfo, Input: qid}
	labels, err := p.sparqlBindings(ctx, fmt.Sprintf(`SELECT ?occ ?occLabel WHERE {
  wd:%s wdt:P106 ?occ .
  SERVICE wikibase:label { bd:serviceParam wikibase:language "ru,en". }
}`, qid))
	if err != nil {
		step.Value = "error: " + err.Error()
		traceStep(ctx, step)
		return
	}
	writers, err := p.sparqlBindings(ctx, fmt.Sprintf(`SELECT DISTINCT ?occ WHERE {
  wd:%s wdt:P106 ?occ . ?occ wdt:P279* ?base . VALUES ?base { %s }
}`, qid, writerBaseClasses))
	if err != nil {
		step.Value = "error: " + err.Error()
		traceStep(ctx, step)
		return
	}
	isWriter := map[string]bool{}
	for _, b := range writers {
		isWriter[b["occ"]] = true
	}
	out := make([]string, 0, len(labels))
	for _, b := range labels {
		l := b["occLabel"]
		if isWriter[b["occ"]] {
			l += "*"
		}
		out = append(out, l)
	}
	sort.Strings(out)
	step.Value = strings.Join(out, ", ")
	traceStep(ctx, step)
}

// sparqlBindings — строки результата SPARQL как «переменная → значение».
func (p *WikidataAdaptationsProvider) sparqlBindings(ctx context.Context, query string) ([]map[string]string, error) {
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
		return nil, fmt.Errorf("decode sparql: %w", err)
	}
	out := make([]map[string]string, 0, len(resp.Results.Bindings))
	for _, b := range resp.Results.Bindings {
		row := make(map[string]string, len(b))
		for k, v := range b {
			row[k] = v.Value
		}
		out = append(out, row)
	}
	return out, nil
}
