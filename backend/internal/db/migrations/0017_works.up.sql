-- Логическая «книга» (Work) над физическими «изданиями» (= строки books,
-- каждая = один fb2-файл). Переход к FRBR-модели: одна книга может иметь
-- несколько изданий (разные переводы/обложки/годы издания), у которых ОБЩИЕ
-- год написания, серия, экранизации, авторы, жанры.
--
-- Аддитивно: books НЕ переименовываем и не ломаем (Meili PK = books.id,
-- reads/favorites FK на books.id остаются). Добавляем works + books.work_id.
-- Group-by-work в read-path'ах подключается ПОЗЖЕ (отдельная фаза) — пока
-- work_id проставлен, но не читается, приложение работает как раньше.
--
-- Инвариант после миграции: у КАЖДОЙ книги есть work_id, у КАЖДОГО work ≥1
-- издание (бэкфилл ниже делает каждую существующую книгу singleton-работой).
CREATE TABLE works (
    id                  BIGSERIAL PRIMARY KEY,
    -- Каноническая идентичность Work (выбирается при группировке; пока = поля
    -- единственного издания).
    title               TEXT   NOT NULL,
    normalized_title    CITEXT NOT NULL,
    primary_author_id   BIGINT REFERENCES authors(id) ON DELETE SET NULL,
    -- Work-level факты (живут на издании тоже, на транзите; read-path'ы
    -- переключатся читать отсюда отдельной фазой).
    written_year        SMALLINT,
    written_year_source TEXT,
    series_id           BIGINT REFERENCES series(id) ON DELETE SET NULL,
    ser_no              INTEGER,
    -- Внешняя идентичность работы, резолвится джобой группировки (OL Work /
    -- Wikidata QID) — {ol_work, wd_qid}.
    ext_ids             JSONB  NOT NULL DEFAULT '{}'::jsonb,
    -- Сколько изданий под этой работой (денормализация для UI/статистики).
    edition_count       INTEGER NOT NULL DEFAULT 1,
    -- Экранизации — свойство работы, не издания (на транзите дублируется маркер
    -- с books; book_adaptations пока остаётся book_id-keyed).
    adaptations_fetched_at TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX works_primary_author       ON works(primary_author_id) WHERE primary_author_id IS NOT NULL;
CREATE INDEX works_series_id            ON works(series_id) WHERE series_id IS NOT NULL;
CREATE INDEX works_normalized_title_trgm ON works USING gin ((normalized_title::text) gin_trgm_ops);

ALTER TABLE books ADD COLUMN work_id BIGINT REFERENCES works(id) ON DELETE SET NULL;
CREATE INDEX books_work_id ON books(work_id);

-- Бэкфилл: каждая существующая книга → свой singleton Work, с поднятыми наверх
-- Work-level полями. Маппинг id↔id через временную колонку seed_book_id
-- (одним INSERT…SELECT нельзя сопоставить новые works.id с их books.id).
ALTER TABLE works ADD COLUMN seed_book_id BIGINT;

INSERT INTO works (
    title, normalized_title, primary_author_id,
    written_year, written_year_source, series_id, ser_no, seed_book_id
)
SELECT b.title, b.normalized_title, pa.author_id,
       b.written_year, b.written_year_source, b.series_id, b.ser_no, b.id
FROM books b
LEFT JOIN LATERAL (
    SELECT ba.author_id
    FROM book_authors ba
    WHERE ba.book_id = b.id
    ORDER BY ba.position
    LIMIT 1
) pa ON true;

UPDATE books b SET work_id = w.id
FROM works w
WHERE w.seed_book_id = b.id;

ALTER TABLE works DROP COLUMN seed_book_id;
