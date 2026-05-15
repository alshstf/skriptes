-- Био и фото авторов: новые поля под lazy-enrichment через Wikipedia/OL.
--
-- Поля nullable: enrichment асинхронный, для большинства авторов сразу
-- после импорта будут пустыми. metadata_fetched_at — флаг "была попытка",
-- чтобы UI мог решить, показывать ли скелетон или fallback.

ALTER TABLE authors
    ADD COLUMN bio TEXT,
    ADD COLUMN photo_path TEXT,
    ADD COLUMN metadata_fetched_at TIMESTAMPTZ;
