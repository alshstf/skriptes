package importer

import (
	"context"
	"fmt"

	"github.com/meilisearch/meilisearch-go"
)

const booksIndex = "books"

// worksIndex — индекс ЛОГИЧЕСКИХ книг (works). В отличие от booksIndex (один
// документ на издание + distinctAttribute=work_id), здесь один документ на
// работу, поэтому фасетные счётчики считают РАБОТЫ, а не издания. Веб-список
// (/api/books) и Cmd+K ищут здесь; OPDS остаётся на booksIndex (скачивание
// идёт по id издания, схлопывание делает distinct).
const worksIndex = "works"

// MeiliMaxTotalHits — потолок пагинации/подсчёта ОБОИХ индексов (books, works).
// Meili-дефолт 1000 капил EstimatedTotalHits (счётчик «N книг» на /books всегда
// показывал 1000 на большой коллекции) и молча обрезал deep-paging: за капом
// Meili возвращает пустые hits. Значение с запасом больше коллекции (~470k
// изданий); память заранее не аллоцируется — это только верхняя граница
// глубины. books.Service зеркалит константу (meiliMaxTotalHits) как guard
// глубины offset — тест-синхронизатор в books/service_guard_test.go.
const MeiliMaxTotalHits = 1_000_000

// MeiliMaxValuesPerFacet — сколько значений фасета отдаёт Meili. По умолчанию 100
// по алфавиту: в коллекции ~350 жанров и ~170 языков оригинала, и «русский»,
// «фантастика» и всё после буквы «f» в фасет не попадали (#276). Порядок в
// фильтрах наводит фронт.
const MeiliMaxValuesPerFacet = 1000

// bookDoc — документ для индекса "books" в Meilisearch.
// id используется как primary key (совпадает с books.id в Postgres).
type bookDoc struct {
	ID              int64    `json:"id"`
	Title           string   `json:"title"`
	NormalizedTitle string   `json:"normalized_title"`
	Authors         []string `json:"authors"`
	AuthorIDs       []int64  `json:"author_ids"`
	Series          string   `json:"series,omitempty"`
	SeriesID        *int64   `json:"series_id,omitempty"`
	Genres          []string `json:"genres"`
	Year            *int     `json:"year,omitempty"` // = written_year; синкается ResyncYears (НЕ date_added)
	Lang            string   `json:"lang,omitempty"`
	Popularity      int64    `json:"popularity"` // обновляется отдельным процессом
	LibID           string   `json:"lib_id"`
	Archive         string   `json:"archive"`
	// WorkID — логическая книга (works.id). distinctAttribute индекса = work_id:
	// поиск/список отдают ОДНО издание на работу (схлопывание дублей/переводов).
	// Синкается ResyncWorkIDs (импорт + после прохода группировки).
	WorkID int64 `json:"work_id"`
}

// ConfigureIndex применяет настройки индекса books идемпотентно — для вызова на
// СТАРТЕ backend (а не только при импорте): на стабильном деплое без нового
// inpx импорт пропускается по хэшу, и новые настройки (например
// distinctAttribute=work_id из Phase 3) иначе не применились бы.
func (im *Importer) ConfigureIndex(ctx context.Context) error {
	return configureIndex(ctx, im.deps.Meili)
}

// ConfigureWorksIndex применяет настройки индекса works идемпотентно. Зеркало
// ConfigureIndex — вызывать на каждом старте, чтобы индекс существовал и имел
// нужные filterable/sortable атрибуты даже на стабильном деплое без импорта.
func (im *Importer) ConfigureWorksIndex(ctx context.Context) error {
	return configureWorksIndex(ctx, im.deps.Meili, worksIndex, im.builtWorksSchema(ctx))
}

// Версии схемы works-индекса, с которых у документов есть поисковые поля:
//   - foldedSearchSchemaVersion — title_s/authors_s/series_s (свёртка «ё»→«е», #278);
//   - altTitlesSchemaVersion — alt_titles_s (названия изданий и оригинала) и
//     authors_latin (латинское имя автора из fb2 переводов, #291).
const (
	foldedSearchSchemaVersion = 9
	altTitlesSchemaVersion    = 11
)

// builtWorksSchema — схема, которой собран живой works-индекс (последний флаг
// works_index_synced_v<N>). Поиск переключается на новые поля только после
// пересборки ими (RebuildWorksIndex собирает временный индекс сразу с ними): иначе
// старые документы без полей не находились бы, а переиндексация Meili 1.13 при
// смене searchableAttributes ещё и оставляет битой близость слов (см.
// RebuildWorksIndex).
func (im *Importer) builtWorksSchema(ctx context.Context) int {
	if im.deps.Pool == nil {
		return 0
	}
	var v int
	if err := im.deps.Pool.QueryRow(ctx, `
		SELECT COALESCE(max(substring(key FROM 'works_index_synced_v([0-9]+)')::int), 0)
		FROM app_settings WHERE key LIKE 'works_index_synced_v%'`).Scan(&v); err != nil {
		return 0
	}
	return v
}

// worksSearchable — поисковые поля works-индекса для схемы schema. Порядок важен:
// правило ранжирования attribute ставит совпадение в названии выше совпадения в
// авторе, а альтернативные названия и латинские имена — ниже всех.
func worksSearchable(schema int) []string {
	switch {
	case schema >= altTitlesSchemaVersion:
		return []string{"title_s", "authors_s", "series_s", "alt_titles_s", "authors_latin"}
	case schema >= foldedSearchSchemaVersion:
		return []string{"title_s", "authors_s", "series_s"}
	default:
		return []string{"title", "authors", "series"}
	}
}

// workDoc — документ индекса "works". id = works.id (primary key). Поля авторов/
// жанров/языков — UNION по живым изданиям работы. lang — массив (json-ключ
// "lang"), фильтруется/фасетится как и в booksIndex, но считает работы.
type workDoc struct {
	ID              int64    `json:"id"`
	Title           string   `json:"title"`
	NormalizedTitle string   `json:"normalized_title"`
	Authors         []string `json:"authors"`
	AuthorIDs       []int64  `json:"author_ids"`
	Series          string   `json:"series,omitempty"`
	// Поисковые копии title/authors/series со свёрткой «ё»→«е» (textnorm.FoldYo):
	// Meili не приравнивает их, а показывать надо исходное написание (#278).
	TitleSearch   string   `json:"title_s"`
	AuthorsSearch []string `json:"authors_s"`
	SeriesSearch  string   `json:"series_s,omitempty"`
	// Только для поиска (#291): названия изданий, отличные от названия работы, и
	// названия оригинала (src_title) — «The Hobbit» находит «Хоббита»; латинские
	// имена авторов оригинала (src_author_normalized) — «doyle» находит Конан Дойля.
	AltTitlesSearch []string `json:"alt_titles_s"`
	AuthorsLatin    []string `json:"authors_latin"`
	SeriesID        *int64   `json:"series_id,omitempty"`
	Genres          []string `json:"genres"`
	Year            *int     `json:"year,omitempty"` // = written_year (COALESCE work → min издания)
	Langs           []string `json:"lang"`           // массив языков всех изданий работы
	SrcLangs        []string `json:"src_lang"`       // массив языков ОРИГИНАЛА изданий (fb2 src-lang; пусто = неизвестен/не перевод)
	// OrigLangs — ЭФФЕКТИВНЫЙ язык оригинала: src_lang, а если пусто — язык
	// издания (натив = сам себе оригинал). На нём стоит фильтр «Язык оригинала»
	// (/books, авторы): «оригинал: французский» ловит и переводы с французского
	// (src_lang=fr), и нативно-французские (lang=fr, src_lang пуст). SrcLangs
	// остаётся сырым — карточка показывает «Перевод с …» только у переводов.
	OrigLangs    []string `json:"orig_lang"`
	Popularity   int64    `json:"popularity"` // сумма популярности изданий
	EditionCount int      `json:"edition_count"`
	// Kind — тип работы: "" (обычное произведение) | collection | anthology |
	// omnibus (works.kind, миграция 0034). Пишем ВСЕГДА (пустую строку для
	// обычных), чтобы NOT-фильтр «скрыть сборники» не зависел от поведения
	// Meili на отсутствующем поле.
	Kind string `json:"kind"`

	// renownPop — популярность работы БЕЗ внутриинстансной вовлечённости
	// (computeWorkPopularityExternal) — сырьё для authors.renown. Неэкспортируемое:
	// в Meili-док НЕ сериализуется (это не поле индекса).
	renownPop int64
}

// configureWorksIndex создаёт и настраивает индекс works (uid — живой или
// временный для пересборки) идемпотентно. Без distinctAttribute: каждый
// документ уже = одна работа. schema — схема документов индекса: от неё зависят
// поисковые поля (worksSearchable, builtWorksSchema).
func configureWorksIndex(ctx context.Context, m meilisearch.ServiceManager, uid string, schema int) error {
	idx := m.Index(uid)
	if _, err := m.CreateIndexWithContext(ctx, &meilisearch.IndexConfig{
		Uid:        uid,
		PrimaryKey: "id",
	}); err != nil {
		if !isMeiliAlreadyExists(err) {
			return fmt.Errorf("create works index: %w", err)
		}
	}
	searchable := worksSearchable(schema)
	if _, err := idx.UpdateSearchableAttributesWithContext(ctx, &searchable); err != nil {
		return fmt.Errorf("works update searchable: %w", err)
	}
	filterable := []any{"genres", "lang", "src_lang", "orig_lang", "year", "series_id", "author_ids", "kind"}
	if _, err := idx.UpdateFilterableAttributesWithContext(ctx, &filterable); err != nil {
		return fmt.Errorf("works update filterable: %w", err)
	}
	if _, err := idx.UpdateSortableAttributesWithContext(ctx, &[]string{"year", "popularity", "edition_count"}); err != nil {
		return fmt.Errorf("works update sortable: %w", err)
	}
	if _, err := idx.UpdateRankingRulesWithContext(ctx,
		&[]string{"words", "typo", "proximity", "attribute", "sort", "exactness", "popularity:desc"}); err != nil {
		return fmt.Errorf("works update ranking: %w", err)
	}
	if _, err := idx.UpdatePaginationWithContext(ctx,
		&meilisearch.Pagination{MaxTotalHits: MeiliMaxTotalHits}); err != nil {
		return fmt.Errorf("works update pagination: %w", err)
	}
	if _, err := idx.UpdateFacetingWithContext(ctx,
		&meilisearch.Faceting{MaxValuesPerFacet: MeiliMaxValuesPerFacet}); err != nil {
		return fmt.Errorf("works update faceting: %w", err)
	}
	return nil
}

// configureIndex применяет настройки к индексу books идемпотентно.
// Запускается один раз в начале каждого импорта.
func configureIndex(ctx context.Context, m meilisearch.ServiceManager) error {
	idx := m.Index(booksIndex)

	// Создание индекса с указанием primary key. Если уже есть — это no-op.
	if _, err := m.CreateIndexWithContext(ctx, &meilisearch.IndexConfig{
		Uid:        booksIndex,
		PrimaryKey: "id",
	}); err != nil {
		// 'index_already_exists' — это нормально для повторных запусков.
		if !isMeiliAlreadyExists(err) {
			return fmt.Errorf("create index: %w", err)
		}
	}

	// Настройки настраиваем по отдельности, а не через UpdateSettings одной
	// большой структурой: SDK добавляет в Settings поля новее серверной
	// версии (например disableOnNumbers в TypoTolerance), и сервер ругается
	// на неизвестные поля. Поэтому шлём только то, что нам нужно.
	if _, err := idx.UpdateSearchableAttributesWithContext(ctx, &[]string{"title", "authors", "series"}); err != nil {
		return fmt.Errorf("update searchable: %w", err)
	}
	filterable := []any{"genres", "lang", "year", "series_id", "author_ids", "work_id"}
	if _, err := idx.UpdateFilterableAttributesWithContext(ctx, &filterable); err != nil {
		return fmt.Errorf("update filterable: %w", err)
	}
	if _, err := idx.UpdateSortableAttributesWithContext(ctx, &[]string{"year", "popularity"}); err != nil {
		return fmt.Errorf("update sortable: %w", err)
	}
	// distinctAttribute = work_id: поиск/список схлопываются в ОДНО издание на
	// логическую книгу (works). Представитель — самое релевантное издание;
	// edition_count/языки догидрируются из PG на страницу выдачи.
	if _, err := idx.UpdateDistinctAttributeWithContext(ctx, "work_id"); err != nil {
		return fmt.Errorf("update distinct: %w", err)
	}
	if _, err := idx.UpdateRankingRulesWithContext(ctx,
		&[]string{"words", "typo", "proximity", "attribute", "sort", "exactness", "popularity:desc"}); err != nil {
		return fmt.Errorf("update ranking: %w", err)
	}
	if _, err := idx.UpdatePaginationWithContext(ctx,
		&meilisearch.Pagination{MaxTotalHits: MeiliMaxTotalHits}); err != nil {
		return fmt.Errorf("update pagination: %w", err)
	}
	if _, err := idx.UpdateFacetingWithContext(ctx,
		&meilisearch.Faceting{MaxValuesPerFacet: MeiliMaxValuesPerFacet}); err != nil {
		return fmt.Errorf("update faceting: %w", err)
	}
	return nil
}

// indexer аккумулирует документы в батч и flush-ит их в Meilisearch.
// Не потокобезопасен — используется одним горутином в Importer.
type indexer struct {
	mgr   meilisearch.ServiceManager
	batch []bookDoc
	limit int
}

func newIndexer(m meilisearch.ServiceManager, batchSize int) *indexer {
	if batchSize <= 0 {
		batchSize = 1000
	}
	return &indexer{mgr: m, limit: batchSize, batch: make([]bookDoc, 0, batchSize)}
}

func (i *indexer) add(doc bookDoc) error {
	i.batch = append(i.batch, doc)
	if len(i.batch) >= i.limit {
		return i.flush(context.Background())
	}
	return nil
}

// flush отправляет накопленные документы в Meili (upsert по id) и
// дожидается завершения соответствующей задачи Meili.
// Без ожидания таски выполняются асинхронно — это создаёт гонку с
// последующим поиском (например, в тестах сразу после flush).
func (i *indexer) flush(ctx context.Context) error {
	if len(i.batch) == 0 {
		return nil
	}
	idx := i.mgr.Index(booksIndex)
	pk := "id"
	task, err := idx.AddDocumentsWithContext(ctx, i.batch, &meilisearch.DocumentOptions{PrimaryKey: &pk})
	if err != nil {
		return fmt.Errorf("meili add documents: %w", err)
	}
	final, err := i.mgr.WaitForTaskWithContext(ctx, task.TaskUID, 0)
	if err != nil {
		return fmt.Errorf("wait meili task %d: %w", task.TaskUID, err)
	}
	if final.Status != meilisearch.TaskStatusSucceeded {
		return fmt.Errorf("meili task %d ended with status %s: %v", final.UID, final.Status, final.Error)
	}
	i.batch = i.batch[:0]
	return nil
}

// isMeiliAlreadyExists возвращает true если ошибка Meili — "index_already_exists".
func isMeiliAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	mErr := &meilisearch.Error{}
	if as := errorsAs(err, mErr); as && mErr.MeilisearchApiError.Code == "index_already_exists" {
		return true
	}
	// fallback по строке (на случай иной упаковки ошибки)
	return contains(err.Error(), "index_already_exists")
}

// isMeiliIndexNotFound возвращает true если ошибка Meili — "index_not_found"
// (индекса ещё нет — например, чистая установка или пустой Meili после
// восстановления базы).
func isMeiliIndexNotFound(err error) bool {
	if err == nil {
		return false
	}
	mErr := &meilisearch.Error{}
	if as := errorsAs(err, mErr); as && mErr.MeilisearchApiError.Code == "index_not_found" {
		return true
	}
	return contains(err.Error(), "index_not_found")
}

// маленькие inline-обёртки чтобы не тащить fmt/strings в caller.
func contains(s, sub string) bool {
	return len(sub) <= len(s) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// errorsAs — обёртка вокруг errors.As, чтобы не тащить пакет в файл,
// который и так обильно нагружен. Чисто для краткости.
func errorsAs(err error, target any) bool {
	type asErr interface{ As(target any) bool }
	if x, ok := err.(asErr); ok {
		return x.As(target)
	}
	return false
}
