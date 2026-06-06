DROP INDEX IF EXISTS books_work_unscanned;
ALTER TABLE books DROP COLUMN IF EXISTS work_scanned_at;
DROP TABLE IF EXISTS book_work_lookups;
