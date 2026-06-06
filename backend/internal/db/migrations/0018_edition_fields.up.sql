-- Поля уровня ИЗДАНИЯ (= конкретного fb2-файла), извлекаемые локальным
-- проходом по fb2 (см. metadata.EnsureEditionMeta). Нужны, чтобы:
--   1) показать на карточке книги атрибуты каждого издания (переводчик,
--      издатель, год издания, ISBN, язык оригинала);
--   2) служить ключами группировки изданий в одну работу (отдельная фаза):
--      - src_* из <src-title-info> — межъязыковой ключ (рус. перевод ↔ оригинал);
--      - isbn — резолв внешней работы (OpenLibrary Work) + дедуп;
--      - fb2_doc_id (<document-info><id>) — точный дубль одного и того же fb2.
--
-- Все nullable: метаданные в fb2 разрежены. edition_year уже был (миграция
-- 0012) — тут добавляем остальное. edition_meta_scanned_at — маркер «локальный
-- edition-проход уже прошёл» (аналог year_local_scanned_at): отдельный от
-- year-маркера, чтобы уже-просканированные на год книги тоже добрали edition-поля.
ALTER TABLE books
    ADD COLUMN translator              TEXT,    -- <title-info><translator> → "Фамилия Имя"
    ADD COLUMN isbn                    TEXT,    -- <publish-info><isbn>, нормализован (uppercase, [0-9X], только len 10/13)
    ADD COLUMN publisher               TEXT,    -- <publish-info><publisher>
    ADD COLUMN edition_title           TEXT,    -- <publish-info><book-name> (название именно этого издания)
    ADD COLUMN src_lang                TEXT,    -- язык оригинала: <title-info><src-lang> / <src-title-info><lang>
    ADD COLUMN src_title               TEXT,    -- <src-title-info><book-title> (оригинальное название)
    ADD COLUMN src_author_normalized   CITEXT,  -- нормализованный первый <src-title-info><author>
    ADD COLUMN fb2_doc_id              TEXT,    -- <document-info><id>
    ADD COLUMN page_count              INT,     -- если выводимо (обычно NULL — fb2 не хранит)
    ADD COLUMN edition_meta_scanned_at TIMESTAMPTZ;
CREATE INDEX books_isbn ON books(isbn) WHERE isbn IS NOT NULL;
