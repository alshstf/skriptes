-- 0057: синхронизация KOReader (#389, B4).
--
-- book_documents — какой книге соответствует документ KOReader: «частичный MD5»
-- файла, как его отдали при скачивании (свой у каждого формата и издания).
-- kosync_progress — позиция чтения, присланная читалкой (KOReader, Readest):
-- progress — их собственная метка (xpointer), percentage — доля прочитанного.
CREATE TABLE book_documents (
    document   TEXT        PRIMARY KEY,
    book_id    BIGINT      NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    format     TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX book_documents_book_idx ON book_documents (book_id);

CREATE TABLE kosync_progress (
    user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    document   TEXT        NOT NULL,
    progress   TEXT        NOT NULL,
    percentage REAL        NOT NULL,
    device     TEXT        NOT NULL DEFAULT '',
    device_id  TEXT        NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, document)
);
