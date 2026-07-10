# Account Sub-Agent

Agent-Name: `account`

Per-module instructions for `internal/account/` — merged with the global rules
(`CLAUDE.md`, `ai/*.md`) by any assistant working here (see `ai/agentic-workflow.md`).

## Doc-Pack (module)

Extends the project base Doc-Pack (`CLAUDE.md` → "Pipeline config") — never replaces it.
A dispatched worker/reviewer reads: base pack + this list + this file, before any write.

- `docs/deployment.md` — DATABASE_URL and local Postgres setup this module depends on
- `ai/go-conventions.md` §persistence — binding here: goose migrations are the single
  sqlc schema source; convert pgtype values to domain types at the DB→domain boundary

## Responsibility

Owns user accounts and their per-user Tesla credentials: Google-identity upsert, Tesla
token storage (access token ~8 h; refresh token ~3 months, single-use), token refresh,
and the per-user vehicle registry. Multi-tenant by design — never assume a single user
or vehicle. Token refresh is THIS module's job, not the `tesla` adapter's.

## Public interface (the module's port)

Consumers (e.g. the gateway) call these — never this module's tables
(see `internal/account/service.go`):

- `UpsertFromOAuth` — create/find an account from a Google identity
- `SaveTeslaTokens(ctx, accountID, TeslaTokens)` — persist a user's Tesla tokens
- `AccessTokenFor(ctx, accountID) (string, error)` — a valid access token for Fleet API
  calls (refreshing behind the scenes when needed)
- `RegisteredVehicles` — the user's persisted vehicle registry

## Boundaries

- Data lives in `internal/account/db/` (goose migrations + `query.sql`, sqlc-generated
  code). No other module touches these tables — ever.
- Does not import other feature modules; consumers wire account and tesla together
  (e.g. `AccessTokenFor` → `tesla.Credentials` happens in the gateway, not here).
- No HTML, no HTTP handlers — that is the gateway's layer.

## Testing

- Unit tests with fakes at the DB boundary, plus integration tests
  (`service_integration_test.go`) against local Postgres.
- Tokens are stored plaintext (local Postgres only). Encrypting at rest is a known open
  item before any non-local deploy — do not "fix" it silently inside an unrelated change.
