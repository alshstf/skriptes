-- Подсказки авторов/серий, поиск на /authors и плашка «автор по запросу» в /books
-- сравнивают имя со свёрткой «ё/э/й» → «е/е/и» (#516): «бредбери» находит
-- «Брэдбери», «хаинлаин» — «Хайнлайн». Индексы по тому же выражению, что в
-- запросах (textnorm.NameFoldSQL); на них же — триграммный добор похожих имён
-- (оператор <%). Прежние индексы со свёрткой одной «ё» (0042) больше не нужны.
CREATE INDEX authors_name_fold_trgm ON authors USING gin ((translate(normalized_name::text, 'ёэй', 'ееи')) gin_trgm_ops);
CREATE INDEX series_title_fold_trgm ON series USING gin ((translate(normalized_title::text, 'ёэй', 'ееи')) gin_trgm_ops);
DROP INDEX IF EXISTS authors_name_yo_trgm;
DROP INDEX IF EXISTS series_title_yo_trgm;
