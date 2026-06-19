-- Личные полки (коллекции) пользователя — раздел «Жанры» (/genres).
--
-- Полка — произвольный именованный список книг, который пользователь
-- собирает вручную (в отличие от серий/жанров, которые приходят из метаданных).
--
-- ⚠️ Имя таблицы — user_collections (НЕ collections): таблица `collections`
-- уже занята в 0001_init под коллекции импорта (один .inpx-файл = одна
-- collection). Это РАЗНЫЕ сущности; чтобы не путать и не ломать импорт —
-- отдельное пространство имён `user_*` (как user_favorite_genres в 0020).
--
-- Две таблицы:
--   * user_collections       — сама полка (имя + владелец);
--   * user_collection_books   — членство книги в полке (M:N книга↔полка).
--
-- ON DELETE CASCADE: удаление пользователя сносит его полки, удаление полки
-- сносит её членства, удаление книги убирает её из полок. PRIMARY KEY
-- (collection_id, book_id) даёт идемпотентный «добавить в полку»
-- (INSERT ... ON CONFLICT DO NOTHING).
CREATE TABLE user_collections (
    id         BIGSERIAL   PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Список полок пользователя — частый запрос (страница «Жанры»); индекс по
-- владельцу убирает seq-scan по всей таблице.
CREATE INDEX user_collections_user ON user_collections(user_id);

CREATE TABLE user_collection_books (
    collection_id BIGINT      NOT NULL REFERENCES user_collections(id) ON DELETE CASCADE,
    book_id       BIGINT      NOT NULL REFERENCES books(id)            ON DELETE CASCADE,
    added_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (collection_id, book_id)
);

-- Индекс по book_id: PK ведёт по collection_id, поэтому удаление/замена книги
-- (importer DEL=1 → FK ON DELETE CASCADE) и будущий запрос «в каких полках эта
-- книга» иначе сканировали бы таблицу целиком. На большой коллекции (462k книг)
-- каждое удаление книги без него — seq-scan по членствам.
CREATE INDEX user_collection_books_book ON user_collection_books(book_id);
