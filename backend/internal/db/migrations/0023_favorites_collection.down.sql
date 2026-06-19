-- Откат: вернуть таблицу favorites и залить её из favorites-коллекции.
CREATE TABLE favorites (
    user_id  BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id  BIGINT      NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, book_id)
);

INSERT INTO favorites (user_id, book_id, added_at)
SELECT c.user_id, cb.book_id, cb.added_at
FROM user_collections c
JOIN user_collection_books cb ON cb.collection_id = c.id
WHERE c.kind = 'favorites'
ON CONFLICT DO NOTHING;

-- Удалить favorites-коллекции (членство уйдёт каскадом) + индекс + колонку.
DELETE FROM user_collections WHERE kind = 'favorites';
DROP INDEX IF EXISTS user_collections_one_fav;
ALTER TABLE user_collections DROP COLUMN kind;
