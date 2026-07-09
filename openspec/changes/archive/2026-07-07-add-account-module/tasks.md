> **Applied by Claude Code (Opus 4.8), 2026-07-07.** Code is written; the codegen/build
> steps marked _(user)_ are run by you (assistant never runs builds/codegen). Decisions taken
> while applying: UUID → `google/uuid.UUID`; migrations → **goose**; Tesla tokens **plaintext**
> for now (local Postgres, no Docker). **Deviation:** the goose migration is the single schema
> source (sqlc reads `migrations/`); there is no separate `schema.sql`.

## 1. Persistence setup (Postgres + sqlc)

- [x] 1.1 Add dependencies: `jackc/pgx/v5` + `google/uuid` in `go.mod` — _(user)_ run `go mod tidy`
- [x] 1.2 Schema DDL for `accounts` + `tesla_tokens` (in the goose migration, not a separate schema.sql)
- [x] 1.3 Migration tool = **goose**; initial migration `internal/account/db/migrations/20260707000001_init_account.sql` (Up/Down)
- [x] 1.4 Root `sqlc.yaml`: module-scoped `account` → `internal/account/db`, package `accountdb`, postgresql/`pgx/v5`, `uuid`→`google/uuid.UUID` + `timestamptz`→`time.Time` overrides
- [x] 1.5 `internal/account/db/query.sql`: upsert-account-from-oauth, get-account-by-provider-id, insert token, update/rotate token, get-latest (+ FOR UPDATE variant)
- [x] 1.6 _(user)_ Run `sqlc generate`; `accountdb` package produced (`db.go`, `models.go`, `query.sql.go`)

## 2. Domain types & interface

- [x] 2.1 `account.Account`, `account.OAuthIdentity`, `account.TeslaTokens` domain types (no vendor suffix) — `internal/account/account.go`
- [x] 2.2 `account.Service` interface (`UpsertFromOAuth`, `SaveTeslaTokens`, `AccessTokenFor`) + `ErrNoTeslaConnection`

## 3. Service implementation

- [x] 3.1 `UpsertFromOAuth` over `accountdb`, idempotent per (provider, provider_id) via `ON CONFLICT`
- [x] 3.2 `SaveTeslaTokens` stores a new connection (an account may have several)
- [x] 3.3 `AccessTokenFor` with refresh-on-expiry: `auth.RefreshTokens`, row locked `FOR UPDATE`, rotated pair persisted in the same tx as the read
- [x] 3.4 Row→domain mapping in `service.go`; `accountdb` stays internal (no other module imports it)

## 4. Wiring & config

- [x] 4.1 `DATABASE_URL` added to `internal/config` (env access stays in config only)
- [x] 4.2 `account.NewService(pool, clientID, clientSecret)` constructor; pool (`pgxpool`) created by the caller (future `cmd/web`)

## 5. Verification

- [x] 5.1 `go build ./...` + `go vet ./...` + `go test ./...` green (via `make check`, go 1.26.4)
- [x] 5.2 `openspec validate add-account-module --strict` passes
- [x] 5.3 Tests: pure unit (`needsRefresh`, `accessExpiry`) always-run; `DATABASE_URL`-gated integration for provisioning idempotency and refresh-on-expiry rotation
