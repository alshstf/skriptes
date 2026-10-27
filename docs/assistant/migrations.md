# Миграции: что добавила каждая

Справка для ассистента; сами миграции — `backend/migrations/`. Новые — сверху.

- `0057_author_lifetime` (`authors.born_year`/`died_year` — годы жизни из Wikidata принятой поиском био статьи, QID автора — `ext_ids.wd_qid`; правило года работы и био-таймлайн, #465)
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
- `0042_yo_fold_trgm` (trigram-индексы по `replace(…,'ё','е')` имён авторов и названий серий — подсказки/поиск без различия «ё»/«е», #278; заменены в 0059)
- `0041_series_kind` (`series.kind='multi'` — межавторская/издательская серия, грабля №22)
- `0040_author_name_note` (уточнение тёзки `authors.name_note`, автор уникален по (имя, уточнение), журнал `author_splits` — грабля №22)
- `0039_book_identity_archive_libid` (книга = (архив, lib_id), схлопывание дублей из двух INPX одной библиотеки, см. карту «INPX → upsert»)
- `0038_author_renown`
- `0037_adaptation_tmdb` (`book_adaptations.tmdb_movie_id`/ `tmdb_tv_id`/`poster_checked_at` — TMDB-id из Wikidata P4947/P4983 персистятся при записи адаптации + поштучный TTL перепроверки постер-дыр; частичный индекс `idx_book_adaptations_poster_hole`; авто-фаза `RecheckPosterHoles` воркера «Экранизации» — см. граблю №11/постеры)
- `0036_service_authors` (`authors.is_service`/`is_service_source` — служебные авторы-агрегаты вне списка /authors)
- `0035_book_src_lang_lookups` (учёт попыток Wikidata-backfill src_lang)
- `0034_work_kind` (`works.kind`/`kind_source` — тип работы: collection/anthology/omnibus, NULL=обычная; эвристический классификатор `metadata/work_kind.go::ClassifyWorkKinds` — title-паттерн + серия-паразит (ТОЛЬКО мн.ч. «Сборники»; ед.ч. «(сборник)» — librusec-разворот на рассказы, НЕ метим) + ≥4 авторов; правила #284: серия — только если в ней ВСЕ живые издания работы, «Миры X» и «Собрание сочинений» как серии не сигнал, ≥4 авторов не для нон-фикшна (`sci_`/`nonf_`/справочные), известный роман (Фантлаб ≥ 500 или ≥ 10 Википедий) по серии и авторам не красится (на проде снято 5,1 тыс. меток: «Мастер и Маргарита», «Дракула», «Сто лет одиночества»); классификатор возвращает изменённые работы → после импорта они уходят в works-индекс; runOnce-гейт `work_kind_classified_v3` (`ReclassifyAllHeuristic` — пересчёт всех эвристических меток; v3 — тома собраний по названию, #532: ПСС, «Миры X. Том N», «Том N.» + жанровое слово или ≥3 названия, «Полная коллекция», «…логия в одном томе»; «Том 9. Мастер и Маргарита» и «Тихий Дон. Том 1» — не сборники) + вызов после импорта; kind_source: приоритет override > fantlab > heuristic — **fantlab-типизация реализована**: `work_type_id` приходит в том же ответе search-works renown-воркера (`fantlab.go::fantlabKind`: 3→collection, 17/56→anthology, роман/повесть/рассказ→"novel" = снять ошибочную эвристику kind→NULL; пишет `renown_backfill.go::writeRenown`, override неприкосновенен); kind в works-индексе (schema v6, filterable) → секция «Сборники и антологии» внизу карточки автора; фильтр «Тип» на /books (`kind=book|collection|anthology|omnibus`, `books.kindClause`, фасет `kind`; «Книги» = `kind NOT IN [...]`; у кого «Скрывать сборники» — блок скрыт, #379); **профильная настройка «Скрывать сборники»** (opt-in, дефолт выкл): `ContentConfig.HideCompilations` → `ContentResolver.Exclusions` (3-й результат) → Meili `kind NOT IN [...]` в ListWorks/SuggestWorks + kind-клауза `bookExclusionClause` (карточки автора/серии); прямые ссылки НЕ блокируются (зеркало политики жанров/языков), список авторов и лента подписок сознательно не фильтруются; **loose coupling статистики автора**: сборники/их серии БЕЗУСЛОВНО (не opt-in) вне АГРЕГАТОВ автора — `catalog.notCompilationClause` (`COALESCE((SELECT wk.kind …),'')=''`) в book_count/годах/жанрах/языках/рейтинге/экранизациях (`ListAuthorsFiltered` через `renderAggExclusion`, `GetAuthor` в query-агрегатах); НЕ трогает базовую видимость автора, `fav_books` (личное избранное) и СПИСОК книг карточки (сборники видны в своей секции); план `~/projects/plans/skriptes/archive/compilations-author-page-plan.md`)
- `0033_collection_version` (`collections.inpx_version` — version.info последнего импортированного INPX; заполняет `markCollectionImported` из `inpx.Open→ix.Version`, отдаётся публичной ручкой `/api/version` вместе с версией Skriptes → подвал меню пользователя в `Layout.tsx`)
- `0032_work_wd_sitelinks` (`works.wd_sitelinks` — число языковых разделов Википедии со статьёй, сигнал известности от источника wikidata renown-воркера)
- `0031_work_renown` (внешние счётчики известности на `works`: `fantlab_marks`/`ol_ratings_count`/`ol_want_count` + `work_renown_lookups` — сигналы интегральной популярности, наполняет `metadata/renown_backfill.go`)
- `0030_normalize_src_lang` (канонизация `books.src_lang` lower+trim+срез субтега — язык оригинала стал фасетом works-индекса, коды обязаны быть каноническими; зеркало 0015/0016 для lang)
- `0029_engagement_book_idx` (индексы `views(book_id)`/`reads(book_id)` для популярности works); `0028_metadata_overrides` (`metadata_overrides` — локальные ручные правки метаданных каталога, только админ; см. граблю №19)
- `0027_external_rating_lookups` (`book_external_rating_lookups`); `0026_external_rating` (`books.external_rating`/`_source`/`_count` — внешний рейтинг из сети, см. граблю №17)
- `0025_rating_prompts` (`reads.acquired_at` + `book_rating_prompts` для отложенных запросов оценки); `0024_book_ratings` (пользовательские оценки книг), `0023_favorites_collection` (★-избранное → служебная полка, граблю №16), `0022_feed_dismissals`, `0021_collections`, `0020_genre_favorites`, `0019_book_work_lookups` (граблю №15).

## 0064_instance_hidden

Скрытие авторов и работ на инстансе (админ, план `~/projects/plans/skriptes/instance-hide-authors-works.md`): явные
скрытия `hidden_authors(author_id PK, hidden_at, hidden_by, note)` и `hidden_works(work_id PK, …)`; флаги-материализация
`authors/works/books.instance_hidden BOOLEAN NOT NULL DEFAULT false` (константный DEFAULT — без перезаписи таблиц) с
частичными индексами `WHERE instance_hidden`. Флаги пересчитывает `importer.SyncInstanceHidden` (после скрытия/возврата
и в конце прохода импорта); правила — в `importer/instance_hidden.go`.

## 0063_book_src_year

Год оригинала перевода (#533, этап 4): `books.src_year SMALLINT` из fb2 `<src-title-info><date>`. Пишет обработка коллекции
(`EnsureEditionMeta`, `scanFb2EditionMeta` → `EditionMeta.SrcYear`); уже просканированные переводы догоняет разовый
`metadata/src_year_backfill.go::BackfillSrcYears` (по архивам, курсор `app_settings.fb2_src_years_v1`, старт через 10 мин
после запуска). Читает `RecomputeWorkYears` — ещё один fb2-кандидат года работы (источник `fb2_src`).

## 0062_author_fantlab_lookups

Год написания из библиографии автора на Фантлабе (#533): `author_fantlab_lookups(author_id PK, outcome found/not_found/error,
fantlab_id, via wikidata|search, matched, dated, checked_at)`, FK на `authors` с `ON DELETE CASCADE`. Пишет фаза
`metadata/year_fantlab.go` воркера «Год написания» (кандидаты — авторы с живыми работами без года, которых пора спросить:
TTL not_found/error из настроек, found — 180 дней); год — в `works.external_year` (`external_year_source='fantlab'`),
дальше `RecomputeWorkYears`. «Сбросить неудачные» чистит и её not_found/error; учёт — `coverage.fantlab` в
`GET /api/admin/year-enrichment`.

## 0061_author_duplicate_rejections

«Не дубликат» (#527): `author_duplicate_rejections(author_a < author_b, rejected_by, rejected_at)`, FK на `authors` с
`ON DELETE CASCADE`. Пишет `catalog.RejectDuplicate` (`POST /api/admin/authors/{id}/not-duplicate {other_id}`); читают
`AuthorDuplicates` (подсказка на карточке) и `DuplicatePairs` (страница «Дубли авторов») — отклонённые пары не
возвращаются, счётчик пар — без них.

## 0060_work_merge_rejections

«Не объединять» (#517): `work_merge_rejections(work_a < work_b, rejected_by, rejected_at)`, FK на `works` с
`ON DELETE CASCADE` (слитая/удалённая работа снимает пару). Пишет `WorkGroupController.RejectMerge`
(`POST /api/admin/works/merge-rejections`), читают подсказки слияния (`merge_rejections` в ответах автора/серии) и
фоновая группировка (`loadRejectedPairs` → `clusterTier1With`).

## 0059_name_fold_trgm

Свёртка имён «ё/э/й» → «е/е/и» для поиска в PG (#516): trigram-индексы `authors_name_fold_trgm` и
`series_title_fold_trgm` по `translate(normalized_name|normalized_title::text, 'ёэй', 'ееи')`
(`textnorm.NameFoldSQL`, Go-зеркало — `textnorm.FoldName`) вместо `authors_name_yo_trgm` / `series_title_yo_trgm`
из 0042 (удалены). Их используют подсказки авторов/серий, поиск `/authors`, плашка «автор по запросу» в `/books`
и добор похожих имён (оператор `<%`, `catalog/fuzzy_names.go`). Выражение в запросе обязано совпадать с индексным.

## 0058_bio_timeline

Био-таймлайн (план v3): `author_bio(qid PK, name, data JSONB, version, updated_at)` — кэш встроенного датасета
пакета датасета (`alshstf/skriptes-bio`, до 1.55.0 — `internal/bio/data`; bio.Author без evidence; сев на старте при смене версии датасета, ключ `app_settings.bio_dataset_version`)
и `author_event_hides(qid, key, hidden_at, hidden_by)` — админское «скрыть веху», переживает пересев. Связь с `authors` —
только через `authors.ext_ids->>'wd_qid'` на чтении.

## 0065_feed_dismissal_reason

Причина скрытия из ленты рекомендаций: `feed_dismissals.reason` (`not_interested` / `already_read` / `later`,
NULL у скрытий до 1.57 = «не интересно»). Модель близости (`internal/recsys`) считает негативом только
«не интересно». План recsys/01, задача 1.3.

## 0066_read_positions

Журнал позиций чтения `read_positions(user_id, book_id, fraction, at)` — append-only, пишет `history.SavePosition`
(строка — если доля сдвинулась ≥ 0.01 или прошло ≥ 10 минут от прошлой; без доли, как с Kindle, — ничего).
Из него на лету считается личная скорость чтения (`recsys.EstimateSpeed`: сессии с разрывом < 30 мин, медиана
по 20 последним; байт fb2 в минуту, файлы > 3 МБ не в счёт) → «ещё ≈ N минут» в «Продолжить чтение» и на
карточке (`reading_minutes`). Старше 90 дней — строка в день на книгу (`CompactReadPositions`, на старте и раз в
сутки). План recsys/01, задача 1.2.

## 0067_search_log

Лог поиска `search_log(id, user_id, q, norm, filters, hits, clicked_work_id, at)` — пишет `handleListBooks`
(первая страница текстового запроса; продолжение/откат набора в окне 60 с обновляет ту же строку, короче 3
символов не пишется), клик из выдачи — `clicked_work_id`. `search_matches(user_id, work_id, q, searched_at,
matched_at)` — ненайденный запрос совпал с работой, появившейся при импорте (`recsys.Matcher` после импорта,
поиск Meili по `id IN [...]` с порогом `_rankingScore ≥ 0.8`). Срок — `SKRIPTES_SEARCH_LOG_DAYS` (дефолт 365,
0 — лог выключен), чистка в housekeeping-цикле. План recsys/01, задача 1.4.

## 0068_feedback_signals

Явная обратная связь (план recsys/03, задачи 3.1–3.3). `rating_reasons(user_id, work_id, reason, at)` — причины
низкой оценки, закрытый список `recsys.AllRatingReasons` (`too_long`, `gloomy`, `language`, `not_mine`, `unfinished`,
`dated`, `boring`), несколько на работу; живут с оценкой: `RemoveRating` и новая оценка ≥ 4 удаляют
(`history.SetRating`), слияние работ переносит (`reassignWorkUserData`). `blocked_authors(user_id, author_id,
added_at)` и `blocked_series(user_id, series_id, added_at)` — «не предлагать» (только рекомендации и новинки по
подпискам; каталог и поиск не трогает); слияние авторов и сборка межавторской серии переносят, как подписки.
Частичный уникальный индекс `user_collections_one_to_read` — служебная полка «Хочу прочитать»
(`user_collections.kind = 'to_read'`, одна на пользователя, как «Избранное» в 0023).

## 0069_session_mood

Настроение сессии (план recsys/03, задача 3.4). `session_mood(id, user_id, mood, at)` — append-only лог выбора чипа
на Главной («лёгкое · умное · мрачное · смешное · короткое», CHECK по `recsys.AllMoods`), индекс `(user_id, at)`;
пишет `history.LogMood` (`POST /api/me/mood`). Само настроение живёт в браузере и уходит параметром `mood=` в
`GET /api/me/recommendations`; лог — чтобы на приёмке 3.7 видеть, что выбирают по вечерам.

## 0070_adaptation_release_date

Дата выхода экранизации (#581). `book_adaptations.released_on DATE`, `released_precision SMALLINT` (10 — месяц, на
1-е число; 11 — день; год без даты — только `year`), `released_checked_at` — TTL перепроса. Пишет фаза воркера
«Экранизации» `metadata.RefreshReleaseDates` (`adaptation_release.go`): фильмы этого года и двух следующих, один SPARQL
P577 с точностью по VALUES, дата — самая точная и ранняя в пределах `year` фильма (фестиваль прошлого года не в счёт),
на все строки фильма `(provider, ext_id)` разом, TTL 3 дня. Читает подборка «Экранизации» (`books/presets.go`):
порядок и подпись «1 мая 2026».

## 0071_import_batches

Импорт истории чтения из файла (recsys/04, 4.4, 1.68.0): `import_batches(id, user_id, source kindle|calibre|goodreads|csv,
file_name, items, applied, ratings, reads, to_read, highlights, kept, applied_at, undone_at)` — итоги пакета, файла нет;
nullable `import_batch_id` (FK `ON DELETE SET NULL`, частичные индексы) в `book_ratings`, `reads`, `annotations`,
`user_collection_books` — что записал пакет. Пишет `histimport.Apply` (своё пользователя не перетирает), откат —
`histimport.Undo` (удаляет помеченное; у `reads` снимает `completed_at`, строку — только пустую). Пользовательская запись
поверх снимает метку: `history.SetRating` (оценка и авто-«прочитано»), `MarkRead`, `UnmarkRead`, `UpdateAnnotationNote`.

## 0072_wishlist

Список желаний (recsys/04, 4.3, #597, 1.71.0): `wishlist(id, user_id, q, norm, added_at, import_batch_id)`, UNIQUE
`(user_id, norm)` — книги из файла импорта истории, которых нет в библиотеке («название автор»), если в предпросмотре
стоит галочка «Следить за ненайденными». `import_batch_id` — FK `ON DELETE CASCADE`, откат пакета (`histimport.Undo`)
удаляет его желания; `import_batches.wishlist` — сколько взято. Читает `recsys.Matcher.MatchNewWorks` после импорта INPX
вместе с ненайденными поисками (`search_log`, hits = 0): попадание → `search_matches` («Появилось то, что вы искали»),
исполненное желание вычёркивается. В отличие от лога поиска у списка нет срока и выключателя.
