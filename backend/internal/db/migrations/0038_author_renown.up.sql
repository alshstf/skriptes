-- 0038: материализованная «известность» автора для дефолтной сортировки /authors.
--
-- authors.renown — производное число (см. importer/author_renown.go:
-- max(computeWorkPopularity по не-сборниковым работам автора) + бонус за широту
-- значимого корпуса). Пересчитывается целиком (runOnce-гейт, after-import,
-- после drain-прохода воркера «Известность») — ручной правки нет, source не нужен.
ALTER TABLE authors ADD COLUMN renown BIGINT NOT NULL DEFAULT 0;

-- Partial-предикат совпадает с базовым WHERE списка /authors (NOT is_service):
-- фаза 1 двухфазного запроса (ORDER BY renown DESC ... LIMIT) идёт index-scan'ом.
CREATE INDEX authors_renown_idx ON authors (renown DESC) WHERE NOT is_service;
