ALTER TABLE user_collection_books DROP COLUMN IF EXISTS import_batch_id;
ALTER TABLE annotations           DROP COLUMN IF EXISTS import_batch_id;
ALTER TABLE reads                 DROP COLUMN IF EXISTS import_batch_id;
ALTER TABLE book_ratings          DROP COLUMN IF EXISTS import_batch_id;
DROP TABLE IF EXISTS import_batches;
