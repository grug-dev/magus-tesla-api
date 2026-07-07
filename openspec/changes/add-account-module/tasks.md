## 1. Persistence setup (Postgres + sqlc)

- [ ] 1.1 Add dependencies: `jackc/pgx/v5` (driver + `pgxpool`) and `google/uuid`; run `go mod tidy`
- [ ] 1.2 Create `internal/account/db/schema.sql` with the `accounts` and `tesla_tokens` tables (see design.md)
- [ ] 1.3 Choose a migration tool (lean `goose`) and add the initial migration from the schema
- [ ] 1.4 Create root `sqlc.yaml`: a module-scoped entry for `account` → `internal/account/db`, package `accountdb`, engine postgresql, `pgx/v5`, with UUID/timestamptz type overrides
- [ ] 1.5 Write `internal/account/db/query.sql`: upsert-account-from-oauth, get-account-by-provider-id, insert/rotate tesla token, get-latest-token-by-account
- [ ] 1.6 Run `sqlc generate` (user) and confirm the `accountdb` package is produced

## 2. Domain types & interface

- [ ] 2.1 Define `account.Account`, `account.OAuthIdentity`, `account.TeslaTokens` domain types
- [ ] 2.2 Define the `account.Service` interface (`UpsertFromOAuth`, `SaveTeslaTokens`, `AccessTokenFor`) per design.md

## 3. Service implementation

- [ ] 3.1 Implement `UpsertFromOAuth` over `accountdb`, enforcing one account per (provider, provider_id)
- [ ] 3.2 Implement `SaveTeslaTokens` (store a new connection / rotate an existing one)
- [ ] 3.3 Implement `AccessTokenFor` with refresh-on-expiry: reuse `auth.RefreshTokens`, persist the rotated pair in the same transaction as the read
- [ ] 3.4 Map DB rows to domain types; keep `accountdb` internal so no other module imports it

## 4. Wiring & config

- [ ] 4.1 Add `DATABASE_URL` handling to `internal/config` (env access stays in config only)
- [ ] 4.2 Provide a `pgxpool` connection and an `account.NewService(pool)` constructor

## 5. Verification

- [ ] 5.1 `go build ./...` and `go vet ./...` (user)
- [ ] 5.2 `openspec validate add-account-module --strict` passes
- [ ] 5.3 Unit tests: provisioning idempotency (same identity → same account) and refresh-on-expiry rotation
