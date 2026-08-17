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
  `NewReader(pool *pgxpool.Pool) Reader` is the constructor. The gateway (tier 5,
  `gateway-read-stored-vehicles`) depends on this interface, never on `telemetrydb` directly.

- `GapWriter` — one write method, `ReconcileWindow(ctx context.Context, accountID uuid.UUID,
  teslaID int64, start, end time.Time, flagged []ChargeGap) error`: makes `charge_gaps` agree
  with `flagged` for exactly the vehicle-day range `[start, end]` inclusive. Every day present
  in `flagged` is upserted (inserted, or refreshed in place if `MissingChargingType`/`VIN`
  changed since the last run — the `UNIQUE (account_id, tesla_id, gap_date)` constraint is the
  idempotency mechanism, not application-level dedup); every existing row for
  `(accountID, teslaID)` in `[start, end]` with no matching entry in `flagged` is **deleted**.
  `flagged` may be empty (every previously-flagged day resolved — every existing row in the
  window is deleted, none re-inserted). Every element of `flagged` MUST carry the SAME
  `accountID`/`teslaID` as the call's own arguments AND a `Date` within `[start, end]`; a
  violation returns an error and writes **nothing** (validated in a loop BEFORE any
  transaction opens — see "GapWriter's upsert-and-delete lifecycle" below). Runs inside a
  single DB transaction: either every upsert/delete succeeds, or the call has no effect.
  `internal/battery` is the only intended caller (via `cmd/poller`) — `internal/telemetry`
  never calls `battery`, preserving the one-way dependency the platform's graph already
  assumes. `NewGapWriter(pool *pgxpool.Pool) GapWriter` is the constructor; implementation in
  `gap_writer.go`. Added by `RM28-telemetry-add-charge-gap-storage` (MAG-15).

- `SuperchargerReader` — exposes `supercharger_sessions` for read-only consumption, a
  separate port from `Reader` (snapshot-centric):
  - `SuperchargerSessionsByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]SuperchargerSession, error)`:
    all sessions for an account, ordered `charge_start_date_time DESC`, limited to `limit`
    rows (`0` = server default `math.MaxInt32`). Non-nil empty slice when none exist.
  - `SuperchargerSessionsByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]SuperchargerSession, error)`:
    same shape, scoped to one vehicle within the account.
  - `SuperchargerSessionsByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]SuperchargerSession, error)`:
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
    trade-off and fallback). Reuses the existing `rowToSuperchargerSession` mapper — no new
    field, no new mapper. Non-nil empty slice when none exist (parity with the other two
    methods). `account_id` AND `tesla_id` filter provides defense-in-depth tenant isolation.
    Added by `RM28-telemetry-add-charge-gap-storage` (MAG-15, roadmap D9/D12).
  `NewSuperchargerReader(pool *pgxpool.Pool) SuperchargerReader` is the constructor;
  implementation in `reader.go`. Callers MUST NOT import `telemetrydb` (design DBS6).

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

Owns four tables in the module-scoped `internal/telemetry/db` (goose migrations are the
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
  fresh insert, explicitly set to `now()` on a same-day conflict-update (design D5). Five more
  nullable derived-consumption columns added by migration `20260814000001`:
  `distance_traveled_km_calc DOUBLE PRECISION`, `battery_used_pct_calc INTEGER`,
  `km_per_pct_calc DOUBLE PRECISION`, `estimated_range_km_calc DOUBLE PRECISION`,
  `days_spanned_calc INTEGER` — see below.
- `poll_attempts` — **unaffected, still append-only/immutable** (design D4): one row per
  (vehicle, run): `account_id`, `tesla_id`, `attempted_at`, `outcome` (`success`|`failure`),
  `reason` (`ok`|`asleep-timeout`|`unauthorized`|`api-error`). Doubles as future availability /
  sleep-behavior data — a daily collapse would destroy that signal, so this table is explicitly
  out of scope for the dedupe change.
- `supercharger_sessions` — one row per Tesla `session_id` (UPSERT, not append-only: billing
  state — `is_paid`, invoice status — mutates post-session, migration `20260716000001`).
  Extended by `RM27-telemetry-add-supercharger-battery-pct` (MAG-14, migration
  `20260815000001`) with five new nullable columns, all excluded from
  `UpsertSuperchargerSession`'s `INSERT`/`ON CONFLICT DO UPDATE SET` (see "Battery-%
  verification columns" below for the full convention):
  - `start_battery_pct SMALLINT CHECK (0..100)`, `end_battery_pct SMALLINT CHECK (0..100)` —
    human-owned verification/override trio.
  - `battery_pct_source TEXT CHECK (IN ('user_verified', 'polled'))` — why the trio is set;
    NULL when no override exists.
  - `start_battery_pct_est SMALLINT CHECK (0..100)`, `end_battery_pct_est SMALLINT CHECK
    (0..100)` — frozen, write-once verification-time snapshot pair (design D6).
- `charge_gaps` — one row per flagged vehicle-day whose battery math does not add up
  (migration `20260815000002`, `RM28-telemetry-add-charge-gap-storage`, MAG-15). Written
  through the `GapWriter` port; `internal/battery` is the only intended caller (it derives
  each day's consumption and detects the gap — `telemetry` never computes one itself, and
  never calls `battery`). Columns: `id UUID PRIMARY KEY`, `account_id UUID NOT NULL`,
  `tesla_id BIGINT NOT NULL` (**always resolved, NOT NULL** — unlike
  `supercharger_sessions.tesla_id`, since `internal/battery` filters out any
  vehicle/session it cannot attribute to a currently-registered vehicle before gap
  detection ever runs), `vin TEXT NOT NULL`, `gap_date DATE NOT NULL` (the flagged calendar
  day, plain `DATE` — no time-of-day component), `missing_charging_type TEXT NOT NULL CHECK
  (IN ('MANUAL', 'SUPERCHARGER'))` (which charge source is suspected missing — `SUPERCHARGER`
  when a Supercharger session exists that day with NULL start/end battery percentages,
  `MANUAL` otherwise), `created_at TIMESTAMPTZ NOT NULL DEFAULT now()` (when FIRST flagged —
  preserved across every re-upsert of the same still-flagged day), `updated_at TIMESTAMPTZ
  NOT NULL DEFAULT now()` (refreshed to `now()` on every re-confirmation). `UNIQUE
  (account_id, tesla_id, gap_date)` constraint (`charge_gaps_account_tesla_date_unique`) is
  both the write-idempotency mechanism (`ON CONFLICT DO UPDATE`) and the index that serves
  `GapWriter`'s own read-before-diff query — no separate index needed for that path. A second
  index, `idx_charge_gaps_account (account_id, gap_date DESC)`, serves the future
  account-wide notification read pattern (no `tesla_id` predicate) — out of scope this
  change, no read port exists for it yet. **No FK** on `account_id`/`tesla_id` (same
  no-cross-module-FK precedent as `manual_charge_entries`/`supercharger_sessions` —
  referential integrity is upheld by flow, not a DB constraint, `ai/architecture.md` §2).
  **No `raw_data` JSONB** — this table stores a Go-computed conclusion (`internal/battery`'s
  derivation), not an external API response, so the mandatory-`raw_data` rule
  (`ai/go-conventions.md` §persistence) does not apply here (same precedent as
  `manual_charge_entries`).

### `GapWriter`'s upsert-and-delete lifecycle — **no `resolved_at`, ever**

`charge_gaps` has **no soft-delete / `resolved_at` column** — `ReconcileWindow` `UPSERT`s
every day that still flags and **`DELETE`s** every previously-stored day, within the window
it just recomputed, that no longer flags (design D7b). Fixing a charge entry clears the row
on the very next nightly run with no extra wiring. This table is a **live worklist** ("what
is outstanding right now"), not an audit trail of resolved gaps.

**If you are the one adding the future notification feature or any other consumer of this
table: do NOT "fix" this into a soft-delete/`resolved_at` shape.** A soft-deleted row would
need its own cleanup story (when does a resolved row actually get purged?) that this design
deliberately avoids by making resolution a plain `DELETE` — the row's mere existence already
means "outstanding," so a consumer needs no `WHERE resolved_at IS NULL` filter and no purge
job. If a history of resolved gaps is ever needed, that is a **new, separate** table (e.g. an
append-only `charge_gap_history`), not a mutation of `charge_gaps`'s own delete-on-resolve
contract — see `RM28-telemetry-add-charge-gap-storage`'s `design.md` "Migration Plan" /
D-Table2 for the rejected `resolved_at` alternative and its reasoning (once archived, that
file moves under `openspec/changes/archive/`).

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
- **Five derived-consumption columns** (`DistanceTraveledKmCalc *float64`,
  `BatteryUsedPctCalc *int`, `KmPerPctCalc *float64`, `EstimatedRangeKmCalc *float64`,
  `DaysSpannedCalc *int`) are computed in Go, at write time, by the pure function
  `deriveConsumption(prev, cur)` (`service.go`) — comparing the incoming snapshot against its
  predecessor for the same `(account_id, tesla_id)` — and are **never** derived on read. NULL
  convention: all five are NULL when no predecessor exists (the vehicle's first-ever snapshot);
  `KmPerPctCalc`/`EstimatedRangeKmCalc` are additionally NULL whenever the battery-used divisor
  (`BatteryUsedPctCalc`) is zero or negative (charging/parked day) — a stored value is always a
  truthful reading, never a placeholder, per the module's D12/DSA3 NULL-vs-zero convention above.
  `BatteryUsedPctCalc` itself may be a truthful negative (net charge overnight). Migration
  `20260814000001` backfilled every pre-existing row in the same schema change via a one-time
  `LAG()` window pass — no row is permanently stuck NULL except each vehicle's earliest row.
  Introduced by `telemetry-add-derived-consumption-columns` (MAG-10).

### Battery-% verification columns (`supercharger_sessions`) — introduced by `RM27-telemetry-add-supercharger-battery-pct` (MAG-14)

`SuperchargerSession` gains five pointer fields (`StartBatteryPct *int`, `EndBatteryPct *int`,
`BatteryPctSource *string`, `StartBatteryPctEst *int`, `EndBatteryPctEst *int`), mapped by
`rowToSuperchargerSession` (`mapping.go`) via the new `pgNullableInt16AsInt` helper (first
`SMALLINT`/`pgtype.Int2` column in this module; reused for all four `SMALLINT` fields) and the
existing `pgNullableText` helper for `BatteryPctSource`.

> **SCOPE NOTE (2026-08-15) — there is no estimator, and there will not be one under RM27.**
> RM27 originally planned two further tiers: a taper-curve SOC estimator in `internal/battery`
> and a gateway page rendering it. **Both were descoped by the owner**; RM27 ships these five
> columns and nothing else. Wherever the text below says an estimate is computed "on read",
> read that as *not implemented* — the platform computes no SOC estimate anywhere. The columns
> remain a purely human-owned channel with no writer yet. Deferred work: backlog entry 11.

- **Trio NULL convention:** `StartBatteryPct`/`EndBatteryPct`/`BatteryPctSource` all `nil` means
  "no value has been recorded". A non-nil trio means a human verified/overrode the value;
  `BatteryPctSource` records why (`"user_verified"` or `"polled"`). Nothing fills a NULL trio
  today — no fallback, no estimate.
- **Never auto-written (R3/R7):** all five columns are excluded from
  `UpsertSuperchargerSession`'s `INSERT` column list and its `ON CONFLICT DO UPDATE SET` clause
  — deliberately, not an oversight (design D3). The nightly poller re-upserts every session
  because Tesla billing state (`is_paid`, invoices) mutates post-session; if any of these five
  were bound as a query parameter, a human-verified value would be silently overwritten on the
  next nightly re-upsert. **No writer for any of the five columns exists anywhere in this
  repository as of this change** — the future verification UI's Writer port is out of scope
  here (backlog entry 11).
- **`BatteryPctSource` never stores `"estimated"`** (R4/R6): `BatteryPctSource` only ever
  describes why a **verified** trio exists. With the estimator descoped nothing produces an
  estimate at all; and were one ever added, the distinction would still be carried structurally
  (by column presence), never by a stored label, since a persisted estimate goes stale the
  moment the model behind it changes — exactly what R6 forbids.
- **`StartBatteryPctEst`/`EndBatteryPctEst` are RESERVED and, today, always NULL.** With the
  estimator descoped there is nothing to snapshot, so no code path writes them. They were kept
  rather than dropped (owner's call, 2026-08-15) so that a future estimator can land without a
  migration. **If you are the one adding that estimator, read this before touching either
  field:** they are a FROZEN, write-once verification snapshot — NOT a cache, NOT a
  nightly-refreshed pair (design D6). They must be written **exactly once**, in the same write
  as the trio (by a future verification UI), capturing what the estimator showed **at that
  moment** ("model said 82, human said 79" — a permanent drift-log entry), and **never updated
  again**, including by a later, improved model: staleness relative to a newer model is the
  correct, intended behavior for a dated observation, not a bug. They must **never be read back
  into a live estimate computation** — reading them "to save a computation" defeats the entire
  point of the drift log. They carry the identical R3 write-exclusion as the trio (never in
  `UpsertSuperchargerSession`). Full rationale — including why a nightly-refreshed `_est` pair
  (the shape this is NOT) has no legal writer under this project's module-ownership rule — in
  `openspec/changes/archive/2026-08-15-RM27-telemetry-add-supercharger-battery-pct/design.md` D6.

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
- **When neither is available, the DB-backed tests SKIP — they do not fail** (added by
  `telemetry-add-derived-consumption-columns`). `TestMain` logs
  `no Postgres available, SKIPPING all DB-backed tests` and still runs the suite, and
  `newTestStore` calls `t.Skip`. This keeps the package's offline tests
  (`deriveConsumption`, `dayStart`, `dateOnly`, `snapshotFrom`, scheduler math) runnable
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
