-- Год оригинала перевода из fb2 <src-title-info><date> (этап 4 добора года
-- написания, #533): кандидат года работы наравне с fb2-годом
-- (metadata/work_years.go, источник fb2_src). Пишет обработка коллекции
-- (EnsureEditionMeta); уже просканированные переводы догоняет разовый проход
-- metadata/src_year_backfill.go.
ALTER TABLE books ADD COLUMN src_year SMALLINT;
