-- Popularity (proxy: wikibase:sitelinks — число языковых Wikipedia,
-- ссылающихся на статью фильма). Используется как primary sort в
-- service.List: сначала знаменитые экранизации, потом по убыванию года.
--
-- Сбрасываем существующие adaptations и снимаем флаг adaptations_fetched_at:
-- старые записи были без popularity и со старым ext_url (Wikidata-ссылка,
-- а не Кинопоиск/IMDb). При следующем GET'е /api/books/{id}/adaptations
-- enrichment запустится заново и заполнит обе колонки правильно.
--
-- Безопасно для прод-данных: в proде adaptations enrich'ится lazy при
-- первом открытии карточки книги, потеря не критична (несколько секунд
-- ожидания при повторном открытии).

ALTER TABLE book_adaptations ADD COLUMN popularity INTEGER;

DELETE FROM book_adaptations;
UPDATE books SET adaptations_fetched_at = NULL WHERE adaptations_fetched_at IS NOT NULL;
