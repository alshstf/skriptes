-- Био-таймлайн автора (план v3): PG — кэш встроенного датасета internal/bio
-- (источник правды — файлы в репо, сев на старте при смене версии) плюс
-- админское «скрыть веху», которое переживает пересев и регенерацию (ключ
-- вехи стабилен: <type>-<yyyy>[-n]). Связь с authors — только через
-- authors.ext_ids->>'wd_qid' на чтении.
CREATE TABLE author_bio (
    qid        TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    data       JSONB NOT NULL,          -- bio.Author без evidence
    version    TEXT NOT NULL,           -- версия датасета, из которой посеяно
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE author_event_hides (
    qid       TEXT NOT NULL,
    key       TEXT NOT NULL,
    hidden_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    hidden_by BIGINT,
    PRIMARY KEY (qid, key)
);
