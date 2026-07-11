## Context

`internal/tesla` is the anti-corruption adapter over the Tesla Fleet API
(`ai/architecture.md` §1.4, §6): stateless about identity, credentials passed per call,
vendor-shaped `...Tesla` DTOs. Its port is `VehicleService` with three methods —
`ListVehicles`, `VehicleData`, `WakeUp` — implemented by `*Client` over a shared private
`do(ctx, method, creds, path, out)` that handles auth, 401→`ErrUnauthorized`, and JSON
decoding. Exploration siblings (`ListVehiclesRaw`, `VehicleDataRaw`, `WakeUpRaw`) live in
`raw.go`, off the interface, and are surfaced by `cmd/explore-tesla-api`.

The nightly-vehicle-telemetry roadmap (Decisions, 2026-07-10) needs the adapter to hand a
future `internal/telemetry` module, in one paid call, both a lossless raw `vehicle_data`
payload (for JSONB storage) and the typed fields it extracts into columns. Today
`VehicleData` returns only the decoded DTO, and the DTO lacks charge limit and sentry mode.

Notable current facts verified in code:
- `VehicleData` and `WakeUp` have **no production callers**; the only other implementer of
  `VehicleService` is the gateway's test fake (`internal/gateway/handlers/handlers_test.go`).
- `internal/tesla` has **no `_test.go` files at all** yet; `baseURL` is a package const, so
  the client cannot currently be pointed at a test server.

## Goals / Non-Goals

**Goals:**
- One Fleet API request yields BOTH the parsed `VehicleDataTesla` and the raw payload.
- Raw payload is byte-lossless: exactly what Tesla sent for the vehicle data object.
- DTO carries every typed column the roadmap Decisions list (adds charge limit, sentry mode).
- Keep the wake command a single fire-and-return call (resulting vehicle state only).
- Establish an offline (httptest) test pattern for the module's typed methods.
- Keep the exploration capability in sync and strictly test-free.

**Non-Goals:**
- No wake/poll/retry orchestration in the adapter — that is telemetry-module logic (tier 3).
- No storage, scheduler, or per-vehicle failure policy (tier 3).
- No account/token logic — refresh stays in `internal/account`.
- No change to `ListVehicles` or to the exploration (`Raw*`) behavior.
- No new Fleet API endpoints (both endpoints already exist on the adapter).

## Decisions

**1. Expose the raw payload as an additional return value on the existing method:**
`VehicleData(ctx context.Context, creds Credentials, vehicleID int64) (*VehicleDataTesla, json.RawMessage, error)`.
This is a breaking interface change, chosen because the method has zero production callers —
the blast radius is one mechanical line in the gateway's test fake.
_Alternatives rejected:_
- A `Raw json.RawMessage \`json:"-"\`` field on `VehicleDataTesla` — non-breaking, but it
  pollutes a vendor-shaped DTO with a synthetic non-JSON field; `...Tesla` structs must
  mirror Tesla's JSON and nothing else (`ai/architecture.md` §6).
- A second interface method `VehicleDataWithRaw` — keeps a redundant method nobody calls,
  bloats the port, and invites accidental double (paid) fetches by callers needing both.

**2. Single fetch, two artifacts — decode once from the same bytes.** The implementation
decodes the HTTP body into an envelope whose `response` field is captured as
`json.RawMessage`, then unmarshals `VehicleDataTesla` from those same bytes. This
guarantees (a) exactly one Fleet API request and (b) the DTO and the raw payload can never
disagree, because the DTO is parsed *from* the returned raw bytes. (The existing
`dataResponseTesla` envelope in `types.go` changes its `Response` field to
`json.RawMessage` to support this.)

**3. The raw payload is the inner `response` object** (the `vehicle_data` document itself),
not the transport envelope `{"response": ...}`. The envelope is Fleet API plumbing with no
telemetry value; the inner object is what telemetry stores in JSONB. Tooling that wants the
full envelope already has `VehicleDataRaw` (exploration capability).

**4. Vehicle identifier stays `int64`** — the Fleet API numeric `id`, identical to the
existing `ListVehicles`/`VehicleData`/`WakeUp` parameter. No new identifier type is
introduced; mapping VINs/registry IDs to this `int64` is the callers' concern (account
module registry, tier 2).

**5. `WakeUp` is already correct — keep name and shape.** The roadmap tier says
"WakeVehicle", but the adapter has had `WakeUp(ctx, creds, vehicleID) (*VehicleTesla, error)`
returning the vehicle's reported state since the port was created; renaming would be churn
with no behavioral gain. The delta spec keeps the existing "Vehicle Wake" requirement
untouched.

**6. New DTO fields need no metric companions.** `ChargeLimitSoc int` (percent) and
`SentryMode *bool` are not miles/mph values, so the mandatory `Km()`/`Kmh()` rule does not
apply. All four existing miles/mph fields already have companions (`BatteryRangeKm`,
`ChargeRateKmh`, `SpeedKmh` nil-safe, `OdometerKm`); a task re-audits this on the enriched
DTO.

**6a. `SentryMode` is `*bool`, not `bool` (leader triage override, 2026-07-10).** The
initial draft used a value `bool`, accepting Tesla's absence as `false`. The pipeline
leader overrode this: a pointer keeps an absent field (a vehicle that does not report
sentry) distinguishable from a reported-off sentry — `nil` = not reported, `*false` = off,
`*true` = on. This matches the existing nil-safe pointer convention in the same package
(`DriveStateTesla.Speed`) and the platform principle of never discarding a distinction the
source made (AGENTS.md). The raw JSONB still holds the ground truth regardless; the pointer
just avoids baking a lossy coercion into the typed column.

**7. Testability via an unexported `baseURL` field on `Client`.** `NewClient()` keeps its
signature and defaults the field to the production const; same-package tests construct a
client pointing at an `httptest.Server` that serves canned Fleet API JSON. Public API
unchanged, zero live calls in tests. Only the typed methods get tests — `raw.go` and
`cmd/explore-tesla-api` remain test-free per `CLAUDE.md`.

## Risks / Trade-offs

- **Breaking port signature** → one-line fix in the gateway test fake; coordinated by the
  pipeline leader since it crosses the tesla worker's sandbox. Accepted: cheapest moment ever
  to break this method (no production callers yet; tier 3 adds the first real one).
- **`sentry_mode` may be absent from some vehicles/payloads** → modeled as `*bool`, so
  absent stays `nil` rather than collapsing to `false` (Decision 6a). Telemetry keeps the
  raw JSONB too, so ground truth is preserved either way; the pointer just keeps the typed
  column honest for consumers that never touch the raw payload.
- **Raw payload size** (tens of KB per snapshot) → a storage concern owned by tier 3's
  schema, not the adapter; the adapter just hands bytes through.
- **Envelope type change (`dataResponseTesla.Response` → `json.RawMessage`)** is internal to
  the package (unexported), so it cannot affect other modules.
