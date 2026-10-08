-- Умные полки (#389): сохранённые фильтры каталога /books — полка сама
-- обновляется, её состав считает поиск. filters — объект фильтров /books
-- (q, genres, lang, src_lang, kind, year_from, year_to, series_id, author_id,
-- sort, unread), проверяется в collections/smart.go.
CREATE TABLE smart_shelves (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT   NOT NULL,
    filters    JSONB  NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX smart_shelves_user_idx ON smart_shelves (user_id, created_at);
