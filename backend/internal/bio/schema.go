// Package bio — курируемый датасет био-таймлайна автора: вехи жизни, главы,
// периоды написания работ и исторический фон, ключ — Wikidata QID. Датасет
// лежит в репозитории (data/authors/<QID>.json, data/context/<region>.json),
// встраивается в бинарник и на старте сеется в Postgres (кэш для чтения и
// «скрыть»); источник правды — файлы. Генерация — cmd/biogen (офлайн, LLM +
// автопроверка + ревью), runtime внешних вызовов не делает.
//
// План и решения владельца — ~/projects/plans/skriptes/bio-timeline-v3-plan.md.
package bio

// Precision — точность даты: год, месяц или день. ISO нового стиля.
type Precision string

const (
	PrecisionYear  Precision = "year"
	PrecisionMonth Precision = "month"
	PrecisionDay   Precision = "day"
)

// Tier — вес вехи: major показывается всегда (в свёрнутом тизере и на
// карточке книги), minor — только в развёрнутом таймлайне, приглушённо.
type Tier string

const (
	TierMajor Tier = "major"
	TierMinor Tier = "minor"
)

// EventType — тип вехи (иконка в UI, порядок в окне карточки книги).
type EventType string

const (
	EventBirth       EventType = "birth"
	EventDeath       EventType = "death"
	EventLoss        EventType = "loss"   // смерть близкого
	EventLove        EventType = "love"   // брак, роман, развод
	EventChild       EventType = "child"  // рождение ребёнка
	EventFamily      EventType = "family" // прочее семейное
	EventIllness     EventType = "illness"
	EventWar         EventType = "war"         // фронт, служба, плен
	EventPersecution EventType = "persecution" // арест, ссылка, травля, запрет
	EventRelocation  EventType = "relocation"  // переезд, эмиграция, возвращение
	EventPoverty     EventType = "poverty"     // долги, нищета
	EventFaith       EventType = "faith"       // кризис веры, обращение
	EventEducation   EventType = "education"
	EventCareer      EventType = "career"   // признание, провал, редакторство
	EventCreation    EventType = "creation" // обстоятельства работы над текстом
	EventOther       EventType = "other"
)

// EventTypes — все допустимые типы в порядке объявления.
var EventTypes = []EventType{
	EventBirth, EventDeath, EventLoss, EventLove, EventChild, EventFamily, EventIllness, EventWar,
	EventPersecution, EventRelocation, EventPoverty, EventFaith, EventEducation, EventCareer,
	EventCreation, EventOther,
}

// Капы датасета (план v3): major ≤ 20 (якоря рождения/смерти не в счёт), всего ≤ 45 вех на автора; short ≤ 60
// символов (строка десктопа), long ≤ 300.
const (
	MaxMajorEvents = 20
	MaxEvents      = 45
	MaxShortRunes  = 60
	MaxLongRunes   = 300
	MaxNoteRunes   = 200
)

// Date — дата вехи. From/To — ISO с точностью Precision ("1868", "1868-05",
// "1868-05-24"); To задан только у периодов (каторга, эмиграция).
type Date struct {
	From      string    `json:"from"`
	To        string    `json:"to,omitempty"`
	Precision Precision `json:"precision"`
}

// WorkLink — явный линк вехи на работу автора (ref из Author.Works). Note —
// только задокументированная связь (письма, воспоминания, литературоведение),
// показывается как пометка, не как факт.
type WorkLink struct {
	Ref  string `json:"ref"`
	Note string `json:"note,omitempty"`
}

// Place — место вехи; QID — Wikidata места, если известен.
type Place struct {
	Label string `json:"label"`
	QID   string `json:"qid,omitempty"`
}

// Evidence — основание вехи для автопроверки генератора: короткая цитата из
// источника, в которой найден факт и дата. В UI не выводится, в PG не сеется.
type Evidence struct {
	Source string `json:"source"`
	Quote  string `json:"quote"`
}

// Event — веха. Key стабилен между регенерациями: "<type>-<yyyy>[-<n>]" — по
// нему живёт «скрыть». Short — именная группа без года и названий работ.
type Event struct {
	Key      string     `json:"key"`
	Date     Date       `json:"date"`
	Type     EventType  `json:"type"`
	Tier     Tier       `json:"tier"`
	Short    string     `json:"short"`
	Long     string     `json:"long,omitempty"`
	Place    *Place     `json:"place,omitempty"`
	Works    []WorkLink `json:"works,omitempty"`
	Chapter  string     `json:"chapter,omitempty"`
	Evidence *Evidence  `json:"evidence,omitempty"`
}

// Chapter — глава жизни (секция полной страницы), годы включительно.
type Chapter struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	From  int    `json:"from"`
	To    int    `json:"to"`
}

// Written — период написания работы, годы включительно.
type Written struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// Work — работа автора в датасете: ref для линков, заголовок для матчинга с
// каталогом (точный нормализованный), QID — приоритетный ключ матчинга.
type Work struct {
	Ref     string   `json:"ref"`
	Title   string   `json:"title"`
	QID     string   `json:"qid,omitempty"`
	Written *Written `json:"written,omitempty"`
	Aliases []string `json:"aliases,omitempty"`
}

// Source — источник (атрибуция в подвале секции).
type Source struct {
	ID  string `json:"id"`
	URL string `json:"url"`
	Rev int64  `json:"rev,omitempty"`
}

// Author — запись датасета по одному автору.
type Author struct {
	QID      string    `json:"qid"`
	Name     string    `json:"name"`
	Born     string    `json:"born"`
	Died     string    `json:"died,omitempty"`
	Sources  []Source  `json:"sources,omitempty"`
	Context  []string  `json:"context,omitempty"` // регионы исторического фона (data/context/<region>.json)
	Chapters []Chapter `json:"chapters,omitempty"`
	Events   []Event   `json:"events"`
	Works    []Work    `json:"works,omitempty"`
}

// ContextEvent — событие исторического фона (ручной датасет по региону).
type ContextEvent struct {
	Key   string `json:"key"`
	From  int    `json:"from"`
	To    int    `json:"to,omitempty"`
	Short string `json:"short"`
}

// ContextRegion — файл data/context/<region>.json.
type ContextRegion struct {
	Region string         `json:"region"`
	Title  string         `json:"title"`
	Events []ContextEvent `json:"events"`
}
