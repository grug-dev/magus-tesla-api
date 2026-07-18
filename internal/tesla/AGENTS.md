# internal/tesla — Module Brief

Agent-Name: tesla

## Doc-Pack (module)

Module-specific docs, additive to the base pack declared in the repo-root `CLAUDE.md`
(never replacing it):

- **Tesla Fleet API docs** — <https://developer.tesla.com/docs/fleet-api/getting-started/what-is-fleet-api>
  The authoritative reference for every endpoint, request/response shape, and authentication
  flow this adapter wraps. Consult it before adding any new Fleet API method or DTO.
- `ai/tesla-fleet-api-endpoints.md` — local inventory of the Fleet API data-read endpoints
  and which the adapter already implements (✅) vs. candidates (⬜). The "what to consume
  next" map; keep the ✅ column in sync with `vehicles.go`/`raw.go`.
- `README.md` (this module) — the human-facing adapter overview. It does **not** track endpoint
  implementation status itself; it points at `ai/tesla-fleet-api-endpoints.md` (above) as the
  single source of truth. See the **endpoint-status single-source rule** below.
- `docs/layer2-user-vehicle-access.md` — OAuth login, the two tokens, fetching vehicle data.
- `cmd/explore-tesla-api/README.md` — the exploration runnable this module's `raw.go` backs,
  including the adapter↔explorer sync rule.

## Responsibility

Anti-corruption adapter over the external Tesla Fleet API (`ai/architecture.md` §1.4, §6).
It translates Fleet API HTTP calls into typed Go values and nothing more. It is **stateless
about identity**: every method receives the `Credentials` to use — the adapter never reads
config, never stores tokens, never refreshes them (refresh is `internal/account`'s job).
One shared `*Client` instance safely serves all users.

## Public interface (the port)

Verified against the code — keep this section in sync when the port changes:

```go
type VehicleService interface {
    ListVehicles(ctx context.Context, creds Credentials) ([]VehicleTesla, error)
    VehicleData(ctx context.Context, creds Credentials, vehicleID int64) (*VehicleDataTesla, json.RawMessage, error)
    WakeUp(ctx context.Context, creds Credentials, vehicleID int64) (*VehicleTesla, error)
    ChargingHistory(ctx context.Context, creds Credentials, params ChargingHistoryParams) (*ChargingHistoryTesla, error)
}
```

Other public symbols: `Credentials{AccessToken}`, `NewClient() *Client`, sentinels
`ErrUnauthorized` (returned on HTTP 401 — detect with `errors.Is`) and `ErrForbidden`
(returned on HTTP 403, most commonly a missing-scope grant — detect with `errors.Is`; the
wrapped error carries Tesla's response body with the specific reason), and the `...Tesla`
DTOs (`VehicleTesla`, `VehicleDataTesla`, `ChargeStateTesla`, `ClimateStateTesla`,
`DriveStateTesla`, `VehicleStateTesla`) with their metric companion methods; plus the
charging history types `ChargingHistoryTesla`, `ChargingSessionTesla`, `ChargingFeeTesla`,
`ChargingInvoiceTesla`, and `ChargingHistoryParams`.

**Off the interface, deliberately:** the `Raw*` methods in `raw.go` (`ListVehiclesRaw`,
`VehicleDataRaw`, `WakeUpRaw`, `ChargingHistoryRaw`) exist only on the concrete `*Client`
for the `tesla-exploration` capability (`cmd/explore-tesla-api`). Domain code must never
call them.
Sync rule: every new typed Fleet API method in `vehicles.go` gets a `Raw*` sibling here
(reuse `get`/`post`, return `json.RawMessage`, stay off the interface) and explorer coverage
in `cmd/explore-tesla-api/main.go` + its README (`CLAUDE.md` §Tesla API Exploration).
**Reverse-order case:** `ChargingHistoryRaw` pre-existed the typed `ChargingHistory` method —
the raw sibling was already present when the typed method was added (RM2-tesla-add-charging-history),
so no new raw method was created. The sync rule is satisfied by the pre-existing `ChargingHistoryRaw`.

**Endpoint-status single-source rule:** implementation status for the Fleet API data-read
endpoints (which are ✅ implemented vs ⬜ candidate) is tracked in **exactly one** place —
`ai/tesla-fleet-api-endpoints.md`. Do **not** re-list wrapped endpoints anywhere else (the
module `README.md` only *references* that inventory, never copies it — a second list only
drifts). So the **definition of done for adding (or removing) a typed endpoint** is:
1. Add/remove the method on `vehicles.go` + the `VehicleService` interface (`client.go`), and
   update the port block above.
2. Add its `Raw*` sibling in `raw.go` + explorer coverage in `cmd/explore-tesla-api/main.go`
   (and that README) — unless the raw sibling already exists (reverse-order case above).
3. **Flip the endpoint's status to ✅ in `ai/tesla-fleet-api-endpoints.md`** (naming the typed
   method) — this is the ONLY status list, so a change that skips it leaves the repo lying about
   what's wrapped. Reviewers reject an endpoint change that leaves this inventory stale.
The README needs no per-endpoint edit — it points at the inventory, so it can't go stale.

## Imports — allowed and forbidden

- **Allowed:** Go standard library only (`context`, `encoding/json`, `errors`, `fmt`,
  `io`, `net/http`, `strings`, and stdlib test packages such as `net/http/httptest`).
- **Forbidden:** any other `internal/` module (this adapter is a leaf — it depends on
  nobody); `html/template`/Templ (no HTML outside the gateway); any DB driver (`pgx`,
  `sqlc` output); `os.Getenv` (env access lives in `internal/config` only).
- Other modules consume this one **only** through `VehicleService`; this module calls no
  other module.

## Data

None. No database, no tables, no persisted state, no cached tokens. Anything worth storing
(snapshots, attempts) belongs to the module that owns that data (e.g. `internal/telemetry`).

## DTO conventions

- Every struct mirroring Tesla JSON carries the `...Tesla` suffix and lives in `types.go`;
  these are the **only** structs that unmarshal Tesla JSON (`ai/architecture.md` §6). Never
  build domain logic directly on them.
- **Miles → km is mandatory** (`ai/go-conventions.md`): every miles/mph field gets a
  value-receiver companion `<Field>Km()` / `<Field>Kmh()` multiplying by the package
  `milesToKm` constant (1.609344). Never a JSON-tagged km field (km is derived, never
  unmarshalled). Pointer fields get nil-safe companions (nil in → nil out), e.g.
  `DriveStateTesla.SpeedKmh()`.
- Response envelopes (`listResponseTesla`, `dataResponseTesla`, `wakeResponseTesla`) stay
  unexported.

## Testing notes

- **The exploration capability is strictly test-free** (`CLAUDE.md`): no `_test.go` may
  cover `raw.go` or `cmd/explore-tesla-api/`, and no test anywhere may call a `Raw*` method
  or the explorer — those calls are paid and wake the car. Do not add such tests even when
  asked to "add tests" broadly.
- **Typed methods are tested offline** against a local `httptest.Server` serving canned
  Fleet API JSON (same-package tests point the client's unexported base URL at the fake).
  Tests never hit the live Fleet API and need no `.env`, no token, no DB.
- Pure conversion logic (the `Km()`/`Kmh()` companions) is unit-tested on canned values,
  including the nil-safe pointer path.
