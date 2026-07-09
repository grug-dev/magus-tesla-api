# Tasks — add-google-login

> Decisions self-resolved for a single-VPS deploy: `x/oauth2` + Google endpoint (no managed auth),
> identity in the encrypted session cookie (no per-request DB read), `state` CSRF, `BASE_URL`
> callback. Builds/codegen run by the assistant (CLAUDE.md project override).

## 1. Deps & config
- [x] 1.1 Add `golang.org/x/oauth2`; `go mod tidy`
- [x] 1.2 Add `GoogleClientID`, `GoogleClientSecret`, `BaseURL` (default `http://localhost:8080`) to `internal/config`; document in `.env.example` + `docs/deployment.md` (incl. creating Google OAuth credentials + redirect URI)

## 2. googleauth adapter
- [x] 2.1 `internal/googleauth/googleauth.go` — `Client` over `oauth2.Config` (google endpoint, scopes openid/email/profile); `AuthCodeURL(state)`; `Exchange(ctx, code) (Identity, error)` fetching `userinfo` → `Identity{Sub,Email,Name}`

## 3. Templates
- [x] 3.1 `templates/pages/login.templ` — "Sign in with Google" linking to `/auth/google/login`
- [x] 3.2 Update `templates/pages/home.templ` — `HomeView` gains `Email`/`SignedIn`; show email+logout when signed in, else a sign-in link
- [x] 3.3 `templ generate`

## 4. Handlers & routes
- [x] 4.1 Extend the gateway `Handler` with a `*googleauth.Client`; a `randomState()` helper (crypto/rand)
- [x] 4.2 `LoginPage` (`GET /login`); `GoogleLogin` (`GET /auth/google/login` — set state in session, redirect); `GoogleCallback` (`GET /auth/google/callback` — validate state, exchange, `account.UpsertFromOAuth`, set `uid`+`email`, redirect `/`); `Logout` (`POST /logout` — clear session)
- [x] 4.3 `Home` reads `uid`/`email` from session → auth-aware view
- [x] 4.4 Register routes in `gateway.NewEngine`; build the `googleauth.Client` in `cmd/web` from config

## 5. Verify
- [x] 5.1 `templ generate` + `make build` + `make vet`
- [x] 5.2 `make test` — httptest: `/auth/google/login` redirects (302) to Google with a `state`; callback with mismatched state is rejected (no session); anonymous home offers sign-in
- [x] 5.3 `openspec validate add-google-login --strict`
