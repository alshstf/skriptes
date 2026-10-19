DROP INDEX IF EXISTS books_instance_hidden;
DROP INDEX IF EXISTS works_instance_hidden;
DROP INDEX IF EXISTS authors_instance_hidden;
ALTER TABLE books   DROP COLUMN IF EXISTS instance_hidden;
ALTER TABLE works   DROP COLUMN IF EXISTS instance_hidden;
ALTER TABLE authors DROP COLUMN IF EXISTS instance_hidden;
DROP TABLE IF EXISTS hidden_works;
DROP TABLE IF EXISTS hidden_authors;
