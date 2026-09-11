# magus-tesla-api

Personal Go project to monitor **Tesla Vehicles**  via the [Tesla Fleet API](https://developer.tesla.com/docs/fleet-api).

Fetches live vehicle data (battery, range, climate, location, state) and serves as the foundation for a personal dashboard and future home automation. Built as a **modular monolith** in Go with strict package separation so new capabilities can be added cleanly over time.

---

## Project Goal

Build a personal, multitenant, self-hosted tool that:
- Authenticates with the Tesla Fleet API using OAuth 2.0
- Fetches real-time data from Tesla vehicles (charge level, range, climate, location, vehicle state)
- Stores and exposes that data for dashboarding and personal automation
- Grows modularly — new Tesla API capabilities slot into their own package without touching existing code

---

## Quick Start

> **Prerequisites:** Go 1.25+ (see `go.mod`), a registered Tesla Fleet API app, and a completed setup (see [docs/post-registration-setup.md](docs/post-registration-setup.md)).

```bash
# Install dependencies
go mod tidy

# One-time OAuth setup — saves access + refresh tokens to .env
# (single-user smoke path; the web gateway handles its own per-user OAuth)
go run ./cmd/setup

# Run the multi-tenant web gateway (needs DATABASE_URL + SESSION_SECRET in .env)
go run ./cmd/web

# Run the nightly telemetry poller (needs DATABASE_URL in .env)
go run ./cmd/poller          # nightly scheduled collection (blocks)
go run ./cmd/poller --once   # one immediate collection cycle, then exit
```

---

## Building the modular monolith

> **Prerequisites:** Go 1.25+ (see `go.mod`). The DB-backed modules use **generated** code, but
> that output is **committed**, so a fresh checkout compiles as-is. Install `sqlc`
> (`brew install sqlc`) and, for migrations, `goose` before you *change* schema or queries —
> full tooling list in [docs/0-set-up/deployment.md](docs/0-set-up/deployment.md).

The monolith is a single Go module: `go build ./...` compiles **every** package and command at
once. Each DB-backed module has its own sqlc package — `accountdb`, `telemetrydb`,
`chargingdb`, `analyticsdb` (one `sql:` entry per module in `sqlc.yaml`) — and all four are checked in, so
regeneration is only needed after you edit a `query.sql` or a migration.

```bash
# 1. Dependencies + regenerated DB code
#    (after changing go.mod, any query.sql, or a migration)
make tidy      # go mod tidy
make sqlc      # sqlc generate → internal/{account,telemetry,charging,analytics}/db/{db,models,query.sql}.go

# 2. Compile the whole monolith
make build     # go build ./...   — all internal/ packages + every cmd/

# 3. Full local gate
make check     # build + vet + ui-guard + i18n-guard + money-guard + tz-guard + migration-guard
#              # + boundary-guard + theme-guard + archive-guard + test
```

Raw Go equivalents (no Make):

```bash
go mod tidy
sqlc generate
go build ./...   # compile everything
go vet ./...     # static analysis
go test ./...    # tests — internal/testdb uses TEST_DATABASE_URL when set and reachable,
                 # otherwise a disposable postgres:16-alpine testcontainer
```

> `make test` never reads `DATABASE_URL` at all and always runs against a disposable
> testcontainer, so the suite can never touch the real magus database. Use
> `make test-with-db` to opt in — it forwards your `DATABASE_URL` as `TEST_DATABASE_URL`
> (CI with a managed Postgres).

To produce runnable **binaries** (not just compile), build the entrypoints into `./bin`:

```bash
make bins        # → bin/setup, bin/web, …  (go build -o bin/ ./cmd/...)
```

> `make sqlc` is an explicit step, never part of `make build`. Re-run it whenever you edit a
> `query.sql` or a migration, then rebuild. First-time database setup is separate — see
> **[docs/0-set-up/deployment.md](docs/0-set-up/deployment.md)** (`make db-setup`).

---

## Project Structure

```
magus-tesla-api/
│
├── deploy/
│   └── docker/          # Dockerfile, Dockerfile.dockerignore, compose.yaml, Caddyfile,
│                        # backup-db.sh — all Docker deploy files in one folder
│                        # (see docs/1-deploy/docker.md)
│
├── cmd/               # Executable entry points — see cmd/README.md
│   ├── setup/          # One-time Tesla OAuth flow (saves tokens to .env)
│   ├── web/            # Multi-tenant HTTP gateway (vehicle dashboard)
│   ├── poller/         # Nightly telemetry collection (run once or scheduled)
│   ├── migrate/        # One-shot goose migration runner — the "migrate" service's ENTRYPOINT
│   ├── explore-tesla-api/  # On-demand raw Tesla Fleet API JSON inspector
│   └── monthly-capacity/  # On-demand monthly pack-capacity measurement, run by hand (not in the deploy image)
│
├── internal/
│   ├── account/        # Per-user Tesla tokens (persisted, refreshed) in Postgres
│   ├── tesla/          # State-less Fleet API adapter (handed creds per call)
│   ├── telemetry/      # Nightly vehicle snapshot collection + storage (poller)
│   ├── charging/       # User-asserted charge entries (home/work/3rd-party sessions)
│   ├── analytics/      # Derived vehicle metrics (Wh/km, consumed %/day, distance/day)
│   │   └── db/             #   analyticsdb: vehicle_metrics read model + recompute watermarks
│   ├── app/            # Application layer: ProcessVehicleData (one cycle = sync → charging → analytics) + the daily scheduler. Owns no data.
│   ├── gateway/        # Gin + Templ + htmx + DaisyUI web layer (the ONLY place HTML lives)
│   │   ├── handlers/       #   thin handlers: session/auth → module interface → render
│   │   ├── i18n/           #   translation catalogue + per-request language resolution (es default, en)
│   │   ├── templates/      #   Templ: layouts/ (drawer shell) · pages/ · fragments/ · ui/ (typed DaisyUI kit)
│   │   ├── static/         #   embedded: htmx.min.js · app.js · DaisyUI .mjs bundles · input.css · generated app.css
│   │   └── tools/          #   git-ignored Node-less Tailwind CLI binary (make ui-toolchain)
│   ├── googleauth/     # Google OAuth for user login
│   ├── config/         # .env loading and token persistence
│   ├── auth/           # Tesla OAuth URL, code exchange, token refresh
│   ├── clock/          # Platform default time zone (America/Bogota) + calendar-day normalization. Stdlib time only.
│   └── testdb/         # Test-only Postgres provisioning (TEST_DATABASE_URL → testcontainer fallback)
│
├── magus-public-key-netlify/   # EC public key hosted on Netlify for Tesla verification
│   └── well-known/appspecific/
│       └── com.tesla.3p.public-key.pem
│
└── docs/
    ├── post-registration-setup.md   # Full setup guide — start here
    ├── battery-consumed-graph.md    # How the Battery Consumed pipeline works end to end
    ├── 0-set-up/deployment.md       # New-machine + VPS runbook (§8: Docker Compose deploy)
    └── 1-deploy/docker.md           # Day-to-day Docker command reference (start here after the first deploy)
```

For details on the `cmd/` convention and each binary, see [cmd/README.md](cmd/README.md).

---

## Setup Guide

All Tesla API registration steps — from getting credentials to the first API call — are documented in:

**[docs/post-registration-setup.md](docs/post-registration-setup.md)**

It covers:
1. Protecting credentials in `.env`
2. Generating the EC key pair
3. Hosting the public key on Netlify
4. Registering with the Tesla Developer Portal
5. Registering the public key via the Fleet API
6. Running the OAuth flow with `go run ./cmd/setup`
7. Virtual key pairing (skipped — only needed for commands)
8. Fetching live data — now served by the multi-tenant web gateway (`go run ./cmd/web`)

---

## Deployment (new machine / production)

Standing the project up on a fresh machine — including PostgreSQL, `sqlc`/`goose` tooling,
and the one-command database setup (`make db-setup`) — is documented in:

**[docs/0-set-up/deployment.md](docs/0-set-up/deployment.md)**

The database is configured entirely through `DATABASE_URL` in `.env`; `make db-setup` creates
the database (if needed) and applies migrations idempotently. Persistence coding conventions
live in [ai/go-conventions.md](ai/go-conventions.md) → *Persistence (Postgres + sqlc + goose)*.

**Deploying to a VPS with Docker** — a first-time runbook lives in
[docs/0-set-up/deployment.md](docs/0-set-up/deployment.md) §8; the day-to-day command
reference (logs, migrations, backups, troubleshooting) lives in
[docs/1-deploy/docker.md](docs/1-deploy/docker.md). Use the first once, to stand up the
VPS; come back to the second every time after.

---

## Sessions & staying logged in

The web gateway (introduced in **Tier 2**) uses **cookie-based sessions**
(`gin-contrib/sessions` cookie store): session data lives in a **signed + AES-encrypted cookie
in the browser**, not in server memory, keyed by a `SESSION_SECRET` from `.env`.

**Do you stay logged in after the server restarts? — Yes.** The server holds *no* session
state; on each request it simply verifies/decrypts the cookie the browser presents. Restarting
the server (or running multiple instances) does **not** log anyone out.

**The one rule: keep `SESSION_SECRET` stable.** If you rotate/change it, existing cookies can no
longer be verified and every user is logged out on their next request. Store it in `.env`
(gitignored) and inject it as a secret in production.

> Trade-offs of the alternatives: an **in-memory** store would log everyone out on restart and
> can't scale past one instance; a **Postgres-backed** store also survives restarts and adds
> server-side revocation, at the cost of a DB read per request. We can switch to Postgres-backed
> sessions later if revocation becomes a requirement.

---

## Architecture

This is a **modular monolith** — one Go module, multiple internal packages, each owning a single concern. New Tesla API capabilities (charging, telemetry, commands) slot into new packages under `internal/` without touching existing code.

| Package | Responsibility |
|---|---|
| `internal/account` | Per-user Tesla tokens (persisted + refreshed) in Postgres |
| `internal/tesla` | Stateless Fleet API adapter (handed credentials per call) |
| `internal/telemetry` | Nightly per-vehicle snapshot collection + storage |
| `internal/charging` | User-asserted charge entries (home/work/3rd-party), plus **`charging.supercharger_sessions`** — a mirror of each Supercharger session's window, site, energy and cost, and the home of the human-verified battery percentages. Owns all charge data the app treats as a charge, whoever reported it. Since RM52 tier 1 (MAG-32) it also **measures** each vehicle's effective pack capacity every month into **`charging.monthly_effective_capacity`**, which `packCapacityKWh` reads instead of a hardcoded constant. |
| `internal/analytics` | Derived vehicle metrics computed over stored telemetry: rolling Wh/km, the per-day **battery consumed %** (raw SoC delta corrected by both charge sources) and per-day **distance**, plus the gap detection it stores through its own `GapWriter`. Owns **`vehicle_metrics`**, a precomputed read model written by `Recalculator` and read by `Reader` — the derivation is no longer recomputed per request. Since RM38 that table also mirrors the eight vehicle-status fields, which `Reader.LatestMetricsByAccount` serves as the latest-row-per-vehicle status port. See [docs/battery-consumed-graph.md](docs/battery-consumed-graph.md). |
| `internal/app` | **Application layer.** Exposes one port, `Processor.ProcessVehicleData`, running one full cycle as four named steps — sync fleet data (`telemetry`) → process charging data (the Supercharger mirror into `charging`) → recalculate analytics → measure monthly vehicle capacity — and hosts the daily `Scheduler` that drives it. Step 4 (RM52 tier 2, MAG-32) runs only on the first day of the month, in the platform zone, and measures the previous month through `charging.MonthlyCapacityCalculator`. Owns **no data**: no table, no migration, no pool. Called by `cmd/poller`'s scheduler and its manual-rerun HTTP listener. |
| `internal/gateway` | Gin + Templ + htmx web layer, styled with Node-less Tailwind + DaisyUI (drawer nav, typed `ui/` component kit). The only package allowed to produce HTML. |
| `internal/googleauth` | Google OAuth for user login |
| `internal/config` | Load `.env`, typed config, token persistence |
| `internal/auth` | Tesla OAuth URL, code exchange, token refresh |
| `internal/clock` | Platform default time zone (`America/Bogota`) and calendar-day normalization — `Zone()`, `Now()`, `LoadOrDefault()`, `CalendarDay()`. Stdlib `time` only, so nothing can cycle through it. Adopted by `config`, `telemetry`, `analytics`, `app` and `gateway` (`RM35` tiers 2–6). |
| `internal/testdb` | Test-only Postgres provisioning helper (uses `TEST_DATABASE_URL` when set and reachable, else a disposable `postgres:16-alpine` testcontainer). Import from `_test.go` files **only**. |

### Dependency graph

Internal imports only (stdlib and third-party omitted). Dependencies flow **downward** — the
gateway calls domain modules, domain modules call adapters, and nothing calls back up:

```text
┌─ COMPOSITION ROOT ── cmd/ wires concrete types together at startup ──────┐
│  cmd/web ────────────► gateway, account, telemetry, charging,            │
│                        analytics, tesla, googleauth, config              │
│  cmd/poller ─────────► app, telemetry, account, analytics, charging,     │
│                        tesla, config      (wiring only — no logic)       │
│  cmd/setup ──────────► auth, config                                      │
│  cmd/explore-tesla-api ► tesla, auth, config                             │
│  cmd/monthly-capacity ─► charging, config                                │
│  cmd/migrate ────────► config          (the "migrate" Docker service)    │
├─ LAYER 3 ── presentation ────────────────────────────────────────────────┤
│  gateway ────────────► account, charging, analytics,                     │
│    │                   tesla, googleauth, clock                          │
│    ├─ handlers ──────► account, auth, charging,                          │
│    │                   analytics, tesla, googleauth, i18n, clock,        │
│    │                   templates/*                                       │
│    ├─ templates/* ───► i18n, templates/ui                                │
│    └─ i18n ──────────► account            (the Language type only)       │
├─ LAYER 2.5 ── application layer ─────────────────────────────────────────┤
│  app ────────────────► telemetry, charging, analytics, account, clock    │
├─ LAYER 2 ── derived read-side ───────────────────────────────────────────┤
│  analytics ──────────► account, charging, telemetry, clock               │
├─ LAYER 1 ── domain modules & config ─────────────────────────────────────┤
│  telemetry ──────────► account, tesla, clock, telemetry/db               │
│  charging ───────────► charging/db                                       │
│  account ────────────► auth, account/db                                  │
│  config ─────────────► clock                                             │
├─ LAYER 0 ── adapters & leaves (no internal dependencies) ────────────────┤
│  tesla    googleauth    auth    clock    testdb    <module>/db           │
└──────────────────────────────────────────────────────────────────────────┘
```

`cmd/` importing many modules at once is not a boundary violation — it is the **composition
root**, the one place allowed to know every concrete type so it can inject them into each other.
That is what keeps the layers below it depending on interfaces rather than on one another.

This graph is acyclic and the Go compiler keeps it that way — an import cycle between packages
is a **compile error** (`import cycle not allowed`), not a lint warning. Note the consequence
for `internal/tesla`: it stays stateless about identity (credentials are passed *in*) partly so
it never needs to import `account`, which would close the loop `account → tesla → account`.

When two modules genuinely need each other, do **not** create a `shared` package and do **not**
merge them — declare a small **consumer-side interface** in the package that needs the data and
wire the concrete type in at `cmd/` startup. Full rule and example:
[`ai/architecture.md`](ai/architecture.md) §2 "Dependency direction & import cycles".

For a worked example of these boundaries in one feature — four modules plus the poller, with the
composition root joining a derivation in one module to a writer in another — see
**[docs/battery-consumed-graph.md](docs/battery-consumed-graph.md)**, which traces the Battery
Consumed chart end to end: where each value is calculated, when `charge_gaps` rows are written and
deleted, and what happens when a charge record is edited.

To regenerate this graph:

```bash
go list -f '{{.ImportPath}}|{{join .Imports ","}}' ./cmd/... ./internal/... \
  | sed 's|github.com/cristianpena/magus-tesla-api/||g'
```

### Database tables by module

Every table is owned by **exactly one** module: only that module's sqlc package queries it, and
another module reads it **only** through the owner's public Go interface — never a cross-module
join. Migrations live with the owner (`internal/<module>/db/migrations/*.sql`, goose) and the dir
must be listed in `MIGRATIONS_DIRS` in the `Makefile`.

| Module | sqlc package | Schema | Table | What it stores |
|---|---|---|---|---|
| `internal/account` | `accountdb` | `account` | `accounts` | One row per logged-in user — provider identity (`google` + subject id), email, display name, UI language. |
| | | `account` | `tesla_tokens` | The Tesla OAuth pair (access + refresh) and access-token expiry, **one row per account** (unique on `account_id`). |
| | | `account` | `vehicles` | Tesla vehicles registered to an account — `tesla_id`, VIN, display name, access type, captured vehicle config. |
| `internal/telemetry` | `telemetrydb` | `telemetry` | `vehicle_snapshots` | Nightly per-vehicle snapshot: battery/charge, range, odometer, temps, TPMS pressures, lock/sentry — **one row per vehicle per calendar day**, plus the lossless `raw_data` JSONB. Carries **observations only**: the five derived consumption columns moved to `internal/analytics` (RM29 tier 4), which computes them from the exact predecessor rather than reading them back, and latitude/longitude live only in `raw_data`. |
| | | `telemetry` | `poll_attempts` | Audit row for **every** collection attempt (outcome + reason), successful or not. Since RM29 tier 7 it also carries `run_id` — every row one `ProcessVehicleData` invocation writes shares one, so per-run facts come from `GROUP BY run_id` — and `triggered_by` (`scheduler` or `api` for a manual rerun via `cmd/poller`'s HTTP listener, `platform-add-manual-rerun-api`). The table stayed in `internal/telemetry` rather than moving to `internal/app` as the roadmap first planned: it always held our own facts (our clock, our failure classification), never anything Tesla reported. |
| | | `telemetry` | `supercharger_history` | Tesla Supercharger sessions — site, start/stop, `energy_kwh`, cost + currency, paid flag — upserted on Tesla's `session_id`. Supercharger-only: home / 3rd-party charging never appears in this feed. |
| | | `telemetry` | `poll_runs` | One row per `run_id` (PRIMARY KEY), written **once** per `app.ProcessVehicleData` invocation — success or a whole-cycle failure alike — reproducing the poller's per-cycle log line (account/vehicle attempt-outcome counts, Tesla API call count, duration) as a queryable row. **No reader yet** (backlog); the only way to see a row today is direct SQL. |
| `internal/charging` | `chargingdb` | `charging` | `manual_charge_entries` | User-asserted charge sessions — the home / work / 3rd-party gap the Tesla feed can't fill. Carries a lifecycle `status`, the `energy_source` / `price_source` provenance columns the module always computes itself, and the generated `inferred_capacity_kwh_calc`. |
| | | `charging` | `supercharger_sessions` | Mirror of each Supercharger session, one row per (account, Tesla session id), refreshed nightly as Tesla's fees settle — plus the three **human-verified** battery-percentage columns the sync structurally cannot touch, and the `status` computed from them. The separate `telemetry.supercharger_history` keeps the raw vendor payload. |
| | | `charging` | `monthly_effective_capacity` | The measured pack capacity per vehicle per month — the median of `inferred_capacity_kwh_calc` over that vehicle's valid records. Read by `packCapacityKWh`, which falls back to 62.0 kWh only while no month is measured. **No `account_id`**: it describes a battery pack, not user data. |
| | | `charging` | `mirror_watermarks` | One Supercharger-mirror cursor per account. Never advances to `now()` — only to the highest `updated_at` a non-empty read actually observed. |
| `internal/analytics` | `analyticsdb` | `analytics` | `vehicle_metrics` | The precomputed per-day read model — one row per (account, vehicle, day) the vehicle reported, holding the raw observations plus five derived `_calc` columns. Since RM38 the raw observations also **mirror eight vehicle-status fields** from `vehicle_snapshots` (`locked`, `sentry_mode`, `car_version`, `inside_temp_c`, `outside_temp_c`, `charging_state`, `charge_limit_soc_pct`, `captured_at`) — copied verbatim, never re-derived, and populated even on a day with no predecessor; all eight are nullable with no backfill, so rows written before that migration keep them NULL. **Dense**: a day with no computable predecessor still gets a row, with its `_calc` columns and `consumed_pct` NULL and `flagged` an explicit `false`, which is why both reader queries filter `IS NOT NULL` rather than trusting a zero. Written by `Recalculator`, read by `Reader`. |
| | | `analytics` | `vehicle_metric_watermarks` | One recompute cursor per (account, vehicle, source), three sources. Drives `Reconcile`'s incremental pass; **no row means epoch** — backfill the vehicle's full history. |
| | | `analytics` | `charge_gaps` | Vehicle-days whose battery math doesn't add up because a charge record is missing or incomplete — **one row per (account, vehicle, day)**, with the suspected missing source (`MANUAL` / `SUPERCHARGER`). A live worklist, not an audit trail: no `resolved_at`, a day that stops flagging is deleted by the next nightly reconciliation. Written by `internal/analytics` through its own `GapWriter` port. |
| `internal/gateway` | — | — | *(none)* | Renders HTML; calls module interfaces, never a database. |
| *(tooling)* | — | `public` | `goose_db_version` | Not owned by any module — goose's own ledger, a **single shared table** across all migration dirs. That is why `make migrate-up` runs each dir with `-allow-missing`. |

> Every column, CHECK, generated column and index decision for the four `charging` tables —
> including the reason each un-indexed column was left un-indexed, and its revisit trigger —
> lives in [`kkpa/context/architecture/charging-tables.md`](kkpa/context/architecture/charging-tables.md).

---

## Making a change — what to touch, what to run

The modular-monolith boundaries decide *where* code goes; this table is the quick map. Golden
rules: **HTML lives only in `internal/gateway/`**, **no module reads another module's DB**, and
**cross-module data flows only through public Go interfaces**. Full recipes live in
[`internal/gateway/AGENTS.md`](internal/gateway/AGENTS.md), [`ai/architecture.md`](ai/architecture.md),
and [`ai/go-conventions.md`](ai/go-conventions.md).

| You're adding… | Modules / files to touch | Regenerate / verify |
|---|---|---|
| **A new page** (HTML using data a module already exposes) | `internal/gateway/` only — `templates/pages/*.templ` + `fragments/*.templ` composing the `templates/ui/` kit, a thin `handlers/*.go`, and a route in `gateway.go`. Prefer `kkpa-goth-scaffold-ui scaffold`. | `make templ` (+ `make css` if you used a new class) → `make check` |
| **A new UI endpoint** (an htmx `/ui/...` fragment or a write action) | `internal/gateway/` — handler + fragment + `/ui/...` route; **CSRF + tenant check on writes** (see AGENTS.md). If it needs data no module exposes yet, also add a method to the **owning** module's `Service`/`Reader`/`Writer`. | `make sqlc` (if new query) → `make templ` (+ `make css`) → `make check` |
| **A new upstream (Tesla Fleet) API call** | `internal/tesla/vehicles.go` (typed method) **and** `raw.go` (the `Raw*` sibling) **and** `cmd/explore-tesla-api/main.go` + its README — required by CLAUDE.md. Miles→km companions mandatory; **never** add tests that hit the live paid API. | `make check` |
| **A new database table / column** | The **owning** `internal/<module>/` only — `db/migrations/*.sql` (goose) + `db/queries.sql`, exposed through the module's `Service`. Add the module's dir to `MIGRATIONS_DIRS` in the Makefile if it's the module's first table, and add the table to the README **Database tables by module** list in the same change. **`database` is a design-gate — confirm the design first.** | `make sqlc` → `make migrate-up` → `make check` |
| **A new module** (a new subsystem/concern) | New `internal/<module>/` with a `Service` interface + DTOs; wire into the gateway **only** via `Deps` + its interface. Update the README **Project Structure** tree, the **Architecture** table, and (if it owns tables) **Database tables by module** in the same change. | `make sqlc` / `make templ` as needed → `make check` |

**`openspec/changes/archive/` is immutable.** An archived change records what was proposed and
decided at that time, so it is never edited or deleted — not even to correct it. Archiving a new
change (adding its folder) and regrouping one unchanged under `archive/<module>/` are the only
writes that folder ever takes. When an archived doc is stale, fix the live spec under
`openspec/specs/` instead. **`make archive-guard`** (wired into `make check`) fails if any file
already in the archive was modified or deleted, measured against the merge-base with `main` so it
covers the whole branch plus uncommitted work. It exists because the docs-sweep rule above points
you at every doc a change invalidated, and a grep for a module name will hit the archive. Escape
hatch — only for something that is *not* a rewrite of the record, e.g. purging a leaked secret:
`ARCHIVE_GUARD_ALLOW=1 make archive-guard`, with the reason in the commit message.

Before committing any change, run **`make generate`** (sqlc + templ + css) then **`make check`**
(build + vet + guards + test). `make up` does generate + migrate + run.

---

## Web UI (gateway)

The web UI lives **only** in `internal/gateway/` — the single module allowed to produce HTML.
It's built on the **GOTH stack**: Go + [Templ](https://templ.guide) + htmx, styled with a
**Node-less** standalone Tailwind CLI + **DaisyUI** (responsive drawer nav, a typed
`templates/ui/` component kit, semantic theme tokens — never hex). Two dark themes ship,
both compiled into `app.css`: **`apex`** (the default — Tesla-red primary, charcoal
surfaces) and **`graphite`** (blue primary, every colour pair WCAG-AA). The daisyUI
builtin `halloween` stays registered as a fallback. See *Switching the theme* below.

**Only the gateway uses the UI stack.** Every domain module (`account`, `tesla`, `telemetry`,
`charging`, `googleauth`) is UI-agnostic: it owns data and exposes Go interfaces, and the
gateway renders them. No domain module imports Templ, references a DaisyUI class, or knows a
theme exists — so restyling or re-theming never ripples past the gateway boundary.

> **Foundation already scaffolded here (2026-07-24).** The one-time UI foundation was laid
> down by the local `kkpa-goth-scaffold-ui` skill's `init` mode and is committed (drawer
> `base.templ`, the `ui/` kit, `static/input.css` + `app.css` + DaisyUI bundles, the `make css`
> target). **Do not run `init` again** — it overwrites `base.templ` / `input.css` / the `ui/`
> kit. Add new pages with `kkpa-goth-scaffold-ui scaffold <concept> [module]`; edit styling in
> the committed files. Full rules: [`ai/htmx-conventions.md`](ai/htmx-conventions.md) and
> [`internal/gateway/AGENTS.md`](internal/gateway/AGENTS.md).

### Switching the theme

**As a user:** sign in and open [`/settings`](internal/gateway/templates/pages/settings.templ) —
the theme dropdown there applies your choice instantly (no page reload) and persists it to
your account, so it follows you across devices and sessions. There is no longer a
source-edit step for switching between themes that already exist: `data-theme` on
[`base.templ`](internal/gateway/templates/layouts/base.templ) is resolved **per request**
from `ui.ThemeFromContext(ctx)` (`RM42-gateway-add-theme-selector`), never a hardcoded
literal. A signed-in request resolves it from the `account.settings` row (one query, shared
with the language preference); an anonymous request, or any page loaded right after logout,
falls back to the `theme` cookie the last signed-in switch wrote.

**Adding a new palette** is four steps plus a restart (`ui.Themes` is the single closed vocabulary — see
`kkpa/context/architecture/gateway-theming.md`):

1. New `internal/gateway/static/themes/<name>.css` — one `@plugin` block, mirror `graphite.css`.
2. One `@import "./themes/<name>.css";` line in `internal/gateway/static/input.css`.
3. Add `"<name>"` to `ui.Themes` in
   [`internal/gateway/templates/ui/theme.go`](internal/gateway/templates/ui/theme.go).
4. Regenerate and verify:

   ```sh
   make css
   make theme-guard
   ```

   `make theme-guard` (wired into `make check`) fails the build if `ui.Themes`,
   `internal/account`'s own `Theme*` constants, and `input.css`'s registered themes ever
   disagree — so a palette added to only one of the three is caught immediately rather than
   shipping a silently wrong dropdown.

5. Restart with `make up`. If `make dev` is already running, just refresh the browser — the
   tailwind watcher picks the new CSS up.

**Where the palettes live:** `internal/gateway/static/themes/`. Each theme file
(`apex.css`, `graphite.css`) holds **only** its palette — one `@plugin` block of
`--color-*` / radius / size tokens. Everything shared sits in `_shared.css`: the
self-hosted Inter + JetBrains Mono `@font-face` blocks, the font tokens, the
battery-level colour scale (`--color-battery-*` + the `.text-battery-*` utilities), and
the `.divider` reset. A theme must never redefine those — the battery scale is a *state*
vocabulary, so "critically low" looks the same whichever palette is active. That is also
why both palettes keep their primary out of the red/orange/yellow/green band.

`halloween` is a daisyUI builtin, registered via the `@plugin "./daisyui.mjs" { themes:
halloween; }` block rather than being a palette of its own. It is **not** unstyled: since
MAG-49 a third file, `themes/halloween.css`, overrides its four status colours as a plain
unlayered `[data-theme="halloween"]` rule (a `@plugin` block would fight the builtin's own
registration). Everything else about it stays the builtin's.

**Status colours follow the battery scale's rule (MAG-49).** `error`/`warning`/`success`
are a state vocabulary — red, amber, green, in every theme; re-hueing them per palette
would make a failure read as decoration. **`info` is the one exception** and carries each
theme's identity: apex `#7c8cff`, graphite `#22d3ee`, halloween `#c084fc`. Alerts render
via `alert-soft`, so the status colour is the *text*; every value is measured against its
own `base-100` and clears WCAG AA.

**CSS toolchain & deploy:** the generated `internal/gateway/static/app.css` is **committed**
and embedded via `//go:embed static`, so a production build (`go build ./cmd/web`) is
self-contained — **no Node, npm, or Tailwind binary needed at build or run time**. The
git-ignored Tailwind binary (`make ui-toolchain`) is only needed on a **dev machine that
regenerates CSS** after editing templates or adding classes. See
[`docs/0-set-up/deployment.md`](docs/0-set-up/deployment.md) → *Web UI CSS*.

**Date-filtered reads: `?start=&end=`, never `?days=N`.** Every gateway endpoint that filters
by a date range takes absolute `?start=YYYY-MM-DD&end=YYYY-MM-DD` query params (both whole
calendar days, `end` inclusive), rejecting a malformed or over-wide window with HTTP 400. The
window cap is set **per endpoint** by its source table's row density — `GET /ui/dashboard/history`
caps at 90 days (`vehicle_snapshots` is dense), `GET /ui/supercharger-stats` caps at 400 days
(`charging.supercharger_sessions` is sparse). Full contract:
[`internal/gateway/AGENTS.md`](internal/gateway/AGENTS.md) §"HTTP date-filter convention".

---

## External Services

| Service | Purpose |
|---|---|
| [Tesla Fleet API](https://fleet-api.prd.na.vn.cloud.tesla.com) | Vehicle data and commands |
| [Tesla Auth](https://auth.tesla.com/oauth2/v3) | OAuth 2.0 tokens |
| [magus-monitor.netlify.app](https://magus-monitor.netlify.app) | Hosts the EC public key for Tesla domain verification |

---


---

## Q&A

### Does the Netlify deployment need to be running always?

There's no server to "keep running" — the Netlify deploy is **static hosting** (a single PEM
file on Netlify's CDN), not a running process. Once deployed it stays live permanently at no cost
and with no compute; there is nothing to start, restart, or keep awake, and it is unaffected by
whether your local machine is on.

That said, the file must **stay published**. Tesla requires the public key to remain permanently
accessible at `https://magus-monitor.netlify.app/.well-known/appspecific/com.tesla.3p.public-key.pem`
so it can re-verify the app's domain and key over time. So:

- **Keep the `magus-monitor` Netlify site deployed** — don't delete it or unpublish the deploy.
- You only need to **redeploy** if the key changes or you move domains:
  `netlify deploy --dir=magus-public-key-netlify --prod`.
- This is Layer 1 (app-level) infrastructure — it's shared by the whole app and is independent of
  the OAuth tokens and data fetching in Layer 2. See
  [docs/layer1-app-registration.md](docs/layer1-app-registration.md) for context.


### How artifacts work?

design.md — in the OpenSpec flow, proposal.md carries the what/why, specs/ carry behavioral requirements, tasks.md the implementation steps, and design.md is where technical decisions like table design, indexes, and migrations belong

## Update GO

`mise use go@latest` to update to the latest Go version. If you don't have `mise` installed