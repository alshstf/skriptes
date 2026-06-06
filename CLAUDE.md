# CLAUDE.md — гайд для Claude Code (и любого другого AI-ассистента)

Это онбординг-указатель для нового чата / TUI-сессии. Цель — за минуту дать
контекст, который иначе пришлось бы выковыривать grep'ом, плюс предупредить о
граблях, на которых я уже спотыкался.

Полную документацию **не дублирую** — она в `README.md` (что приложение делает
и как развернуть релиз) и `CONTRIBUTING.md` (структура репо, все make-таргеты,
архитектура pipeline'ов). Сюда — только то, чего там нет.

## TL;DR в трёх строках

skriptes — каталогизатор fb2-библиотеки. Go (chi + pgx + sqlc-style raw SQL +
golang-migrate) на бэке, React + Vite + TanStack Router + shadcn/ui на фронте,
Postgres + Meilisearch + Caddy в docker compose. Книги физически живут на
read-only volume и конвертируются на лету через fb2cng.

## Рабочее окружение — только основной чекаут

Все правки, сборки, тесты и git — в основном чекауте
`/Users/alexandershustov/projects/skriptes`, **не в `.claude/worktrees/...`**.
Worktree путает: docker build context `../frontend` / `../backend` берёт
исходники из той папки, откуда запущен compose, поэтому «редактирую в одной,
собираю из другой» = старый бандл и расхождение веток (см. также грабли №1).
Если сессия вдруг стартовала в worktree — сразу `cd` в основной чекаут и работай
там. Ветки под PR создаём прямо в основном чекауте. (Явное предпочтение
пользователя, 2026-05-29.)

## Быстрые команды

```bash
# pre-commit самопроверка (запускай ВСЁ перед коммитом — это явное user-feedback)
cd backend  && go test ./... && golangci-lint run --timeout=5m ./...
cd frontend && npm run lint && npm run typecheck && npm test && npm run build
cd frontend && npx playwright test        # ловит layout-регрессии, jsdom не умеет CSS

# поднять локально + увидеть свои изменения
cd infra && docker compose build backend frontend && \
            docker compose up -d --force-recreate backend frontend
# context: ../backend и ../frontend — то есть git pull в основном чекауте
# (НЕ в worktree) перед билдом, иначе подтянутся старые исходники

# проверить что новый bundle действительно поехал
docker compose exec frontend ls /usr/share/nginx/html/assets/
# хэш index-*.js должен поменяться после контентной правки
```

`make help` покажет полный список таргетов.

## Грабли, на которых я уже спотыкался

### 1. Frontend bundle не обновляется после `docker compose build`

Vite хэширует имя бандла по контенту. Если `git pull` подтянул новые исходники
**в worktree, а не в основной чекаут**, build возьмёт старое содержимое из
`../frontend` (build context в `infra/docker-compose.yml`) и выдаст тот же хэш.
Делай `git pull` в `/Users/<...>/projects/skriptes` (или в любом основном
чекауте), не только в `.claude/worktrees/...`.

Если что-то не сходится — `docker compose build --no-cache frontend` снимает
кэш слоя COPY.

### 2. Жанры на фронте — display-имена и стиль чипов в нескольких местах

`book.genres` от Meili приходит как массив `fb2_code`'ов (строк). Display-имена
живут в Postgres + ежесборочный seed (см. `backend/internal/genres/`),
exposed через `GET /api/genres`. На фронте — `useGenreMap()` в
`frontend/src/lib/genres.ts`.

**Места рендера chips с жанрами:**
- `frontend/src/components/GenreChips.tsx` — одна строка + кликабельный «+N» поповер; список `/books` (BooksPage::BookCard)
- `frontend/src/components/BookListItem.tsx` — AuthorPage / SeriesPage (свои inline-`Badge`)
- `frontend/src/pages/BookDetailPage.tsx` — карточка книги (backend hydrate'ит `g.display` сам, useGenreMap не нужен)

**Стиль** чипов (приглушённый/контрастный) — персональная настройка «Внешний
вид»: класс берётся из `genreChipClass(useGenreChipStyle())`
(`frontend/src/lib/appearance.ts`); его используют и `GenreChips`, и
`BookListItem`. Правишь имена/стиль жанров — пройди по всем местам. Языки в
фильтре резолвятся через `useLanguageMap()` (`lib/content.ts`).

### 3. `date_added` ≠ год написания книги

INPX'овский `date_added` — когда книга добавилась в библиотеку librusec, а не
когда автор её написал. Не использовать как proxy для creation date.

Настоящий год берём из fb2 (миграция 0012, два РАЗНЫХ поля — не смешивать):
- `books.written_year` — год написания / первого издания произведения.
  Источники по приоритету: fb2 `<title-info><date>` (локально — джоба обработки
  коллекции `metadata.Prewarmer`/`EnsureYearLocal`, под-тумблер «Года») →
  OpenLibrary `first_publish_year` → Wikidata `P577` (внешний rate-limited
  backfill-воркер `metadata.YearBackfillController`; per-source учёт попыток в
  таблице `book_year_lookups`, чтобы не долбить источник). Обе джобы — opt-in из
  админки **«Фоновые операции»**. По нему строится гистограмма «Книги по годам
  написания» (`catalog/service.go`); сюда же ляжет будущая bio-корреляция.
  `written_year_source` хранит происхождение.
- `books.edition_year` — год конкретного бумажного издания этого fb2
  (`<publish-info><year>`). Справочное поле на карточке книги, в статистику НЕ
  идёт.

Meili-поле `year` (фильтр/сортировка/фасет «Год» на `/books`) тоже = `written_year`:
импорт его НЕ берёт из `date_added`, а синкает `importer.ResyncYears` — АВТОМАТИЧЕСКИ
(в конце импорта и после прохода прогрева/внешнего воркера, если год реально менялся;
ручной кнопки нет). Год наполняется обогащением ПОСЛЕ импорта, поэтому в поиске он
разрежён, пока обработка коллекции / внешний воркер не наполнят и не синкнут.

### 4. jsdom не делает CSS layout

Vitest + RTL гоняют в jsdom — `getBoundingClientRect()` всегда возвращает
нули, `top` / `width` бессмыслены. Любые layout-чувствительные проверки
(порядок элементов по DOM ≠ ок, нужна реальная позиция; overflow; line-clamp;
sticky) — только Playwright e2e в `frontend/e2e/`. Это записано и в моей
auto-memory как `feedback_visual_layout_testing`.

### 5. Миграции и Seed запускаются автоматически при старте backend

Не нужно отдельно `migrate up`. Логи backend покажут `migrations applied` и
`genres dictionary seeded entries=268` сразу после healthcheck. Если seed не
сработал — справочник пустой и фронт покажет коды жанров вместо имён
(fallback не молчит, видно сразу).

### 6. Каждая миграция — новый номер, прошедшие не править in-place

Текущая верхняя — `0018_edition_fields` (поля уровня издания на `books`;
до неё `0017_works` — таблица `works` + `books.work_id`, см. граблю №15).
Backend хранит applied version в `schema_migrations` (golang-migrate), править
уже-применённые .sql имеет смысл только до push'а.

### 7. PR'ы идут через CI + watcher, merge только когда зелёное

User'ский флоу: `gh pr create` → армировать Monitor (`gh pr view --json
state,mergedAt` + `gh pr checks`) → дождаться `[pr] MERGED`. Это записано в
auto-memory как `feedback_pr_lifecycle_watcher`. Не объявлять задачу
выполненной только потому что CI зелёный — merge ≠ pass.

**Кто мержит:** базово — пользователь (после ревью). Сам мержу (на зелёном CI)
ТОЛЬКО мелочи — бамп версии релиза / правки `.md` / микрофиксы — или по явной
просьбе по конкретному PR. Заметные UI-изменения пользователь смотрит глазами →
их не мержим вслепую. См. auto-memory `feedback_pr_merge_autonomy`.

⚠️ `gh pr checks` сразу после `gh pr create` / force-push может вернуть
`no checks reported` — это значит CI ещё **не зарегистрировался**, а НЕ что
проверок нет / они зелёные. Подождать и перепроверить (дождаться событий
watcher'а), не мержить по этому статусу. (Один раз так чуть не смержили вслепую.)

**Релиз:** бамп `SKRIPTES_VERSION` в `infra/.env.example` + `README.md` → PR →
merge → аннотированный тег `vX.Y.Z` (identity-флаги, см. ниже) → `release.yml`
собирает multi-arch образы в ghcr. Moving-теги `latest` / `{major}.{minor}` /
`{major}` ставятся ТОЛЬКО на stable (без `-` в теге); пре-релизы `-beta` их не
трогают. Текущая версия — **0.6.5** (стабильный); `:latest` опубликован,
в `.env` можно пинить `SKRIPTES_VERSION=latest`.

### 8. Не выдумывать семантику данных

Если в `.inp`-записи попадается неизвестный маркер (например, цифра в поле
`DEL` помимо 0/1, новое поле в `structure.info`, неожиданное значение `EXT`) —
не гадать, что оно значит. Записать defensive-обработку с явным fallback'ом и
TODO «уточнить у пользователя / в спеке». Это `feedback_no_invented_semantics`
в моей auto-memory.

### 9. Чёрно-белая тема — цвет только в контенте

UI монохромный; цвет появляется только в контенте (обложки, фото). Подсказки и
предупреждения — НЕ цветом, а монохромным `callout` (рамка + `bg-muted` +
иконка): `frontend/src/components/ui/callout.tsx`. Не вводить `text-amber/yellow`
и т.п. в UI (исключение — семантический `text-destructive` для ошибок).

### 10. Контролы и типографика

- Мгновенное вкл/выкл → `ui/switch.tsx` (применяется сразу). Checkbox в наших
  разделах = «отметь, потом Сохрани кнопкой» — не путать семантику.
- Контекстозависимое сохранение формы (бар «Есть несохранённые изменения»,
  появляется при изменениях) → общий `frontend/src/components/SaveBar.tsx`.
- Висячее слово на последней строке абзаца (orphan) лечится `text-pretty`
  (Tailwind `text-wrap: pretty`), а не переформулировкой.

### 11. Кэш картинок — три РАЗНЫХ бакета, не путать жизненный цикл

`/cache/covers` (обложки книг), `/cache/posters` (постеры экранизаций),
`/cache/author-photos` (фото авторов) — отдельные каталоги + отдельные
`CoverCache` (свои бюджеты), пути выводятся как соседние к `coverRoot` в
`metadata.New`. Обложки книг **регенерируются** из fb2 (`ServeCoverByID` сам
переизвлекает) → их LRU-эвиктит и сносит «Очистить кэш обложек». Постеры/фото —
**внешний источник, НЕ регенерируются**: свои кнопки очистки
(`/clear-posters`/`/clear-photos`, зануляют `poster_path`/`photo_path` чтобы не
осталось битых `?`) и бюджеты (дефолт 0 = без эвикции). Раздавать всё —
`GET /api/covers/{name}` через `Enricher.ResolveCachedFile` (ищет по трём дирам).
Висячие указатели (файл удалён, ссылка осталась) лечит `Enricher.HealDanglingAssets`
на старте: зануляет + сбрасывает маркер попытки (`adaptations_fetched_at`/
`metadata_fetched_at`), чтобы дозаполнение перекачало. Не клади постеры/фото в
`coverRoot` — их снесёт эвикция обложек.

### 12. Lazy-обогащение автора — single-shot по `metadata_fetched_at`

Био/фото автора тянутся лениво при открытии карточки. Чтобы не долбить
Wikipedia/OL на каждый заход у авторов без биографии: `EnsureAuthorBio` ставит
`metadata_fetched_at` и на промахе, `triggerAuthorEnrichmentAsync` пропускает,
если `EnrichmentFetched`, фронт (`useAuthor`) прекращает поллинг и показывает
fallback по `enrichment_fetched`. Тот же принцип у экранизаций
(`adaptations_fetched_at`). Хочешь ретрай — нужен отдельный TTL-механизм.

### 13. Матчинг автора во внешних — гейт по имени (анти-false-positive)

Поиск автора по имени даёт однофамильцев («Гарднер Лиза» → «Иван Гарднер»).
`metadata/authormatch.go::authorNameMatches` принимает кандидата, только если
совпадают И фамилия, И имя (транслитерация Cyrillic↔Latin, Левенштейн ≤1).
Встроен в `WikipediaProvider.resolveTitle` (bio+photo) и
`OpenLibraryProvider.authorSearch`. Философия — **precision > recall**: лучше
пустая карточка, чем чужие био/фото. Гейт only по фамилии, если имени нет.
Это первый «быстрый слой»; дальше — occupation=writer (Wikidata P106) и якорь
на книгах автора. Уже сохранённые ДО гейта неверные матчи он не чистит —
для фото поможет «Очистить фото» (Q1, перекачает уже через гейт).

### 14. Коды языка нормализуются (lower+trim); видимость контента — и в каталоге

Два связанных правила про язык/видимость:

1. **Нормализация `lang`.** В INPX/fb2 один язык приходит в разном регистре
   (`ru`/`RU`) и с локалями (`ru-RU`, `en_US`), из-за чего он двоился в списке
   языков/фильтре/видимости (скрыть надо было каждый вариант отдельно). Импорт
   нормализует код: `importer.normalizeLang` = lower + trim + срез регионального/
   скриптового субтега (`ru-RU`/`zh-Hans`→`ru`/`zh`) в `processRecord` → PG и Meili.
   Существующие данные чинят миграции `0015` (lower+trim) и `0016` (срез субтега), а
   Meili-индекс — разовый `Importer.ResyncLangs` на старте (гейт
   `app_settings.lang_normalized_vN`, см. `cmd/skriptes/main.go::runOnceLangResync`;
   ключ бампается при смене правил нормализации, чтобы Meili пересинкнулся).
   `catalog.ListLanguages` тоже группирует по `lower(btrim(lang))` (defensive-дедуп).
   Новый код, кладущий `lang`, обязан нормализовать.

2. **Видимость контента течёт в каталог, если не фильтровать.** `/books` режет
   скрытые жанры/языки Meili-фильтром, но карточки автора/серии — отдельные
   PG-запросы. `catalog.GetAuthor`/`GetSeries` принимают `excludeGenres/excludeLangs`
   (из `ContentResolver.Exclusions`, прокинуты хендлерами) и добавляют
   `bookExclusionClause` к спискам книг и счётчику. Добавляешь новый список книг
   (где угодно) — прогоняй через те же исключения, иначе скрытый контент всплывёт.

### 15. Книга (Work) vs издание (fb2-файл) — переход к FRBR, идёт по фазам

Большой рефактор: логическая **книга** (`works`) над физическими **изданиями**
(строка `books` = один fb2-файл). План — `~/.claude/plans/joyful-gliding-lerdorf.md`.
**Phase 1 (сделано) — только фундамент данных, read-path'ы НЕ тронуты:**
- Миграция `0017_works`: таблица `works` (Work-level поля: каноническое
  название, primary_author_id, written_year, series_id/ser_no, ext_ids,
  edition_count) + `books.work_id` FK. Бэкфилл сделал каждую существующую книгу
  **singleton-работой**. Инвариант: у каждой книги есть `work_id`, у каждого
  Work ≥1 издание. Импортёр (`importer/upsert.go::ensureSingletonWork`) держит
  инвариант для новых книг (своя работа на каждую; группировку НЕ делает).
- Миграция `0018_edition_fields`: edition-поля на `books` — `translator`,
  `isbn` (норм. len 10/13), `publisher`, `edition_title`, `src_lang`,
  `src_title`, `src_author_normalized`, `fb2_doc_id`, `page_count`,
  `edition_meta_scanned_at`. Извлекаются локально из fb2:
  `metadata/fb2_provider.go::scanFb2EditionMeta` → `enricher.go::EnsureEditionMeta`
  (single-shot по `edition_meta_scanned_at`, COALESCE) под тумблером «Года»
  прогрева (`prewarm.go`, отдельный маркер от `year_local_scanned_at`).
- ⚠️ work_id ПОКА не читается в read-path'ах (это Phase 3) — `works` есть, но
  каталог/поиск/карточки всё ещё per-fb2. Группировка изданий (Tier 1
  `<src-title-info>`/title+lang + Tier 2 OpenLibrary Work/Wikidata P629), админка
  и ручной split/merge — Phase 2; works-индекс Meili и схлопывание в одну
  карточку — Phase 3; редизайн страницы книги — Phase 4.

## Где что искать (карта по реальным путям)

| Я ищу… | Файл |
|---|---|
| Парсер INPX | `backend/internal/inpx/parser.go` |
| INPX → upsert в PG + Meili | `backend/internal/importer/` |
| Список книг с фильтрами/фасетами/сортировкой | `backend/internal/books/` + `backend/internal/api/books.go` |
| Поисковая логика + re-ranking | `backend/internal/search/` |
| Enrichment (cover, annotation, bio, adaptations) | `backend/internal/metadata/` |
| Год книги (written/edition) + гистограмма | `metadata/fb2_provider.go::FetchYears` · `metadata/enricher.go::EnsureYearLocal` · `catalog/service.go` (year stats) · `frontend/src/components/YearHistogram.tsx` |
| Порядок книг в серии (карточки автора/серии) | `catalog/seriesorder.go::assignSeriesOrder` (каскад `ser_no`→`written_year`→`edition_year`→эвристика `parseTitleOrdinal`→`date_added`, по-серийно all-or-nothing) → `ListItem.SeriesOrder` · фронт сортирует группу по `bySeriesOrder` (`lib/books.ts`), `AuthorPage`/`SeriesPage` · **ленивое дозаполнение года при просмотре** (чтобы каскад не висел на фолбэке): `api/yearenrich.go::triggerSeriesYearEnrichmentAsync` (локально `EnsureYearLocal` → внешне `YearBackfillController.EnrichBooksNow`), гейт `Cover.Prewarm&SyncYears` / `YearEnrichment.Enabled`; поллинг по `year_enrichment_pending` в `useAuthor`/`useSeries` |
| Фоновые операции (админка) — обработка коллекции (fb2) + внешние источники | Локальная джоба: `metadata/prewarm.go` (под-тумблеры covers/annotations/years + интенсивность + авто-ресинк) · Внешние воркеры (зеркало друг друга): год — `metadata/year_backfill.go` (OL `FetchYear`→Wikidata `wikidata_year.go`, учёт `book_year_lookups`); обложки — `metadata/cover_backfill.go` (OL→Google Books `FetchCover` через `Enricher.FetchCoverFrom`, учёт `book_cover_lookups`); био/фото авторов — `metadata/author_backfill.go` (Wikipedia/OL); экранизации книг — `metadata/adaptation_backfill.go` (Wikidata) — **последние два БЕЗ lookups-таблиц**, кандидаты по маркерам `authors.metadata_fetched_at`/`books.adaptations_fetched_at` · Ось «Выкл» (подавить даже lazy, ключ `enrichment_gates`): `settings/enrichment_gates.go` (`EnrichmentGateResolver` — зеркало `ContentResolver`) гейтит 3 lazy-триггера в `api/covers.go`·`catalog.go`·`adaptations.go` (год в эту ось НЕ входит; у года свой ленивый путь для порядка в серии — `api/yearenrich.go`, гейт по `Cover`/`YearEnrichment`, не по `enrichment_gates`) · API: `api/admin_settings.go` (cover-cache=обработка) + `api/admin_year_enrichment.go` + `api/admin_cover_enrichment.go` + `api/admin_bio_adaptation.go` + `api/admin_enrichment_gates.go` · Фронт: `frontend/src/pages/AdminBackgroundPage.tsx` — **аккордеон по ТИПАМ данных** (обложки/аннотации/год/био+фото/экранизации), на каждый тип режим **Выкл/Лениво/Фоном** (производное состояние — раскладывается на gate+локальные тумблеры+внешние воркеры в `applyMode`); год двухпозиционный {Выкл,Фоном} |
| Конвертация формата | `backend/internal/converter/fb2cng.go` |
| OPDS-каталог | `backend/internal/opds/` |
| HTTP-роутер | `backend/internal/api/router.go` |
| TanStack Router маршруты | `frontend/src/router.tsx` |
| Layout / навбар | `frontend/src/components/Layout.tsx` |
| Команда поиска (typeahead) | `frontend/src/components/CommandPalette.tsx` |
| Сайдбар фильтров | `frontend/src/components/FiltersSidebar.tsx` + `GroupedGenresFilter.tsx` |
| Видимость контента (скрыть жанры/языки) | `backend/internal/settings/content.go` (resolver, `Exclusions`=admin∪user) + `backend/internal/api/content.go` + `frontend/src/components/ContentVisibility.tsx` (Admin/Profile). **Исключения применяются И в `/books` (Meili-фильтр), И на карточках автора/серии** (`catalog/service.go::bookExclusionClause` в `GetAuthor`/`GetSeries`) — иначе скрытый контент течёт в каталог (см. граблю №14) |
| Внешний вид (стиль чипов, профиль) | `backend/internal/settings/appearance.go` + `frontend/src/lib/appearance.ts` + `frontend/src/pages/ProfileAppearancePage.tsx` |
| Рантайм-настройки (кэш обложек / контент / appearance) | `backend/internal/settings/` (таблицы `app_settings` + `user_settings`) |
| Языки коллекции (ISO 639-1 → имя) | `backend/internal/catalog/languages.go` |
| Общие UI-примитивы | `frontend/src/components/ui/` (button, switch, callout, sheet, popover, dialog, …) |
| API-клиент (fetch + auth + error toast) | `frontend/src/lib/api.ts` |
| TanStack Query hooks | `frontend/src/lib/books.ts`, `authors.ts`, `series.ts`, `genres.ts`, `content.ts`, `appearance.ts`, и т.д. |
| Docker compose (dev / release) | `infra/docker-compose.yml` / `infra/docker-compose.release.yml` |
| Релиз (CI) | `.github/workflows/release.yml` (триггер — тег `v*.*.*`) |
| TLS + reverse-proxy | `infra/Caddyfile` |

## Что лежит вне репо (но тоже релевантно)

- **Roadmap / план фаз** — `~/.claude/plans/cozy-zooming-popcorn.md`. Что
  сделано, что в работе, что отложено и почему. Перед началом новой фичи
  заглянуть.
- **Auto-memory пользователя** — `~/.claude/projects/<encoded>/memory/`.
  Personal-feedback'и (pre-commit чек, PR-watcher, no-invented-semantics,
  visual-testing-via-Playwright). Они подгружаются автоматически в каждую
  сессию — в этом файле их повторил коротко, чтобы новый чат на чужой машине
  тоже знал.
- **INP/INPX формат** — `~/.claude/projects/<encoded>/memory/inp_format.md`.
  Спецификация полей `.inp` файла (порядок полей берётся из `structure.info`,
  AUTHOR/GENRE — несколько через `:`, имя файла внутри zip = `FILE.EXT`).

## Что НЕ делать

- Не коммитить `infra/.env`, `infra/data/`, `cache/`, `pg_data/`, `meili_data/`
  (всё в `.gitignore`, но напоминание).
- Не амендить чужие коммиты в PR — делать новый коммит (это и user'ское
  предпочтение по pre-commit feedback).
- Не пушить force на main / в чужие PR-ветки.
- Не делать `make clean` без явной просьбы — удаляет volumes со всеми данными
  пользователя.
- Не предлагать «давай ещё фичу» после fix'а — фокус на одной задаче, ждать
  следующего запроса.
