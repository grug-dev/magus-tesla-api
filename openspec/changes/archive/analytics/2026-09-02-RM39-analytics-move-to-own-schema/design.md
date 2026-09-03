## Context

`internal/analytics` owns three tables, all currently in the `public` schema:

- `vehicle_metrics` (`20260821000001_add_vehicle_metrics.sql`, plus eight status columns
  from `20260901000001_add_vehicle_status_columns.sql`): `id` (PK), `account_id`, `tesla_id`,
  `metric_date`, `battery_level_pct`, `odometer_km`, `battery_range_km`, five nullable `_calc`
  columns, `consumed_pct`, `flagged`, `missing_charging_type`, `created_at`, `updated_at`, plus
  the eight RM38 status columns (`locked`, `sentry_mode`, `car_version`, `inside_temp_c`,
  `outside_temp_c`, `charging_state`, `charge_limit_soc_pct`, `captured_at`) — all nullable, no
  backfill. Constraints: `vehicle_metrics_account_tesla_date_unique UNIQUE (account_id,
  tesla_id, metric_date)`, `vehicle_metrics_missing_type_iff_flagged CHECK`. Index:
  `idx_vehicle_metrics_latest (account_id, tesla_id, metric_date DESC)`.
- `vehicle_metric_watermarks` (`20260821000002_add_vehicle_metric_watermarks.sql`, source
  vocabulary migrated in place by `20260822000002` and `20260828000001`): `id` (PK),
  `account_id`, `tesla_id`, `source` (`CHECK (source IN ('vehicle_snapshots',
  'charge_sessions', 'manual_charge_entries'))`), `source_updated_at`, `created_at`,
  `updated_at`. Constraint: `vehicle_metric_watermarks_account_tesla_source_unique UNIQUE
  (account_id, tesla_id, source)`.
- `charge_gaps` (`20260815000002_add_charge_gaps.sql`, originally
  `RM28-telemetry-add-charge-gap-storage`, moved into this module unchanged by
  `RM29-analytics-own-charge-gaps`): `id` (PK), `account_id`, `tesla_id`, `vin`, `gap_date`,
  `missing_charging_type` (`CHECK (... IN ('MANUAL', 'SUPERCHARGER'))`), `created_at`,
  `updated_at`. Constraint: `charge_gaps_account_tesla_date_unique UNIQUE (account_id,
  tesla_id, gap_date)`. Index: `idx_charge_gaps_account (account_id, gap_date DESC)`.

Roadmap `RM39-schema-per-module.md` establishes, by running the real toolchain (not by
reasoning), that:

1. sqlc **tracks** `ALTER TABLE … SET SCHEMA` across an additive second migration — the
   generated struct name changes to `<Schema><Table>` unless told otherwise.
2. sqlc **rejects a bare table name** once that table has left `public` — codegen fails at
   generate time, not run time, so a `search_path` cannot paper over it.
3. `gen.go.rename` needs the **singularized** `<schema>_<table>` key form. The plural form is
   silently ignored — no error, exit 0 — so every tier must verify `models.go` by diff, not
   trust the config.
4. Raw SQL in `_test.go` files breaks and **no Claude-runnable signal catches it** (D9) —
   discovered on tier 1 only after the owner ran the suite: `query.sql` was fully qualified and
   `go build`/`go vet`/`gofmt`/both guards were clean, yet 10 account integration tests failed
   with `relation "accounts" does not exist`.

This tier (tier 2 of 5) mirrors tier 1's pattern exactly for the migration/query/sqlc.yaml
layer. It differs from tier 1 in two ways this design records explicitly:

- **`vehicle_metric_watermarks.source` holds literal string values that name other modules'
  tables by convention** — `'vehicle_snapshots'`, `'charge_sessions'` (formerly
  `'supercharger_sessions'`, migrated by `20260828000001`), `'manual_charge_entries'` — under a
  CHECK constraint. These are DATA, not table references this migration's DDL resolves. See
  D-source-values below.
- **One test file replays a historic migration out of chronological order** via
  `goose.Provider.ApplyVersion`, which resurfaces the D9 problem in a form tier 1 never hit
  (tier 1 has no such replay test). See D-watermark-replay below.

Performance profile: **read-heavy** (`ai/architecture.md` §7). This migration is a one-time DDL
event, not a recurring read/write path — see the Index Plan section for why it costs nothing on
the steady-state read paths.

## Goals / Non-Goals

**Goals:**
- Move `vehicle_metrics`, `vehicle_metric_watermarks`, `charge_gaps` into a dedicated
  `analytics` Postgres schema via one additive goose migration, with a real reversible
  `-- +goose Down`.
- Preserve every row, every constraint (PK/UNIQUE/CHECK), and every index unchanged.
- Keep `ChargeGap`, `VehicleMetric`, `VehicleMetricWatermark` and every exported `Reader` /
  `Recalculator` / `GapWriter` method's name and signature byte-for-byte unchanged — this is
  pure namespacing, invisible to every consumer (`internal/app`, `internal/gateway`).
- Make the module boundary (`ai/architecture.md` §2) checkable at the database catalog level,
  not just by Go import guards.
- Leave `vehicle_metric_watermarks.source`'s stored vocabulary and its CHECK constraint
  completely untouched.

**Non-Goals:**
- No table, column, index, or constraint is renamed. D5a/D5b/D5c (the table-rename decisions)
  are scoped to `telemetry` and `charging` — they do not touch `analytics`.
- No data is deleted, backfilled, or transformed. Unlike tier 3's D8 (which deletes specific
  `vehicle_metric_watermarks` rows as a side effect of a CHECK-set vocabulary collision that
  does not exist here), this tier's migration has zero data-mutation statements — every
  statement is `CREATE SCHEMA` or `ALTER TABLE … SET SCHEMA`.
- No `search_path`, role, or `GRANT` change on the **production** connection path — the
  roadmap's Makefile audit already established migrations run as the app role, so `CREATE
  SCHEMA analytics` makes that role the schema owner automatically. (The one `search_path`
  change this tier does make is test-connection-scoped only — D-watermark-replay below — and
  touches no production code path.)
- No `db-reset`. D4/D1 deliberately avoid needing one; the roadmap's stopper gate (an owner-run
  reset) sits after tier 3, not here.
- goose itself is untouched. `public.goose_db_version` stays exactly where it is (D4).
- No change to `vehicle_metric_watermarks.source`'s vocabulary or CHECK constraint — that is
  tier 3's D8, for an unrelated reason (a name collision from `charging`'s rename), not this
  tier's.

## Decisions

### D1 — One additive migration: `CREATE SCHEMA` + `ALTER TABLE … SET SCHEMA` (restated, binding)

Exact DDL — one new goose migration file,
`internal/analytics/db/migrations/20260902000002_move_analytics_to_own_schema.sql` (next free
chronological timestamp — see "Migration filename" below):

```sql
-- +goose Up
CREATE SCHEMA IF NOT EXISTS analytics;

ALTER TABLE vehicle_metrics           SET SCHEMA analytics;
ALTER TABLE vehicle_metric_watermarks SET SCHEMA analytics;
ALTER TABLE charge_gaps               SET SCHEMA analytics;

-- +goose Down
ALTER TABLE analytics.charge_gaps               SET SCHEMA public;
ALTER TABLE analytics.vehicle_metric_watermarks SET SCHEMA public;
ALTER TABLE analytics.vehicle_metrics           SET SCHEMA public;

DROP SCHEMA IF EXISTS analytics;
```

**Why additive, not rewriting the six existing migrations to `CREATE TABLE analytics.vehicle_metrics
(...)` directly.** Same reasoning as tier 1: rewriting history desyncs `goose_db_version` from
reality on every environment that already applied those migrations, and it would need its own
coordinated edit for no benefit. Rejected: rewriting the six pre-existing `analytics` migration
files to create tables directly under `analytics.` from the start.

**Why one migration for all three tables, not three.** All three move together in one deploy;
D7 (tier 3's statement-ordering requirement) does not apply here — analytics has no colliding
table name with another module's still-`public` table.

**Why `IF NOT EXISTS` / `IF EXISTS`.** Follows this module's own precedent (`DROP CONSTRAINT IF
EXISTS`, `to_regclass`-guarded backfills elsewhere in the codebase) — idempotent DDL that
tolerates being re-run against a partially-applied state without erroring.

### D2 — Every table reference in `query.sql` becomes schema-qualified (restated, binding)

Not a style choice — sqlc resolves table names statically against the migration files at
**generate** time and fails codegen on a bare name once a table has left `public`. Every `FROM
vehicle_metrics`, `FROM vehicle_metric_watermarks`, `FROM charge_gaps`, `INSERT INTO
vehicle_metrics`, `INSERT INTO vehicle_metric_watermarks`, `INSERT INTO charge_gaps`, `DELETE
FROM vehicle_metrics`, `DELETE FROM charge_gaps` across all 10 `-- name:` blocks in
`internal/analytics/db/query.sql` becomes `analytics.vehicle_metrics` /
`analytics.vehicle_metric_watermarks` / `analytics.charge_gaps`. No `EXISTS` subqueries exist
in this file (unlike tier 1's account queries) — every reference is a top-level `FROM`/`INTO`.

### D3 — `gen.go.rename` keeps `ChargeGap`, `VehicleMetric`, `VehicleMetricWatermark` unchanged (restated, binding)

Added under the analytics entry's existing `gen.go` block in the root `sqlc.yaml` — **not** the
top-level `overrides:` block, which sqlc ignores for this purpose:

```yaml
        rename:
          analytics_charge_gap:               "ChargeGap"
          analytics_vehicle_metric:            "VehicleMetric"
          analytics_vehicle_metric_watermark:  "VehicleMetricWatermark"
```

Key form is the **singularized** `<schema>_<table>` — `charge_gaps` → `charge_gap`,
`vehicle_metrics` → `vehicle_metric`, `vehicle_metric_watermarks` → `vehicle_metric_watermark`,
each prefixed with the schema name `analytics`. Without this block, sqlc would generate
`AnalyticsChargeGap`, `AnalyticsVehicleMetric`, `AnalyticsVehicleMetricWatermark`.

**Verification is mandatory, not optional** — a wrong key produces no error and exit 0. After
`make sqlc`, `internal/analytics/db/models.go` was diffed against its pre-migration version:
**zero diff** (see Test Contract below for the exact struct bodies).

### D4 — goose is unchanged (restated, binding)

`public.goose_db_version` is untouched by this migration. `make migration-guard` remains
required. No `db-reset` is needed by this tier.

### D-source-values — `vehicle_metric_watermarks.source`'s literal strings are DATA, never schema-qualified

`vehicle_metric_watermarks.source` is a closed-vocabulary `TEXT` column holding one of three
literal strings — `'vehicle_snapshots'`, `'charge_sessions'`, `'manual_charge_entries'` — each
naming, by convention, the physical table a given recompute cursor tracks (see this table's own
column comment, `models.go`). The CHECK constraint enforcing this vocabulary
(`vehicle_metric_watermarks_source_check`, most recently rewritten by `20260828000001`) compares
the `source` **column value** against these three string literals.

**These are DATA, not table references.** A string stored in a `TEXT` column, or a string
literal inside a CHECK constraint's `IN (...)` list, is never resolved by Postgres (or by sqlc)
as an identifier — there is no schema-qualification concept that applies to it at all. Prefixing
`'vehicle_snapshots'` with `analytics.` would not "qualify" anything; it would silently corrupt
the stored vocabulary (`'analytics.vehicle_snapshots'` would fail the CHECK, or worse, succeed
against a differently-written constraint and desync `Recalculator.Reconcile`'s own Go-side
source labels in `recalculate.go` from what the column actually stores).

This migration's DDL contains **zero** `source`-column references and **zero** edits to the
CHECK constraint — it is three `ALTER TABLE … SET SCHEMA` statements and nothing else. The
comments in `models.go`, `query.sql`, and `AGENTS.md` that mention `vehicle_snapshots` /
`supercharger_sessions` / `charge_sessions` by name (as prose, describing what the stored value
means) are also untouched — they are English, not SQL, and a schema move has nothing to say
about them.

**Confirmed by inspection**, not assumed: `git diff` of this change touches no line inside any
`CHECK (...)` clause and no line inside a Go string literal comparing against `source`. See the
final report's explicit confirmation.

**Tier 3 is the one that touches this vocabulary** (D8: `charging`'s rename creates a
byte-identical collision with the pre-`20260828000001` vocabulary, forcing a CHECK rewrite and a
row DELETE) — that is a name-collision problem this tier does not have, and this tier
deliberately does nothing to anticipate or prepare for it.

### D-watermark-replay — a test-only `search_path` fix for a historic migration replayed out of order

`db_watermark_migration_integration_test.go` (added by
`RM31-analytics-read-sessions-from-charging`, predates this tier) drives a `goose.Provider`
scoped to `internal/analytics/db/migrations`, using **`ApplyVersion`** — never `Up`/`DownTo` —
to roll migration `20260828000001` (the watermark-source CHECK rewrite) back to its pre-state,
assert against it, then re-apply it. The file's own header explains why `ApplyVersion` and not a
directional walk: this package's shared `goose_db_version` table interleaves three modules'
migrations by insertion order, and a directional walk would immediately hit a version this
provider's own analytics-only filesystem doesn't know about. `ApplyVersion` instead targets
exactly one `version_id`, touching no other row.

**The problem this tier introduces:** `TestMain` provisions the WHOLE package via
`testdb.ProvisionDirs`, which applies every migration in this module's directory — including
this tier's own `20260902000002_move_analytics_to_own_schema.sql` — before any test runs. So by
the time `TestMigration_WatermarkSourceVocabulary` calls `ApplyVersion(…, 20260828000001,
false)`, the table already lives in `analytics.vehicle_metric_watermarks`. Migration
`20260828000001`'s own Up/Down SQL references the table **bare** (`ALTER TABLE
vehicle_metric_watermarks …`) — correctly so, per D9/roadmap: historic migration files stay bare
because they normally run **before** the schema move, in chronological order, resolving through
the default `search_path` (`"$user", public`) to `public`. This one file's replay technique
breaks that assumption: it re-runs an old migration's script **after** the schema move, on a
connection whose default `search_path` does not include `analytics`.

**The fix is scoped to this one test connection, not the migration file.** Editing
`20260828000001`'s bare SQL is exactly what D9/the roadmap forbids — that file must stay
resolvable through `public` for its NORMAL (forward, chronological) application, which every
other test in this package (and every real deploy) exercises. Instead,
`newAnalyticsMigrationProvider`'s `sql.Open` DSN gains `search_path=public,analytics`:

```go
db, err := sql.Open("pgx", withSearchPath(testDSN, "public", "analytics"))
```

pgx treats any DSN query parameter it does not itself recognize as a Postgres runtime (startup)
parameter — `postgres://host/db?search_path=myschema,public` is documented pgx behavior, not a
hand-rolled protocol detail (verified via Context7 against `/jackc/pgx`'s own configuration
docs). `public` stays first in the search order so a run against a database that has **not**
applied `20260902000002` yet (impossible in this package today, since `TestMain` always fully
provisions first, but harmless to keep true) still resolves to `public` exactly as it did before
this tier.

**Rejected alternative: edit `20260828000001` to reference `analytics.vehicle_metric_watermarks`
directly.** This is the one thing every other rule in this roadmap forbids doing to a historic
migration, and for good reason here specifically: this file runs both as part of the NORMAL
forward chain (via `ProvisionDirs`'s initial "up", where the table is still in `public` at that
point in file order — schema-qualifying it there would make it fail on every environment) and as
part of THIS test's out-of-order replay (where the table has already moved). A single migration
file cannot correctly reference both states at once; only a connection-scoped `search_path`
resolves both.

**Scope check:** this fix touches one `_test.go` file, inside `internal/analytics/`, adds no new
dependency, and changes no migration file, no production code path, and no other module.

### D-index-plan — index and constraint preservation through `ALTER TABLE … SET SCHEMA`

**Claim: `ALTER TABLE … SET SCHEMA` is a catalog-only operation.** Same PostgreSQL documentation
basis as tier 1 (`SET SCHEMA` is grouped with the other lightweight catalog-only forms, not the
data-rewriting ones). Every object referencing the table by OID — every index, every
PK/UNIQUE/CHECK constraint, every sequence backing `DEFAULT gen_random_uuid()` — continues to
reference the same OID; only the table's `pg_namespace` entry changes.

| Object | Type | Preserved because |
|---|---|---|
| `vehicle_metrics`'s PK | PK on `id` | catalog-only OID reference |
| `vehicle_metrics_account_tesla_date_unique` | UNIQUE on `(account_id, tesla_id, metric_date)` | same |
| `vehicle_metrics_missing_type_iff_flagged` | CHECK | catalog-only; no schema-qualified body |
| `idx_vehicle_metrics_latest` | index on `(account_id, tesla_id, metric_date DESC)` | catalog-only |
| `vehicle_metric_watermarks`'s PK | PK on `id` | catalog-only |
| `vehicle_metric_watermarks_account_tesla_source_unique` | UNIQUE on `(account_id, tesla_id, source)` | catalog-only |
| `vehicle_metric_watermarks_source_check` | CHECK — vocabulary strings, see D-source-values | catalog-only, and the string literals inside it are untouched |
| `charge_gaps`'s PK | PK on `id` | catalog-only |
| `charge_gaps_account_tesla_date_unique` | UNIQUE on `(account_id, tesla_id, gap_date)` | catalog-only |
| `idx_charge_gaps_account` | index on `(account_id, gap_date DESC)` | catalog-only |
| Every row in all three tables | data | untouched — no `INSERT`/`UPDATE`/`DELETE` in this migration |

**No cross-table FK exists** on any of the three tables (no cross-module FK precedent,
`ai/architecture.md` §2, restated in `AGENTS.md`'s Data Ownership section) — so there is no
FK-to-a-moved-table case to reason about, unlike tier 1's `tesla_tokens`/`vehicles` → `accounts`
FKs.

**No new index is added by this tier**, and none is needed: this migration changes zero query
predicates, zero access patterns, and zero row volume. Every existing read path
(`RecentEfficiency`'s `SnapshotsByVehicleSince`-driven derivation isn't touched at all — it never
reads these tables; `ConsumedByDay`/`OdometerDeltaByDay`/`BatteryLevelByDay` via
`vehicle_metrics_account_tesla_date_unique`/`idx_vehicle_metrics_latest`; `LatestMetricsByAccount`
via `idx_vehicle_metrics_latest`; `GapWriter.ReconcileWindow`'s read-before-diff via
`charge_gaps_account_tesla_date_unique`) continues to use exactly the same physical indexes
after the move.

**Lock consideration:** `ALTER TABLE … SET SCHEMA` takes a brief `ACCESS EXCLUSIVE` lock per
table during the catalog update (metadata-only, no page rewrite). This runs once, during a
deploy's migration step, against tables sized in the tens-to-low-thousands of rows scoped to
this project's user base — not a steady-state read path.

**Rejected alternative:** re-creating each table under the new schema with `CREATE TABLE
analytics.X AS SELECT * FROM public.X` plus manually re-adding every constraint and index.
Strictly worse for the same reasons as tier 1: an explicit index/constraint rebuild step, a
window with two copies of the data, and a follow-up `DROP TABLE public.X`.

## Migration filename

Latest migration timestamp across ALL module migration directories at the time of this
change: `internal/account/db/migrations/20260902000001_move_account_to_own_schema.sql` (tier
1's own migration). Analytics's own latest is `20260901000001_add_vehicle_status_columns.sql`.
`20260902000002` collides with neither — confirmed immediately before creating the file by
listing every module's `db/migrations/` directory.

## Test Contract (authored before implementation, per `ai/go-conventions.md` §Testing)

1. **`internal/analytics/db/models.go` after `make sqlc` — byte-for-byte type names.** The
   generated `ChargeGap`, `VehicleMetric`, and `VehicleMetricWatermark` struct names and every
   field name/type MUST be identical to the pre-migration file. Confirmed: `git diff
   internal/analytics/db/models.go` is empty.

2. **Catalog resolution after `make migrate-up`.** Run against the applied database:
   ```sql
   SELECT to_regclass('analytics.vehicle_metrics'), to_regclass('analytics.vehicle_metric_watermarks'),
          to_regclass('analytics.charge_gaps');
   ```
   Expected: all three return their table's OID (non-`NULL`). And:
   ```sql
   SELECT to_regclass('public.vehicle_metrics'), to_regclass('public.vehicle_metric_watermarks'),
          to_regclass('public.charge_gaps');
   ```
   Expected: all three return `NULL`.

3. **`internal/analytics`'s existing test suite — zero assertion changes.** Every existing test
   in `db_integration_test.go`, `db_gap_writer_integration_test.go`, and
   `db_watermark_migration_integration_test.go` MUST continue to pass with the exact same
   expected values it asserts today — this tier changes where the tables physically live, not
   what any port method returns, and not the watermark vocabulary. If any existing assertion
   needs to change to pass, that is a signal this migration did more than namespace —
   investigate before proceeding, do not adjust the test to match.

4. **Row counts before vs. after — manual verification, not a new automated test.** `SELECT
   count(*) FROM analytics.vehicle_metrics`, `analytics.vehicle_metric_watermarks`,
   `analytics.charge_gaps` immediately after migration MUST equal the same three counts taken
   against `public.*` immediately before it.

No new `_test.go` test is required specifically to prove the schema move, for the same reasoning
as tier 1: `internal/testdb` already provisions from this migrations directory, and any
pre-existing test failing to run against the new schema is itself the signal.

## Makefile / tooling re-check (per `CLAUDE.md`'s "reverse direction" docs rule)

The roadmap's own audit (`RM39-schema-per-module.md` §"The Makefile needs no changes") already
found zero `schema`/`public`/`GRANT`/`search_path` occurrences in the `Makefile`. This tier's
own check, scoped to `analytics`, confirms nothing here invalidates that finding:
`MIGRATIONS_DIRS` already includes `internal/analytics/db/migrations`, this migration adds no
new directory, no new guard target, and no new `sqlc.yaml` structural entry (the existing
analytics `sql:` block is edited in place). `internal/testdb`'s `ProvisionDirs` needs no change —
it applies whatever is in `db/migrations/*.sql`, and this tier adds one file there. The one
tooling change this tier makes at all is the test-only `search_path` DSN parameter in
`db_watermark_migration_integration_test.go` (D-watermark-replay), which is not part of the
Makefile or any deploy path — `go test` only.
