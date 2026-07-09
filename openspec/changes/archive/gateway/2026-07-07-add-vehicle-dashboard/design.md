## Context

This is the capstone tier: it wires the full request flow from `ai/htmx-go-integration.md`
end-to-end for the first time. All dependencies exist; the work is orchestration + presentation.
Decisions self-resolved for the single-VPS goal (no new services).

## Goals / Non-Goals

**Goals**
- List a signed-in user's vehicles (name, VIN, state) on an htmx page + refreshable fragment.
- Handle the real-world empty states: not connected, expired credentials, adapter error.

**Non-Goals**
- Per-vehicle detail, live telemetry, charging/climate data (future domain modules + tiers).
- Waking sleeping vehicles, polling, or historical storage (`cmd/poller`, later).

## Decisions

### Orchestrate account → tesla; map DTOs to a clean view model
The dashboard handler calls `account.AccessTokenFor(ctx, uid)` for the current user, wraps the
token in `tesla.Credentials`, and calls `tesla.ListVehicles`. It then maps each `VehicleTesla`
DTO to a suffix-free presentation model (`fragments.Vehicle{DisplayName, VIN, State}`) before
rendering — vendor DTOs never enter a template (§6). The gateway stays a translator; it holds no
state and reaches modules only through their interfaces.

### Explicit empty/error states (not blank pages)
- `account.ErrNoTeslaConnection` → "no Tesla connected" with a Connect link.
- `tesla.ErrUnauthorized` (detected with `errors.Is`) → "reconnect your Tesla" — the token expired
  and `account`'s single refresh didn't recover it.
- Any other error → a friendly notice (no Go error string leaked to the page, per the htmx
  response conventions).

### Testable core: `vehiclesFor(ctx, uid)`
The listing/mapping/error logic lives in `vehiclesFor(ctx, uid) fragments.VehiclesData`, decoupled
from `gin.Context` and the session. Because `account.Service` and `tesla.VehicleService` are
interfaces, this is unit-tested with fakes (no DB, no real Tesla) for all four states. The thin
handlers just guard auth (`currentUID`) and render.

### Fragment-first (same page serves full load + htmx refresh)
`pages.Dashboard` wraps the list in `@templ.Fragment("vehicles")`; `GET /dashboard` renders the
whole page, `GET /ui/vehicles` renders only the fragment for the Refresh button — one component,
no duplicate markup.

## Routes

| Route | Auth | Purpose |
|---|---|---|
| `GET /dashboard` | required | Vehicle list page |
| `GET /ui/vehicles` | required | Vehicle list fragment (htmx refresh) |

## Risks / Trade-offs

- **Vehicles may be asleep** — `ListVehicles` still returns them with their state; we display
  state and do not wake them (waking is a future, cost-aware action).
- **Live Tesla calls per view** — acceptable for a personal app; a future `cmd/poller` + cache
  would reduce API calls if needed.

## Open Questions
- **Auto-refresh interval** (htmx polling) — deferred; manual Refresh for now to avoid needless
  Tesla API calls (and cost) on a small VPS.
