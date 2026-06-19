# syntax=docker/dockerfile:1.7
ARG GO_VERSION=1.25
ARG ALPINE_VERSION=3.20
ARG FB2CNG_VERSION=1.3.8

# ── builder ─────────────────────────────────────────────────────
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/skriptes ./cmd/skriptes && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" \
    -o /out/skriptes-seed ./cmd/skriptes-seed

# ── fb2cng fetcher ──────────────────────────────────────────────
# Скачиваем готовый релиз fb2cng (бинарь называется `fbc`).
FROM alpine:${ALPINE_VERSION} AS fb2cng
ARG FB2CNG_VERSION
ARG TARGETARCH
RUN apk add --no-cache curl unzip ca-certificates && \
    case "${TARGETARCH}" in \
      amd64) ARCH=amd64 ;; \
      arm64) ARCH=arm64 ;; \
      *) echo "unsupported arch: ${TARGETARCH}" && exit 1 ;; \
    esac && \
    URL="https://github.com/rupor-github/fb2cng/releases/download/v${FB2CNG_VERSION}/fbc-linux-${ARCH}.zip" && \
    mkdir -p /opt/fb2cng && cd /opt/fb2cng && \
    # --retry/--retry-all-errors: загрузка релиза с GitHub-CDN иногда обрывается
    # на середине (TLS "unexpected eof") при сборке за VPN с низким MTU — повтор
    # лечит этот transient, чтобы один сетевой сбой не валил весь билд (в т.ч. CI).
    curl -fsSL --retry 5 --retry-all-errors --retry-delay 3 \
         --connect-timeout 30 --max-time 300 -o fbc.zip "$URL" && \
    unzip fbc.zip && rm fbc.zip && chmod +x fbc

# ── runtime ─────────────────────────────────────────────────────
FROM alpine:${ALPINE_VERSION}
# gcompat — glibc-compat shim. Релизные бинари fb2cng (fbc) линкуются
# с glibc + FORTIFY_SOURCE (`__vfprintf_chk`, `__fprintf_chk`), которых
# на alpine с musl нет; без gcompat fbc падает с misleading ошибкой
# "no such file or directory" при exec. См. полный разбор в PR #33.
#
# Альтернативы которые мы рассмотрели:
#  - Перейти на debian-slim: +30MB к образу, перевыпуск всех слоёв.
#  - Build fb2cng из исходников с CGO_ENABLED=0: +~5 мин к docker build
#    (скачивание CTAN-словарей и vendoring), плюс хрупкая зависимость
#    от upstream Taskfile.
# gcompat — +~2MB, одна строка, легко вернуть назад если upstream
# когда-нибудь выпустит static-релиз.
RUN apk add --no-cache ca-certificates tzdata gcompat && \
    addgroup -S skriptes && adduser -S -G skriptes skriptes && \
    mkdir -p /data/books /data/inpx /cache && \
    chown -R skriptes:skriptes /cache
COPY --from=builder /out/skriptes      /usr/local/bin/skriptes
COPY --from=builder /out/skriptes-seed /usr/local/bin/skriptes-seed
COPY --from=fb2cng  /opt/fb2cng/fbc    /usr/local/bin/fbc
# Smoke-test: бинарь должен запускаться в финальном образе. Если
# линковка снова рассинхронится (новая версия fb2cng / новый gcompat-gap
# / переезд base), docker build упадёт сразу, не доезжая до релиза.
RUN /usr/local/bin/fbc --help >/dev/null
USER skriptes
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/skriptes"]
