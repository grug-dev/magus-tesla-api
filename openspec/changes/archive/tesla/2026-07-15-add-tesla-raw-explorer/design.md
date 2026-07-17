## Context

The `internal/tesla` adapter (`ai/architecture.md` §6) is an anti-corruption layer: it exposes a
typed `VehicleService` interface backed by `...Tesla`-suffixed DTOs that model only the fields the
app currently uses. The Fleet API actually returns far more per endpoint. To learn the API we want
to inspect the **complete** payload, on demand, without paying for those calls on every `go test`.

The adapter already has all the machinery: a private
`do(ctx, method, creds, path, out any)` that performs the authenticated request and decodes the
body into `out`, plus `get`/`post` helpers, endpoint paths in `vehicles.go`, `Credentials`, and a
distinct `ErrUnauthorized` on HTTP 401. `config.Load()` already reads `TESLA_ACCESS_TOKEN` into
`cfg.AccessToken`.

## Goals / Non-Goals

**Goals:**
- Expose each Fleet API endpoint's raw, undecoded JSON body from the tesla adapter.
- Provide an on-demand `cmd/explore-tesla-api` runnable that prints the full payloads.
- Keep the paid API calls out of the test suite entirely.
- Leave the typed `VehicleService` contract and all existing callers untouched.

**Non-Goals:**
- No change to the typed DTOs or `VehicleService`.
- No DB, no token refresh (uses the `.env` token; re-run `cmd/setup` when stale).
- No redaction/transformation of the output — the point is to see the real response.
- No new spec requirement for wake ordering (already covered by the `tesla` capability's
  existing "Wake-Before-Data Ordering" requirement; the explorer simply follows it).

## Decisions

**1. Capture raw bytes by passing `*json.RawMessage` to the existing `do`.**
`json.RawMessage` implements `json.Unmarshaler` by copying the raw bytes, so
`do(ctx, method, creds, path, &raw)` yields the undecoded body with zero new HTTP code. All
network + auth + 401 handling stays inside the adapter (boundary rule).
_Alternative rejected:_ a new low-level `RawCall(method, path)` exposing arbitrary paths — looser
and pushes endpoint knowledge into `cmd/`. Sibling methods keep paths owned by the adapter.

**2. Raw methods live in a new `internal/tesla/raw.go` as methods on `*Client`, NOT on
`VehicleService`.** Domain code depends on the interface and never sees the raw methods; only the
concrete `*tesla.Client` (which `cmd/explore-tesla-api` constructs directly) exposes them. This is
the "contract isolation" guarantee in the spec.
_Alternative rejected:_ adding them to `VehicleService` — would leak exploration surface into every
consumer and the gateway's fakes.

**3. `cmd/explore-tesla-api` is a thin `package main` with no `_test.go`.** It wires
config → adapter → stdout and holds no domain logic. Flow: load `.env` token → `ListVehiclesRaw`
(print; locally unmarshal a tiny `{Response:[{id,state,display_name}]}` struct just to pick the
target vehicle, avoiding a second list call) → if not online, `WakeUpRaw` then poll `ListVehiclesRaw`
(~3s interval, ~60s cap) until online → `VehicleDataRaw` (print). Progress logs to stderr, JSON to
stdout via `json.Indent`. An optional `-i` index flag selects among multiple vehicles.
_Rationale:_ `package main` with no tests is never compiled by `go test ./...`, so the paid call
fires only on explicit `go run ./cmd/explore-tesla-api`.

**4. Reuse `ErrUnauthorized` for expired-token UX.** Because raw methods route through `do`, a 401
already yields `tesla.ErrUnauthorized`; the command checks `errors.Is` and prints a "re-run
`go run ./cmd/setup`" hint with a non-zero exit. This is the spec's "preserve unauthorized signal"
guarantee — no new error type.

## Risks / Trade-offs

- **New capability `tesla-exploration` lives in `internal/tesla`, not its own module** → bends the
  "one capability per module" convention. Mitigation: it is explicitly tooling-facing and additive;
  the proposal states the exception, and the typed `tesla` contract is untouched.
- **Endpoint path duplication** between `vehicles.go` and `raw.go` (3 short strings) → Mitigation:
  optionally lift the three paths into shared `const`s used by both files; low value, acceptable
  either way.
- **Stale `.env` token** → the run fails fast with a clear `ErrUnauthorized`-driven hint to re-run
  `cmd/setup`; no silent failure.
- **Every poll iteration is a real list call** → inherent to waking a car; capped at ~60s to bound
  cost.
