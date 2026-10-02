-- Причина решения перепроверки биографий и фото авторов (#280, разбор ошибок
-- обогащения): по каждому источнику — чем кончился поиск («wikipedia/ru: reject
-- name_gate «Гарднер, Иван»; openlibrary: reject search»). Записи до этой
-- миграции причины не знают — NULL.
ALTER TABLE author_meta_recheck ADD COLUMN reason TEXT;
