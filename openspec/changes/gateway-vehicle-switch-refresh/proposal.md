# Proposal: gateway-vehicle-switch-refresh

## Why

The gateway already ships a multi-vehicle **context switcher** in the nav header, but the
`gateway` spec still declares "A multi-vehicle selector is out of scope," and it has no
requirement for what a switch actually *does* to the page. This change brings the spec back in
sync with shipped, tested behavior: switching the active vehicle now refreshes the per-vehicle
regions of the current page (the dashboard bento and the manual-records content) in place —
without a full reload — driven by an `HX-Trigger: vehicle-changed` event. The code and the
`ai/*.md` / `AGENTS.md` docs are already updated; only the formal spec lags.

## What Changes

- **MODIFY "Navigation Vehicle Header"** — remove the stale "A multi-vehicle selector is out of
  scope" statement. Document the shipped switcher: a `<select>` of the account's registered
  vehicles that `POST`s to `/ui/vehicle/select`, is CSRF-protected (`csrf_vehicle_select`) and
  tenant-ownership-validated, persists the chosen vehicle (`TeslaID` + `VIN`) in the session,
  auto-selects the first `OWNER` vehicle when none is chosen, and on a successful switch
  responds with the `HX-Trigger: vehicle-changed` header.
- **ADD requirement "Vehicle-Scoped Cross-Region Refresh"** — a vehicle switch refreshes the
  current page's per-vehicle regions with no full reload: the switcher emits
  `HX-Trigger: vehicle-changed` (bubbles to `<body>`); the dashboard's `#dashboard-content`
  region subscribes with `hx-trigger="vehicle-changed from:body"` and re-fetches
  `GET /ui/dashboard`; the manual-records `#charges-content` region subscribes likewise and
  re-fetches `GET /ui/charges`. Every per-vehicle read scopes to the selected vehicle's
  `TeslaID` via `resolveSelectedVehicle`.
- Not breaking. Retroactive documentation of already-merged, already-tested behavior — no code
  change is required by this proposal.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `gateway` — modifies the "Navigation Vehicle Header" requirement and adds the
  "Vehicle-Scoped Cross-Region Refresh" requirement. (The stale "Vehicle Dashboard"
  list-vs-bento drift is deliberately **out of scope** here — a separate cleanup.)

## Impact

- **Spec:** `openspec/specs/gateway/spec.md` (delta only; no other capability spec changes).
- **Code (already merged, documented for traceability):** `internal/gateway/handlers/handlers.go`
  (`DashboardFragment`, `VehicleSelect` `HX-Trigger`), `internal/gateway/handlers/charges.go`
  (`ChargesContentFragment`, variadic `renderFragment`), `internal/gateway/gateway.go`
  (`GET /ui/dashboard`, `GET /ui/charges` routes), and the `dashboard.templ` / `charges.templ`
  swappable regions.
- **Read paths:** no new database access. The refresh handlers reuse the existing per-request
  reads (`account.RegisteredVehicles`, `telemetry.Reader.LatestSnapshotsByAccount`,
  `manualcharge.Reader.ListEntriesByVehicle`) already exercised by the full-page renders;
  scoping to one `TeslaID` narrows, never widens, those reads.
- **No API/dependency changes.**
