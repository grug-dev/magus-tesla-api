# Tesla Fleet API — Data Endpoint Inventory — magus-tesla-api

A local, at-a-glance map of the Tesla Fleet API endpoints that **consume vehicle data**,
and which of them this repo's `tesla` adapter has wired up. Use it to answer "what can we
pull from Tesla?" and "what's left to build?" without re-scanning Tesla's docs each time.

- **Base URL:** `https://fleet-api.prd.na.vn.cloud.tesla.com`
- **Auth:** every call sends `Authorization: Bearer <access token>` (per-user; see
  [`architecture.md`](./architecture.md) §5 and the project `CLAUDE.md` §Token Behavior).
- **Scope of this doc:** data **reads** only. The Fleet API also has a large (~40-endpoint)
  **vehicle-commands** category (lock/unlock, climate, charging control, honk, …) that
  *acts on* the car — those are intentionally **excluded** here.
- **Cost warning:** Tesla warns that regularly polling `vehicle_data` is expensive and
  wakes the car. For continuous data they recommend **Fleet Telemetry** streaming (bottom
  of this doc) over polling.

> **Source of truth:** Tesla's live docs at
> <https://developer.tesla.com/docs/fleet-api/endpoints/vehicle-endpoints> (and the
> sibling charging / energy / user endpoint pages). This inventory was verified against
> those docs; treat Tesla's pages as authoritative if they ever diverge.

---

## Legend

- ✅ **Implemented** — wired into the adapter (`internal/tesla/`) as a typed method with a
  `Raw*` sibling and explorer coverage.
- ⬜ **Candidate** — documented by Tesla, not yet in this repo. Fair game for "what to
  build next."

---

## Vehicle endpoints

| Endpoint | Method | Returns | Status |
|---|---|---|---|
| `/api/1/vehicles` | GET | All vehicles on the account (paginated, 100/page) | ✅ `ListVehicles` |
| `/api/1/vehicles/{id}` | GET | Single vehicle summary (state, VIN, display name) | ⬜ |
| `/api/1/vehicles/{vin}/vehicle_data` | GET | Full realtime snapshot — battery, charge, climate, drive/GPS, vehicle state, config | ✅ `VehicleData` |
| `/api/1/vehicles/{vin}/wake_up` | POST | Wakes a sleeping car (prerequisite to `vehicle_data`; not data itself) | ✅ `WakeUp` |
| `/api/1/vehicles/{vin}/nearby_charging_sites` | GET | Superchargers / destination chargers near the car | ⬜ |
| `/api/1/vehicles/{vin}/service_data` | GET | Service status of the vehicle | ⬜ |
| `/api/1/vehicles/{vin}/recent_alerts` | GET | Recent vehicle alerts | ⬜ |
| `/api/1/vehicles/{vin}/release_notes` | GET | Software release notes | ⬜ |
| `/api/1/vehicles/{vin}/options` | GET | Factory option / config codes | ⬜ |
| `/api/1/vehicles/{vin}/eligible_subscriptions` | GET | Subscriptions the car can buy (e.g. Premium Connectivity) | ⬜ |
| `/api/1/vehicles/{vin}/eligible_upgrades` | GET | Upgrades available for purchase | ⬜ |
| `/api/1/vehicles/{vin}/mobile_enabled` | GET | Whether mobile access is enabled | ⬜ |
| `/api/1/vehicles/{vin}/drivers` | GET | Authorized drivers on the vehicle | ⬜ |
| `/api/1/vehicles/{vin}/fleet_telemetry_config` | GET | Current streaming-telemetry config | ⬜ |
| `/api/1/vehicles/fleet_status` | POST | Online/asleep + command-capability for a set of VINs (read, but POST — see notes) | ⬜ |

**`vehicle_data` note:** on firmware **2023.38+**, GPS/location fields require the query
param `?location_data=true`, which also shows a location-sharing icon on the car's UI.

---

## Charging endpoints

| Endpoint | Method | Returns | Status |
|---|---|---|---|
| `/api/1/dx/charging/history` | GET | **Tesla-billed charging sessions only** — Supercharger + DC fast-charging that Tesla invoiced (has `fees`, `invoices`, `billingType`). Home/Wall Connector charging is unbilled and **never appears here** — see "Home charging" note below. | ✅ `ChargingHistory` (typed on `VehicleService`); `ChargingHistoryRaw` remains available for exploration |
| `/api/1/dx/charging/sessions` | GET | Session pricing + energy — **business fleet accounts only** (still only Tesla-billed sessions) | ⬜ |
| `/api/1/dx/warranty/details` | GET | Warranty information | ⬜ |

> **Home charging is NOT available from any `/dx/charging/*` endpoint.** Those are Tesla's
> *billing* records, not a charging-event log. Home, destination-L2, Mobile Connector, and
> any non-Tesla-billed session are invisible there. The Tesla app reconstructs the full
> charging picture (including home) from **two** sources:
>
> 1. **Billing history** (`/api/1/dx/charging/history`) — paid sessions only.
> 2. **Vehicle telemetry** (`/api/1/vehicles/{vin}/vehicle_data` → `charge_state`) — every
>    charge event, regardless of charger. A session is derived from `charging_state`
>    transitions (`Disconnected → Charging` start, `→ Complete/Disconnected` end) plus
>    `charge_energy_added` (or `battery_level` delta × usable capacity); "home" is inferred
>    from location at the time. This platform already polls `vehicle_data` for nightly
>    snapshots — extending collection to capture `charge_state` transitions during a charge
>    is the way to derive home/destination sessions. Aligned with `AGENTS.md` §Polling
>    Strategy and §Metrics.
>
> **One shortcut:** if the user owns a **Tesla Wall Connector** registered to their account,
> `/api/1/energy_sites/{energy_site_id}/telemetry_history?kind=charge&start_date=...&end_date=...&time_zone=...`
> (see Energy endpoints) returns per-session watt-hours delivered by the Wall Connector
> directly — no derivation needed. Requires `GET /api/1/products` first to enumerate
> `energy_site_id`s, and only covers Tesla-branded Wall Connectors, not third-party EVSEs.

---

## User / account endpoints

| Endpoint | Method | Returns | Status |
|---|---|---|---|
| `/api/1/users/me` | GET | Account profile | ⬜ |
| `/api/1/users/region` | GET | Which regional API host to call | ⬜ |
| `/api/1/users/orders` | GET | Order history | ⬜ |
| `/api/1/users/feature_config` | GET | Feature flags for the account | ⬜ |

---

## Energy endpoints (only if the account has Powerwall / Solar)

| Endpoint | Method | Returns | Status |
|---|---|---|---|
| `/api/1/products` | GET | All products — vehicles **and** energy sites | ⬜ |
| `/api/1/energy_sites/{site_id}/site_info` | GET | Site configuration | ⬜ |
| `/api/1/energy_sites/{site_id}/live_status` | GET | Live power flow / battery % | ⬜ |
| `/api/1/energy_sites/{site_id}/history` | GET | Energy/power/self-consumption over time | ⬜ |
| `/api/1/energy_sites/{site_id}/calendar_history` | GET | Calendar-bucketed history | ⬜ |

---

## Streaming — Fleet Telemetry (not REST polling)

Tesla's **push stream** of vehicle data, and their recommended alternative to polling
`vehicle_data`. The car streams configured fields directly to a server you run.

- Configured per-vehicle via `fleet_telemetry_config` (see the vehicle table).
- Reference server: [`teslamotors/fleet-telemetry`](https://github.com/teslamotors/fleet-telemetry).
- Not a single GET endpoint — it's a streaming transport, so it doesn't fit the tables above.

---

## Accuracy / gotchas

- **`fleet_status` is a POST** that takes a body of VINs even though it reads status — don't
  assume every "read" is a GET.
- **`vehicle_data` location** needs `?location_data=true` on firmware 2023.38+ (see above).
- **Account-gated endpoints:** charging `sessions` (business fleet) and all `energy_sites`
  endpoints (requires owned energy products) return nothing/403 for accounts without them.
- **Scopes:** everything here is reachable under the app's current `vehicle_device_data`
  scope for the vehicle/user reads; energy and charging endpoints may require their own
  scopes — confirm against Tesla's docs before implementing.
- **Commands excluded:** the ~40 vehicle-command endpoints are a separate category and are
  not in this inventory.

---

## Repo sync rule — adding any candidate

When you implement one of the ⬜ endpoints, the project's standing rule (see the root
[`CLAUDE.md`](../CLAUDE.md) §Tesla API Exploration and
[`internal/tesla/AGENTS.md`](../internal/tesla/AGENTS.md)) requires **all three**:

1. A typed method in `internal/tesla/vehicles.go` (returns `...Tesla` DTOs; miles→km
   companions where applicable per [`go-conventions.md`](./go-conventions.md)).
2. A `Raw*` sibling in `internal/tesla/raw.go` (reuse `get`/`post`, return
   `json.RawMessage`, stay **off** the `VehicleService` interface).
3. Explorer coverage in `cmd/explore-tesla-api/main.go` (and its README).

The `tesla-exploration` capability (`raw.go` + `cmd/explore-tesla-api/`) stays **test-free** —
its calls hit the live, paid Fleet API and wake the car.
