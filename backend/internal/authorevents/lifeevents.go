package authorevents

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Блок «В жизни автора в это время» на карточке книги: 2–4 события вокруг
// года написания. Смысл — тот же, что у таймлайна автора, но с точки зрения
// одной книги: что происходило, пока она писалась.
//
// ⚠️ Вся арифметика ГОДОВАЯ (грабля №21): written_year — год без месяца,
// поэтому «в год смерти дочери» честно, а «через три месяца после» — нет,
// даже когда у события date_precision='day'.

// Relation — связь события с годом написания книги.
const (
	RelationSameYear = "same_year"   // событие того же года
	RelationBefore   = "years_after" // книга написана через N лет после события
	RelationDuring   = "during"      // период накрывает год написания (каторга, эмиграция)
	RelationDelayed  = "delayed"     // «формирующее» событие сильно раньше (Дрезден → «Бойня №5»)
)

// LifeEvent — событие + его связь с книгой.
type LifeEvent struct {
	Event
	Relation string `json:"relation"`
	// YearsGap — сколько лет прошло (для years_after/delayed; 0 иначе).
	YearsGap int `json:"years_gap,omitempty"`
}

type LifeEventsResponse struct {
	AuthorID   int64  `json:"author_id"`
	AuthorName string `json:"author_name"`
	// WrittenYear — год написания книги; без него блок не строится.
	WrittenYear int         `json:"written_year,omitempty"`
	Items       []LifeEvent `json:"items"`
	// Eligible — показывать ли блок (тот же критерий таймлайна автора плюс
	// требование непустого окна).
	Eligible    bool                `json:"eligible"`
	Attribution []SourceAttribution `json:"attribution,omitempty"`
}

var ErrWorkNotFound = errors.New("work not found")

// Окно связи. Точечные события берём за [wy-windowYears .. wy]: книга пишется
// не мгновенно, а «что было за пару лет до» — тот самый контекст замысла.
// Вперёд не смотрим: событие после написания на книгу не влияло.
const (
	windowYears = 3
	maxItems    = 4
	// Порог «в окне пусто» — добираем одним формирующим событием (война,
	// каторга, травля), даже если оно давно: Воннегут писал «Бойню №5» через
	// 24 года после Дрездена, и это самая интересная связь в его биографии.
	sparseWindow    = 2
	formativeWeight = 5
	// Порог веса для блока на карточке книги — выше, чем «нетривиальное» в
	// таймлайне (2). Здесь всего 2–4 строки, и место дорогое: публикации и
	// переиздания (career, вес 2) вытесняли утраты и переезды, а читателю на
	// карточке книги нужны обстоятельства жизни, а не издательская хроника.
	bookBlockWeight = 3
	// Дальше этого формирующие события не тянем: «родился» тоже вес 0, но
	// «через 60 лет после войны» уже не связь, а совпадение.
	maxDelayedGap = 40
)

// ListForWork — события жизни автора вокруг года написания работы.
func (s *Service) ListForWork(ctx context.Context, workID int64, isAdmin bool) (LifeEventsResponse, error) {
	var (
		authorID    *int64
		authorName  string
		writtenYear *int
	)
	err := s.pool.QueryRow(ctx, `
		SELECT w.primary_author_id,
		       COALESCE(TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name)), ''),
		       w.written_year
		FROM works w
		LEFT JOIN authors a ON a.id = w.primary_author_id
		WHERE w.id = $1`, workID).Scan(&authorID, &authorName, &writtenYear)
	if errors.Is(err, pgx.ErrNoRows) {
		return LifeEventsResponse{}, ErrWorkNotFound
	}
	if err != nil {
		return LifeEventsResponse{}, fmt.Errorf("query work: %w", err)
	}

	resp := LifeEventsResponse{Items: []LifeEvent{}}
	if authorID != nil {
		resp.AuthorID = *authorID
	}
	resp.AuthorName = authorName
	if writtenYear != nil {
		resp.WrittenYear = *writtenYear
	}
	// Без автора или года написания связывать нечего.
	if resp.AuthorID == 0 || resp.WrittenYear == 0 {
		return resp, nil
	}

	// Критерий «не скучно» у автора — общий с таймлайном: если его лента
	// пуста/тривиальна, отдельные вехи на карточке книги тоже не нужны.
	authorEvents, err := s.List(ctx, resp.AuthorID, isAdmin)
	if err != nil {
		return LifeEventsResponse{}, err
	}
	if !authorEvents.Eligible {
		return resp, nil
	}

	resp.Items = selectLifeEvents(authorEvents.Items, resp.WrittenYear)
	resp.Eligible = len(resp.Items) > 0
	if resp.Eligible {
		resp.Attribution = authorEvents.Attribution
	}
	return resp, nil
}

// selectLifeEvents — отбор и типизация связей. Чистая функция (юнит-тесты).
func selectLifeEvents(events []Event, writtenYear int) []LifeEvent {
	var picked []LifeEvent
	for _, ev := range events {
		if ev.Hidden != nil && *ev.Hidden {
			continue
		}
		if ev.Weight < bookBlockWeight {
			continue // порог выше, чем у таймлайна: см. bookBlockWeight
		}
		switch {
		case ev.YearTo != nil && ev.YearFrom <= writtenYear && *ev.YearTo >= writtenYear && durationMatters(ev.Type):
			// Период накрывает год написания — самая сильная связь.
			picked = append(picked, LifeEvent{Event: ev, Relation: RelationDuring})
		case ev.YearFrom == writtenYear:
			picked = append(picked, LifeEvent{Event: ev, Relation: RelationSameYear})
		case ev.YearFrom < writtenYear && writtenYear-ev.YearFrom <= windowYears:
			picked = append(picked, LifeEvent{
				Event: ev, Relation: RelationBefore, YearsGap: writtenYear - ev.YearFrom,
			})
		}
	}
	// Сильная связь вперёд: during → same_year → недавнее.
	sortLifeEvents(picked)

	if len(picked) < sparseWindow {
		if f := formativeEvent(events, writtenYear); f != nil {
			picked = append(picked, *f)
		}
	}
	if len(picked) > maxItems {
		picked = picked[:maxItems]
	}
	return picked
}

// durationMatters — имеет ли смысл показывать период как «в это время».
//
// Брак и место жительства формально периоды (P26 с датами, P551), но
// семантически это СОСТОЯНИЕ: брак Толстого 1862–1910 накрывает каждую его
// книгу, и строка «в это время: Брак: Софья Андреевна» появлялась бы на всех
// карточках, вытесняя настоящие обстоятельства. Как момент (женитьба в год
// написания) они по-прежнему ловятся веткой same_year.
func durationMatters(t string) bool {
	switch t {
	case "war", "persecution", "isolation", "poverty", "illness", "relocation", "spiritual":
		return true
	}
	return false
}

// formativeEvent — одно «формирующее» событие сильно раньше книги (война,
// каторга, травля): ловит отложенные связи, ради которых фича и задумана.
func formativeEvent(events []Event, writtenYear int) *LifeEvent {
	var best *Event
	for i, ev := range events {
		if ev.Hidden != nil && *ev.Hidden {
			continue
		}
		if ev.Weight < formativeWeight {
			continue
		}
		end := ev.YearFrom
		if ev.YearTo != nil {
			end = *ev.YearTo
		}
		gap := writtenYear - end
		if gap <= windowYears || gap > maxDelayedGap {
			continue // ближние уже в окне, дальние — совпадение
		}
		// Ближайшее к книге из формирующих: свежая травма связнее давней.
		if best == nil || end > bestEnd(*best) {
			best = &events[i]
		}
	}
	if best == nil {
		return nil
	}
	end := best.YearFrom
	if best.YearTo != nil {
		end = *best.YearTo
	}
	return &LifeEvent{Event: *best, Relation: RelationDelayed, YearsGap: writtenYear - end}
}

func bestEnd(ev Event) int {
	if ev.YearTo != nil {
		return *ev.YearTo
	}
	return ev.YearFrom
}

// sortLifeEvents — during → same_year → years_after (ближе к книге раньше),
// внутри одной связи весомее вперёд. Свой компаратор вместо sort.Slice:
// порядок здесь — часть продуктового смысла, его фиксирует тест.
func sortLifeEvents(items []LifeEvent) {
	rank := func(r string) int {
		switch r {
		case RelationDuring:
			return 0
		case RelationSameYear:
			return 1
		case RelationBefore:
			return 2
		default:
			return 3
		}
	}
	for i := 1; i < len(items); i++ {
		for j := i; j > 0; j-- {
			a, b := items[j-1], items[j]
			less := false
			switch {
			case rank(b.Relation) != rank(a.Relation):
				less = rank(b.Relation) < rank(a.Relation)
			case b.YearsGap != a.YearsGap:
				less = b.YearsGap < a.YearsGap
			default:
				less = b.Weight > a.Weight
			}
			if !less {
				break
			}
			items[j-1], items[j] = b, a
		}
	}
}
