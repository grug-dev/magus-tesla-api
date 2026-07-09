## Context

Tiers 1–2 give us `account.UpsertFromOAuth` and a gateway with cookie sessions. This change wires
the actual OAuth login. Design decisions here were **self-resolved** (per the session goal) with
one overriding constraint: the app must run cheaply on a **single small VPS** — no managed auth
service, no extra datastore, minimal moving parts.

## Goals / Non-Goals

**Goals**
- Sign in with Google → account provisioned/resolved → authenticated session.
- CSRF-protected OAuth flow; logout.
- Auth-aware landing page.

**Non-Goals**
- Tesla connect (Tier 4) or any Tesla data (Tier 5).
- Multiple identity providers, account settings, email verification flows.
- Server-side session/revocation store (stays cookie-based).

## Decisions

### `golang.org/x/oauth2` + Google endpoint (not a managed auth service)
Standard, free, no vendor. `oauth2.Config{Endpoint: google.Endpoint, Scopes: openid/email/profile}`
builds the consent URL and exchanges the code; the user's identity is read from Google's
`userinfo` endpoint (`sub`, `email`, `name`). A managed service (Auth0/Firebase/Cognito) would add
cost and an external dependency — rejected for the VPS goal.

### Identity lives in the encrypted session cookie
On callback we store `uid` (account UUID) and `email` in the session — not just `uid`. The landing
page then renders auth state with **no database read per request**, and the `account` interface
needs no new "get by id" method. Cheapest and simplest for one VPS. (Cookie is signed+encrypted;
email isn't secret anyway.)

### `googleauth` as a thin adapter
`internal/googleauth` wraps the OAuth config and returns its own `Identity{Sub,Email,Name}` — it
does not import `account`. The gateway maps `Identity` → `account.OAuthIdentity{Provider:"google",
ProviderID: sub, ...}` and calls `UpsertFromOAuth`. Keeps the adapter independent and the
boundary clean (mirrors the `tesla` adapter pattern).

### CSRF via OAuth `state` in the session
`/auth/google/login` generates a random `state` (crypto/rand), stores it in the session, and
includes it in the consent URL. `/auth/google/callback` rejects any request whose `state` doesn't
match the stored one, then deletes it. Prevents login-CSRF without extra infrastructure.

### `BASE_URL` config for the callback
Google requires an exact redirect URI. We derive it as `BASE_URL + /auth/google/callback`, so the
same binary works on `http://localhost:8080` in dev and `https://<domain>` on the VPS by changing
one env var.

## Routes

| Route | Purpose |
|---|---|
| `GET /login` | Login page (Sign in with Google) |
| `GET /auth/google/login` | Set `state`, redirect to Google consent |
| `GET /auth/google/callback` | Validate `state`, exchange code, upsert account, set session, redirect `/` |
| `POST /logout` | Clear the session |

## Risks / Trade-offs

- **Cookie identity can go stale** (e.g., email change) until next login — acceptable; identity is
  re-fetched on every sign-in.
- **No server-side revocation** — logout clears the cookie; a stolen cookie is valid until it
  expires. Acceptable for this app's scope; documented in the sessions note.
- **Google credential setup is manual** (Cloud Console) — documented in `docs/deployment.md`.

## Open Questions
- **Route protection middleware** (redirect anonymous users away from private pages) — not needed
  until Tier 4/5 add private pages; will add a small `requireUser` middleware then.
