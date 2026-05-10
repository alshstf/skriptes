package importer

import "time"

// bookRow — DTO для INSERT в books. Содержит уже разрешённые id внешних ключей
// и нормализованные значения. seriesID = nil если книга не в серии.
type bookRow struct {
	collectionID int64
	archiveID    int64

	libID    string
	fileName string
	ext      string
	size     int64

	title           string
	normalizedTitle string
	seriesID        *int64 // nil если нет
	serNo           *int   // nil если нет
	lang            string
	dateAdded       *time.Time
	rating          *int // nil если 0/нет
	keywords        string
	deleted         bool
}

// Stats — агрегированные результаты одного запуска импортёра.
type Stats struct {
	Records       int           // всего записей в INPX
	Books         int           // строк books затронуто
	BooksInserted int           // из них вставлено впервые
	BooksUpdated  int           // обновлено существующих
	BooksDeleted  int           // записей с DEL=1 (хранятся в PG, не индексируются в Meili)
	BooksIndexed  int           // отправлено в Meilisearch
	Authors       int           // уникальных авторов в этом импорте
	Series        int           // уникальных серий
	Genres        int           // уникальных жанров
	Errors        int           // записей с ошибкой (пропущены)
	Skipped       bool          // импорт пропущен (хэш не изменился)
	Duration      time.Duration // полное время Run
}
