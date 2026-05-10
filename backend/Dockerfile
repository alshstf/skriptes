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
    -o /out/skriptes ./cmd/skriptes

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
    curl -fsSL -o fbc.zip "$URL" && \
    unzip fbc.zip && rm fbc.zip && chmod +x fbc

# ── runtime ─────────────────────────────────────────────────────
FROM alpine:${ALPINE_VERSION}
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S skriptes && adduser -S -G skriptes skriptes && \
    mkdir -p /data/books /data/inpx /cache && \
    chown -R skriptes:skriptes /cache
COPY --from=builder /out/skriptes /usr/local/bin/skriptes
COPY --from=fb2cng /opt/fb2cng/fbc /usr/local/bin/fbc
USER skriptes
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/skriptes"]
