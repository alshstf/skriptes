# skriptes

[![ci](https://github.com/alshstf/skriptes/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/alshstf/skriptes/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/alshstf/skriptes?include_prereleases&sort=semver)](https://github.com/alshstf/skriptes/releases)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

Каталогизатор домашней библиотеки fb2-книг. Импортирует метаданные из INPX (формат MyHomeLib / Flibusta / Lib.rus.ec), хранит их в PostgreSQL, индексирует в Meilisearch, отдаёт книги на лету с конвертацией в epub / kepub / azw8 / kfx (через [fb2cng](https://github.com/rupor-github/fb2cng)). Сами архивы книг не копирует — читает из read-only volume.

Карточки автоматически обогащаются из открытых источников: обложки и аннотации из fb2 / Open Library / Google Books, биографии и портреты авторов из Wikipedia, экранизации книг через Wikidata. Готовые epub можно скачать или сразу отправить на Kindle через email.

> **Status:** alpha. API и схема могут поменяться без deprecation; для первых пользователей и обратной связи.

## Возможности

**Каталог и поиск**
- Мгновенный опечаткостойкий поиск по названию / автору / серии (Meilisearch)
- Браузинг по авторам и сериям с обратными ссылками; статистика по автору (топ-жанры, серии, гистограмма по годам)
- Фильтры по жанру / языку / году; персонализированный re-ranking по истории просмотров
- Избранное на книги, авторов и серии

**Чтение и доставка**
- Скачивание в **epub3 / epub2 / kepub / azw8 / kfx / fb2** (passthrough) с конвертацией на лету
- **Send-to-Kindle** через SMTP: одна или несколько целей (`@kindle.com`), выбор адресата перед отправкой
- Кэш сконвертированных файлов — повторное скачивание мгновенно

**Обогащение карточек**
- **Обложки** книг — fb2 (~99% хит-рейт на русскоязычной коллекции) → Open Library → Google Books
- **Аннотации** — из fb2 → OL works.description → Google Books
- **Биографии и портреты авторов** — Wikipedia REST API (полный intro section) + Open Library как fallback, разворачиваемый текст для длинных био
- **Экранизации книг** — через Wikidata SPARQL (P144 "based on"): фильмы и сериалы, отсортированные по известности (число языковых Wikipedia на статью), с постерами и прямыми ссылками на Кинопоиск / IMDb

Обогащение **lazy**: запускается при первом открытии карточки, кэшируется в БД и `/cache/covers/`, повторные открытия мгновенны.

**Аутентификация**
- Мульти-пользователь, локальная аутентификация (cookie-сессии, bcrypt)
- Роли admin / user; admin создаёт пользователей из CLI

**Развёртывание**
- Один `docker compose up`, все 5 сервисов; авто-TLS через Caddy для `*.localhost`
- Multi-arch образы (linux/amd64 + linux/arm64)
- Идемпотентный импорт INPX — повторный запуск на том же файле no-op (sha256 хэш-чек)

---

## Архитектура (с точки зрения развёртывания)

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

- **backend** — REST API, импорт INPX, чтение книг из zip, конвертация через fbc, обогащение метаданными
- **frontend** — SPA с авторизацией, листингами, поиском, скачиванием, страницей профиля
- **postgres** — каталог (книги, авторы, серии, жанры, пользователи, сессии, history, экранизации)
- **meilisearch** — поисковый индекс книг (typo-tolerant, instant)
- **caddy** — reverse-proxy, TLS

Чувствительные данные (книги, метаданные пользователей) **не покидают сервер**; внешние API дёргаются только для обогащения карточек (Wikipedia, Wikidata, Open Library, Google Books) без отправки личных данных.

---

## Быстрый старт

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
#    SKRIPTES_VERSION=1.3.9                # тег релиза
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

### Импорт INPX

- Положите файл `*.inpx` в каталог `INPX_HOST_PATH` **до** запуска backend (или перезапустите его — `docker compose restart backend`).
- Backend на старте сканирует каталог и запускает импорт каждого `*.inpx`. Прогресс — в логах: `docker compose logs -f backend`.
- Импорт **идемпотентный**: повторный старт на том же файле — no-op (хэш-чек). Чтобы переимпортировать — замените файл новой версией.

### Где должны лежать архивы с книгами?

В `BOOKS_HOST_PATH` (на вашем хосте). Имя каждого `*.zip` совпадает с записью в БД, которая выводится из имени `*.inp` (заменой расширения). Пример коллекции librusec:

```
BOOKS_HOST_PATH/
├── fb2-001234-005678.zip
├── fb2-005679-007890_lost.zip   ← "_lost" суффикс сохраняется как есть
└── ...
```

Внутри каждого zip — fb2-файлы с именами как в `LIBID` поле INPX (`749080.fb2`). Backend открывает zip и читает нужный файл по запросу — данные не дублируются на диск.

### Обновление до новой версии

```bash
# поправьте SKRIPTES_VERSION в .env на новый тег, потом:
docker compose -f docker-compose.release.yml --env-file .env pull
docker compose -f docker-compose.release.yml --env-file .env up -d
```

Миграции БД применяются автоматически при старте backend.

### Развёртывание в homelab (порты заняты / есть свой reverse-proxy)

Релизный compose **не пробрасывает** на хост порты `postgres` / `meilisearch` / `backend` — вся коммуникация между сервисами идёт через внутреннюю docker-сеть. Единственное что слушает на хосте — `caddy` на портах 80 / 443. Это важно, потому что в homelab'е на одном Docker-хосте часто крутится несколько стеков, и порты типа 5432/7700/8080 заняты другими postgres/meili/backend.

Два типичных кейса для homelab'а:

#### А. Порты 80 / 443 заняты другим reverse-proxy (но Caddy внутри стека нужен)

Переопределите `HTTP_PORT` / `HTTPS_PORT` в `.env` на свободные:

```env
HTTP_PORT=8080
HTTPS_PORT=8443
```

Caddy внутри контейнера остаётся на 80/443, наружу торчит на указанных портах. Дальше вы либо ходите по `https://skriptes.localhost:8443`, либо ваш внешний reverse-proxy делает `proxy_pass` на 127.0.0.1:8443.

#### Б. У вас уже есть свой edge reverse-proxy и Caddy внутри стека не нужен

Используйте готовый override-файл `docker-compose.no-caddy.override.yml` (выключает Caddy, пробрасывает `backend` и `frontend` на 127.0.0.1):

```bash
curl -fO $RAW/docker-compose.no-caddy.override.yml

docker compose \
  -f docker-compose.release.yml \
  -f docker-compose.no-caddy.override.yml \
  --env-file .env up -d
```

Затем в конфиге вашего edge-proxy (nginx / Traefik / Caddy на хосте) сделайте:
- `/api/*`, `/healthz`, `/readyz` → `127.0.0.1:8080` (backend)
- всё остальное → `127.0.0.1:3000` (frontend, отдаёт SPA)

Готовый пример nginx-секции — в комментариях `docker-compose.no-caddy.override.yml`. Не забудьте обновить `SKRIPTES_HOST` и `SKRIPTES_ALLOWED_ORIGINS` в `.env` под ваш реальный домен — без них CSRF middleware backend'а отбросит мутирующие запросы (login, send-to-kindle и т.п.).

---

## Send-to-Kindle (настройка)

Для отправки книги на Kindle нужен SMTP-аккаунт (Gmail, Yandex и т.п.). Без SMTP функция выключена: на карточке книги вместо кнопки «На Kindle» появляется ссылка на настройки, а API возвращает 503.

#### 1. Подключите SMTP в `.env`

```env
# Gmail (через STARTTLS на 587)
SKRIPTES_SMTP_HOST=smtp.gmail.com
SKRIPTES_SMTP_PORT=587
SKRIPTES_SMTP_USER=your-account@gmail.com
SKRIPTES_SMTP_PASSWORD=app-password   # НЕ основной пароль; см. ниже
SKRIPTES_SMTP_FROM=your-account@gmail.com
SKRIPTES_SMTP_USE_TLS=false           # false = STARTTLS, true = implicit TLS

# Yandex (implicit TLS на 465)
# SKRIPTES_SMTP_HOST=smtp.yandex.ru
# SKRIPTES_SMTP_PORT=465
# SKRIPTES_SMTP_USE_TLS=true
```

Для Gmail создайте **app-password** в [myaccount.google.com/apppasswords](https://myaccount.google.com/apppasswords); основной пароль аккаунта не сработает (Google блокирует SMTP с основным паролем с 2022 г.). Для Yandex.Mail — аналогичный «пароль для приложений» в настройках почты.

Перезапустите backend (`docker compose restart backend`).

#### 2. Добавьте FROM-адрес в «Утверждённые отправители» Amazon

[amazon.com/hz/mycd/myx#/home/settings](https://www.amazon.com/hz/mycd/myx#/home/settings) → блок Personal Document Settings → Approved Personal Document E-mail List → Add a new approved e-mail address. Без этого Amazon молча отбросит письмо.

#### 3. Зарегистрируйте Kindle-адрес в профиле

На странице профиля (иконка шестерёнки в шапке) добавьте один или несколько `@kindle.com` адресов с человекочитаемыми лейблами («Мой Kindle», «Kindle жены»). На карточке книги появится кнопка «На Kindle» — при нескольких целях откроется выпадашка с выбором.

Файл отправляется в **epub3** — формат, который Kindle принимает напрямую с 2022 г. без конвертации в kf8.

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
| `SKRIPTES_CACHE_ROOT` | `/cache` | Кэш конвертированных файлов и обложек |
| `SKRIPTES_FBC_PATH` | `fbc` | Путь к fbc-бинарю (вшит в образ) |
| `BACKEND_PORT` | `8080` | Порт на хосте (только 127.0.0.1; основной доступ через Caddy) |

### Auth / Cookies

| Переменная | Дефолт | Описание |
|---|---|---|
| `SKRIPTES_COOKIE_SECURE` | `true` | `false` только для чистого-HTTP dev |
| `SKRIPTES_COOKIE_DOMAIN` | (пусто) | Пусто = текущий host |
| `SKRIPTES_ALLOWED_ORIGINS` | `https://skriptes.localhost` | CSV-список разрешённых Origin'ов для мутирующих запросов (CSRF) |

### Send-to-Kindle / SMTP

Все опциональные; если `SKRIPTES_SMTP_HOST` пуст — функция выключена.

| Переменная | Дефолт | Описание |
|---|---|---|
| `SKRIPTES_SMTP_HOST` | (пусто) | Хост SMTP-сервера; пусто = функция отключена |
| `SKRIPTES_SMTP_PORT` | `587` | 587 для STARTTLS (Gmail), 465 для implicit TLS (Yandex) |
| `SKRIPTES_SMTP_USER` | (пусто) | Логин SMTP-аккаунта |
| `SKRIPTES_SMTP_PASSWORD` | (пусто) | App-password (НЕ основной пароль для Gmail / Яндекса) |
| `SKRIPTES_SMTP_FROM` | (пусто) | From-адрес; пусто = берётся USER. Должен быть в «Утверждённых отправителях» Amazon |
| `SKRIPTES_SMTP_USE_TLS` | `false` | `false` = STARTTLS на 587, `true` = implicit TLS на 465 |

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

## Внешние источники данных

Backend дёргает следующие открытые API при первом открытии карточки книги или автора (lazy enrichment). Запросы анонимные, без OAuth и токенов; ничего личного наружу не передаётся (только название книги / имя автора для поиска).

| Источник | Что берём | Документация |
|---|---|---|
| **Open Library** | Обложки книг, аннотации (works.description) | [openlibrary.org/developers/api](https://openlibrary.org/developers/api) |
| **Google Books** | Обложки и аннотации (fallback после OL) | [developers.google.com/books](https://developers.google.com/books) |
| **Wikipedia REST API** | Биография автора (полный intro section), портреты | [en.wikipedia.org/api/rest_v1/](https://en.wikipedia.org/api/rest_v1/) |
| **Wikidata SPARQL** | Экранизации книг (P144 "based on"); идентификаторы Кинопоиска (P2603) и IMDb (P345); постеры (P18); популярность (sitelinks) | [query.wikidata.org](https://query.wikidata.org/) |
| **Wikimedia Commons** | Постеры экранизаций по Wikidata-P18 | [commons.wikimedia.org](https://commons.wikimedia.org/) |

Если какой-то источник недоступен (нет сети, упал rate-limit) — backend просто переходит к следующему в цепочке и не валит остальной флоу. Результаты enrichment'а кэшируются в БД и на диске (`/cache/covers/{sha256.ext}`).

---

## Безопасность

- Пароли — **bcrypt** cost=12, явный лимит 72 байта (bcrypt тихо обрезает дальше)
- Сессии — opaque-токены 256 бит из `crypto/rand`, в PG-таблице `sessions` с TTL 30 дней
- Cookie — `HttpOnly`, `SameSite=Lax`, `Secure` (если `SKRIPTES_COOKIE_SECURE=true`)
- CSRF — Origin/Referer-чек на мутирующих методах через middleware
- Защита от user enumeration — login всегда отвечает одинаково при неверном email и неверном пароле, плюс «балансировочный» bcrypt при unknown email чтобы timing не выдавал

В alpha-релизе **нет**: rate-limit'а на login (защита bcrypt cost=12 + сетевой перебор), 2FA, OIDC. Это для домашнего сервера с доверенной сетью.

---

## Разработка

Хотите внести изменения, прогнать тесты локально или поднять dev-стек с горячей перезагрузкой? Смотрите [CONTRIBUTING.md](CONTRIBUTING.md) — там сборка из исходников, Make-таргеты, структура репо и инструкции по тестам.

---

## Лицензия

[MIT](LICENSE).
