# skriptes

[![ci](https://github.com/alshstf/skriptes/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/alshstf/skriptes/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/alshstf/skriptes?include_prereleases&sort=semver)](https://github.com/alshstf/skriptes/releases)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

Каталогизатор домашней библиотеки fb2-книг. Импортирует метаданные из INPX (формат MyHomeLib / Flibusta / Lib.rus.ec), хранит их в PostgreSQL, индексирует в Meilisearch, отдаёт книги на лету с конвертацией в epub / kepub / azw8 / kfx (через [fb2cng](https://github.com/rupor-github/fb2cng)). Сами архивы книг не копирует — читает из read-only volume. Читать можно во встроенном веб-ридере, скачать, отправить на Kindle или подключить e-reader по OPDS.

Карточки обогащаются из fb2 и открытых источников: обложки, аннотации, года написания, внешние рейтинги (Open Library / Google Books), биографии и портреты авторов (Wikipedia), экранизации книг (Wikidata). Издания одной книги (переводы, переиздания) группируются в логические «работы» — дубли схлопываются в поиске и списках.

> **Status:** активная разработка (1.x). Обновления между minor-версиями штатные (миграции применяются автоматически); API может меняться без deprecation-цикла.

## Возможности

**Каталог и поиск**
- Мгновенный опечаткостойкий поиск по названию / автору / серии (Meilisearch) + командная палитра (Cmd+K) и hero-поиск на Главной
- **Работы и издания**: переводы/переиздания одной книги группируются в логическую «работу» (автоматически + ручные merge/split у админа) — в поиске и списках нет дублей, на карточке видны все издания; ошибочно слитые работы чинятся кнопками «Пересобрать» (точечно на карточке) и «Пересобрать группировки» (все разом, админка → Фоновые операции, с прогрессом и отменой)
- Разделы **Авторы** (фильтры: жанры, языки, года активности, рейтинги, экранизации) и **Жанры**; статистика по автору (топ-жанры, серии, гистограмма по годам написания)
- Фильтры по жанру / языку / **языку оригинала** / году написания; персонализированный re-ranking по истории просмотров
- **«Золотая полка» по умолчанию**: без поискового запроса каталог упорядочен интегральной «известностью» книги — переиздания/переводы в коллекции, рейтинг LIBRATE, голоса Google Books / Open Library, экранизация, внешние счётчики известности (Фантлаб / Open Library — opt-in воркер «Известность» в админке) + ваши просмотры, прочтения и оценки; при близкой релевантности поиска известная книга тоже выигрывает
- **Главная**: «Продолжить чтение» и «Новинки по подпискам» (подписка-колокольчик на авторов и серии)
- **Избранное** книг (★) и личные **полки** (коллекции) с drag-and-drop переносом
- **Оценки**: свои оценки книг 1–5 + средняя по инстансу; отдельный «внешний рейтинг» (LIBRATE из INPX / Google Books / Open Library)
- **Видимость контента**: скрытие жанров/языков глобально (админ) и персонально (профиль)
- **Правка каталога** (админ): inline-редактирование любых полей книги прямо на карточке — название, года, серия/номер, жанры, авторы, язык, ISBN/издатель/переводчик; правки переживают ре-импорт и обогащение, откатываются

**Чтение и доставка**
- **Встроенный веб-ридер** (foliate-js) с сохранением позиции и прогрессом чтения; PWA — ставится на телефон, обложки офлайн
- Скачивание в **epub3 / epub2 / kepub / azw8 / kfx / fb2** (passthrough) с конвертацией на лету
- **OPDS-каталог** (`/opds`, HTTP Basic) для e-reader-клиентов: KOReader, Moon+ Reader и т.п.; fb2 отдаётся без конвертации
- **Send-to-Kindle** через SMTP: одна или несколько целей (`@kindle.com`), выбор адресата перед отправкой
- Кэш сконвертированных файлов — повторное скачивание мгновенно

**Обогащение карточек**
- **Обложки** книг — fb2 (~99% хит-рейт на русскоязычной коллекции) → Open Library → Google Books
- **Аннотации** — из fb2 → OL works.description → Google Books
- **Год написания** — fb2 `<title-info><date>` → OL first_publish_year → Wikidata; отдельно — год издания из fb2
- **Язык оригинала и переводчик** — из fb2 (`<src-lang>`, translator); на карточке — строка «Перевод с французского — …»
- **Внешний рейтинг** — Google Books / Open Library, когда в INPX нет LIBRATE
- **Биографии и портреты авторов** — Wikipedia REST API (полный intro section) + Open Library как fallback
- **Экранизации книг** — через Wikidata SPARQL (P144 "based on"): фильмы и сериалы с постерами и ссылками на Кинопоиск / IMDb

Режим обогащения настраивается в админке «Фоновые операции» **на каждый тип данных**: Выкл / Лениво (при первом открытии карточки) / Фоном (воркеры проходят всю коллекцию с rate-limit'ами). Результаты кэшируются в БД и `/cache`; неудачные попытки можно сбросить кнопкой и перепройти.

> ⚠️ Для обогащения из **Google Books нужен API-ключ** (`SKRIPTES_GOOGLE_BOOKS_API_KEY`): анонимные запросы GB отбивает по общей квоте (429), т.е. без ключа GB-обогащение фактически не работает. См. «Внешние источники данных».

**Аутентификация**
- Мульти-пользователь, локальная аутентификация (cookie-сессии, bcrypt); регистрация закрыта — пользователей создаёт админ в разделе администрирования (первый админ — CLI-командой при установке)
- Роли admin / user; **rate-limit логина** (анти-брутфорс, настраиваемый)

**Развёртывание**
- Один `docker compose up`, все 5 сервисов; авто-TLS через Caddy для `*.localhost`
- Multi-arch образы (linux/amd64 + linux/arm64); backend и frontend работают **non-root** (frontend — nginx-unprivileged)
- Идемпотентный импорт INPX — повторный запуск на том же файле no-op (sha256 хэш-чек)
- Для публикации в интернет — **hardening-overlay** (`docker-compose.harden.yml`: cap_drop/read-only/лимиты + Cloudflare Tunnel), см. раздел ниже

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

- **backend** — REST API + OPDS, импорт INPX, чтение книг из zip, конвертация через fbc, обогащение метаданными (non-root)
- **frontend** — SPA с авторизацией, листингами, поиском, ридером, скачиванием (nginx-unprivileged: non-root, слушает 8080)
- **postgres** — каталог (книги/работы, авторы, серии, жанры, пользователи, сессии, history, оценки, экранизации)
- **meilisearch** — поисковые индексы (typo-tolerant, instant): `works` для веба, `books` для OPDS
- **caddy** — reverse-proxy, TLS

Чувствительные данные (книги, метаданные пользователей) **не покидают сервер**; внешние API дёргаются только для обогащения карточек (Wikipedia, Wikidata, Open Library, Google Books) без отправки личных данных.

---

## Быстрый старт

#### Требования
- Linux x86_64 или arm64 (домашний сервер / mini-PC / NAS / Mac на Apple Silicon)
- Docker 24+ и `docker compose` 2+
- Каталог где лежат zip-архивы с книгами (например `/srv/library/books`)
- Каталог где лежат `*.inpx` (часто тот же)
- Резерв под данные зависит от размера коллекции: на десятки тысяч книг хватит ~2 ГБ, на сотни тысяч PG + Meilisearch занимают **десятки ГБ** (плюс кэш обложек/конвертаций — настраиваемый LRU-бюджет)

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
#    SKRIPTES_VERSION=1.7.3               # тег релиза
#    Рекомендуется сразу задать и SKRIPTES_GOOGLE_BOOKS_API_KEY —
#    без него обогащение из Google Books не работает (см. «Внешние источники»).
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

### Публичный доступ из интернета (hardening-overlay)

Выставлять стек в интернет «как есть» не стоит. Для публичного деплоя в репо есть overlay `infra/docker-compose.harden.yml` (+ шаблон `infra/.env.public.example`):

```bash
docker compose -f docker-compose.release.yml -f docker-compose.harden.yml \
               --env-file .env --profile public up -d
```

- **Хардненинг контейнеров**: `cap_drop: ALL`, read-only FS + tmpfs, `no-new-privileges`, лимиты памяти; backend и frontend — non-root.
- **Cloudflare Tunnel** (сервис `cloudflared` под `--profile public`): исходящее соединение к Cloudflare — **ноль входящих портов** на роутере, origin-IP скрыт. Токен туннеля — `CLOUDFLARE_TUNNEL_TOKEN` в `.env`; public hostname (→ `http://caddy:80`) и identity-гейт **Cloudflare Access** (email-код, отдельная политика на `/admin`) настраиваются в дашборде Zero Trust.
- ⚠️ `/opds` наружу не публикуйте: его HTTP Basic несовместим с Cloudflare Access (закройте Access-политикой Deny или не добавляйте путь).
- Второй слой к app-rate-limit'у логина — WAF rate-limit на `/api/auth/login` в Cloudflare.

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

## OPDS для e-reader'ов

Каталог доступен по `https://<ваш-хост>/opds` (OPDS 1.2). В e-reader-клиенте (KOReader, Moon+ Reader, CoolReader и т.п.) добавьте каталог с **HTTP Basic**-авторизацией — логин/пароль вашей учётки skriptes. Навигация: новинки / авторы / серии / жанры / поиск. Форматы: **fb2 первым** (отдаётся из архива без конвертации — мгновенно), epub/kepub/azw8 — конвертация на лету. Скачивание через OPDS учитывается как «приобретение» (питает блок «Оцените прочитанное»).

⚠️ При публикации инстанса в интернет `/opds` наружу не выставляйте: HTTP Basic несовместим с identity-гейтом Cloudflare Access (см. «Публичный доступ»).

---

## Конфигурация (env reference)

Все переменные читаются из `.env` (см. `infra/.env.example` как шаблон; для публичного деплоя — `infra/.env.public.example`).

### Общее

| Переменная | Дефолт | Описание |
|---|---|---|
| `COMPOSE_PROJECT_NAME` | `skriptes` | Префикс имён контейнеров/volume'ов |
| `TZ` | `UTC` | Часовой пояс контейнеров (например `Europe/Moscow`) |

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
| `SKRIPTES_GOOGLE_BOOKS_API_KEY` | (пусто) | API-ключ Google Books — **обязателен для GB-обогащения** (обложки/рейтинги/группировка): анонимные запросы GB отбивает 429 по общей квоте. Google Cloud Console → включить Books API → Credentials → API key; free-квота ≈1000 запросов/день на проект |
| `BACKEND_PORT` | `8080` | Порт на хосте (только 127.0.0.1; основной доступ через Caddy) |

### Auth / Cookies

| Переменная | Дефолт | Описание |
|---|---|---|
| `SKRIPTES_COOKIE_SECURE` | `true` | `false` только для чистого-HTTP dev |
| `SKRIPTES_COOKIE_DOMAIN` | (пусто) | Пусто = текущий host |
| `SKRIPTES_ALLOWED_ORIGINS` | `https://skriptes.localhost` | CSV-список разрешённых Origin'ов для мутирующих запросов (CSRF) |
| `SKRIPTES_LOGIN_RATELIMIT_IP` | `10` | Анти-брутфорс: лимит **неудачных** логинов с одного IP за 5-минутное окно (за Cloudflare берётся `CF-Connecting-IP`). `0` = слой выключен |
| `SKRIPTES_LOGIN_RATELIMIT_EMAIL` | `20` | То же per-email за 15-минутное окно (щедрее, чтобы атакующий не мог залочить чужой аккаунт). `0` = выключен |

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

Backend дёргает следующие открытые API — лениво при первом открытии карточки и/или фоновыми воркерами (режим на каждый тип данных — в админке «Фоновые операции»). Ничего личного наружу не передаётся: только название книги / имя автора для поиска; для переводных книг внешний поиск идёт по **оригинальному** названию и латинскому имени автора (из fb2 `src-title-info`) — по русскому переводу западные источники ничего не находят.

| Источник | Что берём | Аутентификация | Документация |
|---|---|---|---|
| **Open Library** | Обложки, аннотации, год первой публикации, рейтинги, счётчики известности (оценки + want-to-read), work-ключи (группировка изданий), фото авторов | Не нужна, но обязателен осмысленный User-Agent (шлём сами) | [openlibrary.org/developers/api](https://openlibrary.org/developers/api) |
| **Google Books** | Обложки, аннотации, рейтинги (fallback/дополнение к OL) | **Нужен API-ключ** (`SKRIPTES_GOOGLE_BOOKS_API_KEY`) — анонимные запросы получают 429 по общей квоте | [developers.google.com/books](https://developers.google.com/books) |
| **Фантлаб** | Число оценок произведения (сигнал известности для сортировки по популярности) — силён на русскоязычной фантастике, поиск нативно русский | Не нужна; API полуофициальное (v0.9), ходим с вежливым лимитом | [github.com/FantLab/FantLab-API](https://github.com/FantLab/FantLab-API) |
| **Wikipedia REST API** | Биография автора (полный intro section), портреты | — | [en.wikipedia.org/api/rest_v1/](https://en.wikipedia.org/api/rest_v1/) |
| **Wikidata SPARQL** | Экранизации книг (P144 "based on") с идентификаторами Кинопоиска/IMDb и постерами; год публикации (P577); QID работ (группировка) | — | [query.wikidata.org](https://query.wikidata.org/) |
| **Wikimedia Commons** | Постеры экранизаций по Wikidata-P18 | — | [commons.wikimedia.org](https://commons.wikimedia.org/) |

**Rate-limit'ы.** Фоновые воркеры ходят во внешние API с настраиваемым RPM, а Open Library дополнительно **автоматически прижимается к 60 запросам/мин** (актуальная политика OL, май 2026: 1 запрос/с анонимно, 3/с с идентифицирующим User-Agent; обложки — свой лимит 100/IP за 5 мин, там потолок 18/мин) — задрать выше из настроек не выйдет, и это осознанно: выше лимита OL начинает резать соединения. Фантлаб лимиты не документирует — держим вежливый потолок 60/мин (дефолт 30). Если источник недоступен — backend переходит к следующему в цепочке; неудачные попытки учитываются per-source и **не долбятся повторно** (перепроверить после улучшений — кнопка «Сбросить неудачные попытки» в админке). Результаты кэшируются в БД и на диске (`/cache`).

---

## Безопасность

- Пароли — **bcrypt** cost=12, явный лимит 72 байта (bcrypt тихо обрезает дальше)
- Сессии — opaque-токены 256 бит из `crypto/rand`, в PG-таблице `sessions` с TTL 30 дней
- Cookie — `HttpOnly`, `SameSite=Lax`, `Secure` (если `SKRIPTES_COOKIE_SECURE=true`)
- CSRF — Origin/Referer-чек на мутирующих методах через middleware
- Защита от user enumeration — login всегда отвечает одинаково при неверном email и неверном пароле, плюс «балансировочный» bcrypt при unknown email чтобы timing не выдавал
- **Rate-limit логина** — считает только **неудачные** попытки (легитимного пользователя не лочит): по IP (10 за 5 мин; за Cloudflare — `CF-Connecting-IP`) и по email (20 за 15 мин), ответ 429 + `Retry-After`. Настраивается, `0` = выключить (инстанс за своим WAF)
- Регистрация закрыта (invite-only: пользователей создаёт админ), публичного password-reset нет
- Контейнеры backend и frontend — **non-root**; для публичного деплоя есть hardening-overlay (cap_drop ALL, read-only FS, no-new-privileges, Cloudflare Tunnel — см. «Публичный доступ»)

Пока **нет**: 2FA, OIDC. Базовый сценарий — домашний сервер в доверенной сети; для публикации наружу используйте hardening-overlay + identity-гейт на краю (Cloudflare Access).

---

## Разработка

Хотите внести изменения, прогнать тесты локально или поднять dev-стек с горячей перезагрузкой? Смотрите [CONTRIBUTING.md](CONTRIBUTING.md) — там сборка из исходников, Make-таргеты, структура репо и инструкции по тестам.

---

## Лицензия

[MIT](LICENSE).
