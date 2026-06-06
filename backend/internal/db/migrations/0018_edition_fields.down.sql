DROP INDEX IF EXISTS books_isbn;
ALTER TABLE books
    DROP COLUMN IF EXISTS translator,
    DROP COLUMN IF EXISTS isbn,
    DROP COLUMN IF EXISTS publisher,
    DROP COLUMN IF EXISTS edition_title,
    DROP COLUMN IF EXISTS src_lang,
    DROP COLUMN IF EXISTS src_title,
    DROP COLUMN IF EXISTS src_author_normalized,
    DROP COLUMN IF EXISTS fb2_doc_id,
    DROP COLUMN IF EXISTS page_count,
    DROP COLUMN IF EXISTS edition_meta_scanned_at;
