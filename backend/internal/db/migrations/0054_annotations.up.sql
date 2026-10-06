-- Закладки и выделения с заметками в веб-ридере (#389, B6). Позиция — EPUB CFI
-- издания (свой у каждого издания и формата ридера); excerpt — выделенный текст,
-- label — глава, fraction — доля книги (для порядка и подписи).
CREATE TABLE annotations (
    id         BIGSERIAL   PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id    BIGINT      NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    kind       TEXT        NOT NULL CHECK (kind IN ('bookmark', 'highlight')),
    cfi        TEXT        NOT NULL,
    excerpt    TEXT        NOT NULL DEFAULT '',
    note       TEXT        NOT NULL DEFAULT '',
    label      TEXT        NOT NULL DEFAULT '',
    fraction   REAL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, book_id, kind, cfi)
);

CREATE INDEX annotations_book_idx ON annotations (book_id);
