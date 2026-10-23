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
	SectionNextInSeries     SectionKind = "next_in_series"            // дальше в цикле
	SectionAuthorFinished   SectionKind = "author_you_finished"       // автор, которого дочитали
	SectionDropped          SectionKind = "dropped_then_loved_author" // брошенное, к чему дорос
	SectionForgottenClassic SectionKind = "forgotten_classic"         // забытая классика
	SectionReread           SectionKind = "reread"                    // перечитать
	SectionFitsTonight      SectionKind = "fits_tonight"              // на вечер: «у меня есть N минут»
	// Фаза 3 (recsys/03): напоминание о полке «Хочу прочитать».
	SectionToRead SectionKind = "to_read_reminder" // вы хотели прочитать
	// SectionMood — «Под настроение» (#580): только при выбранном настроении,
	// первой в ленте (mood_pool.go).
	SectionMood SectionKind = "mood"
)

// AllSections — все секции ленты в порядке Главной (для проверки настроек
// сворачивания; фронт держит тот же порядок в HOME_SECTIONS).
var AllSections = []SectionKind{
	SectionMood, SectionContinueReading, SectionSearchedAppeared, SectionNextInSeries, SectionAuthorFinished,
	SectionSubscriptions, SectionToRead, SectionDropped, SectionForgottenClassic, SectionReread, SectionFitsTonight,
	SectionRatePrompts,
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
	// ReasonDropped — бросили At, с тех пор полюбили Count работ автора AuthorID/Author.
	ReasonDropped ReasonKind = "dropped_then_loved_author"
	// ReasonReread — оценили на 5, прочитали At, с тех пор не открывали.
	ReasonReread ReasonKind = "reread"
	// ReasonForgottenClassic — Year написания, внешний Rating из Source (fantlab | librate), на инстансе никто не открывал.
	ReasonForgottenClassic ReasonKind = "forgotten_classic"
	// ReasonFitsTonight — укладывается в Minutes при личной скорости; Genre — любимый жанр (fb2-код).
	ReasonFitsTonight ReasonKind = "fits_tonight"
	// ReasonToRead — положили на полку «Хочу прочитать» At и с тех пор не открывали.
	ReasonToRead ReasonKind = "to_read"
	// ReasonMood — под настроение Mood (#580): Genre — fb2-код жанра настроения,
	// Minutes — «≈ N минут» у «короткого».
	ReasonMood ReasonKind = "mood"
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
	// Count — сколько (dropped_then_loved_author: полюбленных работ автора).
	Count int `json:"count,omitempty"`
	// Year — год написания (forgotten_classic).
	Year int `json:"year,omitempty"`
	// Rating/Source — внешний рейтинг и его источник (forgotten_classic: "fantlab" — средняя 1–10, "librate" — 1–5).
	Rating float64 `json:"rating,omitempty"`
	Source string  `json:"source,omitempty"`
	// Genre — fb2-код жанра (fits_tonight; имя — фронт по useGenreMap).
	Genre string `json:"genre,omitempty"`
	// Mood — карточка поднята под настроение сессии (recsys/03, 3.4; mood.go):
	// у секций правил — пересортировка, у «Забытой классики» — фильтр.
	Mood Mood `json:"mood,omitempty"`
}

// Item — карточка ленты. ID — представительное ИЗДАНИЕ (on-demand обложка,
// фолбэк ссылки; у «продолжить чтение» — то, где лежит позиция), WorkID —
// работа для ссылки /works/{id}. CoverPath — обложка издания или соседнего
// живого издания работы. AuthorIDs — те же авторы, что Authors, в том же
// порядке (kebab «не предлагать автора», recsys/03 3.2); SeriesID — цикл
// подписи Series. Заполняет Hydrate; у секций history их нет.
type Item struct {
	ID        int64    `json:"id"`
	WorkID    int64    `json:"work_id,omitempty"`
	Title     string   `json:"title"`
	Authors   []string `json:"authors"`
	AuthorIDs []int64  `json:"author_ids,omitempty"`
	Series    string   `json:"series,omitempty"`
	SeriesID  int64    `json:"series_id,omitempty"`
	LibID     string   `json:"lib_id"`
	CoverPath string   `json:"cover_path,omitempty"`
	Reasons   []Reason `json:"reasons"`
}

// RatingReason — почему оценка низкая (план recsys/03, задача 3.1): закрытый
// список чипов второго шага после оценки 1–2 (по желанию — 3); несколько на
// работу, таблица rating_reasons. Что с ними делает модель близости — affinity.go
// (ReasonNotMine усиливает минус к автору и жанру, ReasonLanguage — к языку
// оригинала, ReasonTooLong — «На вечер» предпочитает короткое).
type RatingReason string

const (
	ReasonTooLong    RatingReason = "too_long"   // слишком длинно
	ReasonGloomy     RatingReason = "gloomy"     // мрачно
	ReasonLanguage   RatingReason = "language"   // язык / перевод
	ReasonNotMine    RatingReason = "not_mine"   // не моё
	ReasonUnfinished RatingReason = "unfinished" // не дочитал
	ReasonDated      RatingReason = "dated"      // устарело
	ReasonBoring     RatingReason = "boring"     // скучно
)

// AllRatingReasons — порядок чипов в UI; тот же список — в CHECK миграции 0068.
var AllRatingReasons = []RatingReason{
	ReasonTooLong, ReasonGloomy, ReasonLanguage, ReasonNotMine, ReasonUnfinished, ReasonDated, ReasonBoring,
}

// IsRatingReason — s — известная причина.
func IsRatingReason(s string) bool {
	for _, r := range AllRatingReasons {
		if string(r) == s {
			return true
		}
	}
	return false
}

// Mood — настроение сессии (план recsys/03, задача 3.4): чип на Главной,
// живёт в браузере и уходит параметром mood= в GET /me/recommendations;
// выбор логируется в session_mood (миграция 0069). Закрытый список. До фазы 7
// (атрибуты работ) настроение переводится в веса жанров по mood-genres.json
// (mood.go); «короткое» — ещё и по размеру издания.
type Mood string

const (
	MoodLight Mood = "light" // лёгкое
	MoodSmart Mood = "smart" // умное
	MoodDark  Mood = "dark"  // мрачное
	MoodFunny Mood = "funny" // смешное
	MoodShort Mood = "short" // короткое
)

// AllMoods — порядок чипов в UI; тот же список — в CHECK миграции 0069.
var AllMoods = []Mood{MoodLight, MoodSmart, MoodDark, MoodFunny, MoodShort}

// IsMood — s — известное настроение.
func IsMood(s string) bool {
	for _, m := range AllMoods {
		if string(m) == s {
			return true
		}
	}
	return false
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

// Negative — скрытие с этой причиной отрицает вкус: «не интересно» и скрытия
// без причины (до 1.57). «Уже читал» и «потом» — нет. Одно определение на
// модель близости (Score) и персону поиска (history.PersonaProfile, recsys/03 3.5).
func (r DismissReason) Negative() bool {
	return r == "" || r == DismissNotInterested
}

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
