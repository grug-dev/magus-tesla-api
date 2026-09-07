# Tasks — platform-add-docker-compose-deploy

> **Dependencies / parallelism.**
> - **T1** (`internal/config` fix + tests) has no dependencies. Pure, offline, no DB,
>   no container — write and test it FIRST, TDD-style, per design.md's test contract.
>   Disjoint files from every other task; MAY run in parallel with T2/T3/T4.
> - **T2** (`Dockerfile`, `.dockerignore`) has no dependencies — its contract (stage
>   names `web`/`poller`/`migrate`, binary paths) is fully fixed by design.md. Disjoint
>   files from T1/T3/T4; MAY run in parallel with them.
> - **T3** (`compose.yaml`, `deploy/Caddyfile`, `deploy/migrate-entrypoint.sh`,
>   `deploy/backup-db.sh`, `Makefile` targets) has no dependencies for the same reason —
>   it references T2's stage names, which design.md already fixes. Disjoint files from
>   T1/T2/T4; MAY run in parallel with them.
> - **T4** (`.env.example` additions) has no dependencies. Disjoint file; MAY run in
>   parallel with T1/T2/T3.
> - **T5a** (VPS runbook in `docs/0-set-up/deployment.md`), **T5b**
>   (`docs/1-deploy/docker.md`, new file), and **T5c** (`README.md`, `cmd/README.md`,
>   `internal/config/AGENTS.md`, cross-links) each depend on **T1, T2, T3, T4** — they
>   document exact commands, file names, and behavior that must already exist and be
>   final. T5a/T5b/T5c touch disjoint files and MAY run in parallel with each other,
>   once T1–T4 are done.
> - **T6** (verification) depends on **everything** — it is the final wave.
>
> **Leader-integrated step:** none. This change adds no database object and no sqlc
> input (design.md → "Database objects"), so no codegen re-run is needed beyond the
> ordinary `go build`/`go vet` signals in T6.

## T1. Fix `internal/config.Load()` for a missing `.env` — no dependencies, parallel-ok with T2/T3/T4

- [x] T1.1 Write `internal/config/config_test.go` FIRST (TDD-style), covering all four
      cases from design.md's test contract, before touching `config.go`:
      1. `.env` present with values → `Load()` succeeds, fields come from `.env`.
      2. `.env` absent, real env vars set → `Load()` succeeds (today this fails — the
         bug this task fixes), fields come from the environment.
      3. `.env` absent, required vars also missing → `Load()` still fails, with the
         existing `"TESLA_CLIENT_ID and TESLA_CLIENT_SECRET must be set in .env"`
         message (proves the missing-file fix does not swallow a real validation
         error).
      4. `.env` present AND a real env var set for the same key → the real env var
         wins (`godotenv.Load()`'s existing non-overriding behavior — a regression
         test, since this now matters far more for a container).
      Each subtest uses `t.TempDir()` + `os.Chdir` (restore original cwd in `defer`)
      and unsets/restores every env var it touches in `defer`, so it never leaks
      state into `quote_test.go` or `timezone_test.go` in the same package.
      Acceptance: the new test file compiles (`go vet ./internal/config/...`) and, by
      construction, cases 2 and 4 currently FAIL against the unmodified `config.go` —
      confirm that red state before T1.2.
- [x] T1.2 Change `internal/config/config.go`'s `Load()`:
      ```go
      if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
          return nil, fmt.Errorf("step 1: could not load .env file: %w", err)
      }
      ```
      Add `"errors"` and `"io/fs"` to the import block. No other line of `Load()`
      changes.
      Acceptance: all four T1.1 cases pass; `go vet ./internal/config/...` is clean;
      `gofmt -l internal/config/config.go` prints nothing.
- [x] T1.3 Update the doc comment on `Load()` to state the new behavior (a missing
      `.env` is not an error; a present one still loads; a real env var always wins)
      instead of the old "errors if... `.env` cannot be read" wording.

## T2. `Dockerfile` + `.dockerignore` — no dependencies, parallel-ok with T1/T3/T4

- [x] T2.1 Write the repo-root `Dockerfile` exactly per design.md's "multi-stage
      Dockerfile plan": one `builder` stage (`golang:1.25-alpine`) compiling
      `cmd/web`, `cmd/poller`, and `github.com/pressly/goose/v3/cmd/goose` (the
      `go.mod`-pinned `v3.27.3`, not a separately chosen version); three final stages
      named exactly `web`, `poller`, `migrate` (`compose.yaml` in T3 references these
      names), each `FROM alpine:3.20`, non-root `app` user, `ca-certificates`
      installed on `web`/`poller`/`migrate`, `wget` additionally on `web` (used by its
      compose healthcheck in T3).
      Acceptance: `docker build --target web .` and the `poller`/`migrate` targets are
      left for the owner to run (this pipeline does not execute `docker build` —
      see T6); the file itself must match design.md's plan exactly, stage names
      included.
- [x] T2.2 Write `.dockerignore` at the repo root: `.git`, `.env`, `.env.example`,
      `*.pem`, `private-key*`, `bin/`, `tmp/`, `.air/`,
      `internal/gateway/tools/tailwindcss`, `magus-public-key-netlify/`,
      `cmd/explore-tesla-api/output/`, `kkpa/`, `openspec/changes/archive/`.
      Acceptance: `.env` and every `*.pem`/`private-key*` pattern appear; `go.sum`,
      `go.mod`, and every `internal/*/db/migrations/*.sql` are NOT excluded (the
      `migrate` build stage needs them).

## T3. `compose.yaml`, Caddy, migrate entrypoint, backup script, Makefile — no dependencies, parallel-ok with T1/T2/T4

- [x] T3.1 Write `compose.yaml` with exactly the five services and the
      `depends_on`/healthcheck graph from design.md: `db` (`postgres:16-alpine`,
      named volume `pgdata`, `pg_isready` healthcheck, `restart: unless-stopped`),
      `migrate` (build target `migrate`, `depends_on: db: condition: service_healthy`,
      `env_file: .env`, `restart: "no"` — the deliberate exception, see design.md D2),
      `web` (build target `web`, `depends_on: migrate: condition:
      service_completed_successfully`, `env_file: .env`, no published port,
      `wget --spider http://localhost:8080/healthz` healthcheck, `restart:
      unless-stopped`), `poller` (build target `poller`, same `depends_on` as `web`,
      `env_file: .env`, no healthcheck, `restart: unless-stopped`), `caddy`
      (`caddy:2-alpine`, `ports: ["80:80", "443:443"]` — the ONLY service publishing
      ports, mounts `deploy/Caddyfile` read-only plus named volumes `caddy_data` and
      `caddy_config`, `depends_on: web`, `restart: unless-stopped`).
      Acceptance: `docker compose config` (a config-only lint — no build, no
      container start) is left for the owner to confirm; the file's service names,
      `depends_on` conditions, and restart policies must match design.md's graph
      exactly, including `migrate`'s `restart: "no"` exception.
- [x] T3.2 Write `deploy/Caddyfile`: one site block for `{$BASE_DOMAIN}` (read from
      the environment, or hardcode with a comment showing where to edit it),
      `reverse_proxy web:8080`, relying on Caddy's default automatic HTTPS (no manual
      ACME config needed for a public domain with ports 80/443 reachable).
- [x] T3.3 Write `deploy/migrate-entrypoint.sh`: a `#!/bin/sh` (or `bash`) script that
      loops the four migration directories COPIED into the `migrate` image
      (`/migrations/account`, `/migrations/telemetry`, `/migrations/charging`,
      `/migrations/analytics` — in that exact order, mirroring the Makefile's
      `MIGRATIONS_DIRS`), calling `goose -dir "$dir" postgres "$DATABASE_URL" up
      -allow-missing` for each, `set -e` so any failure stops the script and the
      container exits non-zero (which blocks `web`/`poller` from starting, per the
      `service_completed_successfully` condition).
      Acceptance: the four directory names and their order match the Makefile's
      `MIGRATIONS_DIRS` value byte-for-byte in intent (source paths differ only
      because they were `COPY`'d under `/migrations/` in the image).
- [x] T3.4 Write `deploy/backup-db.sh`: runs
      `docker compose exec -T db pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB"`,
      pipes to `gzip`, writes to a dated filename (e.g.
      `backups/magus-YYYY-MM-DD.sql.gz`), then deletes any file in that directory
      older than 7 days (`find ... -mtime +7 -delete`). Creates the `backups/`
      directory if missing.
- [x] T3.5 Add four `Makefile` targets (new; do not touch any existing target):
      `docker-up` (`docker compose up -d --build`), `docker-down`
      (`docker compose down`), `docker-logs` (`docker compose logs -f`), `backup-db`
      (`./deploy/backup-db.sh`). Add them to the existing `.PHONY` list.
      Acceptance: `grep -n '^docker-up:\|^docker-down:\|^docker-logs:\|^backup-db:'
      Makefile` shows all four; no existing target's recipe changed.

## T4. `.env.example` additions — no dependencies, parallel-ok with T1/T2/T3

- [x] T4.1 Add `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB` to `.env.example`,
      with a comment distinguishing them from `MAGUS_DB_PASSWORD` (host-only,
      `make db-setup`'s role bootstrap — untouched) and stating they provision the
      compose-local `db` service only; the app itself reads only `DATABASE_URL`.
      Add an example compose-local `DATABASE_URL` value using the `db` service
      hostname, e.g. `postgres://magus:CHANGE_ME@db:5432/magus?sslmode=disable`,
      alongside the existing `localhost`-based example (comment which is for local
      host dev vs. compose).

## T5a. VPS runbook — `docs/0-set-up/deployment.md` §8 — depends on T1, T2, T3, T4

Add a new "## 8. Docker Compose deploy (VPS / production)" section at the end of the
existing file (do not edit any existing section). It must be a **numbered, copy-paste
runbook** for someone who has never deployed to a VPS, covering at minimum:

- [x] T5a.1 **Buy/prepare the VPS.** Point at Hostinger's Ubuntu 22.04+ VPS plans (2 GB
      RAM minimum — Postgres + Go binaries + Caddy comfortably fit); note the VPS's
      public IP will be needed for the DNS step next.
- [x] T5a.2 **Point a DNS A record** at the VPS's public IP (exact steps: in the
      domain's DNS provider, add an `A` record for the chosen subdomain, e.g.
      `magus.example.com`, pointing at the VPS IP; note propagation can take up to a
      few hours, and Caddy's certificate issuance will fail until it resolves).
- [x] T5a.3 **Install Docker + Compose plugin** on the VPS — exact commands (Ubuntu):
      ```bash
      curl -fsSL https://get.docker.com | sudo sh
      sudo usermod -aG docker $USER
      ```
      (log out and back in for the group change to take effect), then verify with
      `docker compose version`.
- [x] T5a.4 **Clone the repo** on the VPS:
      ```bash
      git clone <repo-url> magus-tesla-api && cd magus-tesla-api
      ```
- [x] T5a.5 **Create `.env`** from `.env.example` (`cp .env.example .env`), then fill
      every value — link to §2 of this same doc for the Tesla/Google credential
      fields, and to T4.1's new `POSTGRES_USER`/`POSTGRES_PASSWORD`/`POSTGRES_DB` +
      compose-shaped `DATABASE_URL`. Set `BASE_URL=https://<the DNS name from T5a.2>`.
- [x] T5a.6 **Generate `SESSION_SECRET`**: `openssl rand -hex 32`, paste into `.env`.
- [x] T5a.7 **Add the production redirect URIs** to Google Cloud Console
      (`<BASE_URL>/auth/google/callback`) and to the Tesla developer app
      (`<BASE_URL>/connect/tesla/callback`) — both alongside the existing localhost
      entries, never replacing them.
- [x] T5a.8 **First deploy**: `docker compose up -d --build` (or `make docker-up`).
      Explain what happens: `db` starts and becomes healthy, `migrate` runs all
      pending migrations and exits 0, `web`/`poller` start, `caddy` issues its
      certificate.
- [x] T5a.9 **Verify**: `curl -I https://<domain>/healthz` — expect `HTTP/2 200`. Also
      `docker compose ps` — expect every long-running service `Up` (or `healthy`) and
      `migrate` `Exited (0)`.
- [x] T5a.10 **Set up the daily backup cron**:
      ```bash
      crontab -e
      # add:
      0 2 * * * cd /path/to/magus-tesla-api && make backup-db >> /var/log/magus-backup.log 2>&1
      ```
- [x] T5a.11 **Deploying an update, afterwards**:
      ```bash
      git pull
      docker compose up -d --build
      ```
      One link at the end of this section to `docs/1-deploy/docker.md` for every
      other day-to-day command (logs, restart one service, rollback, etc.) — no
      second copy of that content here.

## T5b. New Docker command reference — `docs/1-deploy/docker.md` — depends on T1, T2, T3, T4

- [x] T5b.1 Create the new folder `docs/1-deploy/` and the file
      `docs/1-deploy/docker.md`, following design.md's "Docker command reference doc"
      section for structure and content, in this order: (1) the mental model table of
      all five services, (2) local Docker use vs. `make up`/host dev — when to use
      which, (3) a one-paragraph pointer to `docs/0-set-up/deployment.md` §8 for first
      deploy (no duplicated steps), (4) an everyday-commands table (start, stop,
      restart one service, rebuild, follow one service's logs, follow all logs, list
      running services, check health), (5) the exact update-deploy sequence, (6)
      migrations (checking status via `docker compose logs migrate`, how it re-runs
      safely on every `up`, re-running by hand), (7) database access (`psql` inside
      `db`, one ad-hoc query, manual dump, restore), (8) backups (where files land,
      confirming the cron ran, the exact restore command), (9) a troubleshooting
      table covering at least: `web` will not start, `migrate` exits non-zero, Caddy
      cannot get a certificate, `poller` not collecting, "database connection
      refused", (10) a safety section naming `docker compose down -v` explicitly as
      destroying the `pgdata` volume and all data, with a clear safe-vs-destructive
      command list, (11) the four-step "switch to a managed database" recap from
      design.md D1.
      Every command in its own fenced code block with a one-line plain-words purpose
      immediately above it; no unexplained placeholder — every `<...>` must say where
      to find the real value.
- [x] T5b.2 Add the required cross-links: this file links back to
      `docs/0-set-up/deployment.md` §8 (for first-time setup); `docs/0-set-up/deployment.md`
      §8 (T5a.11) links forward to this file (for day-to-day commands).

## T5c. Remaining docs — `README.md`, `cmd/README.md`, `internal/config/AGENTS.md` — depends on T1, T2, T3, T4

- [x] T5c.1 `README.md` "Project Structure" tree: add `Dockerfile`, `.dockerignore`,
      `compose.yaml`, and `deploy/` as new top-level entries.
- [x] T5c.2 `README.md` "Deployment (new machine / production)" section: add a link to
      `docs/1-deploy/docker.md` alongside the existing link to
      `docs/0-set-up/deployment.md`, one sentence distinguishing the two ("first-time
      setup" vs. "day-to-day Docker commands").
- [x] T5c.3 `cmd/README.md`: add a note under the `cmd/web` and `cmd/poller` table rows
      that both are built into containers by the repo-root `Dockerfile` for
      production, linking to `docs/1-deploy/docker.md`.
- [x] T5c.4 `internal/config/AGENTS.md` "Public interface" bullet for `Load()`: update
      "errors if ... `.env` cannot be read" to describe the new behavior — a missing
      `.env` file is not an error (falls back to real environment variables); a
      present `.env` still loads; a real environment variable always wins over a
      `.env` value.
- [x] T5c.5 Confirm the `kkpa/context/` grep check (already run once at proposal time:
      `grep -rl "internal/config\|POLLER_TIMEZONE\|godotenv\|\.env\b"
      kkpa/context/` returned no hits — there is no KB guide to update for this
      change). Re-run it once more before archiving, since T1–T5b may have introduced
      new grep-able terms (e.g., "docker", "compose") that still map to no existing
      guide, so this stays a no-op — but the check itself must be re-run, not assumed.

## T6. Verification — depends on EVERYTHING (final wave)

- [ ] T6.1 `go build ./...`, `go vet ./...`, `gofmt -l .` — all clean (T1's fix and its
      new test file are the only Go changes in this entire proposal).
- [ ] T6.2 `make migration-guard`, `make boundary-guard`, `make archive-guard` — all
      pass (design.md's reverse-direction check predicts no impact; this confirms it).
- [ ] T6.3 `openspec validate --strict platform-add-docker-compose-deploy` — passes.
- [ ] T6.4 Hand the owner the commands this pipeline does not run: the test suite
      (`go test ./...` / `make test`), plus everything Docker-specific this pipeline
      has no authorization to execute — `docker compose config` (config lint),
      `docker build --target web . && docker build --target poller . && docker build
      --target migrate .` (image builds), and a real `docker compose up -d --build`
      smoke test (locally or on the VPS). None of these are covered by
      `Test-Execution-Policy`'s allowed list (`go build`/`go vet`/`gofmt`/`make
      build`/`make vet`/`make bins`/the standalone guards) — Docker execution is the
      owner's step, same as the test suite.

## T2b. Fix D2's impossible goose-CLI build — blocker fix, see design.md amendment (D10, D11)

`go build github.com/pressly/goose/v3/cmd/goose` fails: this project depends on goose
as a library only, so `go.sum` has no entries for 8 optional driver modules the CLI's
`main` package imports. `docker build --target migrate` would fail on the VPS. Fixed by
replacing the goose CLI with a small `cmd/migrate` Go program using the goose library.

- [x] T2b.1 Write `cmd/migrate/main.go`: a new runnable using
      `goose.NewProvider(goose.DialectPostgres, db, os.DirFS(dir),
      goose.WithAllowOutofOrder(true))`, one provider per directory, applied in
      order (account, telemetry, charging, analytics). Reads `DATABASE_URL` directly
      with `os.Getenv` (justified in design.md D11 — `config.Load()`'s Tesla-credential
      validation is the wrong fit). Reads the migrations root from `MIGRATIONS_ROOT`
      (default `/migrations`). Does not import `internal/testdb`. Exits non-zero on any
      failure. Logs which directory it applies and how many migrations it ran.
      Acceptance: `go build ./cmd/migrate` succeeds; `go vet ./cmd/migrate` is clean;
      `gofmt -l cmd/migrate` prints nothing.
- [x] T2b.2 Update the `Dockerfile`: builder stage builds `/out/migrate` from
      `./cmd/migrate` instead of the goose CLI; `migrate` final stage ships that binary
      as `ENTRYPOINT` instead of `goose` + the shell script. Keep the four `COPY` lines
      for the migration directories and the non-root `app` user.
- [x] T2b.3 Delete `deploy/migrate-entrypoint.sh`. The Go binary now owns the loop over
      directories, so the script would be a second, driftable copy of the same logic.
- [x] T2b.4 Pin `PORT=8080` in `compose.yaml`'s `web` service `environment:` block, with
      a comment explaining why: the healthcheck and `deploy/Caddyfile` both hardcode
      `web:8080`, so a `PORT` set in `.env` for local host dev would otherwise silently
      break both.
- [x] T2b.5 Append the amendment section to `design.md` recording D10 and D11, the exact
      `go build` error that proved D2 impossible, and this task. Do not edit or remove
      D2 itself.
      Acceptance: `make archive-guard` still passes (this change is not yet archived,
      so this is a no-op check, run anyway per policy).

## T7. Fix `cmd/migrate`'s `os.Getenv` convention violation — found during doc review, depends on T2b

D11's `cmd/migrate` reads `DATABASE_URL`/`MIGRATIONS_ROOT` directly with
`os.Getenv`, documented there as a deliberate deviation from
`ai/go-conventions.md`'s "no `os.Getenv` outside `internal/config`" rule. The
justification was real (`config.Load()` also validates Tesla credentials, which
a migration-only tool must not require), but the correct fix is a small typed
loader in `internal/config`, not a standing exception.

- [x] T7.1 Add `internal/config.LoadMigration() (*MigrationConfig, error)`:
      loads `.env` the same tolerant way `Load()` does (missing-file is not
      fatal), does NOT require any Tesla credential, errors when `DATABASE_URL`
      is empty, and defaults `MigrationsRoot` to `/migrations` when
      `MIGRATIONS_ROOT` is unset. The missing-`.env` logic is factored into one
      shared `loadDotEnv()` helper used by both `Load()` and `LoadMigration()`.
      Acceptance: `go build ./...` and `go vet ./...` clean.
- [x] T7.2 Change `cmd/migrate/main.go` to call `config.LoadMigration()`.
      Remove both `os.Getenv` calls and the local `defaultMigrationsRoot`
      const. Replace the doc-comment paragraph describing the `os.Getenv`
      deviation with one line saying config comes from `internal/config`.
      Acceptance: `grep -rn "os.Getenv" cmd/` returns nothing.
- [x] T7.3 Add tests for `LoadMigration()` to `internal/config/config_test.go`
      (appended, existing tests untouched): `.env` missing with `DATABASE_URL`
      set in the real environment; `DATABASE_URL` empty gives an error;
      `MIGRATIONS_ROOT` unset defaults to `/migrations`; `MIGRATIONS_ROOT` set
      is honored.
- [x] T7.4 Update `internal/config/AGENTS.md`'s "Public interface" section to
      list `LoadMigration`, and fix its stale "Testing" note (it said `Load()`
      was not unit-tested directly — T1 already added direct tests for it).

## T8. Make `cmd/migrate` runnable locally — found from the owner's own question,
depends on T7

`cmd/migrate` only worked inside the Docker image, because it built its four
migration paths as `MigrationsRoot + "/" + module` — a layout the Dockerfile
creates but a repo checkout does not have. The owner asked how to run it and
found there was no way to. Fixed by teaching `LoadMigration()` an optional,
ordered `MIGRATIONS_DIRS` env var and adding two `make` targets.

- [x] T8.1 Add `MigrationsDirs []string` to `internal/config.MigrationConfig`
      and resolve it in `LoadMigration()`: when `MIGRATIONS_DIRS` is set, split
      it on whitespace (dropping empty entries) and use it, in order; when
      unset, fall back to `MigrationsRoot + "/" + module` for each of the four
      module names, in order — unchanged image-default behavior. Keep
      `MigrationsRoot` on the struct.
      Acceptance: `go build ./...` and `go vet ./...` clean; `gofmt -l
      internal/config` prints nothing.
- [x] T8.2 Change `cmd/migrate/main.go` to loop `cfg.MigrationsDirs` instead of
      building paths itself. Delete the local `moduleDirs` var. Update the file
      doc comment to explain both ways to run it: the image default, and
      `MIGRATIONS_DIRS` for a local run.
      Acceptance: `grep -rn "os.Getenv" cmd/` returns nothing (still true —
      T8 does not reintroduce it); `go build ./cmd/migrate` succeeds.
- [x] T8.3 Append tests to `internal/config/config_test.go` (existing tests
      untouched): `MIGRATIONS_DIRS` set to two paths gives exactly those two in
      order; `MIGRATIONS_DIRS` unset gives the four default paths in account,
      telemetry, charging, analytics order; extra whitespace between entries
      produces no empty directory.
- [x] T8.4 Add two `Makefile` targets, next to the other `docker-`/`migrate-`
      targets, each with a `## ` help comment: `migrate-run` (runs `cmd/migrate`
      locally against `DATABASE_URL`, passing the Makefile's own
      `MIGRATIONS_DIRS` — does NOT depend on `check-goose`, since it runs the Go
      program, not the goose CLI) and `docker-migrate` (`docker compose run --rm
      migrate`). Change no existing target.
      Acceptance: `make help | grep -E "migrate-run|docker-migrate"` shows both.
- [x] T8.5 Update docs in the same change: `docs/1-deploy/docker.md` §6
      documents `make docker-migrate` and `make migrate-run`, stating plainly
      how `migrate-run` differs from `migrate-up` (same migrations, same order;
      `migrate-run` uses the Go program the container runs, `migrate-up` uses
      the goose CLI). `cmd/README.md`'s `cmd/migrate` row says how to run it
      (`make migrate-run` / `make docker-migrate`). `internal/config/AGENTS.md`
      records the new `MIGRATIONS_DIRS` variable and its test coverage.
