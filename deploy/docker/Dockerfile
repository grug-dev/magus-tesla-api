# syntax=docker/dockerfile:1
# Multi-stage build for magus-tesla-api.
#
# The builder stage compiles cmd/web, cmd/poller, and cmd/migrate. cmd/migrate
# is a small wiring program that uses the goose LIBRARY (already a pinned
# go.mod dependency) to apply migrations — NOT the goose CLI. Building the
# goose CLI (github.com/pressly/goose/v3/cmd/goose) fails in this repo:
# go.sum has no entries for the CLI's optional database drivers, since this
# project only ever uses goose as a library. See cmd/migrate/main.go's doc
# comment and design.md's amendment (D10/D11) for the full story.
#
# Three final stages exist: web, poller, migrate. compose.yaml picks one with
# `target: <name>`. Each stage is small (Alpine base) and runs as a non-root
# user.
FROM golang:1.25-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=linux go build -o /out/web     ./cmd/web
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=linux go build -o /out/poller  ./cmd/poller
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=linux go build -o /out/migrate ./cmd/migrate

# ---- web: the HTTP gateway ----
FROM alpine:3.20 AS web
RUN apk add --no-cache ca-certificates wget && \
    addgroup -S app && adduser -S app -G app
COPY --from=builder /out/web /usr/local/bin/web
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/web"]

# ---- poller: the nightly telemetry collector ----
FROM alpine:3.20 AS poller
RUN apk add --no-cache ca-certificates && \
    addgroup -S app && adduser -S app -G app
COPY --from=builder /out/poller /usr/local/bin/poller
USER app
ENTRYPOINT ["/usr/local/bin/poller"]

# ---- migrate: applies all four migration directories, then exits ----
FROM alpine:3.20 AS migrate
RUN apk add --no-cache ca-certificates && \
    addgroup -S app && adduser -S app -G app
COPY --from=builder /out/migrate /usr/local/bin/migrate
COPY internal/account/db/migrations   /migrations/account
COPY internal/telemetry/db/migrations /migrations/telemetry
COPY internal/charging/db/migrations  /migrations/charging
COPY internal/analytics/db/migrations /migrations/analytics
USER app
ENTRYPOINT ["/usr/local/bin/migrate"]
