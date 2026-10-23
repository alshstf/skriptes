-- Дата выхода экранизации (#581): Wikidata P577 с точностью — день (11) или
-- месяц (10); у фильма с одним годом даты нет (остаётся year). Подборка
-- «Экранизации» сортирует по ней и пишет «1 мая 2026» вместо «2026».
-- released_checked_at — TTL перепроса: даты будущих фильмов уточняются и
-- переносятся (фаза воркера «Экранизации», metadata.RefreshReleaseDates).
-- Пишется на все строки фильма (provider, ext_id) разом.
ALTER TABLE book_adaptations
    ADD COLUMN released_on         DATE,
    ADD COLUMN released_precision  SMALLINT CHECK (released_precision IN (10, 11)),
    ADD COLUMN released_checked_at TIMESTAMPTZ;
