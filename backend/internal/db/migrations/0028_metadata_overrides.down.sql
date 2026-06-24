-- Материализованные в books.*/works.* значения не откатываем (down-миграция
-- данные-фичи «расправить» не может — это ожидаемо).
DROP TABLE IF EXISTS metadata_overrides;
