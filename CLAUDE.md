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

**Обогащённая плашка книги** (плотная строка сигналов, как у авторов): общий
компонент `frontend/src/components/BookMeta.tsx` (приём — структурный `BookMetaFields`,
поэтому годится и `BookListItem`, и `CollectionBook`) — год · 🌐 внешний рейтинг (Tooltip:
источник) · 📖 оценка читателей · 🎬 экранизация · язык · ✓/N% чтение · ★ избранное.
Используется в `BookListItem` (автор/серия), `BooksPage::BookCard` (/books) и `ShelvesPage`
(полки). НЕ-user сигналы (рейтинги/источник/экранизация) гидрирует `books.WorkMeta(ctx,
pool, workIDs) → map[workID]ListMeta` по work_id — зовут `books.HydrateListMeta` (для
`[]books.ListItem`: ListWorks + `catalog.GetAuthor/GetSeries`) и `collections.ListCollectionBooks`
(полки, свой DTO). User-сигналы (★/чтение) — в api-слое: `hydrateUserListMeta`
(`[]books.ListItem`) и `hydrateUserCollectionMeta` (`[]CollectionBook`), оба через
`history.FavoriteWorkSet` + `WorkReadStatusSet`. Suggest/Cmd+K плашку НЕ обогащаем
(минимальная, latency-sensitive).

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

Текущая верхняя — `0028_metadata_overrides` (`metadata_overrides` — локальные ручные
правки метаданных каталога, только админ; см. граблю №19); до неё `0027_external_rating_lookups`
(`book_external_rating_lookups`); `0026_external_rating` (`books.external_rating`/`_source`/`_count` —
внешний рейтинг из сети, см. граблю №17); до неё `0025_rating_prompts` (`reads.acquired_at` +
`book_rating_prompts` для отложенных запросов оценки); `0024_book_ratings`
(пользовательские оценки книг), `0023_favorites_collection` (★-избранное → служебная
полка, граблю №16), `0022_feed_dismissals`, `0021_collections`, `0020_genre_favorites`,
`0019_book_work_lookups` (граблю №15). Backend хранит applied version в
`schema_migrations` (golang-migrate), править уже-применённые .sql имеет смысл только
до push'а.

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
трогают. Текущая версия — **1.3.10** — iOS safe-area для ВСЕХ оставшихся оверлеев
(исчерпывающий аудит fixed/sticky): Cmd+K палитра (моб. `top-4`→safe), тосты (sonner
top-right), `SaveBar` (`sticky bottom-0`→home-indicator), ридер-тулбар, `Dialog` (`max-h`+
overflow), Radix-поперы (`collisionPadding` из `lib/safeArea.ts`). Все оверлеи на shadcn-
примитивах → новый получает safe-area автоматически (грабля №18). До неё **1.3.9** — iOS-
добивка: hero-поиск Главной просвечивал ПОВЕРХ
хэдера при скролле — враппер был `relative z-40` всегда (stacking-контекст выше хэдера
z-20); `z-40` теперь условный (только при открытом дропдауне — над скримом z-30), неактивен
→ `z-auto`, уезжает под хэдер как остальной контент (`HomePage::HeroSearch`). До неё
**1.3.8** — iOS PWA/Safari: **системная safe-area** для всех
край-оверлеев (хэдер/дроверы Sheet/sticky-саб-бары/баннер — единые `pt-safe`/`pb-safe`,
вместо `env()` ad-hoc, отсюда баги «в случайных местах») + **тень-сепарация хэдера**
(он, контент и боди — один цвет `--background`, граница 10% невидима → при скролле
«сливался»; box-shadow надёжнее backdrop-blur на iOS). Грабля №18. До неё **1.3.7** — два
каталожных фикса: (1) **админ-кнопка «Сбросить
неудачные попытки»** обогащения (год/рейтинг/обложки) — `ResetFailedLookups` чистит
`not_found`/`error` из `book_*_lookups`, чтобы воркеры перепроверили книги после улучшения
поиска (`POST /admin/{year-enrichment,cover-enrichment,external-rating}/reset-failed`,
кнопки в «Фоновых операциях»); (2) **локализация `works.title` на язык библиотеки**
(`metadata/work_title.go`): при слиянии «оригинал+перевод» каноникой могло стать иноязычное
издание → карточка (`COALESCE(w.title,b.title)`) и works-поиск были английскими при русских
изданиях; `dominantLang`+`recomputeWorkTitles` переписывают `works.title`/`normalized_title`
на издание в доминирующем языке (группировка/ручные merge-split + разовый backfill
`runOnceWorkTitleLocalize` за гейтом `work_title_localized_v1` + ресинк works-индекса);
`visibleWorkEditionID` предпочитает издание-якорь — обложка/lang совпадают с заголовком
(грабля №15). До неё **1.3.6** — внешнее обогащение для переводных книг (по докам
сервисов): (1) осмысленный `User-Agent` для OL/GB через http-transport (`metadata/
httpclient.go`) — анонимный Go-UA OpenLibrary троттлит (отсюда `context deadline`); OL-
клиент 20с; OL covers RPM по док-лимиту 100/IP/5мин → 18 (кламп в `cover_backfill`);
(2) GB API-ключ из env `SKRIPTES_GOOGLE_BOOKS_API_KEY` (`GoogleBooksProvider.WithAPIKey`,
обязателен — без него GB отдаёт 429); (3) для переводных книг (есть `books.src_title`)
внешний поиск идёт по ОРИГИНАЛУ — `src_title` + латинский автор (`src_author_normalized`
∥ транслит) + `src_lang` (`metadata/external_query.go::buildExternalQuery`, в cover/year/
external_rating воркерах) — OL/GB по русскому переводу + кириллице давали 0 совпадений.
До неё **1.3.5** — фикс отдачи обложек: карточка/издания/Главная
грузят обложку по регенерирующему `/api/covers/book/{editionId}` (`ServeCoverByID`,
переизвлекает из fb2) вместо by-name `/api/covers/{cover_path}` (`ResolveCachedFile`, не
регенерит) — обложки переживают «Очистить кэш»/LRU-эвикцию (раньше 404 → плейсхолдер на
популярной коллекции). До неё **1.3.4** — три визуальных фикса: отступ ser_no-префикса в
списке серии (`BookListItem` `min-w` — год-как-номер не упирается в название); iOS
PWA standalone — хэдер не лезет под статус-бар (`env(safe-area-inset-top)`); iOS Safari —
сплошной `bg-background` у хэдера (был `/95`+blur, контент просвечивал при скролле). До
неё **1.3.3** — фикс порядка книг в серии: `assignSeriesOrder`
переведён на ser_no-каркас с вставкой без-номерных книг по году в пропуск
(`assignBySerNoBackbone`) вместо all-or-nothing каскада, который при дырявом `ser_no`
ронял серию на `date_added` (см. карту, строку «Порядок книг в серии»). До неё
**1.3.2** — фикс таймаута сортировки в разделе «Авторы»
(`sort=rating`/`book_count`/`reader_rating` падали в 500 на большой библиотеке —
`ListAuthorsFiltered` переведён на двухфазный запрос: страница по фильтрам+сортировке+
LIMIT, потом богатые подзапросы только для ≤Limit строк; `handleListAuthors` логирует
ошибку + таймаут 5→15с). До неё **1.3.1** — редизайн карточки книги: компактная строка
сигналов (`CardSignalRow`: год · 🌐 рейтинг · 📖 оценка · язык именем), блок «Моё»
(`MyBlock`: оценка + переключатель «прочитано»), тех-детали под раскрывашкой
«Детали файла» (`FileDetails`, размер в КБ/МБ — `lib/format`), сворачивание
аннотации (`ExpandableText`), «Детали файла»/«На полку» в шапке у обложки +
полноширинная панель оценок как разделитель «шапка/контент», мобильная раскладка
действий по приоритету («Читать» крупно с текстом; `DownloadMenu`/`SendToKindleButton`
получили `showLabel`). До неё **1.3.0** — крупный заход по UX каталога: OPDS снова
доступен через домен + канал приобретения (граблю №17/OPDS); **внешний рейтинг**
(LIBRATE∪web, иконка `Globe`) + фоновое онлайн-обогащение из Google Books/OpenLibrary
(воркер `external_rating_backfill`, миграции 0026/0027); **UX авторов** (фильтры в
URL — переживают back; источник рейтинга в тултипе; агрегаты на карточке автора);
**обогащённая плашка книги** во всех списках (/books, автор, серия, полки): год · 🌐
рейтинг · 📖 оценка читателей · 🎬 экранизация · язык · ✓/N% чтение · ★ (`BookMeta` +
`books.WorkMeta`); **drag-and-drop перенос книг между полками** (`@dnd-kit`,
мышь/тач/клавиатура). До неё **1.2.1** — полировка рейтингов (иконки `Landmark`/
`BookHeart` вместо звезды; заголовок Главной → «Skriptes»). До неё **1.2.0** —
**пользовательские оценки книг** — см. граблю
№17: оценка 1–5 на карточке работы (`book_ratings`, миграция 0024); блок «Оцените
прочитанное» на Главной с отложенными запросами по приобретению (`reads.acquired_at`
+ `book_rating_prompts`, миграция 0025); фильтр/сортировка «по оценке читателей» в
Авторах). До неё **1.1.0** — ★-избранное книг = служебная полка
`user_collections.kind='favorites'`, унифицировано с полками, миграция 0023 (граблю
№16); подписка на авторов/серии — иконка-колокольчик; личные полки в `/shelves`. До
неё **1.0.0** — первый
мажорный; **РЕДИЗАЙН навигации и разделов**: новая Главная `/` — hero-поиск
(бывший Cmd+K, нечёткий typeahead) +
блоки «Продолжить чтение» и «Новинки по подпискам» (по избранным авторам И сериям,
× скрывает из ленты); раздел **Авторы** `/authors` (список с фильтрами: жанры,
языки, годы активности, экранизации, библ. рейтинг, избранное); **Жанры** `/genres`
(избранное жанров + личные полки/коллекции, «Добавить на полку» на карточке);
горизонтальная навигация в хэдере + бургер на мобиле (пункта «Главная» нет — это
логотип). Новые таблицы: `user_favorite_genres`, `user_collections`/
`user_collection_books`, `feed_dismissals`. Модель «книга → издания» (works) из
0.9.x — без изменений; 0.9.2 — якорное издание + мини-обложки + фикс серии после
split; 0.9.0 — ручной merge/split; 0.8.0 — works-индекс Meili + `/works/{id}`);
`:latest` опубликован, в `.env` можно пинить `SKRIPTES_VERSION=latest`.

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
**Phase 2 (сделано) — джоба группировки + админка + ручной split/merge:**
- `metadata/work_grouper.go`: `WorkGrouper`/`WorkGroupController` (клон
  `year_backfill.go`). Работает **по автору** (blast radius = 1 автор, НИКОГДА
  не сливает разных primary-авторов). ⚙️ **Tier-1 и Tier-2 РАЗВЯЗАНЫ** (после
  0.9.0): `Run` чередует быстрый `sweepTier1` (Tier-1/1.5, без сети, по всей
  коллекции до исчерпания) и `crawlTier2Batch` (внешний, ОДИН батч/итерация,
  rate-gated) с приоритетом Tier-1. Раньше Tier-2 был вшит в каждого автора и
  тормозил весь проход до ~RPM (на 462k — дни); теперь Tier-1/1.5 догруппирует за
  минуты, Tier-2 ползёт в фоне отдельно. `RunOnce`/тесты — `drainAll`. Кандидаты
  Tier-2: `fetchTier2Authors` (singleton-работа + due по `book_work_lookups`).
  Tier-1 (без сети, union-find): дубли
  `(normalized_title, lang)` + `fb2_doc_id` + перевод↔оригинал/переводы между
  собой через `src_*` + **Tier-1.5** `(series_id, ser_no)` (один том серии ⇒ одна
  работа — ловит разно-названные переводы без src-title-info; гейт точности: бакет
  пропускается при ≥2 разных непустых `src_title`). ⚠️ применяется только к
  кандидатам/переобрабатываемым авторам — существующие дубли чинятся подсказками/
  ручным merge (см. ниже). Tier-2 (opt-in, rate-gated, `book_work_lookups` TTL):
  внешний Work ID — `WorkKeyResolver` на OL (`/isbn/.json`→work, иначе
  title+author за гейтом `authorNameMatches`) и Wikidata (`resolveBookQID`→QID).
  Кандидаты: `work_scanned_at IS NULL` (фолбэк — ещё `edition_meta_scanned_at NOT
  NULL`). apply транзакционно: каноника = work с большинством членов (тай → min
  id), GC опустевших works, пересчёт `edition_count`/`written_year`/`series`,
  `ext_ids` += work_key. Ручные `SplitEditions`/`MergeWorks` (стабильны, т.к.
  scanned-книги не переобрабатываются).
- Настройки `settings.WorkGroupingConfig` (ключ `work_grouping`, зеркало
  year/cover). API `api/admin_work_grouping.go` (`/admin/work-grouping`
  GET/PUT/run/stop + `/admin/works/split`,`/merge`). Фронт — секция
  «Группировка изданий» в `AdminBackgroundPage` (Выкл/Фоном, источники Tier-2,
  scope, coverage), хуки в `lib/admin.ts`. main: `workGroupCtl` в `SettingsDeps`.
- **Ручные merge/split + подсказки НА КАРТОЧКАХ (админ), не из админки**
  (после 0.8.0): хуки `useMergeWorks`/`useSplitEditions` (`lib/admin.ts`,
  инвалидируют каталожные кэши + тост). `components/MergeSuggestions.tsx` —
  read-only подсказки на карточке серии/автора: `computeMergeSuggestions`
  (`lib/books.ts`) группирует загруженные книги по `ser_no` (>0) → если ≥2 разных
  `work_id` → плашка «Объединить?» (каталог несёт `ListItem.WorkID`/`ser_no`,
  отдельный эндпоинт НЕ нужен). `components/MergeWorksDialog.tsx` — ручной выбор
  ≥2 работ → merge (серия/автор). `components/SplitEditionsDialog.tsx` — split
  изданий из секции «Издания» карточки книги. **ЯКОРНОЕ издание выносить нельзя**
  (title-derived: его `normalized_title` == названию работы; тай/fallback → min id;
  ровно одно издание — якорь, `EditionRef.is_anchor` из `books.queryEditions`).
  Якорь залочен; выбор только среди НЕ-якорных: один не-якорь (часто при 2
  изданиях) → подтверждение без выбора, несколько → чек-лист. Бэкенд-защита:
  `WorkGroupController.SplitEditions` отклоняет вынос якоря (`metadata.ErrSplitAnchor`
  → 400; SQL якоря синхронен с `anchorEditionID`). Показывает СОБСТВЕННЫЕ
  название/серию издания (`EditionRef.title`/`series` из `books.title`/`series_id`)
  — видно «чужое» издание после ошибочного merge. ⚠️ `recomputeWorkAggregates` пересчитывает edition_count/written_year/
  series **авторитетно** из текущих живых изданий (а не set-if-null) — иначе
  после split у работы оставались серия/год вынесенной книги. Мини-обложки
  изданий — `BookCover placeholder="mini"` (иконка без названия; текст на 48px
  нечитаем).
  `components/MergeIntoWorkDialog.tsx` — на карточке книги «Объединить с другой
  книгой…»: поиск целевой работы через `useSuggest` (works), merge с `target` =
  ТЕКУЩАЯ работа (она выживает, URL карточки не ломается) — для дублей вне общей
  серии/автора. Все компоненты сами скрываются у не-админа (`useMe().role`).
  merge/split детачнуто синкают поиск через `syncSearchAfterManual` (ResyncWorkIDs
  + works-индекс).
**Phase 3 (сделано) — поиск/список схлопываются по работе (Meili distinct):**
- `bookDoc.WorkID` + `distinctAttribute=work_id` на индексе `books`
  (`importer/index.go`) → OPDS отдаёт ОДНО издание на логическую книгу
  (представитель — самое релевантное издание). Веб-список/Cmd+K с Phase 6
  переехали на отдельный индекс `works` (точные фасеты), books-индекс — для OPDS.
- `Importer.ResyncWorkIDs` (зеркало `ResyncLangs`) синкает `work_id` в Meili.
  `Importer.ConfigureIndex` (экспортирован) применяет настройки индекса на КАЖДОМ
  старте (`runOnceWorkIDResync` в `main.go`) — иначе на стабильном деплое без
  нового inpx `distinct` не включился бы (configureIndex живёт только в Run).
  One-shot гейт `app_settings.work_id_synced_v1`. Группировка после прохода с
  merge синкает `work_id` через `WorkIDResyncer` (`work_grouper`→`imp`).
**Phase 4a (сделано) — бэкенд work-centric read-model:**
- `books.Get` отдаёт work-level карточку: `WorkID`, `Editions []EditionRef` (все
  издания работы, открытое — первым), Title/WrittenYear/Series/SerNo и
  Authors/Genres (union) уровня работы; top-level поля (lang/cover/file/size/
  annotation) = ОТКРЫТОГО издания (id в URL) для back-compat и скачивания.
- `catalog.GetAuthor/GetSeries` схлопывают издания по `work_id` (представитель +
  `ListItem.EditionCount`); `series_order` ранжирует работы; счётчики книг и
  read-count — `count(DISTINCT COALESCE(work_id,-id))`. Фронт не менялся (DTO
  аддитивен; на singleton-данных выдача та же).
**Phase 4b (сделано) — редизайн страницы книги (editions UI):**
- `BookDetailPage` (`frontend/src/pages/BookDetailPage.tsx`): при ≥2 изданиях —
  секция «Издания» (`components/EditionRow.tsx`): ПЛОСКИЙ список равноправных
  изданий (никакого «открытого»/выделения — убрано как непонятное), на каждое:
  мини-обложка, язык (`useLanguageMap`), переводчик/издатель/год издания/ISBN/
  размер (формат НЕ показываем — вся коллекция fb2), прогресс чтения per-edition,
  и компактные действия `components/EditionActions.tsx` — «Читать» + одно меню
  «⋯» (скачать форматы + На Kindle), чтобы строки не рябили при многих изданиях.
  Форматы вынесены в `lib/formats.ts` (общие с `DownloadMenu`). Title/год/серия/
  авторы/жанры — уровня работы.
- Обложка/аннотация карточки — work-level: открытого издания, иначе любого
  издания работы (`books.Get`, COALESCE по `id`). work-level favorite/read
  (`history.IsWorkFavorite`/`WorkReadStatus`) + per-edition прогресс
  (`WorkEditionReads` → `EditionRef.reading_fraction`/`is_read`, мёрджит
  `api/books.go::handleGetBook`).
- Бейдж «N изданий»: `BookListItem` + `BooksPage::BookCard`;
  `hydrateCovers` догидрачивает `edition_count` (+ обложку из любого издания).
- ⚠️ Ридер: «К карточке» = `navigate(replace)` на карточку, НЕ
  `window.history.back()` (foliate в iframe плодит свои history-записи →
  back уводил в чужой ридер). `/foliate-reader.html` в PWA
  `navigateFallbackDenylist` (`vite.config.ts`) — иначе SW отдаёт в iframe
  index.html (всё SPA), ридер виснет на «Подготовка…».
**Phase 6 (сделано) — отдельный works-индекс Meili + маршрут `/works/{id}`:**
- **Два индекса.** `books` (1 док/издание, `distinctAttribute=work_id`) остаётся
  ДЛЯ OPDS (скачивание по id издания). Новый `works` (1 док/работа, БЕЗ distinct)
  — для веба: фасетные счётчики считают РАБОТЫ, а не издания. `importer/index.go`:
  `workDoc` + `configureWorksIndex` (searchable title/authors/series; filterable
  genres/lang/year/series_id/author_ids; lang — МАССИВ языков изданий). Популярности
  у works нет (в PG её нет — поле books-индекса; в works = 0).
- **Ресинк works-индекса** (`importer/importer.go`): `ResyncWorksIndex` (полный
  upsert всех живых работ, батчи по id, UNION авторов/жанров/языков подзапросами,
  year = COALESCE(work.written_year, min года изданий)), `UpsertWorksToIndex(ids)` /
  `DeleteWorksFromIndex(ids)` (таргетно). Точки синка: импорт (полный, в конце Run),
  старт (`runOnceWorksIndexSync`, гейт `works_index_synced_v1`, + `ConfigureWorksIndex`
  на КАЖДОМ старте), группировка (`work_grouper` копит touched/deleted → таргетный
  синк через type-assert `WorksIndexSyncer`; GC работ = `DELETE ... RETURNING`),
  год (`year_backfill` drain post-pass — таргетный upsert изменённых работ; ленивый
  `EnrichBooksNow` works НЕ трогает — наполнится на следующем полном ресинке),
  ручные split/merge (`syncSearchAfterManual` — детачнуто: ResyncWorkIDs + works-синк).
- **Веб-путь** (`books/service.go`): `ListWorks`/`SuggestWorks` (индекс `works`,
  id = works.id, обложка+`cover_edition_id`+`edition_count` гидрируются по work_id),
  `GetWork(workID, excl)` = выбрать ВИДИМОЕ представительное издание → `Get(repID)`.
  `handleListBooks`→`ListWorks`, `handleSuggest`→`SuggestWorks` (+ work-level
  is_favorite через `history.FavoriteWorkSet`). OPDS остаётся на `List`/`Suggest`.
- **Скрытие контента на works**: genres — `NOT IN` (жанры уровня работы); язык —
  `lang IN [видимые]` (видимые = вселенная−скрытые, кэш `allLangs` 5 мин), т.е.
  работа видна, если есть издание на видимом языке (мультиязычную работу не прячем
  целиком из-за одного скрытого языка). Вселенная неизвестна → fallback `NOT IN`.
- **Маршрут `/works/{id}`** (основной для карточки): `api/works.go`-нет, ручка
  `handleGetWork` в `api/books.go` (общий хелпер `writeBookCard` с `handleGetBook`);
  router `/works/{id}` без bookGate (видимость решает `GetWork`→404). `/books/{id}`
  остаётся (back-compat: прямые ссылки, возврат из ридера). Фронт: `router.tsx`
  `/works/$id`→`<BookDetailPage mode="work">` (`useBookCard(id,mode)`/`useWork`);
  discovery-ссылки (`BookListItem`, `BooksPage`, `CommandPalette`) → `/works/{work_id ?? id}`
  (catalog кладёт `ListItem.WorkID`, т.к. там ID = издание; в works-выдаче ID и так
  = works.id). Ридер/скачивание/«Читать» — по-прежнему `/books/{editionId}`.
- ⚠️ id работ и изданий ПЕРЕСЕКАЮТСЯ (отдельные sequence) → `/books/{N}` ≠
  `/works/{N}`; поэтому работа-URL — отдельный маршрут, не «трактовать books id как work».
- **`works.title` локализован на язык библиотеки** (`metadata/work_title.go`): при
  слиянии «оригинал+перевод» каноникой могло стать иноязычное издание → заголовок
  карточки (`COALESCE(w.title,b.title)`) и works-индекс были английскими при русских
  изданиях (рассинхрон со списком `b.title` представителя + провал поиска по рус.
  названию). `recomputeWorkTitles` переписывает `works.title`/`normalized_title` на
  издание в `dominantLang` (самый частый язык коллекции) — ТОЛЬКО если такое издание
  есть (иноязычную-без-перевода работу не трогаем). Точки: группировка `apply` +
  ручные merge/split (затронутые работы → в touchedWorks → таргетный ресинк индекса);
  разовый backfill `runOnceWorkTitleLocalize` (гейт `app_settings.work_title_localized_v1`,
  ПОСЛЕ `runOnceWorksIndexSync`) + `UpsertWorksToIndex(changed)`. Представитель карточки
  `books.visibleWorkEditionID` предпочитает издание-якорь (`normalized_title ==
  works.normalized_title`) → обложка/lang/скачивание совпадают с локализованным title.

### 16. ★-избранное книг — это служебная полка, таблицы `favorites` БОЛЬШЕ НЕТ

После унификации (PR #109, миграция `0023`) книжное «избранное» = членство в
служебной коллекции `user_collections.kind='favorites'` (одна на юзера через
partial unique `user_collections_one_fav`). ★ на книге — её one-click шорткат.
Таблица `favorites` **дропнута** — не ищи её, не пиши в неё. Книжный API не
менялся (`POST/DELETE /api/books/{id}/favorite`, поле `is_favorite`); репойнт
только внутри бэка — все читают/пишут favorites-коллекцию:
- `history/service.go` — `AddFavorite` (CTE: ensure favorites-полки + membership),
  `RemoveFavorite`/`IsFavorite`/`IsWorkFavorite`/`FavoriteWorkSet`/`ListFavorites`/
  `FavoritesCount`; `history/persona.go` — источник `FavoriteBooks` для re-ranking;
  `catalog/authors_list.go` — счётчик `fav_books` на `/authors`.
- Служебную полку нельзя переименовать/удалить/создать-дубль:
  `collections.ErrSystemCollection`/`ErrReservedName` → 400 (`api/collections.go`).
  `ListCollections` отдаёт `kind` и закрепляет favorites ПЕРВОЙ.
- Фронт: `Collection.kind`/`BookShelf.kind` (`lib/collections.ts`); ★↔полки
  держат синхронными кросс-инвалидацией (`invalidateFavoriteSide` + `['me','collections']`
  в `useToggleFavorite`). На карточке книги `ShelfSection` исключает favorites из
  чипов «На полках» (её передаёт ★). `/shelves` — favorites закреплена, без удаления.

⚠️ **Авторы/серии — это ПОДПИСКА, не «избранное»** (питает «Новинки по подпискам»;
свои таблицы `favorite_authors`/`favorite_series` — НЕ трогались). Иконка у них —
колокольчик (`Bell`, монохром), а не звезда: `FavoriteButton` (`target!=='book'`),
suggest `FavoriteMark`, hero-дропдаун (`HomePage`), строка автора (`AuthorsPage`).
Книжная ★ — жёлтая звезда (исключение из монохрома, граблю №9).

### 17. ДВА рейтинга — не путать: библиотечный (донор) vs читательский (инстанс)

После фичи оценок (1.2.0) у книги/автора ДВА разных рейтинга:
- **Внешний (НЕ читательский)** — единый «Внешний рейтинг» (иконка `Globe`), объединяет
  два источника с приоритетом показа LIBRATE → web:
  - LIBRATE (донор) — `books.rating` из INPX (рейтинг librusec/flibusta), только из импорта;
  - web — `books.external_rating`/`_source`/`_count` (Google Books/OpenLibrary), миграция
    **0026**, заполняется фоновым воркером `metadata/external_rating_backfill.go` (opt-in из
    админки «Фоновые операции», per-source выбор + охват; учёт попыток `book_external_rating_lookups`,
    миграция **0027**; из включённых источников берётся оценка с бОльшим числом голосов).
  Карточка: `BookDetailPage::externalRatingDisplay` (LIBRATE→«N · библиотека», иначе
  web→«N · Google Books»). Авторы: единый агрегат `external_rating` =
  `max(COALESCE(rating, external_rating))`, фильтр `MinRating`/sort=`rating`/бейдж с `Globe`
  (`catalog/authors_list.go`). ⚠️ `library_rating` в DTO Авторов БОЛЬШЕ НЕТ — теперь
  `external_rating` (float).
- **Читательский (этого инстанса)** — пользовательские оценки 1–5, work-level:
  - `book_ratings(user_id, work_id, rating)` (миграция **0024**); сервис —
    `history/ratings.go` (`SetRating` — авто-проставляет «Прочитана» в tx;
    `WorkRatingAggregate` — средняя+count по инстансу; `UserRating`). На карточке —
    блок «Моё» (`BookDetailPage::MyBlock`: `RatingControl` 1–5 НЕ звезда +
    переключатель «прочитано»); средняя — в строке сигналов (`CardSignalRow`,
    BookHeart-чип). Поля карточки `user_rating`/`rating_avg`/`rating_count`
    (`api/books.go::bookResponse`). API `PUT/DELETE /api/works/{id}/rating`.
  - **«Оцените прочитанное»** на Главной — отложенные запросы: книга «вероятно
    прочитана» = приобретена (Send-to-Kindle/скачивание → `reads.acquired_at`,
    `history.RecordAcquisition`) ≥ задержки назад ИЛИ есть read_signal (отметка
    «Прочитана»/web-fraction≥0.95). Скрытия — `book_rating_prompts` (миграция **0025**):
    `never` (бесповоротно) / `snooze`. `history/rating_prompts.go::RateableWorks`.
    Настройка профиля `settings.RatingPromptConfig` (вкл/выкл + задержка). Kebab
    `RatingPromptMenu` — ТОЛЬКО в ленте Главной (с карточки убран как лишний).
  - Авторы: «оценка читателей» = avg `book_ratings` по работам автора по инстансу,
    без порога (`reader_rating`/`reader_rating_count`, фильтр `MinReaderRating`,
    sort=`reader_rating`).

Иконки развели: ★ (жёлтая звезда) — ТОЛЬКО книжное избранное; `Globe` — внешний рейтинг;
`BookHeart` — оценка читателей; колокольчик `Bell` — подписка на авторов/серии. Онлайн-обогащение
внешнего рейтинга реализовано (воркер `metadata/external_rating_backfill.go`, зеркало
`cover_backfill.go`, секция «Внешний рейтинг» в админке «Фоновые операции»).

### 18. iOS PWA safe-area — СИСТЕМНО (не env() по месту); хэдер отделён тенью

`index.html`: `viewport-fit=cover` + `apple-mobile-web-app-status-bar-style=black-translucent`
⇒ в standalone-PWA на iOS контент full-bleed'ит ПОД системные бары (часы/home-indicator).
Значит **любой `fixed`/`sticky` элемент у края экрана ОБЯЗАН добавить соответствующий
safe-area-инсет**, иначе контент лезет под бар. Раньше инсет ставили `env(...)` ad-hoc
(только хэдер) → дроверы/саб-бары его не получали и ломались «в случайных местах».

Теперь паттерн **единый**: именованные утилиты `pt-safe`/`pb-safe` (`@utility` в
`src/index.css` = `padding-top/bottom: env(safe-area-inset-*)`; на десктопе/в Safari = 0).
Где применены и почему (правишь top-/bottom-anchored оверлей — добавляй так же):
- **Хэдер** (`Layout.tsx`): `pt-safe` + **тень** `shadow-[0_10px_22px_-10px_rgba(0,0,0,0.9)]`.
  Хэдер, контент и боди — ОДИН цвет `--background`, а `--border`=10% white почти невидим
  → при скролле хэдер «сливался» с контентом (юзер читал как «прозрачный»; на деле
  `bg-background` сплошной). Тень даёт глубину — хэдер читается как слой над контентом
  (box-shadow на iOS Safari надёжен, в отличие от backdrop-blur). z-20 (выше саб-баров).
- **Sheet-дроверы** (`ui/sheet.tsx`, ВСЕ usage — nav/мобильные фильтры): `pt-safe pb-safe`
  на боковых (во всю высоту), `pt-safe`/`pb-safe` на верх/низ; close-кнопка
  `top-[calc(env(safe-area-inset-top)+0.75rem)]` (absolute, иначе под баром).
- **Sticky-саб-бары фильтров** (`AuthorsPage`/`BooksPage`): липнут ПОД хэдером →
  `top-[calc(env(safe-area-inset-top)+3.5rem)]` (инсет + высота хэдера h-14). На `top-14`
  уезжали бы ПОД хэдер (который на iOS выше на инсет) и пропадали.
- **Нижний баннер установки** (`InstallPromptBanner`): `pb-safe`.
- **CommandPalette** (`components/CommandPalette.tsx`): на мобиле прижат к верху →
  `top-[calc(env(safe-area-inset-top)+1rem)]` (десктоп — по центру). БЫЛ `top-4` → лез под бар.
- **Dialog** (`ui/dialog.tsx`): центрирован, но высокий мог переполнить под бары →
  `max-h-[calc(100dvh-env(safe-area-inset-top)-env(safe-area-inset-bottom)-2rem)] overflow-y-auto`.
- **SaveBar** (`sticky bottom-0`): `pb-[calc(0.75rem+env(safe-area-inset-bottom))]` (home-indicator).
- **Ридер** (`ReaderPage`, `fixed inset-0`): тулбар `pt-[calc(0.5rem+env(safe-area-inset-top))]`
  (контент iframe ниже — immersive).
- **Toaster** (sonner `top-right`, `main.tsx`): CSS-override в index.css
  `[data-sonner-toaster][data-y-position=top]{top:calc(env(safe-area-inset-top)+1rem)}`.
- **Radix-поперы** (`ui/dropdown-menu`/`popover`/`tooltip`): `collisionPadding={safeCollisionPadding()}`
  (`lib/safeArea.ts` — меряет инсеты проб-элементом, т.к. env() из JS недоступен; 0 на десктопе).
⚠️ Все оверлеи на shadcn-примитивах (Sheet/Dialog/Popover/Dropdown/Tooltip/CommandPalette+Toaster) —
покрыты централизованно: новый оверлей на этих примитивах safe-area получает автоматически.
⚠️ Playwright/Chromium даёт `env(safe-area-*)=0` (как десктоп) — реальные инсеты только на
iOS-устройстве; визуально проверять симуляцией (инъекция `top/padding:47px`) или на айфоне.

### 19. Локальные оверрайды метаданных — МАТЕРИАЛИЗАЦИЯ в колонку, не query-time

Ручная корректура каталога (только админ, глобально): `metadata_overrides` (миграция
**0028**) + `metadata.OverrideController` (`metadata/overrides.go`). Правка **материализуется
в реальную колонку** (`books.*`/`works.*`) — иначе НЕ попадёт в Meili (индексы строятся из
колонок, query-time-join туда не дотянется); бонусом обогащение (`COALESCE` set-if-null) не
перетирает непустое значение. Леджер хранит `original_value` (захват ДО первой правки;
повторная правка его НЕ перезахватывает) → откат + индикатор «изменено». API под
`requireAdmin`: `POST/DELETE /admin/overrides` + `GET /admin/overrides?work_id=` (индикаторы) +
`POST /admin/overrides/revert-all`. Фронт: `useSetOverride`/`useRevertOverride`/`useOverrides`
(`lib/admin.ts`) + `InlineEditableField` — **визуально незаметно по умолчанию** (правят редко,
карточку читают): десктоп — карандаш на ховере у значения, мобила — лонг-тап → action-меню
(«Редактировать»/«Отменить правку»), правка **in-place** (без отдельной панели). Применён к
заголовку (`layout='heading'`, оборачивает `CardTitle`), году в `CardSignalRow`, полям издания
в `EditionRow`/`FileDetails`. Не-админ видит обычный текст. **План —
`~/.claude/plans/cryptic-roaming-turing.md`.**
- **PR1 (сделано):** фундамент + edition-СКАЛЯРЫ (`edition_year`/`isbn`/`publisher`/
  `translator`/`edition_title`) — не индексируются и не перетираются импортом, поэтому
  материализуются прямо в `books.*` без ресинка/ре-апплая/гейтов. Шипает кейс Чарушина
  (`edition_year=1000`).
- **PR2 (сделано):** work-СКАЛЯРЫ `title`/`written_year` — материализуются в `works.*`
  (title → `title`+`normalized_title` через `lower(btrim(regexp_replace(...,'\s+',' ')))` =
  зеркало `importer.normalize`; written_year → +`written_year_source='override'`). Видимость:
  карточка (`COALESCE(w.*,b.*)`) + works-индекс (`UpsertWorksToIndex` детачнуто после
  set/revert) + **списки автора/серии переведены на `COALESCE((SELECT ww.title FROM works ww
  WHERE ww.id=b.work_id), b.title)`** (`catalog/service.go` — коррелированный подзапрос, без
  join/GROUP BY-возни; иначе списки читали `b.title` и оверрайд бы не видели). Гейты
  recompute (`NOT EXISTS metadata_overrides` в written_year-UPDATE `recomputeWorkAggregates`
  + `recomputeWorkTitles`) → группировка/merge/split не перетирают. Импорт `works.*` НЕ
  трогает → ре-апплай не нужен (гейт recompute достаточен).
- **PR3 (сделано):** `ser_no` (правит порядок книги в серии) — то же, что title: `works.ser_no`
  + каталог COALESCE (`b.ser_no` → `COALESCE((SELECT ww.ser_no …), b.ser_no)` в обоих списках,
  т.к. сортировка серии `seriesorder.go` читает `ListItem.SerNo`) + гейт series-UPDATE
  `recomputeWorkAggregates` (`o.field IN ('ser_no','series')`). Не индексируется, ре-апплай не
  нужен. UI: `#N` в строке серии редактируем.
- **PR4 (сделано):** `lang` (edition-СКАЛЯР, но индексируется + перетирается импортом).
  Материализуется в `books.lang` (нормализация lower+trim, зеркало `importer.normalizeLang`,
  грабля №14), помечен `indexed` в `bookScalarFields` → после set/revert детачнутый
  `resyncBookWork` → `UpsertWorksToIndex(work)` обновляет `lang[]` works-индекса (веб-фасет
  `/books`). Импорт перетирает `books.lang` → `OverrideController.ReapplyAfterImport(ctx)`
  (зовётся из `runStartupImport` ПОСЛЕ `imp.Run`): обновляет `original_value` на свежеимпорт.
  значение → ре-материализует оверрайд → таргетный ресинк works-индекса. UI: lang-бейдж в
  `EditionRow` редактируем СЕЛЕКТОМ (`InlineEditableField` `kind='lang'` → `<select>` из
  `useLanguages()`). UI-уточнение: lang редактируем и в `CardSignalRow` карточки (book-level,
  таргет — представляющее издание `book.id`), т.к. секция «Издания» есть только при ≥2
  изданиях; селект отдаёт ВЕСЬ ISO 639-1 (`useLanguageOptions` в `lib/content.ts`, имена через
  `Intl.DisplayNames('ru')`), а не только языки коллекции — мислейбл правят на отсутствующий
  язык. ⚠️ OPDS books-индекс (`lang`) на правке НЕ ресинкается — как и title (PR2): вторично,
  освежается полным импортом. `overrideCtl` создаётся РАНЬШЕ (сразу после `imp`) — нужен
  `runStartupImport` для ре-апплая.
- **PR5 (сделано):** `genres` (M:N, work-level). Жанры карточки/works-индекса = union
  `book_genres` всех живых изданий работы (`queryWorkGenres`/`workDocSelect`), поэтому
  оверрайд набора кодов материализуется в `book_genres` ВСЕХ изданий (одинаковый набор →
  union = набор; читается всеми путями БЕЗ COALESCE — это не `works.*`-колонка). `original_value`
  — per-edition снапшот кодов (точный откат). Импорт (`replaceBookGenres`) перетирает →
  `ReapplyAfterImport` ре-применяет (проход по `field='genres'`: свежий снапшот в `original` →
  ре-материализация → ресинк works-индекса). Диспетчер `SetOverride`/`RevertOverride`:
  `kind='work' && field='genres'` → `setWorkGenres`/`revertWorkGenres` (M:N-хелперы на общем
  интерфейсе `pgxExec` — годятся и для tx, и для пула в ре-апплае). UI: `components/GenresEditor.tsx`
  — чипы + (ховер/лонг-тап) поповер с поиском и мультиселектом (`Command` cmdk) по `useGenres()`;
  `useLongPress` вынесен в `lib/useLongPress.ts`. ⚠️ OPDS books-индекс на правке НЕ ресинкается
  (как lang/title).
- **PR6+ (план):** авторы (M:N — резолв/создание + `primary_author_id` + инвалидация кэшей
  авторов); перенос между сериями.

## Где что искать (карта по реальным путям)

| Я ищу… | Файл |
|---|---|
| Парсер INPX | `backend/internal/inpx/parser.go` |
| INPX → upsert в PG + Meili | `backend/internal/importer/` |
| Список книг с фильтрами/фасетами/сортировкой | `backend/internal/books/` + `backend/internal/api/books.go` |
| Поисковая логика + re-ranking | `backend/internal/search/` |
| Enrichment (cover, annotation, bio, adaptations) | `backend/internal/metadata/` |
| Год книги (written/edition) + гистограмма | `metadata/fb2_provider.go::FetchYears` · `metadata/enricher.go::EnsureYearLocal` · `catalog/service.go` (year stats) · `frontend/src/components/YearHistogram.tsx` |
| Порядок книг в серии (карточки автора/серии) | `catalog/seriesorder.go::assignSeriesOrder`: есть хоть один `ser_no` → **ser_no-каркас** (`assignBySerNoBackbone`: нумерованные на свой номер, без-номерные ВСТАВЛЯЮТСЯ по году `written_year`∥`edition_year` в пропуск — `key = serNoПоследнегоЯкоряСГодом≤ +0.5`; без года → хвост); ни у кого нет `ser_no` → каскад `written_year`→`edition_year`→эвристика `parseTitleOrdinal`→`date_added` (`assignByCascade`, all-or-nothing). Раньше весь каскад был all-or-nothing — при дырявом `ser_no` серия валилась на `date_added`. → `ListItem.SeriesOrder` · фронт сортирует группу по `bySeriesOrder` (`lib/books.ts`), `AuthorPage`/`SeriesPage` · **ленивое дозаполнение года при просмотре** (чтобы каскад не висел на фолбэке): `api/yearenrich.go::triggerSeriesYearEnrichmentAsync` (локально `EnsureYearLocal` → внешне `YearBackfillController.EnrichBooksNow`), гейт `Cover.Prewarm&SyncYears` / `YearEnrichment.Enabled`; поллинг по `year_enrichment_pending` в `useAuthor`/`useSeries` |
| Фоновые операции (админка) — обработка коллекции (fb2) + внешние источники | Локальная джоба: `metadata/prewarm.go` (под-тумблеры covers/annotations/years + интенсивность + авто-ресинк) · Внешние воркеры (зеркало друг друга): год — `metadata/year_backfill.go` (OL `FetchYear`→Wikidata `wikidata_year.go`, учёт `book_year_lookups`); обложки — `metadata/cover_backfill.go` (OL→Google Books `FetchCover` через `Enricher.FetchCoverFrom`, учёт `book_cover_lookups`); внешний рейтинг — `metadata/external_rating_backfill.go` (Google Books `averageRating`/OL `ratings.json` через `FetchRating`, учёт `book_external_rating_lookups`, пишет `books.external_rating`); био/фото авторов — `metadata/author_backfill.go` (Wikipedia/OL); экранизации книг — `metadata/adaptation_backfill.go` (Wikidata) — **последние два БЕЗ lookups-таблиц**, кандидаты по маркерам `authors.metadata_fetched_at`/`books.adaptations_fetched_at` · Ось «Выкл» (подавить даже lazy, ключ `enrichment_gates`): `settings/enrichment_gates.go` (`EnrichmentGateResolver` — зеркало `ContentResolver`) гейтит 3 lazy-триггера в `api/covers.go`·`catalog.go`·`adaptations.go` (год в эту ось НЕ входит; у года свой ленивый путь для порядка в серии — `api/yearenrich.go`, гейт по `Cover`/`YearEnrichment`, не по `enrichment_gates`) · API: `api/admin_settings.go` (cover-cache=обработка) + `api/admin_year_enrichment.go` + `api/admin_cover_enrichment.go` + `api/admin_external_rating.go` + `api/admin_bio_adaptation.go` + `api/admin_enrichment_gates.go` · Фронт: `frontend/src/pages/AdminBackgroundPage.tsx` — **аккордеон по ТИПАМ данных** (обложки/аннотации/год/био+фото/экранизации), на каждый тип режим **Выкл/Лениво/Фоном** (производное состояние — раскладывается на gate+локальные тумблеры+внешние воркеры в `applyMode`); год двухпозиционный {Выкл,Фоном} |
| Конвертация формата | `backend/internal/converter/fb2cng.go` |
| OPDS-каталог | `backend/internal/opds/` |
| HTTP-роутер | `backend/internal/api/router.go` |
| TanStack Router маршруты | `frontend/src/router.tsx` (`/`=Главная, `/authors`=список, `/genres`, `/books`, `/works/{id}`) |
| Layout / навбар | `frontend/src/components/Layout.tsx` + `MainNav.tsx` (горизонт. навигация + бургер; `heroSearch.ts` — поиск в хэдере прячется пока виден hero Главной) |
| Команда поиска (typeahead) | `frontend/src/components/CommandPalette.tsx` (Cmd+K); тот же `useSuggest` (`lib/suggest.ts`) — hero-поиск на Главной |
| Новая Главная (hero + динам. блоки) | `frontend/src/pages/HomePage.tsx` + `frontend/src/lib/home.ts` (фид) · бэк: `history/service.go::ContinueReading`/`SubscriptionFeed`/`DismissFeedItem` (новинки = добавленные ПОСЛЕ подписки на автора/серию; `feed_dismissals` — скрытые) |
| Раздел «Авторы» (список + фильтры) | `frontend/src/pages/AuthorsPage.tsx` + `frontend/src/lib/authors.ts` · бэк: `catalog/authors_list.go::ListAuthorsFiltered` + `api/authors.go` (только авторы с ≥1 видимой книгой; фильтр языка матчит lang∪src_lang). **Фильтры/поиск/сортировка живут в URL-search** (`AuthorsSearch`/`validateSearch` в `router.tsx`, зеркало `/books`) — переживают возврат с карточки автора. Список несёт `external_rating_source` (источник топ-рейтинга, в тултипе); карточка автора (`catalog.GetAuthor`→`queryAuthorMeta`/`queryAuthorLanguages`, `AuthorPage.tsx`) дублирует те же агрегаты — рейтинги/языки/экранизации/годы. Общий формат рейтинга/ярлык источника — `lib/ratingDisplay.ts` |
| Раздел «Жанры» + личные полки | `frontend/src/pages/GenresPage.tsx` + `frontend/src/lib/collections.ts` + `components/AddToShelfDialog.tsx` · бэк: `internal/collections/service.go` + `api/collections.go`; избранное жанров — `history` + `catalog.ListGenres(userID)`. **`/shelves` (ShelvesPage): DnD-перенос книги между полками** (@dnd-kit; Pointer+Touch(long-press)+Keyboard сенсоры) — `useMoveBookBetweenShelves` = add(целевая)+remove(исходная) через те же collection-эндпоинты (для «Избранного» тоже, ★ синкается `invalidateFavoriteSide`) |
| Сайдбар фильтров | `frontend/src/components/FiltersSidebar.tsx` + `GroupedGenresFilter.tsx` (проп `showCounts` — на /authors книжные счётчики скрыты) |
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
