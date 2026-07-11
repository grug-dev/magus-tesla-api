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

1. `GET /api/1/vehicles` — prints the full raw inventory, then selects the vehicle at `-i`
   (default `0`).
2. If that vehicle is not `online`, `POST .../wake_up` and poll the inventory every ~3s
   (up to ~60s) until it reports `online`.
3. `GET .../vehicle_data` — prints the full raw snapshot.

**Output:** progress messages go to **stderr**; the raw JSON payloads are pretty-printed to
**stdout**. To capture just the JSON:

```bash
go run ./cmd/explore-tesla-api > payloads.json
```

## Flags

| Flag | Default | Meaning |
|------|---------|---------|
| `-i` | `0`     | Index of the vehicle to explore, from the inventory list. |

## Keeping it in sync with the adapter

When a new Fleet API call is added to `internal/tesla` (a new typed method in `vehicles.go`),
add its **raw sibling** in `internal/tesla/raw.go` and surface it here so the explorer keeps full
coverage of the adapter. See the project `CLAUDE.md` (“tesla-exploration” notes) for the rule.

Note that the typed `VehicleData` method now returns the raw inner `vehicle_data` payload
alongside the decoded DTO, so production callers (e.g. the telemetry collector) get lossless
bytes without this tool. The explorer stays the way to inspect the **full transport envelope**
and any fields the typed DTOs omit — its `VehicleDataRaw` sibling is unchanged.
