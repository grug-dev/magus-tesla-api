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

The module's contract is a Go interface (`ai/go-conventions.md` — interface-first).
**Signatures and the per-method doc comments live in `internal/analytics/analytics.go` — read
them there.** They are deliberately not copied here.

Three ports. `Reader` exposes five reads; `Recalculator` and `GapWriter` are the write side:

| Port | Method | Returns |
|---|---|---|
| `Reader` | `RecentEfficiency` | `Efficiency` — rolling Wh/km over the constructed window |
| | `ConsumedByDay` | `[]DayConsumption` — corrected per-day battery-consumed % |
| | `OdometerDeltaByDay` | `[]DayDistance` — per-day distance from `vehicle_metrics` |
| | `BatteryLevelByDay` | `[]DayBattery` — per-day battery level + estimated range |
| | `LatestMetricsByAccount` | `[]VehicleStatus` — latest row per vehicle for an account |
| `Recalculator` | `Recalculate`, `Reconcile` | rebuild `vehicle_metrics`; `Reconcile` is the incremental watermark pass |
| `GapWriter` | `ReconcileWindow` | upsert the days that flag, DELETE the days that stopped |

What the source does not tell you:

- **Never fabricate a number.** `RecentEfficiency` returns `ok=false` with no error when there
  is not enough data. `Efficiency.Approximate=true` means the pack capacity was unknown and
  the SoC-drift correction was dropped — the computation still ran.
- **Results are SPARSE, and absence IS the "no data" signal.** A day with no computable value
  gets no entry — never a zero. Do not densify a result to make a chart simpler; the gateway
  already handles gaps.
- **`BatteryLevelByDay` is the exception to that filter.** Its columns are `NOT NULL` raw
  observations with no predecessor requirement, so a predecessor-less day — which
  `ConsumedByDay` and `OdometerDeltaByDay` exclude — still gets an entry here.
- **A returned `Date` is a FINAL bucket key.** It is the row's own already-effective
  `metric_date` and must never be re-projected through a caller's own day math.
- **Calendar-day bucketing uses the platform zone, never UTC.** This module never reads
  `telemetry.Snapshot.EffectiveDate`, which is UTC-derived.
- **Return this module's own domain types, never `telemetry.Snapshot`.** `VehicleStatus` is
  the analytics-owned equivalent of a telemetry snapshot, and every optional field on it is
  pointer-typed: `nil` means "no value", never a fabricated default. A reported `0` is a real
  reading and is never collapsed to `nil`.
- **`VehicleStatus.MaxRangeChargeCounter` is a LIFETIME count**, monotonic across rows —
  never a per-day delta.
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

**Column-by-column detail for all three tables** — every `_calc` column, what `nil` means on
each projected field, the density rule, and the index decisions — **lives in
`kkpa/context/entities/vehicle-metrics/guide.md`.** Fetch it before adding or changing a
column.

Two rules about this module's tables that are easy to break without opening them:

- **`vehicle_metric_watermarks.source` stores strings that NAME other modules' tables
  (`'vehicle_snapshots'`, `'supercharger_sessions'`, `'manual_charge_entries'`).** They are
  **data, not table references.** `'supercharger_sessions'` is a value reused from before
  RM31: it now names `charging`'s table, not telemetry's. Do not "correct" it during a rename
  sweep — changing the string orphans every stored watermark.
- **No `raw_data` JSONB on any of them.** The mandatory-`raw_data` rule applies to an external
  API response; these tables store this module's own Go-computed conclusions.

The module owns no *domain* data: every input is another module's, read through its public
port. What it owns is the **derivation** — which is the point of the boundary. The one
non-database piece of module-local state is `capacity.go`'s `packCapacityKWh`, an in-package
Go `map[string]float64` maintained from public Tesla spec sheets. It is **not** a database
object and **not** subject to the `database` design gate — update the map directly, no
migration.

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

Both offline unit tests and `TEST_DATABASE_URL`-gated DB-integration tests. **The offline tests
must pass with `TEST_DATABASE_URL` unset and Docker down** — the DB-backed ones self-skip in that
state.

- **`testdb_test.go` uses `testdb.ProvisionDirs`, NOT `testdb.Provision`.** This module's
  fixtures span three schemas — `Recalculate` reads telemetry's snapshots and charging's
  entries and sessions, then writes this module's `vehicle_metrics` — and `//go:embed` cannot
  reach outside its own directory tree, so a single embedded filesystem could only ever carry
  this module's own tables.
- **The cross-module fixtures are seeded with direct `INSERT`s, deliberately and with
  authorization.** `telemetry` exposes no public writer for a single snapshot and none at all
  for a Supercharger session, and this module may not import `internal/tesla` to drive
  `Collector.CollectAll`. **Where a public writer does exist, use it** —
  `charging.NewWriter(pool).Create` for a manual-charge entry.
- **`GapWriter.ReconcileWindow` has no offline counterpart, by design.** It is a transactional
  read-diff-write, not a pure function, so there is nothing to unit-test without a database.
  Do not add a fake-backed "unit test" for it.
- The pure derivation functions take plain inputs and need no fakes. `RecentEfficiency` is
  tested against hand-written fakes of its four ports — fake *ports*, not a fake *store*.

Run `go test ./internal/analytics/...`.
