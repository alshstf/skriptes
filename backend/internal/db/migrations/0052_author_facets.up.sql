-- Фасеты авторов (#389, A3): чем автор «отмечен» для счётчиков фильтров /authors —
-- жанры (genre), категории жанров (gcat), языки изданий (lang), языки оригинала
-- (src), экранизации (adapt). Только книги, которые входят в статистику автора
-- (живые, не сборники) — как фильтры списка. Пересчитывает
-- catalog.RecomputeAuthorFacets вместе с хранимыми агрегатами авторов.
CREATE TABLE author_facets (
    author_id BIGINT NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
    kind      TEXT   NOT NULL,
    value     TEXT   NOT NULL,
    PRIMARY KEY (author_id, kind, value)
);

CREATE INDEX author_facets_kind_value_idx ON author_facets (kind, value);
