# Design — RM57-charging-rekey-supercharger-sessions-on-tesla-id

Source ticket: MAG-67. Roadmap tier 2.
This change touches the database, so this file is the design gate. It carries
the full schema, the rationale, the index plan justified against queries that
exist today, and the expected values of the tests — authored before the
implementation.

## Overview

`charging.supercharger_sessions` and `charging.mirror_watermarks` stop being
account-scoped and become vehicle-scoped. Four things move together, and none
of them works without the others:

1. `supercharger_sessions` drops `account_id`, makes `tesla_id NOT NULL`, and
   changes its uniqueness rule from `(account_id, session_id)` to `(session_id)`.
2. `mirror_watermarks` re-keys its cursor from `account_id` to `tesla_id`.
3. Six port methods drop their `accountID` parameter; a seventh
   (`VerifySession`) replaces it with `teslaID`.
4. The nightly mirror runs one pass per vehicle instead of one pass per account.

The roadmap already settled point 1 with the owner (RD1/RD2). This file records
what follows from it.

---

## Decisions

### D1 — `tesla_id NOT NULL`, `account_id` dropped (roadmap RD1, not re-opened)

Nothing but `account_id` tied a row here to an account. `vin` reaches an
account only through `account.vehicles`, which lists registered vehicles only.
After the drop, a row with `tesla_id IS NULL` is unreachable by every query
this module has — all three session reads already filter `tesla_id = $1`, and
`tesla_id = NULL` is never true. Keeping the column nullable would store rows
nobody can read.

Tier 1 applied the same rule to the source table, so the mirror and its source
now agree on what identifies a session.

### D2 — `UNIQUE (session_id)` replaces `UNIQUE (account_id, session_id)`, and the migration must de-duplicate first

The upsert target has to change: `ON CONFLICT (account_id, session_id)` names a
column that is being dropped.

**The replacement is not a free rename.** The old rule allowed two rows with
the same `session_id`, one per account. That was by design: the nightly mirror
looped per account, and `internal/app/processor.go`'s own comment says "the
same car registered to two accounts is two pairs, and each account mirrors its
own copy." If such a pair exists, `ADD CONSTRAINT … UNIQUE (session_id)` fails
and the migration aborts.

So the migration de-duplicates before adding the constraint, keeping one row
per `session_id` by this order of preference:

1. a row carrying a human-entered battery percentage (`start_battery_pct` or
   `end_battery_pct` non-`NULL`) — a human's reading is the only value in this
   table that cannot be re-derived from telemetry;
2. then the oldest `created_at` — it holds the true first-seen instant;
3. then the lowest `id`, purely so the result is deterministic.

Every column that is *not* a human percentage is a mirror of telemetry, so
discarding a duplicate loses nothing: the surviving row is refreshed to the
same values on the next nightly pass.

**The one case that does lose something**: two duplicate rows where *both*
carry human percentages, and they differ. Then the loser's percentages are
gone and cannot be recovered. `tasks.md` T1.0 makes the owner run the count
query before applying the migration, so this is a checked fact and not a hope.

Rejected alternative — **keep `UNIQUE (account_id, session_id)` and leave
`account_id`**: that is the thing this roadmap exists to remove, and it would
leave one car's history split across accounts, which is what makes a
per-vehicle read incomplete.

Rejected alternative — **`UNIQUE (tesla_id, session_id)`**: it allows the same
session id under two vehicles, which cannot be true — a Supercharger session
happened to exactly one car. It would also keep a duplicate pair alive with no
rule to pick between them. The source table already uses `UNIQUE (session_id)`;
matching it keeps the mirror checkable against its source by inspection.

### D3 — `VerifySession` is re-keyed on `tesla_id`, not stripped of its scope

`VerifySession` and its companion `LockSessionForVerification` scope on
`WHERE id = @id AND account_id = @account_id`. That account predicate is the
**sole tenant boundary** on the whole Supercharger write path: the gateway
handler `SuperchargerRowUpdate` deliberately does no ownership check of its own
and says so in its doc comment.

Dropping the parameter and leaving `WHERE id = @id` would hand any signed-in
user the ability to edit any session whose UUID they can guess or observe. That
is a security regression, not a simplification, so it is not done.

Instead the scope is re-keyed: `WHERE id = @id AND tesla_id = @tesla_id`. The
caller must name the vehicle it believes the session belongs to. The gateway
already resolves a selected vehicle on every Supercharger route, so it has the
value to pass; tier 3 replaces that resolve with `authorizeVehicle`, which
turns the check into a compile-time obligation.

**This does not violate RD8.** RD8 removes a method whose *whole* predicate is
`account_id`, because dropping it would leave a whole-table scan.
`VerifySession`'s predicate is a primary-key point lookup plus a tenant filter;
dropping the tenant filter leaves a point lookup, not a scan. The problem here
is access control, not performance, and the fix is a narrower key rather than a
removal.

A mismatched vehicle matches zero rows and surfaces as `pgx.ErrNoRows`, wrapped
exactly as the existing unknown-id case is — a caller cannot tell "not yours"
from "does not exist", which is the same property the gateway's 404-not-403
rule already depends on.

### D4 — `mirror_watermarks` deletes every cursor rather than mapping them

The cursor is per account today and per vehicle after this change. An account
with two cars has **one** cursor and needs **two**. There is no honest way to
split it: the single stored instant does not say how far each car was mirrored.
Copying it to both would claim progress that was never made for at least one of
them, and a cursor that is too far ahead silently skips rows forever — the
exact failure the watermark's own "never advance to `now()`" rule exists to
prevent.

So every row is deleted. An absent cursor already has a defined meaning:
`MirrorWatermark` returns the zero `time.Time`, which the caller reads as
"epoch" and backfills the vehicle's whole history in one pass.

**The cost is one longer nightly run, not a data change.** The mirror is an
idempotent upsert, and since `RM44-charging-add-change-detecting-mirror` its
`updated_at` only advances when one of the five refreshed columns actually
differs. So a full re-read rewrites nothing, and `analytics.Recalculator.Reconcile`
— which watches that same `updated_at` — sees no work.

Rejected alternative — **backfill one cursor per vehicle by reading
`account.vehicles` from the migration**. It would preserve the cursors, but it
makes a `charging` migration depend on the `account` schema. That is the
cross-module coupling `ai/architecture.md` §2 forbids, paid for a value that
rebuilds itself for free on the next run.

Rejected alternative — **keep `account_id` and add `tesla_id`**. The table then
carries two keys for one cursor and every read has to decide which one wins.
This roadmap exists to remove the account key, not to add a second one beside it.

### D5 — `internal/app` needs a per-vehicle mirror loop in this change, and it is leader-owned

`internal/app/processor.go`'s `processChargingData` is the only production
caller of `MirrorSessions`, `MirrorWatermark` and `AdvanceMirrorWatermark`.
Today it groups vehicles per account, reads a per-account cursor, fans out the
telemetry read over that account's vehicles (tier 1's own bridge), mirrors, and
advances one cursor.

D4 makes the cursor per vehicle, so that shape cannot survive: there is no
account-shaped cursor left to read or advance.

Three orderings were considered:

1. **Leave the build red until roadmap tier 3.** Rejected, for the same reason
   tier 1 rejected it: the project's cheap deterministic signals (`go build`,
   `go vet`, the guards) stop working across a tier boundary, and tier 3's
   worker inherits a break it did not cause.
2. **Keep the account loop and advance one vehicle's cursor per account pass.**
   Rejected as incorrect. It would move one car's cursor past another car's
   unread rows, and those rows would never be mirrored again — a silent,
   unrecoverable loss.
3. **Loop once per distinct `tesla_id`.** Chosen. Read that vehicle's cursor,
   read its telemetry sessions since `cursor - mirrorOverlap`, mirror them,
   advance that vehicle's cursor to the highest `updated_at` actually observed.
   Skip the advance entirely on a zero-row read, exactly as today.

Option 3 is also what the roadmap assigns to tier 3 as leader-owned
integration. It is pulled forward because D4 forces it, and tier 3 is then left
with the gateway authorization work it was really about.

A side effect worth stating: a car registered to two accounts is mirrored
**once**, not twice. That is the same de-duplication D2 applies to the stored
rows, applied to the work that writes them.

### D6 — `ListSessionsByVehicleUpdatedSince` still gets no index of its own

Tier 1 added `(tesla_id, updated_at)` to the source table for its own
updated-since read. This table does **not** get the equivalent, and the
difference is not an oversight.

Telemetry's query orders by `updated_at`, so an `updated_at` index serves the
predicate and the ordering together. This module's query orders by
`charge_stop_date_time` (RM30 D1, so it shares one index with the other two
vehicle reads), so a `(tesla_id, updated_at)` index would satisfy the predicate
and then force a sort — strictly worse than the current plan, which prunes to
one vehicle on the shared index and evaluates `updated_at >= $1` as a residual
filter inside that already-narrow scan.

The volume argument that justified this at RM30 is unchanged: roughly one row
per Supercharger session per vehicle, written nightly.

### D7 — `db_backfill_integration_test.go` is deleted, not repaired

That file extracts the `DO $$ … $$` backfill block from
`20260823000001_add_charge_sessions.sql` at runtime and re-executes it. Its
`INSERT` names `account_id`, so after this change it fails against the live
schema.

It is deleted rather than repaired, for two reasons that point the same way.

**The project's own rule.** `ai/go-conventions.md` §Testing: "Do not test
migrations. Delete migration tests when you find them." The stated cost is
exactly this one — a migration is frozen the moment it is applied, but its test
is not, so every later change to those columns breaks a test of work that
already ran correctly. The precedent is `TestMigration_TpmsPressureBackfill`,
deleted by MAG-65 for the identical reason.

**The test no longer tests what ships.** It already string-rewrites the
extracted SQL in three places (`charge_sessions` → `charging.supercharger_sessions`,
and the `to_regclass` guard) to keep it runnable after RM39 moved the tables.
Repairing it here means a fourth rewrite that deletes a column from the
`INSERT` list. At that point the executed statement is the test's own
construction, not the shipped one — which was the whole argument for extracting
it.

**Lost coverage, named plainly.** Two tests go: A1, which asserts the shipped
backfill copies four rows and resolves a `NULL` `battery_pct_source` to
`'user_verified'` on the one percentage-bearing row; and A2, which asserts
re-running it overwrites nothing. Both describe a one-time statement whose
`to_regclass('public.supercharger_sessions')` guard has been permanently false
since RM39 tier 4, so it cannot run on any database again. Nothing in
production depends on it.

### D8 — table and column `COMMENT`s are not edited

`COMMENT ON TABLE charging.supercharger_sessions` still describes a per-account
mirror, and `COMMENT ON COLUMN … .tesla_id` still says the value is set `NULL`
when the VIN is not a registered vehicle. Both are wrong after this change.

Neither is corrected. This project does not ship a migration to fix comment
text, and a comment that drifts into `models.go` is not treated as a defect
here. Left as it stands, deliberately.

---

## Schema

### Current shape (before this change)

Table `charging.supercharger_sessions`, **19 columns**:
`id`, `account_id`, `vin`, `tesla_id`, `session_id`, `charge_start_date_time`,
`charge_stop_date_time`, `site_location_name`, `energy_kwh`, `total_cost`,
`currency`, `is_paid`, `start_battery_pct`, `end_battery_pct`,
`battery_pct_source`, `created_at`, `updated_at`,
`inferred_capacity_kwh_calc`, `status`.

| Object | Definition today |
|---|---|
| `account_id` | `UUID NOT NULL` |
| `tesla_id` | `BIGINT` (nullable) |
| `supercharger_sessions_pkey` | `PRIMARY KEY (id)` |
| `supercharger_sessions_account_session_unique` | `UNIQUE (account_id, session_id)` |
| `idx_supercharger_sessions_vehicle_stop` | `(account_id, tesla_id, charge_stop_date_time)` ASC |

Table `charging.mirror_watermarks`, 5 columns: `id`, `account_id`,
`source_updated_at`, `created_at`, `updated_at`.

| Object | Definition today |
|---|---|
| `account_id` | `UUID NOT NULL` |
| `mirror_watermarks_pkey` | `PRIMARY KEY (id)` |
| `mirror_watermarks_account_unique` | `UNIQUE (account_id)` |

### Migration 1 — up

File: `internal/charging/db/migrations/20260912000002_rekey_supercharger_sessions_on_tesla_id.sql`

```sql
-- +goose Up
-- A row with tesla_id IS NULL is a session whose VIN was not a registered
-- vehicle when it was mirrored. Once account_id is gone, nothing ties such a
-- row to an account and every read on this table filters tesla_id = $1, which
-- a NULL never matches. Delete first, then forbid new ones. Checked on the
-- live database before writing this: zero such rows, so this deletes nothing
-- today. It guards against a row arriving before the migration runs.
DELETE FROM charging.supercharger_sessions
WHERE tesla_id IS NULL;

-- The old rule allowed one row per session PER ACCOUNT, so two accounts
-- sharing a car could each hold a copy of the same session. UNIQUE (session_id)
-- cannot be added while such a pair exists. Keep one row per session: prefer
-- the one carrying a human's battery percentages (the only value here that
-- cannot be re-derived from telemetry), then the oldest, then the lowest id so
-- the result is deterministic. Everything else on the losing row is a mirror
-- of telemetry and is rewritten onto the survivor by the next nightly pass.
DELETE FROM charging.supercharger_sessions
WHERE id IN (
    SELECT id
      FROM (
            SELECT id,
                   ROW_NUMBER() OVER (
                       PARTITION BY session_id
                       ORDER BY (start_battery_pct IS NOT NULL
                                 OR end_battery_pct IS NOT NULL) DESC,
                                created_at ASC,
                                id ASC
                   ) AS dup_rank
              FROM charging.supercharger_sessions
           ) ranked
     WHERE dup_rank > 1
);

ALTER TABLE charging.supercharger_sessions
    ALTER COLUMN tesla_id SET NOT NULL;

-- The upsert target moves with the key. A session happened to exactly one car,
-- so one row per session id is the true rule; the source table already uses it.
ALTER TABLE charging.supercharger_sessions
    DROP CONSTRAINT supercharger_sessions_account_session_unique;

ALTER TABLE charging.supercharger_sessions
    ADD CONSTRAINT supercharger_sessions_session_id_unique UNIQUE (session_id);

-- Schema-qualified: an index name resolves through search_path, and goose does
-- not guarantee charging is on it.
DROP INDEX charging.idx_supercharger_sessions_vehicle_stop;

ALTER TABLE charging.supercharger_sessions
    DROP COLUMN account_id;

-- The same read this index always served, one column narrower: tesla_id
-- equality prunes to the car, ascending charge_stop_date_time serves the
-- half-open window and the ORDER BY in the same scan, and a backward walk of
-- the same index serves the newest-first limited read.
CREATE INDEX idx_supercharger_sessions_vehicle_stop
    ON charging.supercharger_sessions (tesla_id, charge_stop_date_time);
```

### Migration 1 — down

```sql
-- +goose Down
-- NOT an undo. Rows the Up migration deleted are gone, and every surviving
-- row's account_id went with the column. Down restores the SHAPE only:
-- account_id comes back NULLABLE and empty, so the index it recreates leads on
-- a column holding nothing until something refills it.
DROP INDEX IF EXISTS charging.idx_supercharger_sessions_vehicle_stop;

ALTER TABLE charging.supercharger_sessions
    ADD COLUMN account_id UUID;

ALTER TABLE charging.supercharger_sessions
    ALTER COLUMN tesla_id DROP NOT NULL;

ALTER TABLE charging.supercharger_sessions
    DROP CONSTRAINT supercharger_sessions_session_id_unique;

-- Over an all-NULL account_id this constraint blocks nothing: Postgres treats
-- NULLs as distinct, so every row satisfies it until the column is refilled.
ALTER TABLE charging.supercharger_sessions
    ADD CONSTRAINT supercharger_sessions_account_session_unique
    UNIQUE (account_id, session_id);

CREATE INDEX idx_supercharger_sessions_vehicle_stop
    ON charging.supercharger_sessions (account_id, tesla_id, charge_stop_date_time);
```

`account_id` comes back as `UUID` (nullable), not `UUID NOT NULL`: the old
values are gone, so there is nothing to backfill a `NOT NULL` column with. Same
reasoning as tier 1's own down migration.

### Migration 2 — up

File: `internal/charging/db/migrations/20260912000003_rekey_mirror_watermarks_on_tesla_id.sql`

```sql
-- +goose Up
-- One cursor per account becomes one cursor per vehicle. An account with two
-- cars has one cursor and needs two, and the stored instant does not say how
-- far each car got — so there is no honest mapping. Copying it to both would
-- claim progress never made for at least one, and a cursor that is too far
-- ahead skips rows silently, forever.
--
-- Delete them instead. An absent cursor already means "epoch": the next
-- nightly run re-reads that vehicle's whole Supercharger history once. The
-- mirror is an idempotent upsert that only moves updated_at when a value
-- really changed, so that pass writes nothing new. The cost is one longer
-- run, not a data change.
DELETE FROM charging.mirror_watermarks;

ALTER TABLE charging.mirror_watermarks
    DROP CONSTRAINT mirror_watermarks_account_unique;

ALTER TABLE charging.mirror_watermarks
    DROP COLUMN account_id;

-- NOT NULL with no DEFAULT is only legal because the DELETE above left the
-- table empty. That is the point: there is no value a surviving row could
-- honestly take.
ALTER TABLE charging.mirror_watermarks
    ADD COLUMN tesla_id BIGINT NOT NULL;

ALTER TABLE charging.mirror_watermarks
    ADD CONSTRAINT mirror_watermarks_vehicle_unique UNIQUE (tesla_id);
```

### Migration 2 — down

```sql
-- +goose Down
-- NOT an undo: the cursors the Up migration deleted are gone, and so is every
-- tesla_id written after it. Down restores the shape and leaves the table
-- empty, which is the safe state — an absent cursor re-reads the full history
-- rather than skipping it.
DELETE FROM charging.mirror_watermarks;

ALTER TABLE charging.mirror_watermarks
    DROP CONSTRAINT mirror_watermarks_vehicle_unique;

ALTER TABLE charging.mirror_watermarks
    DROP COLUMN tesla_id;

ALTER TABLE charging.mirror_watermarks
    ADD COLUMN account_id UUID NOT NULL;

ALTER TABLE charging.mirror_watermarks
    ADD CONSTRAINT mirror_watermarks_account_unique UNIQUE (account_id);
```

### Index plan

The rule this plan follows (MAG-63, binding on every RM57 tier): a
`UNIQUE (a, b)` constraint already builds a btree serving equality on `a`,
point lookups, range scans on `b` within one `a`, and `ORDER BY b DESC`. Never
add an index it already covers. An index nothing reads today is dropped, not
kept "just in case".

**`charging.supercharger_sessions`**

| Index | Action | Shape after | The query that reads it, and where the caller lives |
|---|---|---|---|
| `supercharger_sessions_pkey` | keep, untouched | `(id)` | `LockSessionForVerification` and `VerifySuperchargerSession` (`db/query.sql`), both `WHERE id = @id` — a primary-key point lookup. Callers: `sessionVerifier.VerifySession` (`internal/charging/session_verifier.go`), reached from `SuperchargerRowUpdate` (`internal/gateway/handlers/supercharger.go`). |
| `supercharger_sessions_account_session_unique` | **replace** | `supercharger_sessions_session_id_unique UNIQUE (session_id)` | `MirrorSuperchargerSession` (`db/query.sql`) — the `ON CONFLICT (session_id)` target. Caller: `sessionWriter.MirrorSessions` (`internal/charging/session_writer.go`), from the nightly `processChargingData` (`internal/app/processor.go`). Also the only point-lookup path by session id. |
| `idx_supercharger_sessions_vehicle_stop` | **replace** | `(tesla_id, charge_stop_date_time)` ASC | Three queries, all in `db/query.sql`: **(a)** `ListSessionsByVehicleBetween` — `WHERE tesla_id = $1 AND charge_stop_date_time >= $2 AND < $3 ORDER BY charge_stop_date_time ASC`; exact match, no sort step. Callers: `buildSuperchargerStatsView` and `fetchSuperchargerRowVM` (`internal/gateway/handlers/supercharger.go`) and `recalculator.Recalculate` (`internal/analytics/recalculate.go:158`). **(b)** `ListSessionsByVehicle` — `WHERE tesla_id = $1 ORDER BY charge_stop_date_time DESC LIMIT n`; the same btree walked backwards, which costs the same. Caller: `reader.ConsumedByDay` (`internal/analytics/reader.go:141`). **(c)** `ListSessionsByVehicleUpdatedSince` — `WHERE tesla_id = $1 AND updated_at >= $2 ORDER BY charge_stop_date_time ASC`; `tesla_id` prunes, `updated_at` is a residual filter inside that scan (D6). Caller: `recalculator.Reconcile` (`internal/analytics/recalculate.go:279`). |
| — | **none added** | — | `ListValidSessionCapacitiesForPeriod` (`db/query.sql`) filters `status = 'DONE'` and a `charge_stop_date_time` range with no vehicle. It has no index today and gets none: it is a once-a-month write-path batch (`MonthlyCapacityCalculator.Calculate`, `internal/charging/monthly_capacity.go`), not a read path, and the project's read-heavy profile does not buy indexes for monthly batches. |

Three notes on why this is the whole list:

- **Why `UNIQUE (session_id)` does not cover the vehicle index.** It is keyed on
  `session_id` alone. Every vehicle-scoped read filters on `tesla_id`, which
  that index does not contain, so it cannot prune to a car at all.
- **Why the vehicle index is not `(tesla_id, charge_stop_date_time DESC)`.** A
  btree is walked in either direction at identical cost, so one ascending index
  serves both the ascending bounded window and the descending limited read. The
  ascending build is kept because the bounded window is the read the page makes
  on every render. This is RM30 D1's original reasoning, unchanged — only the
  leading column moved.
- **Why nothing replaces the dropped `account_id` leading column.**
  `ai/go-conventions.md` says every multi-tenant table leads its index with
  `account_id`. After this change the table is not multi-tenant: it has no
  tenant column. The convention's own reason — "every dashboard read scopes by
  account" — no longer describes any query here; every one of them scopes by
  vehicle.

**`charging.mirror_watermarks`**

| Index | Action | Shape after | The query that reads it |
|---|---|---|---|
| `mirror_watermarks_pkey` | keep, untouched | `(id)` | row identity; no query filters on `id`. |
| `mirror_watermarks_account_unique` | **replace** | `mirror_watermarks_vehicle_unique UNIQUE (tesla_id)` | `GetMirrorWatermark` — `WHERE tesla_id = $1`, a single-row lookup — and `UpsertMirrorWatermark`'s `ON CONFLICT (tesla_id)` target (`db/query.sql`). Callers: `mirrorWatermarkStore` (`internal/charging/mirror_watermark.go`), from `processChargingData` (`internal/app/processor.go`). |
| — | **none added** | — | The unique constraint's own index serves the only read there is. Same conclusion the table shipped with. |

### Makefile, guards and codegen — checked, with findings

| Thing checked | Finding |
|---|---|
| `MIGRATIONS_DIRS` order (`account telemetry charging analytics`) | **Unaffected, and verified — not an assumption.** No later module's migration reads `charging.supercharger_sessions` or `charging.mirror_watermarks`: `internal/analytics`' only cross-module migration read is `telemetry.vehicle_snapshots` (`20260908000002`). This change's own migrations read no other module's table, so charging's position in the order does not matter to them. The cross-module DROP hazard that blocks MAG-76 does not apply. |
| The historic backfill in `20260823000001_add_charge_sessions.sql` | **Checked, and it is why a test dies (D7).** That migration's `DO $$ … $$` block inserts `account_id` into this table. It is never edited — historic migrations are frozen — and it never runs again: its `to_regclass('public.supercharger_sessions')` guard has been permanently `NULL` since RM39 tier 4. On a fresh database it is skipped, so `goose up` stays green with the new migrations applied after it. What does break is `db_backfill_integration_test.go`, which re-executes that block by hand; see D7. |
| `make migration-guard` | Versions `20260912000002` and `20260912000003` are free across all four module directories — today's highest anywhere is `20260912000001` (tier 1). Roadmap tier 3 must take `20260912000004` and higher if it ships a migration. |
| `sqlc` inputs | `sqlc.yaml`'s charging entry points `schema:` at `internal/charging/db/migrations` and `queries:` at `internal/charging/db/query.sql`. Both change, neither moves. No rename key is affected — neither table is renamed. Run `make sqlc` after the migrations and the query edits. |
| `make db-setup` / `db-reset` role and ownership | Unaffected. No new schema, no new table, no new role. Both migrations run as `APP_ROLE`, which already owns the `charging` schema. |
| `make boundary-guard` | Unaffected. The guard forbids `internal/telemetry` imports inside `internal/gateway`; this change adds none. |
| `make vehicleref-guard` | Checked. This change adds no `vehicleref.Ref` parameter — the ports take a plain `teslaID int64`, matching tier 1's telemetry ports. Tier 3 owns the gateway-side proof. |
| `make archive-guard` | Nothing under `openspec/changes/archive/` is edited. A grep for `account_id` hits archived designs; those are the record of what was decided then and stay. |
| `make tz-guard`, `money-guard`, `i18n-guard`, `ui-guard` | Unaffected — no time-zone call, no monetary column, no user-facing string is touched. |

---

## Queries (`internal/charging/db/query.sql`)

| Query | Change |
|---|---|
| `MirrorSuperchargerSession` | Remove `account_id` from the INSERT column list and from `VALUES`. `ON CONFLICT (account_id, session_id)` becomes `ON CONFLICT (session_id)`. Remove `account_id` from **both** `'{…}'::text[]` deny-list arrays. `@tesla_id` becomes a plain `BIGINT` parameter (sqlc infers non-null from the column). The load-bearing comment about the human-owned battery-% trio stays word for word. Rewrite the `tesla_id` note: it stays inside the comparison because telemetry refreshes it and a mirrored column takes its source's write semantics — the old reason (a `NULL`→value change recovering an orphan) no longer exists. |
| `ListSessionsByVehicleBetween` | Drop the `account_id` predicate. Update the index comment to `(tesla_id, charge_stop_date_time)`. Delete the closing paragraph about `tesla_id = @tesla_id` excluding `NULL` rows — the column is `NOT NULL`. |
| `ListSessionsByVehicleUpdatedSince` | Drop the `account_id` predicate. Same two comment fixes. Keep the "no new index" paragraph and point it at D6. |
| `ListSessionsByVehicle` | Drop the `account_id` predicate. Same two comment fixes. Keep the backward-index-scan paragraph. |
| `LockSessionForVerification` | `WHERE id = @id AND tesla_id = @tesla_id`. Update the comment: the scope is the vehicle now, not the account. |
| `VerifySuperchargerSession` | `WHERE id = @id AND tesla_id = @tesla_id`. Same comment fix, and note that this predicate is the write path's tenant boundary (D3). |
| `GetMirrorWatermark` | `WHERE tesla_id = @tesla_id`. Update the comment: one cursor per vehicle, served by `mirror_watermarks_vehicle_unique`. |
| `UpsertMirrorWatermark` | `INSERT … (tesla_id, source_updated_at)`, `ON CONFLICT (tesla_id)`. `created_at` stays out of the SET clause, unchanged. |
| `ListValidSessionCapacitiesForPeriod` | Delete `AND tesla_id IS NOT NULL` — the column is `NOT NULL`, so the predicate is now always true. Update the comment that explains it. |

After `make sqlc`, the generated `chargingdb.SuperchargerSession` model loses
`AccountID` and its `TeslaID` becomes `int64`. Every `…Params` struct for the
queries above loses `AccountID`; `LockSessionForVerificationParams` and
`VerifySuperchargerSessionParams` gain `TeslaID int64`;
`GetMirrorWatermark` takes a plain `int64`;
`UpsertMirrorWatermarkParams.AccountID` becomes `TeslaID int64`.
`ListValidSessionCapacitiesForPeriodRow.TeslaID` becomes `int64`.

The deny-list in `MirrorSuperchargerSession` goes from 14 columns to 13. The
SET list stays at 5. The live table goes from 19 columns to 18, so the
self-checking schema test's partition still balances: 5 + 13 = 18.

---

## Ports and implementation

### Domain types (`charging.go`)

```go
type SessionMirror struct {
    VIN       string
    TeslaID   int64 // always a registered vehicle at write time
    SessionID int64
    // … every other field unchanged …
}

type Session struct {
    ID        uuid.UUID
    VIN       string
    TeslaID   int64
    SessionID int64
    // … every other field unchanged …
}
```

`AccountID` is removed from both: there is no column left to map it from.
`TeslaID` becomes a value, not a pointer, because the column is `NOT NULL`. The
pointer used to carry "the VIN is not a registered vehicle"; D1 removes that
state from the table, so it has nothing left to express.

### Ports (`charging.go`)

```go
type SessionWriter interface {
    MirrorSessions(ctx context.Context, sessions []SessionMirror) error
}

type SessionReader interface {
    ListSessionsByVehicleBetween(ctx context.Context, teslaID int64, from, to time.Time) ([]Session, error)
}

type SuperchargerSessionAnalyticsReader interface {
    ListSessionsByVehicleUpdatedSince(ctx context.Context, teslaID int64, since time.Time) ([]Session, error)
    ListSessionsByVehicle(ctx context.Context, teslaID int64, limit int) ([]Session, error)
}

type SessionVerifier interface {
    VerifySession(ctx context.Context, teslaID int64, id uuid.UUID, startBatteryPct, endBatteryPct *int) (Session, error)
}

type MirrorWatermarkStore interface {
    MirrorWatermark(ctx context.Context, teslaID int64) (time.Time, error)
    AdvanceMirrorWatermark(ctx context.Context, teslaID int64, observed time.Time) error
}
```

`MirrorSessions`'s doc comment loses the "every entry's `AccountID` must equal
`accountID`" contract — there is no scope argument left to match against.

### `session_writer.go`

- Delete the validation loop that compared each entry's `AccountID` to the call
  scope. Keep the empty-slice short circuit and the single-transaction shape.
- Drop `AccountID` from the params literal; pass `TeslaID: s.TeslaID` directly.
- `int64PtrToPgInt8` loses its last call site here. Check for other users
  before deleting it — `pgInt8ToInt64Ptr`'s remaining users are decided in
  `session_reader.go` and `monthly_capacity.go` below.

### `session_reader.go`

The three methods drop `accountID` from their signature and their `…Params`
literal. `rowToSession` drops `AccountID` and assigns `TeslaID: r.TeslaID`
directly, so `pgInt8ToInt64Ptr` loses a call site here too.

### `session_verifier.go`

- `VerifySession` takes `teslaID int64` in place of `accountID uuid.UUID`, and
  passes it to both `LockSessionForVerification` and
  `VerifySuperchargerSession`.
- The derived-start branch loses its nil check: `row.TeslaID` is a plain
  `int64`, so `packCapacityKWh(ctx, v, row.TeslaID)` always runs and the
  `defaultPackCapacityKWh` fallback branch disappears. The fallback itself
  survives one level down, inside `packCapacityKWh`, for a vehicle with no
  measured capacity row yet.

### `mirror_watermark.go`

Both methods take `teslaID int64`. The error messages say `vehicle %d` instead
of `account %s`.

### `monthly_capacity.go`

`ListValidSessionCapacitiesForPeriodRow.TeslaID` is a plain `int64`, so the
`pgInt8ToInt64Ptr` call and the `tid == nil` guard go, and the row is grouped
under `r.TeslaID` exactly like the manual-entry branch above it.

---

## Test contract — expected values, authored before the implementation

These are the values the design says the tests must produce. A test written
after reading the implementation confirms what the code does, not what the
design specifies.

### Offline (pure Go, no database)

**This change adds no offline test, and that is a finding, not an omission.**
Every offline file in this module (`charging_test.go`, `entry_status_test.go`,
`monthly_capacity_estimator_test.go`, `price_source_test.go`,
`session_verifier_derivation_test.go`) names `account_id` / `AccountID`
**zero** times, and no pure function changes behaviour here — the change is
entirely in the schema, the queries and the port signatures. The early wave
therefore verifies by grep and by `go vet`, not by new assertions
(`tasks.md` T0).

### Database-backed (`TEST_DATABASE_URL`-gated) — write these last

They cannot compile before the migrations and the sqlc types exist.

**T-1 — the mirror upserts on the session id alone.**
Given an empty table. `MirrorSessions(ctx, []SessionMirror{{TeslaID: 111,
SessionID: 9001, VIN: "VIN_A", SiteLocationName: "Site A",
ChargeStartDateTime: 2026-08-01T10:00Z, ChargeStopDateTime: 2026-08-01T10:30Z,
EnergyKWh: 30.0}})`. Then exactly **1** row exists with `session_id = 9001` and
`tesla_id = 111`. Re-calling with identical values leaves `updated_at`
unchanged. Calling again with `EnergyKWh: 31.0` stores `31.0` and advances
`updated_at`.

**T-2 — one session id can exist only once, whatever the vehicle.**
Given T-1's row (session 9001, `tesla_id` 111). When `MirrorSessions` is called
with `{TeslaID: 222, SessionID: 9001, …}`. Then there is still exactly **1**
row for session 9001, and its `tesla_id` is **222** — the upsert conflicted on
`session_id` and refreshed `tesla_id`. Before this change the same call
inserted a second row. This test is the behaviour change D2 introduces.

**T-3 — `ListSessionsByVehicleBetween` scopes by vehicle alone.**
Seed four rows with distinct session ids: 9101 (`tesla_id` 111, stop
2026-08-01T00:00Z), 9102 (111, stop 2026-08-03T23:59Z), 9103 (111, stop
2026-08-04T00:00Z), 9104 (222, stop 2026-08-02T00:00Z). Query
`ListSessionsByVehicleBetween(ctx, 111, 2026-08-01, 2026-08-03)`. Expect
exactly `[9101, 9102]`, ascending by stop time. 9103 is excluded — the
half-open end bound is `2026-08-04T00:00Z` and the predicate is `<`. 9104 is
excluded — it is another vehicle.

**T-4 — `ListSessionsByVehicle` returns newest-first, limited.**
Seed for `tesla_id` 111: 9201 (stop 2026-08-01), 9202 (2026-08-02), 9203
(2026-08-03); plus 9204 for `tesla_id` 222 (stop 2026-08-04). Query
`ListSessionsByVehicle(ctx, 111, 2)`. Expect exactly `[9203, 9202]`. 9204 never
appears, even though it is the newest row in the table.

**T-5 — `ListSessionsByVehicleUpdatedSince` ordering and boundary.**
Seed for `tesla_id` 111: 9301 (stop 2026-08-01), 9302 (2026-08-02), 9303
(2026-08-03); plus 9304 for `tesla_id` 222. Force `updated_at` with a raw
`UPDATE` per row — `t1 = 2026-08-10T00:00:00Z` on 9301, `t2 = 2026-08-11T00:00:00Z`
on 9302, `t3 = 2026-08-12T00:00:00Z` on 9303, `t2` on 9304 — asserting
`RowsAffected() == 1` on each. Then: `since = t2` returns exactly
`[9302, 9303]`, in that order (ascending stop time); `since = t3` returns
exactly `[9303]`; `since = t3 + 1µs` returns an **empty, non-nil** slice and no
error. 9304 never appears.

The step past `t3` is one **microsecond**, not one nanosecond. Postgres stores
`timestamptz` to microsecond resolution and pgx truncates anything finer on
encode, so `t3 + 1ns` would arrive as plain `t3` and the inclusive `>=` bound
would correctly return 9303.

**T-6 — `VerifySession` is scoped by vehicle, and a wrong vehicle writes nothing.**
Seed session 9401 for `tesla_id` 111 with both percentages `NULL`. Call
`VerifySession(ctx, 222, id9401, 20, 80)`. Expect an error wrapping
`pgx.ErrNoRows`, and a direct `SELECT` showing `start_battery_pct` and
`end_battery_pct` still `NULL` and `status` still `IN_PROGRESS`. Then call
`VerifySession(ctx, 111, id9401, 20, 80)`. Expect success, with the returned
`Session` carrying `StartBatteryPct == 20`, `EndBatteryPct == 80`,
`BatteryPctSource == "user_verified"`, `Status == DONE`, and `TeslaID == 111`
as a plain `int64`.

**T-7 — the derived-start path no longer has a nil-vehicle branch.**
Seed session 9501 for `tesla_id` 111 with `energy_kwh = 31.0` and no row in
`monthly_effective_capacity` for that vehicle, so `packCapacityKWh` falls back
to `62.0`. Call `VerifySession(ctx, 111, id9501, nil, 80)`. Expect
`StartBatteryPct == 30` — `80 − 31.0/62.0*100 = 30` —
`BatteryPctSource == "user_verified"` and `Status == DONE_CALCULATED`.

**T-8 — the change-detection schema self-check still balances.**
`account_id` leaves the deny-list, leaving **13** entries; the SET list stays at
**5**; `information_schema` reports **18** live columns for
`charging.supercharger_sessions`. The partition passes with no leftover and no
stale entry.

**T-9 — an unchanged re-mirror still leaves `updated_at` alone.**
The existing behavioural check, re-keyed. Seed a row, re-mirror with identical
values, assert `updated_at` did not move. Then poke each settable deny-listed
column (minus `account_id`, which is gone) and assert the same.

**T-10 — the bounded per-vehicle read uses its index and does not sort.**
`EXPLAIN` `ListSessionsByVehicleBetween`'s statement. The plan names
`idx_supercharger_sessions_vehicle_stop` and contains **no** `Sort` node.

**T-11 — the newest-first read walks the same index backwards.**
`EXPLAIN` `ListSessionsByVehicle`'s statement. The plan names
`idx_supercharger_sessions_vehicle_stop`, reports a backward scan, and contains
**no** `Sort` node. This is RM30's existing assertion, re-keyed.

**T-12 — the watermark is per vehicle, and two vehicles no longer share one.**
Given an empty table: `MirrorWatermark(ctx, 111)` returns the zero `time.Time`
and no error. `AdvanceMirrorWatermark(ctx, 111, 2026-08-10T00:00:00Z)`, then
`MirrorWatermark(ctx, 111)` returns exactly that instant.
`AdvanceMirrorWatermark(ctx, 111, 2026-08-11T00:00:00Z)`, then the read returns
the **later** instant. Throughout, `MirrorWatermark(ctx, 222)` returns the zero
`time.Time` — a second vehicle of the same account has its own cursor.

**T-13 — the monthly capacity batch attributes a session to its vehicle.**
Seed inside the period, all for `tesla_id` 111: one `status = 'DONE'` session
with `start_battery_pct = 20`, `end_battery_pct = 70`, `energy_kwh = 31.0`
(so `inferred_capacity_kwh_calc = 31.0 / 0.50 = 62.000`), one
`status = 'DONE_CALCULATED'` session, and one `status = 'IN_PROGRESS'` session.
Call `Calculate(ctx, period, &111)`. Expect `VehiclesFound == 1`,
`Measured == 0` and `Thin == 1` — one candidate passes the 15-point delta gate
but `minSamples` is 3 — and the stored `monthly_effective_capacity` row carries
`candidate_count = 1`, `sample_count = 1`, `effective_capacity_kwh` `NULL`.

### Deleted, with the coverage named

`db_backfill_integration_test.go` — both tests — is deleted (D7). A1 asserted
the shipped backfill copies four rows and resolves a `NULL`
`battery_pct_source` to `'user_verified'` on the one percentage-bearing row;
A2 asserted a re-run overwrites nothing. Both describe a statement whose guard
has been permanently false since RM39 tier 4, so it can never run again.

---

## Docs this change invalidates

| File | What is wrong after this change |
|---|---|
| `internal/charging/AGENTS.md` | The `mirror_watermarks` section says the table has "NO `tesla_id` column" and explains why, and describes a per-account cursor and the orphan-recovery case. The mirror section's deny-list names `account_id` as a write-once mirrored column. The `SessionMirror` and `Session` descriptions say `TeslaID` is nil when the VIN is not registered. The Data Ownership table's "Written by" column and the `WHERE account_id = @account_id` tenant-scoping rule. The Testing Notes bullet describing `db_backfill_integration_test.go`, which no longer exists. |
| `openspec/specs/charging/spec.md` | Two requirements modified and two added — this change's delta under `specs/charging/spec.md`. |
| `kkpa/context/architecture/charging-tables.md` | The `supercharger_sessions` column list names `account_id` as write-once mirrored; the index and unique-constraint entries describe the account-leading shapes. |
| `kkpa/context/architecture/nightly-cycle.md` | `MirrorSessions` is described as "one transaction per account, rejects a mis-scoped `AccountID`". The consumer table and the per-table read/write table describe the mirror as account-grained. |
| `kkpa/context/architecture/gateway-reader-writer-ports.md` | It states `VerifySession`'s `WHERE id = @id AND account_id = @account_id` is the boundary and that there is "**No ownership check**". The predicate is now `tesla_id`. |
| `kkpa/context/workflows/supercharger-stats-read.md` | It prints `VerifySession`'s full old signature and says `WHERE id = @id AND account_id = @account_id` "(no `TeslaID` predicate)". Also the tenant-boundary bullet. |
| `kkpa/context/use-case/charging/verify-session-battery.md` | Its call-chain step 3 and its read/write table describe the account-scoped call. |
| `kkpa/context/input-port/charging/supercharger-stats.md` | "`VerifySession`'s own account-scoped …" — the same statement, in the input-port guide. |
| `kkpa/context/architecture/charge-record-mutation.md` | It says `SuperchargerRowUpdate` "relies solely on the SQL `AND account_id` scope". |

`openspec/changes/archive/` is **not** swept. Several archived designs mention
these tables' `account_id`; that is the record of what was decided then and it
stays exactly as it is.

---

## Risks

- **The de-duplication can lose a human's percentages** if two duplicate rows
  both carry them and they differ (D2). Mitigation: T1.0 makes the owner run
  the count query before applying the migration, so the number is known rather
  than assumed.
- **The build is red outside `internal/charging` until D5's bridge lands.**
  Mitigation: T8 names every file and the exact edit. Run it in the same wave,
  not a later one.
- **The gateway's Supercharger write path loses its tenant boundary if
  `VerifySession`'s scope is dropped instead of re-keyed.** Mitigation: D3
  keeps a scope, and T-6 proves a wrong vehicle writes nothing.
- **Raw SQL in `_test.go` files is invisible to `go vet`.** A fixture naming
  `account_id` on either table still compiles and still passes `go vet`; it
  fails only when the owner runs the suite. Mitigation: T6.1 greps every
  `_test.go` in the repo for both table names across line breaks, not line by
  line, and T9 repeats the grep at the end.
- **A fixture `UPDATE`/`DELETE` that matches no row does not error.** Any
  fixture still filtering on `account_id` would silently do nothing and the
  assertions after it would pass or fail for an unrelated reason. Mitigation:
  T5.9 asserts `RowsAffected()` on every fixture write this change touches.
- **The first nightly run after the migration re-reads every vehicle's whole
  Supercharger history** (D4). It writes nothing new, but the run is longer and
  the log line will show a large session count once. Expected, not a defect.
