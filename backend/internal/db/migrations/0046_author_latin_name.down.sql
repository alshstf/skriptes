DROP INDEX IF EXISTS authors_latin_name_trgm;
ALTER TABLE authors DROP COLUMN IF EXISTS latin_name;
