## Why

The gateway (Tier 2) serves pages and has cookie sessions, and the account module (Tier 1) can
provision users from a social OAuth identity — but nothing connects them. There is no way to
actually **sign in**. This change adds Google login: a visitor authenticates with Google, the
gateway provisions/resolves their account via `account.UpsertFromOAuth`, and establishes an
authenticated session. It unblocks Tesla connect (Tier 4) and the per-user dashboard (Tier 5),
which both need "the current user".

## What Changes

- Add a small **`internal/googleauth`** adapter over Google's OAuth 2.0 (via
  `golang.org/x/oauth2`): build the consent URL, exchange the code, and fetch the user's
  identity (`sub`, `email`, `name`) from Google's userinfo endpoint.
- Add gateway login routes:
  - `GET /login` — a page with "Sign in with Google".
  - `GET /auth/google/login` — generate a CSRF `state`, store it in the session, redirect to Google.
  - `GET /auth/google/callback` — validate `state`, exchange the code, `account.UpsertFromOAuth`,
    store the user's `uid`+`email` in the session, redirect home.
  - `POST /logout` — clear the session.
- The landing page reflects auth state: signed-in shows the email + log out; anonymous shows sign-in.
- **Identity is kept in the encrypted session cookie** (`uid`, `email`) — no per-request DB
  lookup, and no new `account` method — cheapest path for a single small VPS.
- Config: `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, and `BASE_URL` (for the callback URL).

## Capabilities

### Modified Capabilities
- `gateway`: adds authenticated login/logout — Google sign-in, CSRF-protected OAuth, session
  identity, and auth-aware UI. (New requirements added to the existing `gateway` spec.)

### New Capabilities
- None. `account` is used through its existing `UpsertFromOAuth` interface; the `googleauth`
  adapter is an implementation detail of the gateway, not a new capability spec.

## Impact

- **New:** `internal/googleauth/`, gateway auth handlers + a login page, session identity.
- **New dep:** `golang.org/x/oauth2` (free; no managed auth service — deliberate for VPS cost).
- **Config:** `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `BASE_URL` (+ `.env.example`,
  `docs/deployment.md`; documents creating Google OAuth credentials).
- **Non-breaking.** Existing routes keep working; unauthenticated users simply see sign-in.
- **Affected:** `gateway` (routes/UI), `account` (called via interface), `googleauth` (new).
