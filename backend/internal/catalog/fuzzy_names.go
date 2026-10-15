package catalog

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/skriptes/skriptes/backend/internal/textnorm"
)

// Похожие имена (#516). Строгий поиск авторов и серий — вхождение каждого
// слова запроса в свёрнутое имя (textnorm.FoldName); одна опечатка — и автора
// нет, хотя его книги Meili находит («рэй бредбери»). Когда строгих совпадений
// не хватает, добираем похожие в два шага:
//
//  1. Кандидаты — trigram-индексом по свёрнутому имени (миграция 0059):
//     word_similarity(самое длинное слово, имя) ≥ fuzzyTrgmThreshold. Порог
//     низкий: сходство у pg_trgm — Жаккар по триграммам, и одна замена в слове
//     из 5–6 букв роняет его до 0,3–0,4, перестановка соседних букв — ниже 0,5.
//  2. Точное правило — в Go (nameDistance): каждое слово запроса — вхождение
//     в имя или слово имени (его начало — набор не дописан) не дальше бюджета
//     правок (fuzzyWordBudget); перестановка соседних букв — одна правка.
//
// Порядок похожих — меньше правок, потом известнее. Приёмка на проде
// (2026-10-16, 823 опечатки — замена, пропуск, перестановка, удвоение буквы —
// в фамилиях 400 самых известных авторов): автор в пятёрке подсказки — 90%,
// первым — 79% (одни триграммы с порогом 0,5 — около половины, до #516 — 2%).
// Промахи — в основном 4-буквенные фамилии и вхождения, занявшие все 5 мест.

const (
	// fuzzyMinWord — слово короче терпит только точное вхождение: у 3 букв
	// любая правка — уже другое слово.
	fuzzyMinWord = 4
	// fuzzyLongWord — с этой длины допускаются две правки.
	fuzzyLongWord = 7
	// fuzzyTrgmThreshold — pg_trgm.word_similarity_threshold на кандидатов.
	fuzzyTrgmThreshold = "0.3"
	// fuzzyTrgmCandidates — сколько кандидатов (по убыванию сходства) проверять
	// точным правилом.
	fuzzyTrgmCandidates = 300
)

// fuzzyWordBudget — сколько правок допускает слово запроса.
func fuzzyWordBudget(w string) int {
	switch n := utf8.RuneCountInString(w); {
	case n >= fuzzyLongWord:
		return 2
	case n >= fuzzyMinWord:
		return 1
	}
	return 0
}

// fuzzyWords — слова запроса для похожих имён: нижний регистр, свёртка имени.
// Не больше 6 (как wordConditions).
func fuzzyWords(query string) []string {
	words := strings.Fields(strings.ToLower(textnorm.FoldName(query)))
	if len(words) > 6 {
		words = words[:6]
	}
	return words
}

// fuzzyProbe — самое длинное слово, если хоть одно слово запроса терпит
// опечатку; "" — похожие не ищем.
func fuzzyProbe(words []string) string {
	probe := ""
	for _, w := range words {
		if utf8.RuneCountInString(w) > utf8.RuneCountInString(probe) {
			probe = w
		}
	}
	if utf8.RuneCountInString(probe) < fuzzyMinWord {
		return ""
	}
	return probe
}

// nameDistance — сколько правок отделяют запрос от имени (свёрнутого, в нижнем
// регистре); -1 — не похоже. Каждое слово запроса должно найтись: вхождением
// (0 правок) или словом имени в пределах бюджета.
func nameDistance(words []string, name string) int {
	if len(words) == 0 || name == "" {
		return -1
	}
	nameWords := strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	total := 0
	for _, w := range words {
		if strings.Contains(name, w) {
			continue
		}
		budget := fuzzyWordBudget(w)
		if budget == 0 {
			return -1
		}
		best := budget + 1
		wr := []rune(w)
		for _, nw := range nameWords {
			if d := prefixDistance(wr, []rune(nw), budget); d < best {
				best = d
			}
		}
		if best > budget {
			return -1
		}
		total += best
	}
	return total
}

// prefixDistance — расстояние от слова запроса до слова имени или его начала
// (подсказка срабатывает на недописанном слове: «брудбе» ~ «бредбери»). Длина
// начала — длина слова запроса ±1; дальше — сразу «больше бюджета».
func prefixDistance(w, nw []rune, budget int) int {
	best := budget + 1
	for _, l := range []int{len(w) - 1, len(w), len(w) + 1, len(nw)} {
		if l <= 0 || l > len(nw) || abs(l-len(w)) > budget {
			continue
		}
		if d := osaDistance(w, nw[:l]); d < best {
			best = d
		}
	}
	return best
}

// osaDistance — расстояние Дамерау–Левенштейна в варианте optimal string
// alignment: вставка, удаление, замена и перестановка соседних — по одной правке.
func osaDistance(a, b []rune) int {
	prev2 := make([]int, len(b)+1)
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(b)]
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// fuzzyHit — похожий кандидат: id, число правок и вес для порядка внутри
// одинакового числа правок (известность автора).
type fuzzyHit struct {
	id     int64
	dist   int
	weight int64
}

// fuzzyRows — кандидаты похожих из trigram-индекса: запрос получает
// probe ($1) и exclude ($2), отдаёт (id, свёрнутое имя, латиница, вес).
// Порог word_similarity — только на транзакцию.
func (s *Service) fuzzyRows(ctx context.Context, sql string, probe string, exclude []int64, words []string, limit int) ([]fuzzyHit, error) {
	if exclude == nil {
		exclude = []int64{}
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("fuzzy names: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('pg_trgm.word_similarity_threshold', $1, true)`, fuzzyTrgmThreshold); err != nil {
		return nil, fmt.Errorf("fuzzy names: threshold: %w", err)
	}
	rows, err := tx.Query(ctx, sql, probe, exclude)
	if err != nil {
		return nil, fmt.Errorf("fuzzy names: %w", err)
	}
	defer rows.Close()
	var hits []fuzzyHit
	for rows.Next() {
		var (
			h           fuzzyHit
			name, latin string
		)
		if err := rows.Scan(&h.id, &name, &latin, &h.weight); err != nil {
			return nil, err
		}
		h.dist = nameDistance(words, name)
		if d := nameDistance(words, latin); d >= 0 && (h.dist < 0 || d < h.dist) {
			h.dist = d
		}
		if h.dist >= 0 {
			hits = append(hits, h)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].dist != hits[j].dist {
			return hits[i].dist < hits[j].dist
		}
		if hits[i].weight != hits[j].weight {
			return hits[i].weight > hits[j].weight
		}
		return hits[i].id < hits[j].id
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

var (
	authorNameFold = textnorm.NameFoldSQL("a.normalized_name")
	seriesNameFold = textnorm.NameFoldSQL("s.normalized_title")
)

// fuzzyAuthors — похожие авторы (кроме exclude): по свёрнутому имени и по
// латинскому имени из fb2 переводов; внутри одинакового числа правок —
// известные первыми.
func (s *Service) fuzzyAuthors(ctx context.Context, words []string, exclude []int64, limit int) ([]fuzzyHit, error) {
	probe := fuzzyProbe(words)
	if probe == "" {
		return nil, nil
	}
	return s.fuzzyRows(ctx, `
		SELECT a.id, `+authorNameFold+`, lower(COALESCE(a.latin_name, '')), a.renown
		FROM authors a
		WHERE ($1::text <% `+authorNameFold+` OR $1::text <% a.latin_name)
		  AND NOT (a.id = ANY($2::bigint[]))
		ORDER BY GREATEST(word_similarity($1::text, `+authorNameFold+`),
		                  COALESCE(word_similarity($1::text, a.latin_name), 0)) DESC,
		         a.renown DESC, a.id
		LIMIT `+fmt.Sprint(fuzzyTrgmCandidates), probe, exclude, words, limit)
}

// fuzzySeries — похожие серии (кроме exclude); внутри одинакового числа правок —
// с большим числом книг первыми.
func (s *Service) fuzzySeries(ctx context.Context, words []string, exclude []int64, limit int) ([]fuzzyHit, error) {
	probe := fuzzyProbe(words)
	if probe == "" {
		return nil, nil
	}
	return s.fuzzyRows(ctx, `
		SELECT s.id, `+seriesNameFold+`, '', (SELECT count(*) FROM books b WHERE b.series_id = s.id)
		FROM series s
		WHERE $1::text <% `+seriesNameFold+`
		  AND NOT (s.id = ANY($2::bigint[]))
		ORDER BY word_similarity($1::text, `+seriesNameFold+`) DESC, s.id
		LIMIT `+fmt.Sprint(fuzzyTrgmCandidates), probe, exclude, words, limit)
}

func hitIDs(hits []fuzzyHit) []int64 {
	ids := make([]int64, len(hits))
	for i, h := range hits {
		ids[i] = h.id
	}
	return ids
}
