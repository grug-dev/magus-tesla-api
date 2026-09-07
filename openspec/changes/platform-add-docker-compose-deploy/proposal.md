# platform-add-docker-compose-deploy

Source: MAG-37 — https://linear.app/magus-monitor/issue/MAG-37/docker-compose

## Why

The project has no Docker setup today. There is no `Dockerfile`, no compose file, and
no `.dockerignore` anywhere in the repo. The owner wants a first production deploy on
a small Hostinger VPS, and it is their first time deploying to a VPS.

`internal/config.Load()` also has a blocker for any container. It calls
`godotenv.Load()` and returns an error when no `.env` file exists. A container gets
its config from real environment variables, not a file. Today, `cmd/web` and
`cmd/poller` both crash on start inside a container, before they read a single flag.
This change must fix that.

The owner asked for best practices, but no infrastructure the small deploy does not
need. Two things force one extra service each: Google OAuth only allows plain `http`
for `localhost`, so a public HTTPS certificate is required to log in at all — hence
Caddy. And the schema is applied by four ordered goose directories against one shared
`goose_db_version` table — hence a small one-shot migration step that runs before the
app starts.

## What Changes

- Add a multi-stage `Dockerfile` that builds `cmd/web` and `cmd/poller` as plain Go
  binaries (no Node, no `templ` CLI, no Tailwind — those outputs are already committed
  to the repo) and a `migrate` target that runs the four goose directories.
- Add a `.dockerignore` that keeps secrets and dev-only files out of the build context
  and the image.
- Add `compose.yaml` with five services: `db` (PostgreSQL, healthchecked, persistent
  volume), `migrate` (one-shot goose runner), `web`, `poller`, and `caddy` (automatic
  HTTPS). Only `caddy` publishes ports.
- Add a `Caddyfile` for the reverse proxy.
- Add a daily Postgres backup script plus a `make` target and a cron line to run it on
  the VPS.
- Fix `internal/config.Load()` so a missing `.env` file is not an error. A real
  environment variable still wins over a `.env` value, unchanged.
- Add a first-time VPS deploy runbook to `docs/0-set-up/deployment.md`, and a new
  standalone Docker command reference at `docs/1-deploy/docker.md` for day-to-day use.
- Update `.env.example`, the root `README.md`, `cmd/README.md`, and
  `internal/config/AGENTS.md` for the changed behavior and the new deploy path.

## Impact

- **Breaking:** No. `internal/config.Load()`'s only behavior change is that a missing
  `.env` file no longer fails — every existing caller that has a `.env` file keeps
  working exactly as before. No public Go interface changes signature.
- **Modules affected:** `internal/config` (the `Load()` fix — the only Go code change
  in this proposal) plus cross-cutting repo-root infrastructure: `Dockerfile`,
  `.dockerignore`, `compose.yaml`, `Caddyfile`, a backup script, `Makefile` targets,
  and docs. No other `internal/` module changes.
- **Read paths affected:** None. This change adds no query, no table, no index, and no
  new read path. It only runs the existing schema and packages the existing binaries.
- **Database objects:** None added. See design.md → "Database objects" for the
  explicit statement that this change creates no table, column, index, constraint,
  view, or migration — it only runs the existing ones. The `database` design gate
  does not apply.
