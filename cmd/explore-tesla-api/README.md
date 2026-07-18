# explore-tesla-api

On-demand tool for inspecting the **raw JSON** the Tesla Fleet API returns — including every
field the typed `...Tesla` DTOs in `internal/tesla` deliberately omit. Use it to learn the API
and decide what to model next.

This is the runnable side of the `tesla-exploration` capability. The raw adapter methods it
calls live in [`internal/tesla/raw.go`](../../internal/tesla/raw.go).

## ⚠️ Costs real money and wakes the car

Every run makes real, **paid** Fleet API calls and will **wake a sleeping vehicle**. That is why
this lives in its own `cmd/` program and **not** in any test: it is `package main` with no
`_test.go`, so `go test ./...` never compiles or runs it. A Tesla call fires only when you run
this explicitly.

## Prerequisites

- A valid `TESLA_ACCESS_TOKEN` **and** `TESLA_REFRESH_TOKEN` in `.env`. Access tokens last ~8
  hours, but the run **refreshes automatically**: on a `401` it uses `TESLA_REFRESH_TOKEN` to mint
  a new access token, saves the rotated pair back to `.env`, and retries — no manual step needed.

  You only need to re-run the one-time OAuth flow if the **refresh token** itself is dead
  (~3-month lifetime, single-use), in which case the run fails with a hint to:

  ```
  go run ./cmd/setup
  ```

  (`cmd/setup` runs the OAuth flow and writes fresh tokens back into `.env`. **Stop the web
  server first** — `cmd/web` and `cmd/setup` both bind `:8080` and share the
  `/connect/tesla/callback` path, so a running web server will intercept the OAuth callback and
  setup's `.env` write will never happen.)

- **Energy scope for steps 3–4 (`/products` energy items + Wall Connector history):** those
  need the **`energy_device_data`** scope, which was added to `internal/auth/oauth.go` but is
  **not** on tokens minted before it. To get energy products you must: (1) enable
  `energy_device_data` for the app in the **Tesla developer portal**, then (2) re-run
  `go run ./cmd/setup` (or `make cmd-setup`) to re-consent and mint a token that carries it
  (`prompt_missing_scopes=true` makes Tesla prompt for the newly-added scope). Until then, step 3
  returns a vehicles-only list (or `403`) and step 4 is skipped. Steps 1–2 work with the current
  token.

## Running it

```bash
# Explore the first vehicle on the account
go run ./cmd/explore-tesla-api

# Explore a specific vehicle by inventory index (0-based)
go run ./cmd/explore-tesla-api -i 1

# Or via the Makefile
make explore-tesla-api
```

## What it does

Each API call's raw response is written to its **own JSON file** under the output
directory (`-out`, default `cmd/explore-tesla-api/output/`), overwritten each run — see
[Output](#output). The steps below refer to that per-call file.

1. `GET /api/1/vehicles` — captures the full raw inventory, then selects the vehicle at `-i`
   (default `0`).
2. `GET /api/1/dx/charging/history` — prints the full raw charging-history payload. This
   call is **account-level** (no vehicle id required) and **does not need the vehicle to be
   awake** — it runs before the wake step so it is always captured regardless of vehicle state.
   (Supercharger / Tesla-billed sessions only — no home charging.)
3. `GET /api/1/products` — prints the account's product list: vehicles **and** energy products
   (Powerwall / solar / **Tesla Wall Connectors**). Server-side, no wake. Energy products only
   appear when the token carries the **`energy_device_data`** scope (see Prerequisites) — without
   it Tesla returns `403` or a vehicles-only list.
4. `GET /api/1/energy_sites/{energy_site_id}/telemetry_history?kind=charge` — **only if this
   account owns an energy site** (an `energy_site_id` was found in step 3) — prints the **Tesla
   Wall Connector** charging history: energy in **watt-hours** as a `time`/`value` series, over
   the window `-months` back → today in `-tz`. ⚠️ This is **connector-level** energy for a Wall
   Connector **on this account** — there is **no VIN**, and a charge at *someone else's* Wall
   Connector (e.g. a friend's) lives on **their** account and will **not** appear here. If the
   account owns no energy site, this step is skipped with a note.
5. If that vehicle is not `online`, `POST .../wake_up` and poll the inventory every ~3s
   (up to ~60s) until it reports `online`. *(Steps 1–4 are all no-wake, so you can `Ctrl-C`
   after them if you only wanted the energy/charging data and don't want to wake the car.)*
6. `GET .../vehicle_data` — prints the full raw snapshot.

## Output

Progress messages (the `→ GET …` request lines and a `→ wrote …` note per call) go to
**stderr**. The output dir is **wiped at the start of every run**, then each call's raw
response is written to its **own file** under `-out` (default
`cmd/explore-tesla-api/output/`) — one file per call, named `<order>-<Call>.json`:

```
output/
├── 1-ListVehicles.json
├── 2-ChargingHistory.json
├── 3-Products.json
├── 4-EnergyChargeHistory.json   # only if the account owns an energy site
├── 5-WakeUp.json                # only if the car was asleep
└── 6-VehicleData.json
```

The `<order>-` prefix makes the directory listing sort in the sequence the calls ran.

Each file is a self-describing envelope — JSON has no comments, so it records **which**
call produced the body and **where** it fell in the run's execution sequence. The three
metadata keys come first, then **Tesla's real response body is spliced in at the top
level** — its own keys (`response`, `data`, `count`, …) verbatim, with **no** extra
wrapper key:

```json
{
  "order": 1,
  "name": "ListVehicles",
  "endpoint": "GET /api/1/vehicles",
  "response": [
    { "id": 3744327027802250, "vin": "…" }
  ],
  "count": 1
}
```

```json
{
  "order": 2,
  "name": "ChargingHistory",
  "endpoint": "GET /api/1/dx/charging/history",
  "data": [
    { "sessionId": 702772122, "vin": "…", "siteLocationName": "Medellín, Colombia" }
  ]
}
```

So the body shown is exactly what Tesla returned — no `"response": { "response": … }`
double-wrapping. Key order and exact numbers (large Tesla ids) are preserved verbatim.

`order` is the runtime call position (1 = first call made this run, 2 = second, …), so
skipped conditional calls (Energy / WakeUp) simply don't consume a number.

> **Not committed.** `cmd/explore-tesla-api/output/` is git-ignored: the payloads carry
> sensitive data (VIN, GPS lat/lng, `energy_site_id`, Wall Connector DIN/serial).

## Flags

| Flag | Default | Meaning |
|------|---------|---------|
| `-i` | `0`     | Index of the vehicle to explore, from the inventory list. |
| `-tz` | `America/Bogota` | IANA time zone for the Wall Connector charge-history window (step 4). |
| `-months` | `6` | How many months back to query Wall Connector charge history (step 4 window = now − months → now). |
| `-out` | `cmd/explore-tesla-api/output` | Directory where each call's JSON file is written (one file per call, overwritten each run). |

## Keeping it in sync with the adapter

When a new Fleet API call is added to `internal/tesla` (a new typed method in `vehicles.go`),
add its **raw sibling** in `internal/tesla/raw.go` and surface it here so the explorer keeps full
coverage of the adapter. See the project `CLAUDE.md` (“tesla-exploration” notes) for the rule.

Note that the typed `VehicleData` method now returns the raw inner `vehicle_data` payload
alongside the decoded DTO, so production callers (e.g. the telemetry collector) get lossless
bytes without this tool. The explorer stays the way to inspect the **full transport envelope**
and any fields the typed DTOs omit — its `VehicleDataRaw` sibling is unchanged.
