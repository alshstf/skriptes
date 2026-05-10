# skriptes

Каталогизатор домашней библиотеки fb2-книг.

Импортирует метаданные из INPX (формат MyHomeLib/Flibusta), хранит описания книг/авторов/серий/жанров в PostgreSQL, индексирует в Meilisearch, отдаёт книги по запросу с конвертацией в epub/kepub/azw8/kfx (через [fb2cng](https://github.com/rupor-github/fb2cng)). Сами архивы книг не копирует — читает из read-only volume.

## Стек

- **Backend:** Go (chi, pgx, sqlc, river queue)
- **Frontend:** React + Vite + TanStack Router/Query + shadcn/ui + Tailwind v4
- **БД:** PostgreSQL 17
- **Поиск:** Meilisearch
- **Конвертация:** fb2cng
- **Reverse proxy:** Caddy

## Структура репозитория

```
backend/      Go API
frontend/     React SPA
infra/        docker-compose, Caddyfile, .env.example
.github/      CI workflows
```

## Быстрый старт (dev)

```bash
cp infra/.env.example infra/.env
make up           # поднять стек: postgres, meilisearch, backend, frontend, caddy
make logs         # логи
make down         # остановить
```

UI доступен на `https://skriptes.localhost` (через Caddy с локальным TLS), API — там же под `/api`.
Для разработки фронта без docker: `make frontend-dev` → `http://localhost:5173`.

## Команды

| Команда | Что делает |
|---|---|
| `make up` | Поднять весь стек |
| `make down` | Остановить |
| `make logs` | Логи всех сервисов |
| `make test` | Прогнать тесты (backend + frontend) |
| `make lint` | Линтеры |
| `make migrate` | Применить миграции БД |
| `make backend-run` | Запустить backend локально (без docker) |
| `make frontend-dev` | Запустить vite dev server |

## Дорожная карта

См. план разработки по фазам в `~/.claude/plans/cozy-zooming-popcorn.md`.

## Лицензия

MIT (см. [LICENSE](LICENSE)).
