Source: MAG-31 — https://linear.app/magus-monitor/issue/MAG-31/analyze-scheme-per-module
Roadmap: openspec/roadmaps/RM39-schema-per-module.md
Tier: 2 of 5 (`account` is archived; tier 3 is `RM39-charging-move-to-own-schema`, tier 4 is
`RM39-telemetry-move-to-own-schema` — blocked on a separate boundary ticket, D6 — tier 5 is
`RM39-telemetry-rename-supercharger-port`, blocked behind tier 4)

## Why

MAG-31 asks that every `internal/` module owning persistence get its own PostgreSQL schema,
named after the module, so the modular-monolith boundary — today enforced only by Go import
guards (`ai/architecture.md` §2 "no cross-module database leaks") and convention — becomes
visible in the database catalog and checkable at codegen time. Tier 1 (`account`) proved the
pattern: one additive goose migration, schema-qualified queries, `gen.go.rename` entries that
freeze the Go surface, and a `models.go` diff as the mandatory verification step (a wrong
`rename` key fails silently at exit 0). This tier repeats that exact pattern for
`internal/analytics`, the second of three unblocked tiers (`charging` is tier 3; `telemetry`
is blocked behind a separate boundary ticket, D6).

`internal/analytics` has more cross-module *reads* than `account` did (it reads
`internal/telemetry` and `internal/charging` through their public ports to derive its own
tables), but **zero cross-module entanglement inside its own migrations or tables** — no other
module imports `analyticsdb`, and none of analytics's own migrations reference another
module's table. So this tier carries the same shape as tier 1: **D1** (one additive migration:
`CREATE SCHEMA IF NOT EXISTS analytics` + one `ALTER TABLE … SET SCHEMA analytics` per table,
real reversible `-- +goose Down`), **D2** (every table reference in
`internal/analytics/db/query.sql` becomes schema-qualified — forced by sqlc, not a style
choice), **D3** (`gen.go.rename` entries under the analytics `sqlc.yaml` entry keep
`ChargeGap`, `VehicleMetric`, `VehicleMetricWatermark` byte-identical, verified by an empty
`models.go` diff), and **D4** (goose itself is untouched — the shared `public.goose_db_version`
table stays, `make migration-guard` is not retired, no `db-reset` is needed). The table-rename
decisions **D5a/D5b/D5c do not touch this module** — analytics's three tables
(`vehicle_metrics`, `vehicle_metric_watermarks`, `charge_gaps`) keep their exact names; only
their schema changes.

**The one thing genuinely specific to this tier**, called out explicitly in the roadmap's
tier-2 row: `vehicle_metric_watermarks.source` stores the literal string values
`'vehicle_snapshots'`, `'supercharger_sessions'`/`'charge_sessions'`, and
`'manual_charge_entries'` — these name OTHER modules' tables by convention, as DATA under a
CHECK constraint, not as table references this migration's DDL needs to resolve. They are left
completely untouched (see `design.md` D-source-values). Tier 3's D8 rewrites that CHECK and
deletes some of those rows for an unrelated reason (a vocabulary collision from the `charging`
rename) — not this tier's concern.

This tier also implements **D9**, the finding tier 1 surfaced only after implementation: raw
SQL hand-written inside this module's own `_test.go` files (`DELETE FROM …`, `SELECT count(*)
FROM …`, fixture-seeding `INSERT INTO …`) is invisible to sqlc and to `go vet`, so no
Claude-runnable signal catches an unqualified reference there — only the owner's suite does.
Every such statement referencing analytics's own three tables is schema-qualified in this
change; statements seeding OTHER modules' tables (`vehicle_snapshots`, `supercharger_sessions`,
`charge_sessions` in `internal/telemetry`/`internal/charging`, seeded directly per this
module's own D19 testing precedent since those modules haven't moved schema yet) are correctly
left bare.

## What Changes

- **Migration** — one new goose migration in `internal/analytics/db/migrations/` creates the
  `analytics` schema and moves `vehicle_metrics`, `vehicle_metric_watermarks`, and `charge_gaps`
  into it via `ALTER TABLE … SET SCHEMA`. No existing migration is edited. Every row, primary
  key, unique constraint, CHECK constraint, and index carries over unchanged — catalog-only, not
  a table rewrite (see `design.md`).
- **sqlc regeneration** — every table reference in `internal/analytics/db/query.sql`
  (`vehicle_metrics`, `vehicle_metric_watermarks`, `charge_gaps`, 10 `-- name:` blocks) becomes
  schema-qualified (`analytics.vehicle_metrics`, etc.). sqlc fails codegen on a bare name once a
  table leaves `public` — forced, not chosen.
- **`sqlc.yaml`** — the analytics entry's `gen.go` block gains a `rename:` map (three entries,
  singularized `analytics_<table>` keys) so `ChargeGap`, `VehicleMetric`, and
  `VehicleMetricWatermark` keep their exact current Go names through the schema move. `models.go`
  was diffed after `make sqlc` and is byte-identical — zero type-name drift.
- **Test files (D9)** — 18 hand-written SQL statements across `db_gap_writer_integration_test.go`
  (4), `db_integration_test.go` (7), and `db_watermark_migration_integration_test.go` (7) that
  reference analytics's own tables are schema-qualified. One additional, structurally distinct
  fix in `db_watermark_migration_integration_test.go`: its `newAnalyticsMigrationProvider` goose
  connection, which replays historic migration `20260828000001`'s own bare Up/Down SQL directly
  (via `ApplyVersion`, out of chronological order, after this tier's schema move has already
  applied), now opens with `search_path=public,analytics` so that historic migration's
  deliberately-unqualified reference still resolves — without editing the migration file itself
  (`design.md` D-watermark-replay).
- **Docs** — `internal/analytics/AGENTS.md`'s "Data ownership" section states the `analytics`
  Postgres schema and explicitly calls out that the watermark source strings are data, not table
  references, and are untouched by this migration.
- **No Go domain-type, port, or query *name* change.** `ChargeGap`, `VehicleMetric`,
  `VehicleMetricWatermark`, `Reader`, `Recalculator`, `GapWriter`, and every exported method keep
  their exact current shape — this tier is pure namespacing at the database layer.

**Not breaking.** The migration is additive (new schema, catalog-only table moves, no dropped
column, no changed type, no renamed table) and every constraint/index survives. It changes zero
public Go signatures, so no consumer (`internal/app`, `internal/gateway`) needs to change.

**Affected modules:** `internal/analytics` only for production code. `internal/telemetry` and
`internal/charging` are read through their public ports (unchanged) and are not schema-moved by
this tier — their own tables stay in `public` until tiers 3/4.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `analytics`: a new requirement, "Module-Scoped Database Schema," stating that the analytics
  module's `vehicle_metrics`, `vehicle_metric_watermarks`, and `charge_gaps` tables live in a
  dedicated `analytics` Postgres schema, that this is a namespacing change only, and that the
  `vehicle_metric_watermarks.source` stored string vocabulary is unaffected — it remains data
  naming other modules' tables by convention, not a schema-qualified reference.

## Impact

- `internal/analytics` — one new migration, ten `query.sql` table references become
  schema-qualified, `sqlc.yaml` gains a `rename:` block, `make sqlc` regeneration (verified
  zero-diff on type names), 18 hand-written test SQL statements schema-qualified, one test-only
  goose connection gains a `search_path` fix, `AGENTS.md` updated. No test *behavior* change —
  the existing suite exercises the same ports with the same expected values; only the underlying
  schema the test database resolves against does.
- No other module. `internal/gateway`, `internal/tesla`, `internal/telemetry`,
  `internal/charging`, `internal/account` are untouched — none imports `analyticsdb`.

**Read paths affected** (per `openspec/config.yaml`'s performance rule): every existing
`analytics` read path (`RecentEfficiency`, `ConsumedByDay`, `OdometerDeltaByDay`,
`BatteryLevelByDay`, `LatestMetricsByAccount`) is unaffected in cost — `ALTER TABLE … SET
SCHEMA` does not touch physical storage, indexes, or their statistics, so every existing index
continues to serve the same plans post-migration (see `design.md`'s index plan). No new index is
added and none is needed.
