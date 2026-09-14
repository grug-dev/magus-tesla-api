# Design — RM58-charging-demote-manual-charge-account-id

Source ticket: MAG-68. Roadmap tier 1.
This change touches the database, so this file is the design gate. It carries the
full schema, the rationale with the rejected alternatives, the index plan
justified against queries that exist today, and — because unit tests are excluded
— the list of existing tests that must change and what they must assert instead.

## Overview

`charging.manual_charge_entries` stops being account-scoped and becomes
vehicle-scoped. Four things move together:

1. The column `account_id` is renamed `created_by_account_id`. It stays
   `UUID NOT NULL`. It stops being a key and stops being a read filter.
2. The two indexes that led on `account_id` are replaced by one index that leads
   on `tesla_id`.
3. Three read queries drop the `account_id` predicate; one read query is re-keyed
   from an account to an array of vehicles. The two **write** queries keep their
   predicate for one tier, on the renamed column (D4).
4. Four read ports drop their `accountID` parameter, and one is renamed. The
   write ports keep their signatures.

The roadmap already settled point 1 with the owner. This file records what
follows from it.

Two things this file deliberately does **not** decide, because the roadmap gives
them to a later tier: retyping `Update`/`Delete` to `vehicleref.Ref` (tier 2), and
proving the entry's vehicle with `authorizeVehicle` in the gateway (tier 3).

---

## Decisions

### D1 — The reads become car-wide (roadmap decision, owner-chosen, not re-opened)

After this change, a read for a vehicle returns **every** manual charge for that
vehicle, whoever typed it.

This is a real behaviour change, not only a schema change. If two accounts are
registered to the same car, each account's `vehicle_metrics` values change,
because `internal/analytics` sums both accounts' entries instead of one
account's.

It is the right answer for the same reason the roadmap gives: a charge happened
to a car. RM57 already made Supercharger sessions car-wide; manual entries were
the last charging source still filtered by account. Leaving them account-scoped
would make one car's own two energy sources disagree about what the car did.

The direct consequence in the test suite is named in §Test contract: the two
tests that assert one account cannot see another account's entry for a shared
vehicle now assert the opposite.

### D2 — The column is demoted, not dropped

RM57 dropped `account_id` from the two Supercharger tables. Those rows are a
mirror of Tesla's own data, so nothing about them is authored by a person.

A manual charge entry **is** authored by a person. `created_by_account_id` is the
only record of who typed it, and it cannot be re-derived from anything else — not
from `tesla_id` (a car can have two registered accounts), not from
`account.vehicles` (it lists registrations, not authorship). Dropping it would
destroy a fact.

So the column stays `NOT NULL`, keeps being written by `CreateEntry`, and is
never read back as a predicate again.

Rejected alternative — **drop the column, matching RM57 exactly**. Rejected: it
loses authorship permanently, and the roadmap's own title is "demote", not "drop".

Rejected alternative — **keep the name `account_id` and only change the
queries**. Rejected: the name is the thing that keeps inviting a predicate. A
column called `account_id` on a multi-tenant platform reads as a tenant key to
every agent and every human, and the next change would filter on it without
thinking. The rename is the guard.

### D3 — The four read ports keep a plain `int64`, not a `vehicleref.Ref`

`internal/analytics` calls three of these ports, and `make vehicleref-guard`
allows `vehicleref.Authorize` and `vehicleref.All` only inside
`internal/vehicleref`, in `_test.go` files, and in the gateway's
`authorizeVehicle`. A domain module therefore cannot build a `Ref`, so a port
that demanded one would be uncallable from `analytics`.

This is not a workaround for the guard. It is the architecture the guard encodes
(`ai/architecture.md` §5): the gateway is the only place that checks whether a
vehicle belongs to the signed-in user, and a module below it trusts the id it
receives. `analytics` runs on the nightly path with no signed-in user at all.

The same reasoning produced the same answer in RM57: the Supercharger read ports
take a plain `teslaID int64`.

Rejected alternative — **give the read ports a `Ref` and let `analytics` build
one**. It would need `vehicleref.All` inside `analytics`, which the guard fails,
and it would put an ownership vocabulary into a module that has no user.

### D4 — The write queries keep their account predicate for one tier, on the renamed column

*(The owner decided this. The leader records it as the user's decision D5; it is
D4 here because it replaces this file's own earlier answer, and the reasoning
that led to it is kept below rather than deleted.)*

`UpdateEntry` and `DeleteEntry` are scoped `WHERE id = @id AND account_id =
@account_id` today. **In this tier they keep that scope, on the new column name:**
`WHERE id = @id AND created_by_account_id = @created_by_account_id`.

Tier 2 removes the predicate, in the same change that adds the `tesla_id` guard
and retypes the ports to `vehicleref.Ref`. So the guard never disappears: it is
swapped, not dropped.

**This is transitional and deliberate, and it is not the rule being forgotten.**
At the end of this tier the demoted column *is* still a read filter, in exactly
two queries. That contradicts the ticket's own "Done when: no read filters on it"
— and the ticket is met at the **end of the roadmap**, not at the end of this
tier. A later reader should take the predicate as a dated placeholder with a
named removal date (tier 2), not as a rule nobody applied.

**What this file first proposed, and why it was wrong.** The roadmap's tier-1
scope lists both queries, so the first draft of this design dropped the predicate
and left `WHERE id = @id`. That is a real authorization hole, not a theoretical
one: the account predicate is the **only** check on the manual-charge write path.
`ExternalChargeRowDelete` does call `fetchEntryTeslaIDAndChargedOn` first, but
only to learn which day to recalculate — its own comment says a lookup miss "just
means there is no day to recalculate; the delete still proceeds". With no
predicate, any signed-in user holding an entry's UUID could update or delete it.
The draft contained that with a branch rule ("do not merge before tier 3"). A
process rule is a weaker guarantee than a `WHERE` clause, and the owner chose the
`WHERE` clause.

**Why the transitional guard is the right shape.** The replacement guard is
`WHERE id = $1 AND tesla_id = $2`. It cannot be built here: `Delete`'s port
signature is `Delete(ctx, accountID, id)`, which carries no vehicle, and adding
one changes a write-port type that the roadmap assigns to tier 2. So the choice
is between the old guard for one tier and no guard for one tier. Keeping the old
guard costs two SQL lines, changes nothing in tier 2, and means **no commit on
this branch ever has an unauthorized write path**.

**The known cost, stated plainly.** For one tier, reads are car-wide (D1) but
writes are author-only. On a vehicle registered to two accounts, the External
Charges page will list a co-owner's entry and offer its edit and delete controls,
and the write will then match zero rows:

- `Update` returns an error wrapping `pgx.ErrNoRows`, and the user sees the
  page's "could not update entry" message.
- `Delete` is an `:exec`, so zero rows deleted is not an error. The fragment
  re-renders and the row is still there.

Tier 3 removes this by authorizing on the entry's own vehicle, after which any
account registered to the car may edit the car's entries. Until then the
behaviour is the same as today's, so nothing a user can do now stops working —
only the newly visible co-owner rows behave this way. Hiding those controls is
gateway work and belongs with tier 3, not here.

Rejected alternative — **drop the predicate now and hold the branch back from
`main` until tier 3 is archived.** This was the draft above. Rejected: it trades
a compile-time-and-runtime guarantee for a human process rule, on a branch that
auto-deploys once merged.

Rejected alternative — **pull tier 2's port change forward into this tier.** It
would give the correct `tesla_id` guard immediately. Rejected: it merges two
tiers, and the roadmap separates them so the `vehicleref.Ref` retype lands with
the gateway work that can actually supply a `Ref`.

### D5 — The cross-module bridge lands in this same change, and it is leader-owned

Renaming `ListEntriesByAccount` and changing four signatures makes
`go build ./...` fail in `internal/analytics` and `internal/gateway`. Two
orderings were considered.

1. **Leave the build red until tier 3.** Rejected, for the reason RM57 rejected
   it: the project's cheap deterministic signals (`go build`, `go vet`, the
   guards) stop working across a tier boundary, and a later worker inherits a
   break it did not cause.
2. **Fix the call sites mechanically in this change.** Chosen. The edits are
   small and carry no new design.

The gateway's two lookup helpers, `fetchEntryVM` and
`fetchEntryTeslaIDAndChargedOn`, need slightly more than an argument drop: they
list every entry of an account and match on id. With no account-wide read left,
they list the entries of the account's **registered vehicles** instead, obtained
from `account.RegisteredVehicles`, which the gateway already calls.

Two consequences of that, both accepted here and both revisited by tier 3:

- An entry whose vehicle the account has since de-registered stops being
  findable by these two helpers. It was already unreachable in the page's own
  list, which is filtered to a selected registered vehicle.
- An entry typed by a co-owner of a shared car becomes findable and editable.
  That is D1 applied to the write path, and it is the intent.

Tier 3 replaces the registered-vehicle list with `vehicleref.All` / `TeslaIDs`
and proves the entry's own `tesla_id` with `authorizeVehicle`.

### D6 — `Entry.AccountID` is renamed `Entry.CreatedByAccountID`

The domain field mirrors the column one-to-one. If the column is renamed and the
field is not, a reader of `charging.go` sees `AccountID` and reasonably concludes
the table still has a tenant key.

The cost is small and bounded: three assignments inside `internal/charging`, two
in `internal/gateway/handlers/external_charges.go` (lines 445 and 1425), and the
test fixtures. Nothing else in the repository reads the field.

Rejected alternative — **keep `Entry.AccountID`**. Rejected: it is precisely the
stale-name trap this project keeps paying for, and the compiler will not catch it
because the sqlc mapping is written by hand in `rowToEntry`.

### D7 — The table `COMMENT` is not edited; one column `COMMENT` is added

`COMMENT ON TABLE charging.manual_charge_entries` says "No cross-module FK on
account_id or tesla_id". That sentence is stale after the rename. It is **not**
corrected: this project does not ship a migration to fix comment text, and a
comment that drifts into `models.go` is not treated as a defect here.

One `COMMENT ON COLUMN` **is** added, for `created_by_account_id`. That is not a
correction of stale text — it is the new column's own documentation, and it is
what carries the "authorship only, never a key, never a predicate" rule into the
database and into `models.go`, where the next agent reads it before any doc. The
same pattern every column-adding migration in this folder already uses
(`20260829000002`, `20260909000001`).

If the owner prefers strictly minimal DDL, dropping that one statement changes
nothing else in this change.

### D8 — The `Down` migration is a true undo

Unlike RM57's two migrations, this one deletes no row and rewrites no value. A
`RENAME COLUMN` is a catalog operation, and the two index rebuilds cost only
index pages. So `Down` restores the exact prior shape **and** the exact prior
data, including every `account_id` value.

This is worth stating because the last three re-key migrations in this repo all
had a `Down` that was explicitly *not* an undo. This one is.

---

## Schema

### Current shape (before this change)

Table `charging.manual_charge_entries`, **24 columns**: `id`, `account_id`,
`tesla_id`, `vin`, `charged_on`, `energy_added_kwh`, `price`, `currency`,
`started_at`, `ended_at`, `start_battery_pct`, `end_battery_pct`,
`charging_type`, `location_kind`, `location_label`, `notes`, `created_at`,
`updated_at`, `inferred_capacity_kwh_calc`, `status`, `energy_source`,
`odometer_km`, `price_source`.

(That is 23 named above plus `id`; the count is not load-bearing anywhere in this
change — no test partitions this table's columns, unlike the Supercharger
mirror's self-check.)

| Object | Definition today |
|---|---|
| `account_id` | `UUID NOT NULL` |
| `tesla_id` | `BIGINT NOT NULL` |
| `manual_charge_entries_pkey` | `PRIMARY KEY (id)` |
| `idx_manual_charge_entries_vehicle_time` | `(account_id, tesla_id, charged_on DESC)` |
| `idx_manual_charge_entries_account_time` | `(account_id, charged_on DESC)` |

There is **no foreign key** on `account_id` and none on `tesla_id`
(`20260718000001` says so explicitly). Nothing about `ON DELETE` changes, because
there is no `ON DELETE` to change.

Both indexes were created unqualified in `20260718000001` and moved into the
`charging` schema with the table by `20260902000003_move_charging_to_own_schema.sql`
(`ALTER TABLE … SET SCHEMA` carries a table's indexes with it). Their names are
unchanged.

### Migration — up

File: `internal/charging/db/migrations/20260914000001_demote_manual_charge_account_id.sql`

```sql
-- +goose Up
-- A charge happened to a car, so tesla_id is the key. A person still typed the
-- row, and nothing else records who: tesla_id cannot say it (a car may have two
-- registered accounts) and the account's vehicle list records registration, not
-- authorship. So the column is kept and renamed rather than dropped. After this
-- migration it is authorship only -- never a key, never a predicate.
--
-- RENAME COLUMN is catalog-only: no row is rewritten, and Postgres rewrites the
-- definitions of the two indexes below to follow the new name. They are dropped
-- anyway, because they lead on a column nothing filters by any more.
ALTER TABLE charging.manual_charge_entries
    RENAME COLUMN account_id TO created_by_account_id;

COMMENT ON COLUMN charging.manual_charge_entries.created_by_account_id IS
    'Which account typed this entry. Authorship only: no query filters, joins, '
    'orders or groups by this column, and none may. Reads are per vehicle, so an '
    'entry is visible to every account registered to its car. Renamed from '
    'account_id, which used to be this table''s tenant key. NOT NULL and not a '
    'foreign key -- a cross-module FK into the account module would couple this '
    'module''s migrations to that schema.';

-- Nothing reads an account-wide charge list any more: the account-wide read
-- became a read over the account's vehicles. An index no query uses is pure
-- write and storage cost. Schema-qualified, because an index name resolves
-- through search_path and goose does not guarantee charging is on it.
DROP INDEX charging.idx_manual_charge_entries_account_time;

DROP INDEX charging.idx_manual_charge_entries_vehicle_time;

-- The same read this index always served, one column narrower: tesla_id
-- equality prunes to the car, and charged_on DESC satisfies the ORDER BY inside
-- that same scan, so the planner needs no sort step.
CREATE INDEX idx_manual_charge_entries_vehicle_time
    ON charging.manual_charge_entries (tesla_id, charged_on DESC);
```

### Migration — down

```sql
-- +goose Down
-- A true undo. Nothing was deleted and no value was rewritten, so restoring the
-- catalog restores the table exactly as it was, every account id included.
DROP INDEX IF EXISTS charging.idx_manual_charge_entries_vehicle_time;

COMMENT ON COLUMN charging.manual_charge_entries.created_by_account_id IS NULL;

ALTER TABLE charging.manual_charge_entries
    RENAME COLUMN created_by_account_id TO account_id;

CREATE INDEX idx_manual_charge_entries_vehicle_time
    ON charging.manual_charge_entries (account_id, tesla_id, charged_on DESC);

CREATE INDEX idx_manual_charge_entries_account_time
    ON charging.manual_charge_entries (account_id, charged_on DESC);
```

### Index plan

The rule this plan follows (MAG-63, binding on every RM57 and RM58 tier): an
index exists for a query that reads it today. An index nothing reads is dropped,
not kept "just in case".

| Index | Action | Shape after | The query that reads it, and where the caller lives |
|---|---|---|---|
| `manual_charge_entries_pkey` | keep, untouched | `(id)` | `UpdateEntry` and `DeleteEntry` (`db/query.sql`), both `WHERE id = @id AND created_by_account_id = @created_by_account_id` (D4). The primary key resolves the row in one point lookup and the second predicate is evaluated on that single row, so it needs no index of its own — and gets none, because the column is dropped from the WHERE clause entirely in tier 2. Callers: `writerService.Update` / `.Delete` (`internal/charging/service.go`), from `ExternalChargeRowUpdate` / `ExternalChargeRowDelete` (`internal/gateway/handlers/external_charges.go`). |
| `idx_manual_charge_entries_vehicle_time` | **replace** | `(tesla_id, charged_on DESC)` | Four queries, all in `db/query.sql`. **(a)** `ListEntriesByVehicle` — `WHERE tesla_id = $1 ORDER BY charged_on DESC LIMIT n`; exact match, no sort step. Caller: `reader.RecentEfficiency` (`internal/analytics/reader.go:146`). **(b)** `ListEntriesByVehicleBetween` — `WHERE tesla_id = $1 AND charged_on BETWEEN $2 AND $3 ORDER BY charged_on DESC`; one range scan inside the vehicle, no sort. Callers: `buildExternalChargesPage` and `inProgressConflictOn` (`internal/gateway/handlers/external_charges.go:651,845`) and `recalculator.Recalculate` (`internal/analytics/recalculate.go:163`). **(c)** `ListEntriesByVehicleUpdatedSince` — `WHERE tesla_id = $1 AND updated_at >= $2 ORDER BY charged_on DESC`; `tesla_id` prunes, `updated_at` is a residual filter inside that scan. Caller: `recalculator.Reconcile` (`internal/analytics/recalculate.go:283`). **(d)** `ListEntriesByVehicles` — `WHERE tesla_id = ANY($1) ORDER BY charged_on DESC LIMIT n`; see the note below. Callers: `fetchEntryVM` and `fetchEntryTeslaIDAndChargedOn` (`internal/gateway/handlers/external_charges.go:778,800`). |
| `idx_manual_charge_entries_account_time` | **drop** | — | Its only reader was `ListEntriesByAccount`, which this change replaces. No query filters on `created_by_account_id` afterwards, so nothing can use an index leading on it. |
| — | **none added** | — | `ListValidManualEntryCapacitiesForPeriod` (`db/query.sql`) filters `energy_source = 'USER'` and a `charged_on` range with no vehicle. It has no index today and gets none: it is a once-a-month write-path batch (`MonthlyCapacityCalculator.Calculate`, `internal/charging/monthly_capacity.go`), not a read path, and the project's read-heavy profile does not buy indexes for monthly batches. This query is otherwise untouched by this change. |

Four notes on why this is the whole list.

- **The multi-vehicle read is the one query that may sort, and that is
  accepted.** With one id in the array, the planner prunes on the index's leading
  column and walks `charged_on DESC` in index order. With more than one id, a
  btree scan over an array of values returns rows grouped per value rather than
  globally ordered, so a `Sort` node is expected on top. The input to that sort is
  exactly the rows of one account's own cars, on a table a person fills by hand —
  a few rows a week per car. The cost is real and small, and it is paid on a
  lookup-one-entry-by-id path, not on a page render.
- **Why a second index on `(charged_on DESC)` is not added to remove that sort.**
  It cannot prune by vehicle at all, so the scan would walk rows belonging to
  every other account's cars and filter them out; with a `LIMIT` it can walk a
  long way before finding the account's own rows. It also costs a second index on
  every write. It trades a guaranteed whole-table walk for a sort over a handful
  of rows.
- **Why nothing replaces the dropped `account_id` leading column.**
  `ai/go-conventions.md` says every multi-tenant table leads its index with
  `account_id`. After this change the table is not multi-tenant at the row level:
  it has no tenant key, only an authorship stamp. The convention's own reason —
  "every dashboard read scopes by account" — no longer describes any query here;
  every one of them scopes by vehicle. Same conclusion RM57 reached for
  `supercharger_sessions`.
- **Why `ListEntriesByVehicleUpdatedSince` still gets no index of its own.** It
  orders by `charged_on DESC`, so a `(tesla_id, updated_at)` index would satisfy
  the predicate and then force a sort — strictly worse than the current plan,
  which prunes to one vehicle on the shared index and evaluates
  `updated_at >= $1` as a residual filter inside that already-narrow scan. The
  volume argument is unchanged: this table is user-typed and small.

### Makefile, guards and codegen — checked, with findings

| Thing checked | Finding |
|---|---|
| `MIGRATIONS_DIRS` order (`account telemetry charging analytics`) | **Unaffected, and verified — not an assumption.** This migration reads no other module's table, so `charging`'s position in the order does not matter to it. In the other direction, no `analytics` migration reads `charging.manual_charge_entries`: `internal/analytics`' only cross-module migration read is `telemetry.vehicle_snapshots` (`20260908000002`). The cross-module DROP hazard `ai/go-conventions.md` warns about does not apply — this change drops no column. |
| `make migration-guard` | **Run, and it passes today.** Version `20260914000001` is free across all four module directories; the highest version anywhere is `20260912000003`. The guard also prints its standing warning about the pre-existing `20260720000001` collision in this same folder — that is backlog item 21 and is not touched here. |
| The historic backfill in `20260823000001_add_charge_sessions.sql` | **Checked — it does not read this table.** It backfills `charge_sessions` from `telemetry.supercharger_sessions`. Nothing in that file names `manual_charge_entries`' `account_id`, so this rename cannot break it. Historic migrations are frozen and none is edited. |
| `20260720000001_require_location_kind.sql` | **Checked — unaffected.** It updates and alters `location_kind` only. It names the table unqualified, which still resolves, and it never names `account_id`. |
| `sqlc` inputs | `sqlc.yaml`'s charging entry points `schema:` at `internal/charging/db/migrations` and `queries:` at `internal/charging/db/query.sql`. Both change, neither moves. The `rename:` key `charging_manual_charge_entry: "ManualChargeEntry"` is a **table** mapping and is unaffected by a column rename. Run `make sqlc` after the migration and the query edits. |
| `make db-setup` / `db-setup-test` / `db-reset` role and ownership | Unaffected. No new schema, no new table, no new role. The migration runs as `APP_ROLE`, which already owns the `charging` schema. |
| `make boundary-guard` | Unaffected. The guard forbids `internal/telemetry` imports inside `internal/gateway`; this change adds none. |
| `make vehicleref-guard` | Checked. This change adds no `vehicleref` import and no `Ref` parameter — the ports take a plain `int64` (D3). Tier 2 and tier 3 own the `Ref` work. |
| `make archive-guard` | Nothing under `openspec/changes/archive/` is edited. A grep for `account_id` hits archived designs; those are the record of what was decided then and they stay. |
| `make tz-guard`, `money-guard`, `i18n-guard`, `ui-guard` | Unaffected — no time-zone call, no monetary column, no user-facing string is added or changed. The gateway edits in D5 touch no template and no label. |

---

## Queries (`internal/charging/db/query.sql`)

| Query | Change |
|---|---|
| `CreateEntry` | `account_id` becomes `created_by_account_id` in the INSERT column list and `@account_id` becomes `@created_by_account_id`. Nothing else moves. The comment gains one sentence: the column records who typed the entry and is never read back as a filter. |
| `UpdateEntry` | **The predicate stays**, on the new name: `WHERE id = @id AND created_by_account_id = @created_by_account_id` (D4). Rewrite the comment: this is the only guard on the write path, it is kept for one tier only, and it is replaced by a `tesla_id` guard once the port can carry a vehicle. The immutable-column list now reads `id`, `created_by_account_id`, `tesla_id`, `vin`, `created_at`. |
| `DeleteEntry` | **The predicate stays**, on the new name: `WHERE id = @id AND created_by_account_id = @created_by_account_id` (D4). Same comment rewrite. |
| `ListEntriesByVehicle` | Drop `WHERE account_id = @account_id`. Update the index comment to `(tesla_id, charged_on DESC)`, and say the read is car-wide: it returns entries typed by any account registered to that car. |
| `ListEntriesByAccount` → **`ListEntriesByVehicles`** | Renamed, and re-keyed: `WHERE tesla_id = ANY(@tesla_ids::bigint[])`, keeping `ORDER BY charged_on DESC LIMIT @limit_count`. New comment: it serves a caller that holds a set of vehicles, the index prunes per vehicle, and a multi-vehicle array is expected to add a sort step (§Index plan). |
| `ListEntriesByVehicleBetween` | Drop `AND account_id = @account_id`. Same two comment fixes as `ListEntriesByVehicle`. Keep the "no LIMIT, the window bounds the result" paragraph. |
| `ListEntriesByVehicleUpdatedSince` | Drop `AND account_id = @account_id`. Same two comment fixes. Keep the "no new index" paragraph and restate its reason: this query orders by `charged_on`, so an `updated_at` index would force a sort. |
| `ListValidManualEntryCapacitiesForPeriod` | **Unchanged.** It never named `account_id`. |
| Every `supercharger_sessions`, `mirror_watermarks` and `monthly_effective_capacity` query | **Unchanged.** |

### What `make sqlc` will produce — and why the per-query `Row` trap does not fire

`sqlc` emits a per-query `…Row` struct when a query stops selecting a column the
table still has. Keeping `created_by_account_id` while most queries stop *reading*
it is exactly the shape that triggers it, and the stale codegen still compiles —
so this has to be checked, not assumed.

**It does not fire here, because no query stops selecting the column.** Every
row-returning query on this table uses `SELECT *` or `RETURNING *`:

| Query | Select shape | Generated result type after this change |
|---|---|---|
| `CreateEntry` | `RETURNING *` | `ManualChargeEntry` |
| `UpdateEntry` | `RETURNING *` | `ManualChargeEntry` |
| `DeleteEntry` | `:exec` | none |
| `ListEntriesByVehicle` | `SELECT *` | `[]ManualChargeEntry` |
| `ListEntriesByVehicles` | `SELECT *` | `[]ManualChargeEntry` |
| `ListEntriesByVehicleBetween` | `SELECT *` | `[]ManualChargeEntry` |
| `ListEntriesByVehicleUpdatedSince` | `SELECT *` | `[]ManualChargeEntry` |
| `ListValidManualEntryCapacitiesForPeriod` | four named columns, none of them this one | `ListValidManualEntryCapacitiesForPeriodRow` — it already had its own Row type, and this change does not alter it |

So the single model survives. `chargingdb.ManualChargeEntry` keeps every field and
renames exactly one: `AccountID` → `CreatedByAccountID`.

The params structs change as follows. **Every write params struct survives** —
D4 keeps both write predicates, so `UpdateEntry` still takes two scope values and
`DeleteEntry` still takes two parameters. Only the field name moves. The read
params are where fields disappear.

| Generated symbol | After `make sqlc` |
|---|---|
| `CreateEntryParams.AccountID` | becomes `CreatedByAccountID uuid.UUID` |
| `UpdateEntryParams.AccountID` | becomes `CreatedByAccountID uuid.UUID`; the struct survives |
| `DeleteEntryParams` | **survives**, as `{ ID uuid.UUID; CreatedByAccountID uuid.UUID }`; `Queries.DeleteEntry(ctx, arg DeleteEntryParams) error` is unchanged in shape |
| `ListEntriesByVehicleParams` | loses `AccountID`; keeps `TeslaID int64`, `LimitCount int32` |
| `ListEntriesByAccountParams` | replaced by `ListEntriesByVehiclesParams{ TeslaIds []int64; LimitCount int32 }` |
| `ListEntriesByVehicleBetweenParams` | loses `AccountID` |
| `ListEntriesByVehicleUpdatedSinceParams` | loses `AccountID` |

**Tier 2 is where `DeleteEntry` falls to a shape worth watching.** When it swaps
`created_by_account_id` for `tesla_id` it still has two parameters, so the struct
survives there too — but a tier that ever reduced it to one parameter would make
`sqlc` drop the struct and emit a bare `uuid.UUID` argument. Worth knowing before
it surprises someone.

`TeslaIds` — not `TeslaIDs` — is what sqlc's default Go naming produces from
`tesla_ids`. `internal/telemetry`'s `LatestSnapshotsByVehicles` already takes the
identical `teslaIds []int64` parameter from the identical
`ANY(@tesla_ids::bigint[])` form; mirror it rather than inventing a spelling.

---

## Ports and implementation

### Domain type (`charging.go`)

```go
type Entry struct {
    ID uuid.UUID
    // CreatedByAccountID records which account typed this entry. No read
    // filters on it: an entry is visible to every account registered to its
    // vehicle. Update and Delete still match on it, as the only guard they
    // have until they can name the vehicle instead.
    CreatedByAccountID uuid.UUID
    TeslaID            int64
    VIN                string
    // … every other field unchanged …
}
```

### Ports (`charging.go`)

```go
type Reader interface {
    ListEntriesByVehicle(ctx context.Context, teslaID int64, limit int) ([]Entry, error)
    ListEntriesByVehicles(ctx context.Context, teslaIDs []int64, limit int) ([]Entry, error)
    ListEntriesByVehicleBetween(ctx context.Context, teslaID int64, from, to time.Time) ([]Entry, error)
    ListEntriesByVehicleUpdatedSince(ctx context.Context, teslaID int64, since time.Time) ([]Entry, error)
}

type Writer interface {
    Create(ctx context.Context, e Entry) (Entry, error)
    Update(ctx context.Context, e Entry) (Entry, error)
    Delete(ctx context.Context, accountID uuid.UUID, id uuid.UUID) error
}
```

`Writer` keeps all three signatures, and `Delete`'s `accountID` **keeps doing
real work**: it is bound to `created_by_account_id` in the SQL (D4), so a delete
naming the wrong account still matches nothing. `Update` scopes the same way,
through `Entry.CreatedByAccountID`. Both doc comments keep their cross-account
promise, and both must say the guard matches the account that typed the entry —
which, for one tier, is narrower than the vehicle the entry belongs to. Tier 2
replaces the parameter with a `vehicleref.Ref` and the predicate with `tesla_id`.

`Reader`'s doc comments lose every "within an account" and every "excludes
entries belonging to other accounts" sentence, and gain the car-wide statement
from D1. `ListEntriesByVehicles`' own comment says the caller supplies the set of
vehicles it is entitled to see, and that an empty or nil slice returns a non-nil
empty result rather than everything — the empty array must not be read as "no
filter".

### `service.go`

- `store.deleteEntry` keeps its shape,
  `deleteEntry(ctx, params chargingdb.DeleteEntryParams) error` — the params
  struct survives (§What `make sqlc` will produce).
- `store.listEntriesByAccount` becomes `listEntriesByVehicles`, taking
  `chargingdb.ListEntriesByVehiclesParams`.
- `writerService.Create` binds `CreatedByAccountID: e.CreatedByAccountID`.
- `writerService.Update` renames the same field in its params literal; it does
  not drop it.
- `writerService.Delete` keeps its signature and binds
  `CreatedByAccountID: accountID`.
- The four reader methods drop `accountID` from their signature and their params
  literal. `ListEntriesByVehicles` keeps the `limit <= 0 → defaultLimit` clamp
  the account-wide read had, and passes `TeslaIds: teslaIDs`.
- `rowToEntry` maps `CreatedByAccountID: r.CreatedByAccountID`.
- The **reader** doc comments lose their account-scoping promises. The **writer**
  doc comments keep theirs, renamed to the new column.

Nothing else in `internal/charging` touches this table:
`session_writer.go`, `session_reader.go`, `session_verifier.go`,
`mirror_watermark.go` and `capacity.go` are untouched, and `monthly_capacity.go`
changes not at all — its manual-entry query never named the column.

---

## Test contract — unit tests are EXCLUDED

The ticket excludes unit tests, so this change adds **no new test file and no new
test function**. What follows is the list of existing tests whose signatures or
expectations change, with what they must assert instead. Authored before the
implementation, so a test is not rewritten to match whatever the code turned out
to do.

### Offline tests — no change, and that is a checked fact

`charging_test.go`, `entry_status_test.go`, `monthly_capacity_estimator_test.go`,
`price_source_test.go` and `session_verifier_derivation_test.go` name
`AccountID`, `accountID` and `account_id` **zero** times between them. No pure
function changes behaviour here: the change is schema, queries and signatures.
`tasks.md` T0 re-runs that grep rather than trusting this paragraph.

### `db_integration_test.go` — the manual-entry suite

**Helpers.** `cleanupAccount` deletes `WHERE account_id = $1`; that column name
changes. `minEntry(accountID, teslaID)` sets `Entry.AccountID`; that field name
changes. Both must keep working for every test in the package.

**Two tests stay, and keep passing. No test is deleted, and no coverage is
lost.**

- `TestUpdate_CrossAccountIsNoOp` asserts that updating an entry while naming a
  different account fails and mutates nothing. D4 keeps that guard, so the test
  keeps passing. Only the field it sets changes: `tampered.AccountID` becomes
  `tampered.CreatedByAccountID`, and the read-back uses
  `ListEntriesByVehicle(ctx, 333, 10)` instead of the account-wide list.
- `TestDelete_CrossAccountGuard` asserts that deleting with the wrong account
  leaves the row. Same: the guard stays, the call keeps its `accountID`
  argument, and only the read-back changes to a per-vehicle list.

These two are the proof that no commit on this branch has an unauthorized write
path. **Tier 2 owns re-keying them onto `tesla_id`**, in the same change that
moves the predicate — a wrong-vehicle update or delete must then match zero rows.
`tasks.md` T5.3 records that hand-off.

**Four tests are renamed and re-keyed**, from the account-wide read to the
multi-vehicle read. Their assertions carry over unchanged apart from the call:

| Today | After | What it must still assert |
|---|---|---|
| `TestListByAccount_AccountIsolation` | `TestListByVehicles_VehicleIsolation` | Seed one entry for vehicle `V1` and one for `V2`, both under one account. `ListEntriesByVehicles(ctx, []int64{V1}, 10)` returns exactly the `V1` entry. The `V2` entry never appears. |
| `TestListByAccount_NewestFirst` | `TestListByVehicles_NewestFirst` | Same seeds as today, read through `[]int64{V1, V2}`. Ordering is `charged_on DESC` across both vehicles. |
| `TestListByAccount_Limit` | `TestListByVehicles_Limit` | `limit = 1` returns exactly one row, the newest `charged_on`. |
| `TestListByAccount_EmptyNonNil` | `TestListByVehicles_EmptyNonNil` | A vehicle id with no entries returns a non-nil, zero-length slice and no error. An **empty** `[]int64{}` must also return a non-nil, zero-length slice — never every row. |

**One test changes its meaning, and it is the proof of D1.**
`TestMultiTenantIsolation_NeverLeaks` seeds one entry by `alice` and one by `bob`,
both for the same `sharedTeslaID`, and today asserts each account sees only its
own. It becomes `TestSharedVehicle_ReadsAreCarWide` and asserts the opposite:

- `ListEntriesByVehicle(ctx, sharedTeslaID, 100)` returns **both** entries, one
  carrying `CreatedByAccountID == alice` and one `== bob`.
- `ListEntriesByVehicles(ctx, []int64{sharedTeslaID}, 100)` returns the same two.
- Each returned entry still carries the `CreatedByAccountID` of whoever typed it
  — the demoted column is still stored and still read back, it just does not
  filter a read.

Taken together with the two tests above, the suite now states the tier's real
shape in one place: **a co-owner can read the other account's entry for a shared
car, and cannot yet write it** (D4). That asymmetry is the tier's known cost, and
it is visible in the tests rather than only in prose.

**Every other test in the file** drops `accountID` from its `ListEntries*` calls
and renames `AccountID` on any `Entry` literal or assertion. Expectations do not
move, with one class of exception: `TestListByVehicleBetween_AccountIsolation`
seeds two accounts holding entries for the **same** `tesla_id` inside the same
window and asserts only one comes back. It becomes
`TestListByVehicleBetween_ReturnsBothAccountsEntries` and asserts both come back,
in `charged_on DESC` order. `TestListByVehicleBetween_VehicleIsolation` is
unaffected — vehicle isolation is exactly what survives.

### `db_entry_status_integration_test.go`, `db_promotion_price_source_integration_test.go`, `db_inferred_capacity_entries_integration_test.go`

Mechanical only. They call `ListEntriesByAccount` / `ListEntriesByVehicle` to read
an entry back after a write, and they build `Entry` literals. Re-key the calls,
rename the field. No expectation changes: none of them depends on account
filtering — each seeds exactly one account.

### `db_monthly_capacity_integration_test.go`

Its `manual_charge_entries` seeds are raw SQL naming `account_id`. Rename the
column in each `INSERT`. The capacity expectations do not change: the monthly
query never filtered by account.

### `testdb_test.go`

Names none of the affected identifiers today. Confirm by grep; change nothing
unless the grep says otherwise.

### Cross-module tests (leader-owned, `tasks.md` T6)

- `internal/gateway/handlers/external_charges_test.go` — `fakeChargeReader`
  re-signs its four methods and renames `ListEntriesByAccount` to
  `ListEntriesByVehicles`. Its `ListEntriesByVehicleUpdatedSince` keeps its
  `panic` body.
- `internal/analytics/reader_test.go` — `fakeManualReader` re-signs its four
  methods. It records `accountID` on two of them for assertions; those
  recordings must go, because the port no longer receives one. The tests that
  asserted on a recorded account id assert on the recorded `teslaID` instead.
- `internal/analytics/recalculate_test.go` and `db_integration_test.go` — the
  same fake re-signing, plus any raw `manual_charge_entries` seed renaming
  `account_id`.

### Two hazards this project has already paid for

- **Raw SQL in `_test.go` is invisible to `go vet`.** A fixture naming
  `account_id` still compiles and `vet` stays green; it fails only when the owner
  runs the suite. `tasks.md` T5.1 and T8.4 grep every `_test.go` in the repo for
  `manual_charge_entries` **across line breaks**, not line by line — a table name
  and its column routinely sit on different lines.
- **A fixture `UPDATE`/`DELETE` that matches no row does not error.**
  `cleanupAccount` is exactly such a statement. If it keeps filtering on
  `account_id`, it silently cleans nothing and later tests fail for reasons that
  have nothing to do with this change. `tasks.md` T5.2 asserts `RowsAffected()`
  on the fixture writes this change touches.

---

## Docs this change invalidates

| File | What is wrong after this change |
|---|---|
| `internal/charging/AGENTS.md` | §Responsibility says the module "enforces multi-tenant data isolation" for manual entries — true only of the two writes now, and only for one tier. §Data Ownership's "No cross-module FK" rule says tenant scoping is "`account_id` for `manual_charge_entries`": the column name changed and reads no longer scope by it. §Public Interface describes `Writer`/`Reader` without the car-wide read rule. |
| `openspec/specs/manual-charge-log/spec.md` | Seven requirements modified, one removed, two added — this change's delta under `specs/manual-charge-log/spec.md`. |
| `kkpa/context/architecture/charging-tables.md` | The `manual_charge_entries` section's two revisit triggers both propose an "`account_id`-leading index", which is no longer a shape this table can have. |
| `kkpa/context/workflows/manual-charge-crud.md` | The `query.sql` row and the Update bullet name the write scope `account_id`. The scope survives this tier but the column is renamed, so both need the new name plus a note that it is transitional. |
| `kkpa/context/architecture/charge-record-mutation.md` | It says `fetchEntryVM` calls `ListEntriesByAccount(ctx, uid, 0)`. |
| `kkpa/context/use-case/charging/update-manual-charge.md` | Its call-chain step 5 and its read/write table name `ListEntriesByAccount`. |
| `kkpa/context/use-case/charging/delete-manual-charge.md` | Its call-chain step 4, its read/write table, and its "100-row lookup cap" note all name `ListEntriesByAccount`. |

`openspec/changes/archive/` is **not** swept, and neither is
`kkpa/context/pending-spec-to-sync/applied/`. Several archived designs and
applied proposals mention this column; that is the record of what was decided
then and it stays exactly as it is. `make archive-guard` enforces the first.

---

## Risks

- **Reads are car-wide but writes are author-only, for one tier** (D4). On a
  vehicle registered to two accounts, the External Charges page lists a co-owner's
  entry with working edit and delete controls, and the write matches zero rows:
  `Update` shows "could not update entry", and `Delete` silently changes nothing,
  so the row is still there after the fragment re-renders. Mitigation: none is
  applied here on purpose. Hiding those controls is gateway work, and tier 3
  removes the cause by authorizing on the entry's own vehicle. **The exposure is
  bounded to shared vehicles only** — on a car with one registered account,
  nothing behaves differently from today.
- **`vehicle_metrics` values change for any car with two registered accounts**
  (D1). This is intended, not a defect, but it is visible: the affected car's
  history numbers move on the next recalculation. Mitigation: none needed — the
  owner chose it — but it should not surprise anyone reading a changed chart.
- **The build is red outside `internal/charging` until D5's bridge lands.**
  Mitigation: T6 names every file and the exact edit. Run it in the same wave as
  the module implementation, not a later one.
- **Raw SQL in `_test.go` files is invisible to `go vet`**, and `cleanupAccount`
  is a fixture `DELETE` that fails silently when its predicate stops matching.
  Mitigation: T5.1, T5.2 and T8.4 above.
- **The write predicate is easy to mistake for a leftover.** After this tier the
  demoted column is still named in two `WHERE` clauses, which reads like the
  rename job was half done. Mitigation: both query comments say it is kept for one
  tier and what replaces it, D4 says the same, and the spec delta says the
  ticket's "no read filters" bar is met at the end of the roadmap.
- **An entry for a de-registered vehicle stops being findable by the gateway's
  two id-lookup helpers** (D5). It was already absent from the page's own list.
  Tier 3 revisits how those helpers resolve their vehicle set.
