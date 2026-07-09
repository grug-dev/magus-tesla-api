## 1. Schema

- [x] 1.1 Add goose migration `internal/account/db/migrations/20260709000001_tesla_tokens_one_per_account.sql`: dedupe existing `tesla_tokens` rows (keep newest per account), drop `idx_tesla_tokens_account_id`, add `UNIQUE (account_id)`; Down reverses both.

## 2. Query & codegen

- [x] 2.1 In `internal/account/db/query.sql`, rename `InsertTeslaToken` → `UpsertTeslaToken` and add `ON CONFLICT (account_id) DO UPDATE` setting tokens, expiry, and `updated_at`.
- [x] 2.2 Regenerate sqlc (`make sqlc`) so `db/query.sql.go` exposes `UpsertTeslaToken` / `UpsertTeslaTokenParams`.

## 3. Service

- [x] 3.1 In `internal/account/service.go`, change `SaveTeslaTokens` to call `s.q.UpsertTeslaToken(...)` (signature unchanged; `TeslaCallback` untouched).

## 4. Tests

- [x] 4.1 Add `TestSaveTeslaTokens_ReplacesExistingConnection` to `internal/account/service_integration_test.go`: save twice for one account with different tokens; assert exactly one row (`SELECT count(*) … WHERE account_id`) and that `GetLatestTeslaTokenByAccount` returns the second pair.

## 5. Verify

- [x] 5.1 `go build ./...` and `go vet ./...` pass.
- [ ] 5.2 Owner applies migration (`make migrate-up`); `go test ./...` green (integration test with `DATABASE_URL`).
