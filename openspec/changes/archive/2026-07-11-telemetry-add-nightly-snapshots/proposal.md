## Why

The platform's whole value is historical: it must turn each user's live vehicle data into
long-term trends (`AGENTS.md` — "Historical data over current snapshots"). To do that it needs
a server-side job that, unattended and per night, captures one anchor snapshot of every
connected user's vehicles and stores it immutably — the foundation every downstream feature
(daily digest, battery-health log, charge-session detection) builds on.

Tiers 1 and 2 of `openspec/roadmaps/nightly-vehicle-telemetry.md` shipped the two ports this job
consumes: the `tesla` adapter now returns a typed **and** raw `VehicleData` plus `WakeUp`
(`internal/tesla`), and the `account` port now enumerates every registered vehicle across all
accounts via `AllRegisteredVehicles` and resolves a token per account via `AccessTokenFor`
(`internal/account`). What is missing is the module that owns the collection logic and its
storage. Nothing currently persists vehicle snapshots or records collection attempts, and there
is no scheduled runner. This tier 3 adds that module and a thin `cmd/poller` to run it.

Users NEVER trigger Tesla API calls on demand (`AGENTS.md` §"Data Access Model") — the only
user-initiated Tesla call is the vehicle list at first connect. A scheduled wake for the nightly
anchor snapshot is the sanctioned, bounded exception the Data Access Model calls out, because no
on-demand collection path exists.

## What Changes

- **New module `internal/telemetry/`** — the first collection/storage domain module. It owns two
  append-only tables and a collection service; it consumes the `account` and `tesla` **ports
  only**, never their tables.
- **New persistence (module-scoped `internal/telemetry/db/`, goose + sqlc):**
  - `vehicle_snapshots` — one immutable row per successful capture: owning `account_id`, vehicle
    `tesla_id`, `captured_at`, the full raw `vehicle_data` JSON in a `jsonb` column, **plus**
    extracted typed columns for dashboards (battery level, rated range, charging state, charge
    limit, odometer, inside/outside temp, locked, sentry mode, car version, lat/lng). Units stay
    API-native (miles); km is NEVER a column — `Km()`/`Kmh()` companions live on the telemetry
    **domain** types (`ai/go-conventions.md` non-negotiable).
  - `poll_attempts` — one row per (vehicle, run): `account_id`, `tesla_id`, `attempted_at`,
    `outcome` (`success` | `failure`), `reason` (`ok`, `asleep-timeout`, `unauthorized`,
    `api-error`, …). Doubles as future availability / sleep-behavior data.
- **Collection service** (`internal/telemetry`, interface-first port): enumerate
  `AllRegisteredVehicles`; per account resolve a token via `AccessTokenFor`; if a vehicle is
  asleep, `WakeUp` and poll `ListVehicles` state until `online` within a bounded timeout (~90 s,
  configurable); then `VehicleData` and store BOTH the typed columns and the raw JSONB; record a
  `poll_attempts` row for every vehicle. **Per-vehicle isolation:** one vehicle's failure never
  aborts the others; one bounded retry per vehicle per run. An expired/revoked token
  (`tesla.ErrUnauthorized` or `account.ErrNoTeslaConnection`) is recorded with reason
  `unauthorized` and the run moves on.
- **In-app daemon scheduler** (`internal/telemetry`): runs a collection cycle daily at
  **03:30 local** (hour/minute/timezone configurable via env), with graceful shutdown on
  SIGINT/SIGTERM. The schedule ships with the app — no OS cron.
- **New thin `cmd/poller`** — loads config, builds the pgxpool + account service + tesla client +
  telemetry service + scheduler, runs until signal. Zero business logic (mirrors `cmd/web`).

**Not breaking.** The change only **adds** a module, two tables, a port, and a command. No
existing public method, type, table, or query is removed or renamed. The `account` and `tesla`
ports are consumed exactly as they exist after tiers 1–2 — neither is modified. No production
gateway code changes.

The roadmap failure policy says an expired/revoked token should also "flag the account's
connection as broken". Surfacing a broken connection to the user would require a NEW `account`
port method (a write into another module) — that is **out of tier-3 scope**: telemetry MUST NOT
reach into account internals or invent a cross-module write here. For tier 3 the broken
connection is recorded in `poll_attempts` (reason `unauthorized`); "surface broken-connection to
the dashboard" is recorded as **future work**.

## Capabilities

### Added Capabilities

- `telemetry`: A new capability that captures one immutable nightly snapshot of every connected
  user's vehicles (waking sleeping vehicles within a bounded timeout), stores the raw payload plus
  extracted typed fields, records the outcome of every collection attempt with per-vehicle
  isolation, and runs unattended on an in-app daily schedule.

## Impact

- **New module / code**
  - `internal/telemetry/telemetry.go` — public port (collection service interface) + domain types
    (snapshot / attempt), with `Km()`/`Kmh()` companions on miles-derived fields.
  - `internal/telemetry/service.go` — the collection service: ports → store, per-vehicle
    isolation, one bounded retry, `pgtype`→domain mapping at the boundary.
  - `internal/telemetry/wake.go` — bounded wake-then-poll-until-online orchestration.
  - `internal/telemetry/scheduler.go` — in-app 03:30-local daemon with graceful shutdown.
  - `internal/telemetry/db/` — goose migration (`vehicle_snapshots`, `poll_attempts`) + `query.sql`
    + sqlc-generated `telemetrydb` package.
  - `internal/telemetry/AGENTS.md` — module rules (created with this change).
  - `cmd/poller/main.go` — thin wiring (mirrors `cmd/web`).
- **Repo-root (leader-integrated, outside the telemetry sandbox):**
  - `sqlc.yaml` — a second `sql:` entry generating package `telemetrydb` into
    `internal/telemetry/db` (one entry per module, per `ai/go-conventions.md` §persistence).
  - `Makefile` — extend the goose migration wiring to also run `internal/telemetry/db/migrations`
    (today `MIGRATIONS_DIR` is a single account-only dir; a second module's migrations need their
    own `-dir` run so `make db-setup` applies both).
  - `internal/config/config.go` — read the poller's env (schedule hour/minute/timezone, wake
    timeout). `os.Getenv` stays confined to `internal/config` per the convention.
- **Specs**: new `specs/telemetry/spec.md` (`## ADDED Requirements` — the new `telemetry`
  capability).
- **Dependencies**: none new — existing pgx / pgxpool / sqlc / uuid / stdlib stack.
- **Migrations**: one new goose migration creating `vehicle_snapshots` + `poll_attempts` in the
  telemetry-scoped `internal/telemetry/db/migrations` dir. Applied with `make db-setup` /
  `make migrate-up`; never as part of a build.
- **Operational**: the poller makes Tesla API calls (including sanctioned wakes) on a nightly
  schedule; it writes append-only rows. It must run as a separate long-lived process from
  `cmd/web`. `DATABASE_URL` required.

> Grill-me was run by the leader — the binding design decisions (cadence, wake policy, snapshot
> content, failure policy, scheduler, module name) are recorded in the **Decisions** section of
> `openspec/roadmaps/nightly-vehicle-telemetry.md` and drive this proposal.
