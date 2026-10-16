-- «Не дубликат» (#527): админ отклонил пару возможных дублей авторов (#308) —
-- пара запоминается и больше не показывается ни в подсказке на карточке автора,
-- ни на странице «Дубли авторов». Пара упорядочена (author_a < author_b); автор
-- удалён — пара уходит каскадом.
CREATE TABLE author_duplicate_rejections (
    author_a    BIGINT NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
    author_b    BIGINT NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
    rejected_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    rejected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (author_a, author_b),
    CHECK (author_a < author_b)
);
CREATE INDEX author_duplicate_rejections_b ON author_duplicate_rejections (author_b);
