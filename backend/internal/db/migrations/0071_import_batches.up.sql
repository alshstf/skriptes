-- Импорт истории чтения из файлов (план recsys/04, 4.4): пакет импорта и метка
-- «это записал пакет N» на всём, что он создал, — откат удаляет только своё.
-- Пользовательская запись поверх (оценка, «прочитано», заметка к подчёркиванию)
-- метку снимает: подтверждённое человеком откат не трогает. Сам файл не
-- хранится — только итоги.
CREATE TABLE import_batches (
    id         BIGSERIAL   PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source     TEXT        NOT NULL CHECK (source IN ('kindle', 'calibre', 'goodreads', 'csv')),
    file_name  TEXT        NOT NULL DEFAULT '',
    items      INTEGER     NOT NULL DEFAULT 0, -- книг в файле
    applied    INTEGER     NOT NULL DEFAULT 0, -- книг, сопоставленных и применённых
    ratings    INTEGER     NOT NULL DEFAULT 0,
    reads      INTEGER     NOT NULL DEFAULT 0,
    to_read    INTEGER     NOT NULL DEFAULT 0,
    highlights INTEGER     NOT NULL DEFAULT 0,
    kept       INTEGER     NOT NULL DEFAULT 0, -- не тронуто: уже было своё (оценка, прочитано, полка)
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    undone_at  TIMESTAMPTZ
);
CREATE INDEX import_batches_user ON import_batches (user_id, applied_at DESC);

ALTER TABLE book_ratings          ADD COLUMN import_batch_id BIGINT REFERENCES import_batches(id) ON DELETE SET NULL;
ALTER TABLE reads                 ADD COLUMN import_batch_id BIGINT REFERENCES import_batches(id) ON DELETE SET NULL;
ALTER TABLE annotations           ADD COLUMN import_batch_id BIGINT REFERENCES import_batches(id) ON DELETE SET NULL;
ALTER TABLE user_collection_books ADD COLUMN import_batch_id BIGINT REFERENCES import_batches(id) ON DELETE SET NULL;

CREATE INDEX book_ratings_import_batch          ON book_ratings (import_batch_id)          WHERE import_batch_id IS NOT NULL;
CREATE INDEX reads_import_batch                 ON reads (import_batch_id)                 WHERE import_batch_id IS NOT NULL;
CREATE INDEX annotations_import_batch           ON annotations (import_batch_id)           WHERE import_batch_id IS NOT NULL;
CREATE INDEX user_collection_books_import_batch ON user_collection_books (import_batch_id) WHERE import_batch_id IS NOT NULL;
