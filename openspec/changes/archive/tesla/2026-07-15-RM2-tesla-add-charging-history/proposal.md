## Why

Tier 1(b) of the `RM2-charging-stats` roadmap (`openspec/roadmaps/RM2-charging-stats.md` —
Decisions section is binding). The live `GET /api/1/dx/charging/history` response has been
captured (2026-07-15) via `ChargingHistoryRaw` + `cmd/explore-tesla-api`, unblocking the
typed DTO and pagination-aware method. The raw side (`ChargingHistoryRaw` in `raw.go` +
explorer surface) is **already implemented and complete**; this change adds only the typed
layer on top.

Tier 2 (`RM2-telemetry-add-charging-stats`) will store one row per session in a
`charge_sessions` table and call `tesla.ChargingHistory` nightly (no wake). It cannot start
until this change ships — it depends on the typed `VehicleService.ChargingHistory` method.

## What Changes

- **`VehicleService.ChargingHistory` is added to the port** — new method
  `ChargingHistory(ctx, creds, params) (*ChargingHistoryTesla, error)` on
  `internal/tesla/client.go`. The method is pagination-aware: it accepts optional filter
  params (date range; page control) and fetches the complete history set by iterating pages
  automatically (see design.md — pagination approach flagged for leader review).
- **`ChargingHistoryTesla` DTO added to `internal/tesla/types.go`** — typed representation
  of the full `dx/charging/history` response: top-level `Data []ChargingSessionTesla` +
  `TotalResults int`. Each session types every field of the live-captured payload including
  `Fees []ChargingFeeTesla` and `Invoices []ChargingInvoiceTesla`.
- **No new `Raw*` sibling** — `ChargingHistoryRaw` in `raw.go` already exists and is already
  surfaced in `cmd/explore-tesla-api`. The raw side is complete; no new raw method is added.
- **`ai/tesla-fleet-api-endpoints.md`** — the `/api/1/dx/charging/history` row is updated from
  `⬜ typed · ChargingHistoryRaw explorer-reachable` to `✅ ChargingHistory` (typed + raw,
  both implemented).
- **`internal/tesla/AGENTS.md`** — "Public interface" section updated to include
  `ChargingHistory`.

## Capabilities

### New Capabilities

- `tesla`: **Charging History** — the adapter can now return a fully typed, paginated charging
  session history (Supercharger / Tesla-billed DC fast charging only; home and third-party AC
  sessions are not available from this endpoint — see Scope note below).

### Modified Capabilities

None — the existing `VehicleService` methods (`ListVehicles`, `VehicleData`, `WakeUp`) are
unchanged.

## Scope note — Supercharger / Tesla-billed sessions only

`GET /api/1/dx/charging/history` is Tesla's **billing** record system, not a universal
charging-event log. It surfaces **only** sessions that Tesla invoiced directly
(Supercharger + Tesla-operated DC fast charging). Home charging (Wall Connector, Mobile
Connector), destination L2 chargers, and any non-Tesla-billed third-party sessions are
**invisible** to this endpoint and will never appear in the returned data. The live-captured
response confirms this: every session has a `billingType` field (`"IMMEDIATE"`) and a `fees`
array with Tesla-issued invoice documents. Callers and downstream modules must not assume
this represents complete charging history.

## Impact

- **Breaking: NO.** `ChargingHistory` is a new method added to the `VehicleService`
  interface. Adding a method to an interface IS a breaking change in Go if there are external
  implementers — however, the only implementer outside `internal/tesla` is the gateway's test
  fake (`internal/gateway/handlers/handlers_test.go`). That fake must add a stub for
  `ChargingHistory` to keep compiling. This is a mechanical one-line addition (returning
  `nil, nil`); no production gateway code calls this method. The leader coordinates the
  cross-module compile fix.
- **Modules affected:**
  - `tesla` (owner) — `client.go`, `vehicles.go`, `types.go`. No `raw.go` change.
  - `gateway` (compile-only) — test fake `fakeTesla` needs a stub `ChargingHistory` method.
    Leader-integrated (outside the tesla sandbox).
  - `ai/tesla-fleet-api-endpoints.md` — endpoint row status update (granted path).
- **No DB, no migrations, no new dependencies, no config changes.** The adapter remains
  stateless about identity. The call is account-scoped (no vehicle id) and server-side (no
  wake required).
- **Performance-Profile:** this call runs on the nightly poller / Tier 2 backfill, not on
  any user-facing hot read path. Read-path impact = **none (write-side collection only)**.
  Pagination is transparent to the caller (the method iterates internally), so Tier 2 sees
  a single call returning the full history slice.
- **Required scope:** `vehicle_charging_cmds` — this scope is already in the OAuth flow
  (`internal/auth/oauth.go`) and confirmed active on the current tokens (the live-captured
  response succeeded with it). No scope change needed.
