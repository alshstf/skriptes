-- Пересказы кусков текста книги для ИИ-функций читалки («Напомнить, что было»,
-- вторая ступень контекста у «Объяснить»): общий кэш для всех читателей книги.
-- Кусок — примерно chunk_runes знаков по границам абзацев (booktext.Flat.Chunks);
-- text_version — хеш epub, из которого взят текст: пересобрали epub — старые
-- строки просто перестают находиться.
CREATE TABLE book_summaries (
    book_id      BIGINT      NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    text_version TEXT        NOT NULL,
    chunk_runes  INTEGER     NOT NULL,
    chunk        INTEGER     NOT NULL,
    summary      TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (book_id, text_version, chunk_runes, chunk)
);
