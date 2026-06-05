-- Срезаем региональный/скриптовый субтег у кодов языка (ru-RU → ru, en_US → en,
-- zh-Hans → zh). 0015 уже привёл к нижнему регистру + trim, но локали остаются
-- отдельными «языками» и снова плодят дубль в списке/фильтре/видимости. Для
-- каталога важен язык, а не локаль — оставляем только первичный субтег.
--
-- regexp_replace('[-_].*$') = срез от первого '-' или '_' до конца, что в точности
-- совпадает с importer.normalizeLang. Meili-индекс выравнивает one-shot ResyncLangs
-- на старте (гейт app_settings.lang_normalized_v2).
UPDATE books
SET lang = regexp_replace(lower(btrim(lang)), '[-_].*$', '')
WHERE lang IS NOT NULL
  AND lang <> regexp_replace(lower(btrim(lang)), '[-_].*$', '');
