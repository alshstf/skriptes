// Package awards — премии (#389, A2): лауреаты премий из белого списка владельца
// (`~/projects/plans/skriptes/awards-dossier.md`, принят 2026-10-07). Источники —
// Фантлаб (лауреаты по годам и номинациям), Wikidata (американские книжные премии,
// кинопремии экранизаций) и список в репозитории (manual.json — русские премии,
// которых нет на Фантлабе); сопоставление с каталогом — по названию и фамилии,
// кинопремии — через экранизации книг (book_adaptations). Премии вне списка не
// показываются и не учитываются.
package awards

// Award — премия белого списка.
type Award struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// Group — раздел списка: «Русские», «Фантастика (русская)», «Международные».
	Group string `json:"group"`
	// FantlabID — id премии на Фантлабе (fantlab.ru/awardN); источник — Фантлаб.
	FantlabID int `json:"-"`
	// Wikidata — элементы премии в Wikidata по номинациям; источник — Wikidata.
	Wikidata []WikidataItem `json:"-"`
	// Manual — лауреаты из списка в репозитории (manual.json).
	Manual bool `json:"-"`
	// Film — кинопремия: лауреат — фильм или сериал, показываем экранизации книг
	// из каталога (сопоставление по QID фильма в book_adaptations).
	Film bool `json:"film,omitempty"`
	// AuthorLevel — премия вручается автору (Нобелевская, «Аэлита»), а не произведению.
	AuthorLevel bool `json:"author_level,omitempty"`
	// MaxYear — последний учитываемый год (0 — все): «довоенные сезоны» (решение владельца).
	MaxYear int `json:"max_year,omitempty"`
	// Nominations — учитываемые номинации Фантлаба (nil — все; 0 — без номинации).
	Nominations []int `json:"-"`
	// Description и Site — о премии и её сайт (about.go, #446).
	Description string `json:"description,omitempty"`
	Site        string `json:"site,omitempty"`
	// Logo — есть картинка премии (logo.go, #446): GET /api/awards/{key}/logo.
	Logo bool `json:"logo,omitempty"`
}

// WikidataItem — элемент премии в Wikidata и номинация, которую он означает.
type WikidataItem struct {
	QID        string
	Nomination string
}

// Источники лауреатов (award_wins.source).
const (
	sourceFantlab  = "fantlab"
	sourceWikidata = "wikidata"
	sourceManual   = "manual"
)

// source — откуда берутся лауреаты премии.
func (a Award) source() string {
	switch {
	case a.Manual:
		return sourceManual
	case len(a.Wikidata) > 0:
		return sourceWikidata
	default:
		return sourceFantlab
	}
}

const (
	groupRussian = "Русская литература"
	groupGenreRu = "Русская фантастика"
	groupIntl    = "Международные"
	groupScreen  = "Кино и сериалы по книгам"
)

// Catalog — белый список (порядок — порядок в разделе «Премии»). Номинации —
// тоже белым списком (досье, «Решения»): книжные, без журналов, фэнзинов,
// постановок, художников, редакторов, издательств и спецпризов; 0 — основная
// премия без номинации.
var Catalog = []Award{
	// Русская литература.
	{Key: "russian-booker", Name: "Русский Букер", Group: groupRussian, FantlabID: 36,
		Nominations: []int{261, 258, 1022}},
	{Key: "natsbest", Name: "Национальный бестселлер", Group: groupRussian, FantlabID: 89, MaxYear: 2021,
		Nominations: []int{0, 696, 697}},
	{Key: "nos", Name: "НОС", Group: groupRussian, FantlabID: 226,
		Nominations: []int{0, 1488, 2648, 3195}},
	{Key: "dar", Name: "Дар", Group: groupRussian, Manual: true},
	{Key: "prosvetitel", Name: "Просветитель", Group: groupRussian, Manual: true},
	{Key: "bely", Name: "Премия Андрея Белого", Group: groupRussian, Manual: true, AuthorLevel: true},
	// Русская фантастика.
	{Key: "new-horizons", Name: "Новые горизонты", Group: groupGenreRu, FantlabID: 251,
		Nominations: []int{0, 4291, 4342}},
	{Key: "interpresscon", Name: "Интерпресскон", Group: groupGenreRu, FantlabID: 11, MaxYear: 2021,
		Nominations: []int{46, 47, 48, 49, 51, 70, 2502}},
	{Key: "roscon", Name: "РосКон", Group: groupGenreRu, FantlabID: 20, MaxYear: 2021, Nominations: []int{101, 102}},
	{Key: "filigran", Name: "Филигрань", Group: groupGenreRu, FantlabID: 32, MaxYear: 2021,
		Nominations: []int{239, 240, 241}},
	{Key: "zilantcon", Name: "Зиланткон", Group: groupGenreRu, FantlabID: 27, MaxYear: 2021, Nominations: []int{206}},
	{Key: "abs", Name: "АБС-премия", Group: groupGenreRu, FantlabID: 13, Nominations: []int{82, 83}},
	{Key: "bronze-snail", Name: "Бронзовая улитка", Group: groupGenreRu, FantlabID: 12, Nominations: []int{55, 56, 57, 58}},
	{Key: "strannik", Name: "Странник", Group: groupGenreRu, FantlabID: 18,
		Nominations: []int{0, 71, 72, 73, 74, 254, 255, 256, 257, 507, 508, 509, 510, 625, 626, 627, 628, 947, 1088}},
	{Key: "sword-without-name", Name: "Меч без имени", Group: groupGenreRu, FantlabID: 56, Nominations: []int{1055, 526}},
	{Key: "marble-faun", Name: "Мраморный фавн", Group: groupGenreRu, FantlabID: 49,
		Nominations: []int{360, 361, 362, 363, 364, 365}},
	{Key: "aelita", Name: "Аэлита", Group: groupGenreRu, FantlabID: 9, MaxYear: 2021, AuthorLevel: true, Nominations: []int{21}},
	{Key: "belyaev", Name: "Беляевская премия", Group: groupGenreRu, FantlabID: 21,
		Nominations: []int{137, 138, 139, 1771, 4606}},
	{Key: "mir-fantastiki", Name: "Итоги года «Мира фантастики»", Group: groupGenreRu, FantlabID: 53,
		Nominations: []int{497, 684, 680, 681, 682, 2434, 3137, 4138, 490, 1164, 1167, 1168, 1169, 2435, 2437, 3138, 3139,
			3618, 3619, 488, 489, 491, 492, 493, 494, 495, 496, 498, 499, 500, 506, 559, 560, 561, 562, 565, 642, 644, 683,
			685, 911, 912}},
	{Key: "fantlab-book", Name: "Книга года Фантлаба", Group: groupGenreRu, FantlabID: 86,
		Nominations: []int{688, 689, 690, 691, 692, 693, 694, 1149, 1150, 2834, 1634, 4451}},
	{Key: "masters-of-horror", Name: "Мастера ужасов", Group: groupGenreRu, FantlabID: 514,
		Nominations: []int{2781, 2782, 2783, 2784, 3067, 3068, 3069, 3070, 3072, 3078, 3532, 4492}},
	// Международные.
	{Key: "nobel", Name: "Нобелевская премия по литературе", Group: groupIntl, FantlabID: 74, AuthorLevel: true},
	{Key: "booker", Name: "Букеровская премия", Group: groupIntl, FantlabID: 73,
		Nominations: []int{0, 663, 641, 2699, 748, 749}},
	{Key: "goncourt", Name: "Гонкуровская премия", Group: groupIntl, FantlabID: 90, Nominations: []int{0}},
	{Key: "pulitzer", Name: "Пулитцеровская премия", Group: groupIntl,
		Wikidata: []WikidataItem{{QID: "Q833633", Nomination: "Художественная книга"}}},
	{Key: "nba", Name: "Национальная книжная премия США", Group: groupIntl,
		Wikidata: []WikidataItem{{QID: "Q3873144", Nomination: "Художественная книга"}}},
	{Key: "hugo", Name: "Хьюго", Group: groupIntl, FantlabID: 2,
		Nominations: []int{1, 2, 10, 3, 2490, 11, 2684, 774, 15, 112}},
	{Key: "nebula", Name: "Небьюла", Group: groupIntl, FantlabID: 3, Nominations: []int{16, 17, 18, 19, 113}},
	{Key: "world-fantasy", Name: "Всемирная премия фэнтези", Group: groupIntl, FantlabID: 4,
		Nominations: []int{59, 60, 62, 63, 64, 65}},
	{Key: "locus", Name: "Локус", Group: groupIntl, FantlabID: 5,
		Nominations: []int{36, 41, 42, 43, 126, 127, 128, 4584, 38, 39, 37, 40, 44, 45, 977, 133}},
	{Key: "clarke", Name: "Премия Артура Кларка", Group: groupIntl, FantlabID: 6},
	// Кино и сериалы: экранизации книг из каталога — только главные награды.
	{Key: "oscar", Name: "Оскар", Group: groupScreen, Film: true, Wikidata: []WikidataItem{
		{QID: "Q102427", Nomination: "Лучший фильм"}, {QID: "Q107258", Nomination: "Лучший адаптированный сценарий"}}},
	{Key: "cannes", Name: "Каннский кинофестиваль", Group: groupScreen, Film: true, Wikidata: []WikidataItem{
		{QID: "Q179808", Nomination: "Золотая пальмовая ветвь"}}},
	{Key: "golden-globe", Name: "Золотой глобус", Group: groupScreen, Film: true, Wikidata: []WikidataItem{
		{QID: "Q1011509", Nomination: "Лучший фильм (драма)"}, {QID: "Q670282", Nomination: "Лучший фильм (комедия или мюзикл)"}}},
	{Key: "bafta", Name: "BAFTA", Group: groupScreen, Film: true, Wikidata: []WikidataItem{
		{QID: "Q139184", Nomination: "Лучший фильм"}}},
	{Key: "emmy", Name: "Эмми", Group: groupScreen, Film: true, Wikidata: []WikidataItem{
		{QID: "Q989438", Nomination: "Лучший драматический сериал"}, {QID: "Q20714679", Nomination: "Лучший мини-сериал"},
		{QID: "Q2110156", Nomination: "Лучший комедийный сериал"}}},
}

// ByKey — премия белого списка по ключу.
func ByKey(key string) (Award, bool) {
	for _, a := range Catalog {
		if a.Key == key {
			return a, true
		}
	}
	return Award{}, false
}

// allows — учитывать ли лауреата: год и номинация.
func (a Award) allows(year, nomination int) bool {
	if a.MaxYear > 0 && year > a.MaxYear {
		return false
	}
	if a.Nominations == nil {
		return true
	}
	for _, n := range a.Nominations {
		if n == nomination {
			return true
		}
	}
	return false
}
