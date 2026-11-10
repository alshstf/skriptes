-- Учёт вызовов языковой модели (ИИ-функции, только при заданном
-- SKRIPTES_LLM_API_KEY): по строке на запрос к модели. По нему считаются
-- суточная квота пользователя (строки с counted) и месячный потолок расходов
-- инстанса (сумма cost_microusd), а админ видит расход по функциям.
-- Текстов запросов и ответов здесь нет.
CREATE TABLE llm_usage (
    id                 BIGSERIAL   PRIMARY KEY,
    at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id            BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    feature            TEXT        NOT NULL,               -- explain, recap, translate, …
    model              TEXT        NOT NULL,
    stage              SMALLINT    NOT NULL DEFAULT 0,     -- ступень контекста (читалка)
    counted            BOOLEAN     NOT NULL DEFAULT false, -- первое обращение действия пользователя: идёт в квоту
    outcome            TEXT        NOT NULL,               -- ok, refusal, truncated, error
    input_tokens       INTEGER     NOT NULL DEFAULT 0,
    output_tokens      INTEGER     NOT NULL DEFAULT 0,
    cache_read_tokens  INTEGER     NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER     NOT NULL DEFAULT 0,
    cost_microusd      BIGINT      NOT NULL DEFAULT 0
);
CREATE INDEX llm_usage_at ON llm_usage (at);
CREATE INDEX llm_usage_user_at ON llm_usage (user_id, at) WHERE counted;
