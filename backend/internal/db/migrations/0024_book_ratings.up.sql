-- Пользовательские оценки книг (work-level). ОТДЕЛЬНО от books.rating (LIBRATE из
-- INPX — это библиотечный рейтинг). Шкала 1–5; одна оценка на (пользователь, работа).
-- Оцениваем логическую книгу (work), а не конкретное издание — как избранное/чтение.
CREATE TABLE book_ratings (
    user_id  BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    work_id  BIGINT      NOT NULL REFERENCES works (id) ON DELETE CASCADE,
    rating   SMALLINT    NOT NULL CHECK (rating BETWEEN 1 AND 5),
    rated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, work_id)
);

-- Для агрегата «средняя оценка работы» (avg/count по work_id).
CREATE INDEX book_ratings_work_idx ON book_ratings (work_id);
