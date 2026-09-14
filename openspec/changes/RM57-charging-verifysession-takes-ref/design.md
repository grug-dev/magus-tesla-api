# Design — RM57-charging-verifysession-takes-ref

Source ticket: MAG-67. Roadmap tier 3.

**This change touches no database object** — no table, column, index, constraint,
view, or migration. Stated explicitly per `openspec/config.yaml`'s design rule rather
than left silent. It exists as its own `design.md` anyway because it retypes a public
port, and this project treats a port signature the same way it treats a schema: a
decision other modules and tests depend on, worth writing down before the
implementation exists.

## Overview

One method, `charging.SessionVerifier.VerifySession`, stops accepting a bare
`teslaID int64` and starts requiring a `vehicleref.Ref` — a value that only exists
once a caller has proven, through `internal/vehicleref`, that it owns the vehicle in
question. Everything downstream of the parameter is unchanged: the implementation
unwraps the `Ref` back to the identical `int64` on its first line and the rest of the
method — the range validation, the derivation branch, the query, the transaction —
does not move.

This is the first real caller of `internal/vehicleref`, built by MAG-66
(`platform-add-vehicle-authorization-seam`) specifically so a later change could give
it one. That change's own D1 deferred every port change on purpose; this change is
what D1 deferred.

## Decisions

### D1 — `internal/charging` may import `internal/vehicleref`

MAG-66's design.md D2 put `vehicleref` in its own package, outside `internal/account`,
*because* `internal/charging` is forbidden from importing `internal/account` — a rule
already settled independently of this change. If `vehicleref.Ref` lived in `account`,
this change could not exist. Confirmed against `ai/architecture.md`'s dependency-
direction rule: `vehicleref` imports nothing project-local (mirrors `internal/clock`'s
shape), so `charging → vehicleref` creates no cycle in either direction, and it is not
one of the four packages `internal/charging/AGENTS.md`'s "MUST NOT import" list names
(`tesla`, `account`, `telemetry`, `gateway`). This is `internal/charging`'s first
sibling-module import — every other import today is a standard-library package, the
external `uuid`/`pgx`/`pgxpool`/`pgtype` packages, or the module's own `chargingdb`.

### D2 — Only `VerifySession` is retyped; the three read ports keep `teslaID int64` (restates roadmap RD14, not re-derived)

`SessionReader.ListSessionsByVehicleBetween`,
`SuperchargerSessionAnalyticsReader.ListSessionsByVehicleUpdatedSince`, and
`…ListSessionsByVehicle` are unchanged. `internal/analytics` calls all three
(`recalculate.go:158`, `recalculate.go:279`, `reader.go:141`), and
`make vehicleref-guard` allows `vehicleref.Authorize`/`.All` only inside the gateway's
`authorizeVehicle` helper (`internal/gateway/handlers/handlers.go`) — `internal/analytics`
has no legal construction site for a `Ref`. `VerifySession` is the one port whose sole
caller is the gateway (`internal/gateway/handlers/supercharger.go:681`), so it is the
one port that can require proof of ownership without locking out an existing caller
that cannot produce one.

This also restates RM57's own roadmap file, which already settled it under "Facts
already checked — do not re-derive": *"`internal/analytics` cannot build a
`vehicleref.Ref` … So the three `ListSessionsByVehicle*` read ports … cannot be
retyped. `VerifySession` can."*

### D3 — The guarantee moves earlier; the runtime predicate does not change

`VerifySession`'s `WHERE id = @id AND tesla_id = @tesla_id` is unchanged — it still
compares two plain values. What changes is where the second value is allowed to come
from. Before this change, a caller could pass any `int64`, including one it never
checked. After this change, a caller can only pass a `vehicleref.Ref`, which
`internal/vehicleref`'s own package guarantees was built by `Authorize` (checked
against an owned list) or `All` (the caller's own already-resolved list) — never a
struct literal from outside that package.

A mismatched vehicle still matches zero rows, exactly like an unknown session id, and
a caller still cannot tell "not yours" from "does not exist" from the returned error
alone — the type change moves the *proof* earlier; it does not change what a mismatch
looks like at the SQL layer. This is why the change needs no new test for "wrong
vehicle behaves like unknown id" — the existing `TestVerifySession_T6_WrongVehicleIsNoOp`
(`db_session_verifier_integration_test.go`) already covers that behavior and keeps
covering it once its argument is a `Ref` instead of a bare `int64` (see D5, D6).

The interface doc comment is rewritten to say this directly. Old text (`charging.go`,
current):

> id/teslaID scope the update: WHERE id = @id AND tesla_id = @tesla_id. This is the
> ONLY tenant boundary left on the Supercharger write path — the gateway resolves
> which vehicle it believes owns the session and passes it here, so a mismatched
> vehicle matches zero rows exactly like an unknown id, and a caller cannot tell "not
> yours" from "does not exist". Zero rows matched surfaces as an error wrapping
> pgx.ErrNoRows, exactly mirroring Writer.Update's own not-found semantics.

New text:

> id/ref scope the update: WHERE id = @id AND tesla_id = @tesla_id, using
> ref.TeslaID(). This is the ONLY tenant boundary left on the Supercharger write
> path. The caller no longer passes a bare id it merely believes is correct — it
> must hold a vehicleref.Ref, which only internal/vehicleref can construct, and only
> from a caller-owned vehicle list. A mismatched vehicle still matches zero rows
> exactly like an unknown id, and a caller still cannot tell "not yours" from "does
> not exist" — this change moves the ownership proof earlier, it does not change
> what a mismatch looks like. Zero rows matched surfaces as an error wrapping
> pgx.ErrNoRows, exactly mirroring Writer.Update's own not-found semantics.

The one other doc-comment line naming the old shape, a few lines above (`charging.go`,
current: `Calling VerifySession(ctx, teslaID, id, nil, nil) clears both percentages…`),
becomes `Calling VerifySession(ctx, ref, id, nil, nil) clears both percentages…`.

### D4 — Implementation: one unwrap line, nothing else moves

```go
func (v *sessionVerifier) VerifySession(ctx context.Context, ref vehicleref.Ref, id uuid.UUID, startBatteryPct, endBatteryPct *int) (Session, error) {
	teslaID := ref.TeslaID()
	// everything below this line is byte-for-byte unchanged
	...
```

Every use of `teslaID` inside the method body — the range checks stay as they are
(they never touched `teslaID`), `LockSessionForVerificationParams{ID: id, TeslaID:
teslaID}`, `packCapacityKWh(ctx, v, row.TeslaID)` (this one reads the *locked row's*
own `TeslaID`, already a plain `int64` since tier 2, and stays untouched), and
`VerifySuperchargerSessionParams{ID: id, TeslaID: teslaID, ...}` — keeps compiling
against the same local `teslaID` name. The comment at the current line 168
(`// the teslaID parameter above -- it is NOT NULL...`) is corrected to say "the
teslaID local variable above" since it is no longer a parameter.

Rejected alternative — **thread `ref` through the whole method body, calling
`ref.TeslaID()` at each use site**: this touches five lines instead of one for zero
behavioral gain and makes a future diff noisier. `internal/vehicleref`'s own design
(MAG-66 D3) already treats `TeslaID()` as a cheap, repeatable accessor with no state
to go stale between calls inside one function — unwrapping once, into a `const`-like
local, is the same pattern `packCapacityKWh(ctx, v, row.TeslaID)` already uses one
line below.

### D5 — Test strategy: one shared helper, kept for brevity, not for the guard

**The guard gap this change first reported is fixed, ahead of this revision.** The
leader corrected `make vehicleref-guard` (`Makefile`) to exempt every `_test.go`
file, and recorded the exemption in `internal/vehicleref/AGENTS.md`'s Boundaries
section, which now tells a future agent to build a test `Ref` with
`vehicleref.All([]int64{teslaID})[0]` directly. So no test file needs the
`// vehicleref:allow: …` marker. **This change adds no such marker anywhere.**

Five `internal/charging` test files call `VerifySession` directly with a bare
`teslaID`, all `package charging_test` (checked directly, one file at a time — not
assumed): `db_session_verifier_integration_test.go`,
`db_inferred_capacity_sessions_integration_test.go`,
`db_session_mirror_change_detection_integration_test.go`,
`db_monthly_capacity_integration_test.go`, and
`db_session_reader_updated_since_integration_test.go`. A shared package-level helper
compiles for all five without a separate import in each.

Real `VerifySession` call sites needing the substitution, counted per file, excluding
comments and non-call lines: 20 + 2 + 1 + 5 + 2 = **30**, all inside `internal/charging`
(exact lines in `tasks.md`). A raw text search for the string `VerifySession(` across
every `_test.go` file that mentions it returns 33, not 30 — the extra three are two
explanatory comments (one in `db_inferred_capacity_sessions_integration_test.go`, one
in `internal/gateway/handlers/supercharger_test.go`) and one fake method declaration,
`func (f *fakeSessionVerifier) VerifySession(...)`, also in `supercharger_test.go`.
That file belongs to `internal/gateway`, tier 4's sandbox — its fake's signature and
its own call sites are tier 4's work, not this change's.

This change still adds **one** shared helper, next to the existing `ptrIntV` helper
in `db_session_verifier_integration_test.go`, purely to keep 30 call sites short and
consistent — not to carry an escape hatch, since none is needed:

```go
// refFor builds a vehicleref.Ref for teslaID for a direct port test. These tests
// call VerifySession without going through a gateway request, so there is no
// account or RegisteredVehicles list to authorize against — the Ref only needs to
// carry the same vehicle id VerifySession's old bare-int64 parameter carried.
func refFor(teslaID int64) vehicleref.Ref {
	return vehicleref.All([]int64{teslaID})[0]
}
```

Every call site becomes `v.VerifySession(ctx, refFor(222), id, ...)` in place of
`v.VerifySession(ctx, 222, id, ...)` — a mechanical, one-argument substitution, never
a value change.

Rejected alternative — **call `vehicleref.All([]int64{teslaID})[0]` inline at each of
the 30 call sites**: correct now that no marker is needed, but it repeats a five-word
idiom 30 times for no gain over one named helper. Rejected alternative — **put
`refFor` in `internal/vehicleref`'s own test-support surface**: `internal/vehicleref`
exports exactly four names by design (MAG-66 D3); adding a helper there for one
consuming module's tests would widen that surface for a single caller.

### D6 — Test contract: no expected value changes, because none of them can

Authored before the implementation exists, per `ai/go-conventions.md` §Testing
("author their expected values up front") — though for this change the point is
narrower than usual: **every existing `VerifySession` test's expected `Session`,
error, and database state stays exactly what it is today.** `refFor(teslaID)`
followed immediately by `.TeslaID()` inside `VerifySession` (D4) round-trips the same
`int64` with no transformation in between, so no test's assertions change, only its
call syntax. This covers the 30 call sites across the five `internal/charging` files
named in D5 — never `internal/gateway/handlers/supercharger_test.go`, which is tier
4's file.

Concretely, for `TestVerifySession_T6_WrongVehicleIsNoOp`
(`db_session_verifier_integration_test.go:154`):

```go
// before this change:
_, err := v.VerifySession(ctx, 222, id, ptrIntV(20), ptrIntV(80))
// err wraps pgx.ErrNoRows; the row seeded under tesla_id 111 is untouched.

// after this change:
_, err := v.VerifySession(ctx, refFor(222), id, ptrIntV(20), ptrIntV(80))
// same err, same untouched row -- refFor(222).TeslaID() == 222, identical to the
// bare literal it replaces.
```

The same substitution, with the same "expected value is unchanged" guarantee,
applies to the other 29 call sites this change touches. No test in this change
asserts a new outcome; every test asserts the same outcome it asserted before,
through a different call shape. This is a deliberate consequence of D4: a change
that only moves *where* a value comes from, never *what* the value is, cannot change
a downstream assertion.

### D7 — The guard fix is leader-owned and already landed (commit `9b39034`)

This change first reported that `make vehicleref-guard` had no `_test.go` exemption,
against MAG-66's own design intent. `Makefile` and `internal/vehicleref/AGENTS.md`
sit outside this module's sandbox, so the leader made the fix directly: the guard's
grep chain now drops any line matching `_test\.go:`, and its comment, help text, and
both echo messages were updated to match. `make vehicleref-guard` passes. `tasks.md`
does not re-do this work — it is done, and confirmed clean by T6 in this change's own
final verification.

## Docs

- **`internal/charging/AGENTS.md`**: add `internal/vehicleref` to the §Allowed
  Imports list (only `charging.go` and `session_verifier.go` import it — following
  that section's existing "only the files that own it" convention). Add one sentence
  to the §"Supercharger session verification port" note: `VerifySession` now requires
  a `vehicleref.Ref` rather than a bare vehicle id, so a caller must have already
  proven ownership through `internal/vehicleref` — cite no decision ID, per this
  project's code-comment rule, but the module's own `AGENTS.md` is not a code comment
  and MAY name this change, since `AGENTS.md` is a living doc that gets corrected in
  place, unlike an archived `design.md`.
- **`kkpa/context/workflows/supercharger-stats-read.md:45`**: the printed signature
  `VerifySession(ctx, teslaID, id, startBatteryPct, endBatteryPct *int) (Session, error)`
  is now stale and becomes
  `VerifySession(ctx, ref vehicleref.Ref, id, startBatteryPct, endBatteryPct *int) (Session, error)`.
  Nothing else on that line changes — the `WHERE` clause and `SET` clause description
  are still accurate.
- **`kkpa/context/use-case/charging/verify-session-battery.md:47`**: the printed call
  `charging.SessionVerifier.VerifySession(ctx, teslaID, id, startBatteryPct, endBatteryPct)`
  becomes `charging.SessionVerifier.VerifySession(ctx, ref, id, startBatteryPct, endBatteryPct)`.
- **Deliberately NOT updated by this change**: `kkpa/context/architecture/gateway-reader-writer-ports.md`
  and `kkpa/context/input-port/charging/supercharger-stats.md` both describe the
  gateway's write path as having "**No `RegisteredVehicles` ownership check**" — that
  sentence is still true after this change lands, because this change does not touch
  the gateway. It becomes false only when tier 4 wires `authorizeVehicle` in. Fixing
  it now would describe a behavior tier 4 has not shipped yet. Tier 4's own `design.md`
  owns that edit.
- **`kkpa/context/pending-spec-to-sync/applied/2026-09-14-charging.md`**: NOT edited.
  It is an applied proposal record (a snapshot of what was true when tier 2 archived),
  not a live guide — the project's own convention keeps `applied/` immutable the same
  way `openspec/changes/archive/` is immutable.
- **`openspec/specs/charging/spec.md`**: one MODIFIED requirement — see the delta
  spec in this change's `specs/charging/spec.md`.

## Reverse-direction check (existing `make` targets and guards)

Per `CLAUDE.md`'s "workflow & architectural decisions" rule, checked explicitly:

- **`make vehicleref-guard`** — now exempts every `_test.go` file (leader fix, D7,
  commit `9b39034`). `refFor`'s `vehicleref.All(...)` call in
  `db_session_verifier_integration_test.go` needs no marker. No further `Makefile`
  change is needed from this change.
- **`make boundary-guard`** — unaffected. It scans `internal/gateway/**/*.go` for the
  `internal/telemetry` import path; this change touches neither `internal/gateway`
  nor `internal/telemetry`.
- **`make check`'s guard list** — unaffected; no guard is added, removed, or
  reordered.
- **`sqlc` / `MIGRATIONS_DIRS`** — unaffected. No query, no migration, no `sql:`
  entry.
- **Finding**: the only file this change needs outside `internal/charging` itself is
  the two `kkpa/context/` guides named above — no `Makefile`, no CI config, no
  `sqlc.yaml` change.

## Risks / Trade-offs

- **[Risk, accepted, roadmap-scoped]** `go build ./...` is red from this change
  landing until tier 4 lands, because `internal/gateway/handlers/supercharger.go`'s
  one call site still passes a bare `int64`. → **Accepted**: this is the same shape
  tier 2 left for `internal/app`/`internal/analytics` for one wave, and the roadmap's
  own tier table already sequences tier 4 immediately after this one, with tier 4's
  proposal prompt already written (`openspec/roadmaps/RM57-rekey-supercharger-pair-on-tesla-id.md`).
- **[Not a risk, a scope note]** This change adds no new behavioral test, because it
  changes no behavior (D6) — only a future reviewer unfamiliar with D3/D6 might expect
  one. → **Mitigation**: D6 states this explicitly and names the one test
  (`TestVerifySession_T6_WrongVehicleIsNoOp`) that already proves the property this
  change is *about* (a mismatched vehicle is rejected), so nothing needs duplicating.

## Rollback

Revert `charging.go`'s signature and doc comment, `session_verifier.go`'s unwrap
line, the `refFor` helper and its 30 call-site substitutions, the `AGENTS.md` entry,
and the two `kkpa/context/` line edits. No migration to reverse, no data to restore —
this change wrote no row and dropped no column. A clean, single-commit revert with no
downstream consequence beyond re-breaking the compile-time guarantee this change adds.
The `vehicleref-guard` fix (D7) is a separate, already-landed commit and is not part
of this rollback.

## Implementation Plan

See `tasks.md` for the full, dependency-ordered breakdown. In outline:

1. `internal/charging/charging.go` — import `vehicleref`, retype
   `SessionVerifier.VerifySession`, rewrite its two affected doc-comment passages
   (D3).
2. `internal/charging/session_verifier.go` — import `vehicleref`, add the one unwrap
   line, correct the one stale comment (D4).
3. `internal/charging/db_session_verifier_integration_test.go` — add `refFor` (D5).
4. Five test files — mechanical `teslaID` → `refFor(teslaID)` substitution at every
   `VerifySession` call site (D6).
5. `internal/charging/AGENTS.md` — Allowed Imports + the one-sentence note (§Docs).
6. Two `kkpa/context/` guides — the two stale printed signatures (§Docs).
7. `go build ./internal/charging/...`, `go vet ./internal/charging/...`, `gofmt -l`,
   `make vehicleref-guard` — expect clean. `go build ./...` (whole repo) is expected
   to fail on `internal/gateway` until tier 4 — this is not a regression to chase in
   this change.

## Open Questions

None. D1–D7 resolve every question raised so far, including the two the dispatch
asked to be verified rather than assumed (whether `vehicleref-guard` exempts
`_test.go` files — it now does, fixed by the leader, D7 — and how a test builds a
`Ref` — `refFor`, D5, no marker needed).
