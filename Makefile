SHELL := /bin/bash
COMPOSE := docker compose -f infra/docker-compose.yml --env-file infra/.env
COMPOSE_RELEASE := docker compose -f infra/docker-compose.release.yml --env-file infra/.env

.PHONY: help up down logs ps build pull test lint \
        up-release down-release \
        backend-run backend-test backend-lint backend-tidy \
        frontend-dev frontend-test frontend-lint frontend-install frontend-e2e \
        migrate seed-admin clean

help:
	@grep -E '^[a-zA-Z_-]+:.*?##' $(MAKEFILE_LIST) | sort | awk 'BEGIN{FS=":.*?##"};{printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ── docker compose ──────────────────────────────────────────────
up: ## Запустить весь стек
	$(COMPOSE) up -d --build
down: ## Остановить
	$(COMPOSE) down
logs: ## Логи (follow)
	$(COMPOSE) logs -f --tail=200
ps: ## Статус контейнеров
	$(COMPOSE) ps
build: ## Пересобрать образы
	$(COMPOSE) build
pull: ## Подтянуть базовые образы
	$(COMPOSE) pull

# ── релизный стек (готовые образы из ghcr.io) ──────────────────
up-release: ## Поднять стек из готовых образов (для конечных пользователей)
	$(COMPOSE_RELEASE) pull
	$(COMPOSE_RELEASE) up -d
down-release: ## Остановить релизный стек
	$(COMPOSE_RELEASE) down

# ── backend ─────────────────────────────────────────────────────
backend-run: ## Запустить backend локально
	cd backend && go run ./cmd/skriptes
backend-test: ## Прогнать тесты backend
	cd backend && go test ./...
backend-lint: ## Линтер Go (та же версия что в CI: --timeout=5m)
	cd backend && golangci-lint run --timeout=5m ./...
backend-tidy: ## go mod tidy
	cd backend && go mod tidy

# ── frontend ────────────────────────────────────────────────────
frontend-install: ## Установить зависимости
	cd frontend && npm install
frontend-dev: ## Vite dev server
	cd frontend && npm run dev
frontend-test: ## Vitest (jsdom unit-тесты)
	cd frontend && npm run test
frontend-e2e: ## Playwright e2e (реальный chromium на vite preview, ловит layout-регрессии)
	cd frontend && npm run e2e
frontend-lint: ## ESLint + tsc
	cd frontend && npm run lint && npm run typecheck

# ── общие ───────────────────────────────────────────────────────
test: backend-test frontend-test ## Все тесты
lint: backend-lint frontend-lint ## Все линтеры

migrate: ## Применить миграции (бэкенд делает это сам при старте; ручной запуск — для миграций без HTTP)
	$(COMPOSE) run --rm --no-deps backend skriptes-seed --help >/dev/null 2>&1 || true
	@echo "ℹ︎  миграции применяются автоматически при старте backend; если нужно отдельно — запустите backend и сразу остановите"

seed-admin: ## Создать admin-пользователя (требует EMAIL и PASSWORD; пример: make seed-admin EMAIL=me@x.com PASSWORD=secret123)
	@if [ -z "$(EMAIL)" ] || [ -z "$(PASSWORD)" ]; then \
		echo "Usage: make seed-admin EMAIL=you@example.com PASSWORD=secret123 [DISPLAY_NAME='Your Name']"; \
		exit 2; \
	fi
	$(COMPOSE) run --rm \
		-e SKRIPTES_SEED_PASSWORD="$(PASSWORD)" \
		--entrypoint skriptes-seed backend \
		--email "$(EMAIL)" \
		$(if $(DISPLAY_NAME),--display-name "$(DISPLAY_NAME)") \
		--no-prompt

clean: ## Удалить volumes (ОСТОРОЖНО — все данные)
	$(COMPOSE) down -v
