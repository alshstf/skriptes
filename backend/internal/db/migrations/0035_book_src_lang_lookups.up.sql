-- book_src_lang_lookups — per-source учёт попыток дозаполнить src_lang (язык
-- оригинала) из внешних источников. Зеркало book_year_lookups (0013): одна
-- строка на пару (книга, источник), чтобы не долбить источник повторно.
--
-- v1 источник один — Wikidata (P407 «язык произведения» с precision-гейтами:
-- ровно один ISO-код и он ≠ языку издания). OpenLibrary НЕ используется: у него
-- нет поля «язык оригинала», а languages работы — union языков ВСЕХ изданий
-- (у «Войны и мира» — 16 языков), для оригинала это гадание.
--
--   outcome 'found'     — источник дал язык оригинала (src_lang заполнен);
--           'not_found' — книга не сопоставлена / P407 нет / гейты отсекли;
--           'error'     — сетевая/HTTP ошибка (ретраить раньше not_found).
--
-- TTL перепроверки — в настройках src_lang_enrichment (not_found ~90 дней,
-- error ~1 день). FK ON DELETE CASCADE — при удалении книги чистится само.
CREATE TABLE book_src_lang_lookups (
    book_id    BIGINT      NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    source     TEXT        NOT NULL,
    outcome    TEXT        NOT NULL,
    src_lang   TEXT,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (book_id, source)
);
