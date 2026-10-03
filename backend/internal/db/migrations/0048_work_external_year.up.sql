-- 0048: внешний год работы (#288) — год первой публикации от курируемого
-- источника (Фантлаб; приходит в ответе поиска «Известности»). Хранится
-- отдельно от изданий: год работы (works.written_year) = самый ранний из
-- правдоподобных fb2-годов изданий и внешнего года, с потолком по самому раннему
-- году издания (metadata.recomputeWorkYears); ручная правка неприкосновенна.
ALTER TABLE works ADD COLUMN external_year SMALLINT;
ALTER TABLE works ADD COLUMN external_year_source TEXT;
