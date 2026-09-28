-- Подсказки авторов/серий и поиск на /authors сравнивают имя без различия «ё»/«е»
-- (#278): «семенов» находит «Семёнов». Выражение в запросах
-- (catalog/suggest.go, authors_list.go) обязано совпадать с индексным.
CREATE INDEX authors_name_yo_trgm ON authors USING gin ((replace(normalized_name::text, 'ё', 'е')) gin_trgm_ops);
CREATE INDEX series_title_yo_trgm ON series USING gin ((replace(normalized_title::text, 'ё', 'е')) gin_trgm_ops);
