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

The module's mandatory contract is a Go interface (`ai/go-conventions.md` — interface-first):

- `Collector` — `CollectAll(ctx context.Context, run RunContext) (CycleReport, error)`: run one
  collection cycle over every registered vehicle across all accounts, capturing a snapshot per
  vehicle and recording every attempt. `run` identifies the invocation (`RunContext.RunID`/
  `TriggeredBy`) and is generated fresh by `internal/app` once per call (`uuid.New()`), then
  threaded straight through to every `poll_attempts` row the cycle writes — `CollectAll` never
  generates or caches a `RunContext` itself; it is a plain parameter threaded
  `CollectAll → collectAccount → record`, never stored as a field on the service (a shared,
  long-lived object reused across cycles). Per-vehicle isolation: one vehicle's failure never
  aborts the cycle. Returns an error only for a whole-cycle failure (e.g. the account enumeration
  itself failing), never for an individual vehicle. Widened by
  `RM29-app-add-process-vehicle-data` (design.md D5).
- `RunContext{RunID uuid.UUID; TriggeredBy TriggeredBy}` and `TriggeredBy` (a string enum,
  `TriggeredByScheduler` | `TriggeredByAPI`) — new exported types, added by the same change.
  `telemetry` owns both because it owns the `poll_attempts` columns they fill (design.md D5); the
  module that generates a fresh `RunContext` per invocation (`internal/app`) only consumes the
  type, it does not declare it.
- Domain types (no vendor suffix — our own models, `ai/architecture.md` §6): `Snapshot` (extracted
  typed fields + raw payload; `SentryMode *bool`), the attempt outcome/reason types, `Attempt`
  (now also carrying `RunID`/`TriggeredBy`), `Config`, and `CycleReport`.
- **`Scheduler`/`NewScheduler` are NO LONGER part of this module's public surface.** They
  **relocated** to `internal/app` (`RM29-app-add-process-vehicle-data` design.md D4, carrying
  owner decision RD8, which superseded an earlier RD5 plan to send them to `cmd/poller`) — this is
  a relocation, not a deletion or a coverage loss: their four tests (`TestNextRun` and the three
  `TestScheduler_*` tests) moved with them, intact, into `internal/app/scheduler_test.go` (design's
  Test Contract group S). `LogCycle`/`formatFailures` stayed here — they never depended on
  `Scheduler` — and now live in `report.go`; `LogCycle` remains **exported** because its new
  cross-boundary caller is `internal/app`'s relocated `Scheduler.Run`, calling
  `telemetry.LogCycle(report, err)` after each `Processor.ProcessVehicleData` invocation, exactly
  where `Scheduler.Run` called it before the move.

- `Reader` — four read methods:
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
  - `SnapshotsByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]Snapshot, error)`:
    return the nightly snapshots for one vehicle within the given account whose
    **`EffectiveDate` calendar day** falls in the caller-supplied `[start, end]` window
    **inclusive**, ordered ascending by EffectiveDate (== ascending by `captured_at`).
    `start`/`end` are whole UTC-midnight-bounded calendar days; `end` is inclusive
    (Decision #2). The port is a clean bounded window with **no lookback parameter**
    (lookback is a gateway concern — Decision #4); the caller supplies any lookback by
    passing `start - 1 day` as `start`. Filters on `captured_at` TIMESTAMPTZ (NOT the
    poller-zone `captured_date`) with bounds derived from the window
    (`start_bound = start + 1 day`, `end_bound = end + 2 days`, half-open
    `>= start_bound AND < end_bound`) so `EffectiveDate ∈ [start, end]` ⟺ `CapturedAt ∈
    [start+1, end+2)` — the translation lives inside the `dbStore` impl (design D1/D5),
    never in the public signature. Reuses the existing
    `idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at)` ascending
    index as a forward range scan — no new DB object (D2). Safety cap: LIMIT 400 (D3,
    parity with `Since`'s D4). `account_id` filter is defense-in-depth tenant isolation.
    Returns an empty (non-nil) slice and nil error on no data. Reuses the single
    `rowToSnapshot` mapper (no per-method duplication). Additive alongside
    `SnapshotsByVehicleSince` (kept unchanged — design D4).
    Added by RM8 tier 1 (`RM8-telemetry-between-range-port`).
  - `SnapshotPrecedingDay(ctx context.Context, accountID uuid.UUID, teslaID int64, day time.Time) (*Snapshot, error)`:
    return the single most recently captured snapshot for one vehicle within the given account
    whose `CapturedDate` is strictly before `day`, or `(nil, nil)` when the vehicle has no
    earlier snapshot at all (its first-ever capture) — a genuine query error is returned as-is
    and MUST NOT be degraded to "no predecessor". The bound is `captured_date < @day` (the
    poller-zone calendar day, stamped once on the write path by `clock.CalendarDay`), never `captured_at`,
    so the predicate is zone-free at query time and a same-day re-capture cannot select its own
    about-to-be-replaced row as its own predecessor. Reuses the existing
    `idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at)` index as a backward
    scan off its two leading equality columns — no new index; `vehicle_snapshots_account_tesla_date_unique`
    guarantees at most one row is examined and rejected by the `captured_date` residual before the
    match (verified via `EXPLAIN` in the DB-integration test). Unlike the two bounded-window
    methods above, there is no lookback limit — it reaches the TRUE predecessor however old. This
    is the module's former private `previousSnapshot` store seam promoted to the public port; its
    only intended caller is `internal/analytics`' `Recalculate`. Added by
    `RM29-telemetry-drop-derived-columns` (design D2, MAG-26 tier 4).
  `NewReader(pool *pgxpool.Pool) Reader` is the constructor. The gateway (tier 5,
  `gateway-read-stored-vehicles`) depends on this interface, never on `telemetrydb` directly.

- `SuperchargerHistoryReader` — exposes `supercharger_history` (renamed from
  `supercharger_sessions` by `RM39-telemetry-move-to-own-schema` tier 4, roadmap D5a/D5c)
  for read-only consumption, a separate port from `Reader` (snapshot-centric).
  **Fully renamed (RM39-telemetry-rename-supercharger-port, roadmap tier 5, closes
  design D6 of tier 4):** the port name `SuperchargerHistoryReader`, its four method
  names below (`SuperchargerHistoryBy…`), the constructor `NewSuperchargerHistoryReader`,
  and the unexported `superchargerHistoryReader` / `rowToSuperchargerHistory` /
  `upsertSuperchargerHistory` helpers now all match the table (`supercharger_history`)
  and the domain type (`SuperchargerHistory`) they have returned/handled since tier 4.
  Tier 4's half-renamed state — the port still saying `…Session…` while returning
  `[]SuperchargerHistory` — is retired; there is no remaining vocabulary mismatch on
  this port.
  - `SuperchargerHistoryByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]SuperchargerHistory, error)`:
    all sessions for an account, ordered `charge_start_date_time DESC`, limited to `limit`
    rows (`0` = server default `math.MaxInt32`). Non-nil empty slice when none exist.
  - `SuperchargerHistoryByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]SuperchargerHistory, error)`:
    same shape, scoped to one vehicle within the account.
  - `SuperchargerHistoryByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]SuperchargerHistory, error)`:
    sessions for one vehicle whose **`ChargeStopDateTime`** falls in the caller-supplied
    `[start, end]` window, inclusive of the whole `end` calendar day, ordered oldest-first
    (ascending by `ChargeStopDateTime`). `start`/`end` are whole UTC-midnight-bounded calendar
    days, matching the platform's HTTP date-filter convention. Filters on
    `ChargeStopDateTime`, NOT `ChargeStartDateTime` (roadmap D12): energy is fully delivered
    at session stop, so a session belongs to the window containing its STOP instant even when
    it started the day before — a session spanning midnight (start before `start`, stop inside
    `[start, end]`) is deliberately **included**. `endBound = end.AddDate(0, 0, 1)` is computed
    in Go (`reader.go`), mirroring `Reader.SnapshotsByVehicleBetween`'s own bounds-translation
    precedent, so the underlying SQL's half-open `>= start AND < endBound` includes every
    instant of the end calendar day. No `limit` parameter — the caller-supplied window is the
    bound. No new index added for this method (see design.md's Index Plan for the documented
    trade-off and fallback). Reuses the existing `rowToSuperchargerHistory` mapper — no new
    field, no new mapper. Non-nil empty slice when none exist (parity with the other two
    methods). `account_id` AND `tesla_id` filter provides defense-in-depth tenant isolation.
    Added by `RM28-telemetry-add-charge-gap-storage` (MAG-15, roadmap D9/D12).
  `NewSuperchargerHistoryReader(pool *pgxpool.Pool) SuperchargerHistoryReader` is the
  constructor; implementation in `reader.go`. Callers MUST NOT import `telemetrydb`
  (design DBS6).

No HTTP/JSON surface in this module (none required — `ai/architecture.md` §3).

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

Owns five tables, all living in the dedicated **`telemetry` Postgres schema**
(`RM39-telemetry-move-to-own-schema` tier 4, MAG-31, migration `20260903000001`; moved
out of `public` — every table reference in `db/query.sql` and in this module's
`_test.go` files is schema-qualified as `telemetry.<table>`, and a bare, unqualified
table name in new code here is a bug, not a style choice, because sqlc resolves names
statically at generate time). Module-scoped `internal/telemetry/db` (goose migrations
are the single schema source; sqlc generates `telemetrydb`, which **no other module
imports**):

- `vehicle_snapshots` — **no longer append-only** (superseded by
  `telemetry-dedupe-daily-snapshots`, migration `20260805000001` — see below): at most one row
  per `(account_id, tesla_id, captured_date)`, enforced by the
  `vehicle_snapshots_account_tesla_date_unique` constraint. A same-day re-capture **REPLACES**
  the existing row via `ON CONFLICT ... DO UPDATE` — the newest capture for a calendar day always
  wins (design D1). Columns: `account_id`, `tesla_id`, `captured_at` (the precise capture
  instant), `captured_date DATE` (the calendar day, **Go-computed** from `captured_at` in the
  poller's configured timezone — `Config.Location`/`clock.CalendarDay`, design D2; never a DB expression,
  since a UNIQUE index cannot depend on the runtime `POLLER_TIMEZONE` env var), `raw_data JSONB`
  (lossless `vehicle_data`), plus extracted typed columns (battery level, rated range, charging
  state, charge limit, odometer, inside/outside temp, locked, `sentry_mode` **nullable**, car
  version, 5 charge-enrichment fields, and `max_range_charge_counter` **nullable** — see migration
  20260801000001). Dropped columns (`latitude`, `longitude`, `fast_charger_type`) remain lossless
  in `raw_data`. `updated_at TIMESTAMPTZ` is the audit trail for a replace: `DEFAULT now()` on a
  fresh insert, explicitly set to `now()` on a same-day conflict-update (design D5). The five
  per-day consumption figures once stored here were **dropped** by migration `20260822000001`
  (`RM29-telemetry-drop-derived-columns`) — the derivation now lives in `internal/analytics`,
  computed from this table's surviving `odometer_km` / `battery_level_pct` / `captured_date`
  columns via `Reader.SnapshotPrecedingDay` (see below); this module never read the dropped
  columns back, and their only consumer was another module.
- `poll_attempts` — **unaffected, still append-only/immutable** (design D4): one row per
  (vehicle, run): `account_id`, `tesla_id`, `attempted_at`, `outcome` (`success`|`failure`),
  `reason` (`ok`|`asleep-timeout`|`unauthorized`|`api-error`). Doubles as future availability /
  sleep-behavior data — a daily collapse would destroy that signal, so this table is explicitly
  out of scope for the dedupe change. **Gains two columns, migration `20260823000002`**
  (`RM29-app-add-process-vehicle-data`): `run_id UUID` (nullable, permanently unbackfilled for
  legacy rows) and `triggered_by TEXT NOT NULL DEFAULT 'scheduler'`. **This SUPERSEDES roadmap
  D2**, which had planned to move this table to a new `internal/app`-owned `process_runs` —
  that plan was reversed during this change's design (design.md D1): `poll_attempts` never held
  anything Tesla reported to begin with (every existing column is already "our own fact about our
  own attempt"), so adding `triggered_by` extends the table rather than blurring a boundary it
  never had. `internal/app` ends up owning no schema at all. Grain is unchanged (one row per
  vehicle per run, design.md D2); no CHECK, no index (nothing reads either column in this tier).
- `supercharger_history` — **renamed from `supercharger_sessions`** by
  `RM39-telemetry-move-to-own-schema` tier 4 (MAG-31, migration `20260903000001`,
  roadmap D5a/D5c) in the same migration that moved it into the `telemetry` schema —
  every catalog object that named the old table (7 constraints + 2 standalone indexes,
  all `supercharger_sessions_*` / `idx_supercharger_sessions_*`) was renamed alongside
  it, and the hand-written Go domain type followed: `SuperchargerSession` →
  `SuperchargerHistory` (`telemetry.go`). One row per Tesla `session_id` (UPSERT, not
  append-only: billing state — `is_paid`, invoice status — mutates post-session,
  migration `20260716000001`). Extended by `RM27-telemetry-add-supercharger-battery-pct`
  (MAG-14, migration `20260815000001`) with three nullable columns (originally five;
  `RM41-telemetry-drop-estimate-columns`, 2026-09-03, dropped the frozen
  verification-snapshot pair), all excluded
  from `UpsertSuperchargerHistory`'s (the sqlc query, renamed from `UpsertSuperchargerSession`
  by the same tier-4 change) `INSERT`/`ON CONFLICT DO UPDATE SET` (see "Battery-%
  verification columns" below for the full convention):
  - `start_battery_pct SMALLINT CHECK (0..100)`, `end_battery_pct SMALLINT CHECK (0..100)` —
    human-owned verification/override trio.
  - `battery_pct_source TEXT CHECK (IN ('user_verified', 'polled'))` — why the trio is set;
    NULL when no override exists.
- `charge_gaps` (the flagged-vehicle-day ledger, formerly owned here as
  `RM28-telemetry-add-charge-gap-storage`, MAG-15) **moved to `internal/analytics`** by
  `RM29-analytics-own-charge-gaps` (MAG-26 tier 5), table, migration, and `GapWriter` port
  together — this module never read it back after writing it, and its only consumer was
  another module.
- `poll_runs` — one row per `run_id` (PRIMARY KEY, no surrogate `id` — `RunContext.RunID`
  is already the row's natural, immutable identity, design D1), written **exactly once**
  per `app.ProcessVehicleData` invocation by `RunWriter.RecordRun`, including on the
  step-1 whole-cycle-failure short-circuit (a failed run still leaves an all-zero-counts
  trace, design D3). A plain single `INSERT`, never an upsert: a duplicate `run_id` is a
  caller bug and fails loudly on the PRIMARY KEY (design D11). No FK to/from
  `poll_attempts` — the two tables correlate only through the application-known `run_id`
  value, since a `poll_runs` row is written after every `poll_attempts` row for that run
  already exists (design D2). Columns reproduce the poller's per-cycle log line without a
  join: `triggered_by`, `started_at`/`finished_at`/`duration_seconds` (all `NOT NULL` —
  design D3), the account-grain counts (`accounts_attempted`/`_succeeded`/`_failed`), the
  vehicle-grain counts and per-reason failure counts, `tesla_api_calls` (every Tesla Fleet
  API request this run made, counted regardless of success/failure by the module's
  internal `callCounter` decorator, design D9/D10), and the three
  charging/config-capture counts already on `CycleReport`. **No read port yet** — no
  `Reader`-style method, no gateway page (design D5/backlog); the only way to observe a
  row today is direct SQL or a future gateway read surface. No index beyond the PK's
  automatic B-tree (design D5: nothing reads this table in this tier, and the future
  "list recent runs" read stays a cheap sequential scan at this table's write volume for
  years). Schema/rationale: `openspec/changes/RM36-telemetry-add-poll-runs/design.md`
  (MAG-35) — moves under `openspec/changes/archive/` once this change is archived.

`account_id`/`tesla_id` are plain columns (no cross-module FK, D2 of the change design). `pgtype`
never leaves the module — convert to/from plain domain types at the DB→domain mapping boundary
(`ai/go-conventions.md` §persistence), mirroring the account module.

## DTO / units conventions

Platform-wide unit rule (normative): `openspec/specs/unit-of-measure/spec.md` and
`ai/go-conventions.md` §Coding Rules / §Persistence — display units, unit-suffixed names,
conversion once on write, never on read. This section records `telemetry`'s own application of
that rule: `vehicle_snapshots` is the table the platform rule was generalised from.

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
- **The five derived-consumption figures no longer live here.** `DistanceTraveledKmCalc`,
  `BatteryUsedPctCalc`, `KmPerPctCalc`, `EstimatedRangeKmCalc`, and `DaysSpannedCalc` were dropped
  from `Snapshot` (and their columns from `vehicle_snapshots`) by `RM29-telemetry-drop-derived-columns`
  (migration `20260822000001`). The derivation moved to `internal/analytics` (`consumption.go`),
  which computes the same figures from this table's surviving `OdometerKm` / `BatteryLevelPct` /
  `CapturedDate` fields, fetching the exact predecessor via the new `Reader.SnapshotPrecedingDay`
  (below) instead of reading a value this module used to precompute. This module never read the
  dropped columns back after writing them — their only consumer was `internal/analytics`, which
  is exactly the boundary blur `RM29-modular-monolith-boundaries` exists to fix.

### Battery-% verification columns (`supercharger_history`, renamed from `supercharger_sessions` by RM39 tier 4) — introduced by `RM27-telemetry-add-supercharger-battery-pct` (MAG-14)

`SuperchargerHistory` gains three pointer fields (`StartBatteryPct *int`, `EndBatteryPct *int`,
`BatteryPctSource *string`), mapped by
`rowToSuperchargerHistory` (`mapping.go`) via the new `pgNullableInt16AsInt` helper (first
`SMALLINT`/`pgtype.Int2` column in this module; reused for both `SMALLINT` fields) and the
existing `pgNullableText` helper for `BatteryPctSource`.

> **SCOPE NOTE (2026-08-15) — there is no estimator, and there will not be one under RM27.**
> RM27 originally planned two further tiers: a taper-curve SOC estimator in `internal/analytics`
> and a gateway page rendering it. **Both were descoped by the owner**; RM27 shipped these three
> columns (plus a since-dropped reserved pair — see the next bullet) and nothing else. Wherever the text below says an estimate is computed "on read",
> read that as *not implemented* — the platform computes no SOC estimate anywhere. The columns
> remain a purely human-owned channel with no writer yet. Deferred work: backlog entry 11.

- **Trio NULL convention:** `StartBatteryPct`/`EndBatteryPct`/`BatteryPctSource` all `nil` means
  "no value has been recorded". A non-nil trio means a human verified/overrode the value;
  `BatteryPctSource` records why (`"user_verified"` or `"polled"`). Nothing fills a NULL trio
  today — no fallback, no estimate.
- **Never auto-written (R3/R7):** all three columns are excluded from
  `UpsertSuperchargerHistory`'s `INSERT` column list and its `ON CONFLICT DO UPDATE SET` clause
  — deliberately, not an oversight (design D3). The nightly poller re-upserts every session
  because Tesla billing state (`is_paid`, invoices) mutates post-session; if any of these three
  were bound as a query parameter, a human-verified value would be silently overwritten on the
  next nightly re-upsert. **No writer for any of the three columns exists anywhere in this
  repository as of this change** — the future verification UI's Writer port is out of scope
  here (backlog entry 11).
- **`BatteryPctSource` never stores `"estimated"`** (R4/R6): `BatteryPctSource` only ever
  describes why a **verified** trio exists. With the estimator descoped nothing produces an
  estimate at all; and were one ever added, the distinction would still be carried structurally
  (by column presence), never by a stored label, since a persisted estimate goes stale the
  moment the model behind it changes — exactly what R6 forbids.
- **The frozen verification-snapshot pair no longer exists.** RM27 (2026-08-15)
  kept it reserved, unwritten, so a future SOC estimator could land without a
  migration. That reservation is now obsolete: the estimator MAG-36 eventually
  shipped (`derivedStartBatteryPct`, `internal/charging/capacity.go`,
  2026-09-01) writes the real `start_battery_pct` column instead of a frozen
  snapshot column — so `RM41-telemetry-drop-estimate-columns` (2026-09-03)
  dropped both columns from `telemetry.supercharger_history` along with the
  two `SuperchargerHistory` fields and the RM27-D6 comment block that
  described them. Full history of the original design:
  `openspec/changes/archive/2026-08-15-RM27-telemetry-add-supercharger-battery-pct/design.md`
  D6 (superseded).

## Testing notes

- **Query/call logging (`RM44-telemetry-add-query-logging`, ticket MAG-48).**
  `query_log.go` holds four decorators (`loggingStore`, `loggingReader`,
  `loggingSuperchargerHistoryReader`, `loggingRunWriter`) instrumenting this
  module's own read/write seams, prefixing every line `telemetry query:`.
  `call_counter.go` was extended with one `log.Printf` per Fleet API method,
  prefixed `fleet api:`. Both are tested purely offline in `query_log_test.go`
  and `call_counter_test.go` (fake `inner`/`minimalFakeTesla`, `log.SetOutput`
  redirected to a buffer, restored via `t.Cleanup` — no `DATABASE_URL`, no
  network). The guarantee that a credential or `raw_data` payload never
  reaches a log line is enforced structurally (see design.md D3/D5) and
  proven by test: `TestCallCounter_NeverLogsCredentials`
  (`call_counter_test.go`) and `TestQueryLog_NeverLogsRawDataContent`
  (`query_log_test.go`). Once this change archives, its design.md moves to
  `openspec/changes/archive/RM44-telemetry-add-query-logging/design.md`.
- Collection-service logic is tested **offline** with fake `account.Service` and
  `tesla.VehicleService` implementations — per-vehicle isolation, reason mapping, one-retry, wake
  timeout, multi-account. NO test may make a live Tesla API call or wake a car (the calls are paid).
- The wake helper's online-vs-timeout outcomes are unit-tested pure (fake clock / short timeout),
  now in `wake_test.go`. `report_test.go` covers `formatFailures` (`LogCycle`'s formatter).
  **Schedule-time math (`nextRun`) and `Scheduler`'s four tests no longer live in this module** —
  `scheduler.go`/`scheduler_test.go` were removed by `RM29-app-add-process-vehicle-data` (design.md
  D4): the `Scheduler` type relocated to `internal/app`, and its tests (`TestNextRun`,
  `TestScheduler_ShutsDownWithoutRunningWhenCancelled`, `TestScheduler_NilLocationDefaultsToLocal`,
  `TestScheduler_RunsAndLogsOneCycle`) moved with it, unchanged, into
  `internal/app/scheduler_test.go`. This is a relocation, not a coverage drop — look there, not
  here, for that coverage.
- Store tests use the shared `internal/testdb` helper (see `testdb_test.go`). When
  `DATABASE_URL` is set AND reachable, that managed Postgres is used; otherwise
  `TestMain` auto-provisions a disposable `postgres:16-alpine` container via
  testcontainers-go and applies the goose migrations embedded under `db/migrations/`.
  `go test ./...` (and `make check`) are green with zero manual DB setup as long as
  Docker is running locally. Verify `sentry_mode` nil↔NULL and append-only behavior
  there.
- **When neither is available, the DB-backed tests SKIP — they do not fail** (added by
  `telemetry-add-derived-consumption-columns`). `TestMain` logs
  `no Postgres available, SKIPPING all DB-backed tests` and still runs the suite, and
  `newTestStore` calls `t.Skip`. This keeps the package's offline tests
  (`clock.CalendarDay`, `snapshotFrom`, scheduler math) runnable
  and `make check` passable on a machine with no Docker daemon, where previously a failed
  provision called `log.Fatalf` and killed the whole test binary before any test ran.
- **Only "no Postgres at all" skips — a broken migration still fails LOUDLY.** `TestMain`
  skips solely on `errors.Is(err, testdb.ErrUnavailable)`, the sentinel `internal/testdb`
  wraps around the "no Docker daemon to start a container" case. Every other `Provision`
  failure — above all `goose up` failing to apply a migration — is still `log.Fatalf`.
  Keep that distinction if you touch this: skipping on a migration failure would let a
  broken schema pass `make check` in silence. (Residual edge case: if `DATABASE_URL` is
  set but unusable AND there is no Docker, the run degrades to a skip — `testdb` logs
  `DATABASE_URL not usable` first, so check the log when a run skips unexpectedly.)
  **Consequence to keep in mind: a green suite does NOT by itself prove the DB tests ran.**
  To actually exercise them, start Docker and run `make test` (disposable container —
  never the real database), or run one file's worth directly, e.g.
  `env -u DATABASE_URL go test ./internal/telemetry/ -run TestStore_ -v`, and confirm the
  output says `PASS` rather than `SKIP`. `make test-with-db` instead runs against whatever
  `DATABASE_URL` points at and applies migrations to it — only use it against a throwaway
  or CI Postgres, never the owner's live database.
