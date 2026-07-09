## Context

Tier 1 gave `account.SaveTeslaTokens`; `internal/auth` already builds the Tesla authorize URL and
exchanges codes (from the `cmd/setup` smoke test). This change reuses both to let a signed-in web
user connect their Tesla. Decisions self-resolved for the single-VPS goal — no new services.

## Goals / Non-Goals

**Goals**
- A signed-in user connects Tesla via OAuth; tokens are persisted per account.
- CSRF-protected; auth-required.

**Non-Goals**
- Displaying vehicles / any Tesla data (Tier 5).
- Managing/disconnecting multiple connections, token health UI.
- Changing token refresh (owned by `account`, Tier 1).

## Decisions

### Reuse `internal/auth`; generalize `BuildAuthURL` for a per-request state
The smoke-test `BuildAuthURL(clientID, redirectURI)` hardcodes `state="magus123"` — unsafe for a
real flow. Generalize it to `BuildAuthURL(clientID, redirectURI, state)` and pass a random state;
update the single `cmd/setup` caller. `ExchangeCode(clientID, secret, code, redirectURI)` is reused
as-is. Keeping the Tesla OAuth mechanics in `internal/auth` preserves the boundary (the gateway
orchestrates; it doesn't own OAuth details).

### Auth-required with a `state` CSRF guard
Both routes require a signed-in session (`uid`); anonymous requests are redirected to `/login`.
`/connect/tesla` stores a random `state` in the session; the callback rejects a mismatch. Same
pattern as Google login (Tier 3), no new infrastructure.

### Store tokens via `account.SaveTeslaTokens`
The callback computes `AccessExpiresAt = now + expires_in` and calls
`account.SaveTeslaTokens(uid, TeslaTokens{...})`. Refresh/rotation is already owned by `account`
(Tier 1), so the gateway only stores the freshly-issued pair. An account may connect more than one
Tesla (1:N) — re-running connect adds another connection, by design.

### `gateway.NewEngine(Deps)` refactor
Parameters (pool, account, google, session secret, and now Tesla client id/secret/redirect) have
outgrown a positional signature. Introduce a `Deps` struct for clarity and easy future growth.

## Routes

| Route | Auth | Purpose |
|---|---|---|
| `GET /connect/tesla` | required | Set `state`, redirect to Tesla authorize URL |
| `GET /connect/tesla/callback` | required | Validate `state`, exchange code, `SaveTeslaTokens`, redirect `/` |

## Risks / Trade-offs

- **No "already connected" UI** — the home link always shows for signed-in users; reconnecting adds
  a connection. Acceptable; Tier 5 surfaces connected vehicles.
- **Redirect URI must be registered with Tesla** — documented; a mismatch fails the OAuth exchange.

## Open Questions
- **Disconnect / manage connections** — deferred until there's a dashboard to manage them from.
