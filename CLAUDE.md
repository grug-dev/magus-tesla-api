# magus-tesla-api — Project Instructions for Claude

## Project Goal

Personal Go modular monolith to monitor **Magus** (a Tesla) via the Tesla Fleet API.
The goal is to fetch live vehicle data (battery, location, climate, state) for personal
dashboarding and future automation. Built as a clean, extensible Go project with strict
package separation so new Tesla API capabilities can be added without touching existing code.

The stack is **Go + htmx** (htmx web layer is planned, not yet built).

---

## Coding Conventions — Read Before Writing Code

Technical coding conventions live in assistant-neutral files under [`ai/`](ai/) (kept
separate from the human-facing Tesla docs in `docs/`). These are the source of truth —
this file only points at them.

- **Before writing or editing any Go code**, read [`ai/go-conventions.md`](ai/go-conventions.md) and follow it exactly.
- **Before writing or editing htmx markup**, read [`ai/htmx-conventions.md`](ai/htmx-conventions.md).
- **Before wiring htmx to the Go/Gin API**, read [`ai/htmx-go-integration.md`](ai/htmx-go-integration.md).

(The two htmx files are stubs today — the web layer doesn't exist yet — but that is where
those conventions belong once it does.)

### Non-negotiables (full detail in `ai/go-conventions.md`)

These are always in effect. Do not violate them even if you haven't opened the conventions file:

- **Modular packages are a hard requirement** — every Tesla API concern gets its own package under `internal/`; one concern per package; `cmd/` stays thin (zero business logic).
- **Miles → km conversion is mandatory** — every miles/mph struct field must have a companion `<Field>Km()` / `<Field>Kmh()` value-receiver method using the `milesToKm` constant. Never a JSON-tagged km field; nil-safe for pointer fields.

---

## Session Start Protocol

At the start of every session, before writing any code:

1. Remind the user of the current open roadmap items (see **Roadmap** section below).
2. Ask which one they want to work on, or if they have something else in mind.
3. If they are unsure, open the brainstorm section and suggest 2–3 ideas based on what's already built.
4. Agree on the goal for the session before starting.

---

## Token Behavior (implemented)

Operational/domain behavior of the two Tesla tokens. The reusable Go error-handling
**code pattern** for this (the `ErrUnauthorized` sentinel + `errors.Is` + retry-on-401)
is documented in [`ai/go-conventions.md`](ai/go-conventions.md).

- `TESLA_ACCESS_TOKEN` — expires every 8 hours. Used as `Authorization: Bearer` on all API calls.
- `TESLA_REFRESH_TOKEN` — expires every 3 months, single-use. Used to silently get a new access token.
- On HTTP 401 from the Fleet API, `cmd/magus` auto-refreshes: it calls `auth.RefreshTokens()`, saves both new tokens to `.env` via `config.SaveTokens()`, and retries once.
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
