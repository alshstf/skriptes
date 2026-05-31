-- book_year_lookups — per-source учёт попыток дозаполнить written_year из
-- внешних источников (OpenLibrary, Wikidata). Нужен, чтобы НЕ долбить один
-- источник повторно: «спросили OpenLibrary час назад — не дёргаем; Wikidata
-- ещё не спрашивали — спросим». Одна строка на пару (книга, источник).
--
--   outcome 'found'     — источник вернул год (year заполнен);
--           'not_found' — источник книгу/год не нашёл;
--           'error'     — сетевая/HTTP ошибка (ретраить раньше not_found).
--
-- TTL перепроверки задаётся в настройках year_enrichment (not_found ~90 дней,
-- error ~1 день). FK ON DELETE CASCADE — при удалении книги чистится само.
CREATE TABLE book_year_lookups (
    book_id    BIGINT      NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    source     TEXT        NOT NULL,
    outcome    TEXT        NOT NULL,
    year       SMALLINT,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (book_id, source)
);
