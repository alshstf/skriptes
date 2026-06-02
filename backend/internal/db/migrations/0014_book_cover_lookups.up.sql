-- book_cover_lookups — per-source учёт попыток дозаполнить cover_path из
-- внешних источников (OpenLibrary, Google Books). Зеркало book_year_lookups:
-- нужен, чтобы НЕ долбить один источник повторно по книге, у которой обложки
-- там нет. Одна строка на пару (книга, источник).
--
--   outcome 'found'     — источник вернул обложку (cover_path заполнен);
--           'not_found' — источник книгу/обложку не нашёл;
--           'error'     — сетевая/HTTP/IO ошибка (ретраить раньше not_found).
--
-- TTL перепроверки задаётся в настройках cover_enrichment (not_found ~90 дней,
-- error ~1 день). FK ON DELETE CASCADE — при удалении книги чистится само.
CREATE TABLE book_cover_lookups (
    book_id    BIGINT      NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    source     TEXT        NOT NULL,
    outcome    TEXT        NOT NULL,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (book_id, source)
);
