# RM58-gateway-authorize-manual-charge-writes

> Source: MAG-68 — https://linear.app/magus-monitor/issue/MAG-68/5-demote-chargingmanual-charge-entriesaccount-id-to-created-by-account
> Tier 3 of `openspec/roadmaps/RM58-rekey-manual-charges-on-tesla-id.md` — the last tier.
> Depends on tier 1 (`RM58-charging-demote-manual-charge-account-id`, archived) and tier 2
> (`RM58-charging-entry-writes-take-ref`, archived).

## Why

Tier 2 already closed the authorization gap: `ExternalChargeRowUpdate` and
`ExternalChargeRowDelete` prove the entry's own vehicle with `authorizeVehicle` and hand
the resulting `vehicleref.Ref` to `charging.Writer.Update`/`Delete`. Writes are safe.

What tier 2 left alone is the **lookup** half. `fetchEntryVM` and
`fetchEntryTeslaIDAndChargedOn` — the two helpers both write handlers call before they can
authorize anything — still build their vehicle-id list with a free helper, `teslaIDsOf`,
reading straight over `acct.RegisteredVehicles`. That helper does the same "which vehicles
may this account see" computation `authorizeVehicle` does, but outside the one file
`make vehicleref-guard` watches, and without producing a `vehicleref.Ref` at all.

This tier gives the gateway **one** place that answers "the vehicles this account may
see": a new `ownedVehicles` helper in `handlers.go`, next to `authorizeVehicle`. Both
lookup helpers call it instead of `teslaIDsOf`. `teslaIDsOf` is deleted.

## What changes

- **`internal/gateway/handlers/handlers.go`** gains
  `ownedVehicles(ctx, uid) (refs []vehicleref.Ref, vehicles []account.Vehicle, ok bool)`,
  placed next to `authorizeVehicle`. It calls `acct.RegisteredVehicles` once and returns
  both the plain vehicle list (needed for `vehicleLabelFor`) and the same list wrapped as
  `vehicleref.Ref` values (needed for `ListEntriesByVehicles`'s `[]int64` argument, via the
  already-exported `vehicleref.TeslaIDs`). `ok` is `false` on a lookup error or an empty
  list — an empty vehicle list is never treated as "no filter".
- **`internal/gateway/handlers/external_charges.go`**: `fetchEntryVM` and
  `fetchEntryTeslaIDAndChargedOn` call `h.ownedVehicles` instead of
  `acct.RegisteredVehicles` + `teslaIDsOf`, and build the id argument to
  `ListEntriesByVehicles` with `vehicleref.TeslaIDs(refs)`. The free helper `teslaIDsOf` is
  deleted.
- **Tests**: a new `ownedVehicles_test.go` (or an addition to `authorize_vehicle_test.go`)
  covers `ownedVehicles` directly, mirroring the existing `authorizeVehicle` test contract.
  No existing test changes, because no observable behaviour changes (see below).
- **Docs**: `internal/gateway/AGENTS.md` "Vehicle ownership — proved once, at the gateway"
  gains `ownedVehicles` alongside `authorizeVehicle`. Two KB guides that quote the literal
  `teslaIDsOf(vehicles)` call — `kkpa/context/architecture/charge-record-mutation.md` and
  `kkpa/context/use-case/charging/delete-manual-charge.md` — are updated to quote the new
  call instead.

**Not one line of Go logic changes what either handler does.** `fetchEntryVM` and
`fetchEntryTeslaIDAndChargedOn` read the same source (`acct.RegisteredVehicles`), apply the
same "error or empty ⇒ not found" rule, and pass the same set of Tesla ids to
`ListEntriesByVehicles`, in the same order. This tier is a **structural** change — one
seam instead of two computations of the same fact — not a behaviour change. See
`design.md` for why scoping the lookup to the account's own vehicles already proves
ownership by construction, independent of which function does the scoping.

## Is a spec delta needed?

**Yes — one, and only one.** No requirement about `internal/charging` or
`internal/gateway`'s write path changes: `openspec/specs/manual-charge-log/spec.md`'s
update/delete ownership-proof requirements are already satisfied and unaffected, and
`openspec/specs/vehicleref/spec.md`'s "Vehicle Ownership Check Happens At The Gateway"
requirement already covers a single named vehicle. That requirement, however, has a
sibling — "Proof For Every Owned Vehicle," the bulk-proof primitive — that the two lookup
helpers this tier changes do **not** use today. Today they assemble their `[]int64` with
`teslaIDsOf`, computed beside the call, not from the bulk proof. After this tier they do.

That is a genuine, checkable property that is **false before this change and true after
it**: "every gateway read scoped to the caller's whole fleet gets its vehicle identities
from the bulk ownership proof, not from an ad hoc list." It is a fact about the security
boundary — a WHAT — not about which function computes it — a HOW — so it does not carry
the category problem an implementation-shaped requirement would.

**Checked, not just asserted, that the requirement is complete.** `ListEntriesByVehicles`
— the port a fleet-wide read calls — has exactly two call sites in `internal/gateway/`,
both the helpers this tier fixes. The one other candidate,
`buildExternalChargesPage`'s `ListEntriesByVehicleBetween` call, filters by a single
selected vehicle (`teslaIDFilter`), not the caller's fleet — out of scope, and still true
before and after this tier. `ListVehicles`/`SeedVehicles` in `handlers.go` are Tesla-API
and signup-seeding calls, not owned-fleet reads. So after this tier, this requirement
holds for every fleet-wide read in the gateway, not only for the two named helpers.

The new requirement — "A Fleet-Wide Read Uses The Bulk Ownership Proof" — is added under
`openspec/changes/RM58-gateway-authorize-manual-charge-writes/specs/vehicleref/spec.md`
as an `## ADDED Requirements` delta on the `vehicleref` capability, with three scenarios:
a caller with vehicles gets a read scoped to exactly those ids, a caller with no vehicles
gets no read at all (the "empty is not 'no filter'" rule, restated as the
security-relevant half), and a lookup failure is treated the same as owning nothing. It
names no function, no type, and no file — that detail lives in `design.md`, which already
has it.

## Breaking?

**No.** `ownedVehicles` is a new, unexported helper. `fetchEntryVM` and
`fetchEntryTeslaIDAndChargedOn` keep their existing signatures and return values.
`teslaIDsOf` is unexported and has no caller outside the two functions being changed, and
no test calls it directly (checked: `grep -rn teslaIDsOf internal/gateway` has exactly the
three hits this change touches — the definition and its two call sites). Deleting it
breaks nothing outside this file.

## Modules affected

- `internal/gateway` only. No other module is touched — this tier is scoped entirely to
  `internal/gateway/handlers/handlers.go` and `external_charges.go`, per the roadmap's own
  tier-3 row.

## Read paths affected

None. `ListEntriesByVehicles` is called with the same argument value it receives today —
only the code path that assembles that argument changes. No query, index, or read
pattern changes.

## Non-goals

- Touching `internal/charging` or `internal/analytics`. Both are already correct after
  tiers 1 and 2.
- Any database object. `design.md` states this explicitly.
- Changing what happens on a lookup miss (still 404, never 403) or on an
  `authorizeVehicle` failure. Both stay exactly as tier 2 left them.
- Widening `ownedVehicles`'s use beyond the two named call sites. The three other
  `RegisteredVehicles` call sites in `external_charges.go` (around lines 256, 384, 664)
  keep their own calls — they answer HTTP 500 with an i18n message on a lookup error,
  where the two lookup helpers answer a silent `false`. Folding them into `ownedVehicles`
  would change what the user sees on an account-lookup failure, which is out of scope for
  a change whose whole point is "no behaviour changes." `design.md` §D2 explains this in
  full.
