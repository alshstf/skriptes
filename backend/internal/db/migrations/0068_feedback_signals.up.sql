-- Явная обратная связь (план recsys/03, задачи 3.1–3.3).

-- 3.1 Причины низкой оценки: закрытый список (recsys.RatingReason), несколько на
-- работу; живут вместе с оценкой — снятие оценки или новая ≥ 4 их удаляет
-- (history.SetRating/RemoveRating). Слияние работ переносит (reassignWorkUserData).
CREATE TABLE rating_reasons (
    user_id BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    work_id BIGINT      NOT NULL REFERENCES works(id) ON DELETE CASCADE,
    reason  TEXT        NOT NULL CHECK (reason IN ('too_long', 'gloomy', 'language', 'not_mine', 'unfinished', 'dated', 'boring')),
    at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, work_id, reason)
);

-- 3.2 «Не предлагать» автора или цикл: только рекомендации и лента подписок,
-- каталог и поиск не меняются (это не скрытие контента на инстансе). Слияние
-- авторов переносит блок (metadata.author_merge), сборка межавторской серии —
-- как подписки (importer.moveSeriesSubscriptions).
CREATE TABLE blocked_authors (
    user_id   BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    author_id BIGINT      NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
    added_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, author_id)
);
CREATE INDEX blocked_authors_author ON blocked_authors (author_id);

CREATE TABLE blocked_series (
    user_id   BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    series_id BIGINT      NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    added_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, series_id)
);
CREATE INDEX blocked_series_series ON blocked_series (series_id);

-- 3.3 Служебная полка «Хочу прочитать» (user_collections.kind = 'to_read'), одна
-- на пользователя, как «Избранное» (0023). Закладка на карточке книги —
-- членство в ней; «Избранное» — «люблю», «Хочу прочитать» — «ещё не читал».
CREATE UNIQUE INDEX user_collections_one_to_read ON user_collections (user_id) WHERE kind = 'to_read';
