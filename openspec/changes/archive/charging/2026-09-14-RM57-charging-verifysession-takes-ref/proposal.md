# RM57-charging-verifysession-takes-ref

> Source: MAG-67 — https://linear.app/magus-monitor/issue/MAG-67/4-re-key-the-supercharger-pair-on-tesla-id-reshape-the-mirror
> Tier 3 of `openspec/roadmaps/RM57-rekey-supercharger-pair-on-tesla-id.md`.
> Depends on tier 1 (`RM57-telemetry-rekey-supercharger-history-on-tesla-id`) and
> tier 2 (`RM57-charging-rekey-supercharger-sessions-on-tesla-id`), both archived.

## Why

`MAG-66` (`platform-add-vehicle-authorization-seam`) built `internal/vehicleref`: a
small type, `vehicleref.Ref`, that only exists once a caller has proven it owns a
vehicle. That change touched no port. It shipped the tool and left every port
unchanged on purpose (its own D1), so a later change could give the tool its first
real user.

`charging.SessionVerifier.VerifySession` is that user. It is the Supercharger write
path's only tenant check — `WHERE id = @id AND tesla_id = @tesla_id`. Today the
caller passes a bare `int64` it merely believes is the right vehicle. Nothing in the
type system stops a handler from passing the wrong one, or from skipping the
ownership check that is supposed to produce it. Retyping the parameter to
`vehicleref.Ref` closes that gap: a `Ref` can only come from `vehicleref.Authorize`
or `vehicleref.All`, so a caller with no `Ref` has nothing to pass, and the mistake
fails to compile instead of failing silently at runtime.

## What changes

- **`internal/charging/charging.go`**: `SessionVerifier.VerifySession`'s second
  parameter changes from `teslaID int64` to `ref vehicleref.Ref`. The doc comment is
  updated to describe the new guarantee — see `design.md` D3.
- **`internal/charging/session_verifier.go`**: the implementation unwraps
  `ref.TeslaID()` into the same local `teslaID` the rest of the method already uses.
  No other line in the method changes — no new branch, no new query.
- **New import**: `internal/vehicleref`, added to both files above and to
  `internal/charging/AGENTS.md`'s Allowed Imports list (`design.md` D1).
- **The three read ports are NOT touched**: `SessionReader.ListSessionsByVehicleBetween`,
  `SuperchargerSessionAnalyticsReader.ListSessionsByVehicleUpdatedSince` and
  `…ListSessionsByVehicle` keep `teslaID int64` (`design.md` D2, restating roadmap
  RD14).
- **No migration, no `sqlc`, no query change.** `VerifySuperchargerSession`'s SQL is
  byte-for-byte unchanged — `ref.TeslaID()` produces the identical `int64` the query
  already bound.
- **Tests**: updated, not new. Every existing `VerifySession` call in
  `internal/charging`'s integration tests replaces its bare `teslaID` argument with a
  `Ref` built through a single new test helper, `refFor` (`design.md` D5). Expected
  values (returned `Session`, error shape, DB state) are unchanged from before this
  change — the helper wraps and immediately unwraps the same `int64`.
- **Docs**: `internal/charging/AGENTS.md` (Allowed Imports, and a new note on the
  verification port's compile-time guarantee) and the two knowledge-base guides that
  print `VerifySession`'s literal signature (`design.md` §Docs).

## Breaking?

**Yes, internally, and deliberately left broken for one tier.** This module exposes
no external API, so nothing outside the repository breaks. Inside the repository,
`internal/gateway/handlers/supercharger.go`'s one `VerifySession` call site
(`supercharger.go:681`) still passes a bare `selected.TeslaID` (an `int64`) after this
change — it will not compile. **This change does not fix that call site.** Wiring the
gateway to call `authorizeVehicle` and pass the resulting `Ref` is roadmap tier 4
(`RM57-gateway-authorize-supercharger-reads`), already scoped and not this worker's
sandbox. `go build ./...` stays red between this change landing and tier 4 landing —
expected, and the same shape tier 2 already left for `internal/app`/`internal/analytics`
until its own leader-owned wave closed the gap.

## Modules affected

- `internal/charging` — the only module this change edits. `SessionVerifier`'s
  signature, its implementation, its tests, and its own `AGENTS.md`.
- `internal/gateway` — **not touched by this change.** Its one call site breaks and
  stays broken until tier 4. Named here only so the break is expected, not discovered.

## Read paths affected

None. This change touches no schema, no index, no query, and no read port. The one
port it retypes is a write (`VerifySession` corrects two columns on one row); the
value it now requires unwraps to the exact `int64` the query already used, so the
query plan, the row scanned, and the return shape are all identical to before this
change.

## Non-goals

- `internal/gateway` wiring `authorizeVehicle` into `SuperchargerRowUpdate` —
  roadmap tier 4.
- Retyping `ListSessionsByVehicleBetween`, `ListSessionsByVehicleUpdatedSince`, or
  `ListSessionsByVehicle`. `internal/analytics` calls all three, and
  `make vehicleref-guard` allows `vehicleref.Authorize`/`.All` only inside the
  gateway's `authorizeVehicle` helper — `internal/analytics` has no legal way to
  build a `Ref` (roadmap "Facts already checked").
- Any migration, schema change, or `sqlc` regeneration — this change owns no table
  and touches no query.
- `charging.manual_charge_entries` — MAG-68.
- The conventions/spec/KB sweep beyond the two guides this change's own signature
  change invalidates — MAG-70 owns the rest.
