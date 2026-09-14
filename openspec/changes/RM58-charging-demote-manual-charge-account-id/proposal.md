# RM58-charging-demote-manual-charge-account-id

> Source: MAG-68 — https://linear.app/magus-monitor/issue/MAG-68/5-demote-chargingmanual-charge-entriesaccount-id-to-created-by-account
> Tier 1 of `openspec/roadmaps/RM58-rekey-manual-charges-on-tesla-id.md`.
> Parent: MAG-63, step 5 of 7. No tier depends on this one being archived first,
> but tiers 2 and 3 both depend on its port shape.

## Why

`charging.manual_charge_entries` is keyed on `account_id`. A charge happened to a
car, so `tesla_id` is the right key.

A person still typed the row, and that authorship is worth keeping. So the column
is not dropped, the way RM57 dropped it from the two Supercharger tables. It is
**demoted**: renamed to `created_by_account_id`, kept as a record of who entered
the charge, and never used again as a key or a read filter.

Manual entries were the last charging source still filtered by account. RM57
already made Supercharger sessions car-wide. After this change the two sources
agree.

## What changes

- **Migration**
  (`internal/charging/db/migrations/20260914000001_demote_manual_charge_account_id.sql`):
  `RENAME COLUMN account_id TO created_by_account_id`, drop
  `idx_manual_charge_entries_account_time`, and replace
  `idx_manual_charge_entries_vehicle_time` with its one-column-narrower
  vehicle-keyed form `(tesla_id, charged_on DESC)`. One new column `COMMENT`
  records that the column is authorship only. No data is deleted or rewritten.
  Full SQL and index plan: `design.md`.
- **Queries** (`internal/charging/db/query.sql`): the `account_id` predicate
  leaves the three per-vehicle reads — `ListEntriesByVehicle`,
  `ListEntriesByVehicleBetween`, `ListEntriesByVehicleUpdatedSince`.
  `ListEntriesByAccount` becomes `ListEntriesByVehicles`, taking a `tesla_id`
  array. `CreateEntry` keeps writing the column under its new name.
  **`UpdateEntry` and `DeleteEntry` keep their predicate**, renamed to
  `created_by_account_id` — transitional and deliberate, so no commit ever has an
  unauthorized write path (`design.md` D4).
- **Ports** (`internal/charging/charging.go`): the four `Reader` methods drop
  `accountID` and keep a plain `int64` vehicle id (`design.md` D3).
  `ListEntriesByAccount` becomes `ListEntriesByVehicles(ctx, teslaIDs []int64,
  limit int)`. `Writer` keeps its signatures.
- **Domain type**: `Entry.AccountID` is renamed `Entry.CreatedByAccountID`, so
  the Go field and the column cannot drift apart (`design.md` D6).
- **Tests**: updated, never added. Unit tests are excluded by the ticket. The
  five offline test files name the column zero times; only the
  `manual_charge_entries` integration suites and the cross-module fakes change.
- **Docs**: `internal/charging/AGENTS.md`, this change's delta on
  `openspec/specs/manual-charge-log/spec.md`, and four KB guides
  (`design.md` §Docs).

`charging.supercharger_sessions`, `charging.mirror_watermarks` and
`charging.monthly_effective_capacity` are **not touched**.

## Breaking?

**Yes, internally.** This module exposes no external API and no HTTP surface, so
nothing outside the repository breaks.

Inside the repository four read ports change shape, one is renamed, and one
domain field is renamed. `go build ./...` fails outside `internal/charging` until
the cross-module bridge lands. That bridge is small and leader-owned — see
`design.md` D5 and `tasks.md` T6.

**It is also a behaviour change, not only a schema change** (`design.md` D1). The
reads become car-wide: `internal/analytics` now reads every manual charge for a
car, whoever typed it. If two accounts are registered to the same vehicle, their
`vehicle_metrics` values change. The owner chose this.

**The write path stays authorized at every commit** (`design.md` D4).
`UpdateEntry` and `DeleteEntry` keep their predicate for this one tier, on the
renamed column, and tier 2 swaps it for the `tesla_id` guard. The guard is never
dropped.

The price of that is one tier where **reads are car-wide but writes are
author-only**. On a vehicle registered to two accounts, the External Charges page
will list a co-owner's entry with edit and delete controls that then fail —
`Update` shows an error, `Delete` silently changes nothing. On a car with one
registered account nothing behaves differently from today. Tier 3 removes the
cause. Full reasoning, including what this design first proposed and why the owner
overruled it, is in `design.md` D4.

## Modules affected

- `internal/charging` — owns the table, the queries, the ports, the domain type
  and every test fixup inside the module. This worker's sandbox.
- `internal/analytics` — **leader-owned**. Three call sites drop their `accountID`
  argument (`reader.go:146`, `recalculate.go:163`, `recalculate.go:283`), plus the
  fakes and the raw seeds in its tests.
- `internal/gateway` — **leader-owned**, and mechanical only. Two
  `ListEntriesByVehicleBetween` calls drop `uid`; `fetchEntryVM` and
  `fetchEntryTeslaIDAndChargedOn` switch from `ListEntriesByAccount` to
  `ListEntriesByVehicles`, fed by the account's registered vehicles; two
  `entry.AccountID` assignments are renamed. Tier 3 then routes these through
  `authorizeVehicle`; this change only keeps the build green.

## Read paths affected

- **The External Charges page** (`GET /charges`, `GET /ui/charges/...`) does one
  `ListEntriesByVehicleBetween` read per render. It keeps running as a single
  index range scan with no sort step — the same shape as today, one column
  narrower (`design.md` §Index plan).
- **The analytics recalculation** reads `ListEntriesByVehicleBetween` and
  `ListEntriesByVehicleUpdatedSince` per vehicle, and `ListEntriesByVehicle` from
  `RecentEfficiency`. All three keep their current plan shape.
- **The entry-by-id lookup** (`fetchEntryVM`, `fetchEntryTeslaIDAndChargedOn`)
  moves from one account-wide read to one multi-vehicle read. It gains a sort
  step for an account with more than one car, over a set of rows that is already
  small (`design.md` §Index plan).
- **The monthly capacity batch** is untouched: it never filtered by account.

## Non-goals

- Retyping `Update` and `Delete` to `vehicleref.Ref`, and the `WHERE id = $1 AND
  tesla_id = $2` write guard — roadmap tier 2
  (`RM58-charging-entry-writes-take-ref`). **Removing the two writes'
  `created_by_account_id` predicate belongs to that same tier**, which drops it in
  the change that adds the replacement. Re-keying the two cross-account write
  tests onto `tesla_id` goes with it.
- Reaching the ticket's "no read filters on `created_by_account_id`" bar. Three
  reads meet it here; the two writes meet it in tier 2. The bar is a
  roadmap-level goal, not a tier-1 one.
- The gateway authorization work — proving the **entry's** `tesla_id` with
  `authorizeVehicle` before a write — roadmap tier 3
  (`RM58-gateway-authorize-manual-charge-writes`).
- Dropping `created_by_account_id`. It stays, on purpose, as the record of who
  typed the charge.
- Renumbering the pre-existing `20260720000001` migration-version collision in
  this same folder. That is backlog item 21.
- The table `COMMENT`, which still says "No cross-module FK on account_id". This
  project does not ship a migration to correct comment text (`design.md` D7).
