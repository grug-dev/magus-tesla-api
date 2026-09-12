# Tasks — platform-add-vehicle-authorization-seam

> **Scope.** Creates `internal/vehicleref` — a new, pure, in-memory module (mirrors
> `internal/clock`) — plus a gateway helper that uses it, a new `make vehicleref-guard`, unit
> tests for both, and the required docs. Changes ZERO existing module port and rewires ZERO
> existing handler (design.md D1). No migration, no schema, no `sqlc` change.
>
> **Dependencies / parallelism:**
> - T1 (`internal/vehicleref` package + tests) has no dependency. It is the foundation every
>   other task reads from (the package's final exported names) or builds on top of (the
>   gateway helper).
> - T2 (gateway `authorizeVehicle` helper + tests) depends on **T1** — it imports the package
>   and calls its exported functions. Disjoint files from T1 (different module); could in
>   principle run as soon as T1's signatures are fixed, but wait for T1 to fully land to avoid
>   working against a moving target.
> - T3 (`internal/vehicleref/AGENTS.md`) depends on **T1** only — it documents the package's
>   final public surface, not the gateway helper. MAY run in parallel with T2.
> - T4 (`make vehicleref-guard`) depends on **T1 and T2** — the guard's allow-list names the
>   exact file/function the helper lives in, so it needs T2's file path to exist first.
> - T5 (`ai/architecture.md` + `internal/gateway/AGENTS.md` rule) depends on **T2** — it
>   documents the helper that must already exist. Disjoint file from T3/T4/T6/T7; MAY run in
>   parallel with them once T2 lands.
> - T6 (`CLAUDE.md` guard entry) depends on **T4** — it names the guard T4 creates. Disjoint
>   file from T3/T5/T7; MAY run in parallel with them once T4 lands.
> - T7 (root `README.md`) depends on **T1** only — it needs the module's real responsibility,
>   not the gateway helper or the guard. MAY run in parallel with T2-T6.
> - T8 (`kkpa/context/` sweep) depends on **T1, T2, and T5** — it needs the final module,
>   helper, and architecture rule to know what changed.
> - T9 (verification) depends on **T1-T8**.
>
> **Leader-integrated step:** none — this change adds no database object and regenerates no
> codegen (`sqlc`, `templ`, etc.).

## T1. `internal/vehicleref` package + tests — no dependencies

- [x] T1.1 Create `internal/vehicleref/vehicleref.go` with a package doc comment stating why
      this wrapper is justified (design.md D4's risk note): it guards a repeated,
      security-critical surface — every account-scoped module port a handler could call — where
      a handler that forgets the check must fail to compile, not merely fail a runtime check a
      reviewer might miss. Write the reason itself; never cite a change id, decision id, or doc
      name in the comment.
      Acceptance: the doc comment names the *reason* (compile-time proof on a security-critical
      surface), not a ticket, tier, or design.md reference.
- [x] T1.2 Implement `type Ref struct { teslaID int64 }` with the field unexported.
      Acceptance: `vehicleref.Ref{}` compiles from outside the package (zero value is fine) but
      `vehicleref.Ref{teslaID: 1}` does not.
- [x] T1.3 Implement `func Authorize(owned []int64, want int64) (Ref, bool)` per design.md D3:
      returns `(Ref{teslaID: want}, true)` when `want` appears in `owned`, else the zero `Ref`
      and `false`.
      Acceptance: matches every case in design.md's Test Contract `Authorize` table exactly,
      including the empty-`owned` case.
- [x] T1.4 Implement `func All(owned []int64) []Ref` per design.md D3: wraps every id in
      `owned`, unchecked, preserving order. An empty or nil `owned` returns a zero-length slice
      (never nil-panics on range).
      Acceptance: matches design.md's Test Contract `All` cases, including the empty-input case.
- [x] T1.5 Implement `func TeslaIDs(refs []Ref) []int64` per design.md D3: unwraps each `Ref`
      back to its `teslaID`, preserving order. Empty/nil input returns a zero-length result.
      Acceptance: matches design.md's Test Contract `TeslaIDs` cases; round-trips with `All`
      (`TeslaIDs(All(xs))` equals `xs` for any `[]int64` input, including nil/empty).
- [x] T1.6 Implement `func (r Ref) TeslaID() int64` — a plain accessor, no logic.
- [x] T1.7 `internal/vehicleref/vehicleref_test.go` — transcribe design.md's Test Contract
      cases for `Authorize`, `All`, and `TeslaIDs` verbatim into table-driven or individual test
      functions.
      Acceptance: `go vet ./internal/vehicleref/...` compiles the test file cleanly (this
      change does not run the tests — `Test-Execution-Policy`; the owner's run turns this from
      `awaiting-user-verification` into `done`).

## T2. Gateway `authorizeVehicle` helper + tests — depends on T1

- [ ] T2.1 Add `func (h *Handler) authorizeVehicle(ctx context.Context, uid uuid.UUID, teslaID
      int64) (vehicleref.Ref, error)` in `internal/gateway/handlers/handlers.go`, placed
      immediately beside the existing `resolveSelectedVehicle` (design.md D5). It calls
      `h.acct.RegisteredVehicles(ctx, uid)` — reusing the exact call `vehiclesFor` already
      makes; do NOT add a second account lookup or a new port method — extracts each vehicle's
      `TeslaID` into an `owned []int64` slice, and calls `vehicleref.Authorize(owned, teslaID)`.
      On a `RegisteredVehicles` error or an `Authorize` miss, return a sentinel error (define
      one, e.g. `errVehicleNotAuthorized`, unexported to this package) — never distinguish the
      two failure modes in what the error communicates to the caller (design.md D5, Risk note:
      both must render as the same 404).
      Acceptance: matches design.md's Test Contract `authorizeVehicle` cases 1-3 exactly,
      including that a lookup failure and an ownership miss are indistinguishable to the caller.
- [ ] T2.2 Do NOT rewire any existing handler to call `authorizeVehicle` in this change
      (design.md D1). The four `LatestMetricsByAccount` call sites and the two
      `ListEntriesByAccount` call sites named in the dispatch are untouched here.
      Acceptance: `grep -rn "authorizeVehicle" internal/gateway/handlers --include='*.go'`
      shows exactly the new function's definition and its own test file — no other call site.
- [ ] T2.3 Add a test in `internal/gateway/handlers/handlers_test.go` (or a new sibling
      `_test.go` file, implementer's choice) covering the Test Contract's three
      `authorizeVehicle` cases, using a fake `account.Service` mirroring the existing fake
      pattern in that file.
      Acceptance: `go vet ./internal/gateway/...` compiles the test file cleanly
      (`Test-Execution-Policy` — the owner runs it).

## T3. `internal/vehicleref/AGENTS.md` — depends on T1

- [x] T3.1 Create `internal/vehicleref/AGENTS.md` mirroring `internal/clock/AGENTS.md`'s shape:
      an `Agent-Name: vehicleref` header, a `## Doc-Pack (module)` section (empty — state
      explicitly that this module needs nothing beyond the base pack, like `clock`'s does), the
      module's responsibility (vehicle-ownership proof, so a handler cannot pass an
      unauthorized vehicle id to a module port), its public interface (`Ref`, `Authorize`,
      `All`, `TeslaIDs`, `(Ref).TeslaID()` — final signatures from T1), why it is not in
      `internal/account` (charging may not import account — design.md D2), data ownership
      (none — no table, no config, no external state), and testing notes (pure, offline,
      table-driven, no DB, no container — same shape as `clock`'s).
      Acceptance: every exported symbol T1 produced is documented; the "why not account"
      rationale is stated as a standalone sentence, not buried in a longer paragraph.

## T4. `make vehicleref-guard` — depends on T1 and T2

- [ ] T4.1 Add a `vehicleref-guard` target to the `Makefile`, mirroring `boundary-guard`'s
      grep-based shape and escape-hatch convention (design.md D4): fail if
      `vehicleref\.Authorize\(` or `vehicleref\.All\(` appears anywhere under `internal/`
      outside `internal/vehicleref` itself (its own package-internal use and its own test file
      are exempt, same as every existing guard excludes its own subject file) and outside
      `internal/gateway/handlers/handlers.go`'s `authorizeVehicle` function (T2.1's exact file).
      A hit anywhere else is a second, unreviewed construction site. Escape hatch: a trailing
      `// vehicleref:allow: <reason>` comment on the same line, matching every existing guard's
      convention.
      Acceptance: `make vehicleref-guard` passes clean against the tree T1-T2 produce (the only
      call sites are inside `internal/vehicleref` itself and inside `authorizeVehicle`).
- [ ] T4.2 Add `vehicleref-guard` to `check`'s prerequisite list in the `Makefile`
      (`build vet ui-guard i18n-guard money-guard tz-guard migration-guard boundary-guard
      theme-guard vehicleref-guard archive-guard test`).
      Acceptance: `grep -n '^check:' Makefile` shows `vehicleref-guard` in the dependency list.

## T5. `ai/architecture.md` + `internal/gateway/AGENTS.md` rule — depends on T2

- [ ] T5.1 Add the rule to `ai/architecture.md` (a short paragraph near §2 "Strict Boundary
      Rules" or §5 "Multi-Tenancy" — implementer's judgment on the best-fitting existing
      section): the gateway authorizes the vehicle; a domain module below it does not
      independently re-check tenant ownership of a vehicle identity it receives (design.md,
      spec.md "Vehicle Ownership Check Happens At The Gateway").
      Acceptance: the new rule is discoverable from the same section a future worker would
      already be reading when adding a new vehicle-scoped port method.
- [ ] T5.2 Add the same rule to `internal/gateway/AGENTS.md`, in a location consistent with its
      existing structure (e.g. near "Vehicle-scoped reads — always send the selected TeslaID" or
      as its own short subsection) — state that `authorizeVehicle` exists and where
      (`handlers.go`, beside `resolveSelectedVehicle`), and that it is not yet called by any
      handler (steps 4-6 of the parent plan wire it in, per-table, as each table's `account_id`
      column is actually dropped).
      Acceptance: a future worker reading this file before adding a new per-vehicle handler
      finds `authorizeVehicle` without having to grep for it.

## T6. `CLAUDE.md` guard entry — depends on T4

- [ ] T6.1 Add `make vehicleref-guard` to `CLAUDE.md`'s "Builds & local checks" allowed-commands
      list (next to `make ui-guard` / `i18n-guard` / etc.) and to the `make check` phase-list
      explanation in the same section.
      Acceptance: `grep -n 'vehicleref-guard' CLAUDE.md` shows it in both the allowed-commands
      bullet and the `make check` explanation sentence.

## T7. Root `README.md` — depends on T1

- [ ] T7.1 Add `internal/vehicleref/` to the "Project Structure" tree, in whichever position
      matches the tree's existing ordering convention (alongside `internal/clock`).
- [ ] T7.2 Add a row for `internal/vehicleref` to the "Architecture" table, one line, describing
      it as the platform's vehicle-ownership-proof package — mirroring the terse, one-line style
      every other row in that table already uses.
      Acceptance: both edits land in this change per `CLAUDE.md`'s docs-track-structural-change
      rule (a new module, same change, never a follow-up).

## T8. `kkpa/context/` sweep — depends on T1, T2, and T5

- [ ] T8.1 Grep `kkpa/context/` for any guide whose consumer map, file map, or title this
      change invalidates — a new module joining `internal/`, or a new tenancy rule an existing
      architecture guide states differently. Fix any hit found, in this change (never touching
      `openspec/changes/archive/` — a name-hit there is not this change's to fix).
      Acceptance: state explicitly in the final report what was greped, what was found (if
      anything), and what was fixed — "nothing needed fixing" is an acceptable finding if the
      grep genuinely turns up nothing relevant, but it must be a reported finding, not a
      skipped step.

## T9. Verification — depends on T1-T8

- [ ] T9.1 `go build ./...` and `go vet ./...` pass repo-wide.
- [ ] T9.2 `gofmt -l` reports no diffs for any file this change touched.
- [ ] T9.3 `make vehicleref-guard` and `make boundary-guard` pass.
- [ ] T9.4 Confirm D1 holds: `grep -rn "authorizeVehicle" internal/gateway --include='*.go'`
      shows only its own definition and test — no existing handler calls it yet.
- [ ] T9.5 Confirm `internal/vehicleref` imports nothing project-local (mirrors `clock`'s
      import-cycle-proof shape) — inspect its import block directly.
- [ ] T9.6 `openspec validate platform-add-vehicle-authorization-seam --strict` passes and
      every checkbox above reflects real completion.
- [ ] T9.7 Report the exact test-suite commands the owner must run
      (`go test ./internal/vehicleref/...`, `go test ./internal/gateway/...`, and the full
      `go test ./...`) — this change writes tests but does not execute them
      (`Test-Execution-Policy`); the owner's run is what turns T1.7/T2.3 from
      `awaiting-user-verification` into `done`.
