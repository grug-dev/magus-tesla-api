## Why

Everything is now in place — accounts (Tier 1), the web gateway (Tier 2), Google login (Tier 3),
and Tesla connect (Tier 4) — but the user still can't *see* anything about their car. This change
delivers the payoff: a dashboard that lists the signed-in user's Tesla vehicles. It completes the
lifecycle "sign in with Google, connect your Tesla, and monitor your vehicles."

## What Changes

- Add gateway routes (signed-in only):
  - `GET /dashboard` — a page listing the user's vehicles.
  - `GET /ui/vehicles` — the htmx fragment (refresh) rendering just the vehicle list.
- The gateway orchestrates the canonical multi-tenant flow (`ai/htmx-go-integration.md`):
  `account.AccessTokenFor(uid)` → wrap in `tesla.Credentials` → `tesla.ListVehicles(creds)` →
  map the `...Tesla` DTOs to a clean view model → render the Templ fragment.
- Graceful states: **no Tesla connected** → prompt to connect; **expired/invalid credentials**
  (`tesla.ErrUnauthorized`) → prompt to reconnect; other errors → a friendly notice.
- The landing page gains a "View your vehicles" link when signed in.
- Wire the `tesla` adapter into the gateway (`tesla.NewClient`).

## Capabilities

### Modified Capabilities
- `gateway`: adds the vehicle dashboard — listing a signed-in user's vehicles via the account and
  tesla modules, with connect/reconnect empty states. (Requirements added to the `gateway` spec.)

### New Capabilities
- None. Uses `account.AccessTokenFor` and `tesla.ListVehicles` through their existing interfaces.

## Impact

- **New:** gateway dashboard handlers/routes, a vehicles fragment + dashboard page, a home link.
- **Wiring:** the `tesla.VehicleService` is added to the gateway `Deps` and built in `cmd/web`.
- **No new dependencies.**
- **Boundary:** the gateway maps `...Tesla` DTOs to a clean presentation model before rendering —
  no vendor DTO reaches a template (`ai/architecture.md` §6, `ai/htmx-conventions.md`).
- **Non-breaking.** Affected: `gateway`; `account` + `tesla` used via interfaces.
