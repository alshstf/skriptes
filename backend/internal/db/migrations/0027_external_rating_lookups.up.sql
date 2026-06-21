-- book_external_rating_lookups — per-source учёт попыток дозаполнить
-- books.external_rating из внешних источников (Google Books, OpenLibrary).
-- Зеркало book_cover_lookups: одна строка на пару (книга, источник), нужна,
-- чтобы НЕ долбить один источник повторно по книге, у которой рейтинга там нет.
--
--   outcome 'found'     — источник вернул рейтинг (external_rating заполнен);
--           'not_found' — источник книгу/рейтинг не нашёл;
--           'error'     — сетевая/HTTP ошибка (ретраить раньше not_found).
--
-- TTL перепроверки задаётся в настройках external_rating_enrichment
-- (not_found ~90 дней, error ~1 день). FK ON DELETE CASCADE — чистится с книгой.
CREATE TABLE book_external_rating_lookups (
    book_id    BIGINT      NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    source     TEXT        NOT NULL,
    outcome    TEXT        NOT NULL,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (book_id, source)
);
