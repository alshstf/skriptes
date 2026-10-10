# CLAUDE.md — гайд для Claude Code (и любого другого AI-ассистента)

Онбординг-указатель для новой сессии: за минуту дать контекст и предупредить о граблях. Полная документация — в
`README.md` (что приложение делает, деплой) и `CONTRIBUTING.md` (структура репо, make-таргеты, pipeline'ы).

**Этот файл грузится в контекст каждой сессии целиком — держать его коротким** (≈18 КБ, не раздувать). Подробности — в
`docs/assistant/` (сами не грузятся, читать нужный файл перед правкой подсистемы):

| Файл | Что там |
|---|---|
| `docs/assistant/gotchas.md` | полные версии граблей ниже (тот же номер раздела): история, прод-кейсы, решения владельца |
| `docs/assistant/code-map.md` | подробная карта «я ищу… → файл» с устройством подсистем |
| `docs/assistant/release-history.md` | история версий: что вошло, PR, что сделать после деплоя |
| `docs/assistant/migrations.md` | что добавила каждая миграция |

Правило правки: в CLAUDE.md — одна-две строки и ссылка, детали — в `docs/assistant/`; версию в CLAUDE.md — только
текущую, историю — в `release-history.md`. (До 2026-10-07 сюда дописывали всё подряд — файл вырос до 260 КБ, ~87 тыс.
токенов в каждой сессии.)

## TL;DR

skriptes — каталогизатор fb2-библиотеки. Go (chi + pgx + raw SQL + golang-migrate) на бэке, React + Vite + TanStack
Router + shadcn/ui на фронте, Postgres + Meilisearch + Caddy в docker compose. Книги лежат на read-only volume и
конвертируются на лету через fb2cng. Текущая версия — **1.39.1**.

## Рабочее окружение

Каждая сессия — в своём worktree (`.claude/worktrees/<имя>`), ветка под PR — там же; в основной чекаут из
worktree-сессии не пишем. `infra/.env` (в `.gitignore`) есть только в основном чекауте — в worktree перед
`docker compose` сделать симлинк: `ln -s /Users/alexandershustov/projects/skriptes/infra/.env infra/.env`.

## Быстрые команды

```bash
# pre-commit самопроверка (запускай ВСЁ перед коммитом — явное user-feedback)
cd backend  && go test ./... && golangci-lint run --timeout=5m ./...
cd frontend && npm run lint && npm run typecheck && npm test && npm run build
cd frontend && npx playwright test        # layout-регрессии; перед ним — свежий build (dist)

# поднять локально из ЭТОГО worktree (context ../backend, ../frontend — чекаут, откуда запущен compose)
cd infra && docker compose build backend frontend && docker compose up -d --force-recreate backend frontend
docker compose exec frontend ls /usr/share/nginx/html/assets/   # хэш index-*.js должен смениться
```

`make help` — полный список таргетов.

## PR, релиз, semver

- **Флоу:** `gh pr create` → вотчер (Monitor) → дождаться MERGED. CI зелёный ≠ смержено. Сразу после create/push
  `gh pr checks` может сказать `no checks reported` — CI ещё не зарегистрировался, это НЕ «зелёный».
- **Вотчер:** событие на КАЖДОЕ терминальное состояние; статусы — `gh pr view N --json state,statusCheckRollup`;
  переменную не называть `status` (zsh read-only). Шаблон — `docs/assistant/gotchas.md` §7; скрипты мержа и
  релиза — `~/projects/plans/skriptes/tools/`.
- **Кто мержит:** на зелёном CI ассистент сам мержит и выпускает минорные (1.x.0) и фикс-релизы (1.x.y). Ждать
  владельца — где он попросил, и для мажора.
- **Документация в каждом PR:** проверить README.md (для пользователя: фичи, env, деплой/compose, внешние API,
  безопасность; не забыть `docker-compose.no-caddy.override.yml`) и CLAUDE.md/`docs/assistant/` (для ассистента).
- **Semver:** публичный DMZ-инстанс сам обновляется по тегу `1` раз в неделю. Ломающее (удалён/изменён env, нужна
  правка compose/Caddyfile/.env/томов, мажор PG/Meili, миграция с ручными шагами, несовместимый внешний API, выросли
  требования к хосту) — только новым мажором и с пометкой `BREAKING`. Новые фичи, env с безопасным дефолтом,
  авто-миграции и ресинки на старте — не ломающие (отметить в release notes). Сомневаешься — спроси владельца.
- **Релиз:** бамп `SKRIPTES_VERSION` в `infra/.env.example` + README + «Текущая версия» здесь + запись в
  `docs/assistant/release-history.md` → PR → merge → аннотированный тег `vX.Y.Z` → `release.yml` (multi-arch в ghcr).
  Moving-теги `latest`/`X.Y`/`X` — только на stable. `infra/.env.public.example` держит `SKRIPTES_VERSION=1`.
- **Миграции:** верхняя — `0057_author_lifetime`. Номер — на момент МЕРЖА (параллельные ветки берут один номер; кто
  мержится вторым — перенумеровывается: golang-migrate молча пропустит меньший номер). Применённые не правим.
  Разовое преобразование данных без смены схемы — идемпотентный шаг на старте (гейт в `app_settings`), не миграция.

## Грабли (выжимка; полностью — `docs/assistant/gotchas.md`, те же номера)

1. **Бандл фронта не обновился** после `docker compose build` — собран другой чекаут. Собирай из worktree с
   правками; не помогло — `--no-cache frontend`.
2. **Жанры:** Meili отдаёт `fb2_code`, имена — `useGenreMap()`; стиль чипов — `genreChipClass(useGenreChipStyle())`.
   Алиасы кодов — `genres/aliases.json` (гейт `genre_aliases_merged_vN` — бампать). Плашка сигналов — общий `BookMeta`.
3. **`date_added` ≠ год написания.** `books.written_year` (fb2 → OL → Wikidata, правила правдоподобия
   `metadata/work_years.go`: опечатки — не раньше рождения автора, год Фантлаба, заглушки < 1450, #465) vs
   `books.edition_year` (год издания, только справочно). Meili `year` = written_year, синкается автоматически.
4. **jsdom не считает layout** — позиции/overflow/sticky проверять только Playwright (`frontend/e2e/`).
5. **Миграции и seed жанров** применяются сами на старте backend.
6. **Миграция — новый номер** (см. «PR, релиз» выше).
7. **PR и релиз** — см. раздел выше.
8. **Не выдумывать семантику данных:** неизвестный маркер в INPX/fb2 — defensive-обработка + вопрос владельцу.
9. **UI монохромный**, цвет — только в контенте; подсказки — `ui/callout.tsx`, не цветом (исключения:
   `text-destructive`, жёлтая ★ избранного).
10. **Контролы:** мгновенное вкл/выкл — `ui/switch`; checkbox = «отметь и Сохрани»; бар несохранённого — `SaveBar`;
    висячее слово — `text-pretty`.
11. **Четыре кэша картинок** — `/cache/covers` (регенерируются из fb2), `/cache/posters`, `/cache/author-photos` и
    `/cache/award-logos` (внешние, не регенерируются). Экранизации — белый список P31; постеры — TMDB → Commons P18.
12. **Lazy-обогащение автора** — single-shot по `metadata_fetched_at` (и `adaptations_fetched_at`); ретрай — только
    отдельным TTL-механизмом.
13. **Матчинг автора во внешних — precision > recall** (`authormatch.go`, `candidate_policy.go`, `namesake.go`,
    `name_equivalents.go`; трасса — `skriptes-explain`). Любая правка поиска — приёмка сухим прогоном по 1000 самым
    известным авторам (урок 1.19.0: очистили био Достоевского).
14. **Коды языка** нормализуются (lower+trim+срез субтега; `src_lang` — только через `langcode.Canonical`).
    Скрытый контент режется И в Meili-фильтре `/books`, И в PG-списках карточек (`bookExclusionClause`) — новый
    список книг прогоняй через те же исключения.
15. **Работа (`works`) над изданиями (`books`).** Группировка `metadata/work_grouper.go` (Tier-1 локально, Tier-2
    внешний, гейты против склеек — §15 в gotchas), ручные merge/split (якорь не выносится; merge зовёт
    `reassignWorkUserData` до GC), авторы работы — `internal/workauthors`. Индексы Meili: `books` (OPDS) и `works`
    (веб); меняешь `workDoc`/`workDocSelect` — бамп `WorksIndexSchemaVersion`. `/works/{id}` ≠ `/books/{id}`.
16. **★-избранное книг = служебная полка** `user_collections.kind='favorites'`; таблицы `favorites` нет. Авторы и
    серии — подписка (колокольчик), не избранное.
17. **Два рейтинга:** внешний (LIBRATE → веб от 5 голосов; иконка `Globe`; рейтинг автора — среднее пяти лучших
    работ, Фантлаб первым в шкале LIBRATE) и читательский (оценки 1–5 инстанса, `BookHeart`). Не путать.
18. **iOS safe-area — системно:** утилиты `pt-safe`/`pb-safe`, оверлеи на shadcn-примитивах получают инсеты сами;
    новый fixed/sticky у края экрана — добавить инсет. Хэдер отделён тенью.
19. **Ручные правки метаданных материализуются в колонки** (`metadata_overrides` + `OverrideController`): поля,
    которые перетирает импорт, ре-применяет `ReapplyAfterImport`; recompute-пути гейтятся по наличию правки.
    Контролы правки на карточках — через `useCanEdit()` (админ + режим «Править», `lib/editMode.ts`, #444), не по роли.
20. **Транзиент ≠ «не найдено»:** 404 → `ErrNotFound`, 429/400/403/5xx → `ErrUpstream` (`statusErr`). Все HTTP-клиенты
    обогащения — через `SourceHTTPClient` (прерыватель по хосту, `ErrSourcePaused` пропускается молча).
21. **Воркеры с lookups-таблицей** выбирают только кандидатов, которых пора спросить, — в SQL (`dueCond`/`dueArgs`),
    не фильтром в Go. Ядро сначала, хвост реже; уже известное (QID, native, skipped) не переспрашиваем.
22. **Тёзки:** автор = (имя, уточнение `[…]` из INPX); прежняя запись — наследнику; `#NNN` наружу не показываем.
    Дубли авторов — ручное слияние с памятью (`author_merges`). `series.kind`: NULL — цикл, `'multi'` —
    межавторская (#448), `'publisher'` — издательская одного автора (#468); на карточках книги — только цикл.
23. **Фоновые горутины** — только через `metadata.Go`/`spawn`, не голый `go` + `context.Background()` (иначе пишут в
    закрытый пул на остановке); подробно — `docs/assistant/code-map.md`, «Фоновые горутины».
24. **Премии — только белый список владельца** (`awards/catalog.go`, `manual.json` — руками раз в год). Новую премию или
    номинацию — только после его проверки (`~/projects/plans/skriptes/awards-dossier.md`); пометок не ставим.

## Где что искать (коротко; подробно — `docs/assistant/code-map.md`)

| Я ищу… | Файл |
|---|---|
| Парсер INPX / импорт в PG + Meili | `internal/inpx/parser.go` · `internal/importer/` (`runImportLoop`, `InpxWatch` в `main.go`) |
| Список книг, фильтры, фасеты, сортировка | `backend/internal/books/` + `api/books.go` |
| Поиск и пересортировка | `books/service.go`, `books/author_query.go`, `books/popular_matches.go` |
| Обогащение (обложки, год, био, экранизации, известность) | `internal/metadata/` (`*_backfill.go`, `*_provider.go`, `fantlab.go`, `wikidata_*.go`) |
| Фоновые операции в админке | `frontend/src/pages/AdminBackgroundPage.tsx` + `api/admin_*.go` |
| Год книги, гистограмма | `metadata/fb2_provider.go`, `metadata/enricher.go::EnsureYearLocal`, `catalog/service.go`, `YearHistogram.tsx` |
| Порядок книг в серии | `catalog/seriesorder.go::assignSeriesOrder` |
| Состав сборников (#388) | `metadata/contents.go`, `books/contents.go`, `components/CompilationContents.tsx` |
| Веб-ридер (оверлей, тема, перелистывание) | `pages/ReaderPage.tsx`, `public/foliate-reader.js`, `lib/readerSettings.ts`; foliate вендорен с патчем — `skriptes:` в `public/foliate/paginator.js` |
| Конвертация формата | `backend/internal/converter/fb2cng.go` |
| Send-to-Kindle | `api/kindle.go`, `email/sender.go`, `lib/kindle.ts` |
| OPDS | `backend/internal/opds/` |
| Роутер, остановка процесса | `api/router.go`, `metadata/lifecycle.go` |
| Маршруты фронта, layout | `frontend/src/router.tsx`, `components/Layout.tsx`, `MainNav.tsx` |
| Поиск-подсказки (Cmd+K, hero) | `components/CommandPalette.tsx`, `lib/suggest.ts`; авторы/серии — `catalog/suggest.go` |
| Главная | `pages/HomePage.tsx`, `lib/home.ts`, `history/service.go` |
| Авторы (список, фильтры, известность) | `pages/AuthorsPage.tsx`, `catalog/authors_list.go`, `catalog/author_stats.go`, `importer/author_renown.go` |
| Премии | `internal/awards/`, `api/awards.go`, `pages/AwardsPage.tsx`, `components/AwardBadges.tsx` |
| Жанры, полки, подборки, умные полки | `pages/ShelvesPage.tsx`, `internal/collections/` (`smart.go`), `books/presets.go`, `components/SmartShelves.tsx` |
| Видимость контента (скрытые жанры/языки) | `settings/content.go`, `api/content.go`, `components/ContentVisibility.tsx` |
| Языки, язык оригинала | `catalog/languages.go`, `metadata/src_lang_backfill.go`, `internal/langcode` |
| Настройки (app/user) | `backend/internal/settings/` |
| UI-примитивы, API-клиент, query-хуки | `frontend/src/components/ui/`, `lib/api.ts`, `lib/*.ts` |
| CSP фронта | `frontend/nginx-security-headers.conf` (inline-скрипты запрещены; e2e под CSP) |
| Docker compose, Caddy, публичный деплой | `infra/docker-compose*.yml`, `infra/Caddyfile`, `infra/Caddyfile.public`, `infra/.env.public.example` |
| Метрики | `backend/internal/metrics` (отдельный сервер `SKRIPTES_METRICS_ADDR`) |
| Postgres в тестах | `backend/internal/testpg` (своих `postgres.Run` не заводить) |
| Релиз (CI) | `.github/workflows/release.yml` |

## Вне git

- **Планы** — `~/projects/plans/skriptes/` (приватный `alshstf/plans`; выполненные — в `archive/`). Бэклог — открытые
  issues + шапки планов в корне. Перед новой фичей — заглянуть туда.
- **Auto-memory пользователя** — `~/.claude/projects/<encoded>/memory/` (pre-commit, вотчер PR, no-invented-semantics,
  визуальные проверки Playwright, формат INPX `inp_format.md`).

## Что НЕ делать

- Не коммитить `infra/.env`, `infra/data/`, `cache/`, `pg_data/`, `meili_data/`.
- Не амендить чужие коммиты, не пушить force в main и чужие ветки.
- Не делать `make clean` без явной просьбы (удаляет volumes с данными).
- Не предлагать «ещё фичу» после фикса — ждать следующего запроса.
