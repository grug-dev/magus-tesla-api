# Design — platform-add-docker-compose-deploy

## Context

The repo has two long-running processes (`cmd/web`, `cmd/poller`), one one-shot local
tool (`cmd/setup`), and one tool that must never run in production
(`cmd/explore-tesla-api` — costs a real paid Tesla API call and wakes the car). All
generated code (41 `*_templ.go` files, `internal/gateway/static/app.css`) is committed
and `//go:embed`ed, so building the image is a plain `go build ./cmd/...` — no Node, no
`templ` CLI, no Tailwind binary at build or run time.

Migrations are four goose directories applied in a fixed order against one shared
`goose_db_version` table (`ai/go-conventions.md` §Persistence):

```
internal/account/db/migrations
internal/telemetry/db/migrations
internal/charging/db/migrations
internal/analytics/db/migrations
```

`make migrate-up` already applies them with `-allow-missing`, for the reason documented
in the Makefile: modules never share tables, so a module's migration version can sit
below another module's already-applied version without meaning anything is wrong.

`internal/config.Load()` currently returns an error when `godotenv.Load()` fails to
find `.env`. A container has no `.env` file by default — it receives configuration as
real environment variables. Every other line of `Load()` already works correctly with
real env vars; only this one guard is wrong for a container.

## Goals / Non-Goals

**Goals:**
- A `git pull && docker compose up -d --build` deploy on a small VPS, with automatic
  restarts, a healthchecked Postgres, and the app waiting for the database and the
  migrations before it starts.
- `DATABASE_URL` stays the one switch between the bundled `db` service and a future
  managed database.
- A first-time VPS runbook a Go/htmx beginner who has never used Docker can follow
  literally, command by command.

**Non-Goals:**
- No registry, no CI/CD pipeline. The image is built on the VPS in this change
  (D4) — but the Dockerfile and compose file are written so switching to a registry
  later is a one-line change.
- No data migration from the owner's local database. The VPS starts empty (D5).
- No new database object of any kind (see "Database objects" below).
- No Kubernetes, no service mesh, no secrets manager. `.env` + compose `env_file:` is
  the whole secrets story, matching the ticket's "keep it simple" instruction.

## Decisions

### D1 — PostgreSQL runs as a compose service; `DATABASE_URL` is the portability switch

A `db` service uses the official `postgres:16-alpine` image (matching the version
already named in `docs/0-set-up/deployment.md` §1, `postgresql@16`), with a named
volume `pgdata:/var/lib/postgresql/data` and a healthcheck (`pg_isready`).

**Why:** the ticket asks for "PostgreSQL configuration" and "persistent PostgreSQL
data" as part of one simple first deploy — no managed database exists yet, and
provisioning one is a separate decision the owner has not made. A compose-bundled
database is the smallest thing that satisfies the ticket today.

**The portability requirement, made concrete:** `DATABASE_URL` is read by exactly two
places — `internal/config` (the app processes) and the `migrate` service's entrypoint
script. Neither ever hardcodes a hostname. The compose-local `db` service is reached
by its *service name* (`db`), which only resolves inside the compose network — so
`DATABASE_URL=postgres://<user>:<pass>@db:5432/<name>?sslmode=disable` in `.env` is
already an ordinary DSN, not a compose-specific format. Moving to a managed host
(RDS, Cloud SQL, Supabase, a Hostinger managed Postgres) is:

1. Change `.env`'s `DATABASE_URL` to the managed host's DSN.
2. Delete the `db` service block from `compose.yaml` (or comment it out).
3. Remove `db: condition: service_healthy` from `migrate`'s `depends_on` (nothing
   else depends on `db` directly — `web` and `poller` depend on `migrate`, not `db`).
4. `docker compose up -d --build`.

No code change, no new environment variable, no proxy layer. This is deliberately
**not** abstracted behind an extra indirection (e.g., a database-locator service) —
`DATABASE_URL` already is that indirection, and the project's own AI-efficiency rule
says not to add a layer where the existing primitive already does the job.

**Rejected:** requiring a managed database from day one. Rejected by the owner
(ticket: "small Hostinger VPS", "keep the setup simple") — a managed DB is real money
and a second thing to provision correctly before the first deploy, for a hobby project
whose owner has never deployed to a VPS before.

**Provisioning constraint, not a choice:** the official `postgres` image creates its
role and database from `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` on the
first boot of an *empty* volume. This is how `db` gets its role, and it fully replaces
`make db-setup` for the compose path — `make db-setup`'s interactive role-creation
flow stays host-only, for local dev on a bare Postgres install, and is untouched by
this change. Two new `.env` variables carry these: `POSTGRES_USER`, `POSTGRES_PASSWORD`,
`POSTGRES_DB` (compose-local only; the app itself never reads them, only `DATABASE_URL`).
They are a different secret from `MAGUS_DB_PASSWORD`, which stays local-dev-only and is
untouched.

### D2 — A one-shot `migrate` service applies the migrations, in order, then exits

A `migrate` service, built from the same `Dockerfile` (a distinct final stage), runs a
small entrypoint script that loops the four migration directories in the exact
`MIGRATIONS_DIRS` order from the Makefile, each with `goose ... up -allow-missing` —
mirroring `make migrate-up` exactly so the two paths cannot silently diverge.

**How goose and the 42 `.sql` files reach the service:** the `migrate` build stage
`COPY`s the four `db/migrations` directories (only those — not the whole repo) and
builds the `goose` CLI itself from the project's own pinned dependency:
`github.com/pressly/goose/v3 v3.27.3` is already a `go.mod` require (used as a library
by `internal/testdb`), so the Dockerfile builds it with
`go build -o /out/goose github.com/pressly/goose/v3/cmd/goose` in the builder stage —
the exact version `go.sum` pins, not a separately-tracked Docker-only version. This
removes one moving part: there is only ever one goose version in this project, the one
`go.mod` already names.

`web` and `poller` both `depends_on: migrate: condition: service_completed_successfully`
— this is the ticket's "app/worker must wait for PostgreSQL to be ready" requirement,
satisfied transitively: `migrate` itself waits on `db`'s healthcheck, and `web`/`poller`
wait on `migrate`'s clean exit, so neither ever opens a pool against a database that is
not both up *and* migrated.

**`migrate` is exempt from `restart: unless-stopped`.** Every long-running service in
this design gets `restart: unless-stopped` (the ticket's "automatic container
restarts" requirement) — `db`, `web`, `poller`, `caddy`. `migrate` is deliberately
different: it is designed to run once and exit 0. Giving it `restart: unless-stopped`
would make compose restart a container that already finished successfully, which
fights the "`service_completed_successfully`" condition `web`/`poller` depend on and
serves no purpose — a migration that already applied cleanly has nothing left to do on
a restart. `migrate` uses the default `restart: "no"`. This is called out explicitly
here because it is the one place this design does not apply the "every service
restarts" rule uniformly, and that is a deliberate exception, not an oversight.

**Rejected:** running migrations from inside `web`'s own entrypoint (a common
shortcut). Rejected because it couples a multi-tenant web server's startup to schema
changes it doesn't own, and because with two consumers of the schema (`web` and
`poller`) it would either run twice (harmless here, since goose is idempotent, but
wasteful and confusing in logs) or need one of them arbitrarily designated "the one
that migrates." A dedicated one-shot service is one job, one place, one log stream.

### D3 — Caddy is in compose for automatic HTTPS

A `caddy` service (`caddy:2-alpine`) is the only service that publishes ports (`80`,
`443`). It reverse-proxies to `web:8080` over the private compose network and manages
Let's Encrypt certificates automatically via a `Caddyfile`.

**Why this earns its place, not "unnecessary infrastructure":** Google OAuth's
authorized-redirect-URI rules permit plain `http://` only for `localhost`. Any public
domain must be `https`. `BASE_URL` (used to build the Google + Tesla redirect URIs)
therefore must be a public `https://` URL the moment this is deployed anywhere but a
developer's own machine — without HTTPS, nobody can log in at all. Caddy gets this for
free (automatic cert issuance and renewal) with a four-line `Caddyfile`, no manual
`certbot` cron job, no separate nginx + certbot pairing to keep in sync.

`web`, `poller`, and `db` publish **no** ports. Only Caddy is reachable from outside
the VPS — this is the smallest network exposure that still serves HTTPS.

**Rejected:** terminating TLS in the Go binary itself (`http.ListenAndServeTLS`).
Rejected because it means hand-rolling ACME certificate issuance and renewal inside
`cmd/web`, which is real complexity for a single-binary hobby project, and it couples
an application restart/redeploy to certificate lifecycle. Rejected also: exposing
`web`'s port directly with no reverse proxy. That fails the HTTPS requirement outright
and needlessly widens the VPS's attack surface.

### D4 — The image is built on the VPS; no registry, no CI, in this change

Deploy is `git pull && docker compose up -d --build`. `compose.yaml`'s `web` and
`poller` services use `build: { context: ., dockerfile: Dockerfile, target: ... }`.

**Why:** the ticket asks for the *first* deploy on a small VPS, done by someone who has
never deployed to a VPS before. A registry (Docker Hub, GHCR) and a CI pipeline that
pushes to it are a second and third thing to set up correctly before the first "hello
world" deploy succeeds. Building on the VPS is one command, uses the VPS's own CPU
(cheap, since deploys are infrequent for a hobby project), and needs no new account or
credential.

**Kept as a one-line upgrade path, deliberately:** switching to a registry later means
replacing `build: {...}` with `image: <registry>/<repo>:<tag>` in `compose.yaml` for
`web`/`poller`/`migrate` — no other file changes, because the Dockerfile's stages,
the compose network, volumes, and `depends_on` graph are all independent of where the
image comes from. This is stated here so nobody re-derives it as "we'd have to
redesign compose" later.

**Rejected:** a registry + CI from day one. Rejected as scope the ticket does not ask
for and the owner does not yet need — "avoid unnecessary infrastructure."

### D5 — The VPS starts with an empty database

No dump/restore of the owner's local history is part of this change. The VPS's
`poller` service starts collecting fresh telemetry from its first scheduled run.

**Why:** this is an explicit owner decision (per the leader's interview), not a
default. It keeps the first deploy to "stand the container stack up and confirm it
works," with data backfill treated as a separate, later concern if the owner ever
wants the VPS to inherit history. No task in this change performs a `pg_dump`/`pg_restore`
of the local database, and none should be added without a new decision.

### D6 — A daily `pg_dump` backup job runs on the VPS

A small shell script (`deploy/backup-db.sh`) runs `pg_dump` against the `db` service
(via `docker compose exec -T db pg_dump ...`), gzips the output to a dated filename,
and deletes any backup file older than 7 days. It is wired as a `make backup-db` target
(runnable by hand) and one crontab line that runs it daily on the VPS.

**Why this survives D5's "start empty":** the moment the VPS has *any* row it did not
inherit from the owner's machine (D5), that row exists nowhere else. A VM disk failure,
an accidental `docker compose down -v` (see the safety note in the Docker command
reference doc), or a bad migration would otherwise lose it permanently. A backup
matters more, not less, on a database with no other copy of its data.

**Rejected:** a managed backup service (e.g., a paid Postgres-as-a-service snapshot
feature). Rejected together with D1's "no managed DB yet" — there is no managed
service to attach a backup feature to. **Rejected:** WAL-archiving/point-in-time
recovery. Rejected as more operational surface than a hobby project's single nightly
poller write pattern justifies; a daily full dump is proportionate to how often the
data actually changes (once a day, at 03:30, per `ai/architecture.md` §7).

## File list

| File | Purpose |
|---|---|
| `Dockerfile` | Multi-stage build. Builder stage compiles `cmd/web`, `cmd/poller`, and the pinned `goose` CLI. Three final stages (`web`, `poller`, `migrate`), each minimal and non-root. |
| `.dockerignore` | Keeps `.git`, `.env`, `*.pem`, `private-key*`, `bin/`, `tmp/`, `.air/`, the git-ignored Tailwind binary, and other dev-only paths out of the build context and the image. |
| `compose.yaml` | Orchestrates `db`, `migrate`, `web`, `poller`, `caddy`. Repo-root, cross-cutting. |
| `deploy/Caddyfile` | Reverse proxy + automatic HTTPS config for the `caddy` service. |
| `deploy/migrate-entrypoint.sh` | Loops the four `MIGRATIONS_DIRS`, in order, calling `goose ... up -allow-missing` for each — the `migrate` service's `ENTRYPOINT`. |
| `deploy/backup-db.sh` | Daily `pg_dump` + gzip + 7-day retention. Invoked by `make backup-db` and by cron on the VPS. |
| `internal/config/config.go` | `Load()` fix: a missing `.env` is no longer a fatal error (D-config below). |
| `internal/config/config_test.go` | New. Tests the fix per the test contract below. |
| `Makefile` | New targets: `docker-up`, `docker-down`, `docker-logs`, `backup-db` (thin wrappers; no change to any existing target). |
| `.env.example` | Add `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB` (compose-local Postgres provisioning, D1), with a comment distinguishing them from `MAGUS_DB_PASSWORD` (host-only). |
| `docs/0-set-up/deployment.md` | Extended with a new "§8 — Docker Compose / VPS deploy" runbook section. Existing host-install sections are untouched. |
| `docs/1-deploy/docker.md` | **New file, new folder.** A standalone, come-back-to-it Docker command reference — distinct from the one-time runbook above. See "Docker command reference doc" below. |
| `README.md` | "Project Structure" tree gains the new root files; "Deployment" section links to both docs above; "Making a change" is unaffected (no module/table added). |
| `cmd/README.md` | Note that `cmd/web` and `cmd/poller` are the two binaries built into containers by the new `Dockerfile`, linking to `docs/1-deploy/docker.md`. |
| `internal/config/AGENTS.md` | Update "Public interface" — `Load()`'s missing-`.env` behavior changes from "errors" to "falls back to the real environment." |

## The `depends_on` / healthcheck graph

```
db (postgres:16-alpine)
 ├─ healthcheck: pg_isready -U ${POSTGRES_USER} -d ${POSTGRES_DB}
 │    interval 5s, timeout 5s, retries 10, start_period 10s
 ├─ volume: pgdata:/var/lib/postgresql/data (named, persistent)
 └─ restart: unless-stopped
      │
      │ depends_on: db (condition: service_healthy)
      ▼
migrate (build target: migrate)
 ├─ env_file: .env   (DATABASE_URL, and everything else it does not need but inherits)
 ├─ runs: deploy/migrate-entrypoint.sh → goose up -allow-missing × 4 dirs, then exits 0
 └─ restart: "no"   (deliberate exception — see D2)
      │
      │ depends_on: migrate (condition: service_completed_successfully)
      ├──────────────────────────────┐
      ▼                              ▼
web (build target: web)        poller (build target: poller)
 ├─ env_file: .env              ├─ env_file: .env
 ├─ no published ports          ├─ no published ports
 ├─ healthcheck: wget --spider  ├─ (no HTTP surface to healthcheck; long-lived
 │    http://localhost:8080/    │    process — Docker's own restart policy is
 │    healthz                   │    the liveness signal)
 └─ restart: unless-stopped     └─ restart: unless-stopped
      ▲
      │ reverse_proxy web:8080
caddy (caddy:2-alpine)
 ├─ ports: 80:80, 443:443   (the ONLY service publishing ports)
 ├─ volumes: deploy/Caddyfile (ro), caddy_data, caddy_config (named, persistent — certs)
 ├─ depends_on: web (no health condition — Caddy retries a down upstream on its own)
 └─ restart: unless-stopped
```

`web`'s healthcheck reuses the existing `/healthz` route
(`internal/gateway/gateway.go`, `Healthz` in `internal/gateway/handlers/handlers.go`) —
already a DB-reachability probe (`pool.Ping`), so it costs zero new application code.
It is not one of the ticket's explicit requirements (only Postgres's healthcheck is
required) but is added because `web` already exposes the exact endpoint for it and
`docker compose ps` benefits from an honest status. `poller` has no HTTP surface, so it
gets no Docker healthcheck; its liveness signal is the process staying up, which
`restart: unless-stopped` already covers.

## The multi-stage `Dockerfile` plan

```
# ---- builder ----
FROM golang:1.25-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/web    ./cmd/web
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/poller ./cmd/poller
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/goose  github.com/pressly/goose/v3/cmd/goose

# ---- web ----
FROM alpine:3.20 AS web
RUN apk add --no-cache ca-certificates wget && \
    addgroup -S app && adduser -S app -G app
COPY --from=builder /out/web /usr/local/bin/web
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/web"]

# ---- poller ----
FROM alpine:3.20 AS poller
RUN apk add --no-cache ca-certificates && \
    addgroup -S app && adduser -S app -G app
COPY --from=builder /out/poller /usr/local/bin/poller
USER app
ENTRYPOINT ["/usr/local/bin/poller"]

# ---- migrate ----
FROM alpine:3.20 AS migrate
RUN apk add --no-cache ca-certificates bash && \
    addgroup -S app && adduser -S app -G app
COPY --from=builder /out/goose /usr/local/bin/goose
COPY internal/account/db/migrations   /migrations/account
COPY internal/telemetry/db/migrations /migrations/telemetry
COPY internal/charging/db/migrations  /migrations/charging
COPY internal/analytics/db/migrations /migrations/analytics
COPY deploy/migrate-entrypoint.sh /migrate-entrypoint.sh
RUN chmod +x /migrate-entrypoint.sh
USER app
ENTRYPOINT ["/migrate-entrypoint.sh"]
```

Every final stage starts `FROM alpine`, not `FROM scratch`: `ca-certificates` is needed
because `web` and `poller` both call the Tesla Fleet API and Google's OAuth endpoints
over HTTPS, and Alpine keeps the image small (a few MB base) while still giving `sh`
for debugging (`docker compose exec web sh`). All three run as the non-root `app`
user. `CGO_ENABLED=0` keeps the binaries statically linked so they run on Alpine's musl
libc with no glibc compatibility layer needed.

`.dockerignore` excludes `.git/`, `.env`, `.env.example`, `*.pem`, `private-key*`,
`bin/`, `tmp/`, `.air/`, `internal/gateway/tools/tailwindcss` (large, platform-specific,
not needed — `app.css` is already committed and embedded), `magus-public-key-netlify/`,
`cmd/explore-tesla-api/output/`, `kkpa/`, and `openspec/changes/archive/` (build-context
hygiene only — excluding it from the Docker build context has no bearing on
`make archive-guard`, which is a git-history check, not a Docker one).

## Test contract for `internal/config` — authored now, before implementation

The fix: `Load()` currently does

```go
if err := godotenv.Load(); err != nil {
    return nil, fmt.Errorf("step 1: could not load .env file: %w", err)
}
```

It becomes:

```go
if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
    return nil, fmt.Errorf("step 1: could not load .env file: %w", err)
}
```

`errors.Is(err, fs.ErrNotExist)` is used (not the older `os.IsNotExist`) because it
correctly unwraps a wrapped `*fs.PathError`, which is what `godotenv.Load()` returns
when `.env` does not exist (it opens the file with `os.Open` before parsing). Any
other error — a permission error, a malformed `.env` — is still fatal, unchanged.
`godotenv.Load()` (not `Overload()`) never overrides a variable already present in the
process environment, so "a real env var beats a `.env` value" is existing, unchanged
behavior; this change only adds a regression test for it, since it now matters far
more (every container env var must survive untouched).

Following `ai/go-conventions.md` §Testing's authoring order: this is a pure, offline
fix with no DB dependency, so its tests are written now, early, TDD-style, and `go vet`
will confirm they compile before any implementation worker touches `config.go`.

**Test 1 — `.env` present with values.** `t.TempDir()`, write a `.env` file there with
`TESLA_CLIENT_ID=fromdotenv` and `TESLA_CLIENT_SECRET=fromdotenv`, `os.Chdir` into it
(restore the original cwd in `defer`), ensure `TESLA_CLIENT_ID`/`TESLA_CLIENT_SECRET`
are unset in the real environment beforehand. Call `Load()`.
**Expected:** no error; `cfg.ClientID == "fromdotenv"`, `cfg.ClientSecret == "fromdotenv"`.

**Test 2 — `.env` absent, real env vars set.** `t.TempDir()` with no `.env` file,
`os.Chdir` into it, `os.Setenv("TESLA_CLIENT_ID", "fromenv")` and
`os.Setenv("TESLA_CLIENT_SECRET", "fromenv")` (unset both in `defer`). Call `Load()`.
**Expected:** no error (this is the exact regression this change fixes — today this
case returns `"step 1: could not load .env file"`); `cfg.ClientID == "fromenv"`.

**Test 3 — `.env` absent, required vars missing.** `t.TempDir()` with no `.env` file,
`os.Chdir` into it, ensure `TESLA_CLIENT_ID`/`TESLA_CLIENT_SECRET` are unset. Call
`Load()`. **Expected:** an error, and it is the existing
`"step 1: TESLA_CLIENT_ID and TESLA_CLIENT_SECRET must be set in .env"` message — proving
a missing `.env` file alone is not fatal, but a missing *required value* still is,
regardless of source.

**Test 4 — a real env var beats a `.env` value.** `t.TempDir()`, write a `.env` file
with `TESLA_CLIENT_ID=fromdotenv`, `os.Chdir` into it, also
`os.Setenv("TESLA_CLIENT_ID", "fromenv")` before calling `Load()` (restore/unset both
in `defer`). **Expected:** no error; `cfg.ClientID == "fromenv"` — the real environment
variable wins, `.env` never overrides it.

Each test must save and restore the working directory and every env var it touches in
`defer`, so the four tests (and the two pre-existing files, `quote_test.go` and
`timezone_test.go`) never leak state into each other — `go test` runs a package's
tests in one process. No test in this contract touches a database or a container,
matching `internal/config/AGENTS.md`'s existing testing note (pure, offline,
table-driven).

## Database objects

**This change creates no table, column, index, constraint, view, or migration.** It
only *runs* the four existing migration directories, inside the `migrate` service,
exactly as `make migrate-up` already does on a host. There is no new schema for the
`database` design gate to review. If a future change needs a new database object for
deploy tooling (for example, a backup-run log table), that is a new, separate decision
— it does not belong in this change, and none was added here.

## Reverse-direction check — existing `make` targets and guards

Verified against the actual `Makefile` and `openspec/config.yaml`, not assumed:

- **`MIGRATIONS_DIRS` order.** Unaffected. `deploy/migrate-entrypoint.sh` hardcodes the
  same four directories in the same order the Makefile already uses
  (`account, telemetry, charging, analytics`) — it is a second consumer of the same
  fact, not a new source of truth. If a fifth module ever gains a migrations
  directory, both the Makefile's `MIGRATIONS_DIRS` and this script need the addition;
  this is noted as a two-place fact so a future change does not update one and miss
  the other.
- **`db-setup` / `db-reset` role-and-ownership assumptions.** Unaffected on the host
  path — both targets are untouched and remain for local/host dev, connecting as the
  OS superuser to create `magusadmindb`. They are simply not used by the compose path,
  which uses the official Postgres image's own first-boot `POSTGRES_USER` /
  `POSTGRES_PASSWORD` / `POSTGRES_DB` provisioning instead (D1). The two provisioning
  paths do not interact and do not share a role name by default.
- **`migration-guard`.** Unaffected. It checks that no two modules' migrations share a
  version number by scanning the same four `db/migrations` directories on disk; it
  does not know or care that a `migrate` container also runs them.
- **`boundary-guard`.** Unaffected. It scans `internal/gateway/**/*.go` for an
  `internal/telemetry` import. This change touches no gateway file and no telemetry
  file.
- **`archive-guard`.** Unaffected. This change adds no file under
  `openspec/changes/archive/` and modifies none.
- **`sqlc`.** Unaffected. No `query.sql` file and no migration in this change adds or
  changes a column, so no sqlc-generated type changes shape. `sqlc.yaml`'s four `sql:`
  entries are untouched.
- **`ui-guard` / `i18n-guard` / `money-guard` / `tz-guard` / `theme-guard`.**
  Unaffected — this change touches no `internal/gateway/templates` file and no
  `internal/gateway/handlers` file.

Finding: all of the above are unaffected, verified by reading the Makefile and the
guard implementations, not assumed unaffected because "it's just Docker."

## Docker command reference doc — `docs/1-deploy/docker.md`

A second, separate doc from the one-time runbook in `docs/0-set-up/deployment.md`.
The runbook is "how do I stand this up the first time"; this file is "I already
deployed it once — what do I run today." Both are needed: a first-time VPS user reads
the runbook once, then comes back to this reference for every routine operation
afterwards. Written for someone who has never used Docker before — no unexplained
placeholder, one command per fenced block with a one-line plain-words purpose above
it, tables for anything looked up rather than read top-to-bottom.

Sections, in order:

1. **The mental model.** A small table of the five services (`db`, `migrate`, `web`,
   `poller`, `caddy`): what each one is, what it depends on, and whether it publishes a
   port. Mirrors the dependency graph above, in plain words instead of ASCII art.
2. **Local use with Docker.** How to run the whole stack on the owner's Mac with
   `docker compose up -d --build`, and how that differs from the existing `make dev`
   / `make up` host workflow (native Go, hot-reload-friendly, no container overhead).
   States plainly: use Docker locally only to rehearse the exact production stack
   before a VPS deploy; use `make up` / `go run` for everyday local development.
3. **First deploy on the VPS.** One paragraph and one link to
   `docs/0-set-up/deployment.md`'s new §8 — never a second copy of those steps.
4. **Everyday commands.** A table: start the stack, stop it, restart one service,
   rebuild after a code change, follow one service's logs, follow all logs, list what
   is running, check a service's health status. Each row names the exact
   `docker compose ...` command.
5. **Deploying an update.** The exact sequence (`git pull`, when a `--build` is needed
   vs. when a plain `up -d` suffices, i.e., no `Dockerfile`/`go.mod`/source change since
   the last build means no rebuild is required — though `--build` is always safe and
   cheap when Docker's layer cache is warm).
6. **Migrations.** How to check `migrate-status`-equivalent output inside Docker
   (`docker compose logs migrate`), how the `migrate` service re-runs automatically on
   every `up` (and is a safe no-op when nothing is pending, since goose is idempotent),
   and how to re-run it by hand if needed.
7. **Database access.** Opening `psql` inside the `db` container
   (`docker compose exec db psql -U ... -d ...`), running one ad-hoc query, taking a
   manual `pg_dump`, and restoring one dump file.
8. **Backups.** Where `deploy/backup-db.sh`'s dated `.sql.gz` files land, how to
   confirm the cron job ran (checking the file's timestamp / a cron log line), and the
   exact restore command.
9. **Troubleshooting table.** Symptom → command to run → likely cause. Required rows,
   at minimum: `web` will not start; `migrate` exits non-zero; Caddy cannot obtain a
   certificate; `poller` is not collecting; "database connection refused."
10. **Safety.** Which commands are always safe to run (`up -d`, `logs`, `ps`, `restart
    <service>`) versus destructive (`down -v` — named loudly: `-v` deletes the
    `pgdata` volume and every row in the database; there is no undo without a backup).
11. **Switching to a managed database later.** A short restatement of D1's four steps,
    so this reference is self-contained for that operation too, without sending the
    reader back to design.md.

Cross-links (each is a task in tasks.md, not left implicit):
- `docs/1-deploy/docker.md` → `docs/0-set-up/deployment.md` (back, for first-time
  setup).
- `docs/0-set-up/deployment.md` → `docs/1-deploy/docker.md` (forward, for day-to-day
  commands) — added at the end of the new §8.
- Root `README.md`'s "Deployment" section → `docs/1-deploy/docker.md` (added alongside
  the existing link to `docs/0-set-up/deployment.md`).

## Amendment — D2's goose-CLI plan was impossible (T2b)

**D2 is kept above unchanged, as the record of what was originally decided.** This
section records what changed and why, per the "append, never edit" rule for an
already-decided design.

### The problem

D2 said the Docker builder stage would build the goose CLI from the project's own
pinned `go.mod` version:

```
go build -o /out/goose github.com/pressly/goose/v3/cmd/goose
```

This command fails. The leader ran it for real and got:

```
go: github.com/pressly/goose/v3/cmd/goose: missing go.sum entry for module providing
package github.com/ClickHouse/clickhouse-go/v2 (imported by github.com/pressly/goose/v3/cmd/goose)
```

and the same "missing go.sum entry" error for seven more modules: `go-sql-driver/mysql`,
`mfridman/xflag`, `microsoft/go-mssqldb`, `tursodatabase/libsql-client-go`,
`vertica-sql-go`, `ydb-go-sdk`, `ziutek/mymysql`.

**Why:** this project imports `github.com/pressly/goose/v3` as a **library** only
(`internal/testdb` calls `goose.NewProvider`). The goose CLI's `main` package additionally
imports every optional database driver it supports, so building the CLI needs `go.sum`
entries for all of them. Since nothing in this repo ever imports those drivers, `go mod
tidy` never added them, and `go.sum` has no entries for them. `docker build --target
migrate` would fail on the VPS with this same error — D2's plan was never buildable in
this repo, not a transient issue.

### D10 — Replace the goose CLI with a `cmd/migrate` Go program using the goose library

A new runnable, `cmd/migrate/main.go`, calls `goose.NewProvider` directly — the same
library API `internal/testdb.applyMigrations` already uses, at the same pinned
`v3.27.3` version. It needs only the `postgres` driver, already an existing dependency
with a valid `go.sum` entry. No CLI binary is built or shipped.

The Dockerfile's builder stage now runs `go build -o /out/migrate ./cmd/migrate`
instead of building the goose CLI. The `migrate` final stage ships this one binary as
its `ENTRYPOINT`, instead of a `goose` binary plus a shell script.

**Rejected:** vendoring or patching `go.sum` just enough to build the CLI. Rejected
because it fights the toolchain instead of using it: `go mod tidy` would keep undoing a
hand-edited `go.sum`, and the fix does not remove the CLI's need for eight driver
packages this project will never use. Using the library is both simpler and the
already-proven-working path (`internal/testdb` already does exactly this).

### D11 — `cmd/migrate` reads migrations from the image filesystem, not `go:embed`

`cmd/migrate` does not use `//go:embed` for the SQL files. The `//go:embed` directive
cannot contain `..` path elements, so a package can only ever embed its own directory
tree — it cannot reach `internal/account/db/migrations` from `cmd/migrate/`.
`internal/testdb.ProvisionDirs`'s own doc comment documents this exact restriction, and
it is the direct precedent this decision follows.

Instead, `cmd/migrate` reads four directories at runtime with `os.DirFS`, rooted at
`/migrations` by default (overridable with `MIGRATIONS_ROOT`). The Dockerfile already
`COPY`s each module's migrations to `/migrations/account`, `/migrations/telemetry`,
`/migrations/charging`, `/migrations/analytics` — this part of D2's original plan was
correct and is unchanged.

`cmd/migrate` applies the four directories in this exact order — account, telemetry,
charging, analytics — matching the Makefile's `MIGRATIONS_DIRS` and
`internal/testdb.ProvisionDirs`'s reasoning: charging's backfill migration reads
telemetry's table, so telemetry must precede charging. Each directory gets its own
`goose.NewProvider` call with `goose.WithAllowOutofOrder(true)` — the Provider API's
equivalent of the CLI's `-allow-missing` flag, required because all four directories
share one `goose_db_version` table (`internal/testdb/testdb.go`'s `applyMigrations`
comment explains this in full).

`cmd/migrate` does **not** import `internal/testdb` — that package pulls in
`testcontainers-go` and its own doc comment says never ship it in a real binary.
`cmd/migrate` calls the goose library directly instead, mirroring `testdb`'s pattern
without depending on the test-only package.

**`DATABASE_URL` is read directly with `os.Getenv`, not through `internal/config`.**
`config.Load()` also validates `TESLA_CLIENT_ID`/`TESLA_CLIENT_SECRET`, which a
migration-only tool has no reason to require — a deploy would fail to migrate over an
unrelated Tesla credential check. This is a deliberate, documented deviation from
`ai/go-conventions.md`'s "no `os.Getenv` outside `internal/config`" rule, recorded here
and in a code comment in `cmd/migrate/main.go`.

**`deploy/migrate-entrypoint.sh` is deleted.** The Go program now owns the loop over
directories; keeping a shell script that only re-implements what the binary already
does would be a second, driftable copy of the same logic. `compose.yaml`'s `migrate`
service is unchanged apart from `target: migrate` still pointing at the same Dockerfile
stage — nothing outside the Dockerfile and the deleted script changes.

**Compliance with D4:** the image is still built on the VPS, with `docker compose up -d
--build`. This amendment changes what the builder stage compiles and what the `migrate`
stage ships — it does not introduce a registry, CI, or any pre-built image. D4 stands
unchanged.
