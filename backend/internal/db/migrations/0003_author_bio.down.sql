ALTER TABLE authors
    DROP COLUMN IF EXISTS metadata_fetched_at,
    DROP COLUMN IF EXISTS photo_path,
    DROP COLUMN IF EXISTS bio;
