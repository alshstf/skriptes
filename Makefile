SHELL := /bin/bash
COMPOSE := docker compose -f infra/docker-compose.yml --env-file infra/.env

.PHONY: help up down logs ps build pull test lint \
        backend-run backend-test backend-lint backend-tidy \
        frontend-dev frontend-test frontend-lint frontend-install \
        migrate seed clean

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
frontend-test: ## Vitest
	cd frontend && npm run test
frontend-lint: ## ESLint + tsc
	cd frontend && npm run lint && npm run typecheck

# ── общие ───────────────────────────────────────────────────────
test: backend-test frontend-test ## Все тесты
lint: backend-lint frontend-lint ## Все линтеры

migrate: ## Применить миграции (через docker-контейнер golang-migrate)
	$(COMPOSE) run --rm migrate up

seed: ## Создать тестового admin/user (TODO в Фазе 1)
	@echo "TODO: будет реализовано в Фазе 1"

clean: ## Удалить volumes (ОСТОРОЖНО — все данные)
	$(COMPOSE) down -v
