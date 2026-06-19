-- Избранные жанры пользователя — раздел «Жанры» (/genres).
--
-- Симметрично favorite_authors/favorite_series (миграция 0002): отдельная
-- таблица с FK на users и genres, ON DELETE CASCADE сам чистит при удалении
-- пользователя или жанра. PRIMARY KEY (user_id, genre_id) — естественная
-- уникальность пары, повторный «добавить в избранное» делается через
-- INSERT ... ON CONFLICT DO NOTHING.
CREATE TABLE user_favorite_genres (
    user_id  BIGINT      NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    genre_id BIGINT      NOT NULL REFERENCES genres(id) ON DELETE CASCADE,
    added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, genre_id)
);
