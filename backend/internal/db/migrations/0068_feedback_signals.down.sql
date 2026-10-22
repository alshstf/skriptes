DROP INDEX IF EXISTS user_collections_one_to_read;
DELETE FROM user_collection_books cb USING user_collections c WHERE cb.collection_id = c.id AND c.kind = 'to_read';
DELETE FROM user_collections WHERE kind = 'to_read';
DROP TABLE IF EXISTS blocked_series;
DROP TABLE IF EXISTS blocked_authors;
DROP TABLE IF EXISTS rating_reasons;
