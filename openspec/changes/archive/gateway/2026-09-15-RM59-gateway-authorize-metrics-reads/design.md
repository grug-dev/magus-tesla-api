# Design — RM59-gateway-authorize-metrics-reads

This change touches **no database object** — no migration, no index, no query.
The design gate in `openspec/config.yaml` applies only to DB-touching changes;
this note exists so the reviewer does not look for an index plan.

## Overview

Tier 1 already re-keyed `analytics.vehicle_metrics` and rewrote every gateway
production call site it needed to keep `go build ./...` green. What is left is
narrower than the roadmap's tier-2 text implies: the production ports and their
callers already match the new shape (verified against the working tree, not
assumed). Three things remain:

1. Three test fakes in `internal/gateway/handlers` still implement the *old*
   port shapes, so `go vet ./...` fails on `_test.go` files only.
2. One production function (`buildExternalChargesPage`) reads the account's
   vehicles twice per render — a real duplicate-read cost tier 1's own review
   found (roadmap F2) but could not fix inside its own sandbox.
3. Five stale mentions of the retired port name in `openspec/specs/gateway/spec.md`.

Two decisions were settled with the owner before this change was written and
are recorded here, not reopened: D1 (RD7 — leave `handlers.go` alone) and D2
(RD6 — the F2 fix).

## D1 — The three `handlers.go` call sites keep `vehicleref.All`, not `ownedVehicles` (RD7)

**Decision:** `vehiclesFor`, `dashboardFor`, and `navHeaderFor` — all three
already calling `h.analyticsReader.LatestMetricsForVehicles(ctx,
vehicleref.All(teslaIDsOf(registered)))` — stay exactly as tier 1 wrote them.
This change does not route them through `ownedVehicles`.

**Rationale:** the roadmap's own tier-2 scope cell suggested using
`ownedVehicles` at these three sites. It is wrong for all three, for the same
reason: each handler already holds its own `registered []account.Vehicle`
slice, read via `h.acct.RegisteredVehicles(ctx, uid)` moments earlier, for a
reason that has nothing to do with this port — building the vehicle card list
(`vehiclesFor`), picking the primary vehicle (`dashboardFor`), or the same
(`navHeaderFor`). Calling `ownedVehicles` at these three sites would add a
**second** account read per render, in the exact hot paths (dashboard, vehicle
cards, nav header) this project's read-heavy Performance-Profile calls out by
name. `vehicleref.All(...)` on the already-resolved slice is legal here
because `make vehicleref-guard` exempts the whole file `handlers.go`, not only
`authorizeVehicle` — confirmed by reading the guard's own grep target in the
Makefile, not assumed. Reusing `ownedVehicles` at these three call sites would
be the "simplify the call, forget the read it hides" mistake the guard's
file-level exemption is precisely there to avoid.

**Not reopened:** this is a deliberate deviation from the roadmap's tier-2
text, settled with the owner before this change was drafted. A future worker
reading only the roadmap file should not "fix" `handlers.go` to match it.

## D2 — `buildExternalChargesPage` collapses to one read (RD6)

**Decision:** `buildExternalChargesPage` calls `h.ownedVehicles(ctx, uid)`
once, at the top of the function (where it currently calls
`h.acct.RegisteredVehicles(ctx, uid)`), and reuses the `refs` it returns for
the battery-suggestion block later in the same function — instead of that
block calling `h.ownedVehicles(ctx, uid)` a second time.

**Current shape** (`external_charges.go`, two separate reads of the same
data):

```go
vehicles, err := h.acct.RegisteredVehicles(ctx, uid)
if err != nil {
    log.Printf("gateway: RegisteredVehicles error for account %s: %v", uid, err)
    return fragments.ExternalChargesPageData{
        CSRFToken: csrfToken,
        Error:     i18n.T(ctx, i18n.KeyChargesErrorCouldNotLoadVehicles),
    }
}
// ... entries, tiles, vms built from vehicles ...
suggestion := ""
if refs, _, ok := h.ownedVehicles(ctx, uid); teslaIDFilter != 0 && h.analyticsReader != nil && ok {
    statuses, snapErr := h.analyticsReader.LatestMetricsForVehicles(ctx, refs)
    // ...
}
```

**New shape** (one read, `refs` carried down to the suggestion block):

```go
refs, vehicles, ok := h.ownedVehicles(ctx, uid)
if !ok {
    log.Printf("gateway: ownedVehicles failed for account %s", uid)
    return fragments.ExternalChargesPageData{
        CSRFToken: csrfToken,
        Error:     i18n.T(ctx, i18n.KeyChargesErrorCouldNotLoadVehicles),
    }
}
// ... entries, tiles, vms built from vehicles, unchanged ...
suggestion := ""
if teslaIDFilter != 0 && h.analyticsReader != nil {
    statuses, snapErr := h.analyticsReader.LatestMetricsForVehicles(ctx, refs)
    // ...
}
```

**Why this file specifically must go through `ownedVehicles` and not a direct
`vehicleref.All` call:** `make vehicleref-guard` exempts only `handlers.go` —
not `external_charges.go`. Reusing `h.ownedVehicles`, already imported and
called elsewhere in this same file, keeps the guard clean without adding a new
exemption.

**Behaviour change, accepted:** `ownedVehicles`'s `ok` is `false` on either a
lookup error **or** an empty vehicle list (its own doc comment: "an empty
vehicle list is never 'no filter'"). The old code only checked `err != nil` —
an account with zero registered vehicles and no error would have fallen
through with an empty `vehicles` slice and kept rendering. The new code
returns the "could not load vehicles" error page in that case instead.

This is accepted because the case is **unreachable in practice**. This
function's own early return, a few lines above the point this diff touches,
already exits before any read when `teslaIDFilter == 0`:

```go
if teslaIDFilter == 0 {
    return fragments.ExternalChargesPageData{ /* empty state, no reads */ }
}
```

`teslaIDFilter` is only ever non-zero when a vehicle was already resolved from
the account's own registered-vehicle list, by the caller, before this function
runs. An account with zero vehicles can never produce a non-zero
`teslaIDFilter`, so the branch that now treats an empty list as `ok == false`
can never be reached by an account that has no vehicles — it can only be
reached by a genuine lookup error, which is exactly the case the old code
already handled the same way. Confirmed against the test suite:
`TestExternalChargePage_E1_NoRegisteredVehicles_NoFilterChrome`, the existing
test that seeds `fakeAccount{registered: nil}` and exercises the
zero-vehicles path, hits the `teslaIDFilter == 0` early return before this
change's edit is ever reached — it does not change behaviour under this
change.

## D3 — `fakeAnalyticsReader` test contract (`history_test.go`)

Unit tests are excluded (no new test, no new scenario). This is the contract
the **existing** fake and its callers must satisfy once updated to compile
against tier 1's port shapes — authored here, before the edit, per this
project's testing convention.

**Struct fields removed** (never read by any assertion — confirmed by grep):
`gotAccount`, `gotOdoAccount`, `gotBattAccount`. Every other field
(`days`, `err`, `snaps`, `distances`, `odometerErr`, `battery`, `batteryErr`,
`gotTeslaID`, `gotStart`, `gotEnd`, `consumedByDayCalled`, `gotOdoTeslaID`,
`gotOdoStart`, `gotOdoEnd`, `odometerByDayCalled`, `gotBattTeslaID`,
`gotBattStart`, `gotBattEnd`, `batteryByDayCalled`, `statuses`,
`statusesErr`) is unchanged.

**Method signatures, before → after:**

| Method | Before | After |
|---|---|---|
| `ConsumedByDay` | `(_ context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]analytics.DayConsumption, error)` | `(_ context.Context, teslaID int64, start, end time.Time) ([]analytics.DayConsumption, error)` |
| `OdometerDeltaByDay` | `(_ context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]analytics.DayDistance, error)` | `(_ context.Context, teslaID int64, start, end time.Time) ([]analytics.DayDistance, error)` |
| `BatteryLevelByDay` | `(_ context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]analytics.DayBattery, error)` | `(_ context.Context, teslaID int64, start, end time.Time) ([]analytics.DayBattery, error)` |
| `LatestMetricsByAccount` | `(context.Context, uuid.UUID) ([]analytics.VehicleStatus, error)` | renamed `LatestMetricsForVehicles(context.Context, []vehicleref.Ref) ([]analytics.VehicleStatus, error)` |

Each method body drops the `f.gotXxxAccount = accountID` recording line (the
field it wrote to is deleted) and is otherwise byte-identical: same recorded
fields, same returned fixture values. `LatestMetricsForVehicles`'s body stays
`return f.statuses, f.statusesErr` — it ignores its `refs` argument today,
exactly as `LatestMetricsByAccount` ignored its `uuid.UUID` argument, because
no existing test asserts on the argument this method receives (confirmed: no
test reads a "got latest ids/account" field — none exists).

**Import added:** `history_test.go` gains
`"github.com/cristianpena/magus-tesla-api/internal/vehicleref"` — needed for
the new method's parameter type. `external_charges_test.go` and
`handlers_test.go` already import it (both already build `vehicleref.Ref`
values for other tests in this package), so no import change is needed there.

**Call-site fixups (same file), each drops its `accountID`/`uid` argument
in place:** every `analyticsReader.ConsumedByDay(...)`,
`.OdometerDeltaByDay(...)`, `.BatteryLevelByDay(...)` call inside
`buildHistoryView` (production code, already fixed by tier 1 — verified, not
re-touched here) is unaffected; this file only touches the **fake's**
signatures and the fake's own internal recording. No test in this file calls
these methods directly (they only construct `*fakeAnalyticsReader{}` and pass
it into `Deps`), so no test body needs an argument-list edit here beyond the
doc-comment rename described in D5.

## D4 — `fakeRecalculator` test contract (`external_charges_test.go`)

**Struct fields removed:** `recalculateCall.accountID` — the only assertion
reading it is deleted (below), leaving nothing else that reads this field.

**Method signature, before → after:**

| Method | Before | After |
|---|---|---|
| `Recalculate` | `(_ context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) error` | `(_ context.Context, teslaID int64, start, end time.Time) error` |

Body: drops `accountID: accountID` from the `recalculateCall{...}` literal it
appends to `f.calls`; `teslaID`, `start`, `end` recording is unchanged.

**One assertion removed, one kept — the exact edit, in**
`TestExternalChargeCreate_RecalculatesAfterSuccessfulWrite`:

```go
// REMOVED — accountID no longer exists as a Recalculate argument:
got := recalc.calls[0]
if got.accountID != uid {
    t.Errorf("want Recalculate scoped to account %s, got %s", uid, got.accountID)
}
// KEPT, unchanged — already covers "scoped to the right vehicle":
if got.teslaID != 1001 {
    t.Errorf("want Recalculate for the session-selected vehicle 1001, got %d", got.teslaID)
}
```

The test's purpose — proving a successful write triggers exactly one
`Recalculate` call, scoped to the right vehicle and the right day — is fully
preserved by the `teslaID` and `start`/`end` assertions that remain; nothing
this test was checking becomes unchecked. `uid` stays in scope and in use
(`postExternalCharge(t, h, uid)` still needs it to build the session), so no
unused-variable cleanup is needed beyond the one deleted block.

No other test in this file reads `recalculateCall.accountID` (confirmed by
grep) — `TestExternalChargeCreate_NoRecalculateWhenWriteFails` and
`TestExternalChargeCreate_RecalculateErrorDoesNotFailTheRequest` both only
check `len(recalc.calls)`, unaffected by this field's removal.

## D5 — Stale comments, mechanical rename only

Every comment across the three files naming the retired
`LatestMetricsByAccount` is updated to `LatestMetricsForVehicles`. No other
wording changes. Locations, exactly as found:

- `history_test.go`: doc comment at the struct's `statuses`/`statusesErr`
  field group, the `LatestMetricsByAccount` method's own doc comment (renamed
  along with the method itself, D3), and one reference near line 1885.
- `handlers_test.go`: three comment-only references (no code change at these
  lines) near lines 217, 233, 1156.
- `external_charges_test.go`: two comment-only references near lines 1490 and
  1539.

These are text-only edits inside existing comments — no code they describe
changes as a result of this decision.

## Verification

- `go build ./...` — already green (tier 1 left it that way); confirms this
  change does not regress it.
- `go vet ./internal/gateway/...` — the bar this change must clear. Red today
  (one reported error, `external_charges_test.go:50`); green once D3/D4 land.
- `make vehicleref-guard` — checked. D2 adds no new `vehicleref.All` or
  `vehicleref.Authorize` call site outside the guard's existing allow-list
  (`internal/vehicleref`, `_test.go` files, `handlers.go`'s
  `authorizeVehicle`); it reuses `h.ownedVehicles`, itself already
  guard-compliant. D1 makes no change at all.
- `make boundary-guard`, `make i18n-guard`, `make tz-guard`,
  `make migration-guard`, `make archive-guard` — unaffected. No
  `internal/telemetry` import, no new user-facing string, no time handling, no
  migration, no archive file touched.
- `gofmt -l` — run after every edit.

## `make` targets and guards — checked, per `CLAUDE.md`'s reverse-direction rule

This change alters no database schema, no module layout, and no codegen
input — it edits Go test doubles, one production function's read pattern, and
a spec's prose. No `make` target or guard assumption is affected beyond the
ones already listed under Verification above.

## Docs

- `openspec/specs/gateway/spec.md`: five mentions of `LatestMetricsByAccount`,
  across four requirements (`Dashboard renders enriched vehicle cards from
  stored telemetry`, `Create Charge Entry`, `Navigation Vehicle Header`,
  `Gateway Imports No chargingdb Package`), renamed to
  `LatestMetricsForVehicles`. See this change's own `specs/gateway/spec.md`
  delta.
- `internal/gateway/AGENTS.md`: already reflects `LatestMetricsForVehicles`
  throughout (tier 1's own leader-owned integration updated it) — checked by
  grep, zero hits for the old name.
- `kkpa/context/entities/vehicle-metrics/guide.md`: already reflects
  `LatestMetricsForVehicles` throughout (tier 1's own "Docs" section updated
  it) — checked by grep, zero hits for the old name.
- A repo-wide grep for `LatestMetricsByAccount` outside this change's own
  files and the archive turns up three more hits, all out of scope, checked
  individually:
  - `openspec/specs/analytics/spec.md:862` — inside the `analytics` capability's
    own "schema move" scenario, listing every port name as a historical
    snapshot of what a 2026-09-02 migration preserved (the same precedent tier
    1's design.md recorded for that requirement — a true claim about a past
    migration, not a standing claim about the port's current name). Not this
    tier's file to edit, and not this tier's module.
  - `openspec/roadmaps/RM59-rekey-vehicle-metrics-on-tesla-id.md` — the
    roadmap file itself, describing the port's pre-tier-1 name as part of its
    own "what changes" narrative. Roadmap files are not specs and are not
    swept by this rule.
  - `kkpa/context/pending-spec-to-sync/applied/*.md` — already-applied
    historical proposal snapshots, the KB's own equivalent of an archived
    change. Not a live guide `kkpa-context-fetch` presents as current.
