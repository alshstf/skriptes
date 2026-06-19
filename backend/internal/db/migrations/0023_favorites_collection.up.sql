-- Унификация книжного ★-избранного с полками: «Избранное» становится служебной
-- коллекцией (kind='favorites'). Книжное favorites переезжает в членство этой
-- коллекции, старая таблица favorites удаляется. ★ на книге = шорткат к этой полке.
-- (favorite_authors / favorite_series — это «подписки», НЕ трогаем.)

-- 1. Тип полки: 'user' (обычная) | 'favorites' (служебная «Избранное», одна на юзера).
ALTER TABLE user_collections ADD COLUMN kind TEXT NOT NULL DEFAULT 'user';

-- 2. Максимум одна favorites-коллекция на пользователя.
CREATE UNIQUE INDEX user_collections_one_fav ON user_collections (user_id) WHERE kind = 'favorites';

-- 3. Создать «Избранное» тем, у кого есть книжные favorites.
INSERT INTO user_collections (user_id, name, kind)
SELECT DISTINCT f.user_id, 'Избранное', 'favorites'
FROM favorites f;

-- 4. Перенести членство, сохраняя added_at.
INSERT INTO user_collection_books (collection_id, book_id, added_at)
SELECT fav.id, f.book_id, f.added_at
FROM favorites f
JOIN user_collections fav ON fav.user_id = f.user_id AND fav.kind = 'favorites';

-- 5. Старая таблица больше не нужна — книжное избранное теперь в коллекции.
DROP TABLE favorites;
