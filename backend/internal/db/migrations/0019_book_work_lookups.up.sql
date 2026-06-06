-- book_work_lookups — per-source учёт попыток зарезолвить ВНЕШНИЙ Work ID
-- издания (OpenLibrary Work / Wikidata QID) для группировки изданий в одну
-- логическую книгу (Tier-2). Зеркало book_year_lookups: одна строка на
-- (книга, источник), чтобы не долбить источник повторно (TTL по outcome).
--
--   outcome 'found'     — источник вернул work_key (work_key заполнен);
--           'not_found' — источник работу не нашёл / не прошёл гейт по автору;
--           'error'     — сетевая/HTTP ошибка (ретраить раньше not_found).
--
-- work_key — ключ работы во внешней системе ('OL12345W' / 'Q42'). Книги с
-- ОДИНАКОВЫМ (source, work_key) и тем же primary-автором сливаются в одну работу.
-- Индекс по (source, work_key) — быстрый поиск «кто ещё резолвится в эту работу».
CREATE TABLE book_work_lookups (
    book_id    BIGINT      NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    source     TEXT        NOT NULL,
    outcome    TEXT        NOT NULL,
    work_key   TEXT,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (book_id, source)
);
CREATE INDEX book_work_lookups_key ON book_work_lookups(source, work_key) WHERE work_key IS NOT NULL;

-- work_scanned_at — маркер «джоба группировки уже обработала эту книгу»
-- (аналог year_local_scanned_at). Кандидаты группировки — work_scanned_at IS
-- NULL. Подтверждённый singleton: work_scanned_at NOT NULL + edition_count=1.
ALTER TABLE books ADD COLUMN work_scanned_at TIMESTAMPTZ;
CREATE INDEX books_work_unscanned ON books(id) WHERE work_scanned_at IS NULL;
