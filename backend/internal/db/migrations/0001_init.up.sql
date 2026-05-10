-- skriptes initial schema.
-- Один большой initial для MVP; дальше — пофичевые миграции.

CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS btree_gin;

-- ── Users / sessions ────────────────────────────────────────────
CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    email         CITEXT      NOT NULL UNIQUE,
    display_name  TEXT        NOT NULL,
    password_hash TEXT        NOT NULL,
    role          TEXT        NOT NULL CHECK (role IN ('admin', 'user')),
    kindle_email  CITEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    token      TEXT        PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ip         INET,
    user_agent TEXT
);
CREATE INDEX sessions_user_id    ON sessions(user_id);
CREATE INDEX sessions_expires_at ON sessions(expires_at);

-- ── Collections / archives ──────────────────────────────────────
CREATE TABLE collections (
    id               BIGSERIAL PRIMARY KEY,
    name             TEXT NOT NULL,
    inpx_filename    TEXT NOT NULL UNIQUE,           -- "librusec_local_fb2.inpx"
    last_inpx_hash   TEXT,                            -- sha256
    last_imported_at TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE archives (
    id            BIGSERIAL PRIMARY KEY,
    collection_id BIGINT  NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    filename      TEXT    NOT NULL,                   -- "fb2-749080-749080.zip"
    available     BOOLEAN NOT NULL DEFAULT true,      -- false для _lost
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (collection_id, filename)
);

-- ── Authors / series / genres ───────────────────────────────────
CREATE TABLE authors (
    id              BIGSERIAL PRIMARY KEY,
    last_name       TEXT   NOT NULL,
    first_name      TEXT   NOT NULL DEFAULT '',
    middle_name     TEXT   NOT NULL DEFAULT '',
    normalized_name CITEXT NOT NULL UNIQUE,           -- "иванов иван иванович"
    ext_ids         JSONB  NOT NULL DEFAULT '{}'::jsonb,  -- {ol_id, wikidata_qid, ...}
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX authors_last_name_trgm  ON authors USING gin (last_name gin_trgm_ops);
CREATE INDEX authors_normalized_trgm ON authors USING gin ((normalized_name::text) gin_trgm_ops);

CREATE TABLE series (
    id               BIGSERIAL PRIMARY KEY,
    title            TEXT   NOT NULL,
    normalized_title CITEXT NOT NULL,
    author_id        BIGINT REFERENCES authors(id) ON DELETE SET NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (normalized_title, author_id)
);
CREATE INDEX series_title_trgm ON series USING gin ((normalized_title::text) gin_trgm_ops);

CREATE TABLE genres (
    id        BIGSERIAL PRIMARY KEY,
    fb2_code  TEXT NOT NULL UNIQUE,                   -- "sf_action"
    name_ru   TEXT,
    name_en   TEXT,
    parent_id BIGINT REFERENCES genres(id) ON DELETE SET NULL
);

-- ── Books ───────────────────────────────────────────────────────
CREATE TABLE books (
    id                  BIGSERIAL PRIMARY KEY,
    collection_id       BIGINT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    archive_id          BIGINT NOT NULL REFERENCES archives(id)    ON DELETE CASCADE,
    lib_id              TEXT   NOT NULL,                 -- LIBID
    file_name           TEXT   NOT NULL,                 -- без расширения
    ext                 TEXT   NOT NULL,                 -- 'fb2'
    size_bytes          BIGINT NOT NULL DEFAULT 0,
    title               TEXT   NOT NULL,
    normalized_title    CITEXT NOT NULL,
    series_id           BIGINT REFERENCES series(id) ON DELETE SET NULL,
    ser_no              INTEGER,
    lang                TEXT,
    date_added          DATE,                            -- DATE из INP
    rating              SMALLINT,                        -- LIBRATE
    keywords            TEXT,
    deleted             BOOLEAN NOT NULL DEFAULT false,  -- DEL=1 → true
    cover_path          TEXT,                            -- относительный путь в /cache/covers/...
    annotation          TEXT,
    metadata_fetched_at TIMESTAMPTZ,
    ext_ids             JSONB  NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (collection_id, archive_id, lib_id)
);
CREATE INDEX books_archive_id            ON books(archive_id);
CREATE INDEX books_series_id             ON books(series_id) WHERE series_id IS NOT NULL;
CREATE INDEX books_normalized_title_trgm ON books USING gin ((normalized_title::text) gin_trgm_ops);
CREATE INDEX books_lang_date             ON books(lang, date_added);
CREATE INDEX books_not_deleted           ON books(id) WHERE deleted = false;

CREATE TABLE book_authors (
    book_id   BIGINT  NOT NULL REFERENCES books(id)   ON DELETE CASCADE,
    author_id BIGINT  NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
    position  SMALLINT NOT NULL DEFAULT 0,
    PRIMARY KEY (book_id, author_id)
);
CREATE INDEX book_authors_author_id ON book_authors(author_id);

CREATE TABLE book_genres (
    book_id  BIGINT NOT NULL REFERENCES books(id)  ON DELETE CASCADE,
    genre_id BIGINT NOT NULL REFERENCES genres(id) ON DELETE CASCADE,
    PRIMARY KEY (book_id, genre_id)
);
CREATE INDEX book_genres_genre_id ON book_genres(genre_id);

-- ── User signals (для re-ranking и истории) ─────────────────────
CREATE TABLE views (
    user_id   BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id   BIGINT      NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    viewed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX views_user_book ON views(user_id, book_id);
CREATE INDEX views_viewed_at ON views(viewed_at);

CREATE TABLE reads (
    user_id      BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id      BIGINT      NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    last_pos     INTEGER,                                 -- позиция (epub-cfi или offset)
    completed_at TIMESTAMPTZ,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, book_id)
);

CREATE TABLE favorites (
    user_id  BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id  BIGINT      NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, book_id)
);

-- ── Background jobs / metadata cache ────────────────────────────
CREATE TABLE import_jobs (
    id            BIGSERIAL PRIMARY KEY,
    collection_id BIGINT      REFERENCES collections(id) ON DELETE SET NULL,
    inpx_path     TEXT,
    inpx_hash     TEXT,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at   TIMESTAMPTZ,
    status        TEXT        NOT NULL CHECK (status IN ('queued','running','succeeded','failed')) DEFAULT 'queued',
    stats         JSONB       NOT NULL DEFAULT '{}'::jsonb,
    error         TEXT
);
CREATE INDEX import_jobs_status ON import_jobs(status);

CREATE TABLE metadata_cache (
    cache_key   TEXT PRIMARY KEY,         -- "ol:work:OL12345W", "wd:Q56789", "fb2:<book_id>"
    payload     JSONB       NOT NULL,
    fetched_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    ttl_seconds INTEGER     NOT NULL DEFAULT 2592000  -- 30d
);
CREATE INDEX metadata_cache_fetched_at ON metadata_cache(fetched_at);
