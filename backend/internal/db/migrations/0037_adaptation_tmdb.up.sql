-- 0037: TMDB-идентификаторы и учёт проверки постера на book_adaptations.
--
-- tmdb_movie_id / tmdb_tv_id — id в The Movie Database (Wikidata P4947/P4983,
-- персистятся при записи адаптации). Позволяют перепроверять ПОСТЕР чистым
-- TMDB-вызовом БЕЗ повторного SPARQL — постеры новых фильмов появляются на
-- TMDB со временем, и авто-фаза воркера «Экранизации» дозаливает их сама
-- (поштучный TTL poster_checked_at), а не только с ручной кнопки.
ALTER TABLE book_adaptations
    ADD COLUMN tmdb_movie_id TEXT,
    ADD COLUMN tmdb_tv_id TEXT,
    ADD COLUMN poster_checked_at TIMESTAMPTZ;

-- Частичный индекс ровно под выборку авто-фазы: постер-дыры с TMDB-id,
-- отсортированные по давности проверки (NULL = ещё не проверяли — первыми).
CREATE INDEX idx_book_adaptations_poster_hole
    ON book_adaptations (poster_checked_at ASC NULLS FIRST)
    WHERE poster_path IS NULL AND (tmdb_movie_id IS NOT NULL OR tmdb_tv_id IS NOT NULL);
