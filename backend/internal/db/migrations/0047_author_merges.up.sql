-- 0047: ручные слияния авторов (#308) — «Лукьяненко Сергей» и «Лукьяненко
-- Сергей Васильевич» одним человеком. Работы источника переходят к цели правкой
-- авторов работы (metadata_overrides, переживает ре-импорт); запись здесь
-- помнит слияние, чтобы новые книги источника из следующих INPX тоже
-- переезжали (metadata.ReapplyAuthorMerges после импорта).
CREATE TABLE author_merges (
    source_id BIGINT PRIMARY KEY REFERENCES authors(id) ON DELETE CASCADE,
    target_id BIGINT NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
    merged_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    merged_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (source_id <> target_id)
);
CREATE INDEX author_merges_target ON author_merges (target_id);
