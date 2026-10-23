ALTER TABLE book_adaptations
    DROP COLUMN IF EXISTS released_on,
    DROP COLUMN IF EXISTS released_precision,
    DROP COLUMN IF EXISTS released_checked_at;
