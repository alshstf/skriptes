-- Лог поисковых запросов (план recsys/01, задача 1.4): что искал пользователь и
-- нашлось ли. Видит только сам пользователь (страница «Поиск» в профиле, удаление
-- строки и всей истории); админу — только агрегат «ищут и не находят» без
-- привязки к людям. Срок — SKRIPTES_SEARCH_LOG_DAYS (0 — не вести).
CREATE TABLE search_log (
    id              BIGSERIAL   PRIMARY KEY,
    user_id         BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    q               TEXT        NOT NULL,                     -- как ввёл пользователь (trim)
    norm            TEXT        NOT NULL,                     -- нормализованный (recsys.NormalizeQuery)
    filters         JSONB       NOT NULL DEFAULT '{}'::jsonb, -- непустые фильтры списка
    hits            INTEGER     NOT NULL,
    clicked_work_id BIGINT      REFERENCES works(id) ON DELETE SET NULL,
    at              TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX search_log_user_at ON search_log (user_id, at DESC);
CREATE INDEX search_log_misses ON search_log (norm) WHERE hits = 0;

-- «Появилось то, что вы искали»: ненайденный запрос совпал с работой, появившейся
-- при импорте (recsys.Matcher после импорта). Скрытие — feed_dismissals, открытие
-- карточки после matched_at убирает из ленты.
CREATE TABLE search_matches (
    user_id     BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    work_id     BIGINT      NOT NULL REFERENCES works(id) ON DELETE CASCADE,
    q           TEXT        NOT NULL,
    searched_at TIMESTAMPTZ NOT NULL,
    matched_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, work_id)
);
