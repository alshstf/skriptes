-- Индексы по book_id для подсчёта популярности работы (Σ изданий: views + 3×reads)
-- в works-индексе. Существующие views_user_book / reads — композит по
-- (user_id, book_id); по одному book_id (все пользователи) такой индекс
-- неэффективен, а на полном ресинке works (сотни тысяч работа × 2 подзапроса)
-- это критично. Отдельные book_id-индексы делают COUNT по книге index-only-fast.
CREATE INDEX IF NOT EXISTS views_book_id ON views (book_id);
CREATE INDEX IF NOT EXISTS reads_book_id ON reads (book_id);
