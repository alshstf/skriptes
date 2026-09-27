-- Уточнение к имени автора (librusec с выпуска 2026-09 различает тёзок
-- квадратными скобками в фамилии: «Антоний [Блум]», «Гибсон [фантаст]»,
-- «Афанасьев [#17465]»). Имя в last/first/middle_name и normalized_name —
-- без скобок; содержимое скобок как есть — в name_note. Автор теперь
-- уникален по (имя, уточнение): тёзки — разные записи.
ALTER TABLE authors ADD COLUMN name_note TEXT;
ALTER TABLE authors DROP CONSTRAINT authors_normalized_name_key;
CREATE UNIQUE INDEX authors_name_note_key ON authors (normalized_name, lower(COALESCE(name_note, '')));

-- Журнал разделения прежних записей на тёзок при импорте (importer.planAuthorSplits):
-- какой вариант стал наследником прежней записи (её id, подписки, известность)
-- и почему. Для разбора SQL-запросами, страницы в админке нет.
CREATE TABLE author_splits (
    id            BIGSERIAL   PRIMARY KEY,
    base_name     TEXT        NOT NULL,  -- normalized_name без уточнения
    old_author_id BIGINT      REFERENCES authors(id) ON DELETE SET NULL,
    note          TEXT        NOT NULL,  -- '' — вариант без уточнения
    is_heir       BOOLEAN     NOT NULL,
    books_before  INTEGER     NOT NULL,  -- книг прежней записи, которые файл приписал варианту
    books_in_file INTEGER     NOT NULL,  -- всего книг варианта в файле
    reason        TEXT        NOT NULL,  -- почему наследник: majority | tie:plain | tie:activity | tie:books | tie:name
    inpx_file     TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX author_splits_base_name ON author_splits (base_name);
