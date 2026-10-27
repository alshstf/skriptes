-- Список желаний (план recsys/04, 4.3; #597): книги из файла импорта истории,
-- которых нет в библиотеке. Когда такая работа появится при импорте INPX,
-- recsys.Matcher запишет «появилось то, что вы искали» (search_matches), как для
-- ненайденных поисков, а желание вычеркнет. В отличие от search_log у списка нет
-- срока и выключателя (SKRIPTES_SEARCH_LOG_DAYS): это осознанный выбор, а не
-- история поиска. Откат пакета импорта удаляет его желания.
CREATE TABLE wishlist (
    id              BIGSERIAL   PRIMARY KEY,
    user_id         BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    q               TEXT        NOT NULL,                 -- «название автор» для поиска
    norm            TEXT        NOT NULL,                 -- recsys.NormalizeQuery(q): без дублей
    added_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    import_batch_id BIGINT      REFERENCES import_batches(id) ON DELETE CASCADE,
    UNIQUE (user_id, norm)
);
CREATE INDEX wishlist_import_batch ON wishlist (import_batch_id) WHERE import_batch_id IS NOT NULL;

ALTER TABLE import_batches ADD COLUMN wishlist INTEGER NOT NULL DEFAULT 0; -- ненайденных под наблюдением
