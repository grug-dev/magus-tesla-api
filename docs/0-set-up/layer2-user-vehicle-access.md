# Layer 2 — User & Vehicle Access

**Authorization layer:** User / vehicle level — *"Which Tesla account has agreed to let this app act on its behalf, and what vehicles come with it?"*

This document covers everything that happens **after** the app is registered: a human logs in
with their personal Tesla account, consents on a browser screen, and the app receives **per-user
tokens** that let it read that account's vehicles. Unlike Layer 1 (one-time, app-wide), this layer
is **per user** — every account that wants Magus Monitor to see its cars runs the OAuth flow once.

> **Prerequisite:** [Layer 1 — Application Registration](./layer1-app-registration.md) must be
> complete. Layer 2 uses the `TESLA_CLIENT_ID` / `TESLA_CLIENT_SECRET` created there, but it does
> **not** re-do any of the app-level setup.

> **Mental model:** Layer 1 answers *"is the app trusted?"* (Client ID/Secret). Layer 2 answers
> *"which account consented?"* (access + refresh tokens). **Vehicles come along for free with
> whichever account authenticates** — the app never targets a car directly; it reads every vehicle
> on the account whose tokens it holds. This is why the project is fundamentally
> one-app → many-users → many-vehicles, even though the current code stores a single account's
> tokens in one `.env`.

> **Codebase (whole layer):** this is where nearly all the Go code lives — `cmd/setup`,
> `cmd/web`, `internal/auth`, `internal/server`, `internal/account`, `internal/tesla`, and
> `internal/gateway`. The `internal/config` package is shared with Layer 1: `config.Load()`
> supplies both the app credentials (Layer 1) and the user tokens (Layer 2), while
> `config.SaveTokens()` is Layer-2-only (used by the `cmd/setup` smoke path; the web gateway
> stores tokens per user in Postgres via `internal/account`).

---

## Step 6 — OAuth 2.0 flow (get your user access token)

This is handled by the Go program in `cmd/setup/`. It automates all three sub-steps:
opens the browser, catches the callback, exchanges the code, and saves both tokens to `.env`.

> **Codebase (Step 6 orchestration):** `cmd/setup/main.go` → `main()` wires the sub-steps together.
> Per the project rule, `cmd/` stays thin — it only orchestrates; the real work lives in the
> `internal/` packages referenced below.

### Project structure

```
cmd/setup/main.go              ← entry point — orchestrates steps 1, 6a, 6b, 6c
internal/config/config.go      ← Step 1  — loads .env, saves tokens back
internal/auth/oauth.go         ← Step 6a — builds the Tesla auth URL, opens browser
internal/server/callback.go    ← Step 6b — Gin server that catches the OAuth redirect
internal/auth/token.go         ← Step 6c — exchanges the code for tokens
```

### First-time run (install dependencies)

```bash
go mod tidy
```

### Run the setup

```bash
go run ./cmd/setup
```

What happens automatically:

1. **[Step 1]** Loads `TESLA_CLIENT_ID` and `TESLA_CLIENT_SECRET` from `.env`.
2. **[Step 6a]** Builds the OAuth URL and opens it in your browser. If the browser doesn't open, the URL is printed to the terminal for manual copy/paste.
3. **[Step 6b]** Starts a Gin server on `http://localhost:8080` and waits for Tesla to redirect with the authorization code. Approve the consent screen in the browser — the server catches the code automatically.
4. **[Step 6c]** Exchanges the code for an access token and refresh token, then writes both to `.env`.

> **Why the browser login is the whole point:** the account you log in with here *is* the account
> whose vehicles the app will read. Consent is what turns "a trusted app" into "a trusted app that
> may see **my** cars." Log in with a different Tesla account and you'd get tokens for that
> account's vehicles instead — same app, different data.

> **Codebase (per sub-step):**
> - **Step 6a** — `internal/auth/oauth.go` → `BuildAuthURL(clientID, redirectURI)` constructs the
>   `auth.tesla.com` URL; `OpenBrowser(authURL)` launches it (falls back to printing the URL).
> - **Step 6b** — `internal/server/callback.go` → `StartCallbackServer(codeCh)` runs the Gin
>   server on `:8080/callback` and pushes the received code onto the channel; `Shutdown(srv)`
>   stops it once the code arrives.
> - **Step 6c** — `internal/auth/token.go` → `ExchangeCode(clientID, clientSecret, code, redirectURI)`
>   POSTs `grant_type=authorization_code` to the token endpoint and returns a `TokenResponse`;
>   `internal/config/config.go` → `config.SaveTokens(accessToken, refreshToken)` writes both back
>   to `.env`.

After a successful run, `.env` will contain:

```
TESLA_ACCESS_TOKEN=...
TESLA_REFRESH_TOKEN=...
```

### Understanding the two tokens

After `go run ./cmd/setup` completes, two tokens are saved to `.env`. Here is what each one is, where it came from, and when you use it.

> **Both tokens are account-scoped.** They encode *which user consented*, not *which app*. That is
> the essential difference from the Layer 1 Partner Token (which is app-scoped and reads no car).

---

#### `TESLA_ACCESS_TOKEN`

| Property | Value |
|---|---|
| **Generated by** | Step 6c — the code exchange call to `https://fleet-auth.prd.vn.cloud.tesla.com/oauth2/v3/token` (with `grant_type=authorization_code`, `client_id`, `client_secret`, `code`, `audience`, `redirect_uri`) |
| **What it is** | A short-lived JWT that proves your app is authorized to act on your behalf |
| **How it's used** | Sent on every Fleet API request as `Authorization: Bearer <token>` |
| **Lifetime** | 8 hours (28800 seconds) |
| **What happens when it expires** | Fleet API returns HTTP 401. You need a new one using the refresh token. |

Think of it as a **daily pass** — it gets you through the door, but expires every 8 hours.

> **Codebase:** `internal/tesla/client.go` → `NewClient()` returns a state-less adapter; every
> call receives `tesla.Credentials{AccessToken}` as an argument (the adapter holds no identity of
> its own). `do()` attaches the token as the `Authorization: Bearer` header. On HTTP 401 the
> adapter returns the sentinel `tesla.ErrUnauthorized`, which callers detect with `errors.Is()`.

---

#### `TESLA_REFRESH_TOKEN`

| Property | Value |
|---|---|
| **Generated by** | Step 6c — same code exchange call, returned alongside the access token |
| **What it is** | A long-lived credential used to get a new access token without re-doing the browser login |
| **How it's used** | Sent to `https://fleet-auth.prd.vn.cloud.tesla.com/oauth2/v3/token` with `grant_type=refresh_token`, `client_id`, `refresh_token` (no `client_secret`) to get a fresh access token |
| **Lifetime** | 3 months |
| **Critical rule** | It is **single-use**. Every time you use it, Tesla returns a brand new refresh token. You must save the new one or you will be locked out. |
| **Safety window** | The previous refresh token stays valid for 24 hours after it has been rotated, in case your app failed to save the new one. |

Think of it as the **master key** — you use it once to get a new daily pass, and it replaces itself each time.

> **Codebase:** `internal/auth/token.go` → `RefreshTokens(clientID, clientSecret, refreshToken)`
> POSTs `grant_type=refresh_token` and returns a fresh `TokenResponse` (new access **and** new
> refresh token). Because it is single-use, the caller must immediately persist the new pair. In
> the multi-tenant path `internal/account` owns this: `account.AccessTokenFor` refreshes a
> near-expiry token and persists the rotated pair atomically (FOR UPDATE) in Postgres; the
> single-user `cmd/setup` smoke persists the pair to `.env` via `config.SaveTokens()`.

---

#### When to re-run `go run ./cmd/setup`

| Situation | What to do |
|---|---|
| Access token expired (8 hours) | Use the refresh token to get a new access token — no browser login needed |
| Refresh token expired (3 months) | Re-run `go run ./cmd/setup` — goes through the full browser login again |
| Refresh token was used but new one wasn't saved | Re-run `go run ./cmd/setup` within 24 hours of last use — old one may still work |
| Both tokens lost or corrupted | Re-run `go run ./cmd/setup` |

> **Implemented (multi-tenant):** `internal/account` proactively refreshes a near-expiry token
> before handing it out (`account.AccessTokenFor`), atomically rotating the single-use refresh
> token in Postgres — no manual action needed for ordinary access-token expiry. A *reactive*
> refresh-on-401 retry (recovering from a token Tesla revoked despite it not being time-expired)
> is a planned follow-up, also to live in `account`. Re-run `go run ./cmd/setup` only for the
> single-user smoke path, or when a stored refresh token itself expires (every 3 months).

> **Codebase:** the auto-refresh path is `internal/account/service.go` (`AccessTokenFor`) →
> `internal/auth/token.go` `RefreshTokens()` → persist the rotated pair in the same
> `FOR UPDATE` transaction.

---

## Step 7 — Pair the virtual key with Magus *(skipped)*

> **Skipped for now.** Virtual key pairing is only required to send commands to Magus (lock, unlock, climate, etc.). Read-only data access works without it. If commands are needed in the future, revisit this step.
>
> When ready: use the Partner Token (Step 5a) to call the pairing initiation endpoint → Tesla sends a request to the mobile app → approve it from the app or inside the vehicle.

> **Why it belongs to Layer 2:** although it reuses the Layer 1 EC key pair and Partner Token, the
> pairing is per-vehicle and per-account — it authorizes commands to *this user's specific car*.
> It is only needed for write operations; every read in Step 8 works without it.



---

## Step 8 — First API call: fetch Magus's live data

Handled by the multi-tenant web gateway (`cmd/web`) using the state-less `internal/tesla`
adapter. Per-user tokens are supplied by `internal/account` (from Postgres), not `.env`.

### Package structure

```
internal/tesla/client.go      ← state-less authenticated HTTP adapter for the Fleet API
internal/tesla/vehicles.go    ← ListVehicles(), VehicleData(), WakeUp()
internal/account/service.go   ← per-user token store + proactive refresh (AccessTokenFor)
internal/gateway/handlers/    ← HTTP handler → account token → tesla.ListVehicles → Templ fragment
cmd/web/main.go               ← entry point: wires config, pgx pool, account, gateway engine
```

### Run it

```bash
# Requires DATABASE_URL + SESSION_SECRET in .env (per-user tokens live in Postgres).
go run ./cmd/web
```

What the vehicle dashboard does:

1. Reads the signed-in user's Tesla access token from Postgres via `account.AccessTokenFor`
   (refreshing it first if it is near expiry).
2. Calls `GET /api/1/vehicles` through the `tesla` adapter to list every vehicle on that
   user's Tesla account.
3. Maps each vendor DTO (`VehicleTesla`) to a clean model and renders it as an htmx/Templ
   fragment on the dashboard.
4. Available endpoints `VehicleData()` (full snapshot) and `WakeUp()` (wake a sleeping vehicle)
   are implemented in the adapter and exposed by its `VehicleService` interface, ready for the
   data-collection / richer-dashboard work to consume.

> **How a vehicle is selected:** `GET /api/1/vehicles` returns **every vehicle on the
> authenticated account**, not just one — the dashboard simply renders the whole list. The
> project is one-app → many-users → many-vehicles; per-user scoping comes from each user's own
> stored Tesla tokens, and supporting multiple vehicles per account is already in place.

### Fleet API endpoints used

| Endpoint | What it returns |
|---|---|
| `GET /api/1/vehicles` | Vehicle list with IDs and current state |
| `GET /api/1/vehicles/{id}/vehicle_data` | Full snapshot: charge, climate, location, state |
| `POST /api/1/vehicles/{id}/wake_up` | Wakes a sleeping vehicle before fetching data |

> **Codebase (per call):**
> - `internal/tesla/client.go` → `NewClient()` builds the state-less adapter; `do()` is the shared
>   request helper (attaches the Bearer token from per-call `tesla.Credentials`, maps 401 → `ErrUnauthorized`).
> - `internal/tesla/vehicles.go` → `ListVehicles` calls `GET /api/1/vehicles`; `WakeUp` calls
>   `POST /api/1/vehicles/{id}/wake_up`; `VehicleData` calls `GET /api/1/vehicles/{id}/vehicle_data`.
>   The public contract is the `VehicleService` interface (in `client.go`).
> - Vendor-shaped DTOs `VehicleTesla`, `VehicleDataTesla`, `ChargeStateTesla`, `ClimateStateTesla`,
>   `DriveStateTesla`, `VehicleStateTesla` live in `internal/tesla/types.go`, along with the
>   mandatory miles→km helpers (`BatteryRangeKm()`, `ChargeRateKmh()`, `SpeedKmh()`, `OdometerKm()`).
> - The gateway handler (`internal/gateway/handlers/handlers.go` → `vehiclesFor`) obtains the
>   user's token via `account.AccessTokenFor`, calls `tesla.ListVehicles`, and maps DTOs to a clean
>   model for the Templ fragment. On `tesla.ErrUnauthorized` it currently prompts reconnect; a
>   reactive refresh-on-401 retry is a planned follow-up owned by `account`.

---

## Checklist — Layer 2

| # | Task | Done |
|---|---|---|
| 6 | OAuth flow — access token + refresh token saved to `.env` | ✅ |
| 7 | Virtual key paired with Magus (for commands) | ⬜ |
| 8 | First API call made successfully | ⬜ |

**Back to:** [Layer 1 — Application Registration](./layer1-app-registration.md) for the one-time
app-level setup that must precede this layer.
