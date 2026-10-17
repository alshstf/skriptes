-- Год написания из библиографии автора на Фантлабе (#533, metadata/year_fantlab.go):
-- учёт опроса по автору — когда спрашивали, нашли ли страницу (via: wikidata — по
-- P7433, search — поиском по имени с гейтом), сколько работ сопоставлено и сколько
-- получили год. Кандидаты фазы выбираются по сроку (грабля 21).
CREATE TABLE author_fantlab_lookups (
    author_id  BIGINT PRIMARY KEY REFERENCES authors(id) ON DELETE CASCADE,
    outcome    TEXT NOT NULL CHECK (outcome IN ('found', 'not_found', 'error')),
    fantlab_id BIGINT,
    via        TEXT,
    matched    INT NOT NULL DEFAULT 0,
    dated      INT NOT NULL DEFAULT 0,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
