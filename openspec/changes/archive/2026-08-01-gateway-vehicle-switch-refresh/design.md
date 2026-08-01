# Design: gateway-vehicle-switch-refresh

## Context

The nav-header vehicle switcher was already live (session-persisted selection,
`resolveSelectedVehicle` first-`OWNER` auto-select, CSRF + tenant validation), but switching
only re-rendered the nav header — the dashboard bento and manual-records content kept showing
the vehicle loaded at page open until a full reload. This change wires the switch to refresh
those per-vehicle regions in place. The implementation is already merged and tested; this
OpenSpec change documents the behavior and corrects the stale "multi-vehicle selector is out of
scope" statement in the gateway spec. `ai/htmx-conventions.md` and `internal/gateway/AGENTS.md`
already carry the convention.

## Goals / Non-Goals

**Goals**
- Formalize: a vehicle switch refreshes the current page's per-vehicle regions with no full reload.
- Formalize: per-vehicle reads scope to the selected vehicle's `TeslaID`.

**Non-Goals**
- The stale "Vehicle Dashboard" requirement (still describes a multi-vehicle *list*, whereas the
  live dashboard is a single-vehicle *bento*) — a separate cleanup, explicitly out of scope.
- Any new module, port, DB column, or migration.

## Decisions

### Event-driven `HX-Trigger` over out-of-band (`hx-swap-oob`) swap

The switcher lives in the nav header, present on every page. On a switch it emits
`HX-Trigger: vehicle-changed`; each per-vehicle region subscribes with
`hx-trigger="vehicle-changed from:body"` and re-fetches its own `/ui` fragment.

- **Why over OOB:** an OOB swap would couple `VehicleSelect` to the dashboard/charges markup and
  waste work on pages where those regions are absent. The event keeps the switcher
  page-agnostic — it fires once, regions opt in — so adding a future per-vehicle page touches
  only that page (change-locality). `from:body` is required because htmx custom events bubble to
  `<body>` (verified against htmx docs).

### `innerHTML` swap with the listening wrapper outside the fragment

Each region is `<div id="…-content" hx-get hx-trigger hx-swap="innerHTML">` wrapping a
`@templ.Fragment(...)`. The wrapper sits **outside** the fragment so the `innerHTML` swap
replaces only the inner content and keeps the listening element (and its `hx-trigger`) in the
DOM across refreshes. The refresh handlers render the same page component filtered to the
region's fragment(s) via `renderFragment` — one template tree, two entry points (full page vs.
fragment), mirroring the existing `charges-list` / `nav-header` pattern.

### Charges refreshes both content fragments in one response

`GET /ui/charges` renders `charges-create-form` + `charges-list` together (variadic
`renderFragment`), because both are vehicle-scoped: the list is filtered by `TeslaID` and the
create form's picker defaults to the selected vehicle. `#charges-content` is itself the flex
container, so both fragments swap into it while its layout + `hx-trigger` persist.

## Risks / Trade-offs

- [A region forgets to subscribe and goes stale after a switch] → The `AGENTS.md`
  "Vehicle-scoped reads" rule makes subscription mandatory for any per-vehicle page; the two
  live pages are covered and tested.
- [`HX-Trigger` header dropped by an intermediary] → Same-origin, same-process response; the
  header is set before `renderFragment`, and `templ.Handler` does not clear existing headers
  (asserted by `TestVehicleSelect_FiresVehicleChangedTrigger`).

## Migration Plan

None. Behavior is already deployed; no data or API migration. Rollback would be reverting the
merged gateway commit — not required by this documentation change.

## Open Questions

None.
