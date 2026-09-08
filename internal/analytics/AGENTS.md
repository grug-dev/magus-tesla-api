# Analytics Sub-Agent

Agent-Name: analytics

Per-module instructions for `internal/analytics/` — merged with the global rules
(`CLAUDE.md`, `ai/*.md`) by any assistant working here (see `ai/agentic-workflow.md`).

## Doc-Pack (module)

Extends the project base Doc-Pack (`CLAUDE.md` → "Pipeline config") — never replaces it.
A dispatched worker/reviewer reads: base pack + this list + this file, before any write.

(No module-specific docs beyond the base pack today. This module owns its own
database (`internal/analytics/db/` — sqlc + goose migrations, see "Data ownership"
below) but still has no HTTP surface and no external SDK of its own. If a future
metric needs an external doc, e.g. a battery-chemistry reference, add it here.)

## Responsibility

`internal/analytics/` is the platform's first **derived-metrics** module. It owns
analytics computed FROM other modules' stored data, not the data itself. Its first
(and currently only) metric is rolling energy-per-kilometre (Wh/km) over a fixed
window, derived from `internal/telemetry/`'s snapshot history plus the two
charging-cost sources the platform stores (`charging.SuperchargerSessionAnalyticsReader`
and `charging.Reader`), with a pack-capacity correction sourced from a small
in-package reference table keyed on the vehicle's `car_type`
(`internal/account.Vehicle.CarType`).

It does the derivation; it does not render it — the gateway consumes this module's
`Reader` port and formats the value for display (`apex-dashboard-efficiency-tile`,
a separate follow-on change; not yet wired as of `battery-add-efficiency-metric`).

Full design rationale (why hybrid energy, why consistent-pair SoC selection, why the
capacity table is model-coarse, why `Approximate` exists instead of refusing to
answer): `openspec/changes/battery-add-efficiency-metric/design.md`.

Under the RM29 roadmap (tier 1 of 8), this module owns what the application
calculates. Tier 3 gave it a database of its own (see "Data ownership" below).
Tier 4 (`RM29-telemetry-drop-derived-columns`) moved the five per-day
consumption figures' derivation itself into this module — they were
previously computed in `internal/telemetry` and copied here verbatim; see
the `Recalculator` entries under "Public interface (the port)" below.

## Public interface (the port)

The module's mandatory contract is a Go interface (`ai/go-conventions.md` —
interface-first):

- `Reader` — `RecentEfficiency(ctx context.Context, accountID uuid.UUID, teslaID int64) (Efficiency, bool, error)`:
  returns the rolling Wh/km efficiency for one vehicle over the window `NewReader` was
  constructed with. Returns `ok=false` (no error) when there is not enough data to
  compute a meaningful value — never a fabricated number (`design.md` "D-ok"). Returns
  `Efficiency.Approximate=true` (still `ok=true`) when the vehicle's pack capacity is
  unknown — the SoC-drift correction term is dropped, not the whole computation
  (`design.md` D1b).
- `Reader` — `ConsumedByDay(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]DayConsumption, error)`:
  returns the corrected per-day battery-consumed percentage (D13) over `[start, end]`
  (whole calendar days, UTC-midnight-represented, `end` inclusive). Calendar-day
  bucketing uses the poller's configured zone (`telemetry.Config.Location`, roadmap
  D6/D18), never UTC — the day is each row's own `telemetry.Snapshot.CapturedDate`
  minus one day (`design.md` D-B7/D-B12); `telemetry.Snapshot.EffectiveDate` (a UTC-derived
  field) is never read by this module. The result is **sparse**: one entry per day with a
  computable value, no entry at all for a day without one — absence IS the "no data"
  signal (`design.md` D-B2), mirroring `internal/gateway/handlers/history.go`'s existing
  `buildOdometerChart`/`buildBatteryChart` convention. Recomputed on every call, no cache
  (`design.md` D2).
- `Efficiency` — the domain result: `WhPerKm` (raw `float64`, unrounded — the gateway
  formats it), `FromKm`/`ToKm` (read directly from `telemetry.Snapshot.OdometerKm` —
  already km-native at capture time, `telemetry-store-display-units` design D1/D3; this
  module performs no unit conversion of its own, per
  `battery-adopt-snapshot-unit-fields`), `BatteryDeltaPct` (net SoC over the window,
  `start − end`; negative means net charge), `Approximate`.
- `DayConsumption` — one calendar day's corrected consumption result: `Date` (the row's
  own effective day, never re-attributed — `design.md` D-B3), `ConsumedPct` (D13 formula,
  raw and unrounded, may be negative or zero), `DistanceKm`, `Flagged` (D5/D5a gap
  detection), `MissingChargingType` (this module's own `MissingChargingType` type, valid only when
  `Flagged`, D7a), `DaysSpanned` (the row's own `DaysSpannedCalc`, >1 signals a multi-day
  span, D8).
- `Reader` — `OdometerDeltaByDay(ctx, accountID, teslaID, start, end) ([]DayDistance, error)`:
  per-day distance travelled over `[start, end]`, read from the precomputed
  `vehicle_metrics` rows. Sparse on the same principle as `ConsumedByDay` — a day whose
  `distance_traveled_km_calc` is NULL (a predecessor-less day, D9) yields no entry, via
  an `IS NOT NULL` filter in the SQL rather than a zero comparison. Added by
  `RM29-analytics-add-vehicle-metrics` so the gateway stops deriving distance from
  snapshots itself (roadmap D5).
- `Reader` — `BatteryLevelByDay(ctx, accountID, teslaID, start, end) ([]DayBattery,
  error)`: per-day battery-level percentage and estimated range over `[start, end]`,
  read from the precomputed `vehicle_metrics` rows, same shape and precomputed
  contract as `ConsumedByDay`/`OdometerDeltaByDay`. `DayBattery.Date` is a FINAL
  bucket key — the row's own already-effective `metric_date`, never re-projected
  through the gateway's `effectiveDayUTC` (design.md D4). Sparse for the usual
  reason: a day with no `vehicle_metrics` row at all yields no entry. **Unlike its
  two siblings, this query has NO `IS NOT NULL` filter** — `battery_level_pct`/
  `battery_range_km` are `NOT NULL` raw observations with no predecessor
  requirement, so a predecessor-less day (that `ConsumedByDay`/`OdometerDeltaByDay`
  exclude) still gets a `DayBattery` entry here. Added by
  `RM40-analytics-add-battery-level-read` (MAG-41 tier 1) so the gateway's last
  production `telemetry.Reader` call site (the battery-history chart) can retarget
  here instead of `internal/telemetry` (tier 2, a separate change). Full rationale:
  `openspec/changes/RM40-analytics-add-battery-level-read/design.md` D1–D6/D-index.
- `Reader` — `LatestMetricsByAccount(ctx, accountID) ([]VehicleStatus, error)`: the latest
  precomputed `vehicle_metrics` row per vehicle for an account — the analytics-owned
  equivalent of `telemetry.Reader.LatestSnapshotsByAccount`, never `telemetry.Snapshot`
  itself (`ai/architecture.md` §6). "Latest" means the row with the greatest
  `metric_date` for that `(account_id, tesla_id)`, via a
  `DISTINCT ON (tesla_id) ... ORDER BY tesla_id, metric_date DESC` query
  (`LatestVehicleMetricsByAccount`). Empty account → an empty (non-nil) slice, nil error,
  same contract as `LatestSnapshotsByAccount`; order of the returned slice is
  unspecified. Added by `RM38-analytics-add-vehicle-status-columns` (MAG-12 tier 1) so
  a future gateway call site (tier 2) can read a vehicle's full dashboard status from
  this module instead of `internal/telemetry`. `VehicleStatus` is the returned domain
  type: `TeslaID`, `BatteryLevelPct`, `BatteryRangeKm`, `OdometerKm` (never nil — the
  three pre-existing raw observations) plus `InsideTempC`, `OutsideTempC`, `Locked`,
  `SentryMode`, `CarVersion`, `ChargingState`, `ChargeLimitSocPct`, `CapturedAt` and
  `MaxRangeChargeCounter`, `TpmsPressureFLPSI`, `TpmsPressureFRPSI`, `TpmsPressureRLPSI`,
  `TpmsPressureRRPSI`, `DistanceTraveledKmCalc` and `ConsumedPct` (all pointer-typed —
  nil means "no value", never a fabricated default; see "Data ownership" below for what
  nil means on each).
  `MaxRangeChargeCounter` is the vehicle's LIFETIME count of charges to its true 100%
  Maximum-Battery-Range limit — monotonic across rows, never a per-day delta — added to
  the projection for the dashboard's "100% Charges" tile (MAG-47). A reported `0` is a
  real reading ("never charged to max range") and is never collapsed to nil. Full rationale, the coverage check against every
  field a future gateway call site needs, and the rejected "return `telemetry.Snapshot`
  directly" alternative: `openspec/changes/RM38-analytics-add-vehicle-status-columns/design.md`
  D4/D5/D6.
  `TpmsPressureFLPSI`/`FR`/`RL`/`RR` are the four tire-pressure raw observations
  (front-left/front-right/rear-left/rear-right, already PSI), added by
  `RM50-analytics-add-tire-pressure-columns`. `DistanceTraveledKmCalc`/`ConsumedPct` are
  the same two `_calc` columns `DayDistance`/`DayConsumption` already expose elsewhere in
  this port — nil on a predecessor-less day, exactly as documented there — gaining a
  second consumer on this projection (same table, same query, no new read). Full
  rationale: `openspec/changes/RM50-analytics-add-tire-pressure-columns/design.md`
  D1–D3.
- `Recalculator` — `Recalculate(ctx, accountID, teslaID, start, end) error`: recomputes
  and UPSERTs the `vehicle_metrics` rows for `[start, end]` from the three source ports.
  Idempotent by design — re-running over the same unchanged sources produces the same
  rows (`design.md` D4). It **computes** the five per-day consumption figures itself
  (`consumption.go`'s `deriveConsumption`, moved in from `internal/telemetry` by
  `RM29-telemetry-drop-derived-columns`) rather than copying them off
  `telemetry.Snapshot` — nothing on `Snapshot` carries them any more. For the fetched
  window's first row it looks up the exact predecessor via
  `telemetry.Reader.SnapshotPrecedingDay(ctx, accountID, teslaID, day)` (a real
  predecessor may sit outside the normal 1-day lookback after a multi-day capture gap),
  and per `design.md` D8b it widens **both** charge-source fetches (Supercharger
  sessions and manual entries) back to that predecessor's effective day whenever it
  precedes the normal lookback start — otherwise a gap day's charge events go unfetched
  and its consumed-percent comes out wrong (and can trigger a false missing-charge
  flag). A `SnapshotPrecedingDay` error aborts `Recalculate`; it is never degraded to
  "no predecessor".
- `Recalculator` — `Reconcile(ctx, accountID, teslaID) error`: the incremental pass. It
  reads the three per-source watermarks, widens by the commit-skew overlap, clamps the
  end to yesterday, and calls `Recalculate` for the affected span. **No prior watermark
  means epoch**, i.e. a full backfill of the vehicle's history (`design.md` D7). It has
  **no injectable clock** and clamps in UTC — a deliberate call (RM29 D13), so do not
  widen the port to make a test deterministic; anchor fixture dates clear of the
  boundary instead. **`internal/app`'s `ProcessVehicleData` is its only production
  caller** (it was `cmd/poller` until RM29 tier 7 moved that orchestration into the
  application layer), once per vehicle after each successful nightly cycle and *before* that cycle's charge-gap step —
  the gateway's post-write `Recalculate` covers only the days a manual charge write
  touches, so if this call is ever dropped, every vehicle's charts silently stop
  advancing.
- `DayDistance` — one calendar day's distance result, backing `OdometerDeltaByDay`.
- `DayBattery` — one calendar day's raw battery-level and range observation, backing
  `BatteryLevelByDay`. Both fields are always populated for any day with a
  `vehicle_metrics` row at all — no predecessor requirement (design.md D3).
- `NewRecalculator(pool *pgxpool.Pool, telemetryReader telemetry.Reader, supercharger charging.SuperchargerSessionAnalyticsReader, manual charging.Reader) Recalculator`
  is the constructor for the write side. `supercharger`'s type retyped from
  telemetry's own Supercharger-session port to `charging.SuperchargerSessionAnalyticsReader`
  by `RM31-analytics-read-sessions-from-charging` (MAG-19 tier 3) — the parameter name is
  unchanged (it describes the role, not the package).
- **`ConsumedByDay`'s signature is unchanged, but its implementation is not.** It used
  to fetch from the three ports and derive on every call; it now reads the precomputed
  `vehicle_metrics` rows. Callers see the same contract; the cost profile is completely
  different, which is the point (`Performance-Profile`: read-heavy).
- `DefaultWindow` — exported `time.Duration` constant, 30 days. Deployment code passes
  it (or a different duration) to `NewReader` at construction; the window is NOT a
  per-call argument to `RecentEfficiency` (`design.md` D3).
- `GapReconciliationWindow` — exported `time.Duration` constant, 30 days. The rolling
  window `internal/app` re-derives and reconciles against this module's own
  `charge_gaps` ledger every nightly run, via `ConsumedByDay` (`design.md` D4/D4a/D7b).
  Unlike `DefaultWindow`, this is not consumed by `NewReader` — `internal/app` passes it
  directly as the `[start, end]` window to `ConsumedByDay`.
- `NewReader(pool *pgxpool.Pool, telemetry telemetry.Reader, supercharger charging.SuperchargerSessionAnalyticsReader, manual charging.Reader, account vehicleLookup, window time.Duration) Reader`
  is the constructor — it gained the leading `*pgxpool.Pool` in
  `RM29-analytics-add-vehicle-metrics`, since `ConsumedByDay`/`OdometerDeltaByDay` now
  read this module's own tables. `supercharger`'s type retyped from
  telemetry's own Supercharger-session port to `charging.SuperchargerSessionAnalyticsReader`
  by `RM31-analytics-read-sessions-from-charging` (MAG-19 tier 3). `vehicleLookup` is an
  unexported narrow interface covering only
  `RegisteredVehicles` — any real `account.Service` satisfies it automatically
  (structural typing), no adapter needed at the call site. `ConsumedByDay` uses only
  three of the four wired dependencies (`telemetry`, `supercharger`, `manual`) and none
  of `account`/`window` — the signature is unchanged by `ConsumedByDay`'s addition
  (`design.md` D-B1).

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
  transaction opens — see "`GapWriter`'s upsert-and-delete lifecycle" under "Data ownership"
  below). Runs inside a single DB transaction: either every upsert/delete succeeds, or the
  call has no effect. This module both **computes** the flagged days (`ConsumedByDay`'s
  D5/D5a rule, `consumed.go`) AND **stores** the conclusion — the port and its storage now
  agree, which was never true while `charge_gaps` sat in `internal/telemetry`.
  `internal/app` is the only caller, wiring `Recalculator.Reconcile` then this port in
  sequence each nightly run (`design.md` D2 of `RM29-analytics-own-charge-gaps`). `NewGapWriter(pool
  *pgxpool.Pool) GapWriter` is the constructor; implementation in `gap_writer.go`. Moved here
  from `internal/telemetry` by `RM29-analytics-own-charge-gaps` (MAG-26 tier 5) — originally
  added by `RM28-telemetry-add-charge-gap-storage` (MAG-15).

No HTTP/JSON surface in this module (none required — `ai/architecture.md` §3).

## Allowed / forbidden imports

**May import (public ports only):**
- `internal/telemetry` — `telemetry.Reader` (`SnapshotsByVehicleSince`,
  `SnapshotPrecedingDay` — added by `RM29-telemetry-drop-derived-columns`, the exact-
  predecessor lookup `Recalculate` uses to derive the five consumption figures itself),
  and the domain type `telemetry.Snapshot`. As of
  `RM31-analytics-read-sessions-from-charging` (MAG-19 tier 3) this module no longer
  imports telemetry's own Supercharger-session port or domain type at all — that read
  moved to `internal/charging` (below).
- `internal/charging` — `charging.Reader` (`ListEntriesByVehicle`) and the domain type
  `charging.Entry`, plus, as of `RM31-analytics-read-sessions-from-charging`,
  `charging.SuperchargerSessionAnalyticsReader` (`ListSessionsByVehicleBetween`,
  `ListSessionsByVehicleUpdatedSince`, `ListSessionsByVehicle`) and the domain type
  `charging.Session` — this module's Supercharger-session source, replacing the
  telemetry-backed port/type this section named before that tier.
- `internal/account` — the narrow `RegisteredVehicles` method (satisfied by
  `account.Service`) and the domain type `account.Vehicle`.
- `internal/clock` — the platform's time primitives (`RM35-analytics-adopt-clock`).
  This module calls `clock.CalendarDay(t, time.UTC)` and `clock.Now()`. Note it still
  owns **no `*time.Location` of its own** (D-B12): every bucketing call passes
  `time.UTC` explicitly, because the values being bucketed are already-normalized
  days and the zone that decides day boundaries is applied upstream, in `telemetry`.
  Importing `clock` does not change that invariant.
- `github.com/google/uuid`, stdlib (`context`, `time`).

**Must NOT import:**
- `internal/telemetry/db` (`telemetrydb`), `internal/charging/db` (`chargingdb`),
  `internal/account/db` (`accountdb`) — another module's sqlc package is never
  importable. Cross-module data flows only through public ports
  (`ai/architecture.md` §2). Note this list no longer includes `pgxpool`/`pgx`: since
  `RM29-analytics-add-vehicle-metrics` this module owns a database of its own and takes
  a `*pgxpool.Pool` in `NewReader` and `NewRecalculator`. It reaches only its OWN
  tables through it.
- `internal/gateway`, `html/template`, `templ` — no HTML in a domain module
  (`ai/architecture.md` §2).
- `internal/tesla` — this module never talks to the Fleet API directly; every value it
  needs (snapshots, charging sessions, vehicle config) has already been captured and
  stored by another module before `analytics` ever runs.

## Data ownership

`internal/analytics/` owns **its own database**, added by
`RM29-analytics-add-vehicle-metrics` (MAG-26 tier 3). Before that change the answer
here was "None"; it is no longer.

- **Data lives in the `analytics` Postgres schema** (tables `vehicle_metrics`,
  `vehicle_metric_watermarks`, `charge_gaps`, moved there by
  `RM39-analytics-move-to-own-schema`, MAG-31 tier 2), managed from
  `internal/analytics/db/` (goose migrations + `query.sql`, sqlc-generated code). This
  is a namespacing change only — no stored data, constraint, or public interface
  behavior changed. `vehicle_metric_watermarks.source`'s stored string values
  (`'vehicle_snapshots'`, `'supercharger_sessions'`, `'manual_charge_entries'`) and its
  CHECK constraint name OTHER modules' tables by convention — they are data, not table
  references, and this schema move does not touch them. `'supercharger_sessions'` is a
  value reused from before RM31, when it named `telemetry`'s table — it now names
  `charging`'s table instead; see `openspec/changes/RM39-analytics-fix-watermark-vocabulary/design.md`
  §6 for the reused-string rationale.
- `internal/analytics/db/` — the module's sqlc package, `analyticsdb`, generated from
  `internal/analytics/db/query.sql` via the `analytics` entry in the root `sqlc.yaml`.
  **No other module may import `analyticsdb`** (`ai/architecture.md` §2), exactly as
  this module may not import `telemetrydb` or `chargingdb`.
- `internal/analytics/db/migrations/` — the module's own goose migrations, applied by
  the Makefile's `MIGRATIONS_DIRS` loop like every other module's.
- `vehicle_metrics` — one row per `(account_id, tesla_id, metric_date)` for every day
  the vehicle reported, holding both the raw observations and the five derived `_calc`
  columns. It is a **precomputed read model**: written by `Recalculator`, read by
  `Reader`. It is **dense** — a day with no computable predecessor still gets a row,
  with its `_calc` columns and `consumed_pct` NULL and `flagged` an explicit `false`
  (`design.md` D9/D10). That is why both `Reader` queries filter `IS NOT NULL` rather
  than trusting a zero.
  - **Eight more columns** (`locked`, `sentry_mode`, `car_version`, `inside_temp_c`,
    `outside_temp_c`, `charging_state`, `charge_limit_soc_pct`, `captured_at`), added by
    `RM38-analytics-add-vehicle-status-columns` (MAG-12 tier 1). All eight are copied
    verbatim from the day's own `telemetry.Snapshot` and — unlike the five `_calc`
    columns above — are always populated regardless of whether that day has a
    computable predecessor. **All eight are nullable, and no backfill was run**
    (roadmap D2): every row that existed before this migration keeps all eight NULL
    forever, self-healing only on that vehicle's next `Reconcile`. `sentry_mode`'s NULL
    is **ambiguous** — it can mean either "the vehicle did not report sentry" or
    "this row predates the migration" — where every other column's NULL means only the
    latter; do not attempt to disambiguate it here without first reading
    `openspec/changes/RM38-analytics-add-vehicle-status-columns/design.md` D2/D3/D8,
    which also documents the `captured_at`-as-proxy disambiguation a future consumer
    can use.
  - **`max_range_charge_counter`** (migration `20260905000001`) is a **ninth** column of
    exactly that shape: copied verbatim from the day's own `telemetry.Snapshot`, always
    populated regardless of a computable predecessor, nullable, **no backfill**. Two
    things set it apart from the eight above. It carries **no unit suffix** because it
    is a count, not a measurement (`ai/go-conventions.md` §display units). And its NULL
    is **ambiguous like `sentry_mode`'s**, not like the other seven: the vehicle may not
    have reported it (the telemetry source field is itself a `*int`) or the row may
    predate the migration — disambiguate via `captured_at`. A reported `0` is stored as
    `0`, never NULL.
  - **`tpms_pressure_fl_psi`/`fr`/`rl`/`rr`** (migration `20260908000002`,
    `RM50-analytics-add-tire-pressure-columns`) are four more columns of exactly the
    same shape as `max_range_charge_counter`: copied verbatim from the day's own
    `telemetry.Snapshot`, always populated regardless of a computable predecessor,
    nullable. Names match `telemetry.vehicle_snapshots`' own column names exactly
    (`fl`/`fr`/`rl`/`rr` = front-left/front-right/rear-left/rear-right), already in PSI —
    no conversion at this layer. **Unlike** `max_range_charge_counter`, this migration
    **DID backfill** every pre-existing row from `telemetry.vehicle_snapshots` in the
    same migration (a one-off, user-confirmed deviation from "No Cross-Module Database
    Access" — a `goose`-run SQL statement, never a Go import; see design.md Part C for
    the full rationale). NULL still means one of two things — the vehicle did not report
    TPMS at that capture, or the row predates the migration and had no matching
    snapshot to backfill from — but no consumer needs to disambiguate them (unlike
    `sentry_mode`/`max_range_charge_counter`, this NULL is not otherwise ambiguous:
    `telemetry.Snapshot`'s own TPMS fields never had a fabricated non-nil default).
- `vehicle_metric_watermarks` — one recompute cursor per `(account_id, tesla_id,
  source)`, three sources. Drives `Reconcile`'s incremental pass; no row means "epoch",
  i.e. backfill the vehicle's full history (`design.md` D7).
- `charge_gaps` — one row per flagged vehicle-day whose battery math does not add up
  (migration `20260815000002`, originally `RM28-telemetry-add-charge-gap-storage`,
  MAG-15; moved into this module, unchanged, by `RM29-analytics-own-charge-gaps`,
  MAG-26 tier 5). Written through the `GapWriter` port, driven by this module's own
  `ConsumedByDay`-derived flagging logic (D5/D5a) via `internal/app`'s nightly
  reconciliation — this module both derives the gap AND stores the conclusion; no
  other module writes or reads this table. Columns: `id UUID PRIMARY KEY`,
  `account_id UUID NOT NULL`, `tesla_id BIGINT NOT NULL` (**always resolved, NOT
  NULL** — this module filters out any vehicle/session it cannot attribute to a
  currently-registered vehicle before gap detection ever runs), `vin TEXT NOT NULL`,
  `gap_date DATE NOT NULL` (the flagged calendar day, plain `DATE` — no time-of-day
  component), `missing_charging_type TEXT NOT NULL CHECK (IN ('MANUAL',
  'SUPERCHARGER'))` (which charge source is suspected missing — `SUPERCHARGER` when
  a Supercharger session exists that day with NULL start/end battery percentages,
  `MANUAL` otherwise), `created_at TIMESTAMPTZ NOT NULL DEFAULT now()` (when FIRST
  flagged — preserved across every re-upsert of the same still-flagged day),
  `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()` (refreshed to `now()` on every
  re-confirmation). `UNIQUE (account_id, tesla_id, gap_date)` constraint
  (`charge_gaps_account_tesla_date_unique`) is both the write-idempotency mechanism
  (`ON CONFLICT DO UPDATE`) and the index that serves `GapWriter`'s own
  read-before-diff query — no separate index needed for that path. A second index,
  `idx_charge_gaps_account (account_id, gap_date DESC)`, serves the future
  account-wide notification read pattern (no `tesla_id` predicate) — out of scope
  today, no read port exists for it yet. **No FK** on `account_id`/`tesla_id` (same
  no-cross-module-FK precedent as `vehicle_metrics`/`vehicle_metric_watermarks` —
  referential integrity is upheld by flow, not a DB constraint,
  `ai/architecture.md` §2). **No `raw_data` JSONB** — this table stores a
  Go-computed conclusion (this module's own derivation), not an external API
  response, so the mandatory-`raw_data` rule (`ai/go-conventions.md` §persistence)
  does not apply here.

### `GapWriter`'s upsert-and-delete lifecycle — **no `resolved_at`, ever**

`charge_gaps` has **no soft-delete / `resolved_at` column** — `ReconcileWindow`
`UPSERT`s every day that still flags and **`DELETE`s** every previously-stored day,
within the window it just recomputed, that no longer flags (design D7b of the
original `RM28-telemetry-add-charge-gap-storage`). Fixing a charge entry clears the
row on the very next nightly run with no extra wiring. This table is a **live
worklist** ("what is outstanding right now"), not an audit trail of resolved gaps.

**If you are the one adding the future notification feature or any other consumer
of this table: do NOT "fix" this into a soft-delete/`resolved_at` shape.** A
soft-deleted row would need its own cleanup story (when does a resolved row
actually get purged?) that this design deliberately avoids by making resolution a
plain `DELETE` — the row's mere existence already means "outstanding," so a
consumer needs no `WHERE resolved_at IS NULL` filter and no purge job. If a history
of resolved gaps is ever needed, that is a **new, separate** table (e.g. an
append-only `charge_gap_history`), not a mutation of `charge_gaps`'s own
delete-on-resolve contract — see the archived
`RM28-telemetry-add-charge-gap-storage`'s `design.md` "Migration Plan" / D-Table2
for the rejected `resolved_at` alternative and its reasoning.

The module still owns no *domain* data: every input is another module's, read through
its public port. What it owns is the **derivation of that input** — which is the whole
point of the boundary (`ai/architecture.md` §6). The one non-database piece of
module-local state remains `capacity.go`'s `packCapacityKWh`, an in-package Go
`map[string]float64` maintained from public Tesla spec sheets, not a database object
and not subject to the `database` design gate. Update that map directly (a code
change); it needs no migration.

## Testing

This module has **both** offline unit tests and `DATABASE_URL`-gated DB-integration
tests. The second half is new as of `RM29-analytics-add-vehicle-metrics`; this section
previously said the module "must never gain one", which stopped being true when the
module gained a database.

Offline (no `DATABASE_URL`, no Docker):

- `derive_test.go` tests the pure derivation functions (`socReadings`,
  `deriveEfficiency`) directly with plain `[]telemetry.Snapshot` / `float64` inputs —
  no fakes needed, since they have zero I/O.
- `reader_test.go` tests `RecentEfficiency` against hand-written fakes of the four
  dependencies (`telemetry.Reader`, `charging.SuperchargerSessionAnalyticsReader`,
  `charging.Reader`, `vehicleLookup`), mirroring the `fakeReadStore`/
  `newFakeReader` pattern in `internal/telemetry/reader_test.go` one level up (fake
  *ports* instead of a fake *store*).

DB-backed (`testdb_test.go` + `db_integration_test.go`):

- `testdb_test.go` provisions the test database with **`testdb.ProvisionDirs`**, not
  `testdb.Provision`. This module's fixtures span three schemas — `Recalculate` reads
  telemetry's `vehicle_snapshots` and `supercharger_sessions` and charging's
  `manual_charge_entries`, then writes this module's `vehicle_metrics` — and
  `//go:embed` cannot reach outside its own directory tree, so a single embedded
  filesystem could only ever carry this module's own two tables. See
  `ai/go-conventions.md` §persistence and `internal/testdb`'s doc comment.
- `db_integration_test.go` seeds those cross-module fixtures with **direct `INSERT`s**
  (RM29 decision D19). That is deliberate and authorized: `telemetry` exposes no public
  writer for a single snapshot and none at all for a Supercharger session, and this
  module may not import `internal/tesla` to drive `Collector.CollectAll`. Use
  `charging.NewWriter(pool).Create` where a manual-charge entry is needed — that writer
  does exist and is the right tool.
- These tests **self-skip** when no Postgres is reachable and no Docker daemon can
  provision one; the offline tests above must still run and pass in that state.
- `RM38-analytics-add-vehicle-status-columns` (MAG-12 tier 1) added `consumed_test.go`'s
  offline Fixture RM38-A/RM38-B cases and `db_integration_test.go`'s DB-backed
  Fixture RM38-A/RM38-B `Recalculate` cases plus four `LatestMetricsByAccount` cases
  (single vehicle, two vehicles' own latest days, a pre-migration row, an empty account).
- `db_gap_writer_integration_test.go`'s 7 tests (`GapWriter.ReconcileWindow` —
  idempotent upsert, delete-on-resolve, empty-flagged-set clear, tenant isolation,
  mis-scoped-entry rejection, outside-window non-interference, per-vehicle
  independence) are **DB-integration-only, with no offline counterpart** —
  `ReconcileWindow` is a transactional read-diff-write, not a pure function, so
  there is nothing to unit-test without a database. This mirrors `SuperchargerReader`'s
  own testing shape one section up in this file: a write/read port whose only
  meaningful test is against a real Postgres. Moved verbatim from
  `internal/telemetry/db_gap_writer_integration_test.go` by
  `RM29-analytics-own-charge-gaps`, repackaged `package telemetry` → `package
  analytics` and re-targeted `newTestStore`'s pool half onto this file's own
  `newTestPool` — no assertion, fixture value, or test name changed.

Run `go test ./internal/analytics/...`. The offline tests must pass with `DATABASE_URL`
unset and Docker down.
