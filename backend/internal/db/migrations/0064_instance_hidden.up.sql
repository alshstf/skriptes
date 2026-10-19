-- Скрытие авторов и работ на инстансе (админ, ~/projects/plans/skriptes/instance-hide-authors-works.md).
-- Явные скрытия — две таблицы; флаги instance_hidden у авторов/работ/книг — материализация для фильтров
-- (пересчитывает importer.SyncInstanceHidden после скрытия/возврата, импорта и перегруппировки):
--   работа скрыта — явно, или скрытый автор — её автор (workauthors.Core), или у неё нет издания без скрытого автора;
--   книга скрыта — среди её авторов скрытый, или скрыта её работа.
-- Строки пользователей (избранное, полки, история) не трогаются: возврат возвращает всё на место.
CREATE TABLE hidden_authors (
    author_id BIGINT PRIMARY KEY REFERENCES authors(id) ON DELETE CASCADE,
    hidden_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    hidden_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    note      TEXT NOT NULL DEFAULT ''
);

CREATE TABLE hidden_works (
    work_id   BIGINT PRIMARY KEY REFERENCES works(id) ON DELETE CASCADE,
    hidden_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    hidden_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    note      TEXT NOT NULL DEFAULT ''
);

-- Константный DEFAULT — без перезаписи таблиц (PG 11+).
ALTER TABLE authors ADD COLUMN instance_hidden BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE works   ADD COLUMN instance_hidden BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE books   ADD COLUMN instance_hidden BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX authors_instance_hidden ON authors(id) WHERE instance_hidden;
CREATE INDEX works_instance_hidden   ON works(id)   WHERE instance_hidden;
CREATE INDEX books_instance_hidden   ON books(id)   WHERE instance_hidden;
