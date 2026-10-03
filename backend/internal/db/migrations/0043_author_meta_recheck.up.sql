-- Журнал перепроверки биографий и фото авторов (#280): что было и что стало у
-- каждого изменённого автора — для отчёта и отката. Подтверждённые без
-- изменений сюда не пишутся.
CREATE TABLE author_meta_recheck (
    id         BIGSERIAL   PRIMARY KEY,
    author_id  BIGINT      NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
    field      TEXT        NOT NULL CHECK (field IN ('bio', 'photo')),
    action     TEXT        NOT NULL CHECK (action IN ('replaced', 'added', 'cleared')),
    old_value  TEXT,
    new_value  TEXT,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX author_meta_recheck_author ON author_meta_recheck (author_id);
