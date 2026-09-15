# Design — RM59-analytics-rekey-vehicle-metrics-on-tesla-id

Required because this change touches the database (`openspec/config.yaml` design gate).

## Overview

Two tables, `analytics.vehicle_metrics` and `analytics.vehicle_metric_watermarks`,
move their identity key from `(account_id, tesla_id, ...)` to `(tesla_id, ...)`.
`tesla_id` already uniquely names one vehicle, and a vehicle belongs to exactly one
account at a time (the MAG-63 decision record) — carrying `account_id` too added no
isolation the vehicle id did not already give, only an extra parameter every query and
five port methods had to carry.

Five pieces, in this order: migration, queries + `sqlc generate`, port + implementation,
callers (leader-owned), then docs.

## D1 — Both tables re-key together, in one migration, one transaction

**Decision:** one migration file changes both `vehicle_metrics` and
`vehicle_metric_watermarks`.

**Rationale:** `internal/analytics/recalculate.go`'s `Recalculate` and `Reconcile` are
the sole writer of both tables, and `Reconcile` reads a watermark, derives a window,
then calls `Recalculate`, which writes `vehicle_metrics` — one write path, two tables,
always used together. Splitting the re-key across two migrations would leave a window
where one table has no `account_id` and the other still requires it, and every query in
between would need to handle both shapes. Goose applies a migration file's statements
in one transaction by default, so either the whole re-key applies or none of it does.

## D2 — Migration mirrors the telemetry gold standard exactly

**Decision:** the new migration follows
`internal/telemetry/db/migrations/20260911000002_rekey_vehicle_snapshots_on_tesla_id.sql`'s
shape: a duplicate-collapsing `DELETE` first, then `DROP CONSTRAINT`, `DROP INDEX`
(schema-qualified), `DROP COLUMN account_id`, then `ADD CONSTRAINT` and (where needed)
`CREATE INDEX`, per table.

**Rationale:** this repo already has three precedents for exactly this operation
(telemetry's `vehicle_snapshots`, analytics' own `charge_gaps`, charging's
`supercharger_sessions`). Reusing the same shape means a reader who has seen one
rekey migration can read this one without re-deriving the pattern — the AI-efficiency
argument for a closed, small vocabulary applies to SQL shapes as much as to Go code.

## D3 — Duplicate collapse rule: keep the latest `updated_at`

**Decision:** for both tables, when a collapse is needed, keep the row with the
greatest `updated_at`, ties broken by `id`.

**Rationale:** `updated_at` on `vehicle_metrics` reflects the last time
`Recalculate`'s `UpsertVehicleMetric` touched this row — the write path's own
`ON CONFLICT ... DO UPDATE` already treats "most recently written" as "most correct"
for every column except `created_at`. Collapsing on `updated_at` extends that same
rule one column narrower, from `(tesla_id, metric_date)` scoped by account to
`(tesla_id, metric_date)` alone. `vehicle_metric_watermarks.updated_at` reflects the
last time `Reconcile`'s `UpsertVehicleMetricWatermark` advanced this cursor — the
furthest-advanced cursor is the one that will not cause redundant re-derivation, so
keeping the most recently updated row is also the safer choice there.

**Why this is defensive, not expected:** measured on the dev database 2026-09-14:
`vehicle_metrics` has 120 rows, 0 duplicate `(tesla_id, metric_date)` groups, 1
account, 2 distinct `tesla_id`; `vehicle_metric_watermarks` has 4 rows, 0 duplicate
`(tesla_id, source)` groups. A real database is expected to have **zero** rows
deleted by either collapse step. It exists so the migration is safe on a database
that was migrated before this Go change deploys — the same reasoning MAG-65's and the
charge_gaps rekey's own collapse steps recorded.

## D4 — No `SET NOT NULL`, no `DROP INDEX idx_vehicle_metrics_vehicle_date`

**Decision:** the migration does not touch `tesla_id`'s nullability on either table,
and does not drop an index named `idx_vehicle_metrics_vehicle_date`.

**Rationale:** `tesla_id` is already `NOT NULL` on both tables (verified against the
live DDL, not assumed from the ticket). `idx_vehicle_metrics_vehicle_date` does not
exist — `pg_indexes` lists only `idx_vehicle_metrics_latest`, the two unique indexes,
and the two primary keys. The ticket's text asks for both; both are wrong (see
`proposal.md`'s correction section and the roadmap file). Writing either into the
migration would either error (`DROP INDEX` on a name that does not exist, without
`IF EXISTS`) or be a silent no-op (`SET NOT NULL` on an already-`NOT NULL` column) —
neither belongs in a migration meant to be read later as a true record of what
changed.

## D5 — `idx_vehicle_metrics_latest` is recreated, not dropped

**Decision:** `idx_vehicle_metrics_latest` is dropped (it references `account_id`,
which is being removed from the table) and immediately recreated under the **same
name**, now `(tesla_id, metric_date DESC)`.

**Rationale — this is the roadmap's own required justification, restated with the
query it serves:** `LatestVehicleMetricsByVehicles` (`query.sql`, renamed from
`LatestVehicleMetricsByAccount`) is:

```sql
SELECT DISTINCT ON (tesla_id) ...
FROM analytics.vehicle_metrics
WHERE tesla_id = ANY(@tesla_ids::bigint[])
ORDER BY tesla_id, metric_date DESC;
```

The new `UNIQUE (tesla_id, metric_date)` constraint's own index is all-ascending. This
query orders `tesla_id ASC, metric_date DESC` — a **mixed** direction. An all-ascending
index cannot produce that order in one scan; Postgres would need an incremental sort
per `tesla_id` group. `idx_vehicle_metrics_latest`'s whole reason to exist (added at
the original `RM38-analytics-add-vehicle-status-columns` design gate) was to eliminate
exactly that incremental sort. Dropping it and relying on the new UNIQUE index alone
would reintroduce the sort it was built to remove — a regression on this project's
mandatory-fast read path (four gateway call sites: the dashboard vehicle cards, the
single-vehicle dashboard, the nav header, and the external-charges suggestion).

**Why the same name:** no consumer references the index by name (SQL never does), so
renaming would buy nothing and would cost a reader checking "is this the same index as
before" one more diff to read.

**`= ANY(@tesla_ids::bigint[])` is still served by this index**, not only a single
`tesla_id = $1` equality: for a small array on a leading indexed column, Postgres
executes a `ScalarArrayOpExpr` index scan that visits the index once per array
element in index order, which `DISTINCT ON (tesla_id)` can then consume without an
extra sort node — the same mechanism that already served the pre-existing
`account_id = $1` equality case, generalized to a handful of ids instead of one.

## D6 — The three chart-read queries: no index change

**Decision:** `VehicleMetricsConsumedByVehicleBetween`, `VehicleMetricsOdometerByVehicleBetween`,
`VehicleMetricsBatteryByVehicleBetween` need no new index.

**Rationale — the roadmap's second required confirmation:** all three are, and remain:

```sql
WHERE tesla_id = @tesla_id AND metric_date BETWEEN @start_date AND @end_date
[AND <calc column> IS NOT NULL]
ORDER BY metric_date
```

Before this change they were served by `vehicle_metrics_account_tesla_date_unique`
`(account_id, tesla_id, metric_date)` with `account_id` pinned by equality, `tesla_id`
pinned by equality, `metric_date` range-scanned in ascending order — no sort needed
(`ORDER BY metric_date` ascending matches the index's own ascending order). After this
change they are served by the new `vehicle_metrics_tesla_date_unique`
`(tesla_id, metric_date)`: `tesla_id` pinned by equality, `metric_date` range-scanned
ascending — the identical access shape, one column narrower. The `IS NOT NULL` clause
on two of the three remains a residual predicate evaluated against the already-tiny
(≤ 90-row, per `historyRangeMaxDays`) range-scanned result, exactly as it was before.

`UpsertVehicleMetric`'s `ON CONFLICT` target and `DeleteVehicleMetricsInRangeExcept`'s
`WHERE` are served by the same new unique index, one column narrower than before, for
the identical reason.

## D7 — `vehicle_metric_watermarks`: no second index

**Decision:** `vehicle_metric_watermarks_tesla_source_unique` `(tesla_id, source)` is
the table's only index after this change, exactly as
`vehicle_metric_watermarks_account_tesla_source_unique` was its only index before.

**Rationale:** `GetVehicleMetricWatermark`'s `WHERE tesla_id = $1 AND source = $2` and
`UpsertVehicleMetricWatermark`'s `ON CONFLICT` target are both single-row lookups on
exactly this column pair — a point lookup, served completely by the unique index. No
other query touches this table.

## D8 — How to verify these index claims (the table is too small for a bare `EXPLAIN`)

`analytics.vehicle_metrics` has 120 rows. A bare `EXPLAIN` on a table this size
returns `Seq Scan` regardless of which indexes exist, because the planner correctly
judges a sequential scan of 120 rows cheaper than an index lookup — that check can
never pass and proves nothing. The correct verification is:

```sql
SET enable_seqscan = off;
EXPLAIN SELECT ... -- the query under test
```

This forces the planner to use an index if one can serve the query at all, proving
the index *can* serve it — which is what these decisions claim, not that the planner
*chooses* to on a table this small. `tasks.md`'s verification task uses this form.
This is a read-only session setting plus a read-only `EXPLAIN` — no DDL, no data
change.

## Migration SQL

New file:
`internal/analytics/db/migrations/20260914000001_rekey_vehicle_metrics_on_tesla_id.sql`
(highest version in the `internal/analytics/db/migrations/` folder; see "Migration
order check" below).

```sql
-- +goose Up
-- Collapse any (tesla_id, metric_date) duplicates before the new UNIQUE
-- constraint can be added. Keeps the row with the latest updated_at, ties
-- broken by id -- the write path's own UPSERT already treats "most recently
-- written" as "most correct" for every column but created_at; this applies
-- that same rule one column narrower. Measured on the dev database
-- (2026-09-14): 0 duplicate groups exist today. This step exists so the
-- migration is still safe against a database migrated before this Go change
-- deploys.
DELETE FROM analytics.vehicle_metrics a
USING analytics.vehicle_metrics b
WHERE a.tesla_id = b.tesla_id
  AND a.metric_date = b.metric_date
  AND (a.updated_at < b.updated_at
       OR (a.updated_at = b.updated_at AND a.id < b.id));

ALTER TABLE analytics.vehicle_metrics
    DROP CONSTRAINT vehicle_metrics_account_tesla_date_unique;

-- Schema-qualified: an index name is resolved through search_path, and goose
-- does not guarantee analytics is on it.
DROP INDEX analytics.idx_vehicle_metrics_latest;

ALTER TABLE analytics.vehicle_metrics
    DROP COLUMN account_id;

ALTER TABLE analytics.vehicle_metrics
    ADD CONSTRAINT vehicle_metrics_tesla_date_unique UNIQUE (tesla_id, metric_date);

-- Recreated under the SAME name, one column narrower (design.md D5): the
-- query it serves orders tesla_id ASC, metric_date DESC -- a mixed direction
-- the new all-ascending UNIQUE index cannot produce in one scan. Dropping
-- this index without recreating it would reintroduce the incremental sort it
-- exists to avoid.
CREATE INDEX idx_vehicle_metrics_latest
    ON analytics.vehicle_metrics (tesla_id, metric_date DESC);

-- Same collapse rule as above, one table over: source_updated_at is a
-- cursor Reconcile only ever advances forward, so the most recently
-- advanced (updated_at) row is also the correct one to keep.
DELETE FROM analytics.vehicle_metric_watermarks a
USING analytics.vehicle_metric_watermarks b
WHERE a.tesla_id = b.tesla_id
  AND a.source = b.source
  AND (a.updated_at < b.updated_at
       OR (a.updated_at = b.updated_at AND a.id < b.id));

ALTER TABLE analytics.vehicle_metric_watermarks
    DROP CONSTRAINT vehicle_metric_watermarks_account_tesla_source_unique;

ALTER TABLE analytics.vehicle_metric_watermarks
    DROP COLUMN account_id;

ALTER TABLE analytics.vehicle_metric_watermarks
    ADD CONSTRAINT vehicle_metric_watermarks_tesla_source_unique UNIQUE (tesla_id, source);

-- +goose Down
-- NOT a full rollback of history: rows deleted by the Up migration's collapse
-- steps are not recoverable, and every account_id value dropped by DROP
-- COLUMN is gone. Down only reverses this migration's own schema changes,
-- restoring shape, not data -- the same limitation every DROP COLUMN in this
-- codebase's migrations has on Down (mirrors the telemetry and charge_gaps
-- rekey precedents).
ALTER TABLE analytics.vehicle_metric_watermarks
    DROP CONSTRAINT vehicle_metric_watermarks_tesla_source_unique;

ALTER TABLE analytics.vehicle_metric_watermarks
    ADD COLUMN account_id UUID;

ALTER TABLE analytics.vehicle_metric_watermarks
    ADD CONSTRAINT vehicle_metric_watermarks_account_tesla_source_unique
    UNIQUE (account_id, tesla_id, source);

DROP INDEX analytics.idx_vehicle_metrics_latest;

ALTER TABLE analytics.vehicle_metrics
    DROP CONSTRAINT vehicle_metrics_tesla_date_unique;

ALTER TABLE analytics.vehicle_metrics
    ADD COLUMN account_id UUID;

CREATE INDEX idx_vehicle_metrics_latest
    ON analytics.vehicle_metrics (account_id, tesla_id, metric_date DESC);

ALTER TABLE analytics.vehicle_metrics
    ADD CONSTRAINT vehicle_metrics_account_tesla_date_unique
    UNIQUE (account_id, tesla_id, metric_date);
```

`account_id` comes back **nullable**, not `NOT NULL`, in both `Down` blocks: the `Up`
migration's `DROP COLUMN` threw the values away, so there is nothing to backfill a
`NOT NULL` constraint with on a populated table — the same limitation every
`DROP COLUMN` in this codebase's migrations has on `Down`.

The tables' `COMMENT ON TABLE` / `COMMENT ON COLUMN` text lives entirely in the
**original** migration files (`20260821000001_add_vehicle_metrics.sql`,
`20260821000002_add_vehicle_metric_watermarks.sql`) and is not touched here — those
files are never edited (`ai/go-conventions.md`'s "historic migrations are never
edited" rule), and a stale table comment is never, on its own, a finding worth a
migration.

## Queries (`internal/analytics/db/query.sql`)

- `UpsertVehicleMetric`: drop `account_id` from the column list, the `VALUES` list,
  and the `ON CONFLICT` target (becomes `(tesla_id, metric_date)`).
- `DeleteVehicleMetricsInRangeExcept`: drop `account_id` from the `WHERE` clause.
- `VehicleMetricsConsumedByVehicleBetween`, `VehicleMetricsOdometerByVehicleBetween`,
  `VehicleMetricsBatteryByVehicleBetween`: drop `account_id` from the `WHERE` clause
  (the MAG-71 queries, folded in per roadmap RD2).
- `GetVehicleMetricWatermark`, `UpsertVehicleMetricWatermark`: drop `account_id` from
  the predicate / column list / `ON CONFLICT` target.
- `LatestVehicleMetricsByAccount` → `LatestVehicleMetricsByVehicles`: replace
  `WHERE account_id = @account_id` with `WHERE tesla_id = ANY(@tesla_ids::bigint[])`.
  The `SELECT` list, `DISTINCT ON (tesla_id)`, and `ORDER BY tesla_id, metric_date DESC`
  are unchanged.

Several of these queries' doc comments currently cite the constraint names being
replaced (`vehicle_metrics_account_tesla_date_unique`) or an index plan by name
("Index Plan, read pattern #1/#3/#4") from the now-archived, frozen tier-3/tier-38/
tier-40 design docs. Updating each comment is part of this change — replace the
citation with the reason itself (this file's D5/D6/D7 give the substance), never with
a new citation to this change's own name or ID (`ai/go-conventions.md`'s code-comment
rule).

Then run `sqlc generate` (`make sqlc`) so `internal/analytics/db` regenerates every
affected `*Params`/`*Row` type without an `AccountID` field, and
`LatestVehicleMetricsByVehiclesParams`/`Row` in place of the `...ByAccount` names.

## Ports and implementation (`internal/analytics/analytics.go`, `reader.go`, `recalculate.go`, `consumed.go`, `mapping.go`)

### D9 — `LatestMetricsForVehicles` takes `[]vehicleref.Ref`, not `[]int64` (roadmap RD4, not reopened)

`LatestMetricsByAccount(ctx, accountID uuid.UUID)` becomes
`LatestMetricsForVehicles(ctx context.Context, refs []vehicleref.Ref)`. This is the
one port whose `WHERE account_id = ...` filter disappears entirely — a plain
`[]int64` would move the "these are this account's own vehicles" guarantee from SQL
into whatever Go code builds the slice, where a future bug could leak one account's
vehicle status into another's response. A `vehicleref.Ref` can only be constructed by
`vehicleref.Authorize`, `vehicleref.All`, or a `_test.go` file
(`make vehicleref-guard` enforces this repo-wide), and in production those are only
ever called from the gateway's own `authorizeVehicle`/`ownedVehicles` helpers, both of
which start from `account.RegisteredVehicles`. The compiler enforces what the SQL
filter used to: a caller with no authorized `Ref` has nothing to pass.

**This adds `internal/analytics`'s first-ever import of `internal/vehicleref`.**
Checked against `make boundary-guard`: the guard only forbids `internal/telemetry`
imports inside `internal/gateway` — it says nothing about `internal/vehicleref`, and
`vehicleref` itself imports nothing project-local (its own package doc: "imports
NOTHING project-local"). So `analytics → vehicleref` adds no cycle and trips no guard.
Dependency direction stays one-way (`ai/architecture.md` §2): `vehicleref` sits below
every domain module, exactly where `clock` already sits.

**Implementation shape** (`reader.go`): the internal `vehicleMetricsStore` interface's
method is renamed to match the regenerated sqlc method,
`LatestVehicleMetricsByVehicles(ctx, teslaIDs []int64)` — this is the layer *below*
`vehicleref`, so it still deals in plain `int64`, matching every other `analyticsdb`
method. `reader.LatestMetricsForVehicles` unwraps the given `refs` via
`vehicleref.TeslaIDs(refs)` (the existing, exported inverse of `vehicleref.All` — built
for exactly this "build a SQL `= ANY($1)` parameter" case) before calling into the
store. An empty or nil `refs` yields an empty, non-nil `teslaIDs` slice, which
`= ANY('{}'::bigint[])` correctly matches zero rows — an empty result, not an error,
matching the port's existing "no vehicles → empty slice" contract.

### D10 — `ConsumedByDay`, `OdometerDeltaByDay`, `BatteryLevelByDay`, `Recalculate`, `Reconcile` drop `accountID`; `RecentEfficiency` is untouched

These five simply lose their `accountID uuid.UUID` parameter — `teslaID int64` is
unchanged (roadmap RD5, not reopened here: `internal/app/processor.go` calls
`ConsumedByDay` from a background job with no signed-in user and cannot build a
`vehicleref.Ref`, and its two chart siblings stay on `int64` so `buildHistoryView`
calls all three side by side with a uniform shape). `RecentEfficiency` keeps
`accountID` because it still calls `account.RegisteredVehicles(ctx, accountID)` via
`carTypeFor` — a real, ongoing dependency this ticket does not touch.

**`vehicleMetricRow.AccountID` (`consumed.go`) is deleted, not left unused.** It exists
today only to flow into `analyticsdb.UpsertVehicleMetricParams.AccountID`
(`upsertVehicleMetricParamsFrom`, `recalculate.go`), which no longer has that field
once `UpsertVehicleMetric` drops `account_id`. `Recalculate`'s
`for i := range rows { rows[i].AccountID = accountID }` loop (recalculate.go:173) is
deleted along with it — there is no longer an `accountID` parameter to assign from.

**Doc comments on all five methods currently describe account-scoping as a security
property** ("scoped to the given account", "never a different account's vehicle").
Each is rewritten to describe scoping by vehicle identity instead — the underlying
guarantee (a caller cannot reach another vehicle's rows by mistake) is unchanged, it
is now enforced by the query filtering on `tesla_id` alone rather than
`account_id AND tesla_id` together, because `tesla_id` alone already names one
vehicle uniquely.

## Leader-owned integration

### D11 — `internal/app/processor.go`: dedupe by `tesla_id` (roadmap RD3, not reopened)

`recalculateAnalytics`'s loop currently runs `p.recalculator.Reconcile(ctx, v.AccountID, v.TeslaID)`
and `p.analyticsReader.ConsumedByDay(ctx, v.AccountID, v.TeslaID, start, end)` once per
element of `p.acct.AllRegisteredVehicles(ctx)` — one row per `(account, vehicle)` pair,
with no election applied. Once `Reconcile`/`ConsumedByDay` are keyed on the car alone,
a car registered to two accounts would run the *identical* `Reconcile` call twice in
one cycle — wasted work, not a correctness bug (both calls derive the same rows from
the same sources), but wasted work this project's read-heavy Performance-Profile gives
no license to accept on a step that already runs nightly for every vehicle.

**Fix:** before the loop, build the distinct set of `tesla_id`s from
`AllRegisteredVehicles`'s result, mirroring `processChargingData`'s own existing
`slices.Contains` dedupe idiom in the same file (`internal/app/processor.go`, a few
lines above `recalculateAnalytics`). Loop over that distinct set instead of over
`vehicles` directly. `Reconcile(ctx, teslaID)` and `ConsumedByDay(ctx, teslaID, start,
end)` drop `v.AccountID`. `ChargeGap{TeslaID: ..., VIN: ..., ...}` literals are
unaffected — `ChargeGap` carries no `AccountID` field today (the charge_gaps rekey
already removed it) and `VIN` still needs to come from one representative
`account.OwnedVehicle` per `tesla_id` (any one — VIN does not vary across accounts for
the same car).

### D12 — Gateway: stopgap only, full rewrite is tier 2

This tier's mandate (per the dispatch and the roadmap's own tier split) is
**"only as far as needed to keep `go build ./...` green"** — production code only, no
test-file fixups, and no new design decision about *how* the gateway should prove
ownership. That design (using the existing `ownedVehicles` helper correctly, handling
its `ok=false` path, and fixing `handlers_test.go`/`history_test.go`/
`external_charges_test.go`) is `RM59-gateway-authorize-metrics-reads`'s own job, with
its own design.md.

**What changes here, file by file:**

- `internal/gateway/handlers/history.go`'s `buildHistoryView`: the three chart calls
  (`BatteryLevelByDay`, `OdometerDeltaByDay`, `ConsumedByDay`) drop their `uid`
  argument — a mechanical one-argument removal, no behavior change (RD5: `teslaID`
  was always the real scoping argument on these three; `uid` is simply gone from the
  call, not replaced by anything).
- `internal/gateway/handlers/handlers.go`'s three `LatestMetricsByAccount(ctx, uid)`
  call sites (`vehiclesFor`, `dashboardFor`, `navHeaderFor`): each already resolves
  `registered []account.Vehicle` via `h.acct.RegisteredVehicles(ctx, uid)` immediately
  before the analytics call, for reasons unrelated to this port (building the vehicle
  card list, picking the primary vehicle). Build `refs := vehicleref.All(teslaIDsOf(registered))`
  inline from that already-resolved slice — no second account read — and call
  `h.analyticsReader.LatestMetricsForVehicles(ctx, refs)`. This is guard-compliant:
  `make vehicleref-guard` exempts every line of `internal/gateway/handlers/handlers.go`
  itself, not only `authorizeVehicle`.
- `internal/gateway/handlers/external_charges.go`'s one call site
  (`buildExternalChargesPage`, guarded by `teslaIDFilter != 0 && h.analyticsReader != nil`):
  this file is **not** exempt from `make vehicleref-guard` — only `handlers.go` is — so
  a literal `vehicleref.All(...)` call here would fail the guard. Use the existing
  `h.ownedVehicles(ctx, uid)` helper instead (already called elsewhere in this same
  file, at the `buildExternalChargesForm` neighbor function), which returns
  `[]vehicleref.Ref` without this file ever naming `vehicleref.All` itself. Pass its
  `refs` to `LatestMetricsForVehicles`; keep the existing "loop the returned statuses,
  match `s.TeslaID == teslaIDFilter`" filtering unchanged.
- **Expected residual state after this stopgap:** `go build ./...` is green.
  `go vet ./internal/gateway/...` is **expected to still fail** — the package's own
  test files (`handlers_test.go`, `history_test.go`, `external_charges_test.go`)
  implement fakes against the *old* `LatestMetricsByAccount`/four-argument chart
  signatures and will not compile until tier 2 updates them. This mirrors this
  project's own precedent (`RM57-charging-rekey-supercharger-sessions-on-tesla-id`'s
  T8, which left its own tier's gateway stopgap short of `go vet`-clean for the same
  reason) — it is expected, not a regression to chase down in this tier.

## Test contract (authored before the implementation, per this project's testing convention)

Unit tests are excluded; this is the contract the **existing** integration tests must
still satisfy once updated to the new key shape — not new scenarios.

**Given** two `vehicle_metrics` rows both with `tesla_id = 778899`,
`metric_date = 2026-08-01`:
- Row X: `account_id = <acct-old>`, `updated_at = 2026-08-02T03:30:00Z`
- Row Y: `account_id = <acct-new>`, `updated_at = 2026-08-05T03:30:00Z`

**When** the migration's `DELETE` step runs.

**Then** Row X is deleted (earlier `updated_at`); Row Y survives with every column
(including `battery_level_pct`, `odometer_km`, and every nullable `_calc` column)
byte-identical to before the migration except `account_id`, which is gone. After the
migration, `SELECT COUNT(*) FROM analytics.vehicle_metrics WHERE tesla_id = 778899 AND
metric_date = '2026-08-01'` returns exactly 1.

**Given** a database with no duplicate `(tesla_id, metric_date)` or `(tesla_id,
source)` pairs (the expected, verified real-world state).

**When** the migration runs.

**Then** both `DELETE` statements affect 0 rows, and every existing row's `id`,
`tesla_id`, every data column, `created_at`, and `updated_at` are byte-identical to
before the migration — only `account_id` is gone and the constraint/index names
changed.

**Given** a vehicle with a `vehicle_metrics` row for `2026-08-10` and no watermark row
for `sourceVehicleSnapshots`.

**When** `Reconcile(ctx, teslaID)` is called (post-migration signature, no `accountID`).

**Then** it backfills from the epoch exactly as it did before this change — the
watermark's own "no row = epoch" contract (design D7 of the tier-3 change) is
unaffected by the key change, since it was already keyed on `tesla_id` for
`WHERE`-clause purposes before this change; only the constraint's column set narrows.

`Recalculate`/`Reconcile`'s own behavioral contract (UPSERT-and-delete-stale
semantics, lookback widening, watermark advancement) is unchanged by this change —
only the identity key each operates against narrows from `(account_id, tesla_id)` to
`tesla_id` alone. The existing scenarios in `internal/analytics/db_integration_test.go`
already cover that contract; the test-fixup plan below lists which of them need
updating to the new key shape, not new scenarios.

## Existing test fixups (`internal/analytics`, this worker's sandbox)

**D-TESTFIX (binding, from the ticket and the project's own testing convention):**
unit tests are excluded. No new test file, and no new test case testing new
behavior, is written. But `go vet ./...` compiles every `_test.go` file, and this
change is not `done` until that stays clean — every existing test that references a
changed signature, a changed field, or a changed column must be updated to compile
and to keep asserting what it already asserted, using the new shape. This is keeping
the build green, not writing tests.

Two files carry this module's own test surface for the changed ports:

### `internal/analytics/reader_test.go` (offline, fakes only)

- `TestRecentEfficiency_AccountIDScoping_PassedToEveryPort` (line 461) — **unchanged**.
  `RecentEfficiency` keeps `accountID` (D10); this test is out of scope.
- `TestReader_ConsumedByDay_ReadsPrecomputedRows`,
  `TestReader_OdometerDeltaByDay_ClampsOnRead`,
  `TestReader_ConsumedByDay_ExcludesPredecessorlessRow`,
  `TestReader_OdometerDeltaByDay_ExcludesPredecessorlessRow`,
  `TestReader_VehicleMetricsStoreError_Propagates` (lines 606–768): each calls
  `r.ConsumedByDay(ctx, accountID, teslaID, ...)` / `r.OdometerDeltaByDay(...)` — drop
  the `accountID` argument at every call site.
- The fake implementing the `vehicleMetricsStore` interface in this file: its
  `VehicleMetricsConsumedByVehicleBetweenParams`/`...OdometerByVehicleBetweenParams`/
  `...BatteryByVehicleBetweenParams` literals drop `AccountID:`; any assertion
  comparing `gotConsumedParams.AccountID` is deleted (there is no longer an
  `AccountID` field to compare); the fake's `LatestVehicleMetricsByAccount` method is
  renamed to `LatestVehicleMetricsByVehicles(ctx, teslaIDs []int64)` and its recorded
  `gotLatestAccountID uuid.UUID` field becomes `gotLatestTeslaIDs []int64`.

### `internal/analytics/db_integration_test.go` (DB-backed, this table's own suite — the largest surface)

This file is `vehicle_metrics`'/`vehicle_metric_watermarks`' own integration test, and
it is far more coupled to `account_id` than a one-line signature fixup: every helper
that seeds or reads these two tables takes `accountID uuid.UUID` as a keying
parameter, and every `Recalculate`/`Reconcile`/`LatestMetricsByAccount`/
`ConsumedByDay`/`OdometerDeltaByDay`/`BatteryLevelByDay` call passes one.

- **Helpers** (`cleanupVehicleMetrics`, `fetchVehicleMetric`,
  `seedPreMigrationVehicleMetric`, `fetchWatermark` and its siblings, `seedManualEntry`
  where it takes `accountID` only to build a fixture argument that flows into a
  `Recalculate` call): drop the `accountID uuid.UUID` parameter and the
  `account_id = $N` predicate from any raw SQL, keyed on `tesla_id` (+ `metric_date` /
  `+ source` where the original already filtered on it) alone.
- **Every `Recalculate(ctx, accountID, teslaID, ...)` / `Reconcile(ctx, accountID,
  teslaID)` call**: drop the `accountID` argument.
- **`fixtureAPair`/`fixtureBPair`/etc. fixture builders** that currently take or embed
  an `accountID`: drop it if it exists solely to be threaded through to a call above;
  keep it if the fixture also seeds a `telemetry.Snapshot`/`charging.Session` row that
  still legitimately carries its own `account_id` (those tables are untouched by this
  change).
- **`TestRecalculate_AccountIDScoping_PassedToEveryPort`** (line 186): its own doc
  comment already explains it now asserts `teslaID` scoping to the three source ports,
  not `accountID` scoping (that scoping moved to a comment noting "the account still
  scopes the rows Recalculate writes" — that comment is now wrong, since after this
  change nothing scopes the written rows by account at all; `tesla_id` alone does).
  Rename to `TestRecalculate_TeslaIDScoping_PassedToEveryPort`, drop the `accountID`
  variable and argument, and rewrite the doc comment's now-false closing sentence.
- **`TestReader_LatestMetricsByAccount_*`** (lines 2336, 2407, 2480, 2568, 2675 — five
  tests): rename to `TestReader_LatestMetricsForVehicles_*`, replace each
  `r.LatestMetricsByAccount(ctx, accountID)` call with
  `r.LatestMetricsForVehicles(ctx, vehicleref.All([]int64{teslaID, ...}))` (a test file
  is exempt from `make vehicleref-guard`, so calling `vehicleref.All` directly here is
  correct, not a violation).
  `TestReader_LatestMetricsByAccount_EmptyAccountReturnsEmptyNonNilSlice` becomes
  `TestReader_LatestMetricsForVehicles_EmptyVehicleSetReturnsEmptyNonNilSlice`, asserted
  against `vehicleref.All(nil)` (or an empty `[]vehicleref.Ref`) rather than an
  account with no vehicles — the empty-input case, not an account-lookup case.
- **`fetchVehicleMetric`'s `SELECT`/`Scan` list**: drop `account_id`/`&m.AccountID`
  from both the SQL and the `Scan(...)` argument list — `analyticsdb.VehicleMetric`
  (the sqlc-generated scan target) no longer has an `AccountID` field once the column
  is dropped, so leaving `&m.AccountID` in the `Scan` call would not compile.

**No sub-case in this file tests cross-account isolation for `vehicle_metrics` /
`vehicle_metric_watermarks` the way the charge_gaps rekey's
`TestGapWriter_ReconcileWindow_RejectsMisScopedFlaggedEntry_WritesNothing` did** — a
grep for "TenantIsolation" / "CrossAccount" against this file's test names found none;
if an implementer's own grep sweep (`tasks.md`'s verification task) finds one this
design missed, treat it as a design gap to escalate, not a task to silently expand,
mirroring the charge_gaps precedent's own T7 caution.

## Migration order check (verified, not assumed — the roadmap's own required record)

`MIGRATIONS_DIRS` applies whole module folders in order: account, telemetry, charging,
analytics. `internal/analytics/db/migrations/20260908000002_add_tpms_pressure_columns.sql:82`
backfills by joining `vehicle_metrics` to `telemetry.vehicle_snapshots` with
`WHERE vm.account_id = vs.account_id`. This new migration's version
(`20260914000001`) is the highest in the `analytics` folder, so it runs strictly
*after* that backfill both under today's per-folder ordering and under the global
version-order MAG-76 (still in Backlog) would introduce — the backfill still sees
`vm.account_id` at the moment it runs, in both orderings. This tier therefore does not
depend on MAG-76, and does not make it harder. Verified by reading the migration file
directly, not assumed from the roadmap's own already-checked note (which covers the
*telemetry* side of this same join; this file's own `analytics`-side check is recorded
here for completeness).

## `make` targets and guards — checked, per `CLAUDE.md`'s reverse-direction rule

- **`db-setup`/`db-reset` role and ownership assumptions**: unaffected. This migration
  adds no new schema, no new role, and runs as the existing `analytics` schema owner
  (the app role), exactly like every prior migration in this folder.
- **`MIGRATIONS_DIRS` order**: unaffected — see "Migration order check" above.
- **`make migration-guard`**: unaffected. This migration introduces no cross-module
  schema reference (it reads and writes only its own two tables).
- **`make boundary-guard`**: unaffected — see D9. No `internal/telemetry` import is
  added anywhere, and the guard does not concern itself with `internal/vehicleref`.
- **`make vehicleref-guard`**: checked — see D12. The gateway stopgap adds exactly one
  `vehicleref.All(...)` call site, inside `internal/gateway/handlers/handlers.go`
  (guard-exempt), and reuses the existing `h.ownedVehicles` helper (itself already
  guard-compliant) from `external_charges.go`. No new call site outside those two
  files.
- **`sqlc`**: one `sql:` entry already exists for `analytics` in the root `sqlc.yaml`;
  this change adds no table and needs no new entry, only a regeneration
  (`make sqlc`) after the query file changes.

## Docs

- `internal/analytics/AGENTS.md`: the "Public interface (the port)" table's
  `LatestMetricsByAccount` row is renamed to `LatestMetricsForVehicles`, with its
  "Returns" cell updated to describe a vehicle-set input rather than an account
  input. The "Allowed / forbidden imports" section gains `internal/vehicleref`
  under "May import", with the one-line reason from D9. No other section of this
  file names `account_id` on either table (column detail is deferred to the KB
  guide), so no other edit is expected here — verify with a grep rather than
  assuming.
- `kkpa/context/entities/vehicle-metrics/guide.md`: update the `vehicle_metrics` and
  `vehicle_metric_watermarks` column lists — remove `account_id UUID NOT NULL` from
  both, rename `vehicle_metrics_account_tesla_date_unique` /
  `vehicle_metric_watermarks_account_tesla_source_unique` to
  `vehicle_metrics_tesla_date_unique` / `vehicle_metric_watermarks_tesla_source_unique`
  with their new column sets, and update `idx_vehicle_metrics_latest`'s documented
  column list to `(tesla_id, metric_date DESC)`. This guide is fetched by
  `kkpa-context-fetch` as authoritative — a stale column list here is worse than no
  guide at all.
- This change's own `specs/analytics/spec.md` delta (below) is synced into
  `openspec/specs/analytics/spec.md` at archive time via the normal OpenSpec sync
  step. **`openspec/specs/analytics/spec.md`'s "Module-Scoped Database Schema"
  requirement is deliberately NOT touched by this change**, mirroring the charge_gaps
  rekey's own precedent: its "Existing metrics, watermarks, and gap rows..." scenario
  names the OLD constraint names (`UNIQUE (account_id, tesla_id, metric_date)` etc.)
  as a **historical snapshot** of what the 2026-09-02 schema-move migration preserved
  — a true claim about that migration, not a standing claim about the tables' current
  shape. Do not edit that requirement.

## Risks

- **Migration order matters only within this file** — each table's collapse `DELETE`
  must run before its own `DROP CONSTRAINT`/`ADD CONSTRAINT` pair, which this
  migration's own statement order already guarantees (goose runs a migration file's
  statements in the order written).
- **No cross-module migration ordering concern beyond the one already checked** — this
  migration's own writes touch only `analytics.vehicle_metrics` and
  `analytics.vehicle_metric_watermarks`; only the pre-existing tpms backfill *reads*
  `vehicle_metrics` cross-table, and that reads `telemetry`, not the other way — see
  "Migration order check" above.
- **The gateway stopgap (D12) leaves `go vet ./internal/gateway/...` red until tier 2
  lands.** This is expected and recorded, not silently accepted — `go build ./...`
  is the bar this tier must clear, per the dispatch's own scope.
