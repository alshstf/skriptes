-- Возврат к уникальности по имени. Если тёзки уже разделены (есть записи с
-- одинаковым normalized_name), ограничение не создастся — их нужно слить вручную.
DROP TABLE author_splits;
DROP INDEX authors_name_note_key;
ALTER TABLE authors ADD CONSTRAINT authors_normalized_name_key UNIQUE (normalized_name);
ALTER TABLE authors DROP COLUMN name_note;
