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
| `web` | `cmd/web/` | HTTP gateway serving the per-user vehicle dashboard (multi-tenant, DB-backed) |

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

## Persistence (Postgres + sqlc + goose)

Conventions established by the `account` module — the project's first DB-backed module.
Every future DB-backed module follows the same shape. Full deploy runbook:
[`../docs/deployment.md`](../docs/deployment.md).

- **PostgreSQL via `pgx/v5` + `pgxpool`.** UUID primary keys (`gen_random_uuid()`, built into
  Postgres 13+). In generated Go, UUIDs are `github.com/google/uuid.UUID` (via a `sqlc.yaml`
  override). `timestamptz` stays the pgx/v5 default (`pgtype.Timestamptz`); the module converts
  it to/from plain `time.Time` at its DB→domain **mapping boundary** so `pgtype` never leaks into
  a module's public types.
- **Module-scoped DB package.** Each module's queries live in `internal/<module>/db`, generated
  by sqlc into package `<module>db` (e.g. `accountdb`). **No other module imports it** — this is
  how "no cross-module DB access" (`architecture.md` §2) is enforced at the package level.
- **`sqlc.yaml` — one `sql:` entry per module.** Add an entry when a module gains a DB; never
  merge two modules into one package. Keep the `uuid → google/uuid.UUID` override; map nullable
  columns (`pgtype.Text`) and `timestamptz` (`pgtype.Timestamptz`) to plain domain types in the
  module's mapping helpers, not via more overrides.
- **goose migrations are the single schema source.** SQL migrations live in
  `internal/<module>/db/migrations/<timestamp>_<name>.sql` (goose `-- +goose Up/Down`). sqlc's
  `schema:` points at that directory, so there is **no separate `schema.sql`** to keep in sync.
- **`DATABASE_URL` is the single source of truth** for the DSN. Only `internal/config` reads it
  (env access stays in config); the `Makefile` derives the DB name + admin connection from it.
- **Setup is one command:** `make db-setup` (idempotent create-if-missing + `goose up`).
  Regenerate code with `make sqlc` after editing any `query.sql` or migration. **Never run these
  as part of a build** — they are explicit developer/deploy steps.
- **Secrets at rest:** Tesla tokens are stored plaintext for now (local Postgres). Add column
  encryption before any non-local deployment — tracked as an open item on the `account` change.
- **DB tests are `DATABASE_URL`-gated** and self-skip when it is unset, so `go test ./...` stays
  green without a database. Pure logic (e.g. token-expiry math) is unit-tested without a DB.

---

## Error-handling pattern (401 / auto-refresh)

- `tesla.ErrUnauthorized` is the sentinel error returned by the adapter on 401 — use `errors.Is()` to detect it in any caller that needs the pattern.
- **Token refresh is owned by the `account` module** (`internal/account`): `account.AccessTokenFor` proactively refreshes an expired (or near-expiry) stored token before handing it out, atomically rotating the single-use refresh token inside a `FOR UPDATE` transaction. Callers wrap the resulting token in `tesla.Credentials` before calling the adapter.
- A *reactive* refresh-on-401 retry (recovering from a token Tesla revoked despite it not being time-expired) is a planned follow-up, also to live in the `account` module.
- Reuse this sentinel + `errors.Is()` shape in any new code that calls the Fleet API. The token *lifetimes* and operational behavior are documented in `CLAUDE.md` → "Token Behavior".

---

## Running

```bash
# Install / update dependencies (first time or after go.mod changes)
go mod tidy

# One-time OAuth setup — single-user smoke path; saves tokens to .env.
# Re-run only when the refresh token expires (every 3 months).
go run ./cmd/setup

# Multi-tenant web gateway — serves the per-user vehicle dashboard.
# Requires DATABASE_URL and SESSION_SECRET in .env.
go run ./cmd/web
```
