DROP INDEX IF EXISTS authors_max_rating_idx;
DROP INDEX IF EXISTS authors_book_count_idx;
ALTER TABLE authors DROP COLUMN IF EXISTS max_rating;
ALTER TABLE authors DROP COLUMN IF EXISTS book_count;
