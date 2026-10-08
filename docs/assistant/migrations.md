# Миграции: что добавила каждая

Справка для ассистента; сами миграции — `backend/migrations/`. Новые — сверху.

- `0056_smart_shelves` (`smart_shelves` — умные полки: имя и фильтры /books в JSONB, состав считает поиск, #389)
- `0055_award_wins` (`award_wins` — лауреаты премий белого списка с Фантлаба: премия, год, номинация, название/оригинал/автор у источника, сопоставление `work_id`/`author_id` с каталогом, #389 A2)
- `0054_annotations` (`annotations` — закладки и выделения с заметками веб-ридера по изданию, CFI, #389)
- `0053_device_passwords` (`device_passwords` — пароли устройств для OPDS и синхронизации читалок, хранится hex(SHA-256(hex(MD5))), #389)
- `0052_author_facets` (`author_facets` — жанры, категории, языки изданий и оригинала, экранизации автора для счётчиков фильтров /authors, #389)
- `0051_work_contents` (`work_contents` — состав сборников из оглавления fb2, `works.contents_scanned_at`, #388)
- `0050_work_fantlab_rating` (`works.fantlab_midmark`/`fantlab_rating` — средняя оценка и рейтинг Фантлаба из ответа воркера «Известность», #296)
- `0049_author_rating_score` (`authors.max_rating` → `rating_score`: рейтинг автора — среднее пяти лучших работ, #296)
- `0048_work_external_year` (`works.external_year`/`_source` — год первой публикации от Фантлаба, #288)
- `0047_author_merges` (журнал ручных слияний авторов, #308)
- `0046_author_latin_name` (`authors.latin_name` — латинское имя автора из fb2 переводов для поиска латиницей, #290/#291)
- `0045_author_stats` (`authors.book_count`/`max_rating` — хранимые ключи сортировок /authors, #302)
- `0044_author_meta_recheck_reason` (`author_meta_recheck.reason` — причина решения перепроверки, #280)
- `0043_author_meta_recheck` (журнал перепроверки био/фото авторов, #280)
- `0042_yo_fold_trgm` (trigram-индексы по `replace(…,'ё','е')` имён авторов и названий серий — подсказки/поиск без различия «ё»/«е», #278)
- `0041_series_kind` (`series.kind='multi'` — межавторская/издательская серия, грабля №22)
- `0040_author_name_note` (уточнение тёзки `authors.name_note`, автор уникален по (имя, уточнение), журнал `author_splits` — грабля №22)
- `0039_book_identity_archive_libid` (книга = (архив, lib_id), схлопывание дублей из двух INPX одной библиотеки, см. карту «INPX → upsert»)
- `0038_author_renown`
- `0037_adaptation_tmdb` (`book_adaptations.tmdb_movie_id`/ `tmdb_tv_id`/`poster_checked_at` — TMDB-id из Wikidata P4947/P4983 персистятся при записи адаптации + поштучный TTL перепроверки постер-дыр; частичный индекс `idx_book_adaptations_poster_hole`; авто-фаза `RecheckPosterHoles` воркера «Экранизации» — см. граблю №11/постеры)
- `0036_service_authors` (`authors.is_service`/`is_service_source` — служебные авторы-агрегаты вне списка /authors)
- `0035_book_src_lang_lookups` (учёт попыток Wikidata-backfill src_lang)
- `0034_work_kind` (`works.kind`/`kind_source` — тип работы: collection/anthology/omnibus, NULL=обычная; эвристический классификатор `metadata/work_kind.go::ClassifyWorkKinds` — title-паттерн + серия-паразит (ТОЛЬКО мн.ч. «Сборники»; ед.ч. «(сборник)» — librusec-разворот на рассказы, НЕ метим) + ≥4 авторов; правила #284: серия — только если в ней ВСЕ живые издания работы, «Миры X» и «Собрание сочинений» как серии не сигнал, ≥4 авторов не для нон-фикшна (`sci_`/`nonf_`/справочные), известный роман (Фантлаб ≥ 500 или ≥ 10 Википедий) по серии и авторам не красится (на проде снято 5,1 тыс. меток: «Мастер и Маргарита», «Дракула», «Сто лет одиночества»); классификатор возвращает изменённые работы → после импорта они уходят в works-индекс; runOnce-гейт `work_kind_classified_v2` (`ReclassifyAllHeuristic` — пересчёт всех эвристических меток) + вызов после импорта; kind_source: приоритет override > fantlab > heuristic — **fantlab-типизация реализована**: `work_type_id` приходит в том же ответе search-works renown-воркера (`fantlab.go::fantlabKind`: 3→collection, 17/56→anthology, роман/повесть/рассказ→"novel" = снять ошибочную эвристику kind→NULL; пишет `renown_backfill.go::writeRenown`, override неприкосновенен); kind в works-индексе (schema v6, filterable) → секция «Сборники и антологии» внизу карточки автора; фильтр «Тип» на /books (`kind=book|collection|anthology|omnibus`, `books.kindClause`, фасет `kind`; «Книги» = `kind NOT IN [...]`; у кого «Скрывать сборники» — блок скрыт, #379); **профильная настройка «Скрывать сборники»** (opt-in, дефолт выкл): `ContentConfig.HideCompilations` → `ContentResolver.Exclusions` (3-й результат) → Meili `kind NOT IN [...]` в ListWorks/SuggestWorks + kind-клауза `bookExclusionClause` (карточки автора/серии); прямые ссылки НЕ блокируются (зеркало политики жанров/языков), список авторов и лента подписок сознательно не фильтруются; **loose coupling статистики автора**: сборники/их серии БЕЗУСЛОВНО (не opt-in) вне АГРЕГАТОВ автора — `catalog.notCompilationClause` (`COALESCE((SELECT wk.kind …),'')=''`) в book_count/годах/жанрах/языках/рейтинге/экранизациях (`ListAuthorsFiltered` через `renderAggExclusion`, `GetAuthor` в query-агрегатах); НЕ трогает базовую видимость автора, `fav_books` (личное избранное) и СПИСОК книг карточки (сборники видны в своей секции); план `~/projects/plans/skriptes/archive/compilations-author-page-plan.md`)
- `0033_collection_version` (`collections.inpx_version` — version.info последнего импортированного INPX; заполняет `markCollectionImported` из `inpx.Open→ix.Version`, отдаётся публичной ручкой `/api/version` вместе с версией Skriptes → подвал меню пользователя в `Layout.tsx`)
- `0032_work_wd_sitelinks` (`works.wd_sitelinks` — число языковых разделов Википедии со статьёй, сигнал известности от источника wikidata renown-воркера)
- `0031_work_renown` (внешние счётчики известности на `works`: `fantlab_marks`/`ol_ratings_count`/`ol_want_count` + `work_renown_lookups` — сигналы интегральной популярности, наполняет `metadata/renown_backfill.go`)
- `0030_normalize_src_lang` (канонизация `books.src_lang` lower+trim+срез субтега — язык оригинала стал фасетом works-индекса, коды обязаны быть каноническими; зеркало 0015/0016 для lang)
- `0029_engagement_book_idx` (индексы `views(book_id)`/`reads(book_id)` для популярности works); `0028_metadata_overrides` (`metadata_overrides` — локальные ручные правки метаданных каталога, только админ; см. граблю №19)
- `0027_external_rating_lookups` (`book_external_rating_lookups`); `0026_external_rating` (`books.external_rating`/`_source`/`_count` — внешний рейтинг из сети, см. граблю №17)
- `0025_rating_prompts` (`reads.acquired_at` + `book_rating_prompts` для отложенных запросов оценки); `0024_book_ratings` (пользовательские оценки книг), `0023_favorites_collection` (★-избранное → служебная полка, граблю №16), `0022_feed_dismissals`, `0021_collections`, `0020_genre_favorites`, `0019_book_work_lookups` (граблю №15).
