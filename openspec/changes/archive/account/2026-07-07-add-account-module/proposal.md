## Why

The platform is multi-tenant, but nothing owns *users* or their Tesla credentials — the
current `.env`-based single-user token handling (`config`/`auth`/`server`, plus the
temporary refresh logic in `cmd/magus`) is a smoke test that cannot serve more than one
person. We need an identity boundary that provisions user accounts and stores each
account's Tesla tokens so every other module can act on behalf of a specific user.

## What Changes

- Add a new `internal/account/` module — the multi-tenant identity boundary (one OpenSpec
  domain per module).
- Provision accounts via **social OAuth** (e.g. Google): store `email`, `provider`,
  `provider_id`, `display_name`. **No passwords are stored.**
- Enforce **one account per `(provider, provider_id)`**.
- Store each account's **Tesla tokens in a separate table** (1:N — a user may connect more
  than one Tesla account), owned exclusively by this module: access token, refresh token,
  access expiry.
- The account module **owns refresh/rotation** of Tesla tokens and persists the rotated pair.
- Expose a **public Go interface** so other modules obtain an account's current Tesla
  credentials without reading the account tables (no cross-module DB access).
- Introduce the project's **first persistence layer**: PostgreSQL via `pgx`, with **sqlc**
  generated queries in a module-scoped package (`internal/account/db`).

## Capabilities

### New Capabilities
- `account`: multi-tenant user identity — provisioning accounts from social OAuth, storing
  per-account Tesla connection tokens, and exposing credentials plus token refresh to other
  modules through an interface.

### Modified Capabilities
- None (no existing specs yet).

## Impact

- **New module:** `internal/account/` plus its module-scoped DB package `internal/account/db`
  (sqlc-generated, package `accountdb`).
- **New infrastructure/dependencies:** PostgreSQL, the `pgx` driver, `sqlc` codegen, and the
  first schema/migration. Establishes the DB + sqlc conventions the rest of the monolith
  will follow.
- **Code:** non-breaking. The `tesla` adapter is unaffected (already credential-parameterized).
  The temporary token-refresh logic in `cmd/magus` will later move into this module.
- **Affected modules:** `account` (new); future consumers (`gateway`, domain modules) will
  depend on its interface, never on its tables.
