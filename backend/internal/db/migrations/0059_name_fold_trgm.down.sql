CREATE INDEX IF NOT EXISTS authors_name_yo_trgm ON authors USING gin ((replace(normalized_name::text, 'ё', 'е')) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS series_title_yo_trgm ON series USING gin ((replace(normalized_title::text, 'ё', 'е')) gin_trgm_ops);
DROP INDEX IF EXISTS series_title_fold_trgm;
DROP INDEX IF EXISTS authors_name_fold_trgm;
