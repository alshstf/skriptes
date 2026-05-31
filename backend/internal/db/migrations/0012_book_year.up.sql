-- Год книги. Разделяем ДВА разных понятия (см. граблю про date_added —
-- дата добавления в коллекцию ≠ год книги):
--
--   written_year  — год написания / первого издания произведения.
--                   Источник: fb2 <title-info><date> → внешние источники
--                   (отдельный PR). Питает гистограмму на страницах автора
--                   и серии и будущую корреляцию вех биографии с книгами.
--                   БЕЗ fallback на год бумажного издания — это сломало бы
--                   смысл статистики.
--   edition_year  — год конкретного бумажного издания, с которого сделан
--                   fb2 (<publish-info><year>). Отдельное справочное поле
--                   на карточке книги; в статистику НЕ идёт.
--
-- written_year_source — происхождение значения written_year:
--   'fb2_title' | 'openlibrary' | 'wikidata' | 'googlebooks' | 'manual'.
--   Нужно для приоритетов при дозаполнении и для честной подписи в UI.
--
-- year_local_scanned_at — отметка, что локальный fb2-проход уже искал год
--   (аналог metadata_fetched_at для обложек). Прогрев по ней не
--   перечитывает книгу повторно; внешний backfill по ней понимает, какие
--   книги уже прошли локальную фазу.
ALTER TABLE books
    ADD COLUMN written_year          SMALLINT,
    ADD COLUMN written_year_source   TEXT,
    ADD COLUMN edition_year          SMALLINT,
    ADD COLUMN year_local_scanned_at TIMESTAMPTZ;
