## Why

A signed-in user (Tier 3) still has no Tesla data — their account holds no Tesla tokens. This
change adds the Tesla OAuth **connect** flow: a signed-in user authorizes Magus against Tesla,
and the gateway stores the resulting tokens via `account.SaveTeslaTokens`. It reuses the existing
`internal/auth` Tesla OAuth helper and unblocks the per-user vehicle dashboard (Tier 5).

## What Changes

- Add gateway routes (signed-in only):
  - `GET /connect/tesla` — generate a CSRF `state`, store it in the session, redirect to Tesla's
    authorize URL.
  - `GET /connect/tesla/callback` — validate `state`, exchange the code (`auth.ExchangeCode`),
    and `account.SaveTeslaTokens` for the current user.
- **Reuse `internal/auth`:** generalize `BuildAuthURL` to take a per-request `state` (update the
  `cmd/setup` caller), and reuse `ExchangeCode` for the token exchange.
- The landing page shows "Connect your Tesla" when signed in.
- Config: `TeslaConnectRedirectURL()` = `BASE_URL + /connect/tesla/callback` (Tesla client
  id/secret already configured).
- Refactor `gateway.NewEngine` to take a `Deps` struct (its parameter list has grown).

## Capabilities

### Modified Capabilities
- `gateway`: adds Tesla account connection — a CSRF-protected, auth-required OAuth connect flow
  that stores the connection's tokens via the account module. (Requirements added to the existing
  `gateway` spec.)

### New Capabilities
- None. `account.SaveTeslaTokens` and `internal/auth` are reused through existing surfaces.

## Impact

- **New:** gateway Tesla-connect handlers/routes + a home link.
- **Changed (internal):** `auth.BuildAuthURL` gains a `state` parameter (`cmd/setup` updated);
  `gateway.NewEngine` takes a `Deps` struct.
- **No new dependencies.**
- **Config:** `TeslaConnectRedirectURL` — the Tesla app must register
  `<BASE_URL>/connect/tesla/callback` as a redirect URI (documented in `docs/deployment.md`).
- **Non-breaking** for end users; internal signature changes only. Affected: `gateway`, `auth`
  (signature), `account` (via interface).
