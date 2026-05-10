# skriptes

[![ci](https://github.com/alshstf/skriptes/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/alshstf/skriptes/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/alshstf/skriptes?include_prereleases&sort=semver)](https://github.com/alshstf/skriptes/releases)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

Каталогизатор домашней библиотеки fb2-книг. Импортирует метаданные из INPX (формат MyHomeLib / Flibusta / Lib.rus.ec), хранит их в PostgreSQL, индексирует в Meilisearch, отдаёт книги на лету с конвертацией в epub / kepub / azw8 / kfx (через [fb2cng](https://github.com/rupor-github/fb2cng)). Сами архивы книг не копирует — читает из read-only volume.

> **Status:** alpha. API и схема могут поменяться без deprecation; для первых пользователей и обратной связи.

## Возможности

- **Поиск** мгновенный, опечаткостойкий, по названию / автору / серии (Meilisearch)
- **Браузинг** по авторам и сериям с обратными ссылками; статистика по автору (топ-жанры, серии)
- **Скачивание** в epub3 / epub2 / kepub / azw8 / kfx / fb2 (passthrough)
- **Мульти-пользователь** с локальной аутентификацией (cookie-сессии, bcrypt)
- **Идемпотентный импорт** INPX — повторный запуск на том же файле no-op (sha256 хэш-чек)
- **Подходит к домашней инфре**: один `docker compose up`, все 5 сервисов, авто-TLS через Caddy для `*.localhost`

## Архитектура

```
┌──────────┐    ┌──────────────────┐
│  Caddy   │ ←→ │ frontend (nginx) │
│ (HTTPS,  │    └──────────────────┘
│  /api/*  │
│   →      │    ┌──────────────────┐    ┌────────────┐
│   →      │ ←→ │ backend (Go)     │ ←→ │ PostgreSQL │
└──────────┘    │  + fbc binary    │    └────────────┘
                │                  │    ┌─────────────┐
                │                  │ ←→ │ Meilisearch │
                └──────────────────┘    └─────────────┘
                         ↑
                         │ read-only mounts
                ┌────────┴────────┐
                │  /data/books    │  zip-архивы с fb2
                │  /data/inpx     │  *.inpx файлы
                └─────────────────┘
```

- **backend** (Go) — REST API, импорт INPX, чтение книг из zip, конвертация через fbc
- **frontend** (React + Vite + Tailwind + shadcn/ui) — SPA с авторизацией, листингами, поиском, скачиванием
- **postgres** — каталог (книги, авторы, серии, жанры, пользователи, сессии, history)
- **meilisearch** — поисковый индекс книг (typo-tolerant, instant)
- **caddy** — reverse-proxy, TLS

---

## Быстрый старт

Два пути: **(а)** развернуть из готовых образов (нет необходимости в Go / Node) и **(б)** клонировать репо для разработки.

### (а) Развёртывание из готовых образов

Подходит, если хотите просто запустить себе. Образы публикуются в [ghcr.io/alshstf/skriptes-backend](https://github.com/alshstf/skriptes/pkgs/container/skriptes-backend) и `skriptes-frontend` под тегами вида `0.1.0-alpha.1`.

#### Требования
- Linux x86_64 или arm64 (домашний сервер / mini-PC / NAS / Mac на Apple Silicon)
- Docker 24+ и `docker compose` 2+
- Каталог где лежат zip-архивы с книгами (например `/srv/library/books`)
- Каталог где лежат `*.inpx` (часто тот же)
- Резерв ~2 ГБ под PG / Meilisearch / cache

#### Шаги

```bash
# 1) подготовьте каталог
mkdir -p ~/skriptes && cd ~/skriptes

# 2) скачайте compose, env-шаблон и Caddyfile
RAW=https://raw.githubusercontent.com/alshstf/skriptes/main/infra
curl -fO  $RAW/docker-compose.release.yml
curl -fo .env $RAW/.env.example
curl -fO  $RAW/Caddyfile

# 3) поправьте .env — минимум эти три значения:
#    BOOKS_HOST_PATH=/srv/library/books    # путь к zip-архивам
#    INPX_HOST_PATH=/srv/library/inpx      # путь к *.inpx
#    SKRIPTES_VERSION=0.1.0-alpha.1        # тег релиза
$EDITOR .env

# 4) запустите стек
docker compose -f docker-compose.release.yml --env-file .env up -d
docker compose -f docker-compose.release.yml --env-file .env ps
# все 5 контейнеров должны прийти в healthy через 30–60 секунд

# 5) создайте первого admin-пользователя
#    -e SKRIPTES_SEED_PASSWORD=<пароль>  передаёт пароль через переменную окружения
docker compose -f docker-compose.release.yml --env-file .env run --rm \
  -e SKRIPTES_SEED_PASSWORD=secret123 \
  --entrypoint skriptes-seed backend \
  --email me@example.com --display-name "Me" --no-prompt

# 6) откройте https://skriptes.localhost и логиньтесь
#    Caddy выдаст самоподписанный сертификат через локальный CA — браузер
#    при первом заходе попросит его принять.
```

#### Импорт INPX

- Положите файл `*.inpx` в каталог `INPX_HOST_PATH` **до** запуска backend (или перезапустите его — `docker compose restart backend`).
- Backend на старте сканирует каталог и запускает импорт каждого `*.inpx`. Прогресс — в логах: `docker compose logs -f backend`.
- Импорт **идемпотентный**: повторный старт на том же файле — no-op (хэш-чек). Чтобы переимпортировать — замените файл новой версией.

#### Где должны лежать архивы с книгами?

В `BOOKS_HOST_PATH` (на вашем хосте). Имя каждого `*.zip` совпадает с записью в БД, которая выводится из имени `*.inp` (заменой расширения). Пример коллекции librusec:

```
BOOKS_HOST_PATH/
├── fb2-001234-005678.zip
├── fb2-005679-007890_lost.zip   ← "_lost" суффикс сохраняется как есть
└── ...
```

Внутри каждого zip — fb2-файлы с именами как в `LIBID` поле INPX (`749080.fb2`). Backend открывает zip и читает нужный файл по запросу — данные не дублируются на диск.

#### Обновление до новой версии

```bash
# поправьте SKRIPTES_VERSION в .env на новый тег, потом:
docker compose -f docker-compose.release.yml --env-file .env pull
docker compose -f docker-compose.release.yml --env-file .env up -d
```

### (б) Сборка из исходников (для разработки)

```bash
git clone https://github.com/alshstf/skriptes.git
cd skriptes
cp infra/.env.example infra/.env
$EDITOR infra/.env   # проставьте BOOKS_HOST_PATH / INPX_HOST_PATH
make up              # build + up
make seed-admin EMAIL=me@example.com PASSWORD=secret123 DISPLAY_NAME="Me"
open https://skriptes.localhost
```

Команды для разработки — см. `make help`.

---

## Конфигурация (env reference)

Все переменные читаются из `.env` (см. `infra/.env.example` как шаблон).

### PostgreSQL

| Переменная | Дефолт | Описание |
|---|---|---|
| `POSTGRES_USER` | `skriptes` | Имя пользователя БД |
| `POSTGRES_PASSWORD` | `skriptes` | Пароль БД (поменяйте в проде!) |
| `POSTGRES_DB` | `skriptes` | Имя БД |
| `POSTGRES_PORT` | `5432` | Порт на хосте (биндится только на 127.0.0.1) |

### Meilisearch

| Переменная | Дефолт | Описание |
|---|---|---|
| `MEILI_MASTER_KEY` | (пусто) | Master-key. В dev можно пусто; для прода обязательно ≥16 байт |
| `MEILI_PORT` | `7700` | Порт на хосте (только 127.0.0.1) |
| `MEILI_ENV` | `development` | Поставьте `production` для prod-режима (требует master key) |

### Backend

| Переменная | Дефолт | Описание |
|---|---|---|
| `SKRIPTES_HTTP_ADDR` | `:8080` | HTTP listen-адрес |
| `SKRIPTES_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `SKRIPTES_LOG_FORMAT` | `json` | `json` для прода, `text` для dev |
| `SKRIPTES_VERSION` | `dev` | Тег для отображения (для релиза = тег образа) |
| `SKRIPTES_BOOKS_ROOT` | `/data/books` | Путь внутри контейнера, не меняйте без причины |
| `SKRIPTES_INPX_ROOT` | `/data/inpx` | Путь внутри контейнера |
| `SKRIPTES_CACHE_ROOT` | `/cache` | Кэш конвертированных файлов |
| `SKRIPTES_FBC_PATH` | `fbc` | Путь к fbc-бинарю (вшит в образ) |
| `BACKEND_PORT` | `8080` | Порт на хосте (только 127.0.0.1; основной доступ через Caddy) |

### Auth / Cookies

| Переменная | Дефолт | Описание |
|---|---|---|
| `SKRIPTES_COOKIE_SECURE` | `true` | `false` только для чистого-HTTP dev |
| `SKRIPTES_COOKIE_DOMAIN` | (пусто) | Пусто = текущий host |
| `SKRIPTES_ALLOWED_ORIGINS` | `https://skriptes.localhost` | CSV-список разрешённых Origin'ов для мутирующих запросов (CSRF) |

### Тома (хост-пути)

| Переменная | Дефолт | Описание |
|---|---|---|
| `BOOKS_HOST_PATH` | `./data/books` | **Обязательно** — путь к zip-архивам с книгами на хосте (read-only) |
| `INPX_HOST_PATH` | `./data/inpx` | **Обязательно** — путь к INPX-файлам на хосте (read-only) |

### Caddy / hostname

| Переменная | Дефолт | Описание |
|---|---|---|
| `SKRIPTES_HOST` | `skriptes.localhost` | Hostname; Caddy получит TLS-сертификат для него |
| `HTTP_PORT` | `80` | HTTP-порт на хосте |
| `HTTPS_PORT` | `443` | HTTPS-порт на хосте |

---

## Команды разработки

```bash
make help              # список всех таргетов

# стек
make up                # поднять весь стек (build + up)
make down              # остановить
make logs              # follow-логи
make ps                # статус контейнеров
make clean             # ⚠ удалить volumes (потеря данных)

# release
make up-release        # поднять стек из готовых ghcr.io-образов
make down-release      # остановить

# backend
make backend-run       # go run ./cmd/skriptes (без docker)
make backend-test      # go test ./...
make backend-lint      # golangci-lint

# frontend
make frontend-dev      # vite dev server (5173)
make frontend-test     # vitest unit-тесты
make frontend-e2e      # Playwright e2e (chromium на vite preview)
make frontend-lint     # eslint + tsc

# admin
make seed-admin EMAIL=alice@example.com PASSWORD=secret123 [DISPLAY_NAME="Alice"]
```

---

## Как это работает (внутренности)

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

---

## Безопасность

- Пароли — **bcrypt** cost=12, явный лимит 72 байта (bcrypt тихо обрезает дальше)
- Сессии — opaque-токены 256 бит из `crypto/rand`, в PG-таблице `sessions` с TTL 30 дней
- Cookie — `HttpOnly`, `SameSite=Lax`, `Secure` (если `SKRIPTES_COOKIE_SECURE=true`)
- CSRF — Origin/Referer-чек на мутирующих методах через middleware
- Защита от user enumeration — login всегда отвечает одинаково при неверном email и неверном пароле, плюс "балансировочный" bcrypt при unknown email чтобы timing не выдавал

В alpha-релизе **нет**: rate-limit'а на login (защита bcrypt cost=12 + сетевой перебор), 2FA, OIDC. Это для домашнего сервера с доверенной сетью.

---

## Лицензия

[MIT](LICENSE).
