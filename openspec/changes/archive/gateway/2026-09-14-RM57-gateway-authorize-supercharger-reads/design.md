# Design — RM57-gateway-authorize-supercharger-reads

Source ticket: MAG-67. Roadmap tier 4.

**This change touches no database object** — no table, column, index, constraint, or
migration. Stated explicitly per `openspec/config.yaml`'s design rule rather than left
silent. It exists as its own `design.md` anyway because it is the change that makes
`go build ./...` green again after tier 3 deliberately left it red, and because it
touches the gateway's one closed list of write apertures — a decision surface this
project always writes down (`internal/gateway/AGENTS.md` §"The write apertures").

## Overview

`charging.SessionVerifier.VerifySession` requires a `vehicleref.Ref` since tier 3. Its
one caller, `SuperchargerRowUpdate`, still passed a bare `int64`
(`selected.TeslaID`, from `resolveSelectedVehicle`) and does not compile. This change
makes the handler do what the type now requires: read the vehicle the user's session
already has selected, prove that vehicle belongs to the signed-in account, and hand the
proof — not the raw id — to the port.

## Decisions

### D1 — The write reads `currentVehicle(c)`, not `resolveSelectedVehicle`

`resolveSelectedVehicle` does two things in one call: it validates a session selection
against `RegisteredVehicles`, and if there is none, it **auto-selects** the first OWNER
vehicle and writes that choice back into the session. That auto-select behavior is
correct for a page render — a user who has never picked a vehicle should still see
one — but it is wrong for a write. `SuperchargerRowUpdate` only ever runs after a page
render already put a vehicle in the session (the row's own action URL comes from that
render), so a request with **no** vehicle in session is not a first-time visitor — it is
a stale session (the user cleared cookies mid-edit) or a hand-crafted `PATCH` that skips
the page entirely. Silently auto-selecting a vehicle for that request would let a
malformed request succeed against whichever vehicle the auto-select policy happens to
land on, instead of failing the way a request with no valid context should.

`currentVehicle(c)` (`handlers.go`) reads the session's two vehicle keys with no
fallback and no side effect — exactly the "no auto-select" semantics this write needs.
A miss renders `HTTP 404` before any port call, using the identical status and message
`SuperchargerRowUpdate` already used for its old "no resolvable vehicle" case
(`i18n.KeySuperchargerErrorSessionNotFound`) — this is not a new failure mode, only a
new way to reach the existing one.

### D2 — The two read sites keep `resolveSelectedVehicle`; no `authorizeVehicle` call is added there

`superchargerStatsViewFor` (page/fragment render) and `fetchSuperchargerRowVM` (the
list-and-match row resolve shared by the GET routes and the write path's own
re-resolve-on-error branch) both call `resolveSelectedVehicle`, and this change leaves
both untouched.

`resolveSelectedVehicle` can only ever return a vehicle `account.RegisteredVehicles`
listed for this account — either a session selection it re-validated against that same
list, or its own auto-selected pick from that list. Either way, the returned
`account.Vehicle` was already filtered to "this account's own vehicles" before the
function returns it. Calling `authorizeVehicle` afterward would run a **second**
`RegisteredVehicles` query only to re-confirm a fact the first query already
established — no new guarantee, one more query per render, on the read path the
platform's Performance-Profile protects. The read ports these two functions feed
(`ListSessionsByVehicleBetween`) also still take a bare `teslaID int64` — tier 3's own
D2 fixed that as deliberate, since `internal/analytics` calls the same three read ports
and cannot construct a `Ref` (`make vehicleref-guard` allows `vehicleref.Authorize`/
`.All` only inside `authorizeVehicle`). Retyping these two call sites would have nothing
downstream to pass a `Ref` to.

Both call sites now carry a one-line comment stating this reasoning, so a future agent
does not "fix" them into calling `authorizeVehicle` a second time.

### D3 — Failure mode: 404, never 403, matching `authorizeVehicle`'s own contract

`authorizeVehicle` (`handlers.go`, MAG-66) already returns one error,
`errVehicleNotAuthorized`, for both "the account lookup failed" and "the vehicle is not
owned" — deliberately indistinguishable, so a lookup failure cannot be used to infer
whether a probed vehicle id is real. `SuperchargerRowUpdate` renders that error the same
way it already rendered a missing session selection: `HTTP 404` with
`i18n.KeySuperchargerErrorSessionNotFound`. This change adds no new status code and no
new error message — both failure branches (`!selOK` from `currentVehicle`, `err != nil`
from `authorizeVehicle`) resolve to the exact same visible response.

### D4 — Query cost: one `RegisteredVehicles` read, not two, and not more than before

Before this change, `SuperchargerRowUpdate` called `resolveSelectedVehicle`, which
issues one `RegisteredVehicles` query (or zero, if the session already held a valid
selection — `resolveSelectedVehicle` still re-validates against a fresh query in that
branch too, so it is one query in every branch that reaches the port call). After this
change, the handler calls `currentVehicle(c)` (no query — a session read) then
`authorizeVehicle` (one `RegisteredVehicles` query). Net: still exactly one account
query on the path that reaches `VerifySession`. This is not a new cost — it is the same
cost moved onto a helper that produces the type the port now requires.

### D5 — Doc-comment cleanup on `SuperchargerRowUpdate`

The function's existing doc comment cited `design.md` decision IDs (D3, D6, D7, D8, D9)
from an earlier, now-archived change. Per this project's code-comment rule, a comment
must state the reason itself, not point at a decision id in a doc that will later be
frozen. Since this change edits this exact function, its doc comment is rewritten to
state the reasons directly (strict body, range validation before the port call, CSRF
key, the new ownership check, and the writer-error re-resolve) with no id references.
No other function's comment in this file is touched.

## Test Contract

Authored against the exact code shape given in this change's own dispatch, before any
test file was edited — the dispatch fixed the handler's new shape in full, so this
section transcribes what that shape implies for every existing test, rather than
deriving it after the fact from a running test suite (`Test-Execution-Policy` — this
project's owner runs the suite, not this change).

`fakeSessionVerifier.VerifySession` changes its second parameter from `teslaID int64`
to `ref vehicleref.Ref`, unwrapping with `ref.TeslaID()` into the existing
`verifySessionCall.teslaID` field — every assertion reading `.calls[i].teslaID` is
unchanged, because the fake still stores the identical `int64`.

`superchargerRowEngine` gains two parameters, `selTeslaID int64, selVIN string`,
seeding the session's `sessionTeslaIDKey`/`sessionVINKey` exactly like the already-
existing `superchargerEngine` (the GET-only engine) does — mirroring an established
pattern in this same file, not inventing a new one. Passing `0, ""` means "seed
nothing," matching every call site whose outcome does not depend on a resolved vehicle.

| Test | Outcome before this change | Session vehicle now seeded | Outcome after this change | Why |
|---|---|---|---|---|
| `TestSuperchargerRowUpdate_AbsentKeyIs400` | 400, 0 `VerifySession` calls | `42, "VIN42"` | Unchanged | Body-key check runs before the vehicle is ever read |
| `TestSuperchargerRowUpdate_BothEmptyClearsBothPercentages` | 200, 1 call, `(nil, nil)` | `42, "VIN42"` | Unchanged | Vehicle must resolve for the write to reach `VerifySession` at all |
| `TestSuperchargerRowUpdate_DecreasingOrderAccepted` | 200, 1 call, `(80, 20)` | `42, "VIN42"` | Unchanged | Same as above |
| `TestSuperchargerRowUpdate_RecalculateWindowFromChargeStopDateTime` | 200, 1 `Recalculate` call, specific window | `42, "VIN42"` | Unchanged | Same as above |
| `TestSuperchargerRowUpdate_OutOfRangeFieldErrorIs422` | 422, 0 `VerifySession` calls | `42, "VIN42"` | Unchanged | Range validation runs before the vehicle is read |
| `TestSuperchargerRowUpdate_NoResolvableVehicleIs404` | 404, 0 calls | `0, ""` (no vehicle registered at all) | Unchanged | `currentVehicle` has nothing to read; same 404 this test always asserted |
| `TestSuperchargerRowUpdate_VerifyUsesResolvedVehicle` | 200, 1 call, `teslaID == 22` | `22, "VIN22"` (the OWNER vehicle) | Unchanged assertion; setup now states the session directly instead of relying on auto-select | The write no longer auto-selects — this test now proves the handler reads and authorizes the SESSION's vehicle, not `vehicles[0]` |
| `TestSuperchargerRowUpdate_NoTokenEverIssuedIs403` | 403, 0 calls | `42, "VIN42"` | Unchanged | CSRF check runs before the vehicle is read |
| `TestSuperchargerRowUpdate_StaleTokenRejected` | 403, 0 calls | `42, "VIN42"` | Unchanged | Same as above |
| `TestSuperchargerRowEditFragment_NotInWindowIs404` | 404 | `42, "VIN42"` | Unchanged | This is a `GET` route; it still uses `resolveSelectedVehicle`, which auto-selects regardless of session state |

**No test's expected status, expected call count, or expected argument value changes.**
The only tests whose *setup* changes are the seven that reach the write path and expect
it to succeed or record a specific `teslaID` — each needed the session vehicle stated
explicitly because the write no longer auto-selects one. `TestSuperchargerRowUpdate_
VerifyUsesResolvedVehicle`'s doc comment is rewritten (still true, no longer accurate as
worded) to describe what it now proves: the handler passes the session-selected vehicle,
not the account's first vehicle — the same guard against a wrong-vehicle regression,
reached a different way.

## Risks / Trade-offs

- **[Risk, mitigated]** A stale session vehicle (the user removed a car from the account
  in another tab) could make `authorizeVehicle` fail where `resolveSelectedVehicle`
  would have silently re-selected a valid one and let the write proceed. →
  **Accepted, and the mitigation is D1's own point**: proceeding on a stale selection
  is exactly the silent-success case D1 argues against. A user in this state gets 404
  and must reload the page, which re-resolves a valid vehicle into the session before
  the next write is possible — a small, honest failure instead of a write against a
  vehicle the user's own browser tab no longer shows.
- **[Not a risk, a scope note]** `internal/analytics`'s three `ListSessionsByVehicle*`
  read ports still take a bare `int64` — unchanged by this change, per tier 3's own D2
  and the roadmap's "Facts already checked." A future agent adding
  `internal/vehicleref` usage to `internal/analytics` would need a new seam;
  `make vehicleref-guard` already blocks it today.

## Reverse-direction check (existing `make` targets and guards)

Per `CLAUDE.md`'s "workflow & architectural decisions" rule, checked explicitly:

- **`make vehicleref-guard`** — passes. `authorizeVehicle` is still the only
  non-test, non-package-internal caller of `vehicleref.Authorize`; this change adds a
  second call SITE to that same function (`SuperchargerRowUpdate`), not a second
  construction site.
- **`make boundary-guard`** — passes, unaffected. This change touches neither
  `internal/telemetry` nor its import in `internal/gateway`.
- **`make i18n-guard`** — passes. No new user-facing string is added; the write path's
  new failure branch reuses the existing `i18n.KeySuperchargerErrorSessionNotFound`
  key.
- **`make archive-guard`** — passes. No archived file is touched.
- **`sqlc` / `MIGRATIONS_DIRS`** — unaffected. No query, no migration.
- **Finding**: no `Makefile` target or guard needs a change for this work.

## Rollback

Revert `supercharger.go`'s write-path block back to calling `resolveSelectedVehicle` and
passing `selected.TeslaID` — this immediately breaks the build again, back to tier 3's
documented state, so this is only a rollback in the sense of "undo this change," not a
safe standalone state. Revert the two read-site comments, the test file's engine
signature and its ten call sites, the doc-comment rewrite, and the doc edits listed
under §Docs. No migration, no data written or read differently — a clean, single-commit
revert.

## Docs

- **`internal/gateway/AGENTS.md`**: the write-apertures table's `SuperchargerRowUpdate`
  row (guard order now includes `authorizeVehicle`); the "Three of them diverge"
  paragraph becomes "Two," with a new paragraph noting the CSRF-before-ownership guard
  order; the "Vehicle ownership — proved once, at the gateway" section's "No handler
  calls it yet" bullet, naming `SuperchargerRowUpdate` as the first caller.
- **`kkpa/context/architecture/gateway-reader-writer-ports.md`**: the component-map row
  and the whole "Aperture 2" section, which documented the *absence* of an ownership
  check as deliberate — now inaccurate. Rewritten to describe the current mechanism.
- **`kkpa/context/input-port/charging/supercharger-stats.md`**: the "No
  `RegisteredVehicles` ownership check on the write" bullet.
- **`kkpa/context/use-case/charging/verify-session-battery.md`**: step 1 of the Flow
  section, which said "No `RegisteredVehicles` ownership check."
- **`kkpa/context/architecture/charge-record-mutation.md`**: the "Different ownership
  vocabulary" bullet, which described the Supercharger path as relying solely on the SQL
  scope.
- **`kkpa/context/architecture/vehicle-ownership-proof.md`**: the Related KB note, which
  said the seam had rewired no port yet.
- **`kkpa/context/INDEX.md`**: the `tenant ownership check` row, which counted two
  apertures skipping it — now one (the theme switch).
- **`openspec/specs/gateway/spec.md`** (the LIVE spec): **not edited by this change.**
  This change's own delta lives under `specs/gateway/spec.md` in this change folder, per
  the OpenSpec workflow — the live spec is synced from it at archive time, not written
  directly by a worker.

## Implementation Plan

See `tasks.md` for the full breakdown. In outline:

1. `internal/gateway/handlers/supercharger.go` — the write-path block (D1, D3, D4), the
   two read-site comments (D2), the `SuperchargerRowUpdate` doc-comment rewrite (D5).
2. `internal/gateway/handlers/supercharger_test.go` — `fakeSessionVerifier`'s retyped
   method, `superchargerRowEngine`'s two new parameters, ten call-site updates, one
   doc-comment update (Test Contract).
3. `internal/gateway/AGENTS.md` + six `kkpa/context/` files (§Docs).
4. `go build ./...`, `go vet ./internal/gateway/...`, `gofmt -l internal/gateway`,
   `make vehicleref-guard`, `make boundary-guard`, `make i18n-guard`,
   `make archive-guard` — all expected clean, `go build ./...` for the first time since
   tier 3 landed.

## Open Questions

None. D1-D5 resolve every question the dispatch raised, including the two it flagged as
most likely to go wrong: the test double's signature (D-Test Contract) and whether any
existing test's expected outcome should change (it does not — only setup does, and only
where the write path is exercised).
