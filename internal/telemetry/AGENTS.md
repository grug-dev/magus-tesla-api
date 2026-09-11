# telemetry — module rules

Agent-Name: telemetry

## Doc-Pack (module)

Extends the project base Doc-Pack (`CLAUDE.md` → "Pipeline config") — never replaces it, and
never restates it: the base list lives in `CLAUDE.md` alone, so a copy here cannot drift.
A dispatched worker reads: base pack + this list + this file, before any write.

*(empty — this module needs nothing beyond the base pack. It is backend-only: no HTML,
no Templ, no htmx.)*

## Responsibility

`internal/telemetry/` is the platform's first **collection + storage** domain module. It captures
one immutable snapshot of every connected user's vehicles on a nightly schedule (waking sleeping
vehicles within a bounded timeout), stores the raw `vehicle_data` payload plus extracted typed
fields for dashboards, and records the outcome of every collection attempt with per-vehicle
isolation. It is the foundation for downstream historical features (daily digest, battery-health
log, charge-session detection). Tier 3 of `openspec/roadmaps/nightly-vehicle-telemetry.md`.

It does the work; it does not render it — dashboards read this module's stored data through a port
later (tier 4), never by importing this module's DB package.

### Why nightly collection exists at all (the Tesla-API constraint)

The nightly poller is not a refresh job — it is the **only** way the platform acquires vehicle
state history. Tesla's `GET /api/1/vehicles/{id}/vehicle_data` returns the **current** vehicle
state only and accepts **no date/time filter** (verified at `internal/tesla/vehicles.go:VehicleData`
— a bare GET, no query params). There is no Fleet API endpoint that returns a vehicle's past
battery level, odometer, or state from yesterday, last week, or last month. Consequently the
platform cannot "query Tesla for last month's battery" — it must **accumulate history one
snapshot at a time**, captured by this nightly cycle. Every historical metric, trend, and forecast
the platform produces is derived from the rows this collector writes. Missing a nightly run means
a permanent gap in the time series that no later API call can backfill. This is the durable reason
the design biases toward "always capture, bounded wake, per-vehicle isolation" rather than "skip if
the vehicle looks unchanged" — a skipped capture is lost forever.

The one Tesla endpoint the poller calls that *does* accept a date range is
`GET /api/1/dx/charging/history` (`startTime`/`endTime`, supported by
`tesla.ChargingHistoryParams`). The poller deliberately leaves both **empty**
(`service.go:collectChargingHistory` passes `ChargingHistoryParams{}`), so the full account
Supercharger history is re-fetched every night and upserted by `session_id` (see
`supercharger_history` below — renamed from `supercharger_sessions` by RM39 tier 4,
roadmap D5a). The dedup/idempotency mechanism is the table's `UNIQUE (session_id)`
constraint + `ON CONFLICT DO UPDATE` (refreshing only mutable columns — `raw_data`, `energy_kwh`,
`total_cost`, `currency`, `is_paid`, `tesla_id`, `updated_at`), NOT any application-level
"what's new since last run" logic. Re-upserting the full history every night is intentional:
billing state (`is_paid`, invoice status) mutates post-session, so a session row is never "done"
on first insert. `tesla_id` is resolved from a VIN→TeslaID map built from the account's currently
registered vehicles; sessions for VINs no longer registered get `tesla_id = NULL` (row kept, VIN
preserved). The three battery-% verification columns are deliberately excluded from the upsert —
they are a human-owned channel that the nightly poller must never overwrite (see "Battery-%
verification columns" below).

## Public interface (the port)

The module's contract is a set of Go interfaces (`ai/go-conventions.md` — interface-first).
**Signatures and the per-method doc comments live in `internal/telemetry/telemetry.go` — read
them there.** They are deliberately not copied here.

| Port | What it is for | Constructor |
|---|---|---|
| `Collector` | `CollectAll` — run one collection cycle over every registered vehicle, all accounts | — |
| `Reader` | Four snapshot reads: latest-per-vehicle, since, between, and the single preceding day | `NewReader(pool)` |
| `SuperchargerHistoryReader` | Four reads over `supercharger_history` | `NewSuperchargerHistoryReader(pool)` |
| `RunWriter` | `RecordRun` — one `poll_runs` row per cycle | — |

Plus the domain types (no vendor suffix — `ai/architecture.md` §6): `Snapshot`,
`SuperchargerHistory`, `Attempt`, `Config`, `CycleReport`, `RunContext` and `TriggeredBy`.

What the source does not tell you:

- **Per-vehicle isolation is the contract.** One vehicle's failure never aborts the cycle.
  `CollectAll` returns an error only for a whole-cycle failure, never for one vehicle.
- **`RunContext` is a parameter, never a field.** `internal/app` generates it fresh once per
  call and it is threaded `CollectAll → collectAccount → record`. Never store it on the
  service — that would share one run's identity across cycles.
- **`telemetry` owns `RunContext` and `TriggeredBy` because it owns the `poll_attempts`
  columns they fill**, even though `internal/app` is what creates them.
- **`Scheduler` / `NewScheduler` are NOT part of this module any more.** They relocated to
  `internal/app`, with their four tests. `LogCycle` / `formatFailures` stayed (`report.go`),
  and `LogCycle` remains exported because its caller is now `internal/app`'s `Scheduler.Run`.
- **`SuperchargerHistoryByAccountUpdatedSince` is the ONLY method on that port that can
  return a session with `tesla_id IS NULL`** — a session whose vehicle is no longer registered
  to the account. That is deliberate, not a bug: a per-vehicle read can never see such a row,
  so this account-wide method is how `internal/charging`'s mirror recovers a session once its
  vehicle re-registers. It takes no `teslaID` for exactly that reason.
- **Every caller depends on the interface, never on `telemetrydb`.**

No HTTP/JSON surface in this module (`ai/architecture.md` §3).
## Allowed / forbidden imports

**May import:**
- `internal/account` — the **public port** (`account.Service`): `AllRegisteredVehicles`,
  `AccessTokenFor`, and its domain types (`OwnedVehicle`) and sentinel `ErrNoTeslaConnection`.
- `internal/tesla` — the **public port** (`tesla.VehicleService`): `ListVehicles`, `WakeUp`,
  `VehicleData`, `Credentials`, `ErrUnauthorized`, and the `...Tesla` DTOs it returns.
- `internal/clock` — `Zone()`/`Now()`/`CalendarDay()`, the platform's default-zone "now" and
  calendar-day primitives (`RM35-telemetry-adopt-clock`, roadmap D4). `internal/clock` imports
  stdlib `time` only, so this creates no import cycle.
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

Owns four tables, all in the dedicated **`telemetry` Postgres schema**: `vehicle_snapshots`,
`poll_attempts`, `supercharger_history`, and `poll_runs`. Module-scoped
`internal/telemetry/db`: goose migrations are the single schema source, sqlc generates
`telemetrydb`, and **no other module imports it**.

| Table | Grain | Write shape |
|---|---|---|
| `vehicle_snapshots` | one row per (account, vehicle, `captured_date`) | UPSERT — a same-day re-capture REPLACES the row |
| `poll_attempts` | one row per (vehicle, run) | append-only, immutable |
| `supercharger_history` | one row per Tesla `session_id` | UPSERT — billing state mutates after the session |
| `poll_runs` | one row per `run_id` (PK) | plain INSERT, exactly once per cycle. A duplicate is a caller bug and fails loudly. |

Rules that bind across all four:

- **Every table reference must be schema-qualified** (`telemetry.<table>`) in `db/query.sql`
  and in every `_test.go`. sqlc resolves names statically at generate time, so a bare table
  name here is a bug, not a style choice.
- **`captured_date` is Go-computed**, from `captured_at` in the platform zone via
  `clock.CalendarDay` — never a DB expression. A UNIQUE index cannot depend on a runtime env
  var.
- **`account_id` / `tesla_id` are plain columns. No cross-module FK**, in either direction.
  The boundary is upheld by the flow, not by a constraint.
- **`pgtype` never leaves the module.** Convert to and from plain domain types at the
  DB→domain mapping boundary, mirroring `internal/account`.
- **`charge_gaps` is NOT owned here any more** — it moved to `internal/analytics` with its
  table, migration and `GapWriter` port. This module never read it back.

**Column-by-column detail — every constraint, every dropped column and why, the
`raw_data` losslessness rule, and the battery-% verification trio — lives in
`kkpa/context/architecture/telemetry-tables.md`.** Fetch it before adding or changing a
column.
## DTO / units conventions

Platform-wide rule (normative): `openspec/specs/unit-of-measure/spec.md` and
`ai/go-conventions.md`. `vehicle_snapshots` is the table that rule was generalised from, so
this module is where it is easiest to break.

- **Units are stored in their DISPLAY unit, converted exactly once at capture time.** km, °C,
  PSI — never miles or bar. Every unit-bearing column and `Snapshot` field carries its unit as
  a name suffix (`OdometerKm`, `InsideTempC`, `TpmsPressureFLPSI`, …).
- **This module holds ZERO conversion constants and ZERO read-time conversion methods.**
  Conversion happens in `snapshotFrom` (`service.go`) by calling the `tesla` adapter's
  `Km()` / `PSI()` companions on the vendor DTO — **never** by multiplying inline here.
  `internal/tesla` is the single owner of `milesToKm` / `barToPSI`.
- **`raw_data JSONB` stays lossless in the Fleet API's native units** (miles, bar). Only the
  typed extracted columns are converted. A backfill reading `raw_data` must apply the
  conversion itself.
- **NULL means "not reported", a real `0` is stored non-NULL.** For every nullable column,
  wrap the ALREADY-CONVERTED value with `ptr()` in `snapshotFrom`. Never collapse "absent"
  into a zero.
- **`SentryMode` is `*bool` and `MaxRangeChargeCounter` is `*int`, end to end.** nil means not
  reported; `*false` / `*0` are real values. Preserve the fidelity.
- Tesla temperatures are already Celsius — assigned straight from the DTO, no conversion.
- **Domain types carry no vendor suffix.** Only the `tesla` adapter's `…Tesla` DTOs unmarshal
  Tesla JSON; this module maps them into clean `Snapshot`s.

**The five derived-consumption figures are NOT here any more.** `DistanceTraveledKmCalc`,
`BatteryUsedPctCalc`, `KmPerPctCalc`, `EstimatedRangeKmCalc` and `DaysSpannedCalc` were
dropped from `Snapshot` and from the table; `internal/analytics` computes them from the
surviving `OdometerKm` / `BatteryLevelPct` / `CapturedDate` via `Reader.SnapshotPrecedingDay`.
Do not re-add a derived column here — that is the boundary blur the move existed to fix.

**The Supercharger battery-% trio is human-owned and never auto-written.**
`start_battery_pct`, `end_battery_pct` and `battery_pct_source` are excluded from
`UpsertSuperchargerHistory`'s `INSERT` list AND its `ON CONFLICT DO UPDATE SET`, deliberately:
the nightly poller re-upserts every session because Tesla billing state mutates after the
session, so binding any of the three would silently overwrite a human's verified reading on
the next run. `battery_pct_source` never stores `"estimated"`. Column detail and the full
history: `kkpa/context/architecture/telemetry-tables.md`.
## Testing notes

- **No test may make a live Tesla API call or wake a car.** The calls are paid. Collection
  logic is tested offline with fake `account.Service` and `tesla.VehicleService`
  implementations — per-vehicle isolation, reason mapping, one-retry, wake timeout,
  multi-account.
- **`Scheduler` and its four tests are in `internal/app`, not here.** They relocated with the
  type. That is a move, not a coverage drop — look there before concluding anything is
  missing.
- **DB-backed tests SKIP when there is no Postgres at all; a broken migration still fails
  LOUDLY.** `TestMain` skips only on `testdb.ErrUnavailable` (no Docker daemon). Every other
  `Provision` failure — above all `goose up` failing — stays `log.Fatalf`. **Keep that
  distinction.** Widening the skip would let a broken schema pass `make check` in silence.
- **A green suite does NOT by itself prove the DB tests ran.** To actually exercise them start
  Docker and run `make test` (disposable container), or
  `env -u DATABASE_URL go test ./internal/telemetry/ -run TestStore_ -v`, and confirm the
  output says `PASS` and not `SKIP`.
- **`make test-with-db` runs against whatever `DATABASE_URL` points at and applies migrations
  to it.** Only ever point it at a throwaway or CI Postgres — never the owner's live database.
- Store tests use the shared `internal/testdb` helper (`testdb_test.go`): `DATABASE_URL` when
  set and reachable, otherwise a disposable `postgres:16-alpine` via testcontainers with the
  embedded goose migrations applied.
- **Two schema self-checks must keep passing as the tables grow.** The change-detecting
  upsert's comparison must cover exactly the columns its `SET` clause writes, with every other
  column on an explicit deny-list; `db_change_detection_schema_test.go` derives the real column
  list from `information_schema` at run time and fails by name when a new column lands on
  neither side. Do not "fix" that test by adding the column to the deny-list without deciding
  which bucket it belongs to.
- **Nothing may log a credential or a `raw_data` payload.** `query_log.go`'s decorators and
  `call_counter.go` are structurally prevented from it, and
  `TestCallCounter_NeverLogsCredentials` / `TestQueryLog_NeverLogsRawDataContent` prove it.

Which test file covers what: `ls internal/telemetry/*_test.go`. The names say it.
