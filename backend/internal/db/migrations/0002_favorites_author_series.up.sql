-- Расширение модели "Избранное" на авторов и серии.
--
-- Контекст: уже есть таблица favorites(user_id, book_id) — избранные книги.
-- Сейчас добавляем симметричные таблицы для:
--   * авторов — "следить" за автором, видеть его новые книги в личной ленте;
--   * серий   — "следить" за серией, см. ещё непрочитанные тома.
--
-- Решение — две отдельные таблицы, а не одна полиморфная:
--   1) Foreign-key на разные родительские таблицы — типобезопасно, ON DELETE
--      CASCADE автоматически чистит при удалении автора/серии.
--   2) Можно индексировать каждую отдельно без частичных индексов и без
--      условных констрейнтов; планировщик PG проще читает план.
--   3) UI и re-ranking всё равно знают типы заранее (нужны разные сигналы),
--      так что полиморфизм ничего бы не упростил.

CREATE TABLE favorite_authors (
    user_id   BIGINT      NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    author_id BIGINT      NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
    added_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, author_id)
);

CREATE TABLE favorite_series (
    user_id   BIGINT      NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    series_id BIGINT      NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    added_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, series_id)
);
