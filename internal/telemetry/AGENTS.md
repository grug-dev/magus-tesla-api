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

- `Reader` — two read methods:
  - `LatestSnapshotsByAccount(ctx context.Context, accountID uuid.UUID) ([]Snapshot, error)`:
    return the latest stored `Snapshot` for each vehicle owned by the given account (batch, single
    Postgres `DISTINCT ON` query — no N+1); empty (non-nil) slice when the account has no snapshots.
    Added by tier 4 (`telemetry-add-snapshot-read-port`).
  - `SnapshotsByVehicleSince(ctx context.Context, accountID uuid.UUID, teslaID int64, since time.Time) ([]Snapshot, error)`:
    return all snapshots for one vehicle within the given account captured at or after `since`,
    oldest-first; empty (non-nil) slice on no data. Reuses the existing
    `idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at)` ascending index as a
    forward range scan — no new DB object (D3). Safety cap: LIMIT 400 (D4). Window math stays in
    the caller (D1); account_id filter is defense-in-depth tenant isolation (D2).
    Added by RM5 tier 1 (`telemetry-add-snapshot-history-read-port`).
  `NewReader(pool *pgxpool.Pool) Reader` is the constructor. The gateway (tier 5,
  `gateway-read-stored-vehicles`) depends on this interface, never on `telemetrydb` directly.

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

Owns two tables in the module-scoped `internal/telemetry/db` (goose migrations are the
single schema source; sqlc generates `telemetrydb`, which **no other module imports**):

- `vehicle_snapshots` — **no longer append-only** (superseded by
  `telemetry-dedupe-daily-snapshots`, migration `20260805000001` — see below): at most one row
  per `(account_id, tesla_id, captured_date)`, enforced by the
  `vehicle_snapshots_account_tesla_date_unique` constraint. A same-day re-capture **REPLACES**
  the existing row via `ON CONFLICT ... DO UPDATE` — the newest capture for a calendar day always
  wins (design D1). Columns: `account_id`, `tesla_id`, `captured_at` (the precise capture
  instant), `captured_date DATE` (the calendar day, **Go-computed** from `captured_at` in the
  poller's configured timezone — `Config.Location`/`dateOnly`, design D2; never a DB expression,
  since a UNIQUE index cannot depend on the runtime `POLLER_TIMEZONE` env var), `raw_data JSONB`
  (lossless `vehicle_data`), plus extracted typed columns (battery level, rated range, charging
  state, charge limit, odometer, inside/outside temp, locked, `sentry_mode` **nullable**, car
  version, 5 charge-enrichment fields, and `max_range_charge_counter` **nullable** — see migration
  20260801000001). Dropped columns (`latitude`, `longitude`, `fast_charger_type`) remain lossless
  in `raw_data`. `updated_at TIMESTAMPTZ` is the audit trail for a replace: `DEFAULT now()` on a
  fresh insert, explicitly set to `now()` on a same-day conflict-update (design D5).
- `poll_attempts` — **unaffected, still append-only/immutable** (design D4): one row per
  (vehicle, run): `account_id`, `tesla_id`, `attempted_at`, `outcome` (`success`|`failure`),
  `reason` (`ok`|`asleep-timeout`|`unauthorized`|`api-error`). Doubles as future availability /
  sleep-behavior data — a daily collapse would destroy that signal, so this table is explicitly
  out of scope for the dedupe change.

`account_id`/`tesla_id` are plain columns (no cross-module FK, D2 of the change design). `pgtype`
never leaves the module — convert to/from plain domain types at the DB→domain mapping boundary
(`ai/go-conventions.md` §persistence), mirroring the account module.

## DTO / units conventions

- **SUPERSEDED (telemetry-store-display-units, migration `20260806000001`): units are now stored
  in their DISPLAY unit, converted exactly once at capture time.** `vehicle_snapshots` stores km /
  °C / PSI, not miles/bar — the opposite of the rule this section used to state. Every unit-bearing
  column and `Snapshot` field carries its unit as a name suffix (`OdometerKm`, `TpmsPressureFLPSI`,
  `InsideTempC`, …), so the unit is self-describing at every call site. This module holds **zero**
  conversion constants and exposes **zero** read-time conversion methods — `milesToKm` and
  `barToPSI` were deleted from `telemetry.go`; the six companion methods that used to convert
  `BatteryRange`/`Odometer`/`TpmsPressure*` on read were deleted too (a field and a method of the
  same name cannot coexist in Go, so renaming the fields to carry the unit forced the deletion —
  design D4). Conversion happens exactly once, in `snapshotFrom` (`service.go`), by calling the
  `tesla` adapter's `Km()`/`PSI()` companions on the vendor DTO — **never** by multiplying inline
  here. `internal/tesla` remains the single owner of `milesToKm` / `barToPSI`; this module only
  calls its companions.
- This inverts the prior "API-native storage, derived on read" rule specifically because reads
  vastly outnumber writes (CLAUDE.md's read-heavy Performance-Profile): the nightly poller writes
  once per vehicle per night, while every dashboard render, chart, and API consumer used to pay
  the conversion cost on every read. Converting once at write time and never on read matches that
  asymmetry.
- The **NULL-vs-zero invariant is unchanged and still binding**: for nullable columns (the four
  TPMS pressure fields and the Source A charge-enrichment fields), NULL means "not reported at
  capture, or the row predates this extraction"; a truthfully reported `0` is stored non-NULL via
  `ptr()` in `snapshotFrom`, applied to the ALREADY-CONVERTED value (D12/DSA3 convention).
- Tesla temperatures are already Celsius — `InsideTempC`/`OutsideTempC` are assigned straight from
  the DTO with no conversion. Domain types carry **no** vendor suffix; only the `tesla` adapter's
  `...Tesla` DTOs unmarshal Tesla JSON, and this module maps them into clean `Snapshot`s.
- `raw_data JSONB` is UNCHANGED by this and stays lossless in the Fleet API's native units (miles/
  bar) — only the typed, extracted columns moved to display units. A backfill from `raw_data` must
  still apply the conversion itself; the raw payload was never converted.
- `SentryMode` is `*bool` end to end (nil = not reported, `*false` = off, `*true` = on) mapping to a
  nullable column — preserve the fidelity, never collapse absent into false.
- `MaxRangeChargeCounter` is `*int` end to end (nil = pre-migration row / not reported, `*0` = new
  vehicle / never charged to max-range, `*N` = charged to max-range N times). Mirrors the same
  pointer-wrap convention as the 5 Source A charge-enrichment fields (D12/DSA3). SQL NULL for
  pre-migration rows; the Up migration backfills from `raw_data->'charge_state'->'max_range_charge_counter'`
  where the JSONB path exists.

## Testing notes

- Collection-service logic is tested **offline** with fake `account.Service` and
  `tesla.VehicleService` implementations — per-vehicle isolation, reason mapping, one-retry, wake
  timeout, multi-account. NO test may make a live Tesla API call or wake a car (the calls are paid).
- Schedule-time math (`nextRun`) and the wake helper's online-vs-timeout outcomes are unit-tested
  pure (fake clock / short timeout).
- Store tests use the shared `internal/testdb` helper (see `testdb_test.go`). When
  `DATABASE_URL` is set AND reachable, that managed Postgres is used; otherwise
  `TestMain` auto-provisions a disposable `postgres:16-alpine` container via
  testcontainers-go and applies the goose migrations embedded under `db/migrations/`.
  `go test ./...` (and `make check`) are green with zero manual DB setup as long as
  Docker is running locally. Verify `sentry_mode` nil↔NULL and append-only behavior
  there.
