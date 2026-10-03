-- Идентичность книги: (архив, lib_id) вместо (коллекция, архив, lib_id).
--
-- Все архивы лежат в одном каталоге BOOKS_ROOT, файл книги — BOOKS_ROOT/<архив>,
-- поэтому пара (имя архива, lib_id) однозначно указывает на файл. Коллекция
-- (= имя INPX-файла) в ключе заставляла переименованный раздачей INPX
-- (librusec_local_fb2 → librusec_mhl/librusec_flib, #250) заводить новую
-- коллекцию и вставлять каждую книгу второй раз. Теперь archives уникальны по
-- имени файла, books — по (archive_id, lib_id); books.collection_id /
-- archives.collection_id остаются как «какой INPX описал последним».
--
-- Если в базе уже есть одни и те же книги из нескольких коллекций (импорт двух
-- INPX одной библиотеки), они схлопываются в одну строку:
--   * остаётся книга с ручными правками метаданных, иначе самая старая
--     (наименьший id — на ней история чтения и группировка в работы);
--   * на неё переносятся чтение, просмотры, полки; у опустевших работ —
--     оценки, запросы оценки и скрытия из ленты — на работу оставшейся книги;
--   * правки метаданных удалённых книг и опустевших работ пропадают (они
--     материализованы в колонки удаляемых строк);
--   * удалённые книги и работы вычищаются из поиска на старте backend по
--     записи в app_settings (book_identity_dedup_v1).
-- На базе без дублей всё это — пустые проходы.
--
-- Временным таблицам — ключи и ANALYZE: autovacuum их не анализирует, и без
-- статистики планировщик на сотнях тысяч дублей уходил во вложенные переборы.

ALTER TABLE books    DROP CONSTRAINT books_collection_id_archive_id_lib_id_key;
ALTER TABLE archives DROP CONSTRAINT archives_collection_id_filename_key;

-- 1. Архивы: одно имя файла — одна строка (остаётся наименьший id).
CREATE TEMP TABLE dedup_archives AS
SELECT id AS old_id, keep AS new_id
FROM (SELECT id, min(id) OVER (PARTITION BY filename) AS keep FROM archives) a
WHERE id <> keep;
ALTER TABLE dedup_archives ADD PRIMARY KEY (old_id);
ANALYZE dedup_archives;

-- 2. Книги: один (архив, lib_id) — одна строка.
CREATE TEMP TABLE dedup_books AS
SELECT id AS old_id, keep AS new_id
FROM (
    SELECT b.id,
           first_value(b.id) OVER (
               PARTITION BY COALESCE(da.new_id, b.archive_id), b.lib_id
               ORDER BY EXISTS (SELECT 1 FROM metadata_overrides o
                                WHERE o.target_kind = 'book' AND o.target_id = b.id) DESC,
                        b.id
           ) AS keep
    FROM books b
    LEFT JOIN dedup_archives da ON da.old_id = b.archive_id
) x
WHERE id <> keep;
ALTER TABLE dedup_books ADD PRIMARY KEY (old_id);
CREATE INDEX ON dedup_books (new_id);
ANALYZE dedup_books;

-- 3. Данные пользователей удаляемых книг → на оставшуюся. Где ключ не даёт
-- двух строк на одну книгу — берём одну (самую свежую), остальное уйдёт
-- каскадом вместе с книгой.
UPDATE reads r SET book_id = p.new_id
FROM (
    SELECT DISTINCT ON (r2.user_id, d.new_id) r2.user_id, r2.book_id, d.new_id
    FROM reads r2
    JOIN dedup_books d ON d.old_id = r2.book_id
    WHERE NOT EXISTS (SELECT 1 FROM reads k WHERE k.user_id = r2.user_id AND k.book_id = d.new_id)
    ORDER BY r2.user_id, d.new_id, r2.updated_at DESC
) p
WHERE r.user_id = p.user_id AND r.book_id = p.book_id;

UPDATE views v SET book_id = d.new_id
FROM dedup_books d
WHERE v.book_id = d.old_id;

UPDATE user_collection_books c SET book_id = p.new_id
FROM (
    SELECT DISTINCT ON (c2.collection_id, d.new_id) c2.collection_id, c2.book_id, d.new_id
    FROM user_collection_books c2
    JOIN dedup_books d ON d.old_id = c2.book_id
    WHERE NOT EXISTS (SELECT 1 FROM user_collection_books k
                      WHERE k.collection_id = c2.collection_id AND k.book_id = d.new_id)
    ORDER BY c2.collection_id, d.new_id, c2.added_at
) p
WHERE c.collection_id = p.collection_id AND c.book_id = p.book_id;

DELETE FROM metadata_overrides o
USING dedup_books d
WHERE o.target_kind = 'book' AND o.target_id = d.old_id;

-- 4. Работы, у которых не останется ни одной книги: их пользовательские данные
-- → на работу оставшейся книги.
CREATE TEMP TABLE dedup_works AS
SELECT DISTINCT ON (lb.work_id) lb.work_id AS old_id, kb.work_id AS new_id
FROM dedup_books d
JOIN books lb ON lb.id = d.old_id
JOIN books kb ON kb.id = d.new_id
WHERE lb.work_id IS NOT NULL AND kb.work_id IS NOT NULL AND lb.work_id <> kb.work_id
  AND NOT EXISTS (
      SELECT 1 FROM books b
      WHERE b.work_id = lb.work_id
        AND NOT EXISTS (SELECT 1 FROM dedup_books d2 WHERE d2.old_id = b.id))
ORDER BY lb.work_id, d.old_id;
ALTER TABLE dedup_works ADD PRIMARY KEY (old_id);
ANALYZE dedup_works;

UPDATE book_ratings r SET work_id = p.new_id
FROM (
    SELECT DISTINCT ON (r2.user_id, w.new_id) r2.user_id, r2.work_id, w.new_id
    FROM book_ratings r2
    JOIN dedup_works w ON w.old_id = r2.work_id
    WHERE NOT EXISTS (SELECT 1 FROM book_ratings k WHERE k.user_id = r2.user_id AND k.work_id = w.new_id)
    ORDER BY r2.user_id, w.new_id, r2.rated_at DESC
) p
WHERE r.user_id = p.user_id AND r.work_id = p.work_id;

UPDATE book_rating_prompts r SET work_id = p.new_id
FROM (
    SELECT DISTINCT ON (r2.user_id, w.new_id) r2.user_id, r2.work_id, w.new_id
    FROM book_rating_prompts r2
    JOIN dedup_works w ON w.old_id = r2.work_id
    WHERE NOT EXISTS (SELECT 1 FROM book_rating_prompts k WHERE k.user_id = r2.user_id AND k.work_id = w.new_id)
    ORDER BY r2.user_id, w.new_id, r2.updated_at DESC
) p
WHERE r.user_id = p.user_id AND r.work_id = p.work_id;

UPDATE feed_dismissals r SET work_id = p.new_id
FROM (
    SELECT DISTINCT ON (r2.user_id, w.new_id) r2.user_id, r2.work_id, w.new_id
    FROM feed_dismissals r2
    JOIN dedup_works w ON w.old_id = r2.work_id
    WHERE NOT EXISTS (SELECT 1 FROM feed_dismissals k WHERE k.user_id = r2.user_id AND k.work_id = w.new_id)
    ORDER BY r2.user_id, w.new_id, r2.dismissed_at DESC
) p
WHERE r.user_id = p.user_id AND r.work_id = p.work_id;

-- Работы, которые затронет схлопывание (для пересчёта и поиска).
CREATE TEMP TABLE dedup_touched_works AS
SELECT b.work_id AS id FROM books b JOIN dedup_books d ON d.old_id = b.id WHERE b.work_id IS NOT NULL
UNION
SELECT b.work_id FROM books b JOIN dedup_books d ON d.new_id = b.id WHERE b.work_id IS NOT NULL;
ALTER TABLE dedup_touched_works ADD PRIMARY KEY (id);
ANALYZE dedup_touched_works;

-- 5. Удаляем лишние книги (зависимые строки — каскадом) и опустевшие работы.
DELETE FROM books b USING dedup_books d WHERE b.id = d.old_id;

CREATE TEMP TABLE dedup_gone_works (id BIGINT PRIMARY KEY);
WITH gone AS (
    DELETE FROM works w
    USING dedup_touched_works t
    WHERE w.id = t.id AND NOT EXISTS (SELECT 1 FROM books b WHERE b.work_id = w.id)
    RETURNING w.id
)
INSERT INTO dedup_gone_works SELECT id FROM gone;
ANALYZE dedup_gone_works;

DELETE FROM metadata_overrides o
USING dedup_gone_works g
WHERE o.target_kind = 'work' AND o.target_id = g.id;

UPDATE works w
SET edition_count = (SELECT count(*) FROM books b WHERE b.work_id = w.id AND b.deleted = false),
    updated_at = now()
FROM dedup_touched_works t
WHERE w.id = t.id;

-- 6. Книги — на оставшийся архив, лишние архивы — прочь.
UPDATE books b SET archive_id = d.new_id FROM dedup_archives d WHERE b.archive_id = d.old_id;
DELETE FROM archives a USING dedup_archives d WHERE a.id = d.old_id;

-- 7. Новые ключи.
ALTER TABLE archives ADD CONSTRAINT archives_filename_key UNIQUE (filename);
ALTER TABLE books    ADD CONSTRAINT books_archive_id_lib_id_key UNIQUE (archive_id, lib_id);

-- 8. Что вычистить из поиска (делает backend на старте, см. importer.PurgeDedupedDocs).
INSERT INTO app_settings (key, value, updated_at)
SELECT 'book_identity_dedup_v1',
       jsonb_build_object(
           'books',        COALESCE((SELECT jsonb_agg(old_id ORDER BY old_id) FROM dedup_books), '[]'::jsonb),
           'works_gone',   COALESCE((SELECT jsonb_agg(id ORDER BY id) FROM dedup_gone_works), '[]'::jsonb),
           'works_update', COALESCE((SELECT jsonb_agg(t.id ORDER BY t.id) FROM dedup_touched_works t
                                     WHERE NOT EXISTS (SELECT 1 FROM dedup_gone_works g WHERE g.id = t.id)),
                                    '[]'::jsonb)),
       now()
WHERE EXISTS (SELECT 1 FROM dedup_books)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();

DROP TABLE dedup_archives, dedup_books, dedup_works, dedup_touched_works, dedup_gone_works;
