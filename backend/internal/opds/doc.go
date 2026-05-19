// Package opds реализует каталог OPDS 1.2 (https://specs.opds.io/opds-1.2)
// поверх наших books/catalog/converter сервисов.
//
// Зачем: e-reader приложения (KOReader, Moon+Reader, FBReader, Marvin)
// не умеют наш React-UI, но умеют OPDS — Atom XML с навигацией и
// acquisition-ссылками. Открыв в KOReader URL https://host/opds/, юзер
// получает листалку библиотеки и качает книгу в формате epub/kfx через
// конвертер.
//
// Архитектура:
//
//	┌──────────────────────────────┐
//	│ HTTP-handler (/opds/*)       │
//	│  basic-auth middleware       │
//	└────────────┬─────────────────┘
//	             │
//	             v
//	┌──────────────────────────────┐
//	│ opds.Feed / opds.Entry       │  ← Atom XML структуры
//	│ render через encoding/xml    │
//	└────────────┬─────────────────┘
//	             │
//	             v
//	┌──────────────────────────────┐
//	│ books.Service / catalog.Svc  │  ← reuse существующих
//	│ converter.Convert (acquisition)│
//	└──────────────────────────────┘
//
// Аутентификация: HTTP Basic. E-reader'ы не поддерживают cookie/CSRF
// и шлют Authorization: Basic header'ом на каждый запрос. Сессии
// бессмысленны — валидируем credentials каждым запросом через
// auth.Service.ValidateCredentials.
//
// URL-схема:
//
//	GET /opds/                       — root navigation feed
//	GET /opds/recent                 — acquisition: новинки (date_added desc)
//	GET /opds/authors                — navigation: алфавитный список авторов
//	GET /opds/authors/{id}           — acquisition: книги автора
//	GET /opds/series                 — navigation: серии по алфавиту
//	GET /opds/series/{id}            — acquisition: книги серии
//	GET /opds/genres                 — navigation: жанры
//	GET /opds/genres/{id}            — acquisition: книги жанра
//	GET /opds/search?q=…             — acquisition: результаты поиска
//	GET /opds/opensearch.xml         — OpenSearch description (для search-link)
//	GET /opds/covers/{name}          — отдача обложки (тот же файл что /api/covers)
//	GET /opds/books/{id}/download    — acquisition link (delegate в существующий converter)
//
// Пагинация: ?page=N (1-indexed) + atom-ссылки rel="next"/"prev"/"first"/"last".
// На каждой странице — 50 элементов по дефолту, кастомизация через ?n=… до 200.
package opds
