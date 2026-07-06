# magus-tesla-api — Project Instructions for Claude

## Project Goal

Personal Go modular monolith to monitor **Magus** (a Tesla) via the Tesla Fleet API.
The goal is to fetch live vehicle data (battery, location, climate, state) for personal
dashboarding and future automation. Built as a clean, extensible Go project with strict
package separation so new Tesla API capabilities can be added without touching existing code.

---

## Session Start Protocol

At the start of every session, before writing any code:

1. Remind the user of the current open roadmap items (see **Roadmap** section below).
2. Ask which one they want to work on, or if they have something else in mind.
3. If they are unsure, open the brainstorm section and suggest 2–3 ideas based on what's already built.
4. Agree on the goal for the session before starting.

---

## Architecture

Standard Go project layout — modular monolith:

- `cmd/` — thin executable entry points, one per concern. No business logic here.
- `internal/` — private packages. Each package owns exactly one concern.

### Current packages

| Package | Path | Responsibility |
|---|---|---|
| `config` | `internal/config/` | Load `.env`, expose typed config, save tokens back to `.env` |
| `auth` | `internal/auth/` | Tesla OAuth URL builder, authorization code exchange, token refresh |
| `server` | `internal/server/` | Gin HTTP server that catches the OAuth redirect on `:8080/callback` |
| `vehicle` | `internal/vehicle/` | Authenticated Fleet API client + all vehicle data types and calls |

### Current commands

| Command | Path | What it does |
|---|---|---|
| `setup` | `cmd/setup/` | One-time OAuth flow — opens browser, catches callback, saves tokens to `.env` |
| `magus` | `cmd/magus/` | Fetches and prints Magus's live vehicle snapshot. Auto-refreshes token on 401. |

---

## Coding Rules

- Every new Tesla API concern gets its own package under `internal/`. Never add charging commands to the vehicle package, never add telemetry to auth, etc.
- `cmd/` files must stay thin — they wire packages together. Zero business logic in `cmd/`.
- Never call `os.Getenv` outside of `internal/config/`. All other packages receive config via function arguments or the `Config` struct.
- All new Fleet API endpoint calls belong in `internal/vehicle/` or a new `internal/<domain>/` package (e.g. `internal/charging/`, `internal/telemetry/`).
- The user wants **modular packages** as a hard requirement — enforce this on every suggestion.
- **Miles → km conversion is mandatory.** Every struct field expressed in miles (or a miles-derived unit like mph) **must** have a companion value-receiver method that returns the metric equivalent, following the `OdometerKm()` pattern:
  - Name it `<Field>Km` for distances and `<Field>Kmh` for speeds/rates (e.g. `BatteryRangeKm()`, `ChargeRateKmh()`, `SpeedKmh()`).
  - Multiply by the package-level `milesToKm` constant (`1.609344`) — never hardcode the factor inline.
  - **Never** add the km value as a JSON-tagged struct field: the Fleet API only sends miles, so km is always **derived**, not unmarshalled.
  - For pointer fields (e.g. `*float64` speed), the method returns a nil-safe pointer (`nil` in → `nil` out).

---

## Running

```bash
# Install / update dependencies (first time or after go.mod changes)
go mod tidy

# One-time OAuth setup — run again only when refresh token expires (every 3 months)
go run ./cmd/setup

# Fetch Magus's live data (auto-refreshes access token if expired)
go run ./cmd/magus
```

---

## Token Behavior (implemented)

- `TESLA_ACCESS_TOKEN` — expires every 8 hours. Used as `Authorization: Bearer` on all API calls.
- `TESLA_REFRESH_TOKEN` — expires every 3 months, single-use. Used to silently get a new access token.
- **Auto-refresh is implemented in `cmd/magus`**: on HTTP 401 from the Fleet API, it calls `auth.RefreshTokens()`, saves both new tokens to `.env` via `config.SaveTokens()`, and retries once.
- `vehicle.ErrUnauthorized` is the sentinel error returned by the client on 401 — use `errors.Is()` to detect it in any future command that needs the same pattern.
- Full token explanation: see `docs/layer2-user-vehicle-access.md` → "Understanding the two tokens".

---

## Security — Files That Must Never Be Committed

| File | Why |
|---|---|
| `.env` | Contains CLIENT_ID, CLIENT_SECRET, ACCESS_TOKEN, REFRESH_TOKEN |
| `private-key.pem` | EC private key for vehicle command signing |
| `public-key.pem` | Already excluded as `*.pem` — copied to Netlify separately |

---

## External Services

| Service | URL | Purpose |
|---|---|---|
| Tesla Fleet API | `https://fleet-api.prd.na.vn.cloud.tesla.com` | Vehicle data and commands |
| Tesla Auth | `https://auth.tesla.com/oauth2/v3` | OAuth 2.0 tokens |
| Netlify — magus-monitor | `https://magus-monitor.netlify.app` | Hosts the EC public key at `/.well-known/appspecific/com.tesla.3p.public-key.pem` |

To redeploy the public key: `netlify deploy --dir=magus-public-key-netlify --prod`

---

## Key Reference

Tesla API setup walkthrough, split by authorization layer (each step references the package/class that implements it). Read before touching auth or setup code:
- `docs/layer1-app-registration.md` — Layer 1 (Steps 1–5): registering the "Magus Monitor" app (Client ID/Secret, EC keys, public-key hosting, partner-account registration).
- `docs/layer2-user-vehicle-access.md` — Layer 2 (Steps 6–8): OAuth login, the two tokens, and fetching vehicle data.
- `docs/post-registration-setup.md` — short index pointing to both.

---

## Roadmap

Open items to discuss at the start of each session. Ask the user which one to tackle.

### High priority

- [ ] **`cmd/poller`** — scheduled runner that periodically calls `cmd/magus` logic and appends Magus's state to a local store. Foundation for all historical data features.
- [ ] **`internal/store`** — package for persisting vehicle snapshots over time (SQLite or JSON log). Required by poller and any dashboard work.

### Medium priority

- [ ] Virtual key pairing (Step 7) — needed before any commands (lock, climate, charge control) can be sent.
- [ ] Expand `internal/vehicle/vehicle.go` with more data fields: tire pressure (TPMS), charge limit, scheduled charging, sentry mode, media state.
- [ ] `.env.example` already exists — add a `Makefile` with `make setup`, `make fetch`, `make poll` targets for convenience.

### Lower priority / future

- [ ] Web dashboard — expose stored data via a simple HTTP API (`cmd/api`) and a frontend.
- [ ] Notifications — Slack or push alert when battery drops below a threshold.
- [ ] Software update tracker — alert when Magus installs a new firmware version.

---

## Brainstorm: What Data Can We Get From Tesla?

Use this section when the user is unsure what to build next. All of these are available
with the current `vehicle_device_data` scope — no new permissions needed.

### Data available right now

| Category | Fields | Ideas |
|---|---|---|
| **Battery** | Level %, estimated range, charging state, charge rate, time to full, charge limit | Range anxiety alert (notify below X%), daily charge report, charge session log |
| **Location** | GPS lat/lng, heading, speed, shift state | Trip detector (driving = speed != null), home/away detection, geo-fence alert |
| **Climate** | Inside/outside temp, climate on/off, driver temp setting | Comfort report, "car is hot" alert when parked in sun |
| **Vehicle state** | Locked, odometer, software version, sentry mode on/off | Mileage tracker, software update notifier, sentry alert |
| **Drive state** | Speed, heading, shift state | Detect when Magus is in motion vs parked |

### Concrete ideas to explore with the user

1. **Trip tracker** — detect when Magus starts moving (speed != null + shift != P), record start/end location and distance. Needs `internal/store`.
2. **Daily digest** — every morning: battery level, overnight charge added, current range, where Magus is parked. Needs `cmd/poller` + `internal/store`.
3. **Battery health log** — record battery level vs odometer over time. Graph degradation. Needs `internal/store`.
4. **Charge session detector** — detect start/end of charging (state changes to/from "Charging"), log kWh added and duration. Needs `cmd/poller`.
5. **Range anxiety guard** — send a notification (Slack, email, push) when battery drops below a configurable threshold. Simple to add to `cmd/magus` or `cmd/poller`.
6. **Geo-fence home alert** — detect when Magus leaves or arrives at home coordinates. Needs stored home location + `cmd/poller`.
7. **Software update notifier** — compare `car_version` to last known value, alert on change.
8. **Sentry mode monitor** — log when sentry mode turns on/off.
