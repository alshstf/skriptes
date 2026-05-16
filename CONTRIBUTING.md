# Разработка skriptes

Документ для тех, кто собирает проект из исходников, прогоняет тесты, или добавляет фичи. Для развёртывания готового релиза смотрите [README.md](README.md).

## Сборка из исходников

```bash
git clone https://github.com/alshstf/skriptes.git
cd skriptes
cp infra/.env.example infra/.env
$EDITOR infra/.env       # минимум — BOOKS_HOST_PATH / INPX_HOST_PATH
make up                  # build + up (использует infra/docker-compose.yml)
make seed-admin EMAIL=me@example.com PASSWORD=secret123 DISPLAY_NAME="Me"
open https://skriptes.localhost
```

`infra/docker-compose.yml` (dev-стек) собирает backend и frontend из исходников. `infra/docker-compose.release.yml` (релизный) подтягивает образы из ghcr.io — используется в README для конечных пользователей и через `make up-release`.

## Структура репозитория

```
backend/                 # Go API + INPX-импортер + enrichment'ы
  cmd/
    skriptes/            # main HTTP-сервер
    skriptes-seed/       # CLI для создания admin-пользователей
  internal/
    adaptations/         # сервис экранизаций (read из book_adaptations)
    api/                 # chi-роутер, handler'ы, middleware
    auth/                # сессии, bcrypt, CSRF
    books/, catalog/     # сервисы книг / авторов / серий / жанров
    config/              # envconfig
    converter/           # обёртка над fb2cng
    db/                  # pgxpool + golang-migrate
      migrations/        # *.sql миграции
    email/               # gomail.v2 для send-to-Kindle
    health/              # /healthz, /readyz
    history/             # views / reads / favorites
    importer/            # оркестрация INPX → PG + Meili
    inpx/                # парсер 0x04-разделённых записей
    kindle/              # CRUD по kindle_targets + send-to-kindle
    metadata/            # обогащение карточек (cover / annotation /
                         #   author bio+photo / adaptations)
  testdata/              # фикстуры (если нужны рядом с пакетами)
frontend/                # React + Vite + TanStack Router + shadcn/ui
  src/
    components/          # переиспользуемые UI-компоненты
    pages/               # страницы (Books, BookDetail, Author, /me, ...)
    lib/                 # React Query hooks + apiFetch
  e2e/                   # Playwright spec'и
infra/
  docker-compose.yml         # dev (build из исходников)
  docker-compose.release.yml # prod (ghcr.io образы)
  Caddyfile                  # reverse-proxy + TLS
  .env.example
  testdata/                  # фикстурный INPX + книжный zip
    test.inpx
    books/fb2-100001-100001.zip
.github/workflows/
  ci.yml                  # PR + push на main
  release.yml             # на тег v*.*.* — multi-arch образы + GH Release
```

## Make-таргеты

```bash
make help              # список всех таргетов

# стек
make up                # поднять весь стек (build + up)
make down              # остановить
make logs              # follow-логи
make ps                # статус контейнеров
make build             # пересобрать образы
make clean             # ⚠ удалить volumes (потеря данных)

# release
make up-release        # поднять из готовых ghcr.io-образов
make down-release      # остановить

# backend
make backend-run       # go run ./cmd/skriptes (без docker)
make backend-test      # go test ./...
make backend-lint      # golangci-lint (та же версия что в CI)
make backend-tidy      # go mod tidy

# frontend
make frontend-install  # npm install
make frontend-dev      # vite dev server (5173)
make frontend-test     # vitest unit-тесты
make frontend-e2e      # Playwright e2e (chromium на vite preview)
make frontend-lint     # eslint + tsc

# общие
make test              # backend-test + frontend-test
make lint              # backend-lint + frontend-lint

# admin
make seed-admin EMAIL=me@example.com PASSWORD=secret123 [DISPLAY_NAME="Me"]
```

## Тестовая фикстура

`infra/testdata/test.inpx` — фикстура с 20 записями в 4 inp-файлах. Используется backend-тестами; копия живёт в `backend/internal/inpx/testdata/test.inpx` (md5 совпадает). При изменении синхронизируйте обе копии.

`infra/testdata/books/fb2-100001-100001.zip` — единственная физическая FB2 в репо (Анна Каренина, public domain). Используется для smoke-теста экранизаций — у этой книги 37+ записей P144 в Wikidata, и она же — единственная книга в фикстуре с реальным fb2-телом (остальные ссылаются на `_lost` архивы или на отсутствующий `fb2-749080-749080.zip`).

Для dev-окружения (docker compose):

```bash
cp infra/testdata/test.inpx           infra/data/inpx/
cp infra/testdata/books/*.zip         infra/data/books/
docker compose restart backend
```

`infra/data/` — gitignore'нутая монтируемая директория.

## Тесты

### Backend

Юнит-тесты (`go test -short`) не требуют ничего внешнего. Интеграционные используют `testcontainers-go` — поднимают временные `postgres:17-alpine` и `getmeili/meilisearch:v1.13` в Docker, для них нужен запущенный Docker daemon.

```bash
make backend-test                              # все тесты, включая интеграционные
go test -short ./...                           # только юниты, быстро
go test ./internal/metadata/ -v -run TestX     # фокус на конкретный пакет
go test ./internal/adaptations/ -count=1       # пересоздать testcontainers (без кэша)
```

**Лицензионное напоминание:** перед push'ем всегда запускайте `make backend-lint` локально — CI прогоняет golangci-lint с тем же конфигом, и красный CI после push — это бесполезный цикл.

### Frontend

```bash
make frontend-test     # vitest (jsdom unit-тесты, ~30)
make frontend-e2e      # Playwright (chromium, ~22)
make frontend-lint     # eslint + tsc --noEmit
```

Playwright использует **собранный билд** (`vite preview` на 4173), а не dev-server. Из-за этого `npm run build` нужен **перед** прогоном e2e после изменений в коде. Если e2e падают «element not found», первое что проверить — пересобран ли фронт.

API в e2e замоканы через `page.route()` — backend поднимать не нужно. Базовый набор стабов и фикстуры — в `frontend/e2e/_fixtures.ts`.

### Что в jsdom не работает

Vitest gegen использует jsdom — он **не считает CSS layout** (нет `getBoundingClientRect` с реальной геометрией, `scrollHeight = 0`). Тесты на line-clamp / overflow / ResizeObserver пиши в Playwright, не в vitest.

## Как работают ключевые системы

### Импорт INPX

Backend на старте `os.ReadDir(SKRIPTES_INPX_ROOT)` ищет `*.inpx`, для каждого:

1. sha256 файла; если совпадает с `collections.last_inpx_hash` → no-op
2. парсит INPX (zip с `version.info` / `collection.info` / опционально `structure.info` / `*.inp`)
3. для каждой записи в `.inp` (поля разделены `0x04`, записи `\r\n`):
   - upsert author / series / genre с дедупом по нормализованному ключу
   - upsert book с UNIQUE `(collection_id, archive_id, lib_id)`
   - replace m:n book↔author / book↔genre
   - индексирование в Meilisearch (батчами, с ожиданием task'а)
4. `DEL=1` записи попадают в PG со `deleted=true`, в Meili не индексируются (по умолчанию скрыты, как в MyHomeLib)

### Скачивание

`GET /api/books/:id/download?format=epub3` → backend:

1. читает книгу из PG, формирует путь к zip + fb2-файл внутри
2. для `format=fb2` — стримит содержимое из zip напрямую
3. для остальных форматов — вызывает `fbc convert` (бинарь вшит в образ), кэширует результат на диск
4. возвращает файл с правильным `Content-Type` и `Content-Disposition` (RFC 5987 для UTF-8 имён)

Кэш — `/cache/converted/<book_id>-<format>.<ext>`. Per-key mutex от race'ов на одинаковые запросы.

### Lazy enrichment

При первом `GET /api/books/{id}` backend запускает fire-and-forget горутины, обогащающие книгу:

- **Cover**: `metadata.Fb2Provider` → `OpenLibraryProvider` → `GoogleBooksProvider`. Первый успешный сохраняет файл в `/cache/covers/{sha256.ext}` и пишет относительный путь в `books.cover_path`.
- **Annotation**: те же три провайдера в той же последовательности, plain-text с `\n\n` между параграфами, пишется в `books.annotation`.

При первом `GET /api/authors/{id}` — аналогично для био и портрета (`metadata.WikipediaProvider` → `OpenLibraryProvider`).

При первом `GET /api/books/{id}/adaptations` — `WikidataAdaptationsProvider`: SPARQL `wbsearchentities` → валидация автора через `P50` → запрос `?film wdt:P144 wd:QID` с `?sitelinks ?kinopoiskId ?imdbId ?image`. Результаты дедуплицируются, постеры скачиваются в `/cache/covers/`, пишутся в `book_adaptations` с приоритетом `kinopoiskId → imdbId → wikidata` для `ext_url`.

Каждый Ensure-метод дедуплицирует параллельные вызовы (in-flight map по ID), пропускает книги где данные уже есть, и помечает `*_fetched_at` после попытки чтобы фронт мог отличить «ещё ищем» от «уже посмотрели и не нашли».

### Frontend polling

`useBook` и `useAdaptations` (TanStack Query) с `refetchInterval`, которая останавливается когда:
- данные пришли (cover_path и annotation для useBook, status="done" для useAdaptations), ИЛИ
- исчерпался лимит ретраев (10 × 2s для книги, 15 × 2s для экранизаций — последние идут через медленный SPARQL)

После исчерпания лимита UI показывает явный fallback («Описание отсутствует», «Экранизаций не найдено») вместо вечного скелетона.

## Релизы

`release.yml` срабатывает на push тега `v*.*.*`:

1. Multi-arch build (`linux/amd64` + `linux/arm64`) backend + frontend
2. Push в `ghcr.io/alshstf/skriptes-backend:{version}` и `skriptes-frontend:{version}`
3. Для не-пре-релизных тегов (без `-` в имени) дополнительно тегами `latest`, `0.X`, `0`
4. Создание GitHub Release с auto-generated notes

Чтобы выпустить релиз:

```bash
git checkout main && git pull
git tag -a v0.X.Y-alpha.Z -m "v0.X.Y-alpha.Z"
git push origin v0.X.Y-alpha.Z
# дальше следите за https://github.com/alshstf/skriptes/actions/workflows/release.yml
```

После успешного релиза обновите `SKRIPTES_VERSION` дефолт в `infra/.env.example`.

## Соглашения по коду

- **Backend**: `go fmt` + `golangci-lint run`, комментарии doc-style для exported имён. SQL миграции — пара up/down с номером (`NNNN_name.up.sql`).
- **Frontend**: ESLint + Prettier + TypeScript strict. UI-компоненты — shadcn/ui (`npx shadcn add`); кастомные стили — Tailwind utility classes, не CSS-модули.
- **Сообщения коммитов**: conventional commits с областью (`feat(kindle):`, `fix(adaptations):`, `test(fixtures):`).
- **PR-описания**: «Summary» что и зачем + «Test plan» с чекбоксами (см. недавние PR как образец).

## Безопасность при разработке

- Никогда не коммитьте секреты в `infra/.env` — он gitignore'нут, но `.env.example` коммитится без боевых значений.
- `make seed-admin` принимает пароль через `SKRIPTES_SEED_PASSWORD` env-переменную (не через argv) чтобы не светить в `ps`.
- Для тестовых данных используйте public-domain книги. Не коммитьте чужие fb2 без явного разрешения.
