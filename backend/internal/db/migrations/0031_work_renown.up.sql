-- Внешние счётчики «известности» работы (сигналы интегральной популярности,
-- см. importer/popularity.go::computeWorkPopularity). Живут на works — известность
-- свойство работы, не издания. Заполняются фоновым воркером
-- metadata/renown_backfill.go (opt-in из админки «Фоновые операции»):
--   fantlab_marks    — число оценок произведения на fantlab.ru (markcount);
--   ol_ratings_count — число оценок на Open Library (ratings_count);
--   ol_want_count    — счётчик полки want-to-read на Open Library.
ALTER TABLE works
    ADD COLUMN fantlab_marks    INTEGER,
    ADD COLUMN ol_ratings_count INTEGER,
    ADD COLUMN ol_want_count    INTEGER;

-- work_renown_lookups — per-source учёт попыток (зеркало
-- book_external_rating_lookups, но work-level):
--   outcome 'found'     — источник вернул счётчики (колонки выше заполнены);
--           'not_found' — источник работу не нашёл / счётчиков нет;
--           'error'     — сетевая/HTTP ошибка (ретраить раньше not_found).
-- TTL перепроверки задаётся настройками renown_enrichment; found тоже
-- освежается (известность растёт), но с большим TTL. FK ON DELETE CASCADE —
-- чистится с работой (GC опустевших работ группировкой).
CREATE TABLE work_renown_lookups (
    work_id    BIGINT      NOT NULL REFERENCES works(id) ON DELETE CASCADE,
    source     TEXT        NOT NULL,
    outcome    TEXT        NOT NULL,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (work_id, source)
);
