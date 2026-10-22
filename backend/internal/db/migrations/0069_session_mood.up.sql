-- Настроение сессии (план recsys/03, задача 3.4): лог выбора чипа на Главной
-- (лёгкое · умное · мрачное · смешное · короткое). Само настроение живёт в
-- браузере и уходит параметром mood= в GET /api/me/recommendations; лог —
-- чтобы потом видеть, что человек выбирает по вечерам (приёмка 3.7).
-- Список значений — recsys.AllMoods.
CREATE TABLE session_mood (
    id      BIGSERIAL   PRIMARY KEY,
    user_id BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mood    TEXT        NOT NULL CHECK (mood IN ('light', 'smart', 'dark', 'funny', 'short')),
    at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX session_mood_user_at ON session_mood (user_id, at);
