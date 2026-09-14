# RM58-charging-entry-writes-take-ref

> Source: MAG-68 — https://linear.app/magus-monitor/issue/MAG-68/5-demote-chargingmanual-charge-entriesaccount-id-to-created-by-account
> Tier 2 of `openspec/roadmaps/RM58-rekey-manual-charges-on-tesla-id.md`.
> Parent: MAG-63, step 5 of 7. Depends on tier 1
> (`RM58-charging-demote-manual-charge-account-id`, archived), which renamed
> `account_id` to `created_by_account_id` and kept it as a transitional write
> guard. This tier removes that guard and replaces it.

## Why

Tier 1 renamed `manual_charge_entries.account_id` to `created_by_account_id`
and made every read car-wide, but kept the old account predicate on
`UpdateEntry` and `DeleteEntry` for one tier — the only guard those two writes
had until a port could carry a vehicle instead of an account. That was a
known, stated cost: for one tier, reads were car-wide but writes were
author-only, so a co-owner of a shared car could see another account's entry
but not edit or delete it.

This tier closes that gap by giving `Update` and `Delete` a vehicle to check
against. `internal/vehicleref.Ref` already exists for exactly this: a value
that only `internal/gateway`'s `authorizeVehicle` can construct, so a caller
that never proved ownership has nothing to pass — the mistake fails to
compile. `internal/charging`'s own `SessionVerifier.VerifySession` already
takes one, for the Supercharger side of this same problem
(`RM57-charging-verifysession-takes-ref`). This tier applies the identical
pattern to the two manual-entry writes.

## What changes

- **Ports** (`internal/charging/charging.go`): `Writer.Update` gains a
  `vehicleref.Ref` parameter; `Writer.Delete`'s `accountID uuid.UUID`
  parameter becomes a `vehicleref.Ref`. `Writer.Create` is untouched.
- **Queries** (`internal/charging/db/query.sql`): `UpdateEntry` and
  `DeleteEntry` swap `WHERE ... AND created_by_account_id =
  @created_by_account_id` for `WHERE ... AND tesla_id = @tesla_id`.
  `DeleteEntry` becomes `:execrows` so a caller can tell a rejected delete
  from a real one. Full reasoning: `design.md` D1–D4.
- **Service** (`internal/charging/service.go`): `writerService.Update`
  overwrites `e.TeslaID` from the `Ref` before deriving energy, so a stale or
  wrong caller-supplied vehicle can never leak into `resolveEnergy`.
  `writerService.Delete` reports `pgx.ErrNoRows` when zero rows matched
  (`design.md` D3, D4).
- **Domain type**: `Entry.TeslaID` gains a doc comment explaining it is
  caller-trusted on `Create` and module-overwritten on `Update`
  (`design.md` §Ports and implementation).
- **Tests**: updated, never added — the ticket excludes unit tests. Every
  `Writer.Update`/`Writer.Delete` call site across four integration test files
  gains a `vehicleref.Ref` argument. Two tests — the cross-guard proofs tier 1
  kept — are renamed from account-keyed to vehicle-keyed, and
  `TestDelete_CrossVehicleGuard` asserts a behaviour change: a mismatched
  delete now returns an error instead of silently doing nothing
  (`design.md` §Test contract).
- **Docs**: `internal/charging/AGENTS.md`, this change's delta on
  `openspec/specs/manual-charge-log/spec.md` (four requirements modified), and
  four KB guides (`design.md` §Docs).

`charging.supercharger_sessions`, `charging.mirror_watermarks`,
`charging.monthly_effective_capacity`, and every `Reader` method are **not
touched**. No table, column, index, or migration is added, dropped, or
altered — see `design.md` D9.

## Breaking?

**Yes, internally.** This module exposes no external API and no HTTP
surface, so nothing outside the repository breaks.

Inside the repository, `Writer.Update` and `Writer.Delete` change signature.
`go build ./...` fails outside `internal/charging` until `internal/gateway`'s
two production call sites and one test fake are updated — small, mechanical,
and leader-owned (`design.md` D8, `tasks.md` T6).

**`Delete` also changes observable behaviour**, not only signature: a delete
naming the wrong vehicle now returns an error instead of silently changing
nothing. `ExternalChargeRowDelete` already has an error branch that handles
this correctly with no further gateway change — checked in `design.md` D4.

## Modules affected

- `internal/charging` — owns the ports, the two queries, the service
  implementation, the domain type's doc comment, and every test fixup inside
  the module. This worker's sandbox.
- `internal/gateway` — **leader-owned**, and mechanical only. `authorizeVehicle`
  called on the already-resolved entry `tesla_id` in
  `ExternalChargeRowUpdate` and `ExternalChargeRowDelete`
  (`external_charges.go`); the resulting `Ref` handed to `Update`/`Delete`.
  `fakeChargeWriter` (`external_charges_test.go`) re-signed to keep
  implementing `charging.Writer`. Full file list and exact edits:
  `tasks.md` T6.
- `internal/analytics` — **not touched**. It calls only `Reader` methods
  (untouched by this tier) and `Writer.Create` (unchanged, seeding only, in
  its own tests) — checked by reading every `charging.Writer`/`charging.Reader`
  call site in the module.

## Read paths affected

None. This change touches no `Reader` method and no `SELECT`/`RETURNING`
list — only two write queries' `WHERE` clause and one write query's
annotation. `design.md` §Schema explains why the index plan is unaffected:
both writes resolve their row by primary key first, and the second predicate
— whichever column it names — is evaluated on that one already-located row.

## Non-goals

- Re-feeding `fetchEntryVM` / `fetchEntryTeslaIDAndChargedOn` through
  `vehicleref.All` / `vehicleref.TeslaIDs` instead of
  `acct.RegisteredVehicles`. Roadmap tier 3
  (`RM58-gateway-authorize-manual-charge-writes`).
- Changing what happens when the entry lookup misses (today
  `ExternalChargeRowDelete`'s delete still proceeds on a miss). Tier 3.
- `external_charges_tiles.go`. Tier 3.
- Any database object. See `design.md` D9 — if implementation finds a reason
  one is needed, that is a STOP, not a decision made here.
- A `GetEntry` port. The 100-row lookup cap `fetchEntryVM` /
  `fetchEntryTeslaIDAndChargedOn` carry is a known, separately tracked gap
  (`kkpa/context/use-case/charging/delete-manual-charge.md`), unaffected by
  this tier.
