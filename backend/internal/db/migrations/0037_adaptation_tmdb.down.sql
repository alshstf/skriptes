DROP INDEX IF EXISTS idx_book_adaptations_poster_hole;
ALTER TABLE book_adaptations
    DROP COLUMN IF EXISTS tmdb_movie_id,
    DROP COLUMN IF EXISTS tmdb_tv_id,
    DROP COLUMN IF EXISTS poster_checked_at;
