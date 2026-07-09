## Why

Connecting Tesla persists tokens with a plain `INSERT`, so every time a signed-in user runs the
Tesla connect flow a new `tesla_tokens` row is appended — even for the same Tesla account. Rows
pile up as dead weight and only the newest is ever read. We want reconnecting to replace the
stored connection in place, and to guarantee one Tesla connection per account.

## What Changes

- **BREAKING** (spec): the `account` capability moves from supporting *one or more* Tesla
  connections per account to *at most one*. Reconnecting **replaces** the stored tokens instead
  of adding a second connection.
- `tesla_tokens` gains a `UNIQUE (account_id)` constraint; a migration first dedupes any existing
  rows (keeping the most-recently-updated per account).
- Token persistence becomes an upsert (`ON CONFLICT (account_id) DO UPDATE`) rather than an insert.
- No change to the public `account` interface (`SaveTeslaTokens`, `AccessTokenFor`) or to the
  gateway connect routes — only the storage semantics change.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `account`: the **Per-Account Tesla Token Storage** requirement changes from "one or more Tesla
  connections per account" to "at most one; reconnecting replaces the stored tokens", and the
  "second Tesla connection" scenario is replaced with a "reconnecting replaces the stored
  connection" scenario.

## Impact

- **Affected module:** `internal/account/` — schema migration, `query.sql`, generated
  `db/query.sql.go` (sqlc), `service.go`, integration test.
- **Data:** the forward migration deletes duplicate `tesla_tokens` rows before adding the unique
  constraint (gated; run by the owner via `make migrate-up`).
- **Behavior:** the gateway `/connect/tesla` flow is unchanged in code; its effect changes —
  reconnecting a Tesla now updates the single row instead of creating another.
- **No** breaking change to any Go interface or HTTP route.
