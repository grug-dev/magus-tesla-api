# magus-tesla-api — Project Instructions for Claude

## Builds & local checks — PROJECT OVERRIDE

**This project overrides the global `## Builds` rule in `~/.claude/CLAUDE.md`.** For
`magus-tesla-api` **only**, Claude MAY run the Go build/verify and codegen commands directly
(the owner has authorized it); it need not hand them back to the user. Allowed without asking:

- `go build ./...`, `go vet ./...`, `go test ./...`
- `make build`, `make vet`, `make test`, `make check`, `make bins`
- `sqlc generate` / `make sqlc`, `go mod tidy` / `make tidy`

Still gated (ask / require explicit request first): anything that mutates or drops data —
`make db-reset`, `make migrate-down`, manual `DROP`/`DELETE`. Applying migrations forward
(`make migrate-up`, `make db-setup`) is fine when the user asks for setup. The global build
prohibition remains in force for every **other** project.

## Project Goal

Go modular monolith — the **Agentic Modular Monolith** (see [`ai/architecture.md`](ai/architecture.md)) —
that monitors Tesla vehicles via the Tesla Fleet API. It is **multi-tenant**: it serves
multiple users, each connecting their own Tesla account, with per-user tokens persisted in
a database (the `internal/account/` module). The goal is to turn live and historical
vehicle data into per-user dashboards and automation. Built with strict package separation
so new Tesla API capabilities slot in without touching existing code.

Magus (the project owner's own Tesla) is the first test vehicle, but the platform is **not**
hardwired to a single car or user.

The stack is **Go + htmx** (htmx web layer is planned, not yet built).

---

## Coding Conventions — Read Before Writing Code

Technical coding conventions live in assistant-neutral files under [`ai/`](ai/) (kept
separate from the human-facing Tesla docs in `docs/`). These are the source of truth —
this file only points at them.

- **Before adding, moving, or removing any code** (modules, boundaries, structure), read [`ai/architecture.md`](ai/architecture.md) — the "Agentic Modular Monolith" structure and boundary rules.
- **Before writing or editing any Go code**, read [`ai/go-conventions.md`](ai/go-conventions.md) and follow it exactly.
- **Before writing or editing htmx markup**, read [`ai/htmx-conventions.md`](ai/htmx-conventions.md) (Templ engine).
- **Before wiring htmx to the Go gateway**, read [`ai/htmx-go-integration.md`](ai/htmx-go-integration.md).
- **For any requirement spanning multiple modules**, follow the lead-orchestrator protocol in [`ai/agentic-workflow.md`](ai/agentic-workflow.md).

(The htmx conventions are decided but the web layer isn't built yet. Note: the multi-tenant
design **is live** — `cmd/web` (`internal/account` + `internal/gateway` + `internal/tesla` +
`internal/googleauth`) is the running server and already scopes data per user. The `vehicle`
package was replaced by the `tesla` adapter. `internal/config`/`auth`/`server` are no longer a
smoke test: `internal/config` is shared config reading used by both `cmd/web` and `cmd/setup`;
`internal/auth` and `internal/server` are used only by `cmd/setup`, the standalone one-time
OAuth-capture tool, not the running server. Treat `ai/architecture.md` as the target structure.)

### Non-negotiables (full detail in `ai/go-conventions.md`)

These are always in effect. Do not violate them even if you haven't opened the conventions file:

- **Modular packages are a hard requirement** — every Tesla API concern gets its own package under `internal/`; one concern per package; `cmd/` stays thin (zero business logic).
- **Miles → km conversion is mandatory** — every miles/mph struct field must have a companion `<Field>Km()` / `<Field>Kmh()` value-receiver method using the `milesToKm` constant. Never a JSON-tagged km field; nil-safe for pointer fields.
- **Boundaries are sacred** (detail in `ai/architecture.md`): no HTML outside `internal/gateway/`; no module reads another module's DB/internals; cross-module data flows only through public Go interfaces; the gateway calls interfaces, never a database.

---

## Session Start Protocol

At the start of every session, before writing any code:

1. Remind the user of the current open roadmap items (see **Roadmap** section below).
2. Ask which one they want to work on, or if they have something else in mind.
3. If they are unsure, open the brainstorm section and suggest 2–3 ideas based on what's already built.
4. Agree on the goal for the session before starting.

---

## Token Behavior

Each Tesla connection uses two tokens, **per user**:

- **access token** — expires every 8 hours; sent as `Authorization: Bearer` on every Fleet API call.
- **refresh token** — expires every 3 months, single-use; exchanged for a new access token.

**Multi-tenant model (target):** tokens are stored **per user in a database**, owned by the
`internal/account/` module (see [`ai/architecture.md`](ai/architecture.md) §5). The `tesla`
adapter is stateless — it receives the access token to use as `tesla.Credentials`. Token
**refresh belongs to the `account` module**, not the adapter.


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

- [ ] **`cmd/poller`** — scheduled runner that periodically calls `the module that fetch vehicle data` logic and appends that data to the database. Foundation for all historical data features.


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
| **Drive state** | Speed, heading, shift state | Detect when the vehicle is in motion vs parked |

### Concrete ideas to explore with the user

2. **Daily digest** — every morning: battery level, overnight charge added, current range, where vehicle is parked. Needs `cmd/poller` + `internal/store`.
3. **Battery health log** — record battery level vs odometer over time. Graph degradation. Needs `internal/store`.
4. **Charge session detector** — detect start/end of charging (state changes to/from "Charging"), log kWh added and duration. Needs `cmd/poller`.
5. **Range anxiety guard** — send a notification (Slack, email, push) when battery drops below a configurable threshold. Simple to add to `cmd/poller`.
6. **Geo-fence home alert** — detect when the vehicle leaves or arrives at home coordinates. Needs stored home location + `cmd/poller`.
7. **Software update notifier** — compare `car_version` to last known value, alert on change.
8. **Sentry mode monitor** — log when sentry mode turns on/off.
