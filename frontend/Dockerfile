# syntax=docker/dockerfile:1.7
ARG NODE_VERSION=22
ARG NGINX_VERSION=1.27-alpine

# ── builder ─────────────────────────────────────────────────────
FROM node:${NODE_VERSION}-alpine AS builder
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm ci
COPY . .
RUN npm run build

# ── runtime ─────────────────────────────────────────────────────
FROM nginx:${NGINX_VERSION}
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /app/dist /usr/share/nginx/html
EXPOSE 80
# 127.0.0.1, а не localhost: в alpine localhost резолвится в IPv6 ::1, где
# nginx не слушает → wget получал "connection refused" и контейнер вечно висел
# unhealthy (хотя снаружи отдавал 200). Стиль (-q --spider, 127.0.0.1) — как у
# остальных healthcheck в compose.
HEALTHCHECK --interval=30s --timeout=3s CMD wget -q --spider http://127.0.0.1/ || exit 1
