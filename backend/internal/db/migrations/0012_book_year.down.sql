ALTER TABLE books
    DROP COLUMN IF EXISTS year_local_scanned_at,
    DROP COLUMN IF EXISTS edition_year,
    DROP COLUMN IF EXISTS written_year_source,
    DROP COLUMN IF EXISTS written_year;
