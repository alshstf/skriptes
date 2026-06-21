-- Внешний рейтинг из сети (Google Books / OpenLibrary) — ОТДЕЛЬНО от books.rating
-- (LIBRATE из INPX, рейтинг донорской библиотеки librusec/flibusta). На UI оба
-- объединяются в единый «Внешний рейтинг» с атрибуцией источника: приоритет
-- показа — LIBRATE → web. Шкала 1–5 (как у LIBRATE и оценок читателей).
-- Заполняется фоновым воркером обогащения (отдельный PR); тут только поля.
ALTER TABLE books
    ADD COLUMN external_rating        REAL,
    ADD COLUMN external_rating_source TEXT,   -- 'google_books' | 'openlibrary'
    ADD COLUMN external_rating_count  INTEGER; -- число голосов у источника
