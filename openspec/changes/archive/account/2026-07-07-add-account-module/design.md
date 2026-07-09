## Context

The platform is multi-tenant (AGENTS.md, `ai/architecture.md` §5) but has no identity layer
and no persistence — tokens live in `.env` for a single user. This change introduces the
`account` module and the project's first database. Boundary constraints from
`ai/architecture.md`: each module owns its own data, cross-module access is interface-only,
and no module reads another's tables. The `tesla` adapter is already credential-parameterized,
so it consumes whatever `account` hands it.

## Goals / Non-Goals

**Goals:**
- `account` owns app users (provisioned via social OAuth) and each user's Tesla tokens.
- Store Tesla connections per account (1:N); own their refresh/rotation.
- Expose a Go interface for other modules to get an account's current Tesla credentials.
- Stand up PostgreSQL + `pgx` + module-scoped `sqlc`, establishing the DB conventions the
  rest of the monolith will reuse.

**Non-Goals:**
- The OAuth *login UI* and the Tesla *connect* flow wiring (a `gateway`/web-layer concern; this
  change defines the storage + interface they will use).
- Moving `cmd/magus`'s temporary refresh logic here (a later change).
- Analytics/time-series tables (owned by future domain modules).
- Token encryption at rest (see Open Questions).

## Decisions

**PostgreSQL via `pgx` (over SQLite).** Chosen for the heavy multi-tenant time-series
analytics the platform targets (trends, aggregations, forecasting) and stronger concurrency.
Trade-off: needs a running server (local Docker) vs SQLite's zero-ops single file.

**Social OAuth, no passwords.** Store `email`, `provider`, `provider_id`, `display_name`.
Alternatives: email+password (password-security burden) or Tesla-identity-only (conflates app
identity with the Tesla connection, and breaks once a user connects multiple Tesla accounts).

**Separate `tesla_tokens` table (1:N), not columns on `accounts`.** Keeps identity separate
from secrets and lets one user connect more than one Tesla account.

**Module-scoped `sqlc` package `internal/account/db` (package `accountdb`).** A single root
`sqlc.yaml` gets one entry per module; each generates its own package that no other module
imports — enforcing "no cross-module DB access" at the package level.

**UUID primary keys (`gen_random_uuid()`).** Non-guessable and merge-friendly for multi-tenant
data; alternative was serial integers.

Proposed schema (the concrete DDL that will become `internal/account/db/schema.sql`):

```sql
CREATE TABLE accounts (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email        TEXT NOT NULL,
    provider     TEXT NOT NULL,          -- e.g. 'google'
    provider_id  TEXT NOT NULL,          -- subject id from the provider
    display_name TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_id)
);

CREATE TABLE tesla_tokens (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id        UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    tesla_email       TEXT,              -- which Tesla account this connection is for
    access_token      TEXT NOT NULL,
    refresh_token     TEXT NOT NULL,
    access_expires_at TIMESTAMPTZ NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tesla_tokens_account_id ON tesla_tokens (account_id);
```

Proposed interface (the module's public "port"):

```go
package account

type OAuthIdentity struct {
    Provider    string
    ProviderID  string
    Email       string
    DisplayName string
}

type Service interface {
    // UpsertFromOAuth creates or resolves the account for a social identity.
    UpsertFromOAuth(ctx context.Context, id OAuthIdentity) (Account, error)
    // SaveTeslaTokens stores or rotates a Tesla connection's tokens.
    SaveTeslaTokens(ctx context.Context, accountID uuid.UUID, t TeslaTokens) error
    // AccessTokenFor returns a currently-valid Tesla access token, refreshing and
    // persisting first if the stored one has expired.
    AccessTokenFor(ctx context.Context, accountID uuid.UUID) (string, error)
}
```

Returning a plain access token (not `tesla.Credentials`) keeps the dependency direction
clean: `account` does not import `tesla`. The caller (gateway/orchestrator) wraps the token
in `tesla.Credentials` before calling the adapter.

## Risks / Trade-offs

- **Tesla tokens stored in plaintext** → Mitigation: lock down DB access now; add column
  encryption (app-level or `pgcrypto`) before any non-local deployment (Open Question).
- **Refresh-token rotation is single-use** → Mitigation: serialize refresh per connection and
  persist the rotated pair in the same transaction as the read, so a crash can't strand a
  consumed token.
- **Postgres ops cost for a personal app** → Mitigation: local Docker Postgres; volume is tiny.
- **`sqlc`/`pgx` type mapping (UUID, TIMESTAMPTZ)** → Mitigation: configure `sqlc` for
  `pgx/v5` with explicit type overrides (`google/uuid`, `time.Time`).

## Migration Plan

- `internal/account/db/schema.sql` is the initial schema; apply it with a migration tool
  (see Open Questions) to a fresh database.
- Rollback: drop `tesla_tokens` then `accounts` — no production data exists yet.

## Open Questions

- **Encrypt Tesla tokens at rest?** Recommended before any non-local use; decide app-level vs
  `pgcrypto`.
- **Migration tool:** `goose` vs `golang-migrate` vs `atlas` vs plain SQL (lean `goose`).
- **UUID representation in generated Go:** `google/uuid.UUID` vs `pgtype.UUID` (affects `sqlc`
  config and the interface signatures above).
