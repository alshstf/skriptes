-- Премии (#389, A2): лауреаты премий из белого списка (awards.Catalog) — с
-- Фантлаба, по годам и номинациям. work_id / author_id — сопоставление с
-- каталогом (по названию и фамилии; NULL — книги или автора в каталоге нет).
CREATE TABLE award_wins (
    id          BIGSERIAL PRIMARY KEY,
    award       TEXT   NOT NULL,          -- ключ премии из белого списка
    year        INT    NOT NULL,          -- год вручения
    nomination  TEXT   NOT NULL DEFAULT '',
    nomination_order INT NOT NULL DEFAULT 0, -- порядок номинации внутри года (у источника)
    kind        TEXT   NOT NULL CHECK (kind IN ('work', 'author')),
    title       TEXT   NOT NULL DEFAULT '', -- название произведения у источника
    orig_title  TEXT   NOT NULL DEFAULT '', -- название в оригинале (для переводных)
    author      TEXT   NOT NULL DEFAULT '', -- автор у источника («Имя Фамилия»)
    source      TEXT   NOT NULL,
    source_ref  TEXT   NOT NULL,          -- id номинанта у источника
    source_link TEXT   NOT NULL DEFAULT '', -- страница произведения или автора у источника
    work_id     BIGINT REFERENCES works(id) ON DELETE SET NULL,
    author_id   BIGINT REFERENCES authors(id) ON DELETE SET NULL,
    UNIQUE (source, source_ref)
);

CREATE INDEX award_wins_award_year_idx ON award_wins (award, year);
CREATE INDEX award_wins_work_idx ON award_wins (work_id) WHERE work_id IS NOT NULL;
CREATE INDEX award_wins_author_idx ON award_wins (author_id) WHERE author_id IS NOT NULL;
