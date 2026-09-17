# eval-display: Hertz + SQLite (modernc, no CGO) + S3/fs.
# Runtime tokens are not baked in; compose or the operator must set
# EVAL_DISPLAY_ADMIN_TOKEN, EVAL_DISPLAY_ANON_KEY, EVAL_DISPLAY_JWT_SECRET.
# Viewer GET /v1 is public — do not require a viewer token.

ARG GO_VERSION=1.27
# Official images match go.mod. If docker.io / proxy.golang.org is unreachable:
#   --build-arg BUILDER_IMAGE=.../golang:1.27-bookworm
#   --build-arg RUNTIME_IMAGE=.../alpine:3.21
#   --build-arg GOPROXY=https://goproxy.cn,direct
ARG BUILDER_IMAGE=golang:${GO_VERSION}-bookworm
ARG RUNTIME_IMAGE=alpine:3.21
FROM ${BUILDER_IMAGE} AS build

WORKDIR /src

ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY} \
    CGO_ENABLED=0 \
    GOTOOLCHAIN=local

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/eval-display ./cmd/eval-display

FROM ${RUNTIME_IMAGE}

RUN apk add --no-cache ca-certificates wget \
    && addgroup -g 65532 -S app \
    && adduser -u 65532 -S -G app -H -D app \
    && mkdir -p /data/s3 /app/migrations /app/static \
    && chown -R app:app /data /app

COPY --from=build --chown=app:app /out/eval-display /usr/local/bin/eval-display
COPY --chown=app:app migrations/*.sql /app/migrations/
COPY --chown=app:app internal/evaldisplay/static/ui /app/static/ui
COPY --chown=app:app internal/evaldisplay/static/admin /app/static/admin

USER app
WORKDIR /data

ENV EVAL_DISPLAY_ADDR=:8080 \
    EVAL_DISPLAY_SQLITE_PATH=/data/eval-display.sqlite \
    EVAL_DISPLAY_S3_DIR=/data/s3

# Optional override of the embedded UI: EVAL_DISPLAY_STATIC_DIR=/app/static
# S3 / RustFS: EVAL_DISPLAY_S3_BACKEND, EVAL_DISPLAY_S3_ENDPOINT, EVAL_DISPLAY_S3_BUCKET,
# EVAL_DISPLAY_S3_REGION, EVAL_DISPLAY_S3_ACCESS_KEY, EVAL_DISPLAY_S3_SECRET_KEY,
# EVAL_DISPLAY_S3_PATH_STYLE, EVAL_DISPLAY_S3_SSE
# Harbor ingest (not required for the public viewer): EVAL_DISPLAY_TOKEN

EXPOSE 8080

HEALTHCHECK --interval=15s --timeout=3s --start-period=8s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/eval-display"]
