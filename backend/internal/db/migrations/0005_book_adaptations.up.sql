-- Экранизации книг — фильмы/сериалы, основанные на конкретной книге.
--
-- Источник: Wikidata SPARQL (P144 "based on"). Поле kind различает
-- film / tv_series / miniseries — берётся из P31 (instance of) с
-- маппингом на узкий набор значений.
--
-- poster_path — относительное имя файла в /cache/covers/{sha256.ext}
-- (тот же кэш что и для обложек книг; имена content-addressable,
-- коллизий не бывает). Может быть пустым: P18 в Wikidata есть не у
-- каждого фильма; в этом случае фронт показывает плейсхолдер.
--
-- ext_url — каноническая ссылка наружу (обычно wikidata.org/wiki/QID
-- или imdb.com/title/...). Открываем в новой вкладке.
--
-- UNIQUE (book_id, provider, ext_id) — идемпотентность повторного
-- enrichment'а: один и тот же фильм не задвоится при ретраях.
--
-- books.adaptations_fetched_at — отдельная колонка от metadata_fetched_at
-- (которая используется для cover/annotation). Семантика разная:
-- "обогащение карточки книги" и "поиск экранизаций" имеют разные TTL и
-- разные источники, мешать в одну отметку нельзя.

CREATE TABLE book_adaptations (
    id          BIGSERIAL   PRIMARY KEY,
    book_id     BIGINT      NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    provider    TEXT        NOT NULL,                   -- 'wikidata' / 'tmdb'
    ext_id      TEXT        NOT NULL,                   -- QID / IMDB-ID / TMDB-ID
    title       TEXT        NOT NULL,
    year        SMALLINT,                               -- год выпуска
    director    TEXT,                                   -- ФИО, plain-text (запятая = несколько)
    kind        TEXT        NOT NULL DEFAULT 'film',    -- 'film' | 'tv_series' | 'miniseries' | 'other'
    poster_path TEXT,                                   -- имя файла в /cache/covers (sha256.ext) или NULL
    ext_url     TEXT,                                   -- каноническая ссылка наружу
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (book_id, provider, ext_id)
);

CREATE INDEX book_adaptations_book_id ON book_adaptations(book_id);

ALTER TABLE books ADD COLUMN adaptations_fetched_at TIMESTAMPTZ;
