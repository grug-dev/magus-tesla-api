# Proposal — RM52-charging-add-monthly-effective-capacity

Source: MAG-32 — https://linear.app/magus-monitor/issue/MAG-32/vehicle-monthly-metrics-new-table
Roadmap: `openspec/roadmaps/RM52-vehicle-monthly-metrics.md` — **tier 1 of 3**, module `charging`,
implementing roadmap decisions **RD1–RD5, RD7 (partly), RD10, RD11** verbatim. Decisions RD6, RD8,
RD9 belong to tier 2 (`app`) and tier 3 (`platform`) and are not implemented here.
Design gate: **TRIPPED.** This change adds a new table, `charging.monthly_effective_capacity`, via
a new goose migration, plus two new read queries against the two existing tables. Per
`openspec/config.yaml` §design and `CLAUDE.md` §Pipeline config → `Design-Gates: database`,
design.md carries the full DDL, the rationale with rejected alternatives, and the index plan, and
**the owner must confirm that design before any implementation is dispatched.** This design was
already confirmed once, before the module changed (roadmap RD11 header) — the owner must confirm
it again now that the table lives in `charging` instead of `analytics`.
Unit tests: **included** — the module's standing testing convention (`ai/go-conventions.md`
§Testing) applies unconditionally to `internal/charging`: "Tests for this module are welcome and
required (no paid-API risk)" (`internal/charging/AGENTS.md` §Testing Notes). Both kinds: offline
unit tests for the pure estimator and the `packCapacityKWh` fallback (no DB), plus
`DATABASE_URL`-gated integration tests for the schema, the job, and the two updated callers.
Expected values are fixed in design.md §Test Contract **before** implementation, per
`ai/go-conventions.md` §Testing authoring order.

The interview of record is the roadmap's own `grill-me` session, 2026-09-10 (see
`openspec/roadmaps/RM52-vehicle-monthly-metrics.md` "Decisions"). This tier does not re-run it and
does not re-open RD1–RD14.

---

## Why

`internal/charging` divides by a hardcoded `62.0` kWh pack capacity every time it needs to derive
energy from a battery-percentage delta, or a start percentage from energy. Every vehicle gets the
same number, and it is wrong for all of them.

MAG-25 already stores `inferred_capacity_kwh_calc` on each charge record — the capacity that one
charge implies. One record is noisy. Many records, pooled and filtered, are not.

This change adds a monthly job, owned by `charging`, that pools a vehicle's valid records for one
month, takes a robust estimate, and stores one number per vehicle per month. `packCapacityKWh`
then reads that table instead of returning the constant. Behaviour is unchanged until the first
month is computed — every vehicle keeps seeing `62.0` until it has a measured row.

### Why this lives in `charging`, not `analytics` (roadmap "Revision")

An earlier version of this roadmap put the table in `internal/analytics`. It was withdrawn before
any code existed: `analytics` already imports `charging`, so `charging` could not import back, and
the plan needed a consumer-side interface to close the loop. Worse, it forced `analytics` to encode
*why* `ESTIMATED` entries and `DONE_CALCULATED` sessions are unusable — a fact about `charging`'s
own internals (they were derived from `charging`'s own `62.0` constant).

`charging` owns every input row, so `charging` owns the derivation. There is no cycle, and no
cross-module port is needed (roadmap RD1, RD14; `ai/architecture.md` §2 "First ask where the fact
belongs, not how to break the cycle").

## What Changes

- **ADDED** — one goose migration
  `internal/charging/db/migrations/20260909000002_add_monthly_effective_capacity.sql`, creating
  `charging.monthly_effective_capacity` exactly as roadmap RD11 specifies: `tesla_id BIGINT NOT
  NULL`, `effective_period DATE NOT NULL` (checked to be the 1st of a month), a nullable
  `effective_capacity_kwh DOUBLE PRECISION`, `sample_count INTEGER NOT NULL DEFAULT 0`, and a
  `UNIQUE (tesla_id, effective_period)` constraint that alone serves both the read and the write
  (no separate index). Full DDL, rationale and index plan in design.md §"Database Changes".
- **ADDED** — two new read queries in `internal/charging/db/query.sql`:
  `ListValidManualEntryCapacitiesForPeriod` (manual entries, `energy_source = 'USER'`) and
  `ListValidSessionCapacitiesForPeriod` (Supercharger sessions, `status = 'DONE'`, `tesla_id IS NOT
  NULL`) — the RD2 filter and the RD5 null-skip, applied at the SQL level.
- **ADDED** — one write query, `UpsertMonthlyEffectiveCapacity` (`ON CONFLICT (tesla_id,
  effective_period) DO UPDATE`), and one read query, `LatestMeasuredCapacity` (the newest row
  whose `effective_capacity_kwh IS NOT NULL`, for one `tesla_id`) — RD11's own two seam queries,
  verbatim.
- **CHANGED** — `internal/charging/db/query.sql`: `LockSessionForVerification` gains `tesla_id` to
  its `SELECT` list (needed by `VerifySession`'s updated call to `packCapacityKWh`, RD11's caller
  table). No other column, predicate, or lock behaviour changes.
- **ADDED** — `internal/charging/monthly_capacity.go`: the RD3 estimator
  (`estimateEffectiveCapacity`, a pure function over a slice — no `ctx`, no I/O), the two Go
  constants `minSamples = 3` and `minDeltaPct = 15`, and the job (`monthlyCapacityCalculator`)
  that applies the RD2 filter (already done in SQL), pools by `tesla_id` (RD5, in Go, across both
  source tables), calls the estimator, and upserts one row per vehicle it considered.
- **ADDED** — `MonthlyCapacityCalculator` (public port) and `MonthlyCapacityReport` (its result
  type) in `internal/charging/charging.go`, plus `NewMonthlyCapacityCalculator` — the module's
  seventh public constructor, following the same one-file-declares-the-interface pattern every
  other port here already uses.
- **CHANGED** — `internal/charging/capacity.go`: `packCapacityKWh` changes shape from
  `packCapacityKWh(ctx, vin string)` to `packCapacityKWh(ctx, lookup packCapacityLookup, teslaID
  int64)` (RD11). It now reads `monthly_effective_capacity` through the new `packCapacityLookup`
  seam instead of returning a hardcoded constant, and falls back to the same value
  (`defaultPackCapacityKWh = 62.0`) only when no measured row exists yet. The stale `TODO(MAG-18)`
  comment is removed — **this change closes backlog item #18** (roadmap RD1).
- **CHANGED** — `internal/charging/service.go`: the unexported `store` interface gains
  `latestMeasuredCapacity`; `dbStore` implements it; `resolveEnergy` gains a `store` parameter and
  passes `e.TeslaID` (not `e.VIN`) to `packCapacityKWh` (RD11's first caller row). `Create` and
  `Update` pass `w.store` through unchanged otherwise.
- **CHANGED** — `internal/charging/session_verifier.go`: `VerifySession` reads the locked row's
  `tesla_id`; when it is `nil` (the VIN is not a currently-registered vehicle), the derivation uses
  `defaultPackCapacityKWh` directly, without calling `packCapacityKWh` at all (RD11's second caller
  row: "nullable — nil goes straight to the `62.0` fallback"). `*sessionVerifier` gains a
  `latestMeasuredCapacity` method so it satisfies `packCapacityLookup` without joining `service.go`'s
  `store` interface (that interface exists for offline-fakeable unit testing of `Create`/`Update`;
  `SessionVerifier` is deliberately not part of it, per its own existing doc comment).
- **CHANGED** — `sqlc.yaml`: one new `rename` entry,
  `charging_monthly_effective_capacity: "MonthlyEffectiveCapacity"`, in the existing `charging`
  module `sql:` block. No other entry changes.
- **CHANGED** — `internal/charging/db/models.go`, `db/query.sql.go` (both **generated**; `make
  sqlc` re-run).
- **ADDED** — `internal/charging/monthly_capacity_estimator_test.go` (offline, package `charging`)
  and `internal/charging/db_monthly_capacity_integration_test.go` (`DATABASE_URL`-gated).
- **CHANGED** — `internal/charging/AGENTS.md` (§Public Interface, §Allowed Imports — adding
  `monthly_capacity.go` to both the `chargingdb` and `pgtype` file lists, §Data Ownership,
  §Testing Notes) — docs-track-structural-change (`CLAUDE.md` §Non-negotiables).
- **UNCHANGED** — every existing table, column, index, and port on `manual_charge_entries` and
  `supercharger_sessions`. **No historical row is recomputed.** Every `ESTIMATED` manual entry and
  every `DONE_CALCULATED` session keeps its stored, `62.0`-derived value forever — this change adds
  a new table and a new read path; it never rewrites `energy_added_kwh`,
  `inferred_capacity_kwh_calc`, or any other stored value on either existing table (roadmap "Future
  work": "Recomputing historical `ESTIMATED` energy values... was explicitly rejected here").

**Out of scope, deliberately:** the nightly-processor trigger (tier 2, `app`), and the `cmd/`
runnable, `make` target, and docs (tier 3, `platform`). This change does not touch `internal/app`,
`internal/gateway`, `cmd/`, or the `Makefile`.

## Breaking?

**NO.** Every port this module already exposes (`Writer`, `Reader`, `SessionWriter`,
`SessionReader`, `SuperchargerSessionAnalyticsReader`, `SessionVerifier`) is untouched: no method
gains, loses, or re-signs. `Entry` and `Session` gain no field. The one exported surface this
change adds — `MonthlyCapacityCalculator`, `MonthlyCapacityReport`, `NewMonthlyCapacityCalculator`
— is wholly new, so nothing outside this module can already depend on it incompatibly.

The two signature changes this tier makes — `packCapacityKWh` and `resolveEnergy` — are both
**unexported** functions, called from exactly the two sites this proposal names, both inside
`internal/charging`. No caller outside this module can observe the change.

**Behaviour is unchanged until the first month is computed.** `packCapacityKWh` still returns
`62.0` whenever `monthly_effective_capacity` has no measured row for a vehicle — which is every
vehicle, until the job in this tier is actually run (by the tier-2 nightly step, or by hand). `go
build ./...` and `go vet ./...` are expected to stay green repeatedly across module boundaries,
with no leader-dispatched cross-module compile fix needed.

## Modules affected

- **`charging`** — owner. Schema, domain types, both updated write-time seams, the new job, tests,
  `AGENTS.md`.
- **`app`** — **not affected by this tier.** Tier 2 (`RM52-app-add-monthly-capacity-step`) is the
  only consumer of `MonthlyCapacityCalculator`, and it has not been dispatched yet.
- **`gateway`**, **`analytics`** — **not affected.** Neither imports anything this change touches;
  `internal/gateway` is not touched by this roadmap at all (roadmap RD8).
- No other module. `monthly_effective_capacity` is owned exclusively by `internal/charging`
  (`ai/architecture.md` §2); no cross-module FK, no cross-module read.

## Read paths affected

Per `openspec/config.yaml` §proposal (this change touches the database).

| Call site | Query | Frequency | Effect |
|---|---|---|---|
| `Writer.Create` / `Writer.Update` (via `resolveEnergy`, only when `EnergyAddedKWh` is `nil`) | `LatestMeasuredCapacity` | request-time, conditional | +1 indexed point lookup (`tesla_id` equality, `effective_period DESC` range on the table's own `UNIQUE` btree), replacing a hardcoded constant |
| `SessionVerifier.VerifySession` (via `packCapacityKWh`, only when `needsDerivedStartBatteryPct` fires, and only when the locked row's `tesla_id` is non-nil) | `LatestMeasuredCapacity` | request-time, conditional, inside the existing transaction | same as above |
| `SessionVerifier.VerifySession` (always, via `LockSessionForVerification`) | `LockSessionForVerification` | request-time, conditional (only when deriving `start_battery_pct`) | +1 column (`tesla_id`) on an existing single-row `FOR UPDATE` fetch; no plan change |
| `MonthlyCapacityCalculator.Calculate` (new; not yet called by anything until tier 2 lands) | `ListValidManualEntryCapacitiesForPeriod`, `ListValidSessionCapacitiesForPeriod` | monthly batch | new, unindexed range scans bounded to one calendar month — see design.md §Index Plan for why no new index is added |
| `MonthlyCapacityCalculator.Calculate` (new) | `UpsertMonthlyEffectiveCapacity` | monthly batch, once per vehicle considered | new write, served by the table's own `UNIQUE` constraint as the conflict target |

**No existing query's plan changes.** `LatestMeasuredCapacity` and `UpsertMonthlyEffectiveCapacity`
are new queries against a new, empty-until-first-run table. The two batch-read queries scan
existing tables but add no `WHERE`, `ORDER BY` or index requirement to any *existing* read — see
design.md §Index Plan for the batch queries' own justification.

## Impact

- **Affected spec:** `monthly-effective-capacity` (new capability) — every requirement is
  **ADDED**.
- **Affected code:** `internal/charging/` only —
  `db/migrations/20260909000002_add_monthly_effective_capacity.sql` (new), `db/query.sql`,
  `db/models.go` + `db/query.sql.go` (regenerated), `sqlc.yaml`, `charging.go`, `capacity.go`,
  `service.go`, `session_verifier.go`, `monthly_capacity.go` (new), new `_test.go` files,
  `AGENTS.md`.
- **Design gate: tripped.** The owner confirms design.md before implementation. The DDL itself was
  already confirmed once (roadmap RD11); what needs re-confirming is that it now lives in
  `charging`'s own migration directory and schema instead of `analytics`'s.
- **`MIGRATIONS_DIRS` order check (recorded, not just claimed).** This migration's `CREATE TABLE`
  names only the new table it creates; it reads no other table and no other module's migration
  reads it. The shared order (account → telemetry → charging → analytics) is unaffected — this is
  a finding, verified by reading the migration's own SQL, not an assumption.
- **Deferred, explicitly NOT in scope:** the nightly-processor wiring (tier 2), the `cmd/`
  runnable and `make` target (tier 3), any gateway surface (roadmap RD8 — none is planned, ever,
  for this feature), and recomputing any historical `ESTIMATED`/`DONE_CALCULATED` row (roadmap
  "Future work" — rejected, needs its own ticket if ever done).

## Modules affected — summary table

| Module | Change |
|---|---|
| `charging` | Owner. New table, new port, updated seam, tests, docs. |
| `app` | None in this tier — tier 2 wires the trigger. |
| `gateway` | None, ever, per roadmap RD8. |
| `analytics` | None — this roadmap explicitly moved the work out of `analytics` (roadmap "Revision"). |
