# Tasks — add-tesla-connect

> Reuses `internal/auth` (Tesla OAuth) + `account.SaveTeslaTokens`. Auth-required + CSRF state,
> same pattern as Google login. Builds/codegen run by the assistant (CLAUDE.md project override).

## 1. Reuse internal/auth + config
- [x] 1.1 Generalize `auth.BuildAuthURL(clientID, redirectURI, state)` (per-request CSRF state); update the `cmd/setup` caller
- [x] 1.2 Add `Config.TeslaConnectRedirectURL()` = `BaseURL + /connect/tesla/callback`; document the Tesla redirect URI in `docs/deployment.md`

## 2. Handlers, routes, wiring
- [x] 2.1 `gateway.NewEngine` → `Deps` struct (pool, account, google, session secret, Tesla client id/secret/redirect); update `cmd/web` and the gateway test
- [x] 2.2 Handler: `currentUID` helper (parse `uid` from session); `ConnectTesla` (`GET /connect/tesla` — auth-required, set state, redirect to `auth.BuildAuthURL`); `TeslaCallback` (`GET /connect/tesla/callback` — auth-required, validate state, `auth.ExchangeCode`, `account.SaveTeslaTokens`, redirect `/`)
- [x] 2.3 Register the two routes

## 3. Template
- [x] 3.1 `home.templ` — show "Connect your Tesla" link when signed in; `templ generate`

## 4. Verify
- [x] 4.1 `make build` + `make vet`
- [x] 4.2 `make test` — httptest: anonymous `/connect/tesla` redirects to `/login`; signed-in `/connect/tesla` redirects to Tesla with a `state`; callback with mismatched state → 400
- [x] 4.3 `openspec validate add-tesla-connect --strict`
