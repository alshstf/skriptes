ALTER TABLE books
    DROP COLUMN IF EXISTS external_rating,
    DROP COLUMN IF EXISTS external_rating_source,
    DROP COLUMN IF EXISTS external_rating_count;
