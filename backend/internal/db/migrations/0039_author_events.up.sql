-- 0039: события биографии автора (био-таймлайн ⟷ книги, план
-- cryptic-roaming-turing / «Фаза 5 v2»).
--
-- Источники: 'wikidata' (структурный скелет, CC0) и 'wikipedia' (вехи из
-- текста био-секции, CC BY-SA — атрибуция через url). 'manual' — follow-up.
-- ext_key даёт идемпотентный upsert (зеркало book_adaptations):
--   wikidata:  '{prop}:{QID-значения|—}:{год}' (напр. 'P26:Q463650:1867')
--   wikipedia: '{lang}:{sha256[:16] нормализованного предложения}'
-- Периоды (каторга, эмиграция, браки) — first-class: year_to NOT NULL.
-- Двойные даты (ст./нов. стиль): нормализованный НОВЫЙ стиль в
-- year_from/date_from, сырая формулировка живёт в quote.
-- hidden — курирование админом («скрыть событие»), ПЕРЕЖИВАЕТ refetch
-- (upsert обязан сохранять hidden).
CREATE TABLE author_events (
    id             BIGSERIAL PRIMARY KEY,
    author_id      BIGINT NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
    source         TEXT NOT NULL,
    ext_key        TEXT NOT NULL,
    event_type     TEXT NOT NULL,
    year_from      SMALLINT NOT NULL,
    year_to        SMALLINT,
    date_from      DATE,
    date_precision TEXT NOT NULL DEFAULT 'year',
    title          TEXT NOT NULL,
    quote          TEXT,
    place          TEXT,
    url            TEXT,
    weight         SMALLINT NOT NULL DEFAULT 0,
    hidden         BOOLEAN NOT NULL DEFAULT false,
    llm_polished   BOOLEAN NOT NULL DEFAULT false,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (author_id, source, ext_key)
);

CREATE INDEX author_events_author_year ON author_events(author_id, year_from);

-- Single-shot маркер «события пробовали тянуть» (зеркало
-- adaptations_fetched_at; транзиент источников маркер НЕ ставит).
ALTER TABLE authors ADD COLUMN events_fetched_at TIMESTAMPTZ;
