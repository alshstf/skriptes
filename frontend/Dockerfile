# syntax=docker/dockerfile:1.7
ARG NODE_VERSION=22
ARG NGINX_VERSION=1.27-alpine

# ── builder ─────────────────────────────────────────────────────
# --platform=$BUILDPLATFORM: сборка фронта (npm ci + vite build) выдаёт
# АРХ-НЕЗАВИСИМЫЙ статический `dist` (JS/CSS/HTML), поэтому гоняем её на
# НАТИВНОЙ платформе раннера (amd64) ОДИН раз для всех target-арок. Без этого
# при multi-arch (linux/arm64) весь npm/vite крутился под qemu-эмуляцией —
# минуты превращались в часы (релиз 0.9.0 упёрся в 6-часовой кап Actions).
# Runtime-стадия (nginx) остаётся per-target-arch и лишь копирует готовый dist.
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine AS builder
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm ci
COPY . .
RUN npm run build

# ── runtime ─────────────────────────────────────────────────────
# nginx-unprivileged: работает под non-root (uid 101), слушает 8080, пишет pid/temp
# в /tmp. Дружит с read-only FS + cap_drop ALL (нулевые капы) — для безопасной
# публикации (DMZ); homelab'у это безвредно лучше. См. docker-compose.harden.yml.
FROM nginxinc/nginx-unprivileged:${NGINX_VERSION}
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /app/dist /usr/share/nginx/html
EXPOSE 8080
# 127.0.0.1, а не localhost: в alpine localhost резолвится в IPv6 ::1, где
# nginx не слушает → wget получал "connection refused" и контейнер вечно висел
# unhealthy (хотя снаружи отдавал 200). Стиль (-q --spider, 127.0.0.1) — как у
# остальных healthcheck в compose.
HEALTHCHECK --interval=30s --timeout=3s CMD wget -q --spider http://127.0.0.1:8080/ || exit 1
