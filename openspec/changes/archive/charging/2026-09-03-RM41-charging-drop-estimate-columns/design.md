# Design — RM41-charging-drop-estimate-columns

## Context

`RM41-supercharger-battery-pct-cleanup` (the roadmap, read in full before this
design) closes out MAG-36. Tier 1 (`RM41-gateway-revise-battery-pct-ui`, archived)
removed every gateway read of `charging.Session.StartBatteryPctEst`/
`EndBatteryPctEst`. This tier, tier 2, drops the two columns those fields backed —
`start_battery_pct_est`/`end_battery_pct_est` on `charging.supercharger_sessions` —
along with every remaining Go, SQL-comment, `AGENTS.md`, KB, and spec reference to
them inside this module's boundary.

The columns have been permanently `NULL` since `RM29-charging-add-charge-sessions`
shipped them: `MirrorSuperchargerSession`'s INSERT/ON CONFLICT clauses exclude them,
`VerifySuperchargerSession`'s SET clause excludes them, and neither `SessionMirror`
nor the query has a field/column to bind one to even by mistake (the RM29/RM31
"protection by compile error" pattern). The estimator that was originally meant to
fill them (`RM27` D11 descoped it) shipped 2026-09-01 as
`charging-add-derived-start-battery-pct`, writing the real `start_battery_pct`
column instead of a frozen snapshot — so the two columns are now permanently dead
schema (roadmap D1/D8).

## Goals / Non-Goals

**Goals:**
- Drop `start_battery_pct_est`/`end_battery_pct_est` from
  `charging.supercharger_sessions` in one migration (roadmap D1, D2).
- Remove `Session.StartBatteryPctEst`/`EndBatteryPctEst` and their `rowToSession`
  mappings, and every doc comment naming them, from `internal/charging/`.
- Rewrite the two `db/query.sql` guarding comments that name the columns as
  excluded — once the columns don't exist, "excluded from the SET clause" no longer
  describes anything real.
- Repair the four `charging` integration tests and the granted
  `internal/analytics/db_integration_test.go` path so all five compile and assert
  the new shape (roadmap D6, D7) — see Test Contract below for every edit,
  authored before implementation.
- Correct `internal/charging/AGENTS.md`, and three `kkpa/context/` files (two
  assigned by the roadmap, one found by this worker — see "Findings correction" in
  proposal.md), per `CLAUDE.md`'s docs-track-change rule.
- Update `openspec/specs/charge-session-log/spec.md`'s three Requirements that
  describe the frozen estimate pair as current capability behavior.
- Leave `grep -rln "StartBatteryPctEst\|EndBatteryPctEst\|start_battery_pct_est\|end_battery_pct_est"
  internal/charging/ internal/analytics/` returning only this tier's own migration
  file and (until tier 3) nothing under `internal/telemetry/` — this tier's own
  definition of done for the columns it owns.

**Non-Goals (explicitly deferred, do not implement here):**
- Dropping the same two columns from `telemetry.supercharger_history`, or touching
  any file under `internal/telemetry/` — tier 3,
  `RM41-telemetry-drop-estimate-columns`.
- Any change under `internal/gateway/` — tier 1, already archived.
- MAG-45's session status column — tiers 4-5, not started, and this migration does
  not touch `status` in any way.
- Fixing the `internal/analytics/db_integration_test.go` → `charging.supercharger_sessions`
  cross-module test seed itself (the boundary crossing) — the roadmap's own "Future
  work" section marks this a separate, unactioned question for `ai/architecture.md`.
  This tier only removes two arguments from that one INSERT.

## Database Design (design gate — `database`, per `openspec/config.yaml` and `CLAUDE.md` Pipeline config)

### Schema change

`ALTER TABLE ... DROP COLUMN` on two columns of `charging.supercharger_sessions`,
both nullable `SMALLINT` with an inline `CHECK (... BETWEEN 0 AND 100)`. No other
column, index, or constraint on this table is touched.

**Migration file:**
`internal/charging/db/migrations/20260903000002_drop_supercharger_est_columns.sql`

Timestamp `20260903000002` sorts after `20260903000001`
(`internal/telemetry/db/migrations/20260903000001_move_telemetry_to_own_schema.sql`),
the newest migration in the repo at the time this design was written — required
because every module's migrations apply against ONE shared `goose_db_version` table
(`ai/go-conventions.md` §Testing), so version numbers must be globally ordered even
though this migration reads and writes nothing outside `charging`'s own schema.

```sql
-- +goose Up
-- RM41 tier 2 (charging-drop-estimate-columns, MAG-36): drop the two dead
-- battery-percentage ESTIMATE columns from charging.supercharger_sessions.
--
-- WHY THIS IS SAFE (roadmap D1, re-verified by this change, not assumed):
--   - Nothing in this repository has ever written either column. MirrorSuperchargerSession's
--     INSERT column list and ON CONFLICT DO UPDATE SET clause both omit them (db/query.sql);
--     VerifySuperchargerSession's SET clause omits them too. SessionMirror has no field for
--     either, so the nightly sync could not write one even if this exclusion were ever
--     accidentally reversed (RM29 design.md D6 "protection by compile error").
--   - The ONE place either column was ever written is the one-time backfill statement inside
--     the historic 20260823000001_add_charge_sessions.sql migration (never edited — RM39 D1),
--     which copied s.start_battery_pct_est/s.end_battery_pct_est FROM
--     telemetry.supercharger_history AT THAT MIGRATION'S APPLY TIME. Telemetry's own copies
--     are equally permanently NULL (telemetry/db/query.sql's own UPSERT excludes them by the
--     identical guarding-comment pattern), so that backfill only ever copied NULL into NULL.
--     There is no code path, past or present, that put a non-NULL value in either column.
--   - The estimator originally intended to eventually populate them
--     (RM27 D11 deferred it) shipped 2026-09-01 as charging-add-derived-start-battery-pct,
--     writing the real start_battery_pct column instead of a frozen snapshot. The columns
--     are permanently unreachable dead schema, not merely currently-unused schema.
--
-- DROP COLUMN also drops each column's inline CHECK constraint
-- (supercharger_sessions_start_battery_pct_est_check /
-- supercharger_sessions_end_battery_pct_est_check, renamed from the charge_sessions_*
-- originals by 20260902000003) and its COMMENT ON COLUMN — both are attached to the
-- column's own catalog entry and Postgres removes them automatically. No separate
-- DROP CONSTRAINT statement is needed or correct (roadmap D2).
ALTER TABLE charging.supercharger_sessions
    DROP COLUMN start_battery_pct_est,
    DROP COLUMN end_battery_pct_est;

-- +goose Down
-- Reintroduces both columns with their original type and CHECK, matching
-- 20260823000001_add_charge_sessions.sql's original column definitions exactly
-- (the CHECK's auto-generated name will resolve against the table's CURRENT name,
-- supercharger_sessions, which is correct — the table was already renamed by
-- 20260902000003 before this migration ever runs).
--
-- NOT a data-preserving reversal: every value either column ever held (always NULL,
-- per the Up comment above) is gone the moment DROP COLUMN runs. Re-adding the
-- columns here restores the SCHEMA shape only, as two new NULL columns — there is
-- no shadow copy anywhere in this migration to restore actual prior values from.
-- This is consistent with every historic Down in this module (compare
-- 20260902000003's own Down comment) and is not a regression introduced by this
-- migration.
ALTER TABLE charging.supercharger_sessions
    ADD COLUMN start_battery_pct_est SMALLINT CHECK (start_battery_pct_est BETWEEN 0 AND 100),
    ADD COLUMN end_battery_pct_est   SMALLINT CHECK (end_battery_pct_est   BETWEEN 0 AND 100);
```

### Rationale — why this design is right

- **`DROP COLUMN` directly, not a phased/soft-delete approach.** Rejected
  alternatives (both explicitly rejected by the roadmap's owner, D1): (a) a
  verify-then-drop migration that first `SELECT COUNT(*) ... WHERE start_battery_pct_est
  IS NOT NULL` — unnecessary because the write-exclusion is structural (compile-error
  protected on the Go side), not merely "nobody has gotten around to writing it yet";
  the count is a foregone `0`, so paying an extra migration round-trip buys nothing.
  (b) Marking the columns deprecated in a comment and leaving them — this is exactly
  the dead schema MAG-36 asked to remove; it would also leave `charging.Session` at
  20 fields for no behavioral reason, keeping alive the two dead Go fields this
  change is meant to retire.
- **One migration, this module's own `migrations/` directory** — forced by the
  module boundary (`ai/architecture.md` §2), not chosen: `charging` owns
  `charging.supercharger_sessions`; no other module's migration may touch it.
  Mirrors `20260902000003`'s and every prior `charging` migration's placement.
- **No `DROP CONSTRAINT` / no `COMMENT ON COLUMN` cleanup statement** — both are
  child objects of the columns being dropped and Postgres removes them
  automatically as part of `DROP COLUMN` (verified against Postgres's documented
  behavior: dropping a column drops every constraint and comment defined on it).
  Adding a redundant `DROP CONSTRAINT` would either no-op or error depending on
  statement order relative to the `DROP COLUMN` — omitting it is both simpler and
  correct.
- **`ALTER TABLE ... DROP COLUMN` on a nullable column with no other table depending
  on it is a catalog-only, non-blocking-in-practice operation** on PostgreSQL: it
  does not rewrite the table's existing rows or scan them (unlike adding a column
  with a non-null `DEFAULT` computed per-row, or an index rebuild). It takes a brief
  `ACCESS EXCLUSIVE` lock only for the catalog update itself, the same class of
  operation as the `RENAME`/`SET SCHEMA` statements `20260902000003` already
  performs on this table without incident, and there are no cross-module foreign
  keys into `supercharger_sessions` (`ai/architecture.md` §2) to worry about
  cascading.
- **Down is schema-only, not data-preserving, and the migration says so explicitly**
  — consistent with `20260902000003`'s own precedent of stating plainly when a Down
  cannot restore prior state. There is nothing to restore here: every prior value
  was `NULL` (see the Up comment's proof), so a schema-only Down is not a
  degradation from some richer alternative — it is the only truthful Down possible.

### Index plan

**No index is added, changed, or removed by this migration**, and that is the
correct call, justified against this table's actual read patterns
(Performance-Profile: read-heavy):

- The table's one read-serving index, `idx_supercharger_sessions_vehicle_stop
  (account_id, tesla_id, charge_stop_date_time)`, does not reference either
  `start_battery_pct_est` or `end_battery_pct_est` in any form — neither column is
  part of the index key, an included column, or a partial-index predicate. Dropping
  two columns the index never named cannot invalidate or require rebuilding it;
  PostgreSQL does not touch an index's definition when an unrelated column is
  dropped.
- Every read against this table (`ListSessionsByVehicleBetween`,
  `ListSessionsByVehicleUpdatedSince`, `ListSessionsByVehicle`, all in
  `db/query.sql`) uses `SELECT *`. Dropping two columns shrinks the row width each
  of these already-indexed scans returns — a strict, free improvement to I/O and
  network transfer per row, not a regression requiring compensating index work.
- No new query is introduced by this change, so no new access pattern exists to
  index for. The **absence** of new indexing work is itself the read-optimized
  choice here: adding an index would be optimizing a column being deleted.

### What happens to existing rows / data-loss statement

- **No data is lost.** Every row's `start_battery_pct_est` and
  `end_battery_pct_est` value is `NULL` today, in every environment, with no
  exception (see the Up migration's comment for the full proof: no ongoing write
  path exists, and the one historic backfill that named these columns only ever
  copied `NULL` from `telemetry.supercharger_history`'s equally-unwritten copies).
  Dropping a column whose value is uniformly `NULL` discards no information.
- **Every other column on every existing row is completely unaffected** — `DROP
  COLUMN` on PostgreSQL does not rewrite, lock beyond the catalog change, or
  otherwise touch the storage of any other column; `start_battery_pct`,
  `end_battery_pct`, `battery_pct_source`, `inferred_capacity_kwh_calc`, and every
  mirrored/write-once column keep their exact stored values.
- **If this assumption is ever wrong in a specific deployed database** (e.g. a
  hand-run `UPDATE` outside the app set a value directly, which nothing in this
  codebase does or has ever done), that value is genuinely lost on `Up` and is
  **not** recoverable from `Down` (Down only re-adds empty columns — see the
  migration's own Down comment). The roadmap's owner accepted this exact risk
  explicitly when choosing D1 over a verify-then-drop migration: "the evidence is
  already conclusive" that no such value exists. This design does not re-litigate
  that decision; it documents the accepted risk plainly, as `CLAUDE.md`'s
  dev-db-holds-irreplaceable-history caution requires for any destructive DB
  action, even one already decided.

## Code changes — exact edits

### `internal/charging/charging.go`

Remove the two fields from `Session` (currently lines 322-323):

```go
	StartBatteryPctEst *int    // frozen snapshot at verification time; nil = nothing recorded
	EndBatteryPctEst   *int    // frozen snapshot at verification time; nil = nothing recorded
```

Update the doc comment above `Session` (currently lines 284-299): "the session's
time window, the session facts internal/telemetry collects, and the five
charging-owned battery-percentage verification/estimate columns" becomes "... and
the three charging-owned battery-percentage verification columns"; "Twenty fields,
one per supercharger_sessions column" becomes "Eighteen fields, one per
supercharger_sessions column" (20 fields minus the 2 removed = 18 — recount
verified against the struct as edited).

Update the comment immediately above the three remaining verification fields
(currently "// Charging-owned verification channel — never written by the nightly
sync." followed by five field declarations) — the comment text itself does not name
a count and needs no edit; only the two `*Est` field lines below it are deleted.

Update `SessionVerifier`'s doc comment on `VerifySession` (currently):

```go
	// VerifySession updates exactly three columns on one account-scoped supercharger_sessions
	// row — start_battery_pct, end_battery_pct, battery_pct_source — plus updated_at.
	// No other column is reachable through this method, including
	// start_battery_pct_est/end_battery_pct_est: the underlying query's SET clause
	// names only these three plus updated_at, so the two _est columns are structurally
	// unreachable, not merely undocumented as targets (design.md D1).
```

becomes:

```go
	// VerifySession updates exactly three columns on one account-scoped supercharger_sessions
	// row — start_battery_pct, end_battery_pct, battery_pct_source — plus updated_at.
	// No other column is reachable through this method: the underlying query's SET
	// clause names only these three plus updated_at (RM31-charging-add-session-verification-port
	// design.md D1). (start_battery_pct_est/end_battery_pct_est, formerly named here as
	// columns this method could never reach, were dropped from the table entirely by
	// RM41-charging-drop-estimate-columns — there is no longer a column to be unreachable from.)
```

### `internal/charging/session_reader.go`

Remove the two mapping lines from `rowToSession` (currently):

```go
		StartBatteryPctEst: pgInt2ToIntPtr(r.StartBatteryPctEst),
		EndBatteryPctEst:   pgInt2ToIntPtr(r.EndBatteryPctEst),
```

Update the mapping-rules doc comment bullet above it (currently):

```go
//   - StartBatteryPct, EndBatteryPct, StartBatteryPctEst, EndBatteryPctEst:
//     pgtype.Int2 → *int via pgInt2ToIntPtr (service.go).
```

becomes:

```go
//   - StartBatteryPct, EndBatteryPct: pgtype.Int2 → *int via pgInt2ToIntPtr (service.go).
```

### `internal/charging/db/query.sql`

Two guarding comments, both rewritten in place (no SQL statement changes — every
query already omits these columns explicitly or uses `SELECT *`, which shrinks
automatically once the columns are gone).

**`MirrorSuperchargerSession`'s comment**, currently:

```sql
-- name: MirrorSuperchargerSession :exec
-- Upsert one Supercharger session's mirrorable subset. Called once per session, in
-- one transaction, by SessionWriter.MirrorSessions.
--
-- LOAD-BEARING: start_battery_pct, end_battery_pct, battery_pct_source,
-- start_battery_pct_est and end_battery_pct_est are ABSENT from both the INSERT
-- column list and the ON CONFLICT DO UPDATE SET clause. They are human-owned; the
-- nightly sync must never write, clear or overwrite one. Unlike
-- telemetry.UpsertSuperchargerSession — which relies on this comment alone —
-- charging.SessionMirror has no field for them either, so binding one here would not
-- even compile (design.md D6). Do NOT "complete the pattern" by adding them.
--
```

becomes:

```sql
-- name: MirrorSuperchargerSession :exec
-- Upsert one Supercharger session's mirrorable subset. Called once per session, in
-- one transaction, by SessionWriter.MirrorSessions.
--
-- LOAD-BEARING: start_battery_pct, end_battery_pct, and battery_pct_source are ABSENT
-- from both the INSERT column list and the ON CONFLICT DO UPDATE SET clause. They
-- are human-owned; the nightly sync must never write, clear or overwrite one. Unlike
-- telemetry.UpsertSuperchargerSession — which relies on this comment alone —
-- charging.SessionMirror has no field for them either, so binding one here would not
-- even compile (RM29-charging-add-charge-sessions design.md D6). Do NOT "complete
-- the pattern" by adding them. (start_battery_pct_est/end_battery_pct_est, formerly
-- also excluded here, were dropped from the table entirely by
-- RM41-charging-drop-estimate-columns — there is no longer a column to guard.)
--
```

**`VerifySuperchargerSession`'s comment**, currently:

```sql
-- name: VerifySuperchargerSession :one
-- Update the human-owned verification channel on one account-scoped charge session:
-- start_battery_pct, end_battery_pct, and battery_pct_source — plus updated_at. No other
-- column is in this SET clause, INCLUDING start_battery_pct_est/end_battery_pct_est —
-- this is the mirror image of MirrorSuperchargerSession's protection (that query cannot touch
-- these three; this query cannot touch anything else), by the query's shape, not by a
-- comment a reviewer has to notice (design.md D1).
```

becomes:

```sql
-- name: VerifySuperchargerSession :one
-- Update the human-owned verification channel on one account-scoped charge session:
-- start_battery_pct, end_battery_pct, and battery_pct_source — plus updated_at. No
-- other column is in this SET clause — this is the mirror image of
-- MirrorSuperchargerSession's protection (that query cannot touch these three; this
-- query cannot touch anything else), by the query's shape, not by a comment a
-- reviewer has to notice (RM31-charging-add-session-verification-port design.md D1).
-- (start_battery_pct_est/end_battery_pct_est, formerly also named here as columns
-- this SET clause could never reach, were dropped from the table entirely by
-- RM41-charging-drop-estimate-columns.)
```

### Regenerated files

`internal/charging/db/models.go` and `internal/charging/db/query.sql.go` are
regenerated by `make sqlc` (or `sqlc generate`) after the migration and the
`query.sql` comment edits land. They are never hand-edited. Expected effect:
`SuperchargerSession.StartBatteryPctEst`/`EndBatteryPctEst` (both `pgtype.Int2`)
disappear from `models.go`; the `SELECT *`-based query result structs in
`query.sql.go` shrink by the same two fields; the two `MirrorSuperchargerSession`/
`VerifySuperchargerSession` guarding comments in the generated file update to match
`query.sql`'s new text (sqlc copies query-level comments verbatim).

## Test Contract

Author's note: this section, together with proposal.md's "Findings correction",
constitutes the up-front expected-value authoring `ai/go-conventions.md` requires
before implementation. Every occurrence below was located by
`grep -n "StartBatteryPctEst\|EndBatteryPctEst" <file>` against the tip of `main`
at the time this design was written; re-run the same grep during implementation to
confirm nothing drifted.

### 1. `internal/charging/db_session_integration_test.go`

- **`superchargerSessionRow` struct** (currently has `StartBatteryPctEst *int` /
  `EndBatteryPctEst *int` after `BatteryPctSource *string`) — drop both fields.
- **`fetchSuperchargerSession`'s SQL and `Scan` call** — the `SELECT` column list
  drops `start_battery_pct_est, end_battery_pct_est` (currently its own line), and
  the `.Scan(...)` call drops `&row.StartBatteryPctEst, &row.EndBatteryPctEst`
  (currently immediately before `&row.CreatedAt, &row.UpdatedAt`).
- **`TestSessionWriter_MirrorSessions_...` (the function containing the assertion
  block starting `if row.StartBatteryPct != nil {` around the "first mirror, no
  verification yet" case)** — drop the two blocks:
  ```go
  if row.StartBatteryPctEst != nil {
  	t.Errorf("StartBatteryPctEst: want nil, got %v", *row.StartBatteryPctEst)
  }
  if row.EndBatteryPctEst != nil {
  	t.Errorf("EndBatteryPctEst: want nil, got %v", *row.EndBatteryPctEst)
  }
  ```
  Expected value UNCHANGED for the surrounding assertions (`StartBatteryPct`,
  `EndBatteryPct`, `BatteryPctSource` all still want `nil`).
- **The B6-shaped test (re-mirroring a session with direct-SQL-set verified
  percentages)** — its direct-SQL fixture statement currently is:
  ```go
  	UPDATE charging.supercharger_sessions
  	SET start_battery_pct = 41, end_battery_pct = 88, battery_pct_source = 'user_verified',
  	    start_battery_pct_est = 39, end_battery_pct_est = 90
  	WHERE account_id = $1 AND session_id = $2`,
  ```
  becomes:
  ```go
  	UPDATE charging.supercharger_sessions
  	SET start_battery_pct = 41, end_battery_pct = 88, battery_pct_source = 'user_verified'
  	WHERE account_id = $1 AND session_id = $2`,
  ```
  (drop the now-nonexistent-column assignment; a statement setting a dropped
  column is a Postgres error, not a silent no-op — this edit is required for the
  file to run at all, not just to compile). Drop the two assertion blocks:
  ```go
  if row.StartBatteryPctEst == nil || *row.StartBatteryPctEst != 39 {
  	t.Errorf("StartBatteryPctEst: want 39 (untouched by sync), got %v", row.StartBatteryPctEst)
  }
  if row.EndBatteryPctEst == nil || *row.EndBatteryPctEst != 90 {
  	t.Errorf("EndBatteryPctEst: want 90 (untouched by sync), got %v", row.EndBatteryPctEst)
  }
  ```
  Expected value UNCHANGED for `StartBatteryPct`/`EndBatteryPct`/`BatteryPctSource`
  (still want `41`/`88`/`"user_verified"`, still proving the sync never touches the
  three remaining verification columns).

### 2. `internal/charging/db_session_reader_integration_test.go`

In the ascending-order/T9 test (`TestListSessionsByVehicleBetween_...`, the
function whose `wantIDs` slice is `[]int64{940001, 940002, 940003}`):

- Drop the two assertions on `s940002`:
  ```go
  if s940002.StartBatteryPctEst == nil || *s940002.StartBatteryPctEst != 22 {
  	t.Errorf("T9: 940002.StartBatteryPctEst = %v, want 22", s940002.StartBatteryPctEst)
  }
  if s940002.EndBatteryPctEst == nil || *s940002.EndBatteryPctEst != 78 {
  	t.Errorf("T9: 940002.EndBatteryPctEst = %v, want 78", s940002.EndBatteryPctEst)
  }
  ```
  Expected value UNCHANGED for the two remaining T9 assertions on `s940002`
  (`StartBatteryPct` wants `20`, `EndBatteryPct` wants `80`, `BatteryPctSource`
  wants `"user_verified"`).
- In the trailing loop over `[]int64{940001, 940003}`, the condition
  ```go
  if s.StartBatteryPct != nil || s.EndBatteryPct != nil || s.BatteryPctSource != nil ||
  	s.StartBatteryPctEst != nil || s.EndBatteryPctEst != nil {
  ```
  becomes
  ```go
  if s.StartBatteryPct != nil || s.EndBatteryPct != nil || s.BatteryPctSource != nil {
  ```
  Expected value UNCHANGED: the loop still expects all remaining fields nil for
  940001/940003.
- **The fixture that seeds session 940002 with `StartBatteryPctEst`/
  `EndBatteryPctEst` set to 22/78** (wherever the seeding call constructs that
  session — via `charging.Session{...}` or a direct-SQL insert local to this file)
  drops those two fields/columns from the fixture construction. If the fixture is
  built via a shared helper this file also uses for other cases, only the
  941xxx-session literal in question is edited — no other fixture in this file
  names `BatteryPctEst`.

### 3. `internal/charging/db_backfill_integration_test.go`

**Two distinct roles for these columns in this file — only one changes.**

- **`superchargerFixtureRow`'s `StartBatteryPctEst`/`EndBatteryPctEst` fields, and
  `insertSuperchargerSessionFixture`'s INSERT into `telemetry.supercharger_history`
  naming `start_battery_pct_est, end_battery_pct_est`** — **UNCHANGED, do NOT
  edit.** `telemetry.supercharger_history` is not touched by this tier (tier 3's
  table); it still has both columns, and this file's job is seeding THAT table as
  the backfill's source, not asserting against it.
- **The two assertion blocks reading back the BACKFILLED row from
  `charging.supercharger_sessions`** (via `fetchSuperchargerSession`, shared with
  file 1 above), in the percentage-bearing-session branch:
  ```go
  if row.StartBatteryPctEst != nil {
  	t.Errorf("session %d: StartBatteryPctEst got %v, want nil", sessionID, *row.StartBatteryPctEst)
  }
  if row.EndBatteryPctEst != nil {
  	t.Errorf("session %d: EndBatteryPctEst got %v, want nil", sessionID, *row.EndBatteryPctEst)
  }
  ```
  and the identical pair in the no-percentages branch — **both pairs deleted**
  (four `if` blocks total, two per branch). Expected value UNCHANGED for every
  remaining assertion in both branches (`StartBatteryPct`/`EndBatteryPct`/
  `BatteryPctSource` keep their existing want-nil / want-29 / want-100 /
  want-"user_verified" values, per branch).

### 4. `internal/charging/db_session_verifier_integration_test.go`

- **T1 assertions** (`TestVerifySession_T1_T4_T9_...`):
  ```go
  if s1.StartBatteryPctEst != nil {
  	t.Errorf("T1: StartBatteryPctEst = %v, want nil (untouched, D1)", s1.StartBatteryPctEst)
  }
  if s1.EndBatteryPctEst != nil {
  	t.Errorf("T1: EndBatteryPctEst = %v, want nil (untouched, D1)", s1.EndBatteryPctEst)
  }
  ```
  deleted outright. Expected value UNCHANGED for `s1.StartBatteryPct`/
  `EndBatteryPct`/`BatteryPctSource` (want `20`/`80`/`"user_verified"`) and for the
  `UpdatedAt` comparison immediately following.
- **T8 bit-identical-columns assertions**:
  ```go
  if !intPtrEqualV(before.StartBatteryPctEst, after.StartBatteryPctEst) {
  	t.Errorf("StartBatteryPctEst changed: before %v, after %v", before.StartBatteryPctEst, after.StartBatteryPctEst)
  }
  if !intPtrEqualV(before.EndBatteryPctEst, after.EndBatteryPctEst) {
  	t.Errorf("EndBatteryPctEst changed: before %v, after %v", before.EndBatteryPctEst, after.EndBatteryPctEst)
  }
  ```
  deleted outright. Expected value UNCHANGED for every other bit-identical-column
  assertion in this block (`VIN`, `TeslaID`, `ChargeStartDateTime`,
  `ChargeStopDateTime`, `SiteLocationName`, `EnergyKWh`, `TotalCost`, `Currency`,
  `IsPaid`, `CreatedAt`, and whatever follows `EndBatteryPctEst` in the original —
  none of those comparisons' expected outcomes change).

### 5. `internal/analytics/db_integration_test.go` (granted path, roadmap D6)

`seedChargeSession`'s direct SQL, currently:

```go
	_, err := pool.Exec(context.Background(), `
		INSERT INTO charging.supercharger_sessions (
			account_id, vin, tesla_id, session_id,
			charge_start_date_time, charge_stop_date_time,
			site_location_name, energy_kwh, total_cost, currency, is_paid,
			start_battery_pct, end_battery_pct, battery_pct_source,
			start_battery_pct_est, end_battery_pct_est,
			created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		s.AccountID, vin, pgInt8FromPtr(s.TeslaID), sessionID,
		pgtype.Timestamptz{Time: s.ChargeStartDateTime, Valid: true},
		pgtype.Timestamptz{Time: s.ChargeStopDateTime, Valid: true},
		siteLocationName, pgFloat8FromPtr(s.EnergyKWh), pgFloat8FromPtr(s.TotalCost),
		pgTextFromStringPtr(s.Currency), pgBoolFromPtr(s.IsPaid),
		pgInt2FromIntPtr(s.StartBatteryPct), pgInt2FromIntPtr(s.EndBatteryPct),
		pgTextFromStringPtr(batteryPctSource),
		pgInt2FromIntPtr(s.StartBatteryPctEst), pgInt2FromIntPtr(s.EndBatteryPctEst),
		pgtype.Timestamptz{Time: createdAt, Valid: true},
		pgtype.Timestamptz{Time: updatedAt, Valid: true},
	)
```

becomes (18 params → 16; `$14`/`$15` in the old placeholder list shift, and every
placeholder after them renumbers down by two — write the new list literally as
`$1..$16`, do not attempt to preserve old numbers with gaps):

```go
	_, err := pool.Exec(context.Background(), `
		INSERT INTO charging.supercharger_sessions (
			account_id, vin, tesla_id, session_id,
			charge_start_date_time, charge_stop_date_time,
			site_location_name, energy_kwh, total_cost, currency, is_paid,
			start_battery_pct, end_battery_pct, battery_pct_source,
			created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		s.AccountID, vin, pgInt8FromPtr(s.TeslaID), sessionID,
		pgtype.Timestamptz{Time: s.ChargeStartDateTime, Valid: true},
		pgtype.Timestamptz{Time: s.ChargeStopDateTime, Valid: true},
		siteLocationName, pgFloat8FromPtr(s.EnergyKWh), pgFloat8FromPtr(s.TotalCost),
		pgTextFromStringPtr(s.Currency), pgBoolFromPtr(s.IsPaid),
		pgInt2FromIntPtr(s.StartBatteryPct), pgInt2FromIntPtr(s.EndBatteryPct),
		pgTextFromStringPtr(batteryPctSource),
		pgtype.Timestamptz{Time: createdAt, Valid: true},
		pgtype.Timestamptz{Time: updatedAt, Valid: true},
	)
```

No other line in `internal/analytics/db_integration_test.go` names
`BatteryPctEst` (confirmed by `grep -n "BatteryPctEst" internal/analytics/db_integration_test.go`
returning exactly this one line at the time of writing) — no other assertion or
fixture in this file changes. No file under `internal/analytics/` other than this
one is touched (roadmap D6).

## Docs

### `internal/charging/AGENTS.md` (module doc — `CLAUDE.md` docs-track-change rule)

Five locations, each an in-place text replacement (exact grep-locatable anchors
given so the acceptance check in tasks.md can confirm each landed):

1. **The `SessionMirror` exclusion note** (anchor: `single most important invariant
   in this file`) — "`SessionMirror` has NO field for `start_battery_pct`,
   `end_battery_pct`, `battery_pct_source`, `start_battery_pct_est` or
   `end_battery_pct_est`" becomes "`SessionMirror` has NO field for
   `start_battery_pct`, `end_battery_pct`, or `battery_pct_source`"; "The five
   battery-percentage columns are absent from `SessionMirror`" becomes "The three
   battery-percentage columns are absent from `SessionMirror`".
2. **The `Session` struct code block** (anchor: `Charging-owned verification
   channel — never written by the nightly sync.`) — delete the two lines
   `StartBatteryPctEst *int` / `EndBatteryPctEst   *int` from the fenced `go`
   block, matching charging.go's own edit above exactly (this code block is a
   documentation mirror of the real struct, not generated — it must be kept
   byte-identical to the real type by hand).
3. **`SessionVerifier`'s doc comment** (anchor: `No other column is reachable
   through this method, including`) — apply the identical rewrite given for
   `charging.go`'s `VerifySession` comment above (this block is a second, embedded
   copy of the same comment).
4. **The Data Ownership section's lead sentence** (anchor: `keeps its own copy of
   all five`) — "`internal/telemetry.supercharger_sessions` keeps its own copy of
   all five battery-percentage columns until a separate, deferred contract change
   drops them" becomes "`internal/telemetry.supercharger_history` keeps its own
   copy of the three remaining battery-percentage columns (`start_battery_pct`,
   `end_battery_pct`, `battery_pct_source`) until tier 3 of this roadmap
   (`RM41-telemetry-drop-estimate-columns`) drops its own est-column pair — see
   that change once it lands." The following sentence naming
   `start_battery_pct_est` / `end_battery_pct_est` as "genuinely exist[ing] in two
   tables" is deleted (only three columns exist in two tables now, not five).
5. **The column-by-column bullet list** — delete the entire
   `start_battery_pct_est`, `end_battery_pct_est` bullet ("**charging-owned**,
   never mirrored, and still unwritable by any code path... Roadmap Decision 3
   (RM31): no estimator exists yet..."). Nothing replaces it — there is no column
   left to describe. The preceding bullet (the three-column verification channel)
   is unchanged; the following bullet (the closed "deliberately not carried" list)
   is unchanged.

**Not edited, deliberately:** the D16/rename-scope sentence in the Data Ownership
intro ("the five CHECKs Postgres auto-named from inline column constraints
(`battery_pct_source`, `start`/`end_battery_pct`, and their `_est` siblings)") is a
historically accurate description of what the September 2026 schema-move migration
renamed at the time it ran — it remains true as a historical statement and is not
an ongoing claim that those two constraints still exist today. Leaving it matches
this repo's existing convention of not editing historic migration commentary
(`ai/go-conventions.md` — "historic migrations are never edited," applied here to a
historical fact about one).

### `kkpa/context/use-case/charging/verify-session-battery.md`

Replace (currently, two lines under the bullet list):

```
- **`start_battery_pct_est` / `end_battery_pct_est` are unreachable from every write path in the
  repo** and therefore always render `—`. They await an estimator that does not exist yet.
  _Source: `charging/db/query.sql`, `superchargerRowVMFromSession`._
```

With:

```
- **`start_battery_pct_est` / `end_battery_pct_est` no longer exist.** Both columns
  were dropped from `charging.supercharger_sessions` by
  `RM41-charging-drop-estimate-columns` (2026-09-03) — they had been unreachable
  from every write path since they shipped (no estimator was ever built to fill
  them), and the estimator MAG-36 eventually shipped
  (`derivedStartBatteryPct`) writes the real `start_battery_pct` column instead of
  a frozen snapshot column. `charging.Session` no longer carries either field.
  _Source: `charging/charging.go`, `charging/db/migrations/20260903000002_drop_supercharger_est_columns.sql`._
```

This is the correction the roadmap's own "Future work" section assigns to this
tier explicitly, restated here as the binding literal.

### `kkpa/context/workflows/supercharger-stats-read.md`

Tier 1 deliberately left one forward-looking clause in its own correction ("There
is no longer an estimate column in the gateway" bullet), reading in part:
"`charging.Session` still carries the two fields until tier 2 of
`RM41-supercharger-battery-pct-cleanup` drops the columns; the gateway simply
stopped naming them." Replace that trailing clause:

Before: "`charging.Session` still carries the two fields until tier 2 of
`RM41-supercharger-battery-pct-cleanup` drops the columns; the gateway simply
stopped naming them."

After: "`charging.Session` no longer carries either field either, since tier 2
(`RM41-charging-drop-estimate-columns`, 2026-09-03) dropped both columns from
`charging.supercharger_sessions`; the gateway had already stopped naming them one
tier earlier."

### `kkpa/context/architecture/charge-record-mutation.md` (found by this worker — see proposal.md "Findings correction")

Replace (currently, in the bullet list, between the OOB-refresh bullet and the
`updated_at`-is-not-a-signal bullet):

```
- **`start_battery_pct_est` / `end_battery_pct_est` are never written** — excluded from
  `VerifySuperchargerSession`, `MirrorSuperchargerSession` and telemetry's own upsert. They are rendered as
  `StartBatteryPctEstLabel` / `EndBatteryPctEstLabel` and always show `—`.
  _Source: `charging/db/query.sql`, `gateway/handlers/supercharger.go`
  `superchargerRowVMFromSession`._
```

With:

```
- **`start_battery_pct_est` / `end_battery_pct_est` no longer exist.** They were
  never written (excluded from `VerifySuperchargerSession`, `MirrorSuperchargerSession`,
  and telemetry's own upsert) for their entire lifetime, and were dropped from
  `charging.supercharger_sessions` by `RM41-charging-drop-estimate-columns`
  (2026-09-03); the gateway had already stopped rendering them one tier earlier
  (`RM41-gateway-revise-battery-pct-ui`).
  _Source: `charging/charging.go`, `charging/db/migrations/20260903000002_drop_supercharger_est_columns.sql`._
```

## Spec delta — `openspec/specs/charge-session-log/spec.md`

Three Requirements describe "an optional frozen pair of estimated percentages" as
currently-specified capability behavior. All three get a `MODIFIED Requirements`
entry in this change's own `specs/charge-session-log/spec.md` (full replacement
text, per OpenSpec convention — see that file in this change for the complete
before/after of every paragraph and scenario). Summary of what changes:

1. **"Battery Percentage Verification On A Charge Session"** — "an optional
   provenance stating where those percentages came from, and an optional frozen
   pair of estimated percentages captured at the moment of verification" loses its
   frozen-pair clause; "these five values" becomes "these three values"; the
   sentence "The frozen estimate pair SHALL never be refreshed after it is first
   recorded" is deleted (nothing left to refresh); the "Synchronization never
   overwrites a verified percentage" scenario's GIVEN drops "and frozen estimates"
   and its THEN changes "all five values" to "all three values".
2. **"Charge Sessions Are Retrievable For A Vehicle Within A Time Window"** — the
   requirement's "and their frozen estimated pair" clause is dropped from the list
   of what each retrieved record carries; the "A retrieved record carries its full
   session detail" scenario's GIVEN drops "and frozen estimates".
3. **"A Charge Session's Battery Percentages Are Correctable By A Human"** — the
   "every other fact ... SHALL be unaffected" paragraph drops ", and its frozen
   estimated percentages"; the "A correction never alters the session's other
   facts" scenario's GIVEN and THEN both drop "and frozen estimated percentages".

`openspec/specs/charging/spec.md` is explicitly **not** touched: its one mention of
the two `_est` columns (in "Supercharger Sessions Table Renamed") is a factual list
of what the September 2026 rename migration renamed at the time — a historical
statement, not an ongoing assertion that those two constraints still exist (the
requirement's own completeness-criterion scenario asserts only "no name beginning
`charge_sessions`", which stays true whether the two auto-named checks exist under
their post-rename name or not at all).

## Risks

- **Five test files, several with shared helpers (`fetchSuperchargerSession` used
  by both file 1 and file 3 above)** — the Test Contract above gives each file's
  exact before/after so no assertion is invented or guessed; `go vet
  ./internal/charging/... ./internal/analytics/...` (compiles `_test.go` files) is
  the cheap signal that every occurrence was actually caught, not just the ones
  enumerated. The acceptance grep in tasks.md's final wave is the authoritative
  completeness check.
- **The B6-shaped test's direct-SQL `UPDATE` naming the dropped columns is a
  correctness requirement, not just a compile requirement** — unlike a Go struct
  literal (which simply fails to compile if a removed field is named), a raw SQL
  statement setting a column that no longer exists is a runtime Postgres error
  (`column "start_battery_pct_est" of relation "supercharger_sessions" does not
  exist`), so this edit must land in the same commit as the migration, not be
  deferred.
- **The `internal/analytics` granted path is one file only** — the roadmap (D6)
  and this design both scope it to `db_integration_test.go`; no other file under
  `internal/analytics/` may be touched by this tier, even if a tempting adjacent
  cleanup is spotted.

**This tier trips the `database` design gate.** Migration SQL, rationale, index
plan, and data-loss statement are all above under "Database Design" — the leader
shows this section to the owner and iterates until confirmed before any
implementation task in tasks.md may start.
