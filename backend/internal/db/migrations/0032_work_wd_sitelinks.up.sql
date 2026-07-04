-- Число sitelinks Wikidata (в скольких языковых разделах Википедии есть статья
-- о книге) — классический прокси мировой известности, слагаемое интегральной
-- популярности (importer/popularity.go). Заполняет источник 'wikidata' воркера
-- metadata/renown_backfill.go (учёт попыток — в общей work_renown_lookups).
ALTER TABLE works
    ADD COLUMN wd_sitelinks INTEGER;
