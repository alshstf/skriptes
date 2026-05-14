// Package metadata — фоновое обогащение карточек книг (и авторов в
// будущих PR) данными из внешних источников плюс из самих fb2-архивов.
//
// Архитектура:
//
//	Enricher (orchestrator)
//	  └── CoverProvider (interface)
//	       ├── Fb2Provider   — читает coverpage из book.zip:fb2 (всегда есть, ~99% hit)
//	       ├── OpenLibrary   — search.json → covers.openlibrary.org
//	       └── GoogleBooks   — volumes?q= → imageLinks.thumbnail
//
// Триггер: HTTP-handler GET /api/books/{id} вызывает Enricher.EnsureCover
// в фоновой goroutine, если у книги нет cover_path. Первый рендер
// карточки идёт без обложки; следующий открытие — уже с ней.
//
// Кэш HTTP-ответов в metadata_cache (TTL 30 дней). Бинарь обложки
// сохраняется в /cache/covers/{sha256}.jpg, путь пишется в books.cover_path.
package metadata
