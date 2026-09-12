## Context

The parent plan (`kkpa/plans/architecture/rekey-vehicle-data-on-vehicle-identity-2026-09-09.md`)
removes `account_id` from every table that only needs to know *which vehicle* the row is about,
not *which account*. Two tables are already done: `analytics.charge_gaps` (step 1, MAG-64) and
`telemetry.vehicle_snapshots` (step 2, MAG-65). Both had no gateway consumer, so removing the
column removed no safety net a user-facing request relied on.

Steps 4, 5, and 6 are different: `charging.supercharger_sessions`, `charging.
manual_charge_entries`, and `analytics.vehicle_metrics` are all read and written from the
gateway today, filtered by `WHERE account_id = $1`. That `WHERE` clause is not just a query
detail — it is the only thing standing between a signed-in user and another user's car data. If
step 4 drops the column before anything replaces that check, a handler bug becomes a data leak
instead of an empty result set.

This change is that replacement. It changes no table and no port. It gives the gateway one new
tool: a way to turn "an account id and a requested vehicle id" into "proof this account may see
that vehicle" — a value, not a boolean — so a module port that later requires the value cannot
be called without the check having already happened.

**Read-heavy performance profile:** not relevant to this change's own work. `internal/vehicleref`
is pure, in-memory, O(n) over a short slice (a user's own vehicle list, never more than a
handful of cars) — no table, no query. The gateway helper this change adds calls
`account.RegisteredVehicles`, a read the gateway already performs on the same request in every
handler that would call `authorizeVehicle` (see `vehiclesFor`, `handlers.go`). No handler in
this change is rewired to call it, so no request gains a query. That happens per-handler in
steps 4-6, each evaluated on its own terms.

**Database rule check:** this change touches no database object — no table, column, index,
constraint, view, or migration. Stated explicitly per `openspec/config.yaml`'s design rule,
rather than left silent.

## Goals / Non-Goals

**Goals:**
- Make "this handler checked vehicle ownership" a fact the Go compiler can verify, not a
  convention a reviewer has to re-check on every new handler.
- Reuse the account lookup the gateway already performs (`RegisteredVehicles`) — add no second
  query, no new port method on `account.Service`.
- Keep `internal/vehicleref` minimal enough that its own `AGENTS.md` can state, and
  `make vehicleref-guard` can enforce, exactly where its constructor may be called from.
- Record, for steps 4-6, the two decisions the user already settled about how their ports
  change shape — so those tickets do not re-derive or re-litigate them.

**Non-Goals (deferred to steps 4-6, or out of scope entirely):**
- Changing any module port's signature. `analytics.Reader.LatestMetricsByAccount`,
  `charging.Reader.ListEntriesByAccount`, and every other `...ByAccount` method are untouched —
  they still take a bare `accountID uuid.UUID` today. Steps 4-6 are what retype them to accept
  `vehicleref.Ref` / `[]vehicleref.Ref`.
- Rewiring any existing handler to call `authorizeVehicle`. The four `LatestMetricsByAccount`
  call sites and the two `ListEntriesByAccount` call sites named in the dispatch keep calling
  what they call today.
- Any migration, schema change, or `sqlc` regeneration — this change owns no table.
- `internal/account` gaining a new method. `RegisteredVehicles` already returns everything the
  helper needs.

## Decisions

### D1 — Scope: the seam only, zero port changes (binding for this change)

This change creates the package, the gateway helper, the guard, the tests, and the docs. It
changes no existing module port and rewires no existing handler. Every read and write in the
gateway today keeps calling exactly what it calls today. This is what makes the change
non-breaking and independently reviewable: nothing that already works can regress, because
nothing that already works is touched.

### D2 — Package: `internal/vehicleref`, a new module mirroring `internal/clock`

Pure, in-memory, no table, no query, no config, no external state — same shape as
`internal/clock` (`ai/architecture.md`'s target blueprint already has a slot for a module like
this: a small, single-purpose library with nothing to protect behind a database). It needs its
own `AGENTS.md` with an `Agent-Name: vehicleref` header and a `## Doc-Pack (module)` section,
empty like `clock`'s — the base pack already covers everything a worker touching this module
needs.

**Why not `internal/account`.** `internal/charging`'s own design forbids importing `account` (a
decision already settled for that module, independent of this change). If `vehicleref.Ref` lived
in `account`, `charging`'s ports could never accept it in steps 4-6, and the whole seam would be
unusable exactly where step 5 needs it most. A cross-cutting type needs a package every consumer
can import, which per `openspec/config.yaml`'s naming rule means a `platform-` change and a home
outside any single domain module.

### D3 — Package surface: exactly four exported names, nothing more

```go
type Ref struct { teslaID int64 }        // unexported field

func Authorize(owned []int64, want int64) (Ref, bool)
func All(owned []int64) []Ref
func TeslaIDs(refs []Ref) []int64
func (r Ref) TeslaID() int64
```

- `Authorize` is the single-vehicle case: "does `want` appear in `owned`?" A miss returns the
  zero `Ref` and `false` — the caller must check the bool; the zero value alone is not a signal
  (its `teslaID` is `0`, which for one Tesla vehicle is a real vendor id in theory, so the bool
  is load-bearing, not a formality).
- `All` is the account-wide case steps 4-6 need for the `...ForVehicles(ctx, refs
  []vehicleref.Ref)` port shape recorded in D6 below — it wraps every owned id, unchecked,
  because "owned" is already the caller's own resolved list; there is nothing left to authorize
  against.
- `TeslaIDs` is the inverse of `All` — unwrap a `[]Ref` back to `[]int64` for the SQL layer's
  `= ANY($1)` parameter. Centralizing this loop in the package that owns the type means every
  module's `db` package writes the same one-line call, not its own copy of the unwrap loop.
- `TeslaID()` is the only way to read the wrapped id back out. A module port that accepts a
  `Ref` calls this once, at the point it builds its query.

Nothing else is exported. No `MustAuthorize`, no `NewUnchecked`, no way to build a `Ref` other
than through `Authorize` or `All` — both of which require the caller to already hold a resolved
ownership list.

### D4 — Two independent guards against misuse, because Go has no `implements`/`friend` mechanism

An unexported field (`teslaID`) blocks a **struct literal** from outside the package
(`vehicleref.Ref{teslaID: 999}` fails to compile). It does **not** block a bad **constructor**:
nothing in the language stops a second exported function, added later, from building a `Ref`
with no check at all — Go has no way to mark `Authorize` as the type's only legal producer.

So the guarantee needs two layers:

1. **The type itself.** Once a module port takes `vehicleref.Ref` instead of a bare `int64`
   (steps 4-6), a handler that never calls `Authorize` or `All` has no `Ref` to pass — the
   handler **fails to compile**, not merely fails a runtime check a reviewer might miss.
2. **`make vehicleref-guard`.** A new standalone guard, in the same grep-based shape as
   `boundary-guard`/`money-guard`/`tz-guard`, that fails if `vehicleref.Authorize(` or
   `vehicleref.All(` appears anywhere outside the gateway's `authorizeVehicle` helper (and its
   own package-internal use, and test files). This is what stops a *future* handler from adding
   its own private wrapper around `RegisteredVehicles` that skips the shared helper and calls
   `Authorize` directly with a hand-rolled ownership list. Escape hatch:
   `// vehicleref:allow: <reason>`, on the same line, matching every existing guard's
   convention (all scanned files are `.go`, so no separate above-the-line placement is needed,
   unlike `i18n-guard`'s `.templ` pass).

Both are needed: the type stops step 4-6's *ports* from being called without a `Ref` in hand;
the guard stops a *second construction site* from ever appearing outside the one helper that is
supposed to be the seam's only front door.

### D5 — The gateway helper: `authorizeVehicle`, returns 404 never 403

```go
func (h *Handler) authorizeVehicle(ctx context.Context, uid uuid.UUID, teslaID int64) (vehicleref.Ref, error)
```

Placed beside `resolveSelectedVehicle` in `internal/gateway/handlers/handlers.go` — both read
`account.RegisteredVehicles(ctx, uid)`, so keeping them next to each other keeps the "how do I
get the caller's vehicle list" logic in one place to read. It:

1. Calls `h.acct.RegisteredVehicles(ctx, uid)` (the exact call `vehiclesFor` already makes; no
   new port method).
2. Extracts the `TeslaID` from each returned `account.Vehicle` into an `owned []int64` slice.
3. Calls `vehicleref.Authorize(owned, teslaID)`.
4. On a miss, or on a `RegisteredVehicles` error, returns a sentinel error the caller renders as
   HTTP **404**, never 403.

**Why 404 and not 403.** A 403 confirms the resource exists and only access is denied — it tells
an attacker probing sequential Tesla ids that `teslaID` is a real vehicle, just not theirs. A
404 makes "not yours" and "does not exist" indistinguishable from outside, which is the correct
leak surface for a numeric id space anyone could enumerate. This mirrors the tenant check the
parent plan's own verification steps require after every step from 3 onward ("request a
`tesla_id` belonging to another account — it must return 404, not data").

### D6 — Forward decision, recorded for steps 4-6: account-wide ports take a list (NOT implemented here)

`analytics.LatestMetricsByAccount(ctx, accountID)` becomes
`LatestMetricsForVehicles(ctx, refs []vehicleref.Ref)`, with the underlying SQL moving from
`WHERE account_id = $1` to `WHERE tesla_id = ANY($1)`. The same reshaping applies to every other
`...ByAccount` port `internal/gateway/AGENTS.md` currently lists (`ListEntriesByAccount`, and
any future one).

**Why a list, not a loop over `Authorize`.** At every one of the four `LatestMetricsByAccount`
call sites in `handlers.go` (`vehiclesFor`, `dashboardFor`, `navHeaderFor`,
`buildExternalChargesPage`'s battery suggestion), the gateway already has the full vehicle list
in hand from its own `RegisteredVehicles` call. Passing that list down costs no extra query.
Keeping the port singular (`LatestMetricsForVehicle(ctx, ref)`) would force the dashboard to run
one query per vehicle per render — wrong for a read-heavy platform whose whole point is cheap,
batched dashboard reads (`ai/architecture.md` §7).

**This decision is not implemented by this change.** No port listed above changes signature
here (D1). It is written into this design so steps 4-6 do not re-derive or re-litigate it —
each of those changes' own design.md should cite this decision by restating it, not by
re-arguing it.

### D7 — Forward decision, recorded for steps 4-6: an empty vehicle list is a warning, not an error (NOT implemented here)

When `refs` is empty, the reshaped port (D6) logs a warning and returns zero rows — it does
**not** return an error. Two reasons:

- `tesla_id = ANY('{}')` matches nothing in Postgres — an empty list is fail-closed by
  construction, the same direction as every other empty-input case in this codebase.
- "This account has no vehicles yet" is a real, expected state (a brand-new signup), not a
  fault. Treating it as an error would force every caller to special-case a state that already
  has its own UI treatment (the `NeedsConnect` flow).

The warning log exists so the case stays visible in the future ports' own telemetry, without
gating the response on it. The gateway's own existing `if len(registered) > 0` guard
(`handlers.go`, around line 220 in `vehiclesFor`) already keeps a brand-new user with zero
vehicles from ever reaching the reshaped port at all — so the warning fires only for a genuinely
unexpected empty list reaching the port some other way, not on every new-user render.

**This decision is not implemented by this change** — no port exists yet to apply it to. Recorded
so steps 4-6 apply the same rule rather than each inventing its own.

### D8 — Unit tests: included, scoped to the seam only

The parent plan's own execution rules say "Unit tests: excluded" for the whole roadmap. The user
overrode that for this one step, because it is the step where a mistake means one user's car
data becomes visible to another. Tests cover:

- `vehicleref.Authorize` — hit and miss cases, including that a `Ref` for an unowned id cannot
  be produced (see Test Contract below).
- `vehicleref.All` / `TeslaIDs` — round-trip shape.
- The gateway's `authorizeVehicle` helper — using an `account.Service` fake, mirroring the
  existing pattern in `handlers/handlers_test.go`.

No test is added outside this scope — no test for a module port this change does not touch.

## Test Contract

Authored before implementation, per `ai/go-conventions.md` §Testing ("author their expected
values up front"). The owner runs these — this change writes them but does not execute
`go test` (`Test-Execution-Policy`).

### `vehicleref.Authorize`

```go
owned := []int64{111, 222, 333}

// Hit: want is in owned.
ref, ok := vehicleref.Authorize(owned, 222)
// ok == true
// ref.TeslaID() == 222

// Miss: want is not in owned.
_, ok = vehicleref.Authorize(owned, 999)
// ok == false
// (the returned Ref is the zero value; callers must check ok, never trust a zero TeslaID)

// Miss: owned is empty.
_, ok = vehicleref.Authorize(nil, 222)
// ok == false
```

### `vehicleref.All`

```go
owned := []int64{111, 222, 333}
refs := vehicleref.All(owned)
// len(refs) == 3
// vehicleref.TeslaIDs(refs) == []int64{111, 222, 333}  (order preserved)

refs = vehicleref.All(nil)
// len(refs) == 0
```

### `vehicleref.TeslaIDs`

```go
refs := []vehicleref.Ref{ /* built via Authorize/All in the test, one per case */ }
got := vehicleref.TeslaIDs(refs)
// got is []int64 in the same order as refs, one element per Ref
vehicleref.TeslaIDs(nil)
// == []int64{} or nil — either is acceptable; the test asserts len == 0, not identity
```

### Gateway `authorizeVehicle` (fake `account.Service`)

```go
// Fake RegisteredVehicles returns [{TeslaID: 111}, {TeslaID: 222}].

// Case 1 — owned vehicle:
ref, err := h.authorizeVehicle(ctx, uid, 111)
// err == nil
// ref.TeslaID() == 111

// Case 2 — vehicle belongs to a different account:
_, err = h.authorizeVehicle(ctx, uid, 999)
// err != nil
// the caller-visible status is 404 (assert via the handler-level test that renders it,
// mirroring the existing httptest pattern in handlers_test.go)

// Case 3 — RegisteredVehicles itself errors:
// fake returns an error
_, err = h.authorizeVehicle(ctx, uid, 111)
// err != nil
// also renders 404 — the helper does not distinguish "lookup failed" from "not owned" in its
// response, so a transient account-read failure cannot be used to infer whether a vehicle id
// is real (same reasoning as D5)
```

## Risks / Trade-offs

- **[Trade-off]** `authorizeVehicle` returns the same 404 for "not yours" and "account lookup
  failed" (Test Contract Case 3) → **Accepted**: distinguishing them in the response would leak
  which failure occurred, and a 5xx here would be worse — it would tell an attacker their probe
  triggered a different code path than a wrong-but-well-formed id. The gateway's own error log
  still records the real cause for the owner.
- **[Risk]** Two independent guard layers (D4) add a small amount of indirection for a
  single-purpose type → **Accepted, explicitly justified**: this is a wrapper on a
  security-critical surface with 30+ eventual call sites (every `...ByAccount` port across three
  modules), which `CLAUDE.md`'s own AI-efficiency principle names as exactly the case where
  indirection earns its cost — a stable, self-describing `int64` would not.
- **[Not a risk, a scope note]** D6 and D7 are written but not implemented here. If a later step
  ships without reading this design, it could re-derive a different shape (e.g. a per-vehicle
  loop instead of a list-taking port) → **Mitigation**: both decisions are stated as binding
  "forward decisions" in this document and in the dispatch to future workers must cite this
  change by name, not by re-deriving from scratch.

## Reverse-direction check (existing `make` targets and guards)

Per `CLAUDE.md`'s "workflow & architectural decisions" rule, checked explicitly rather than
assumed:

- **`make check`'s guard list** — `internal/vehicleref` adds one new guard,
  `vehicleref-guard`, appended to `check`'s prerequisite list in the `Makefile`
  (`build vet ui-guard i18n-guard money-guard tz-guard migration-guard boundary-guard
  theme-guard vehicleref-guard archive-guard test`). No existing guard needs a change: the new
  package has no HTML (`ui-guard`/`i18n-guard` scope is `internal/gateway/templates` and
  `handlers`, unaffected), no money formatting, no time handling beyond none at all
  (`tz-guard` scans `internal` broadly but `vehicleref.go` has no `time` import to trip it), no
  migration (`migration-guard` scans `MIGRATIONS_DIRS`, unaffected), and does not import
  `telemetry` (`boundary-guard`, unaffected).
- **`MIGRATIONS_DIRS`** — unaffected. `internal/vehicleref` has no `db/` folder and is never
  added to this list, exactly like `internal/clock`.
- **`boundary-guard`** — unaffected in the direction it checks (gateway → telemetry). Checked
  the *other* direction too: `internal/vehicleref` imports nothing project-local (mirrors
  `clock`'s "stdlib only" shape, though this package needs no stdlib import beyond nothing
  itself — it operates only on `int64`), so it cannot create a new import cycle with any module,
  including `account` or `charging`.
- **`sqlc`** — unaffected. No `sql:` entry is added to `sqlc.yaml`; the module has no queries.
- **Finding:** none of the eight existing guards needs a code change to keep passing once this
  module lands. The only `Makefile` edit is the new target itself plus its addition to `check`'s
  dependency line.

## Implementation Plan

(No "Migration Plan" — this change owns no database object.)

1. `internal/vehicleref/vehicleref.go` — package doc comment (states the AI-efficiency
   justification per D4's risk note — a wrapper on a repeated, security-critical surface, never
   citing this document or a decision id), `Ref`, `Authorize`, `All`, `TeslaIDs`,
   `(Ref).TeslaID()` (D2, D3).
2. `internal/vehicleref/vehicleref_test.go` — the Test Contract's `Authorize`/`All`/`TeslaIDs`
   cases, transcribed verbatim (D8).
3. `internal/vehicleref/AGENTS.md` — mirroring `internal/clock/AGENTS.md`'s shape: `Agent-Name:
   vehicleref`, empty `## Doc-Pack (module)`, responsibility, public interface, the
   compile-time-guarantee rationale, data ownership (none), testing notes.
4. `internal/gateway/handlers/handlers.go` (or a new sibling file, implementer's choice, next to
   `resolveSelectedVehicle`) — `authorizeVehicle` (D5).
5. `internal/gateway/handlers/*_test.go` — the Test Contract's `authorizeVehicle` cases (D8).
6. `Makefile` — `vehicleref-guard` target (D4), mirroring `boundary-guard`'s grep shape; add it
   to `check`'s prerequisite list.
7. `ai/architecture.md` and `internal/gateway/AGENTS.md` — the rule: the gateway authorizes the
   vehicle; modules below it do not check tenancy (D5).
8. `CLAUDE.md` — add `make vehicleref-guard` to the allowed-commands list and the `make check`
   phase list.
9. Root `README.md` — "Project Structure" tree and "Architecture" table gain
   `internal/vehicleref` (`CLAUDE.md`'s docs-track-structural-change rule).
10. `kkpa/context/` sweep — grep for anything the new module or rule invalidates; fix in this
    change (never touching `openspec/changes/archive/`).
11. `go build ./...`, `go vet ./...`, `gofmt -l`, `make vehicleref-guard`, `make boundary-guard`
    pass. `go test ./...` is the owner's step (`Test-Execution-Policy`).

**Rollback:** delete `internal/vehicleref/`, the `authorizeVehicle` helper, the `vehicleref-guard`
Makefile target, and revert the doc edits. Nothing else in the repo references the package yet
(D1 — zero port changes), so this is a clean, single-commit revert with no downstream
consequence.

## Open Questions

None. All decisions in this document were settled with the user during the leader's dispatch
interview before this proposal was written (D1-D5, D8 are binding for this change; D6-D7 are
forward decisions binding on steps 4-6, not implemented here).
