# Design — RM58-gateway-authorize-manual-charge-writes

Source ticket: MAG-68. Roadmap tier 3 (last tier).
This change touches no database object and no port signature. It moves one computation —
"which vehicles may this account see" — from two duplicated call sites into one named
function. Unit tests: **excluded** by the ticket, except one new small test file/addition
proving the new helper's own three cases (empty account list, error, and the normal
case), because that helper does not exist yet and needs its own direct coverage the same
way `authorizeVehicle` already has one.

## Overview

Today, three functions in `internal/gateway/handlers` independently answer "which
vehicles may account `uid` see": `resolveSelectedVehicle`, `authorizeVehicle`, and — via
the free helper `teslaIDsOf` — both `fetchEntryVM` and `fetchEntryTeslaIDAndChargedOn`.
The first two already read `acct.RegisteredVehicles` and are outside this tier's scope
(`resolveSelectedVehicle` answers a different question — "what is the ONE currently
selected vehicle" — and `authorizeVehicle` already exists). The third — the pair behind
`teslaIDsOf` — is what changes here.

This tier adds one function, `ownedVehicles`, that both lookup helpers call instead of
computing the answer themselves via `teslaIDsOf`. `ownedVehicles` sits in `handlers.go`,
next to `authorizeVehicle`, so `make vehicleref-guard` can see it: the guard already
allows `vehicleref.All` to be called from that one file, and `ownedVehicles` is the call
site that uses that allowance.

**Nothing downstream of the lookup changes.** `fetchEntryVM` still returns
`(fragments.ExternalChargeEntryVM, bool)`; `fetchEntryTeslaIDAndChargedOn` still returns
`(teslaID int64, chargedOn time.Time, ok bool)`. Both still call
`h.chargingReader.ListEntriesByVehicles(ctx, <ids>, 0)` with the exact same set of ids, in
the exact same order, as today — only the code path that assembles `<ids>` changes.

---

## Decisions

### D1 — `ownedVehicles` returns BOTH `[]vehicleref.Ref` and `[]account.Vehicle`, from ONE read

`fetchEntryVM` needs the plain `[]account.Vehicle` to call
`externalChargeEntryVMFromEntry(e, vehicles)`, which resolves the vehicle's display label
via `vehicleLabelFor`. `fetchEntryTeslaIDAndChargedOn` does not need vehicles at all — it
only needs the id list. Both need the id list, and `ListEntriesByVehicles` takes
`[]int64`, not `[]vehicleref.Ref` (the `Reader` port is untouched — see the roadmap's own
D5 in tier 2's design.md: `internal/analytics` calls the same `Reader` methods with plain
ids, on the nightly path with no signed-in user and no `Ref` to build, so the port cannot
require one).

So the id list has to be produced from the `[]account.Vehicle` one way or another.
`vehicleref.TeslaIDs` already exists as the "unwrap a `[]Ref` back to `[]int64`" function,
exported precisely so a caller holding refs does not write its own unwrap loop. Given that
`ownedVehicles` already builds refs (via `vehicleref.All`, inside the guarded file — see
D2), returning them lets both call sites get the id list from
`vehicleref.TeslaIDs(refs)` — a call to an unrestricted, already-existing function — rather
than writing a second private "vehicles → ids" loop next to the one `ownedVehicles` itself
needed to build `refs`. That second loop is exactly what `teslaIDsOf` was, and this
change's whole point is not to keep it under a new name.

**One database read, both values.** `acct.RegisteredVehicles(ctx, uid)` runs exactly once
inside `ownedVehicles`; the two return values are two views of that one result, not two
reads.

Rejected alternative — **`ownedVehicles` returns only `[]account.Vehicle`; callers build
ids themselves.** Rejected: this just moves `teslaIDsOf`'s loop inline at each of the two
call sites instead of deleting it — the module gains no closed vocabulary, and a future
third caller has the same "write a loop or call a stray helper" choice this change exists
to remove.

Rejected alternative — **`ownedVehicles` returns only `[]vehicleref.Ref`; `fetchEntryVM`
re-derives vehicles some other way.** Rejected: there is no other way to get
`[]account.Vehicle` without a second `RegisteredVehicles` call, which is the exact
duplicate read this change removes elsewhere. `vehicleref.Ref` carries only a `TeslaID`
(see `internal/vehicleref/vehicleref.go`) — it has no display name, VIN, or access type,
so it cannot stand in for `account.Vehicle` in `vehicleLabelFor`.

### D2 — `vehicleref.All` is called from `ownedVehicles` in `handlers.go`, never from `external_charges.go`

`make vehicleref-guard` (`Makefile:741-763`) allows `vehicleref.Authorize`/`vehicleref.All`
to be called only from `internal/vehicleref` itself, from any `_test.go` file, and from
`internal/gateway/handlers/handlers.go`. `external_charges.go` is none of those. So the two
lookup helpers must receive refs already built — they cannot build their own — and the
only file that may build them is `handlers.go`. This is why `ownedVehicles` lives there
and not in `external_charges.go` next to the functions that call it.

This also answers the roadmap's own warning about a pointless round-trip: calling
`vehicleref.All` a SECOND time inside `external_charges.go` (if the guard allowed it)
right after `handlers.go` already built refs from the same ids would make
`vehicleref.TeslaIDs(vehicleref.All(ids))` — the exact identity chain the roadmap calls
out as a no-op. That is not what happens here: `vehicleref.All` runs exactly once, inside
`ownedVehicles`; `external_charges.go` only ever calls the unwrap side,
`vehicleref.TeslaIDs`, on the refs `ownedVehicles` already built. One `All`, one
`TeslaIDs`, no redundant pair.

### D3 — The three other `RegisteredVehicles` call sites in `external_charges.go` are NOT folded in

`external_charges.go` has three more call sites reading `acct.RegisteredVehicles`
directly, at (today's line numbers) 256 (`ExternalChargesPage`), 384
(`ExternalChargesListFragment`), and 664 (`buildExternalChargesPage`'s vehicle-label
lookup path). None of these route through `ownedVehicles` in this change.

The reason is what each does on a `RegisteredVehicles` error. The two lookup helpers
being changed here (`fetchEntryVM`, `fetchEntryTeslaIDAndChargedOn`) both return a bare
`false` on any error — silent, matching "not found." The three call sites left alone
instead render an HTTP 500 with a translated error message
(`i18n.KeyChargesErrorCouldNotStartChargeLog`or its neighbours) — a page-load failure the
user must see, not a lookup miss to hide. `ownedVehicles`'s own `ok bool` return
deliberately does not distinguish "error" from "empty list" (see D4) precisely because
its two current callers don't need to — folding in a caller that DOES need to
distinguish them would force `ownedVehicles` to grow a second return shape (an error
value, or a reason code) that only that one new caller would ever read. That is scope
this tier does not need and the roadmap did not ask for (see the roadmap's own D15).

Rejected alternative — **retrofit `ownedVehicles` to also serve the three read-page call
sites, returning an error instead of a bool.** Rejected: it would change three handlers
that already work correctly today, in a change whose entire premise is "no behaviour
changes." If a future change wants one shared vehicle-list seam for every call site, that
is its own proposal, with its own test contract for how each of the five call sites
should react to an error — not a side effect of this one.

### D4 — `ok` is `false` on an error OR an empty list — the existing rule survives unchanged

Both lookup helpers' current doc comments state the rule already: "An error or an empty
list must return false: an empty set of vehicles is not 'no filter', and reading every
entry would leak other accounts' data." `ownedVehicles` is the single place that rule now
lives, and it must enforce it exactly as today — see the Test contract below for the
three cases pinned as expected values before implementation.

Rejected alternative — **treat an empty (but error-free) vehicle list as "no filter" and
let `ListEntriesByVehicles` decide.** Never seriously considered; it is the exact tenancy
leak the existing doc comment already warns against, and this tier changes structure, not
that guarantee.

### D5 — Why scoping the lookup to the account's own vehicles already proves ownership by construction

This is the core reasoning the roadmap asked this design to spell out.
`ListEntriesByVehicles(ctx, ids, limit)` — the `Reader` port both lookup helpers call —
returns entries **only** for the `ids` it is given (see `internal/charging/db/query.sql`'s
`ListEntriesByVehicles`, `WHERE tesla_id = ANY(@tesla_ids)`). It has no notion of "account"
at all; it is a car-scoped read (roadmap tier 1's own decision — see the roadmap's "The
decision that sets the scope").

So the only way an entry belonging to a vehicle the account does NOT own could appear in
`entries` is if that vehicle's id were present in the `ids` slice passed in. Both lookup
helpers build that slice from `ownedVehicles`, and `ownedVehicles` builds it from nothing
but `acct.RegisteredVehicles(ctx, uid)` — the account module's own answer to "which
vehicles does this account see." A vehicle the account does not own is, by definition,
absent from that list; it can therefore never appear in `ids`; it can therefore never be
returned by `ListEntriesByVehicles`; the loop that matches `e.ID == id` can therefore
never find it.

This is exactly the same shape `vehicleref.Ref` gives `authorizeVehicle`'s callers, minus
the compile-time enforcement: a `Ref` makes "you forgot to check" a compile error, because
a port that requires one has nothing to accept from a caller that never authorized
anything. `Reader.ListEntriesByVehicles` takes a plain `[]int64`, so it cannot enforce the
same thing at compile time — but the *scoping* guarantee is identical: the returned set
can only ever be a subset of the account's own vehicles, because the input set was.
Ownership is proved by construction of the input, not by a check on the output.

**What this means for the "behaviour vs. structure" framing in `proposal.md`.** Before
this change, that construction happened in `teslaIDsOf` + its two call sites — three
places computing the same account→ids mapping, none of them named for what they jointly
guarantee. After this change, it happens once, in a function whose name and placement
(next to `authorizeVehicle`, inside the file `make vehicleref-guard` watches) say what it
is for. The guarantee itself — every id in `ids` belongs to `uid`'s account — held before
this change and holds after it, unchanged. That is why this is a structure and
guard-coverage change, not a behaviour change: nothing a user can observe (which entries
come back, what a 404 looks like, what a successful edit does) depends on which function
computed the id list.

### D6 — `resolveSelectedVehicle` is untouched and unrelated

`resolveSelectedVehicle` answers "what is the ONE vehicle currently selected in this
session" (auto-selecting the first `OWNER` vehicle when none is selected yet). It already
calls `acct.RegisteredVehicles` for its own reasons and returns a single
`(account.Vehicle, bool)`, not a list. It is not a third occurrence of the "compute the
account's whole owned-vehicle set" problem `ownedVehicles` solves — it solves a different
problem (which one is active), and nothing about the roadmap or the tier-3 interview asks
it to change. `design.md` calls this out explicitly so a later reader does not wonder why
a third `RegisteredVehicles` caller is left alone right next to the two files this change
does touch.

---

## Ports and implementation

### `internal/gateway/handlers/handlers.go`

Placed immediately after `authorizeVehicle` (today ending around line 1075), before
`ConnectTesla`:

```go
// ownedVehicles resolves uid's registered vehicles once and returns both the plain
// account.Vehicle list and their proofs as vehicleref.Ref. Two callers need two
// different shapes of the same one read -- a display label needs the vehicle struct, a
// Reader-port call needs a plain []int64 -- so this returns both instead of making each
// caller re-derive one from the other. ok is false on a lookup error or an empty list: an
// empty vehicle list is never "no filter", it is "this account has nothing to see."
func (h *Handler) ownedVehicles(ctx context.Context, uid uuid.UUID) (refs []vehicleref.Ref, vehicles []account.Vehicle, ok bool) {
	vehicles, err := h.acct.RegisteredVehicles(ctx, uid)
	if err != nil || len(vehicles) == 0 {
		return nil, nil, false
	}
	owned := make([]int64, 0, len(vehicles))
	for _, v := range vehicles {
		owned = append(owned, v.TeslaID)
	}
	return vehicleref.All(owned), vehicles, true
}
```

`vehicleref` and `account` are both already imported in `handlers.go` (used by
`authorizeVehicle` and `resolveSelectedVehicle` respectively) — no new import.

### `internal/gateway/handlers/external_charges.go`

`teslaIDsOf` (today at line 799-808) is deleted in full — definition and doc comment.

`fetchEntryVM` (today at line 810-833):

```go
func (h *Handler) fetchEntryVM(ctx context.Context, uid uuid.UUID, id uuid.UUID) (fragments.ExternalChargeEntryVM, bool) {
	refs, vehicles, ok := h.ownedVehicles(ctx, uid)
	if !ok {
		return fragments.ExternalChargeEntryVM{}, false
	}
	entries, err := h.chargingReader.ListEntriesByVehicles(ctx, vehicleref.TeslaIDs(refs), 0)
	if err != nil {
		return fragments.ExternalChargeEntryVM{}, false
	}
	for _, e := range entries {
		if e.ID == id {
			return externalChargeEntryVMFromEntry(e, vehicles), true
		}
	}
	return fragments.ExternalChargeEntryVM{}, false
}
```

`fetchEntryTeslaIDAndChargedOn` (today at line 835-862) — identical shape, its own return
type unchanged:

```go
func (h *Handler) fetchEntryTeslaIDAndChargedOn(ctx context.Context, uid uuid.UUID, id uuid.UUID) (teslaID int64, chargedOn time.Time, ok bool) {
	refs, _, ok := h.ownedVehicles(ctx, uid)
	if !ok {
		return 0, time.Time{}, false
	}
	entries, err := h.chargingReader.ListEntriesByVehicles(ctx, vehicleref.TeslaIDs(refs), 0)
	if err != nil {
		return 0, time.Time{}, false
	}
	for _, e := range entries {
		if e.ID == id {
			return e.TeslaID, e.ChargedOn, true
		}
	}
	return 0, time.Time{}, false
}
```

`fetchEntryTeslaIDAndChargedOn` discards `ownedVehicles`'s `vehicles` return with `_` — it
never needed the vehicle list, only the ids, which is why `teslaIDsOf` existed as a
standalone free function in the first place rather than being inlined into `fetchEntryVM`
alone.

Both functions' doc comments keep their existing "error or empty list ⇒ false, never an
unfiltered read" sentence — that rule does not move or change, only which function
enforces it (`ownedVehicles` now, instead of each caller's own
`acct.RegisteredVehicles`/`teslaIDsOf` pair).

`external_charges.go` needs the `vehicleref` import added (it is not imported there
today — checked: `grep -n '"github.com/cristianpena/magus-tesla-api/internal/vehicleref"'
internal/gateway/handlers/external_charges.go` returns nothing before this change).

Nothing else in either file changes. In particular: `resolveSelectedVehicle`,
`authorizeVehicle`, the three `RegisteredVehicles` call sites named in D3, and every write
handler (`ExternalChargeCreate`, `ExternalChargeRowUpdate`, `ExternalChargeRowDelete`) are
untouched — the write handlers already call `authorizeVehicle` directly (tier 2), not
through `fetchEntryVM`/`fetchEntryTeslaIDAndChargedOn`'s new plumbing.

---

## Test contract — authored up front, per the project's testing rule

`ownedVehicles` is a genuinely new function with no existing coverage to inherit, so it
gets direct tests — the one exception to this tier's "no new tests" default, mirroring why
`authorizeVehicle` itself has `authorize_vehicle_test.go`. Every existing test that
exercises `fetchEntryVM`/`fetchEntryTeslaIDAndChargedOn` indirectly (through the read/write
handler HTTP tests) needs NO change, because neither function's behaviour moves — see D5.

### `ownedVehicles` — three cases, pinned as expected values

Using the same `fakeAccount{registered []account.Vehicle, regErr error}` double
`authorize_vehicle_test.go` already defines in `handlers_test.go` (no new fake needed):

| # | Given | When | refs | vehicles | ok |
|---|---|---|---|---|---|
| 1 | `fakeAccount{registered: []account.Vehicle{{TeslaID: 111}, {TeslaID: 222}}}` | `h.ownedVehicles(ctx, someUID)` | `[]vehicleref.Ref` of length 2, `refs[0].TeslaID() == 111`, `refs[1].TeslaID() == 222` (same order as `registered`) | the same two `account.Vehicle` values, same order | `true` |
| 2 | `fakeAccount{registered: nil}` (no error, empty list) | `h.ownedVehicles(ctx, someUID)` | `nil` (or a zero-length slice — the test asserts `len(refs) == 0`, not `refs == nil`, since a `make([]T, 0, 0)` and a nil slice both satisfy that) | `nil` / zero-length, same rule | `false` |
| 3 | `fakeAccount{regErr: errors.New("db unavailable")}` | `h.ownedVehicles(ctx, someUID)` | zero-length | zero-length | `false` |

Case 2 and case 3 are the two the leader's dispatch specifically asked this file to pin:
an account with no registered vehicles, and an account whose `RegisteredVehicles` call
errors. Both produce the same externally-visible result (`ok == false`, empty refs, empty
vehicles) — mirroring `authorizeVehicle`'s own "a lookup failure and a real miss must look
the same to the caller" rule (`errVehicleNotAuthorized`), so a transient read failure
cannot be distinguished from "this account really has no vehicles."

Suggested test names, in a new `owned_vehicles_test.go` next to
`authorize_vehicle_test.go` (or appended to that same file — either is acceptable, the
implementer's choice, since both are small and already share the `fakeAccount` double):

- `TestOwnedVehicles_ReturnsRefsAndVehicles` (case 1)
- `TestOwnedVehicles_NoRegisteredVehicles` (case 2)
- `TestOwnedVehicles_RegisteredVehiclesError` (case 3)

### `fetchEntryVM` / `fetchEntryTeslaIDAndChargedOn` — no new test, existing tests unchanged

Every existing test reaching these two functions goes through an HTTP handler
(`ExternalChargeRowStatic`, `ExternalChargeRowEditFragment`, `ExternalChargeRowUpdate`,
`ExternalChargeRowDelete`) with a `fakeAccount` and `fakeChargeReader` already wired by
`newHandlerForExternalCharges`. Because `ownedVehicles` reproduces the exact same
"error/empty ⇒ false" rule those tests already exercise through the old
`teslaIDsOf`-based path, and calls the exact same `RegisteredVehicles`/
`ListEntriesByVehicles` sequence, no existing assertion changes. The implementer's job is
to run `go build ./internal/gateway/...` and `go vet ./internal/gateway/...` and confirm
they compile clean with zero test edits beyond the three new ones above — a test needing
an edit here would itself be a signal that a supposedly behaviour-neutral change actually
changed behaviour, and that is a STOP, not something to patch quietly.

---

## Database

**None.** No table, column, index, constraint, view, or migration is added, dropped, or
altered by this change. `ownedVehicles` and its two callers read through
`account.Service.RegisteredVehicles` and `charging.Reader.ListEntriesByVehicles` — two
ports that already exist, unchanged, and were already called at these call sites before
this change. This satisfies `openspec/config.yaml`'s design-gate requirement by stating it
explicitly: there is no schema and no index plan to justify, because none of this tier's
work touches the database layer at all.

---

## Docs this change invalidates

| File | What is wrong after this change |
|---|---|
| `internal/gateway/AGENTS.md` | "Vehicle ownership — proved once, at the gateway" section names only `authorizeVehicle`. Add `ownedVehicles` alongside it: same section, same "next to `resolveSelectedVehicle`" framing, stating it is the seam `fetchEntryVM`/`fetchEntryTeslaIDAndChargedOn` now use instead of the deleted `teslaIDsOf`. |
| `kkpa/context/architecture/charge-record-mutation.md` | The "100-row lookup cap" bullet quotes `ListEntriesByVehicles(ctx, teslaIDsOf(vehicles), 0)` literally. Update to `ListEntriesByVehicles(ctx, vehicleref.TeslaIDs(refs), 0)`, and note `vehicles`/`refs` both come from one `h.ownedVehicles(ctx, uid)` call rather than a direct `acct.RegisteredVehicles` + `teslaIDsOf` pair. The rest of that bullet (the 100-row cap, the no-`GetEntry`-port gap) is unaffected — this tier does not touch either. |
| `kkpa/context/use-case/charging/delete-manual-charge.md` | Same literal quote, in the "100-row lookup cap is worst here" gotcha. Same edit. |
| `kkpa/context/use-case/charging/update-manual-charge.md` | Flow step 5 says the lookup reads "over the account's `RegisteredVehicles`" — still true (the underlying call is unchanged), but the step now goes through `ownedVehicles` rather than a direct call. Worth a one-clause addition naming the seam; not a factual correction, since the underlying source did not change. |
| `openspec/specs/manual-charge-log/spec.md` | **Unaffected — checked.** No requirement's text describes which function performs the scoping, only that the gateway performs it before any domain-module call. That remains true. |
| `openspec/specs/vehicleref/spec.md` | **One requirement added** — `specs/vehicleref/spec.md`'s delta, "A Fleet-Wide Read Uses The Bulk Ownership Proof": before this tier the two lookup helpers assembled their vehicle-id list beside the call (`teslaIDsOf`), not from the bulk ownership proof (`vehicleref.All` + `vehicleref.TeslaIDs`); after this tier they do. See `proposal.md` §"Is a spec delta needed?" for why this is behavioural, not implementation detail, and why the requirement is complete (checked every `ListEntriesByVehicles` call site in the gateway). |

`openspec/changes/archive/` is not swept — nothing there describes `ownedVehicles` (it did
not exist when those changes were archived), so there is nothing stale to find there.

---

## Makefile, guards and codegen — checked, with findings

| Thing checked | Finding |
|---|---|
| `make vehicleref-guard` | **Checked — will pass.** The guard (`Makefile:741-763`) greps for `vehicleref\.(Authorize\|All)\(` and excludes hits in `internal/vehicleref/`, any `_test.go` file, and `internal/gateway/handlers/handlers.go`. This change's only new `vehicleref.All(` call site is inside `handlers.go` (`ownedVehicles`), which is on the exclusion list by name. `external_charges.go` gains a `vehicleref.TeslaIDs(` call, which the guard's pattern does not match at all — `TeslaIDs` is not `Authorize` or `All`. No new call trips the guard. |
| `make boundary-guard` | Unaffected — no `internal/telemetry` import touched, this change stays entirely inside `internal/gateway`. |
| `make tz-guard`, `make money-guard`, `make i18n-guard`, `make ui-guard` | Unaffected — no time-zone call, no monetary column, no user-facing string, no `.templ` file. |
| `make migration-guard` | **Not applicable.** This change adds no migration. |
| `sqlc` inputs | **Not applicable.** No query, no `sqlc.yaml` entry touched. |
| `make db-setup` / `db-setup-test` / `db-reset` | Unaffected — no schema change. |
| `make archive-guard` | Nothing under `openspec/changes/archive/` is edited by this change. |
| `gofmt -l` | Expected clean after implementation — two functions moved/added following the existing file's formatting; no new formatting pattern introduced. |

---

## Risks

- **A future third caller re-introduces a `teslaIDsOf`-shaped helper instead of using
  `ownedVehicles`.** Mitigated by naming and placement: `ownedVehicles` sits next to
  `authorizeVehicle`, in the file every gateway worker's doc-pack already points at for
  "how does this module prove vehicle ownership" (`ai/architecture.md` §5's own reference
  to `handlers.go`'s `authorizeVehicle`). No enforcement beyond that — this is a
  discoverability bet, not a guard, and it is the same bet `authorizeVehicle` itself
  already relies on.
- **A worker folds the three `RegisteredVehicles` call sites named in D3 into
  `ownedVehicles` "for consistency."** Explicitly a non-goal (`proposal.md`, D3). Doing so
  would change three handlers' behaviour on an account-lookup error, which is out of scope
  and not something this tier's own test contract covers.
- **Low overall risk.** No port signature changes, no database object changes, no new
  external caller. The blast radius is two functions in one file plus one new function in
  a sibling file, all within `internal/gateway`.
