package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Факты Wikidata о кандидате-авторе, найденном по имени (политика приёма —
// candidate_policy.go). Прежде отсюда шёл только вердикт «писатель / не писатель /
// неизвестно» по P106; разбор ошибок (#280) показал, что этого мало: нужны и класс
// профессии (настоящий писатель или смежная — учёный, журналист), и метки
// профессий (соответствие теме книг), и годы жизни, и работы кандидата (P50).

// writerBaseClasses — корневые классы «пишущих» профессий для P279*-обхода:
// writer (Q36180) и author (Q482980) — их подклассы (novelist, poet,
// playwright, screenwriter, essayist…) обход ловит сам. Плюс те, кто пишет
// нехудожественные книги каталога: scientist Q901, researcher Q1650915,
// scholar Q2248623, academic Q3400985, university teacher Q1622272, historian
// Q201788, philosopher Q4964182, journalist Q1930187, translator Q333634,
// critic Q6430706, editor Q1607826, publicist Q1086863, jurist Q185351 — это
// «смежные» профессии: их тёзки (учёные, юристы) давали больше всего чужих
// биографий, поэтому политика принимает их только с подтверждением.
const writerBaseClasses = "wd:Q36180 wd:Q482980 wd:Q901 wd:Q1650915 wd:Q2248623 wd:Q3400985 wd:Q1622272 " +
	"wd:Q201788 wd:Q4964182 wd:Q1930187 wd:Q333634 wd:Q6430706 wd:Q1607826 wd:Q1086863 wd:Q185351"

// trueWriterClasses — настоящие писатели: writer и author.
var trueWriterClasses = map[string]bool{"Q36180": true, "Q482980": true}

// candidateWorksLimit — сколько работ кандидата (P50) сверять с книгами автора.
const candidateWorksLimit = 300

// CandidateFacts — факты о кандидате по QID: три лёгких запроса (метки профессий
// и годы; классы профессий обходом P279*; работы с автором P50). Совмещённый с
// сервисом подписей запрос с обходом WDQS не успевал выполнить. Пустой QID —
// пустые факты. Ошибка — сбой источника.
func (p *WikidataAdaptationsProvider) CandidateFacts(ctx context.Context, qid string) (CandidateFacts, error) {
	f := CandidateFacts{QID: qid}
	if qid == "" {
		return f, nil
	}
	rows, err := p.sparqlBindings(ctx, fmt.Sprintf(`SELECT ?occ ?occLabel (YEAR(?b) AS ?born) (YEAR(?d) AS ?died) ?human WHERE {
  OPTIONAL { wd:%[1]s wdt:P106 ?occ . }
  OPTIONAL { wd:%[1]s wdt:P569 ?b . }
  OPTIONAL { wd:%[1]s wdt:P570 ?d . }
  OPTIONAL { wd:%[1]s wdt:P31 wd:Q5 . BIND(true AS ?human) }
  SERVICE wikibase:label { bd:serviceParam wikibase:language "ru,en". }
}`, qid))
	if err != nil {
		return f, fmt.Errorf("occupations: %w", err)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if occ := r["occ"]; occ != "" && !seen[occ] {
			seen[occ] = true
			f.Occupations = append(f.Occupations, r["occLabel"])
		}
		if r["human"] == "true" {
			f.Human = true
		}
		f.Born = minYear(f.Born, r["born"])
		f.Died = minYear(f.Died, r["died"])
	}
	sort.Strings(f.Occupations)
	if len(f.Occupations) > 0 {
		classes, err := p.sparqlBindings(ctx, fmt.Sprintf(`SELECT DISTINCT ?base WHERE {
  wd:%s wdt:P106 ?occ . ?occ wdt:P279* ?base . VALUES ?base { %s }
}`, qid, writerBaseClasses))
		if err != nil {
			return f, fmt.Errorf("occupation classes: %w", err)
		}
		for _, r := range classes {
			if trueWriterClasses[entityID(r["base"])] {
				f.Writer = true
			} else {
				f.Adjacent = true
			}
		}
	}
	works, err := p.sparqlBindings(ctx, fmt.Sprintf(`SELECT DISTINCT ?wLabel WHERE {
  ?w wdt:P50 wd:%s .
  SERVICE wikibase:label { bd:serviceParam wikibase:language "ru,en". }
} LIMIT %d`, qid, candidateWorksLimit))
	if err != nil {
		return f, fmt.Errorf("works: %w", err)
	}
	for _, r := range works {
		if l := r["wLabel"]; l != "" {
			f.Works = append(f.Works, l)
		}
	}
	return f, nil
}

// minYear — меньший из годов (0 — неизвестен); s — год строкой из SPARQL.
func minYear(cur int, s string) int {
	y, err := strconv.Atoi(s)
	if err != nil || y == 0 {
		return cur
	}
	if cur == 0 || y < cur {
		return y
	}
	return cur
}

// entityID — QID из адреса сущности Wikidata.
func entityID(uri string) string {
	if i := strings.LastIndex(uri, "/"); i >= 0 {
		return uri[i+1:]
	}
	return uri
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
