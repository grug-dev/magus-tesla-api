# Dashboard page

> **Adapter side:** what the outside world calls, and where it forwards to. **No backend flow
> here** — that lives in the linked use-case files.
>
> "Input port" in this KB means the *driving adapter* — the page, route, or endpoint that
> triggers a use case. (Strict hexagonal reserves "port" for the interface the core exposes;
> this KB deliberately uses the looser sense. Do not go looking for a `Port` interface in the
> code because of this filename.)
>
> All KB links below are relative to `kkpa/context/`.

## Page

- **Route / URI:** `/dashboard`
- **Description:** The signed-in landing page for the **selected** vehicle. A 12-column bento
  grid: an 8-col "Vehicle Status" hero (car image, Odometer / Interior / Exterior / Status
  tiles, software version, last-updated line, stale badge) and a 4-col vital-stats column
  (battery %, range, charge limit) above a self-loading history-charts region.
- **Module:** `gateway`

## Front-end component map

The files that render this page. A UI-only change should never need the use-case file.

| File | Role |
|---|---|
| `internal/gateway/templates/pages/dashboard.templ` | The page. Bento grid, `Vehicle Status` hero, battery card, and the empty `#dashboard-history` container. Wraps everything in `@templ.Fragment("dashboard")` so the htmx swap can re-emit just this region. |
| `internal/gateway/templates/pages/dashboard.go` | Template helpers: `dashSubtitle`, `dashStat` (`—` placeholder), `dashBatteryColorClass` (a thin adapter over `ui.BatteryBandClass`), `dashPctAttr` (returns an `int` for `ui.Progress`), `dashChargeLimit`. |
| `internal/gateway/templates/fragments/dashboard_vm.go` | `fragments.DashboardData` — the logic-free view model. Every metric is a pre-computed display string. |
| `internal/gateway/templates/pages/dashboard_history.templ` | The `#dashboard-history` **contents**: the three charts + the window preset selector. Wrapped in `@templ.Fragment("dashboard-history")`. |
| `internal/gateway/templates/fragments/history_vm.go` | `fragments.HistoryView` / `HistoryChart` / `HistoryBar` — the charts' view model, also fully pre-computed. |
| `internal/gateway/templates/fragments/vehicle_select.templ` | The sidebar vehicle switcher. Fires the `vehicle-changed` event that re-renders this page's bento. |
| `internal/gateway/templates/layouts/` (`BaseAuth`, `nav.go`) | Top nav + left sidebar shell. |
| `internal/gateway/templates/ui/` | The owned kit: `PageHeader`, `Card`, `StatTile`, `Badge`, `Dot`, `Button`, `Alert`, `Icon`, `Progress`. Also `BatteryBandClass` in `ui.go` — the single definition of the battery colour bands, which `dashBatteryColorClass` adapts. Never inline a raw DaisyUI component class — `make ui-guard` fails. |

## Gateway

This project's gateway **is** `internal/gateway/`; there is no second hop to a downstream
service. The rows below are the in-process wiring, not a proxy.

- **Gateway route:** `internal/gateway/gateway.go` registers all three routes on the Gin
  router and injects module ports through `gateway.Deps` → `handlers.Deps`.
- **Auth / transforms:** session cookie → `currentUID(c)`; no user ⇒ `302 /login`. The
  selected vehicle comes from a session value resolved by `resolveSelectedVehicle`
  (auto-selects the first OWNER vehicle when unset). `browserToday(c)` resolves "today"
  from the per-user `browser_tz` cookie, falling back to the platform zone.
- **Files:** `internal/gateway/gateway.go`, `internal/gateway/handlers/handlers.go`,
  `internal/gateway/handlers/history.go`, `internal/gateway/handlers/tz.go`

## Endpoints

The calls this page fires. One row per endpoint; each links to its use case.

| Method + path | Purpose | Use case |
|---|---|---|
| `GET /dashboard` | Full page load. Renders the shell + the bento. | `use-case/gateway/read-dashboard-bento.md` |
| `GET /ui/dashboard` | htmx swap of `#dashboard-content`, triggered by `vehicle-changed from:body` when the sidebar switcher changes vehicle. Same view model, fragment only. | `use-case/gateway/read-dashboard-bento.md` |
| `GET /ui/dashboard/history?start=&end=` | The `#dashboard-history` region. Self-loads via `hx-trigger="load"` on first render, then re-fires on each window-preset click. | `use-case/gateway/read-dashboard-history.md` |

## Conventions & gotchas (page-side)

- **`#dashboard-history` uses `hx-swap="innerHTML"`** so the wrapper element — and its
  `hx-trigger` — survives every swap. Swapping the element itself would remove the trigger.
  _Source: `internal/gateway/templates/pages/dashboard.templ`._
- **The history region inherits `vehicle-changed`** by living inside `#dashboard-content`;
  it needs no trigger of its own for the vehicle switch.
  _Source: `ai/htmx-conventions.md` §"Cross-region refresh"._
- **Templates do no arithmetic, unit conversion, or time math.** If a value needs formatting,
  the handler does it and puts a string on the view model.
  _Source: `ai/htmx-conventions.md` §"No business logic in templates"._
- **Every user-facing label goes through `i18n.T(ctx, key)`**, both ES and EN non-empty.
  `make i18n-guard` fails otherwise; htmx attribute values need an `// i18n:allow:` note.
  _Source: `internal/gateway/AGENTS.md` §i18n._
