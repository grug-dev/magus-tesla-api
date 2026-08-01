# Tasks: gateway-vehicle-switch-refresh

> Retroactive change: the implementation is already merged and tested. Tasks are recorded here
> for traceability and are checked off to reflect the shipped state; verification is re-running
> the existing gate (`make check`).

## 1. Switcher fires the refresh event

- [x] 1.1 `VehicleSelect` sets `HX-Trigger: vehicle-changed` on a successful switch, before rendering the nav-header fragment (`internal/gateway/handlers/handlers.go`).

## 2. Dashboard region subscribes and refreshes

- [x] 2.1 Wrap the dashboard bento in `#dashboard-content` (`hx-get="/ui/dashboard"`, `hx-trigger="vehicle-changed from:body"`, `hx-swap="innerHTML"`) around a `@templ.Fragment("dashboard")` block (`internal/gateway/templates/pages/dashboard.templ`).
- [x] 2.2 Add `DashboardFragment` handler (renders the `dashboard` fragment for the selected vehicle) and register `GET /ui/dashboard` (`handlers.go`, `gateway.go`).

## 3. Manual-records region subscribes and refreshes

- [x] 3.1 Make `#charges-content` the vehicle-scoped swap region (`hx-get="/ui/charges"`, `hx-trigger="vehicle-changed from:body"`, `hx-swap="innerHTML"`) wrapping the create-form + list fragments (`internal/gateway/templates/pages/charges.templ`).
- [x] 3.2 Add `ChargesContentFragment` handler (renders `charges-create-form` + `charges-list` scoped to the selected `TeslaID`, fresh CSRF token) and register `GET /ui/charges`; make `renderFragment` variadic (`charges.go`, `handlers.go`, `gateway.go`).

## 4. Scoping guard

- [x] 4.1 Both refresh handlers resolve the active vehicle via `resolveSelectedVehicle` and scope reads to its `TeslaID`.
- [x] 4.2 Document the rule in `internal/gateway/AGENTS.md` ("Vehicle-scoped reads — always send the selected TeslaID") and the pattern in `ai/htmx-conventions.md` ("Cross-region refresh via HX-Trigger").

## 5. Tests + verification

- [x] 5.1 `TestVehicleSelect_FiresVehicleChangedTrigger`, `TestDashboardFragment_RendersSelectedVehicle`, `TestChargesContentFragment_ScopedToSelectedVehicle`, `TestChargePage_SubscribesToVehicleChanged`.
- [x] 5.2 `make check` (build + vet + ui-guard + tests) passes.
