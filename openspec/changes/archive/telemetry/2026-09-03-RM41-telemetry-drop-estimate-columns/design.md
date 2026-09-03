# Design — RM41-telemetry-drop-estimate-columns

## Context

`RM41-supercharger-battery-pct-cleanup` (the roadmap, read in full before this
design) closes out MAG-36. Tier 1 (`RM41-gateway-revise-battery-pct-ui`, archived)
removed every gateway read of either `*Est` field. Tier 2
(`RM41-charging-drop-estimate-columns`, archived) dropped
`start_battery_pct_est`/`end_battery_pct_est` from `charging.supercharger_sessions`.
This tier, tier 3, drops the **same two columns from telemetry's own, independent
copy** — `telemetry.supercharger_history` — along with every remaining Go,
SQL-comment, `AGENTS.md`, and spec reference to them inside this module's boundary,
plus two small explicitly-granted cross-module corrections.

Telemetry's copy has been permanently `NULL` since `RM27-telemetry-add-supercharger-battery-pct`
shipped it (MAG-14, 2026-08-15): `UpsertSuperchargerHistory`'s INSERT/ON CONFLICT
clauses exclude it, and no Writer for either column has ever existed anywhere in this
repository (the future verification UI that eventually shipped a write path,
`SessionVerifier.VerifySession`, was always `charging`'s, per RM31 — never
telemetry's). The pair was kept rather than dropped specifically "so that a future
estimator can land without a migration." That estimator shipped 2026-09-01 as
`derivedStartBatteryPct`, writing the real `start_battery_pct` column instead of a
frozen snapshot — so the reservation is now permanently obsolete (roadmap D1/D8).

## Goals / Non-Goals

**Goals:**
- Drop `start_battery_pct_est`/`end_battery_pct_est` from
  `telemetry.supercharger_history` in one migration (roadmap D1, D2).
- Remove `SuperchargerHistory.StartBatteryPctEst`/`EndBatteryPctEst`, the RM27-D6
  reservation comment block that describes them, and their `rowToSuperchargerHistory`
  mappings from `internal/telemetry/` (roadmap D8).
- Rewrite the `db/query.sql` guarding comment and the `service.go` comment that name
  the columns as excluded/not-read — once the columns don't exist, "excluded" no
  longer describes anything real.
- Repair `internal/telemetry/db_supercharger_battery_pct_integration_test.go` and the
  granted `internal/charging/db_backfill_integration_test.go` path so both compile
  and (where they still assert against these columns) assert the new shape (roadmap
  D7) — see Test Contract below for every edit, authored before implementation.
- Correct `internal/telemetry/AGENTS.md`, plus two small explicitly-granted
  corrections outside this module: one forward-reference sentence in
  `internal/charging/AGENTS.md` (left open by tier 2 for this tier to close) and one
  comment-only correction inside the historic migration
  `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql`, which
  explicitly names this change as the one responsible for updating it (see
  proposal.md "Findings correction").
- Update `openspec/specs/telemetry/spec.md`'s two Requirements that describe the
  frozen estimate pair as current capability behavior.
- Leave `grep -rln "StartBatteryPctEst\|EndBatteryPctEst\|start_battery_pct_est\|end_battery_pct_est"
  internal/` (excluding `.claude/worktrees/`) returning only this tier's own
  migration file, tier 2's already-archived migration file, and the two historic,
  never-edited migrations that originally added or renamed the columns — this
  tier's own definition of done, and the roadmap's stated completion criterion for
  the whole cleanup.

**Non-Goals (explicitly deferred, do not implement here):**
- Anything under `internal/gateway/` — tier 1, already archived.
- Anything under `internal/charging/` beyond the two explicitly granted, narrowly
  scoped corrections named above. `charging.supercharger_sessions` already lost its
  own `*Est` columns in tier 2; this tier's migration does not touch that table.
- MAG-45's session status column — tiers 4-5, not started, and this migration does
  not touch `status` in any way.
- Fixing `kkpa/context/architecture/nightly-cycle.md`'s stale "five" (charging's own
  gap, not caused by this tier — see proposal.md "Findings correction").
- Fixing the pre-existing trio-divergence staleness in the historic
  `20260823000001` Down comment (predates RM41, caused by RM31's
  `SessionVerifier` write path onto charging's trio only) — flagged, not fixed,
  under this tier's database design gate.

## Database Design (design gate — `database`, per `openspec/config.yaml` and `CLAUDE.md` Pipeline config)

### Schema change

`ALTER TABLE ... DROP COLUMN` on two columns of `telemetry.supercharger_history`,
both nullable `SMALLINT` with an inline `CHECK (... BETWEEN 0 AND 100)`. No other
column, index, or constraint on this table is touched.

**Migration file:**
`internal/telemetry/db/migrations/20260903000003_drop_supercharger_est_columns.sql`

Timestamp `20260903000003` sorts after `20260903000002`
(`internal/charging/db/migrations/20260903000002_drop_supercharger_est_columns.sql`,
tier 2's own migration), the newest migration in the repo at the time this design
was written (confirmed by `find internal -path "*/db/migrations/*.sql" | xargs -n1
basename | sort | tail -1`) — required because every module's migrations apply
against ONE shared `goose_db_version` table (`ai/go-conventions.md` §Testing), so
version numbers must be globally ordered even though this migration reads and
writes nothing outside `telemetry`'s own schema. (`MIGRATIONS_DIRS` in the Makefile
applies `internal/telemetry/db/migrations` before `internal/charging/db/migrations`
regardless of timestamp — directory order, not global timestamp order, governs
actual apply sequence — but there is no cross-module dependency here for that order
to matter: this migration neither reads nor writes anything charging owns.)

```sql
-- +goose Up
-- RM41 tier 3 (telemetry-drop-estimate-columns, MAG-36): drop the two dead
-- battery-percentage ESTIMATE columns from telemetry.supercharger_history.
--
-- WHY THIS IS SAFE (roadmap D1, re-verified by this change, not assumed):
--   - Nothing in this repository has ever written either column. UpsertSuperchargerHistory's
--     INSERT column list and ON CONFLICT DO UPDATE SET clause both omit them (db/query.sql).
--     No Writer for either column, or for the human-owned trio alongside them, has ever
--     existed in this module -- the verification UI that eventually shipped a write path
--     (SessionVerifier.VerifySession) was always charging's, per RM31, never telemetry's.
--   - The estimator this pair was reserved for (a taper-curve SOC estimator RM27 originally
--     planned, plus a gateway page rendering it) was DESCOPED by the owner on 2026-08-15,
--     the same day RM27 shipped -- backlog entry 11. The estimator MAG-36 eventually shipped
--     (derivedStartBatteryPct, internal/charging/capacity.go, 2026-09-01) writes the real
--     start_battery_pct column instead of a frozen verification-time snapshot, so this
--     reserved pair's original purpose no longer applies to any code path, past or present
--     (roadmap D1/D8).
--   - This table's copy of the trio (start_battery_pct, end_battery_pct,
--     battery_pct_source) is UNCHANGED and NOT dropped by this migration -- only the two
--     _est columns go. The trio stays reserved/unwritten here exactly as before this
--     migration; only charging.supercharger_sessions' trio is ever written, by
--     SessionVerifier (RM31). Telemetry's trio remains permanently NULL, same as always.
--   - charging.supercharger_sessions' own copy of these same two columns was already
--     dropped by RM41-charging-drop-estimate-columns (tier 2, migration
--     20260903000002_drop_supercharger_est_columns.sql) -- this migration completes the
--     retirement on the telemetry side; after this migration, neither table has an
--     estimate-pair column anywhere in the platform.
--
-- DROP COLUMN also drops each column's inline CHECK constraint
-- (supercharger_history_start_battery_pct_est_check /
-- supercharger_history_end_battery_pct_est_check, renamed from the
-- supercharger_sessions_* originals by 20260903000001_move_telemetry_to_own_schema.sql)
-- and its COMMENT ON COLUMN -- both are attached to the column's own catalog entry and
-- Postgres removes them automatically. No separate DROP CONSTRAINT statement is needed or
-- correct (roadmap D2).
ALTER TABLE telemetry.supercharger_history
    DROP COLUMN start_battery_pct_est,
    DROP COLUMN end_battery_pct_est;

-- +goose Down
-- Reintroduces both columns with their original type and CHECK, matching
-- 20260815000001_add_supercharger_battery_pct.sql's original column definitions exactly
-- (the CHECK's auto-generated name will resolve against the table's CURRENT name,
-- telemetry.supercharger_history, which is correct -- the table was already moved into
-- the telemetry schema and renamed from supercharger_sessions by 20260903000001 before
-- this migration ever runs).
--
-- NOT a data-preserving reversal: every value either column ever held (always NULL, per
-- the Up comment above) is gone the moment DROP COLUMN runs. Re-adding the columns here
-- restores the SCHEMA shape only, as two new NULL columns -- there is no shadow copy
-- anywhere in this migration to restore actual prior values from. This mirrors
-- charging's own 20260903000002_drop_supercharger_est_columns.sql Down (RM41 tier 2) and
-- this module's own 20260805000001/20260806000001/20260903000001 precedent of a
-- schema-only Down.
ALTER TABLE telemetry.supercharger_history
    ADD COLUMN start_battery_pct_est SMALLINT CHECK (start_battery_pct_est BETWEEN 0 AND 100),
    ADD COLUMN end_battery_pct_est   SMALLINT CHECK (end_battery_pct_est   BETWEEN 0 AND 100);
```

### Rationale — why this design is right

- **`DROP COLUMN` directly, not a phased/soft-delete approach.** Rejected
  alternatives (both explicitly rejected by the roadmap's owner, D1): (a) a
  verify-then-drop migration that first `SELECT COUNT(*) ... WHERE start_battery_pct_est
  IS NOT NULL` — unnecessary because the write-exclusion is structural: no Writer for
  either column has ever existed in this module, so the count is a foregone `0`, and
  paying an extra migration round-trip buys nothing. (b) Leaving the columns reserved
  as they are today — this IS the dead schema MAG-36 asked to remove, and the
  reservation's stated reason (letting a future estimator land without a migration)
  is now permanently moot: the estimator that shipped does not use this shape at all.
- **One migration, this module's own `migrations/` directory** — forced by the
  module boundary (`ai/architecture.md` §2), not chosen: `telemetry` owns
  `telemetry.supercharger_history`; no other module's migration may touch it.
  Mirrors `20260903000001`'s and every prior `telemetry` migration's placement.
- **No `DROP CONSTRAINT` / no `COMMENT ON COLUMN` cleanup statement** — both are
  child objects of the columns being dropped and Postgres removes them
  automatically as part of `DROP COLUMN` (identical to tier 2's own verified
  reasoning, restated here rather than assumed: dropping a column drops every
  constraint and comment defined on it). Adding a redundant `DROP CONSTRAINT`
  would either no-op or error depending on statement order relative to the `DROP
  COLUMN` — omitting it is both simpler and correct.
- **`ALTER TABLE ... DROP COLUMN` on a nullable column with no other table depending
  on it is a catalog-only, non-blocking-in-practice operation** on PostgreSQL: it
  does not rewrite the table's existing rows or scan them (unlike adding a column
  with a non-null `DEFAULT` computed per-row, or an index rebuild). It takes a brief
  `ACCESS EXCLUSIVE` lock only for the catalog update itself, the same class of
  operation as the `SET SCHEMA`/`RENAME` statements `20260903000001` already
  performs on this exact table without incident, and there are no cross-module
  foreign keys into `supercharger_history` (`ai/architecture.md` §2) to worry about
  cascading.
- **Down is schema-only, not data-preserving, and the migration says so explicitly**
  — consistent with this module's own `20260805000001`/`20260806000001` and
  `20260903000001` precedent of stating plainly when a Down cannot restore prior
  state, and with tier 2's identical Down for the same columns on the sibling table.
  There is nothing to restore here: every prior value was `NULL` (see the Up
  comment's proof), so a schema-only Down is not a degradation from some richer
  alternative — it is the only truthful Down possible.

### Index plan

**No index is added, changed, or removed by this migration**, and that is the
correct call, justified against this table's actual read patterns
(Performance-Profile: read-heavy):

- This table's two read-serving indexes, `idx_supercharger_history_vehicle_time
  (account_id, tesla_id, charge_start_date_time DESC)` and
  `idx_supercharger_history_account_time (account_id, charge_start_date_time DESC)`
  (both created by `20260716000001`, renamed by `20260903000001`), do not reference
  either `start_battery_pct_est` or `end_battery_pct_est` in any form — neither
  column is part of an index key, an included column, or a partial-index predicate
  (verified by reading both `CREATE INDEX` statements). Dropping two columns
  neither index names cannot invalidate or require rebuilding either one; PostgreSQL
  does not touch an index's definition when an unrelated column is dropped.
- Every read against this table (`SuperchargerHistoryByAccount`,
  `SuperchargerHistoryByVehicle`, `SuperchargerHistoryByVehicleBetween`,
  `SuperchargerHistoryByVehicleUpdatedSince`, all in `db/query.sql`) uses `SELECT *`
  (verified: `grep -n "^SELECT" internal/telemetry/db/query.sql` shows all four as
  `SELECT * FROM telemetry.supercharger_history`). Dropping two columns shrinks the
  row width each of these already-indexed scans returns — a strict, free
  improvement to I/O and network transfer per row, not a regression requiring
  compensating index work.
- No new query is introduced by this change, so no new access pattern exists to
  index for. The **absence** of new indexing work is itself the read-optimized
  choice here: adding an index would be optimizing a column being deleted.

### What happens to existing rows / data-loss statement

- **No data is lost.** Every row's `start_battery_pct_est` and
  `end_battery_pct_est` value is `NULL` today, in every environment, with no
  exception (see the Up migration's comment for the full proof: no Writer for
  either column has ever existed in this module, and the estimator that would have
  eventually populated them writes a different column entirely).
- **Every other column on every existing row is completely unaffected** — `DROP
  COLUMN` on PostgreSQL does not rewrite, lock beyond the catalog change, or
  otherwise touch the storage of any other column; `start_battery_pct`,
  `end_battery_pct`, `battery_pct_source`, `raw_data`, and every extracted/mirrored
  column keep their exact stored values.
- **If this assumption is ever wrong in a specific deployed database** (e.g. a
  hand-run `UPDATE` outside the app set a value directly, which nothing in this
  codebase does or has ever done), that value is genuinely lost on `Up` and is
  **not** recoverable from `Down` (Down only re-adds empty columns — see the
  migration's own Down comment). The roadmap's owner accepted this exact risk
  explicitly when choosing D1 over a verify-then-drop migration for BOTH tables in
  this roadmap, not only charging's — "the evidence is already conclusive" that no
  such value exists on either table. This design does not re-litigate that
  decision; it documents the accepted risk plainly, as `CLAUDE.md`'s
  dev-db-holds-irreplaceable-history caution requires for any destructive DB
  action, even one already decided.

## Code changes — exact edits

### `internal/telemetry/telemetry.go`

Remove the entire "Reserved, currently unwritten" comment block and the two fields
it describes (currently lines 462-469):

```go
	// --- Reserved, currently unwritten (design D6) ---
	// Intended as a write-once drift log for a future SOC-estimation capability: frozen
	// at the moment a human verifies, never refreshed afterward. That capability was
	// DESCOPED from RM27 (2026-08-15) and does not exist, so these two columns are
	// always NULL today. Kept rather than dropped so the estimator can land later
	// without a migration. Excluded from UpsertSuperchargerHistory like the trio above.
	StartBatteryPctEst *int // NULL until BOTH an estimator and a verification UI exist.
	EndBatteryPctEst   *int // same semantics as StartBatteryPctEst.
```

Update the trailing cross-reference in the surviving trio comment (currently line
454, "No writer exists in this repository yet: RM27 shipped the storage only (see
below)."), which pointed at the block just deleted:

Before: `// in this repository yet: RM27 shipped the storage only (see below).`

After: `// in this repository yet: RM27 shipped the storage only. (The reserved`
`// StartBatteryPctEst/EndBatteryPctEst pair that used to follow this comment was`
`// dropped entirely by RM41-telemetry-drop-estimate-columns — the reservation it`
`// existed for is obsolete; see that change's design.md.)`

### `internal/telemetry/mapping.go`

Remove the two mapping lines from `rowToSuperchargerHistory` (currently):

```go
		StartBatteryPctEst: pgNullableInt16AsInt(r.StartBatteryPctEst),
		EndBatteryPctEst:   pgNullableInt16AsInt(r.EndBatteryPctEst),
```

Update the field-block comment immediately above the mapping (currently):

```go
		// Battery-% verification/override trio + frozen snapshot pair (RM27 tier 1,
		// MAG-14, design D5/D6). Placed last, mirroring the migration's physical
		// column-append order.
```

becomes:

```go
		// Battery-% verification/override trio (RM27 tier 1, MAG-14, design D5).
		// Placed last, mirroring the migration's physical column-append order.
```

Update the mapping-rules doc comment bullet above `rowToSuperchargerHistory`
(currently):

```go
//   - Battery-% verification columns (RM27-telemetry-add-supercharger-battery-pct,
//     design D5/D6): pgtype.Int2 → *int via pgNullableInt16AsInt (all four SMALLINT
//     columns); pgtype.Text → *string via the existing pgNullableText for
//     BatteryPctSource. NULL means no override/no snapshot exists.
```

becomes:

```go
//   - Battery-% verification columns (RM27-telemetry-add-supercharger-battery-pct,
//     design D5): pgtype.Int2 → *int via pgNullableInt16AsInt (both remaining
//     SMALLINT columns); pgtype.Text → *string via the existing pgNullableText for
//     BatteryPctSource. NULL means no override exists.
```

### `internal/telemetry/service.go`

Update the comment on `upsertSuperchargerHistory` (currently):

```go
// Deliberately does NOT read s.StartBatteryPct / s.EndBatteryPct / s.BatteryPctSource /
// s.StartBatteryPctEst / s.EndBatteryPctEst: telemetrydb.UpsertSuperchargerHistoryParams
// has no fields for them (query.sql omits all five from the query entirely — R3/D3/D6,
// RM27-telemetry-add-supercharger-battery-pct). This keeps the nightly poller from ever
// silently overwriting a human-verified value or its frozen snapshot.
```

becomes:

```go
// Deliberately does NOT read s.StartBatteryPct / s.EndBatteryPct / s.BatteryPctSource:
// telemetrydb.UpsertSuperchargerHistoryParams has no fields for them (query.sql omits
// all three from the query entirely — R3/D3, RM27-telemetry-add-supercharger-battery-pct).
// This keeps the nightly poller from ever silently overwriting a human-verified value.
// (s.StartBatteryPctEst/s.EndBatteryPctEst, formerly also named here as fields this
// mapping never reads, were dropped from SuperchargerHistory entirely by
// RM41-telemetry-drop-estimate-columns — there is no longer a field to not read.)
```

### `internal/telemetry/db/query.sql`

One guarding comment, rewritten in place (no SQL statement changes — all four
Supercharger reads already use `SELECT *`, which shrinks automatically once the
columns are gone).

**`UpsertSuperchargerHistory`'s comment**, currently:

```sql
-- LOAD-BEARING (R3, RM27-telemetry-add-supercharger-battery-pct): start_battery_pct,
-- end_battery_pct, battery_pct_source, start_battery_pct_est, and end_battery_pct_est
-- are DELIBERATELY ABSENT from both the INSERT column list and the ON CONFLICT DO
-- UPDATE SET clause below. The first three are a human-owned verification/override
-- channel; the last two are a frozen, write-once verification-time snapshot of the
-- estimate (design D6) -- NEVER refreshed, NEVER a cache read by internal/analytics
-- (see the column comments added by migration 20260815000001). If this query touched
-- any of the five, a user's verified value or its frozen snapshot would be silently
-- overwritten by the next nightly re-upsert. A fresh INSERT leaves all five at their
-- column default (NULL); a re-upsert never assigns any of them. A future writer for
-- these columns belongs on a dedicated query on a dedicated Writer port (out of
-- scope here, backlog entry 11) -- do not "complete the pattern" by adding them here.
```

becomes:

```sql
-- LOAD-BEARING (R3, RM27-telemetry-add-supercharger-battery-pct): start_battery_pct,
-- end_battery_pct, and battery_pct_source are DELIBERATELY ABSENT from both the INSERT
-- column list and the ON CONFLICT DO UPDATE SET clause below. They are a human-owned
-- verification/override channel; the nightly sync must never write, clear or overwrite
-- one. If this query touched any of the three, a user's verified value would be
-- silently overwritten by the next nightly re-upsert. A fresh INSERT leaves all three
-- at their column default (NULL); a re-upsert never assigns any of them. A future
-- writer for these columns belongs on a dedicated query on a dedicated Writer port
-- (out of scope here, backlog entry 11) -- do not "complete the pattern" by adding
-- them here. (start_battery_pct_est/end_battery_pct_est, formerly also excluded here
-- as a frozen, write-once verification-time snapshot pair, were dropped from the
-- table entirely by RM41-telemetry-drop-estimate-columns -- there is no longer a
-- column to guard.)
```

### Regenerated files

`internal/telemetry/db/models.go` and `internal/telemetry/db/query.sql.go` are
regenerated by `make sqlc` (or `sqlc generate`) after the migration and the
`query.sql` comment edit land. They are never hand-edited. Expected effect:
`SuperchargerHistory.StartBatteryPctEst`/`EndBatteryPctEst` (both `pgtype.Int2`)
disappear from `models.go`, along with the `COMMENT ON TABLE` text sqlc copies
verbatim (currently naming "all five excluded from the nightly UPSERT"); the
`SELECT *`-based query result structs in `query.sql.go` shrink by the same two
fields at all four call sites; the `UpsertSuperchargerHistory` guarding comment in
the generated file updates to match `query.sql`'s new text (sqlc copies query-level
comments verbatim).

## Test Contract

Author's note: this section, together with proposal.md's "Findings correction",
constitutes the up-front expected-value authoring `ai/go-conventions.md` requires
before implementation. Every occurrence below was located by `grep -n
"StartBatteryPctEst\|EndBatteryPctEst" <file>` against the tip of this branch at the
time this design was written; re-run the same grep during implementation to confirm
nothing drifted.

### 1. `internal/telemetry/db_supercharger_battery_pct_integration_test.go`

**File-level doc comment** (currently lines 14-19, 21-22) — update the column count
and drop the frozen-pair description:

Before:
```go
// These tests exercise the five battery-% verification/override columns added to
// supercharger_sessions by RM27-telemetry-add-supercharger-battery-pct (MAG-14):
// start_battery_pct, end_battery_pct, battery_pct_source (the human-owned trio, D1/D2)
// and start_battery_pct_est, end_battery_pct_est (the frozen, write-once verification
// snapshot pair, design D6). They implement the test contract authored in this
// change's design.md BEFORE the mapping code existed (§"Test Contract").
//
// No Go writer exists anywhere in this repository for any of the five columns (R7),
```

After:
```go
// These tests exercise the three battery-% verification/override columns added to
// supercharger_sessions by RM27-telemetry-add-supercharger-battery-pct (MAG-14):
// start_battery_pct, end_battery_pct, battery_pct_source (the human-owned trio, D1/D2).
// A fourth and fifth column, start_battery_pct_est/end_battery_pct_est (a frozen,
// write-once verification snapshot pair, design D6), existed alongside the trio until
// RM41-telemetry-drop-estimate-columns (2026-09-03) dropped both — the reservation
// they existed for (a future SOC estimator) turned out unnecessary once the estimator
// that shipped wrote the real start_battery_pct column instead. They implement the
// test contract authored in this change's design.md BEFORE the mapping code existed
// (§"Test Contract").
//
// No Go writer exists anywhere in this repository for any of the three columns (R7),
```

**`TestStore_SuperchargerUpsert_FreshInsertSeedsBatteryPctColumnsNull`** — update its
doc comment ("leaves all five battery-% columns NULL (T6.1)" → "leaves all three
battery-% columns NULL (T6.1)") and delete the two `*Est` assertion blocks
(currently):

```go
	if s.StartBatteryPctEst != nil {
		t.Errorf("StartBatteryPctEst: want nil on fresh insert, got %v", *s.StartBatteryPctEst)
	}
	if s.EndBatteryPctEst != nil {
		t.Errorf("EndBatteryPctEst: want nil on fresh insert, got %v", *s.EndBatteryPctEst)
	}
```

Expected value UNCHANGED for the three remaining assertions (`StartBatteryPct`,
`EndBatteryPct`, `BatteryPctSource` all still want `nil`).

**`TestStore_SuperchargerUpsert_LeavesVerifiedBatteryPctUntouched`** — UNCHANGED.
This test asserts only the trio (`StartBatteryPct`/`EndBatteryPct`/
`BatteryPctSource`) survives a re-upsert; it names neither `*Est` field anywhere
(`grep -c "BatteryPctEst"` against this function returns `0` already). No edit.

**`TestStore_SuperchargerUpsert_LeavesVerificationSnapshotUntouched`** — **DELETE
THIS ENTIRE TEST FUNCTION**, including its doc comment (currently lines 175-254,
80 lines). This is the test roadmap D7 refers to as "the two `telemetry` tests that
assert the columns survive a re-UPSERT" — resolved here to one Go test function
whose loop body performs the re-upsert-and-assert cycle twice ("re-upsert #1" and
"re-upsert #2"), i.e. the two guard assertions D7 names are the two loop iterations
within this one function, not two separate functions (no second telemetry test
function anywhere in this module references either `*Est` field — confirmed by
`grep -rn "BatteryPctEst" internal/telemetry/*_test.go` returning only this file).
This function's ENTIRE purpose was verifying the D6 regression guard (the frozen
snapshot pair survives repeated nightly re-upserts); once the columns it guards
don't exist, deleting the assertion blocks alone would leave a function that only
re-tests "IsPaid/UpdatedAt refresh across two re-upserts" — already covered, once,
by `TestStore_SuperchargerUpsert_LeavesVerifiedBatteryPctUntouched`. Keeping a
gutted duplicate is not "existing tests repaired," it is dead weight the owner's
no-new-unit-tests default does not ask for. Delete the whole function.

**`TestStore_SuperchargerBatteryPctChecks_RejectOutOfRangeAndUnrecognizedValues`** —
two edits:

1. Delete three entries from the `rejected` table (currently):
   ```go
   {"start_battery_pct_est > 100", `UPDATE telemetry.supercharger_history SET start_battery_pct_est = 101 WHERE session_id = $1`},
   {"start_battery_pct_est < 0", `UPDATE telemetry.supercharger_history SET start_battery_pct_est = -1 WHERE session_id = $1`},
   {"end_battery_pct_est > 100", `UPDATE telemetry.supercharger_history SET end_battery_pct_est = 101 WHERE session_id = $1`},
   ```
   (a statement setting a dropped column is a Postgres "column does not exist"
   error — SQLSTATE `42703`, not the `23514` check_violation this test asserts —
   so leaving any of these three would fail the test for the wrong reason, not
   just fail to compile; this edit is a correctness requirement, not a cosmetic
   one). The remaining two entries (`start_battery_pct > 100`/`< 0`,
   `end_battery_pct > 100`) and the two `battery_pct_source` entries are UNCHANGED.
2. Remove the two `*_est` column assignments from the boundary/`'polled'`
   sanity-check UPDATE (currently):
   ```go
   `UPDATE telemetry.supercharger_history SET start_battery_pct = 0, end_battery_pct = 100, battery_pct_source = 'polled', start_battery_pct_est = 0, end_battery_pct_est = 100 WHERE session_id = $1`,
   ```
   becomes:
   ```go
   `UPDATE telemetry.supercharger_history SET start_battery_pct = 0, end_battery_pct = 100, battery_pct_source = 'polled' WHERE session_id = $1`,
   ```
   (same reasoning: this is an UPDATE against a real database, not a struct
   literal — a stale column reference is a runtime error, not a silent no-op).
   Update the trailing comment above this statement ("must be accepted on all
   four SMALLINT columns" → "must be accepted on both remaining SMALLINT
   columns").

**`TestStore_SuperchargerHistoryReader_ReturnsBatteryPctTrioAndSnapshot`** — rename
to `TestStore_SuperchargerHistoryReader_ReturnsBatteryPctTrio` (the function no
longer tests a snapshot; the old name would misdescribe it) and apply four edits:

1. Remove the two `*_est` column assignments from the `sessionSnapshot` fixture's
   direct-SQL UPDATE (currently):
   ```go
   `UPDATE telemetry.supercharger_history SET start_battery_pct = 20, end_battery_pct = 80, battery_pct_source = 'user_verified', start_battery_pct_est = 22, end_battery_pct_est = 78 WHERE session_id = $1`,
   ```
   becomes:
   ```go
   `UPDATE telemetry.supercharger_history SET start_battery_pct = 20, end_battery_pct = 80, battery_pct_source = 'user_verified' WHERE session_id = $1`,
   ```
   (runtime-error reasoning, same as above — required for the file to run, not
   just to compile).
2. Delete the `assertSnapshot` helper function entirely (currently):
   ```go
   assertSnapshot := func(t *testing.T, s SuperchargerHistory) {
   	t.Helper()
   	if s.StartBatteryPctEst == nil || *s.StartBatteryPctEst != 22 {
   		t.Errorf("StartBatteryPctEst: want *22, got %v", s.StartBatteryPctEst)
   	}
   	if s.EndBatteryPctEst == nil || *s.EndBatteryPctEst != 78 {
   		t.Errorf("EndBatteryPctEst: want *78, got %v", s.EndBatteryPctEst)
   	}
   }
   ```
3. Replace the `sessionSnapshot` case's `assertSnapshot(t, s)` call with an inline
   assertion of that session's own (still-set) trio values, so the fixture keeps
   proving multi-session correctness within one account rather than becoming dead
   bookkeeping. Currently:
   ```go
   case sessionSnapshot:
   	foundSnapshotInAccount = true
   	assertSnapshot(t, s)
   ```
   becomes:
   ```go
   case sessionSnapshot:
   	foundSnapshotInAccount = true
   	if s.StartBatteryPct == nil || *s.StartBatteryPct != 20 {
   		t.Errorf("StartBatteryPct: want *20, got %v", s.StartBatteryPct)
   	}
   	if s.EndBatteryPct == nil || *s.EndBatteryPct != 80 {
   		t.Errorf("EndBatteryPct: want *80, got %v", s.EndBatteryPct)
   	}
   	if s.BatteryPctSource == nil || *s.BatteryPctSource != "user_verified" {
   		t.Errorf("BatteryPctSource: want *user_verified, got %v", s.BatteryPctSource)
   	}
   ```
   (Do not generalize `assertVerifiedTrio` to take parameters — it is called
   elsewhere with the hardcoded 18/76 values for `sessionVerified`; adding a
   parameter would touch a call site this edit has no reason to touch.)
4. Update `assertUntouched`'s condition and message (currently):
   ```go
   if s.StartBatteryPct != nil || s.EndBatteryPct != nil || s.BatteryPctSource != nil ||
   	s.StartBatteryPctEst != nil || s.EndBatteryPctEst != nil {
   	t.Errorf("want all five battery-percentage columns nil for an untouched session, got %+v", s)
   }
   ```
   becomes:
   ```go
   if s.StartBatteryPct != nil || s.EndBatteryPct != nil || s.BatteryPctSource != nil {
   	t.Errorf("want all three battery-percentage columns nil for an untouched session, got %+v", s)
   }
   ```

Variable/const names (`sessionSnapshot`, `foundSnapshotInAccount`) are left
UNCHANGED — renaming them is not required for the file to compile or run
correctly, and doing so would widen this edit's diff for no functional gain.

### 2. `internal/charging/db_backfill_integration_test.go` (granted path)

`superchargerFixtureRow`'s two fields, currently:

```go
	StartBatteryPctEst *int
	EndBatteryPctEst   *int
```

deleted outright (no replacement — there is no column left to hold a value for).

`insertSuperchargerSessionFixture`'s INSERT and `Exec` call, currently:

```go
	_, err := pool.Exec(context.Background(), `
		INSERT INTO telemetry.supercharger_history (
			session_id, account_id, vin, tesla_id, site_location_name, country_code,
			charge_start_date_time, charge_stop_date_time, billing_type, vehicle_make_type,
			energy_kwh, total_cost, currency, is_paid, raw_data, created_at,
			start_battery_pct, end_battery_pct, battery_pct_source,
			start_battery_pct_est, end_battery_pct_est
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10,
			$11, $12, $13, $14, '{}'::jsonb, $15,
			$16, $17, $18,
			$19, $20
		)`,
		r.SessionID, accountID, r.VIN, r.TeslaID, r.SiteLocationName, r.CountryCode,
		r.ChargeStartDateTime, r.ChargeStopDateTime, r.BillingType, r.VehicleMakeType,
		r.EnergyKWh, r.TotalCost, r.Currency, r.IsPaid, r.CreatedAt,
		r.StartBatteryPct, r.EndBatteryPct, r.BatteryPctSource,
		r.StartBatteryPctEst, r.EndBatteryPctEst,
	)
```

becomes (20 params → 18; write the new placeholder list literally as `$1..$18`, do
not attempt to preserve old numbers with gaps):

```go
	_, err := pool.Exec(context.Background(), `
		INSERT INTO telemetry.supercharger_history (
			session_id, account_id, vin, tesla_id, site_location_name, country_code,
			charge_start_date_time, charge_stop_date_time, billing_type, vehicle_make_type,
			energy_kwh, total_cost, currency, is_paid, raw_data, created_at,
			start_battery_pct, end_battery_pct, battery_pct_source
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10,
			$11, $12, $13, $14, '{}'::jsonb, $15,
			$16, $17, $18
		)`,
		r.SessionID, accountID, r.VIN, r.TeslaID, r.SiteLocationName, r.CountryCode,
		r.ChargeStartDateTime, r.ChargeStopDateTime, r.BillingType, r.VehicleMakeType,
		r.EnergyKWh, r.TotalCost, r.Currency, r.IsPaid, r.CreatedAt,
		r.StartBatteryPct, r.EndBatteryPct, r.BatteryPctSource,
	)
```

Confirmed by reading every `superchargerFixtureRow{...}` literal in this file
(`seedA1Fixture`'s four rows): none sets `StartBatteryPctEst`/`EndBatteryPctEst`
explicitly — both are always the Go zero value (`nil`) today, so this is a pure
mechanical removal with no fixture-value consequence. No other line in this file
names `BatteryPctEst` (confirmed by `grep -n "BatteryPctEst"
internal/charging/db_backfill_integration_test.go` returning exactly the four
lines edited above at the time of writing) — no assertion in this file reads
either field back (it asserts against `charging.supercharger_sessions`'
`superchargerSessionRow`, a different struct in a different file, already
resolved by tier 2). Touch NOTHING else in this file.

## Docs

### `internal/telemetry/AGENTS.md` (module doc — `CLAUDE.md` docs-track-change rule)

Six locations, each an in-place text replacement (exact grep-locatable anchors
given so the acceptance check in tasks.md can confirm each landed):

1. **The nightly-collection intro sentence** (anchor: `deliberately excluded from
   the upsert`) — "The five battery-% verification columns are deliberately
   excluded from the upsert" becomes "The three battery-% verification columns are
   deliberately excluded from the upsert".
2. **The Data Ownership `supercharger_history` bullet** (anchor: `with five new
   nullable columns`) — "Extended by `RM27-telemetry-add-supercharger-battery-pct`
   (MAG-14, migration `20260815000001`) with five new nullable columns, all
   excluded from ..." becomes "Extended by
   `RM27-telemetry-add-supercharger-battery-pct` (MAG-14, migration
   `20260815000001`) with three nullable columns (originally five;
   `RM41-telemetry-drop-estimate-columns`, 2026-09-03, dropped the frozen
   `start_battery_pct_est`/`end_battery_pct_est` snapshot pair), all excluded
   from ...". Delete the entire `start_battery_pct_est`/`end_battery_pct_est`
   sub-bullet (currently "`start_battery_pct_est SMALLINT CHECK (0..100)`,
   `end_battery_pct_est SMALLINT CHECK (0..100)` — frozen, write-once
   verification-time snapshot pair (design D6)."). Nothing replaces it.
3. **The "Battery-% verification columns" section's field-count sentence** (anchor:
   `gains five pointer fields`) — "`SuperchargerHistory` gains five pointer fields
   (`StartBatteryPct *int`, `EndBatteryPct *int`, `BatteryPctSource *string`,
   `StartBatteryPctEst *int`, `EndBatteryPctEst *int`), mapped by
   `rowToSuperchargerHistory` (`mapping.go`) via the new `pgNullableInt16AsInt`
   helper (first `SMALLINT`/`pgtype.Int2` column in this module; reused for all
   four `SMALLINT` fields) and the existing `pgNullableText` helper for
   `BatteryPctSource`." becomes "`SuperchargerHistory` gains three pointer fields
   (`StartBatteryPct *int`, `EndBatteryPct *int`, `BatteryPctSource *string`),
   mapped by `rowToSuperchargerHistory` (`mapping.go`) via the new
   `pgNullableInt16AsInt` helper (first `SMALLINT`/`pgtype.Int2` column in this
   module; reused for both `SMALLINT` fields) and the existing `pgNullableText`
   helper for `BatteryPctSource`."
4. **The SCOPE NOTE** (anchor: `there is no estimator, and there will not be one
   under RM27`) — "**Both were descoped by the owner**; RM27 ships these five
   columns and nothing else." becomes "**Both were descoped by the owner**; RM27
   shipped these three columns (plus a since-dropped reserved pair — see the next
   bullet) and nothing else."
5. **The "Never auto-written (R3/R7)" bullet** (anchor: `all five columns are
   excluded from`) — "**Never auto-written (R3/R7):** all five columns are
   excluded from `UpsertSuperchargerHistory`'s `INSERT` column list and its `ON
   CONFLICT DO UPDATE SET` clause — deliberately, not an oversight (design D3).
   The nightly poller re-upserts every session because Tesla billing state
   (`is_paid`, invoices) mutates post-session; if any of these five were bound as
   a query parameter, a human-verified value would be silently overwritten on the
   next nightly re-upsert. **No writer for any of the five columns exists
   anywhere in this repository as of this change** — the future verification
   UI's Writer port is out of scope here (backlog entry 11)." becomes "**Never
   auto-written (R3/R7):** all three columns are excluded from
   `UpsertSuperchargerHistory`'s `INSERT` column list and its `ON CONFLICT DO
   UPDATE SET` clause — deliberately, not an oversight (design D3). The nightly
   poller re-upserts every session because Tesla billing state (`is_paid`,
   invoices) mutates post-session; if any of these three were bound as a query
   parameter, a human-verified value would be silently overwritten on the next
   nightly re-upsert. **No writer for any of the three columns exists anywhere
   in this repository as of this change** — the future verification UI's Writer
   port is out of scope here (backlog entry 11)." (Note this bullet's own text
   already correctly says "column" singular nowhere and needs no other change —
   only the three numeral substitutions above.)
6. **The entire `StartBatteryPctEst`/`EndBatteryPctEst` RESERVED bullet** (anchor:
   `are RESERVED and, today, always NULL`) — DELETE the whole bullet (currently
   the 15-line bullet starting "**`StartBatteryPctEst`/`EndBatteryPctEst` are
   RESERVED and, today, always NULL.**" and ending "... in
   `openspec/changes/archive/2026-08-15-RM27-telemetry-add-supercharger-battery-pct/design.md`
   D6."). Replace it with a short reversal note, mirroring `telemetry.go`'s own
   D8 reversal comment:
   ```
   - **`StartBatteryPctEst`/`EndBatteryPctEst` no longer exist.** RM27 (2026-08-15)
     kept them reserved, unwritten, so a future SOC estimator could land without a
     migration. That reservation is now obsolete: the estimator MAG-36 eventually
     shipped (`derivedStartBatteryPct`, `internal/charging/capacity.go`,
     2026-09-01) writes the real `start_battery_pct` column instead of a frozen
     snapshot column — so `RM41-telemetry-drop-estimate-columns` (2026-09-03)
     dropped both columns from `telemetry.supercharger_history` along with the
     two `SuperchargerHistory` fields and the RM27-D6 comment block that
     described them. Full history of the original design:
     `openspec/changes/archive/2026-08-15-RM27-telemetry-add-supercharger-battery-pct/design.md`
     D6 (superseded).
   ```

### `internal/charging/AGENTS.md` (granted path, one sentence)

Tier 2 deliberately left a forward-looking sentence in its own Data Ownership
section for this tier to close (anchor: `keeps its own copy of the three`).
Before:

```
- `internal/telemetry.supercharger_history` keeps its own copy of the three
  remaining battery-percentage columns (`start_battery_pct`, `end_battery_pct`,
  `battery_pct_source`) until tier 3 of this roadmap
  (`RM41-telemetry-drop-estimate-columns`) drops its own est-column pair — see
  that change once it lands.
```

After:

```
- `internal/telemetry.supercharger_history` keeps its own copy of the same three
  battery-percentage columns (`start_battery_pct`, `end_battery_pct`,
  `battery_pct_source`) — permanently, not temporarily. Tier 3 of this roadmap
  (`RM41-telemetry-drop-estimate-columns`, 2026-09-03) dropped telemetry's own
  `start_battery_pct_est`/`end_battery_pct_est` pair, the same drop this change
  performed one tier earlier on `charging.supercharger_sessions`; neither table
  has carried an `_est` column since.
```

### `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql` (granted path, comment-only)

This HISTORIC migration's own `-- +goose Down` comment names this exact future
change and instructs it to update the comment (see proposal.md "Findings
correction" for the full derivation). Before (the second paragraph of the Down
comment only — the first paragraph, "Safe by construction TODAY: ... Nothing is
lost that is not still upstream.", is UNCHANGED):

```
-- (This stops being true once the deferred contract change drops telemetry's five
-- percentage columns -- at that point charge_sessions is the only copy of them and
-- this Down becomes destructive. That change owns updating this comment; see
-- design.md D9, step 6. The mirrored session facts -- site, energy, cost, currency,
-- is_paid -- stay in telemetry.supercharger_history permanently, so they are never at
-- risk.)
```

After:

```
-- UPDATE (RM41-telemetry-drop-estimate-columns, 2026-09-03 -- this comment's own
-- "deferred contract change", D9 step 6): the anticipated drop never happened in the
-- shape D9 described. RM41 dropped only the two frozen ESTIMATE columns
-- (start_battery_pct_est/end_battery_pct_est) from BOTH charging.supercharger_sessions
-- (RM41-charging-drop-estimate-columns, tier 2) and telemetry.supercharger_history
-- (RM41-telemetry-drop-estimate-columns, tier 3) -- neither module is "the only copy"
-- of them, because neither module has them anymore. The three remaining percentage
-- columns (start_battery_pct, end_battery_pct, battery_pct_source) were NOT touched by
-- RM41 and still exist in both tables today, so this Down's original safety claim
-- continues to hold for them unchanged. (Separately, and predating RM41: since
-- RM31-charging-add-session-verification-port, a human's write through
-- SessionVerifier lands only on charging's trio, not telemetry's own -- so
-- telemetry's copy of the trio is not a byte-for-byte upstream mirror the way the
-- raw session facts are. That divergence is unrelated to this DROP and is not
-- evaluated by this comment.)
```

This is a text-only comment correction to an already-applied migration — the file's
own author pre-authorized exactly this edit for exactly this change ("That change
owns updating this comment"). No `-- +goose Up` or `-- +goose Down` SQL statement in
this file is touched; `ai/go-conventions.md`'s "historic migrations are never
edited" governs statements/behavior, not a comment the file's own author scoped to
this future change.

## Spec delta — `openspec/specs/telemetry/spec.md`

Two Requirements describe the frozen verification-time snapshot pair as currently-
specified capability behavior. Both get a `MODIFIED Requirements` entry in this
change's own `specs/telemetry/spec.md` (full replacement text, per OpenSpec
convention — see that file in this change for the complete before/after of every
paragraph and scenario touched). Summary of what changes:

1. **"Supercharger Session Ledger"** — the paragraph beginning "The ledger SHALL
   additionally carry a frozen verification-time snapshot pair for each session..."
   and the NOTE immediately after it are both removed in full. Two scenarios
   entirely about the snapshot pair are edited to drop the snapshot half of their
   GIVEN/WHEN/THEN, keeping only the trio-focused behavior each already exercised
   alongside it: "A newly verified session records a frozen snapshot of the
   estimate alongside the override" is renamed to "A newly verified session records
   its battery-percentage verification trio" and its snapshot-setting/asserting
   clauses are dropped; "A later re-verification of the same session overwrites the
   frozen snapshot with the new verification's own snapshot" is renamed to "A later
   re-verification of the same session overwrites the trio with the new
   verification's own values" and its snapshot clauses are dropped. A third
   scenario, "A nightly refresh never overwrites an already-set verification-time
   snapshot pair", is deleted outright — its trio counterpart, "A nightly refresh
   never overwrites an already-set battery-percentage verification trio", already
   covers the surviving behavior and is UNCHANGED.
2. **"Supercharger Session Read Port"** — the requirement's "and its
   verification-time snapshot pair (start percentage estimate, end percentage
   estimate) exactly as stored — NULL when no verification has occurred for that
   session" clause is dropped from the list of what each retrieved session
   carries. Two scenarios are deleted outright: "A returned session carries its
   verification-time snapshot pair" and "A returned session with no verification
   has a NULL verification-time snapshot pair" — their trio counterparts, "A
   returned session carries its battery-percentage verification trio" and "A
   returned session with no override has a NULL battery-percentage verification
   trio", already cover the surviving behavior and are UNCHANGED.

No other Requirement in `openspec/specs/telemetry/spec.md` mentions `_est`,
"frozen", or "snapshot pair" (confirmed: `grep -n "estimate\|frozen\|snapshot
pair" openspec/specs/telemetry/spec.md` after line 434 returns nothing — the two
Requirements above are the complete set).

## Risks

- **One test file's raw SQL statements set a column that no longer exists — a
  runtime error, not a compile error.** Three `UPDATE` statements in
  `db_supercharger_battery_pct_integration_test.go` (the boundary sanity-check UPDATE
  and the `sessionSnapshot` fixture's UPDATE) and one `INSERT` in the granted
  `internal/charging/db_backfill_integration_test.go` name a dropped column
  directly. `go vet ./internal/telemetry/... ./internal/charging/...` (compiles
  `_test.go` files) verifies the Go source compiles but says nothing about these SQL
  strings — the Test Contract above gives each one's exact before/after so no
  statement is left naming a dropped column; the acceptance grep in tasks.md's
  final wave is the authoritative completeness check, not `go vet` alone.
- **Deleting a whole test function is a judgment call, not a mechanical
  transcription** — `TestStore_SuperchargerUpsert_LeavesVerificationSnapshotUntouched`
  is removed in full rather than having its two assertion blocks trimmed, because
  trimming them would leave a function whose only remaining behavior duplicates
  `TestStore_SuperchargerUpsert_LeavesVerifiedBatteryPctUntouched`. The Test
  Contract section above states this reasoning explicitly so the deletion reads as
  a documented decision, not a missed edit.
- **The two granted cross-module corrections are text-only and narrowly scoped —
  do not let them become an excuse to touch more of `internal/charging/`.** The
  roadmap's own precedent (D6, tier 2) scoped `internal/analytics/db_integration_test.go`
  to "nothing else under `internal/analytics/`"; the same discipline applies here to
  both `internal/charging/AGENTS.md` (one sentence) and the historic migration
  comment (one paragraph). A tempting adjacent cleanup — e.g. the
  `nightly-cycle.md` staleness this worker found and deliberately did NOT fix (see
  proposal.md) — belongs to a different change, not this one.

**This tier trips the `database` design gate.** Migration SQL, rationale, index
plan, and data-loss statement are all above under "Database Design" — the leader
shows this section to the owner and iterates until confirmed before any
implementation task in tasks.md may start.
