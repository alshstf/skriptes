-- 0045: хранимые агрегаты автора для сортировок /authors (#302).
--
-- book_count — число работ автора, max_rating — максимум внешнего рейтинга
-- COALESCE(LIBRATE, web) по его изданиям; оба без сборников (зеркало
-- catalog.notCompilationClause). Считались коррелированным подзапросом по всем
-- авторам на каждый запрос — сортировка «по числу книг» шла ~10 с при тайм-ауте
-- 15 с. Пересчёт целиком — catalog.RecomputeAuthorStats (старт, после импорта,
-- раз в 30 минут). Личные скрытия контента в ключ сортировки не входят:
-- порядок по общему каталогу, числа в строке — по видимым книгам.
ALTER TABLE authors ADD COLUMN book_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE authors ADD COLUMN max_rating REAL;

-- Partial-предикат совпадает с базовым WHERE списка /authors (NOT is_service),
-- как у authors_renown_idx: фаза 1 (ORDER BY … LIMIT) идёт по индексу.
CREATE INDEX authors_book_count_idx ON authors (book_count DESC) WHERE NOT is_service;
CREATE INDEX authors_max_rating_idx ON authors (max_rating DESC NULLS LAST) WHERE NOT is_service;
