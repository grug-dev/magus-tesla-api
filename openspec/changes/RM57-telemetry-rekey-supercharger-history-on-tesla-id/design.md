# Design — RM57-telemetry-rekey-supercharger-history-on-tesla-id

Source ticket: MAG-67. Roadmap tier 1.
This change touches the database, so this file is the design gate. It carries
the full schema, the rationale, the index plan justified against queries that
exist today, and the expected values of the tests — authored before the
implementation.

## Overview

`telemetry.supercharger_history` stops being an account-scoped table and
becomes a vehicle-scoped one. Three things move together, and none of them
works without the others:

1. The column `account_id` is dropped and `tesla_id` becomes `NOT NULL`.
2. The read port loses its two account-wide methods and the `accountID`
   parameter on the three that survive.
3. The write path stops storing a session whose VIN is not a registered
   vehicle, and counts each skip.

The roadmap already settled the first point with the owner (RD1/RD2). This
file records what follows from it.

---

## Decisions

### D1 — `tesla_id NOT NULL`, `account_id` dropped (roadmap RD1, not re-opened)

Nothing but `account_id` tied a row to an account. `vin` reaches an account
only through `account.vehicles`, which lists registered vehicles only. After
the drop, a row with `tesla_id IS NULL` is unreachable by every query the
module has. Keeping the column nullable would store rows nobody can read.

### D2 — The migration deletes `tesla_id IS NULL` rows first (roadmap RD2)

`ALTER COLUMN tesla_id SET NOT NULL` fails if any row holds NULL. The owner
checked the live database before this change: `telemetry.supercharger_history`
has **0** such rows. So the `DELETE` is a guard against a row appearing
between that check and the migration running, not a data migration. On the
owner's database today it deletes nothing.

**The down-migration cannot bring a deleted row back.** It restores the shape
— a nullable `account_id` column, a nullable `tesla_id`, the three old indexes
— and nothing else. The deleted rows and every `account_id` value on every
surviving row are gone for good. This is stated here because "down" usually
implies "undo", and here it does not.

### D3 — `SuperchargerHistoryByAccount` is removed, not re-keyed

The roadmap's tier-1 scope says "every `SuperchargerHistoryReader` method
drops its `accountID` parameter". For this method that is not possible: its
whole predicate is `WHERE account_id = $1`. Dropping the parameter leaves
"every row in the table", which is not a read any caller wants and would be a
tenant leak if one ever used it.

It has **no production caller**. The only references outside the interface,
its implementation, and its logging decorator are telemetry's own tests.
Removing it is therefore free, and keeping a parameterless whole-table scan
would be worse than free. Its per-vehicle sibling `SuperchargerHistoryByVehicle`
covers every access pattern the module still needs.

### D4 — `SuperchargerHistoryByAccountUpdatedSince` is removed too

Same mechanical reason — `WHERE account_id = $1` has no column left. But this
one also loses its *purpose*.

It exists for orphan recovery: a session whose vehicle is not currently
registered has `tesla_id IS NULL`, a per-vehicle read can never see it, so an
account-wide read was how `internal/charging`'s mirror recovered it once the
vehicle re-registered. D1 deletes those rows and forbids new ones, so there is
no orphan left to recover. The method's reason to exist is removed by the same
decision that removes its predicate.

This reverses what `20260906000002_add_mirror_watermarks.sql` says in its own
comment about orphan recovery, and what MAG-63 wrote about keying on `vin`.
The roadmap corrects both on purpose.

**Lost coverage, stated plainly.** Seven tests are deleted with the method
(`db_supercharger_account_updated_since_integration_test.go`). Six of them
asserted behaviour that no longer exists in any form: cross-vehicle results in
one read, an orphan row being included, tenant isolation by `account_id`, and
the account-wide EXPLAIN plan. The seventh — ordering and boundary-inclusive
`since` — survives as the same assertions on
`SuperchargerHistoryByVehicleUpdatedSince` (see the test contract, T-8).

### D5 — Skipping an unregistered VIN is counted (roadmap RD5)

`collectChargingHistory` skips a session whose VIN is not in the account's
VIN→TeslaID map. A silent skip would hide a row Tesla returned and we chose
not to keep. The count goes on `CycleReport` as
`ChargingSessionsSkippedUnregistered`, mirroring the existing
`ChargingFetchFailures` shape, and `LogCycle` prints it as
`charging_skipped_unregistered=N`.

The skip is per session, not per account, so the counter is a session count —
parity with `ChargingSessionsUpserted` on the same line.

### D6 — No `poll_runs` column for the new counter

`poll_runs` has one column per `CycleReport` charging counter, and its stated
purpose is to reproduce the per-cycle log line without a join. Adding
`ChargingSessionsSkippedUnregistered` to the log line without a column breaks
that parity a little.

It is still not added, for one reason: **the column can be added later at zero
cost.** A future `ALTER TABLE … ADD COLUMN charging_sessions_skipped_unregistered
INTEGER NOT NULL DEFAULT 0` backfills every historic row with `0`, which is the
true value for every run that predates the skip. Nothing is lost by waiting and
nothing is gained by adding it now. The roadmap asks for a `CycleReport` count;
this change delivers exactly that.

Trade-off accepted: until that column exists, the skip count lives only in the
log line and in the value `CollectAll` returns.

### D7 — `internal/app` needs a bridge in this change, and it is leader-owned

`internal/app/processor.go:223` is the port's only production caller. It reads
`SuperchargerHistoryByAccountUpdatedSince(ctx, v.AccountID, cursor - overlap)`
once per account, inside a loop that dedupes vehicles down to one pass per
account.

D4 removes that method, so `go build ./...` fails until `internal/app` changes.
Three candidate orderings were considered:

1. **Leave the build red until roadmap tier 3.** Rejected. Tier 2's worker
   would face a broken `go build ./...` it did not cause and cannot fix, and
   the project's cheap deterministic signals stop working for two whole tiers.
2. **Add a plural batch method** `SuperchargerHistoryByVehiclesUpdatedSince(ctx,
   teslaIDs []int64, since)`. Rejected. The roadmap's "facts already checked"
   says the existing per-vehicle method is enough and no new method is needed.
   A new port method invented here would be dead the moment tier 3 lands.
3. **Fan out over the account's vehicles, keeping the account-keyed
   watermark.** Chosen. Inside the existing per-account pass, call
   `SuperchargerHistoryByVehicleUpdatedSince(ctx, v.TeslaID, cursor - overlap)`
   once per vehicle of that account and concatenate the results. The watermark
   read, the watermark advance, `charging.SessionMirror{AccountID: …}` and
   `MirrorSessions(ctx, accountID, …)` all stay exactly as they are.

Why option 3 is correct and not just convenient: the per-account watermark
still bounds a per-account unit of work, because every vehicle of the account
is read in the same pass and the cursor advances once, after all of them. The
result set is the same set of rows as today minus the orphans, which D1 has
already deleted. Tier 3 then replaces the account grouping and re-keys the
watermark on `tesla_id`; this bridge is the step before that, not a competing
design.

**A full per-vehicle rewrite cannot happen in this change.** It needs
`charging.mirror_watermarks` keyed on `tesla_id`, which is roadmap tier 2.
Advancing an account-keyed cursor once per vehicle would move the account's
cursor past rows of its other vehicles, and those rows would never be mirrored
again — a silent, unrecoverable loss. That is why the roadmap puts the rewrite
in tier 3 and why this change stops at the bridge.

### D8 — The table `COMMENT` is not edited

`COMMENT ON TABLE telemetry.supercharger_history` still says "per account".
This project does not ship a migration to correct comment text, and a comment
that drifts into `models.go` is not a defect here. Left as it stands,
deliberately.

---

## Schema

### Current shape (before this change)

Table `telemetry.supercharger_history`, 22 columns. The parts this change
touches:

| Object | Definition today |
|---|---|
| `account_id` | `UUID NOT NULL` |
| `tesla_id` | `BIGINT` (nullable) |
| `supercharger_history_pkey` | `PRIMARY KEY (id)` |
| `supercharger_history_session_id_unique` | `UNIQUE (session_id)` |
| `idx_supercharger_history_vehicle_time` | `(account_id, tesla_id, charge_start_date_time DESC)` |
| `idx_supercharger_history_account_time` | `(account_id, charge_start_date_time DESC)` |
| `idx_supercharger_history_account_updated` | `(account_id, updated_at)` |

### Migration — up

File: `internal/telemetry/db/migrations/20260912000001_rekey_supercharger_history_on_tesla_id.sql`

```sql
-- +goose Up
-- A row with tesla_id IS NULL is a session whose VIN is not a registered
-- vehicle. Once account_id is gone, nothing ties such a row to an account and
-- no query can reach it again. Delete first, then forbid new ones. Checked on
-- the live database before writing this: zero such rows, so this deletes
-- nothing today. It is a guard against a row arriving before the migration
-- runs.
DELETE FROM telemetry.supercharger_history
WHERE tesla_id IS NULL;

ALTER TABLE telemetry.supercharger_history
    ALTER COLUMN tesla_id SET NOT NULL;

-- Schema-qualified: an index name resolves through search_path, and goose
-- does not guarantee telemetry is on it.
DROP INDEX telemetry.idx_supercharger_history_vehicle_time;
DROP INDEX telemetry.idx_supercharger_history_account_time;
DROP INDEX telemetry.idx_supercharger_history_account_updated;

ALTER TABLE telemetry.supercharger_history
    DROP COLUMN account_id;

-- Per-vehicle session list, newest first. tesla_id equality and
-- charge_start_date_time DESC together serve the WHERE and the ORDER BY in
-- one range scan, with no sort step.
CREATE INDEX idx_supercharger_history_vehicle_time
    ON telemetry.supercharger_history (tesla_id, charge_start_date_time DESC);

-- Per-vehicle "changed since" read, which bounds the nightly mirror.
-- tesla_id prunes to the car; updated_at ascending satisfies both the
-- >= predicate and the ORDER BY, so the planner needs no sort step.
CREATE INDEX idx_supercharger_history_vehicle_updated
    ON telemetry.supercharger_history (tesla_id, updated_at);
```

### Migration — down

```sql
-- +goose Down
-- NOT an undo. Rows the Up migration deleted are gone, and every surviving
-- row's account_id went with the column. Down restores the SHAPE only:
-- account_id comes back NULLABLE and empty, so the three account-scoped
-- indexes it recreates index nothing until something refills the column.
DROP INDEX IF EXISTS telemetry.idx_supercharger_history_vehicle_updated;
DROP INDEX IF EXISTS telemetry.idx_supercharger_history_vehicle_time;

ALTER TABLE telemetry.supercharger_history
    ADD COLUMN account_id UUID;

ALTER TABLE telemetry.supercharger_history
    ALTER COLUMN tesla_id DROP NOT NULL;

CREATE INDEX idx_supercharger_history_vehicle_time
    ON telemetry.supercharger_history (account_id, tesla_id, charge_start_date_time DESC);

CREATE INDEX idx_supercharger_history_account_time
    ON telemetry.supercharger_history (account_id, charge_start_date_time DESC);

CREATE INDEX idx_supercharger_history_account_updated
    ON telemetry.supercharger_history (account_id, updated_at);
```

`account_id` comes back as `UUID` (nullable), not `UUID NOT NULL`: the old
values are gone, so there is nothing to backfill a `NOT NULL` column with.
Same reasoning as `20260911000002_rekey_vehicle_snapshots_on_tesla_id.sql`'s
own down migration.

### Index plan

The rule this plan follows (MAG-63, binding on every RM57 tier): a
`UNIQUE (a, b)` constraint already builds a btree serving equality on `a`,
point lookups, range scans on `b` within one `a`, and `ORDER BY b DESC`. Never
add an index it already covers. An index nothing reads today is dropped, not
kept "just in case".

| Index | Action | Shape after | The query that reads it |
|---|---|---|---|
| `supercharger_history_pkey` | keep, untouched | `(id)` | row identity; no query filters on `id` — it is the table's surrogate key |
| `supercharger_history_session_id_unique` | keep, untouched | `UNIQUE (session_id)` | `UpsertSuperchargerHistory` — the `ON CONFLICT (session_id)` target. Also the only point-lookup path by session id. |
| `idx_supercharger_history_vehicle_time` | replace | `(tesla_id, charge_start_date_time DESC)` | `SuperchargerHistoryByVehicle` (`query.sql`) — `WHERE tesla_id = $1 ORDER BY charge_start_date_time DESC LIMIT n`. Exact match: equality on the leading column, ordered range on the second, no sort step. Also prunes `SuperchargerHistoryByVehicleBetween` to one car's rows. |
| `idx_supercharger_history_account_time` | **drop, no replacement** | — | Its only reader was `SuperchargerHistoryByAccount`, removed by D3. No other query filters on `charge_start_date_time` without a `tesla_id`. |
| `idx_supercharger_history_account_updated` | replace | `idx_supercharger_history_vehicle_updated (tesla_id, updated_at)` | `SuperchargerHistoryByVehicleUpdatedSince` (`query.sql`) — `WHERE tesla_id = $1 AND updated_at >= $2 ORDER BY updated_at ASC`. Exact match: `tesla_id` prunes, `updated_at` ascending serves both the range predicate and the ORDER BY. |

Three notes on why this is the whole list:

- **Why `UNIQUE (session_id)` does not cover the two new indexes.** It is
  keyed on `session_id` alone. Every surviving read filters on `tesla_id`,
  which that index does not contain, so it cannot prune to a vehicle at all.
- **Why `idx_supercharger_history_vehicle_updated` is added rather than left
  out.** Its account-keyed predecessor was added deliberately by the owner at
  a design gate (`20260906000001`, MAG-48) to remove the risk of a slow
  nightly mirror read. The query it protects is not removed — it is re-keyed
  and, after D7, becomes the mirror's only read. Dropping the protection while
  keeping the read would undo that decision by accident. This is a re-key of an
  existing index, not a new one.
- **Why no `(tesla_id, charge_stop_date_time)` index for
  `SuperchargerHistoryByVehicleBetween`.** That query filters on
  *stop* time while the vehicle_time index is sorted on *start* time, so the
  stop-time predicate stays a residual filter inside a scan already pruned to
  one vehicle. The change that added the method decided against a third index
  for exactly this reason, and nothing has changed: the method still has **no
  production caller**, and its window is caller-bounded. Revisit only if a real
  caller appears and per-vehicle session volume grows.

### Makefile, guards and codegen — checked, with findings

| Thing checked | Finding |
|---|---|
| `MIGRATIONS_DIRS` order (`account telemetry charging analytics`) | **Unaffected, and verified — this is not an assumption.** No later module's migration reads `telemetry.supercharger_history`. `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql` backfills from `public.supercharger_sessions` behind a `to_regclass` guard; that relation stopped existing when RM39 moved the table into the `telemetry` schema, so the guard short-circuits. `internal/analytics`' only cross-module migration read is `telemetry.vehicle_snapshots` (`20260908000002`), not this table. So the cross-module DROP hazard that blocks `vehicle_snapshots.account_id` (MAG-76) does **not** apply here. |
| `make migration-guard` | Version `20260912000001` is free across all four module directories — today's highest anywhere is `20260911000002`. Roadmap tier 2 must take `20260912000002` and higher. |
| `sqlc` inputs | `sqlc.yaml`'s telemetry entry points `schema:` at `internal/telemetry/db/migrations` and `queries:` at `internal/telemetry/db/query.sql`. Both change, neither moves. The `telemetry_supercharger_history: "SuperchargerHistory"` rename key still matches — the table is not renamed. Run `make sqlc` after the migration and the query edits. |
| `make db-setup` / `db-reset` role and ownership | Unaffected. No new schema, no new table, no new role. |
| `make boundary-guard` | Unaffected. No `internal/telemetry` import is added to `internal/gateway`. |
| `make archive-guard` | Nothing under `openspec/changes/archive/` is edited. The stale mentions of `account_id` in archived design docs are the record of what was decided then, and stay. |
| `make tz-guard`, `money-guard`, `i18n-guard`, `ui-guard` | Unaffected — no time, money or user-facing string is touched. |

---

## Queries (`internal/telemetry/db/query.sql`)

| Query | Change |
|---|---|
| `UpsertSuperchargerHistory` | Remove `account_id` from the INSERT column list and from `VALUES`. Remove `account_id` from **both** `'{…}'::text[]` deny-list arrays in the `updated_at` CASE. `@tesla_id` becomes a plain `BIGINT` parameter (sqlc infers non-null from the column). The load-bearing comment about the human-owned battery-% trio stays word for word. |
| `SuperchargerHistoryByAccount` | **Delete** (D3). |
| `SuperchargerHistoryByVehicle` | `WHERE tesla_id = @tesla_id` only. |
| `SuperchargerHistoryByVehicleBetween` | Drop the `account_id` predicate; the rest of the WHERE and the ORDER BY are unchanged. |
| `SuperchargerHistoryByVehicleUpdatedSince` | Drop the `account_id` predicate. Update its "Index reuse" comment: it now has an exactly matching index and is no longer a residual-filter scan. |
| `SuperchargerHistoryByAccountUpdatedSince` | **Delete** (D4). |

After `make sqlc`, the generated `telemetrydb.SuperchargerHistory` model loses
`AccountID` and its `TeslaID` becomes `int64`. Every `…Params` struct for the
three surviving queries loses `AccountID`, and `TeslaID` becomes `int64`.

The deny-list in `UpsertSuperchargerHistory` goes from 16 columns to 15. The
SET list stays at 6. The live table goes from 22 columns to 21, so the
self-checking schema test's partition still balances: 6 + 15 = 21.

---

## Ports and implementation

### Domain type (`telemetry.go`)

```go
type SuperchargerHistory struct {
    ID                  uuid.UUID
    SessionID           int64
    VIN                 string
    TeslaID             int64 // always a registered vehicle at write time
    // … every other field unchanged …
}
```

`AccountID` is removed: there is no column left to map it from.
`TeslaID` becomes a value, not a pointer, because the column is `NOT NULL`.
The pointer used to carry "VIN is not a registered vehicle"; D1 and D5 remove
that state from the table, so the pointer has nothing left to express.

### `SuperchargerHistoryReader` (`telemetry.go`)

```go
type SuperchargerHistoryReader interface {
    SuperchargerHistoryByVehicle(ctx context.Context, teslaID int64, limit int) ([]SuperchargerHistory, error)
    SuperchargerHistoryByVehicleBetween(ctx context.Context, teslaID int64, start, end time.Time) ([]SuperchargerHistory, error)
    SuperchargerHistoryByVehicleUpdatedSince(ctx context.Context, teslaID int64, since time.Time) ([]SuperchargerHistory, error)
}
```

Five methods become three. The doc comment on
`SuperchargerHistoryByVehicleUpdatedSince` loses the sentence contrasting it
with the account-wide method, and the comment saying the account-wide method is
the only one that can return `tesla_id IS NULL` is deleted with the method.

### `reader.go`

The three surviving methods drop `accountID` from their signature and from the
`…Params` literal. `TeslaID` is assigned directly as `int64`, so
`teslaIDToPgInt8` loses its last three call sites — **delete the helper**
(`service.go:747`); nothing else uses it.

### `service.go`

- `upsertSuperchargerHistory`: drop `AccountID` from the params literal, and
  replace the nullable `pgtype.Int8` wrap with the plain `s.TeslaID`.
- `collectChargingHistory`: the VIN lookup becomes a guard.

```go
for _, session := range history.Data {
    // Tesla returns sessions for cars that are no longer registered here.
    // The table has no column that could hold one, so it is dropped, not
    // stored with a hole. The count makes that visible in the nightly line.
    teslaID, registered := vinToTeslaID[session.VIN]
    if !registered {
        report.ChargingSessionsSkippedUnregistered++
        continue
    }
    …
}
```

The `domainSession` literal drops `AccountID` and assigns `TeslaID: teslaID`.

### `query_log.go`

`loggingSuperchargerHistoryReader` drops the two removed methods. The three
survivors drop `account=%s` from their log line and their signature. The
`upsertSuperchargerHistory` decorator's line drops `account=%s` too, and its
`tesla_id` formatting stops being nil-safe — it is now a plain `%d`.

### `report.go`

`LogCycle` gains `charging_skipped_unregistered=%d` after
`charging_failures=%d`.

---

## Test contract — expected values, authored before the implementation

These are the values the design says the tests must produce. A test written
after reading the implementation confirms what the code does, not what the
design specifies.

### Offline (pure Go, no database) — write these first

**T-1 — a session for an unregistered VIN is skipped and counted.**
Given `owned = [{VIN: "VIN_A", TeslaID: 111}]` and a charging history of three
sessions: `(VIN_A, session 1)`, `(VIN_B, session 2)`, `(VIN_A, session 3)`.
When `collectChargingHistory` runs, then the fake store receives exactly **2**
upserts, for sessions 1 and 3, both with `TeslaID == 111`; session 2 reaches
the store **not at all**; `report.ChargingSessionsUpserted == 2`;
`report.ChargingSessionsSkippedUnregistered == 1`;
`report.ChargingFetchFailures == 0`.

**T-2 — every VIN unregistered.**
Given `owned = []` and a history of 2 sessions. Then the store receives **0**
upserts; `ChargingSessionsUpserted == 0`;
`ChargingSessionsSkippedUnregistered == 2`; `ChargingFetchFailures == 0`; no
error is returned and the cycle continues.

**T-3 — a fetch failure is unchanged by this change.**
Given `ChargingHistory` returns an error. Then `ChargingFetchFailures == 1`,
`ChargingSessionsSkippedUnregistered == 0`, `ChargingSessionsUpserted == 0`.
The skip counter must not absorb a fetch failure.

**T-4 — per-session isolation still holds alongside the skip.**
Given `owned = [{VIN_A, 111}]` and sessions `(VIN_A, 1)`, `(VIN_B, 2)`,
`(VIN_A, 3)`, where the store returns an error for session 1. Then
`ChargingSessionsUpserted == 1` (session 3),
`ChargingSessionsSkippedUnregistered == 1` (session 2), and no error is
returned.

**T-5 — the log line carries the new counter.**
Given a `CycleReport` with `ChargingSessionsUpserted: 4`,
`ChargingFetchFailures: 1`, `ChargingSessionsSkippedUnregistered: 2`. Then the
`LogCycle` output contains
`charging_upserted=4 charging_failures=1 charging_skipped_unregistered=2`, in
that order.

**T-6 — the query-log decorators no longer print an account.**
Given `SuperchargerHistoryByVehicle(ctx, 810100, 5)` returning 2 rows. Then the
logged line is exactly
`telemetry query: SuperchargerHistoryByVehicle tesla_id=810100 limit=5 rows=2`
— no `account=` field anywhere on it.

### Database-backed (`TEST_DATABASE_URL`-gated) — write these last

They cannot compile before the migration and the sqlc types exist.

**T-7 — `SuperchargerHistoryByVehicle` scopes by vehicle alone.**
Seed three rows: `tesla_id=111` with `charge_start_date_time` 2026-08-01T10:00Z
(session 7001), `tesla_id=111` at 2026-08-03T10:00Z (session 7002),
`tesla_id=222` at 2026-08-02T10:00Z (session 7003). Query
`SuperchargerHistoryByVehicle(ctx, 111, 10)`. Expect exactly 2 rows, in the
order `[7002, 7001]` (newest first). Session 7003 does not appear.

**T-8 — `SuperchargerHistoryByVehicleUpdatedSince` ordering and boundary.**
Seed three rows for `tesla_id=111` with `updated_at` `t1 = 2026-08-01T00:00Z`,
`t2 = 2026-08-02T00:00Z`, `t3 = 2026-08-03T00:00Z` (sessions 7101, 7102,
7103), plus one row for `tesla_id=222` at `t2` (session 7104). Then:
`since = t2` returns exactly `[7102, 7103]`, in that order (oldest first);
`since = t3` returns exactly `[7103]`; `since = t3 + 1us` returns an **empty,
non-nil** slice and no error. Session 7104 never appears.

The step past `t3` is one **microsecond**, not one nanosecond. Postgres stores
`timestamptz` to microsecond resolution, and pgx truncates anything finer on
encode, so `t3 + 1ns` would arrive as plain `t3` and the inclusive `>=` bound
would correctly return 7103. One microsecond is the smallest step the column
can represent, so it is the smallest step that tests the bound at all.

**T-9 — the per-vehicle updated-since read uses its index and does not sort.**
Run `EXPLAIN` on the same query. The plan names
`idx_supercharger_history_vehicle_updated` and contains **no** `Sort` node.
This replaces the deleted account-wide EXPLAIN test and is what justifies
keeping the index.

**T-10 — `SuperchargerHistoryByVehicleBetween` keeps its stop-time semantics.**
Unchanged expectations, one parameter narrower. Seed for `tesla_id=111`: a
session stopping 2026-08-01T00:00Z (7201), one stopping 2026-08-03T23:59Z
(7202), one stopping 2026-08-04T00:00Z (7203), and one that **starts**
2026-07-31T23:00Z and **stops** 2026-08-01T01:00Z (7204). Query
`start = 2026-08-01`, `end = 2026-08-03`. Expect `[7201, 7204, 7202]` ordered
by stop time ascending — 7204 included although it started before the window,
7203 excluded although it stopped one instant after the end day.

**T-11 — the upsert round-trips a non-null vehicle id.**
Upsert one session with `TeslaID: 111`. Read it back with
`SuperchargerHistoryByVehicle(ctx, 111, 10)`. Expect one row whose `TeslaID`
is `111` as a plain `int64`. The domain type has no `AccountID` field to
assert on — that is a compile-time check, not a runtime one.

**T-12 — the change-detection schema self-check still balances.**
`changeDetectDenyListColumns` drops `account_id`, leaving **15** entries; the
SET list stays at **6**. `information_schema` reports **21** live columns for
`telemetry.supercharger_history`. The partition test passes with no leftover
and no stale entry.

**T-13 — an unchanged re-upsert still leaves `updated_at` alone.**
The existing behavioural check, re-keyed. Seed a row, re-upsert with identical
values, assert `updated_at` did not move. Then poke each settable deny-listed
column (minus `account_id`, which is gone) and assert the same.

### Deleted, with the coverage named

`db_supercharger_account_updated_since_integration_test.go` — all seven tests
— goes with the method (D4). Six asserted behaviour that no longer exists:
cross-vehicle results in one read, the orphan row being included, tenant
isolation by `account_id`, and the account-wide EXPLAIN plan. The seventh,
ordering and boundary-inclusive `since`, is carried forward as T-8 and T-9.

---

## Docs this change invalidates

| File | What is wrong after this change |
|---|---|
| `internal/telemetry/AGENTS.md` | The "Why nightly collection exists" section says a session for an unregistered VIN gets `tesla_id = NULL` and the row is kept. The upsert's refreshed-column list names `tesla_id`. The data-ownership table says `supercharger_history` "still carries `account_id` too". The port table says `SuperchargerHistoryReader` has four reads. The public-interface notes call `SuperchargerHistoryByAccountUpdatedSince` the only method that can return a NULL `tesla_id`. All five are wrong. |
| `openspec/specs/telemetry/spec.md` | Four requirements modified and one removed — this change's delta under `specs/telemetry/spec.md`. |
| `kkpa/context/architecture/telemetry-ingest-only.md` | Its consumer table (line 63) names `SuperchargerHistoryByAccount` as `internal/app`'s call — already stale since RM44, and removed outright by this change. |
| `kkpa/context/architecture/telemetry-tables.md` | **Addition to the dispatch's list.** Its closing line says "`account_id`/`tesla_id` are plain columns"; `supercharger_history` has no `account_id` after this change, and `vehicle_snapshots` already lost its. Its `supercharger_history` bullet does not mention the key at all. |
| `kkpa/context/architecture/nightly-cycle.md` | **Addition to the dispatch's list.** Lines 71 and 93 both name `SuperchargerHistoryByAccount` as the mirror's read and as the port's only caller. |

`openspec/changes/archive/` is **not** swept. Several archived designs mention
this table's `account_id`; that is the record of what was decided then and it
stays exactly as it is.

---

## Risks

- **The build is red outside `internal/telemetry` until D7's bridge lands.**
  Mitigation: T8 in `tasks.md` names every file and the exact edit. Run it in
  the same wave, not a later one.
- **Raw SQL in `_test.go` files is invisible to `go vet`.** A fixture naming
  `account_id` on this table still compiles and still runs `go vet` green — it
  fails only when the owner runs the suite. Mitigation: T6.1 greps every
  `_test.go` in the repo for `supercharger_history` across line breaks, not
  line by line, and T9 repeats the grep at the end.
- **A fixture `UPDATE`/`DELETE` that matches no row does not error.** Any test
  fixture that still filters on `account_id` would silently do nothing and the
  assertions after it would pass or fail for an unrelated reason. Mitigation:
  T6 asserts `RowsAffected()` on every fixture write it touches.
- **The mirror silently stops recovering a re-registered vehicle's old
  sessions.** That is D1's accepted consequence, not a defect: those rows no
  longer exist. Worth knowing when reading the nightly numbers after the first
  run.
