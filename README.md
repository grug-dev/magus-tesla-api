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
make check     # build + vet + ui-guard + i18n-guard + money-guard + test
```

Raw Go equivalents (no Make):

```bash
go mod tidy
sqlc generate
go build ./...   # compile everything
go vet ./...     # static analysis
go test ./...    # tests — internal/testdb uses DATABASE_URL when reachable,
                 # otherwise a disposable postgres:16-alpine testcontainer
```

> `make test` deliberately **ignores** `.env`'s `DATABASE_URL` and always runs against a
> disposable testcontainer, so the suite can never touch the real magus database. Use
> `make test-with-db` to opt in to the configured `DATABASE_URL` (CI with a managed Postgres).

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
├── cmd/               # Executable entry points — see cmd/README.md
│   ├── setup/          # One-time Tesla OAuth flow (saves tokens to .env)
│   ├── web/            # Multi-tenant HTTP gateway (vehicle dashboard)
│   ├── poller/         # Nightly telemetry collection (run once or scheduled)
│   └── explore-tesla-api/  # On-demand raw Tesla Fleet API JSON inspector
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
│   └── testdb/         # Test-only Postgres provisioning (DATABASE_URL → testcontainer fallback)
│
├── magus-public-key-netlify/   # EC public key hosted on Netlify for Tesla verification
│   └── well-known/appspecific/
│       └── com.tesla.3p.public-key.pem
│
└── docs/
    ├── post-registration-setup.md   # Full setup guide — start here
    └── battery-consumed-graph.md    # How the Battery Consumed pipeline works end to end
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
| `internal/charging` | User-asserted charge entries (home/work/3rd-party), plus **`charge_sessions`** — a mirror of each Supercharger session's window, site, energy and cost, and the home of the human-verified battery percentages. Owns all charge data the app treats as a charge, whoever reported it. |
| `internal/analytics` | Derived vehicle metrics computed over stored telemetry: rolling Wh/km, the per-day **battery consumed %** (raw SoC delta corrected by both charge sources) and per-day **distance**, plus the gap detection it stores through its own `GapWriter`. Owns **`vehicle_metrics`**, a precomputed read model written by `Recalculator` and read by `Reader` — the derivation is no longer recomputed per request. See [docs/battery-consumed-graph.md](docs/battery-consumed-graph.md). |
| `internal/app` | **Application layer.** Exposes one port, `Processor.ProcessVehicleData`, running one full cycle as three named steps — sync fleet data (`telemetry`) → process charging data (the Supercharger mirror into `charging`) → recalculate analytics — and hosts the daily `Scheduler` that drives it. Owns **no data**: no table, no migration, no pool. Called by `cmd/poller` and, later, the parked manual-rerun API. |
| `internal/gateway` | Gin + Templ + htmx web layer, styled with Node-less Tailwind + DaisyUI (drawer nav, typed `ui/` component kit). The only package allowed to produce HTML. |
| `internal/googleauth` | Google OAuth for user login |
| `internal/config` | Load `.env`, typed config, token persistence |
| `internal/auth` | Tesla OAuth URL, code exchange, token refresh |
| `internal/testdb` | Test-only Postgres provisioning helper (uses `DATABASE_URL` when reachable, else a disposable `postgres:16-alpine` testcontainer). Import from `_test.go` files **only**. |

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
├─ LAYER 3 ── presentation ────────────────────────────────────────────────┤
│  gateway ────────────► account, telemetry, charging, analytics,          │
│    │                   tesla, googleauth                                 │
│    ├─ handlers ──────► account, auth, telemetry, charging,               │
│    │                   analytics, tesla, googleauth, i18n,               │
│    │                   templates/*                                       │
│    ├─ templates/* ───► i18n, templates/ui                                │
│    └─ i18n ──────────► account            (the Language type only)       │
├─ LAYER 2.5 ── application layer ─────────────────────────────────────────┤
│  app ────────────────► telemetry, charging, analytics, account           │
├─ LAYER 2 ── derived read-side ───────────────────────────────────────────┤
│  analytics ──────────► account, charging, telemetry                      │
├─ LAYER 1 ── domain modules ──────────────────────────────────────────────┤
│  telemetry ──────────► account, tesla, telemetry/db                      │
│  charging ───────────► charging/db                                       │
│  account ────────────► auth, account/db                                  │
├─ LAYER 0 ── adapters & leaves (no internal dependencies) ────────────────┤
│  tesla    googleauth    auth    config    testdb    <module>/db          │
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

| Module | sqlc package | Table | What it stores |
|---|---|---|---|
| `internal/account` | `accountdb` | `accounts` | One row per logged-in user — provider identity (`google` + subject id), email, display name, UI language. |
| | | `tesla_tokens` | The Tesla OAuth pair (access + refresh) and access-token expiry, **one row per account** (unique on `account_id`). |
| | | `vehicles` | Tesla vehicles registered to an account — `tesla_id`, VIN, display name, access type, captured vehicle config. |
| `internal/telemetry` | `telemetrydb` | `vehicle_snapshots` | Nightly per-vehicle snapshot: battery/charge, range, odometer, temps, TPMS pressures, lock/sentry — **one row per vehicle per calendar day**, plus the lossless `raw_data` JSONB. Carries **observations only**: the five derived consumption columns moved to `internal/analytics` (RM29 tier 4), which computes them from the exact predecessor rather than reading them back, and latitude/longitude live only in `raw_data`. |
| | | `poll_attempts` | Audit row for **every** collection attempt (outcome + reason), successful or not. Since RM29 tier 7 it also carries `run_id` — every row one `ProcessVehicleData` invocation writes shares one, so per-run facts come from `GROUP BY run_id` — and `triggered_by` (`scheduler` today; `api` once the parked manual-rerun API exists). The table stayed in `internal/telemetry` rather than moving to `internal/app` as the roadmap first planned: it always held our own facts (our clock, our failure classification), never anything Tesla reported. |
| | | `supercharger_sessions` | Tesla Supercharger sessions — site, start/stop, `energy_kwh`, cost + currency, paid flag — upserted on Tesla's `session_id`. Supercharger-only: home / 3rd-party charging never appears in this feed. |
| `internal/charging` | `chargingdb` | `manual_charge_entries` | User-asserted charge sessions (the home / work / 3rd-party gap the Tesla feed can't fill): date, kWh, price + currency, optional times, start/end %, AC-DC, location, **odometer_km**, and a **lifecycle `status`** (`IN_PROGRESS` / `DONE`) whose required-field set lives in Go (`charging.RequiredFieldsFor`), not as a database `CHECK`. `energy_added_kwh` is **optional** since MAG-18 — an in-progress entry may not know it yet — and when the user leaves it blank the module derives it from the pack capacity and the battery delta, recording which happened in **`energy_source`** (`USER` / `ESTIMATED`); that provenance is what lets a future per-vehicle capacity average exclude its own derived output. Also carries **`inferred_capacity_kwh_calc`** — the database-computed pack capacity in kWh this entry implies (`energy_added_kwh / ((end_battery_pct - start_battery_pct) / 100)`, a `GENERATED ALWAYS AS … STORED` column recomputed by the engine on every write and unwritable by any caller), `NULL` when the entry's inputs don't support the formula. |
| | | `charge_sessions` | Mirror of each Supercharger session, one row per (account, Tesla session id): the charge window, site, energy, cost, currency and paid flag, refreshed nightly by `internal/app`'s charging step as Tesla's fees settle — plus the five **human-verified** battery-percentage columns, which the sync structurally cannot touch (`charging.SessionMirror` has no field for them). Source of record for those percentages since RM29 tier 5's sibling tier; `telemetry.supercharger_sessions` keeps the vendor payload. Carries the same **`inferred_capacity_kwh_calc`** generated column as `manual_charge_entries` — the implied pack capacity in kWh, recomputed both when the nightly mirror refreshes `energy_kwh` and when a human corrects the percentages through `SessionVerifier`, and `NULL` when the session's inputs don't support the formula (a missing `energy_kwh` included). |
| `internal/analytics` | `analyticsdb` | `vehicle_metrics` | The precomputed per-day read model — one row per (account, vehicle, day) the vehicle reported, holding the raw observations plus five derived `_calc` columns. **Dense**: a day with no computable predecessor still gets a row, with its `_calc` columns and `consumed_pct` NULL and `flagged` an explicit `false`, which is why both reader queries filter `IS NOT NULL` rather than trusting a zero. Written by `Recalculator`, read by `Reader`. |
| | | `vehicle_metric_watermarks` | One recompute cursor per (account, vehicle, source), three sources. Drives `Reconcile`'s incremental pass; **no row means epoch** — backfill the vehicle's full history. |
| | | `charge_gaps` | Vehicle-days whose battery math doesn't add up because a charge record is missing or incomplete — **one row per (account, vehicle, day)**, with the suspected missing source (`MANUAL` / `SUPERCHARGER`). A live worklist, not an audit trail: no `resolved_at`, a day that stops flagging is deleted by the next nightly reconciliation. Written by `internal/analytics` through its own `GapWriter` port. |
| `internal/gateway` | — | *(none)* | Renders HTML; calls module interfaces, never a database. |
| *(tooling)* | — | `goose_db_version` | Not owned by any module — goose's own ledger, a **single shared table** across all migration dirs. That is why `make migrate-up` runs each dir with `-allow-missing`. |

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

Before committing any change, run **`make generate`** (sqlc + templ + css) then **`make check`**
(build + vet + test). `make up` does generate + migrate + run.

---

## Web UI (gateway)

The web UI lives **only** in `internal/gateway/` — the single module allowed to produce HTML.
It's built on the **GOTH stack**: Go + [Templ](https://templ.guide) + htmx, styled with a
**Node-less** standalone Tailwind CLI + **DaisyUI** (responsive drawer nav, a typed
`templates/ui/` component kit, semantic theme tokens — never hex). Default theme: `lemonade`
(`dark` auto-applies via `prefers-color-scheme`).

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
(`charge_sessions` is sparse). Full contract:
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