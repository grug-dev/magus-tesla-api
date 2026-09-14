# RM57-gateway-authorize-supercharger-reads

> Source: MAG-67 — https://linear.app/magus-monitor/issue/MAG-67/4-re-key-the-supercharger-pair-on-tesla-id-reshape-the-mirror
> Tier 4 of `openspec/roadmaps/RM57-rekey-supercharger-pair-on-tesla-id.md`.
> Depends on tier 1 (`RM57-telemetry-rekey-supercharger-history-on-tesla-id`), tier 2
> (`RM57-charging-rekey-supercharger-sessions-on-tesla-id`), and tier 3
> (`RM57-charging-verifysession-takes-ref`), all archived.

## Why

Tier 3 retyped `charging.SessionVerifier.VerifySession` to require a
`vehicleref.Ref` instead of a bare `int64`. It left one call site broken on
purpose: `internal/gateway/handlers/supercharger.go:681` still passed
`selected.TeslaID`, an unproven `int64`, and `go build ./...` has been red
since. This change fixes that call site — not by patching the type back, but
by making the gateway do what `vehicleref.Ref` requires: prove the vehicle
belongs to the signed-in account before the write, using the seam MAG-66
built and tier 3 gave its first real port to require.

## What changes

- **`internal/gateway/handlers/supercharger.go`**: `SuperchargerRowUpdate`
  reads the selected vehicle from the session (`currentVehicle`) instead of
  `resolveSelectedVehicle`'s auto-select, proves it with `h.authorizeVehicle`,
  and passes the resulting `vehicleref.Ref` to `VerifySession`. A missing or
  unowned selection renders `HTTP 404` before any port call — the same status
  and message the handler already used for "no resolvable vehicle".
- **The two read sites stay on `resolveSelectedVehicle`** — one in
  `superchargerStatsViewFor` (page/fragment render), one in
  `fetchSuperchargerRowVM` (row resolve for GET/PATCH). Both only ever return
  a vehicle `RegisteredVehicles` already listed for this account, so ownership
  is already proven; adding `authorizeVehicle` there would cost a second
  account query per render for no new guarantee.
- **`internal/gateway/handlers/supercharger_test.go`**: `fakeSessionVerifier`
  retypes to match the interface; `superchargerRowEngine` gains two
  parameters to seed the session's selected vehicle, mirroring what a real
  page render already does before any `PATCH` is sent. Every test whose
  outcome depends on `VerifySession` being called is updated to seed that
  selection explicitly — see `design.md` for the full list and which tests
  needed no change.
- **Docs**: `internal/gateway/AGENTS.md` (the write-apertures table,
  `authorizeVehicle`'s "no caller yet" note) and five `kkpa/context/` guides
  that described the Supercharger write as having no ownership check — see
  `design.md` §Docs for the full list.

## Breaking?

**No, in the sense that matters here: it is the fix, not a new break.**
`go build ./...` has been red since tier 3 landed, expected and documented
there. This change is what turns it green again — the whole repo compiles
after this change, and that is this change's own point.

## Modules affected

- `internal/gateway` — the only module this change edits code in.
- No other module's Go code changes. `internal/charging`, `internal/account`,
  and `internal/vehicleref` are read through their existing public interfaces
  only.

## Read paths affected

None materially. The two read sites (`superchargerStatsViewFor`,
`fetchSuperchargerRowVM`) are unchanged — still one `resolveSelectedVehicle`
call each, same as before this change. The write path
(`SuperchargerRowUpdate`) gains one `RegisteredVehicles` query it did not
have before (via `authorizeVehicle`), replacing the `resolveSelectedVehicle`
query it used to make — a net-zero change in query count on that path, not an
addition. This does not touch the read-heavy hot path the platform's
Performance-Profile protects.

## Non-goals

- Any change to `internal/charging`, `internal/telemetry`, or
  `internal/analytics` — all three tiers before this one already finished
  their work.
- Adding `authorizeVehicle` to a read site. The roadmap's own tier-4 prompt
  and MAG-66's design explicitly reserve read sites for
  `resolveSelectedVehicle`; a read already proves ownership through
  `RegisteredVehicles` filtering.
- `charging.manual_charge_entries` — MAG-68.
- The rest of the conventions/spec/KB sweep beyond what this change's own
  edit invalidates — MAG-70 owns the rest.
