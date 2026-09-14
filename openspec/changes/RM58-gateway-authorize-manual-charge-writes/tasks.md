# Tasks — RM58-gateway-authorize-manual-charge-writes

All work is inside `internal/gateway` plus two `kkpa/context/` guides. No other module is
touched (`proposal.md` §Modules affected).

**Unit tests: mostly EXCLUDED**, per the ticket. The one exception is T2 — `ownedVehicles`
is a brand-new function with no existing coverage, so it gets its own small direct test,
the same way `authorizeVehicle` already has `authorize_vehicle_test.go`. No other test
file is added, and no existing test's assertions change (`design.md` §Test contract).

## Dependency graph

```
T0 (offline grep, no deps) ─────────────────────────────────┐
                                                              │
T1 (add ownedVehicles, handlers.go) ──► T2 (test ownedVehicles) │
        │                                                     │
        └──► T3 (route lookup helpers through it,             │
                 delete teslaIDsOf, external_charges.go) ──► T4 (build/vet/fmt)
                        │                                     │
                        ├──► T5 (docs: AGENTS.md, module-owned)
                        └──► T6 (docs: KB guides, cross-module-aware)
                                                              │
T7 (final verification) ◄────────────────────────────────────┘
```

Sequencing rules:

- **T1 → T3 is a hard dependency.** `external_charges.go` cannot call a function that does
  not exist yet.
- **T2 depends only on T1**, not on T3 — it tests `ownedVehicles` directly, never through
  `external_charges.go`. May run in parallel with T3.
- **T4 depends on both T2 and T3** — it is the compile/vet/format check across everything
  this change touches.
- **T5 and T6 both depend on T3** (they describe the finished call sites) and are disjoint
  files, so they may run in parallel with each other, after T3.
- **T0 has no dependency** and may start immediately, in parallel with T1.

---

## T0 — Offline check

Depends on: nothing.

- [ ] 0.1 Grep `internal/gateway` for `teslaIDsOf`. Expect exactly 3 hits before this
      change starts: the definition (`external_charges.go:802`) and its two call sites
      (`external_charges.go:823`, `:852`). If the count differs, `design.md` is
      out of date — stop and reconcile before continuing.
- [ ] 0.2 Grep `internal/gateway/handlers/external_charges.go` for
      `"github.com/cristianpena/magus-tesla-api/internal/vehicleref"`. Expect **zero**
      hits — confirms the import needs adding in T3, not already present.
- [ ] 0.3 Grep `internal/gateway/handlers` for `func.*fetchEntryVM\|func.*
      fetchEntryTeslaIDAndChargedOn` in any `_test.go` file, and separately for
      `teslaIDsOf` in any `_test.go` file. Expect **zero** hits both times — confirms no
      test calls either function or the free helper directly, so T3's rewrite needs no
      test-file edit beyond what T2 adds. If either grep finds a hit, that test is a find
      to report, not something to silently work around.

## T1 — Add `ownedVehicles` (`handlers.go`)

Depends on: nothing (uses only imports already present in `handlers.go`).

- [ ] 1.1 Add `ownedVehicles` to `internal/gateway/handlers/handlers.go`, immediately after
      `authorizeVehicle` (today ending around line 1075) and before `ConnectTesla`, exactly
      as `design.md` §Ports and implementation gives it: signature
      `func (h *Handler) ownedVehicles(ctx context.Context, uid uuid.UUID) (refs
      []vehicleref.Ref, vehicles []account.Vehicle, ok bool)`.
- [ ] 1.2 Confirm no new import is needed — `account` and `vehicleref` are both already
      imported in this file (used by `resolveSelectedVehicle` and `authorizeVehicle`
      respectively).
- [ ] 1.3 Do NOT change `authorizeVehicle`, `resolveSelectedVehicle`, or
      `errVehicleNotAuthorized` in this task. They are untouched by this tier
      (`design.md` §D6).
- [ ] 1.4 `go build ./internal/gateway/...` — expect clean (the new function compiles; no
      caller references it yet).

## T2 — Test `ownedVehicles` directly

Depends on: T1 (the function must exist to test).

- [ ] 2.1 Add the three cases from `design.md` §Test contract's table, in a new
      `owned_vehicles_test.go` next to `authorize_vehicle_test.go`, or appended to that
      same file (implementer's choice) — both live in package `handlers` and already share
      the `fakeAccount` double defined in `handlers_test.go`.
- [ ] 2.2 `TestOwnedVehicles_ReturnsRefsAndVehicles` — case 1: `fakeAccount{registered:
      []account.Vehicle{{TeslaID: 111}, {TeslaID: 222}}}`; assert `ok == true`, `len(refs)
      == 2`, `refs[0].TeslaID() == 111`, `refs[1].TeslaID() == 222`, `len(vehicles) == 2`
      and `vehicles` equals the fake's `registered` slice in order.
- [ ] 2.3 `TestOwnedVehicles_NoRegisteredVehicles` — case 2: `fakeAccount{registered:
      nil}`; assert `ok == false`, `len(refs) == 0`, `len(vehicles) == 0`.
- [ ] 2.4 `TestOwnedVehicles_RegisteredVehiclesError` — case 3: `fakeAccount{regErr:
      errors.New("db unavailable")}`; assert `ok == false`, `len(refs) == 0`,
      `len(vehicles) == 0`. Mirrors `TestAuthorizeVehicle_RegisteredVehiclesError`'s shape.
- [ ] 2.5 Do NOT add a fourth test asserting anything about `ListEntriesByVehicles` or
      HTTP behaviour here — that is T3/T4's job, exercised through the existing handler
      tests, not a new `ownedVehicles`-specific one.
- [ ] 2.6 `go vet ./internal/gateway/...` — expect clean (vet compiles the new test file).

## T3 — Route the lookup helpers through `ownedVehicles`; delete `teslaIDsOf`

Depends on: T1 (needs `ownedVehicles` to exist).

- [ ] 3.1 Add the import
      `"github.com/cristianpena/magus-tesla-api/internal/vehicleref"` to
      `internal/gateway/handlers/external_charges.go`'s import block.
- [ ] 3.2 Delete `teslaIDsOf` in full — definition and doc comment
      (`external_charges.go:799-808` today).
- [ ] 3.3 Rewrite `fetchEntryVM` exactly as `design.md` §Ports and implementation gives
      it: replace `vehicles, err := h.acct.RegisteredVehicles(ctx, uid); if err != nil ||
      len(vehicles) == 0 { return ..., false }` with `refs, vehicles, ok :=
      h.ownedVehicles(ctx, uid); if !ok { return ..., false }`, and replace
      `teslaIDsOf(vehicles)` with `vehicleref.TeslaIDs(refs)` in the
      `ListEntriesByVehicles` call. Keep the doc comment's "error or empty list ⇒ false"
      sentence — it still describes the rule, now enforced one level down inside
      `ownedVehicles`.
- [ ] 3.4 Rewrite `fetchEntryTeslaIDAndChargedOn` the same way. It discards the `vehicles`
      return with `_` — it never used the vehicle list, only the ids.
- [ ] 3.5 Do NOT touch the three other `RegisteredVehicles` call sites in this file (around
      lines 256, 384, 664) — explicitly out of scope (`design.md` §D3).
- [ ] 3.6 Do NOT change how `ExternalChargeRowUpdate` or `ExternalChargeRowDelete` call
      `fetchEntryTeslaIDAndChargedOn`, and do NOT touch their own `authorizeVehicle` calls
      (wired in tier 2). Both handlers already call `fetchEntryTeslaIDAndChargedOn` for
      the pre-write old-date lookup — that call site is unchanged; only what happens
      INSIDE the function changes (3.4).
- [ ] 3.7 `go build ./internal/gateway/...` — expect clean.

## T4 — Build, vet, format across the change

Depends on: T2, T3.

- [ ] 4.1 `go build ./...` — expect clean across the whole repository (this change touches
      only `internal/gateway`, so nothing outside it should be affected, but the full
      build is the check that confirms it).
- [ ] 4.2 `go vet ./...` — expect clean.
- [ ] 4.3 `gofmt -l internal/gateway` — expect no output.
- [ ] 4.4 Confirm zero existing test in `internal/gateway/handlers` needed an assertion
      change (only new test additions from T2). If any existing assertion needed changing,
      that is a signal this "no behaviour change" tier actually changed behaviour — STOP
      and report it rather than adjusting the test to match.

## T5 — Docs: `internal/gateway/AGENTS.md` (module-owned)

Depends on: T3 (describes the finished call sites).

- [ ] 5.1 "Vehicle ownership — proved once, at the gateway" section: add a bullet for
      `ownedVehicles` alongside the existing `authorizeVehicle` bullet, stating it is the
      seam `fetchEntryVM`/`fetchEntryTeslaIDAndChargedOn` now use instead of the deleted
      `teslaIDsOf`, per `design.md` §Docs.
- [ ] 5.2 Do not restate the base Doc-Pack list or name reviewer-only docs in this
      section — the project's own "Module Doc-Pack lives in CLAUDE.md only" convention
      applies to the Doc-Pack section specifically; this edit is to a different section
      ("Vehicle ownership"), so it is unaffected, but do not let the edit drift into
      touching the Doc-Pack section while in this file.

## T6 — Docs: KB guides (cross-module-aware)

Depends on: T3 (describes the finished call sites; quoting code that does not exist yet
would describe something untrue).

- [ ] 6.1 `kkpa/context/architecture/charge-record-mutation.md` — the "100-row lookup cap"
      bullet's literal `ListEntriesByVehicles(ctx, teslaIDsOf(vehicles), 0)` quote:
      replace with `ListEntriesByVehicles(ctx, vehicleref.TeslaIDs(refs), 0)`, and note
      `refs`/`vehicles` both come from one `h.ownedVehicles(ctx, uid)` call. Leave the rest
      of the bullet (the 100-row cap, the no-`GetEntry`-port gap) unchanged — this tier
      does not touch either.
- [ ] 6.2 `kkpa/context/use-case/charging/delete-manual-charge.md` — the "100-row lookup
      cap is worst here" gotcha has the same literal quote. Same edit.
- [ ] 6.3 `kkpa/context/use-case/charging/update-manual-charge.md` — flow step 5's "over
      the account's `RegisteredVehicles`" phrasing is still accurate (the underlying call
      is unchanged); add a short clause naming `ownedVehicles` as the seam it now goes
      through, per `design.md` §Docs. This is an addition for discoverability, not a
      factual correction.
- [ ] 6.4 Confirm — do not edit — `openspec/specs/manual-charge-log/spec.md` and
      `openspec/specs/vehicleref/spec.md`. Neither needs a change (`proposal.md`
      §"Is a spec delta needed?"); this task is to VERIFY that by reading both files
      after T3 lands, not to skip reading them.
- [ ] 6.5 Do NOT edit anything under `openspec/changes/archive/` — nothing there
      describes `ownedVehicles` (it postdates every archived change), so there is nothing
      stale to find there. `make archive-guard` enforces leaving it alone regardless.

## T7 — Final verification

Depends on: T0–T6.

- [ ] 7.1 `go build ./...` — clean.
- [ ] 7.2 `go vet ./...` — clean.
- [ ] 7.3 `gofmt -l .` — no output.
- [ ] 7.4 `make vehicleref-guard` — clean. Confirm the output shows the new
      `vehicleref.All(` call in `handlers.go` is accepted (it is on the exclusion list by
      filename) and that no `vehicleref.All`/`vehicleref.Authorize` call appears anywhere
      else new.
- [ ] 7.5 `make boundary-guard`, `make tz-guard`, `make money-guard`, `make i18n-guard`,
      `make ui-guard`, `make migration-guard` — clean (all unaffected by this change, per
      `design.md` §Makefile).
- [ ] 7.6 `make archive-guard` — clean.
- [ ] 7.7 Grep `internal/gateway` for `teslaIDsOf` — expect **zero** hits (fully deleted).
- [ ] 7.8 Grep the whole repository for `.RegisteredVehicles(` inside
      `internal/gateway/handlers/external_charges.go` — expect exactly 3 remaining direct
      call sites (the ones named in `design.md` §D3, left alone on purpose), plus however
      many now go through `ownedVehicles` indirectly (which will not show up in this grep,
      since they call `h.ownedVehicles`, not `h.acct.RegisteredVehicles`, directly — that
      is the point).
- [ ] 7.9 Re-read `design.md` §Docs this change invalidates top to bottom and confirm
      every row was actually done (T5, T6) or explicitly found unnecessary and stated why
      (6.4).
- [ ] 7.10 `openspec validate --strict` on this change — clean.
- [ ] 7.11 Do NOT run `go test ./...`, `make test`, `make test-with-db`, or `make check` —
      owner-only, per the Test-Execution-Policy. Hand the owner these exact commands:
      - `go test ./internal/gateway/...`
      - `go test ./...` (full suite, confirms no other module was affected)
