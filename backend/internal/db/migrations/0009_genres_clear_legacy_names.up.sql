-- Cleanup: старый importer'овский upsertGenre писал name_ru = name_en = fb2_code
-- (комментарий в коде честно говорил «улучшение имён — отдельная задача»).
-- Сейчас эта задача делается: internal/genres.Seed на startup'е backend'а
-- проставит правильные RU-имена из встроенного словаря для known кодов.
--
-- Эта миграция чистит legacy-значения чтобы:
--  1) Seed получил предсказуемое стартовое состояние NULL для всех кодов,
--     потом сам поставил name_ru где есть в словаре.
--  2) Коды которых нет в нашем словаре (новые из реальных коллекций)
--     остались с NULL — тогда COALESCE(name_ru, fb2_code) корректно
--     сфолбэкнет на код вместо показа дублирующего «sf_action / sf_action».
--
-- Идемпотентно: повторный запуск ничего не меняет (после первого все
-- legacy-значения уже NULL).

UPDATE genres SET name_ru = NULL WHERE name_ru = fb2_code;
UPDATE genres SET name_en = NULL WHERE name_en = fb2_code;
