-- 0049: рейтинг автора — среднее лучших работ, а не максимум (#296).
--
-- authors.max_rating (0045) был максимумом COALESCE(LIBRATE, web) по изданиям:
-- у 4 403 авторов стояло «5.0» (у 3 771 — одна книга с LIBRATE 5, у 540 — веб-оценка
-- из 1–4 голосов), и сортировка «по рейтингу» вырождалась в «по числу книг».
-- Теперь rating_score — среднее пяти лучших работ автора (оценка работы —
-- LIBRATE, иначе веб-оценка от 5 голосов), у кого их меньше пяти — с подтяжкой
-- к средней по коллекции (catalog.RecomputeAuthorStats). Столбец переименован,
-- чтобы имя не врало; значения пересчитает старт.
ALTER TABLE authors RENAME COLUMN max_rating TO rating_score;
ALTER INDEX authors_max_rating_idx RENAME TO authors_rating_score_idx;
