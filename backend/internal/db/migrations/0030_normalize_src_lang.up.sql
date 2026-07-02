-- Нормализуем books.src_lang (язык оригинала из fb2 <src-lang>/<src-title-info><lang>)
-- к тем же правилам, что lang (миграции 0015+0016): lower + btrim + срез
-- регионального/скриптового субтега (en-US → en). До сих пор src_lang писался
-- «как есть» (только TrimSpace в scanFb2EditionMeta) и использовался лишь в
-- on-the-fly нормализующих запросах (фильтр авторов, Tier-1 группировка). Теперь
-- src_lang становится фасетом works-индекса и самостоятельным фильтром — коды
-- обязаны быть каноническими в колонке, иначе 'en' и 'EN-us' двоятся в фасете.
-- Пустые строки схлопываем в NULL (семантика «неизвестен» едина).
--
-- Meili works-индекс выравнивает one-shot ресинк на старте
-- (гейт app_settings.src_lang_synced_v1) — SQL-миграция Meili не трогает.
UPDATE books
SET src_lang = NULLIF(regexp_replace(lower(btrim(src_lang)), '[-_].*$', ''), '')
WHERE src_lang IS NOT NULL
  AND src_lang IS DISTINCT FROM NULLIF(regexp_replace(lower(btrim(src_lang)), '[-_].*$', ''), '');
