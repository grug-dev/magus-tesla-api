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
charging-cost sources the platform stores (`telemetry.SuperchargerReader` and
`internal/charging`), with a pack-capacity correction sourced from a small
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
  detection), `MissingChargingType` (`telemetry.MissingChargingType`, valid only when
  `Flagged`, D7a), `DaysSpanned` (the row's own `DaysSpannedCalc`, >1 signals a multi-day
  span, D8).
- `Reader` — `OdometerDeltaByDay(ctx, accountID, teslaID, start, end) ([]DayDistance, error)`:
  per-day distance travelled over `[start, end]`, read from the precomputed
  `vehicle_metrics` rows. Sparse on the same principle as `ConsumedByDay` — a day whose
  `distance_traveled_km_calc` is NULL (a predecessor-less day, D9) yields no entry, via
  an `IS NOT NULL` filter in the SQL rather than a zero comparison. Added by
  `RM29-analytics-add-vehicle-metrics` so the gateway stops deriving distance from
  snapshots itself (roadmap D5).
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
  boundary instead. **`cmd/poller` is its only production caller**, once per vehicle
  after each successful nightly cycle and *before* that cycle's charge-gap step —
  the gateway's post-write `Recalculate` covers only the days a manual charge write
  touches, so if this call is ever dropped, every vehicle's charts silently stop
  advancing.
- `DayDistance` — one calendar day's distance result, backing `OdometerDeltaByDay`.
- `NewRecalculator(pool *pgxpool.Pool, telemetryReader telemetry.Reader, supercharger telemetry.SuperchargerReader, manual charging.Reader) Recalculator`
  is the constructor for the write side.
- **`ConsumedByDay`'s signature is unchanged, but its implementation is not.** It used
  to fetch from the three ports and derive on every call; it now reads the precomputed
  `vehicle_metrics` rows. Callers see the same contract; the cost profile is completely
  different, which is the point (`Performance-Profile`: read-heavy).
- `DefaultWindow` — exported `time.Duration` constant, 30 days. Deployment code passes
  it (or a different duration) to `NewReader` at construction; the window is NOT a
  per-call argument to `RecentEfficiency` (`design.md` D3).
- `GapReconciliationWindow` — exported `time.Duration` constant, 30 days. The rolling
  window `cmd/poller` re-derives and reconciles against `telemetry`'s `charge_gaps`
  ledger every nightly run, via `ConsumedByDay` (`design.md` D4/D4a/D7b). Unlike
  `DefaultWindow`, this is not consumed by `NewReader` — `cmd/poller` passes it directly
  as the `[start, end]` window to `ConsumedByDay`.
- `NewReader(pool *pgxpool.Pool, telemetry telemetry.Reader, supercharger telemetry.SuperchargerReader, manual charging.Reader, account vehicleLookup, window time.Duration) Reader`
  is the constructor — it gained the leading `*pgxpool.Pool` in
  `RM29-analytics-add-vehicle-metrics`, since `ConsumedByDay`/`OdometerDeltaByDay` now
  read this module's own tables. `vehicleLookup` is an unexported narrow interface covering only
  `RegisteredVehicles` — any real `account.Service` satisfies it automatically
  (structural typing), no adapter needed at the call site. `ConsumedByDay` uses only
  three of the four wired dependencies (`telemetry`, `supercharger`, `manual`) and none
  of `account`/`window` — the signature is unchanged by `ConsumedByDay`'s addition
  (`design.md` D-B1).

No HTTP/JSON surface in this module (none required — `ai/architecture.md` §3).

## Allowed / forbidden imports

**May import (public ports only):**
- `internal/telemetry` — `telemetry.Reader` (`SnapshotsByVehicleSince`,
  `SnapshotPrecedingDay` — added by `RM29-telemetry-drop-derived-columns`, the exact-
  predecessor lookup `Recalculate` uses to derive the five consumption figures itself),
  `telemetry.SuperchargerReader` (`SuperchargerSessionsByVehicle`), and the domain
  types `telemetry.Snapshot`, `telemetry.SuperchargerSession`.
- `internal/charging` — `charging.Reader` (`ListEntriesByVehicle`) and the
  domain type `charging.Entry`.
- `internal/account` — the narrow `RegisteredVehicles` method (satisfied by
  `account.Service`) and the domain type `account.Vehicle`.
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
- `vehicle_metric_watermarks` — one recompute cursor per `(account_id, tesla_id,
  source)`, three sources. Drives `Reconcile`'s incremental pass; no row means "epoch",
  i.e. backfill the vehicle's full history (`design.md` D7).

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
  dependencies (`telemetry.Reader`, `telemetry.SuperchargerReader`,
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

Run `go test ./internal/analytics/...`. The offline tests must pass with `DATABASE_URL`
unset and Docker down.
