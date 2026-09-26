-- Возврат к ключу (коллекция, архив, lib_id). Схлопнутые дубли не
-- восстанавливаются — данные под новым ключом удовлетворяют и старому.
ALTER TABLE books    DROP CONSTRAINT books_archive_id_lib_id_key;
ALTER TABLE archives DROP CONSTRAINT archives_filename_key;
ALTER TABLE archives ADD CONSTRAINT archives_collection_id_filename_key UNIQUE (collection_id, filename);
ALTER TABLE books    ADD CONSTRAINT books_collection_id_archive_id_lib_id_key UNIQUE (collection_id, archive_id, lib_id);
