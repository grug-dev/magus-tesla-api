# Go Conventions — magus-tesla-api

Authoritative Go coding conventions for this project. Any AI assistant (Claude Code,
OpenCode, Cursor, …) or human **must read and follow this file before writing or
editing Go code.** Referenced from `CLAUDE.md`. For the module structure and boundary
rules these conventions operate within, see [`architecture.md`](./architecture.md).

---

## Architecture

Standard Go project layout — modular monolith:

- `cmd/` — thin executable entry points, one per concern. No business logic here.
- `internal/` — private packages. Each package owns exactly one concern.

### Current packages

| Package | Path | Responsibility |
|---|---|---|
| `config` | `internal/config/` | Load `.env`, expose typed config, save tokens back to `.env` |
| `auth` | `internal/auth/` | Tesla OAuth URL builder, authorization code exchange, token refresh |
| `server` | `internal/server/` | Gin HTTP server that catches the OAuth redirect on `:8080/callback` |
| `vehicle` | `internal/vehicle/` | Authenticated Fleet API client + all vehicle data types and calls |

### Current commands

| Command | Path | What it does |
|---|---|---|
| `setup` | `cmd/setup/` | One-time OAuth flow — opens browser, catches callback, saves tokens to `.env` |
| `magus` | `cmd/magus/` | Fetches and prints Magus's live vehicle snapshot. Auto-refreshes token on 401. |

---

## Coding Rules

- Every new Tesla API concern gets its own package under `internal/`. Never add charging commands to the vehicle package, never add telemetry to auth, etc.
- `cmd/` files must stay thin — they wire packages together. Zero business logic in `cmd/`.
- Never call `os.Getenv` outside of `internal/config/`. All other packages receive config via function arguments or the `Config` struct.
- All new Fleet API endpoint calls belong in `internal/vehicle/` or a new `internal/<domain>/` package (e.g. `internal/charging/`, `internal/telemetry/`).
- The user wants **modular packages** as a hard requirement — enforce this on every suggestion.
- **Interface-first module contract.** Every module's mandatory, always-present public API is a **Go interface** (its "port") — this is how the gateway and sibling modules call it (in-process, no HTTP). An HTTP `/api/{version}` JSON endpoint is a secondary, optional adapter added per module only when a real external consumer exists (see [`architecture.md`](./architecture.md) §3).
- **Vendor DTOs carry a service-name suffix.** Any struct that mirrors an external service's JSON ends in that service's name (`...Tesla`); our own domain models never carry a vendor suffix. Full rule + rationale in [`architecture.md`](./architecture.md) §6.
- **Miles → km conversion is mandatory.** Every struct field expressed in miles (or a miles-derived unit like mph) **must** have a companion value-receiver method that returns the metric equivalent, following the `OdometerKm()` pattern:
  - Name it `<Field>Km` for distances and `<Field>Kmh` for speeds/rates (e.g. `BatteryRangeKm()`, `ChargeRateKmh()`, `SpeedKmh()`).
  - Multiply by the package-level `milesToKm` constant (`1.609344`) — never hardcode the factor inline.
  - **Never** add the km value as a JSON-tagged struct field: the Fleet API only sends miles, so km is always **derived**, not unmarshalled.
  - For pointer fields (e.g. `*float64` speed), the method returns a nil-safe pointer (`nil` in → `nil` out).

---

## Error-handling pattern (401 / auto-refresh)

- `vehicle.ErrUnauthorized` is the sentinel error returned by the client on 401 — use `errors.Is()` to detect it in any future command that needs the same pattern.
- **Auto-refresh is implemented in `cmd/magus`**: on HTTP 401 from the Fleet API, it calls `auth.RefreshTokens()`, saves both new tokens to `.env` via `config.SaveTokens()`, and retries once.
- Reuse this sentinel + `errors.Is()` + retry-once shape in any new command that calls the Fleet API. The token *lifetimes* and operational behavior are documented in `CLAUDE.md` → "Token Behavior".

---

## Running

```bash
# Install / update dependencies (first time or after go.mod changes)
go mod tidy

# One-time OAuth setup — run again only when refresh token expires (every 3 months)
go run ./cmd/setup

# List all vehicles on the account (tokens from .env, or pass --access-token/--refresh-token).
# Auto-refreshes the access token on HTTP 401.
go run ./cmd/magus
```
