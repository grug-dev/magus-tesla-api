# telemetry — module rules

Agent-Name: telemetry

## Doc-Pack (module)

Additive to the base Doc-Pack (repo-root `CLAUDE.md`, `AGENTS.md`, `ai/architecture.md`,
`ai/go-conventions.md`, `ai/agentic-workflow.md`) — never replacing it. This module adds:



(The htmx docs in some modules' packs are irrelevant here — telemetry is a backend
collection/storage module with no HTML.)

## Responsibility

`internal/telemetry/` is the platform's first **collection + storage** domain module. It captures
one immutable snapshot of every connected user's vehicles on a nightly schedule (waking sleeping
vehicles within a bounded timeout), stores the raw `vehicle_data` payload plus extracted typed
fields for dashboards, and records the outcome of every collection attempt with per-vehicle
isolation. It is the foundation for downstream historical features (daily digest, battery-health
log, charge-session detection). Tier 3 of `openspec/roadmaps/nightly-vehicle-telemetry.md`.

It does the work; it does not render it — dashboards read this module's stored data through a port
later (tier 4), never by importing this module's DB package.

## Public interface (the port)

The module's mandatory contract is a Go interface (`ai/go-conventions.md` — interface-first):

- `Collector` — `CollectAll(ctx context.Context) (CycleReport, error)`: run one collection cycle
  over every registered vehicle across all accounts, capturing a snapshot per vehicle and recording
  every attempt. Per-vehicle isolation: one vehicle's failure never aborts the cycle. Returns an
  error only for a whole-cycle failure (e.g. the account enumeration itself failing), never for an
  individual vehicle.
- Domain types (no vendor suffix — our own models, `ai/architecture.md` §6): `Snapshot` (extracted
  typed fields + raw payload; `SentryMode *bool`), the attempt outcome/reason types, `Config`, and
  `CycleReport`.
- The in-app `Scheduler` (constructed with a `Collector` + schedule config) drives `CollectAll`
  daily at 03:30 local; `Run(ctx)` blocks until `ctx` is cancelled (graceful shutdown).

- `Reader` — `LatestSnapshotsByAccount(ctx context.Context, accountID uuid.UUID) ([]Snapshot, error)`:
  return the latest stored `Snapshot` for each vehicle owned by the given account (batch, single
  Postgres `DISTINCT ON` query — no N+1); empty (non-nil) slice when the account has no snapshots.
  `NewReader(pool *pgxpool.Pool) Reader` is the constructor. The gateway (tier 5,
  `gateway-read-stored-vehicles`) depends on this interface, never on `telemetrydb` directly.
  Added by tier 4 (`telemetry-add-snapshot-read-port`).

No HTTP/JSON surface in this module (none required — `ai/architecture.md` §3).

## Allowed / forbidden imports

**May import:**
- `internal/account` — the **public port** (`account.Service`): `AllRegisteredVehicles`,
  `AccessTokenFor`, and its domain types (`OwnedVehicle`) and sentinel `ErrNoTeslaConnection`.
- `internal/tesla` — the **public port** (`tesla.VehicleService`): `ListVehicles`, `WakeUp`,
  `VehicleData`, `Credentials`, `ErrUnauthorized`, and the `...Tesla` DTOs it returns.
- `github.com/jackc/pgx/v5` + `pgxpool` for this module's own store, and the module-scoped
  `internal/telemetry/db` (`telemetrydb`), `github.com/google/uuid`, stdlib.

**Must NOT import:**
- `internal/account/db` (`accountdb`) or `internal/tesla` internals, or any other module's `db`
  package — cross-module data flows only through the public ports (`ai/architecture.md` §2). No FK
  from telemetry tables into account tables either; the boundary is upheld by the flow, not a DB
  constraint.
- `internal/gateway`, `html/template`, `templ` — no HTML in a domain module (`ai/architecture.md`
  §2). `internal/config` is not imported here; the poller `cmd/` passes typed config in.

## Data ownership

Owns two append-only tables in the module-scoped `internal/telemetry/db` (goose migrations are the
single schema source; sqlc generates `telemetrydb`, which **no other module imports**):

- `vehicle_snapshots` — one immutable row per successful capture: `account_id`, `tesla_id`,
  `captured_at`, `raw_data JSONB` (lossless `vehicle_data`), plus extracted typed columns
  (battery level, rated range, charging state, charge limit, odometer, inside/outside temp, locked,
  `sentry_mode` **nullable**, car version, lat/lng). Never overwritten or deleted.
- `poll_attempts` — one row per (vehicle, run): `account_id`, `tesla_id`, `attempted_at`, `outcome`
  (`success`|`failure`), `reason` (`ok`|`asleep-timeout`|`unauthorized`|`api-error`). Doubles as
  future availability / sleep-behavior data.

`account_id`/`tesla_id` are plain columns (no cross-module FK, D2 of the change design). `pgtype`
never leaves the module — convert to/from plain domain types at the DB→domain mapping boundary
(`ai/go-conventions.md` §persistence), mirroring the account module.

## DTO / units conventions

- Miles → km is mandatory and non-negotiable (`ai/go-conventions.md`): every miles/mph field on a
  domain type has a companion value-receiver `Km()`/`Kmh()` method using a package-level
  `milesToKm = 1.609344` constant. **km is NEVER a stored column and never a struct field** — the
  Fleet API sends miles; km is always derived on read. Pointer fields are nil-safe.
- Units are stored API-native (miles) in `vehicle_snapshots`; Tesla temperatures are already
  Celsius (no conversion). Domain types carry **no** vendor suffix; only the `tesla` adapter's
  `...Tesla` DTOs unmarshal Tesla JSON, and this module maps them into clean `Snapshot`s.
- `SentryMode` is `*bool` end to end (nil = not reported, `*false` = off, `*true` = on) mapping to a
  nullable column — preserve the fidelity, never collapse absent into false.

## Testing notes

- Collection-service logic is tested **offline** with fake `account.Service` and
  `tesla.VehicleService` implementations — per-vehicle isolation, reason mapping, one-retry, wake
  timeout, multi-account. NO test may make a live Tesla API call or wake a car (the calls are paid).
- Schedule-time math (`nextRun`) and the wake helper's online-vs-timeout outcomes are unit-tested
  pure (fake clock / short timeout).
- Store tests are `DATABASE_URL`-gated and self-skip when it is unset, so `go test ./...` stays
  green without a database (`ai/go-conventions.md` §persistence). Verify `sentry_mode` nil↔NULL and
  append-only behavior there.
