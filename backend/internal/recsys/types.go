package recsys

import "time"

// Контракт ленты рекомендаций (план recsys/01, задача 1.3): каждая карточка —
// Item с хотя бы одной причиной Reason; текст причины собирает фронт по Kind
// (components/RecReason.tsx), бэк отдаёт только параметры. Секция — набор
// карточек одного происхождения (SectionKind); главная собирает секции в
// порядке фронта.

// SectionKind — секция ленты. Закрытый список: неизвестную секцию API
// отвергает (400).
type SectionKind string

const (
	SectionContinueReading  SectionKind = "continue_reading"  // книги в процессе
	SectionSubscriptions    SectionKind = "subscriptions"     // новинки по подпискам
	SectionRatePrompts      SectionKind = "rate_prompts"      // «оцените прочитанное»
	SectionSearchedAppeared SectionKind = "searched_appeared" // появилось то, что искали
	// Правила фазы 2 (rules.go): секция = правило.
	SectionNextInSeries   SectionKind = "next_in_series"      // дальше в цикле
	SectionAuthorFinished SectionKind = "author_you_finished" // автор, которого дочитали
)

// AllSections — все секции ленты (для проверки настроек сворачивания).
var AllSections = []SectionKind{
	SectionContinueReading, SectionSearchedAppeared, SectionNextInSeries, SectionAuthorFinished,
	SectionSubscriptions, SectionRatePrompts,
}

// IsSectionKind — s — известная секция ленты.
func IsSectionKind(s string) bool {
	for _, k := range AllSections {
		if string(k) == s {
			return true
		}
	}
	return false
}

// ReasonKind — почему карточка здесь. Закрытый список; у каждого вида — свой
// набор заполненных полей Reason (см. комментарии у констант).
type ReasonKind string

const (
	// ReasonInProgress — остановились на Fraction, последнее движение At, ещё Minutes.
	ReasonInProgress ReasonKind = "in_progress"
	// ReasonNewByAuthor — новинка автора AuthorID/Author из подписок, добавлена At.
	ReasonNewByAuthor ReasonKind = "new_by_author"
	// ReasonNewInSeries — новинка цикла SeriesID/Series из подписок, добавлена At.
	ReasonNewInSeries ReasonKind = "new_in_series"
	// ReasonAcquired — приобретено (OPDS/Kindle) At, оценки ещё нет.
	ReasonAcquired ReasonKind = "acquired"
	// ReasonFinished — дочитано (или почти) — At, если известно.
	ReasonFinished ReasonKind = "finished"
	// ReasonSearchedAppeared — искали Query (At — когда), не нашлось, появилось при импорте.
	ReasonSearchedAppeared ReasonKind = "searched_appeared"
	// ReasonNextInSeries — прочитали WorkID/Title (At) из цикла SeriesID/Series; это № SerNo.
	ReasonNextInSeries ReasonKind = "next_in_series"
	// ReasonAuthorFinished — прочитали WorkID/Title (At) автора AuthorID/Author; это ещё его работа.
	ReasonAuthorFinished ReasonKind = "author_you_finished"
)

// Reason — причина рекомендации: вид и параметры. Поля — плоские и
// необязательные, чтобы контракт был типизирован и в Go, и в TS (без map[any]).
type Reason struct {
	Kind     ReasonKind `json:"kind"`
	At       *time.Time `json:"at,omitempty"`
	Fraction *float64   `json:"fraction,omitempty"`
	AuthorID int64      `json:"author_id,omitempty"`
	Author   string     `json:"author,omitempty"`
	SeriesID int64      `json:"series_id,omitempty"`
	Series   string     `json:"series,omitempty"`
	// Query — текст поиска (searched_appeared).
	Query string `json:"query,omitempty"`
	// Minutes — «ещё ≈ N минут» по личной скорости чтения (in_progress; 0 — неизвестно).
	Minutes int `json:"minutes,omitempty"`
	// WorkID/Title — «потому что X»: работа-основание (правила фазы 2).
	WorkID int64  `json:"work_id,omitempty"`
	Title  string `json:"title,omitempty"`
	// SerNo — номер рекомендуемой книги в цикле (next_in_series; 0 — нет).
	SerNo int `json:"ser_no,omitempty"`
}

// Item — карточка ленты. ID — представительное ИЗДАНИЕ (on-demand обложка,
// фолбэк ссылки; у «продолжить чтение» — то, где лежит позиция), WorkID —
// работа для ссылки /works/{id}. CoverPath — обложка издания или соседнего
// живого издания работы.
type Item struct {
	ID        int64    `json:"id"`
	WorkID    int64    `json:"work_id,omitempty"`
	Title     string   `json:"title"`
	Authors   []string `json:"authors"`
	Series    string   `json:"series,omitempty"`
	LibID     string   `json:"lib_id"`
	CoverPath string   `json:"cover_path,omitempty"`
	Reasons   []Reason `json:"reasons"`
}

// Reason — первая причина вида kind (для тестов и трассы).
func (it Item) Reason(kind ReasonKind) (Reason, bool) {
	for _, r := range it.Reasons {
		if r.Kind == kind {
			return r, true
		}
	}
	return Reason{}, false
}

// Section — ответ GET /api/me/recommendations?section=.
type Section struct {
	Kind  SectionKind `json:"section"`
	Items []Item      `json:"items"`
}

// DismissReason — причина скрытия карточки (feed_dismissals.reason).
// Негатив для модели близости — только DismissNotInterested (и пустая причина
// у скрытий до 1.57): «уже читал» и «потом» вкус не отрицают.
type DismissReason string

const (
	DismissNotInterested DismissReason = "not_interested"
	DismissAlreadyRead   DismissReason = "already_read"
	DismissLater         DismissReason = "later"
)

// ParseDismissReason — "" → DismissNotInterested; неизвестное → ok=false.
func ParseDismissReason(s string) (DismissReason, bool) {
	switch DismissReason(s) {
	case "":
		return DismissNotInterested, true
	case DismissNotInterested, DismissAlreadyRead, DismissLater:
		return DismissReason(s), true
	}
	return "", false
}
