# Read dashboard bento — `GET /dashboard` + `GET /ui/dashboard`

> One external entry point, one output. **Backend only** — the adapter/gateway side lives in the
> input-port file that links here. Paths + symbols only; ask CodeGraph for signatures, never
> record line numbers. All KB links below are relative to `kkpa/context/`.

## Entry point

- **Symbol:** `Handler.dashboardFor` — `internal/gateway/handlers/handlers.go`. This is the
  shared core. Two thin route handlers wrap it: `Handler.Dashboard` (full page) and
  `Handler.DashboardFragment` (htmx fragment). They differ only in `render` vs
  `renderFragment`; the data path is identical.
- **Trigger:** `GET /dashboard` and `GET /ui/dashboard`
- **Module:** `gateway`

## Triggered by

- `input-port/gateway/dashboard.md` — the `/dashboard` page (full load, and the
  `vehicle-changed` htmx swap)

## Input / output

- **Input:** the session user id (`currentUID`), the session's selected vehicle resolved by
  `resolveSelectedVehicle` (a Tesla id; `0` when nothing is persisted yet), and "browser
  today" from `browserToday(c)`. No query parameters.
- **Output:** `fragments.DashboardData` rendered as HTML — the full page, or just the
  `"dashboard"` fragment. Always `200`. **A read error never produces a 500**; it degrades to a
  flagged view model instead (see gotchas).

## Flow

1. `Handler.Dashboard` / `Handler.DashboardFragment` — `internal/gateway/handlers/handlers.go` —
   resolve the session user; redirect to `/login` when absent. `Dashboard` additionally calls
   `vehiclesFor` first, so a first-time user's vehicles are seeded before selection runs.
2. `Handler.resolveSelectedVehicle` — `internal/gateway/handlers/handlers.go` — reads the
   session's selected Tesla id, auto-selecting the first OWNER vehicle when unset.
3. `Handler.dashboardFor` — `internal/gateway/handlers/handlers.go` — the core below.
4. `account.Service.RegisteredVehicles` — the account module's port — the account's vehicles.
   Empty ⇒ `NeedsConnect`, return early.
5. `telemetry.Reader.LatestSnapshotsByAccount` — the telemetry module's read port — the latest
   snapshot per vehicle for the whole account, in one query. **See the boundary gotcha below.**
6. `mergeSnapshots` — `internal/gateway/handlers/handlers.go` — indexes the slice by Tesla id
   and picks the selected vehicle's snapshot. Missing ⇒ `HasSnapshot=false`, return early.
7. `mapDashboardSnapshot` — `internal/gateway/handlers/handlers.go` — formats every display
   string (`formatKm`, `°C`, `%`, `km`, charge limit) and computes `IsStale` via `isStale`.
   `dashStatus` collapses the Tesla charging state into `Charging` / `Parked`.
8. `Handler.vehicleImage` — `internal/gateway/vehicle_image.go` — maps
   (`CarType`, `ExteriorColor`) to a `/static/img/*.png` URL, falling back to `defaultCar.png`.

## Database

The gateway performs **no** database access. Both rows below happen inside the owning module,
behind its interface.

| # | Op | Table / entity | Where |
|---|---|---|---|
| 1 | READ | account vehicles | `account.Service.RegisteredVehicles` |
| 2 | READ | `vehicle_snapshots` | `telemetry.Reader.LatestSnapshotsByAccount` — `DISTINCT ON (tesla_id) … ORDER BY tesla_id, captured_at DESC` |

Columns behind the Vehicle Status tiles: `odometer_km`, `inside_temp_c`, `outside_temp_c`,
`charging_state`; plus `car_version`, `captured_at`, and the battery card's
`battery_level_pct`, `battery_range_km`, `charge_limit_soc_pct`.

## Entities involved

- `architecture/telemetry-data-hub.md` — `telemetry.Snapshot` / `vehicle_snapshots`, the
  module that owns this data and who else reads it

## Related use cases

- `use-case/gateway/read-dashboard-history.md` — the `#dashboard-history` region on the same
  page; a separate request with its own window parameters

## Conventions & gotchas

- **The gateway must stop depending on `internal/telemetry`.** `make boundary-guard` fails on
  any `internal/telemetry` import under `internal/gateway/`, and this use case is one of the
  remaining violations. Do **not** add a new `telemetry.*` type or call here. The port and the
  `telemetry.Snapshot` type must move behind another module's interface.
  _Source: `ai/architecture.md` §"Exception: the gateway may not depend on `telemetry` at all";
  `internal/gateway/AGENTS.md` §`Deps.TelemetryReader`._
- **Four distinct degradation states, never a 500.** `NeedsConnect` (no registered vehicles ⇒
  connect prompt), `TelemetryUnavailable` (reader error ⇒ warning `Alert` + identity-only
  bento), `HasSnapshot=false` (registered but no nightly snapshot yet ⇒ `—` tiles, **no**
  alert, it is not an error), and `IsStale` (snapshot older than the threshold ⇒ warning
  badge). An account-read error returns a notice-only shell.
  _Source: `Handler.dashboardFor` doc comment._
- **One batch read, never N+1.** `LatestSnapshotsByAccount` fetches every vehicle's latest
  snapshot in one query even though the page shows one vehicle. Do not replace it with a
  per-vehicle call in a loop.
  _Source: `internal/telemetry/db/query.sql`; `ai/architecture.md` §read-heavy profile._
- **`now` is passed in, never read inside.** `mapDashboardSnapshot` and `isStale` take the
  current time as an argument so the staleness boundary is deterministic in tests. Time comes
  from `internal/clock`, never a raw `time.Now()` — `make tz-guard` enforces it.
  _Source: `architecture/platform-time-zone.md`._
- **`DefaultHistoryHref` is computed here, not in the template.** The handler formats the
  history region's first-load URL so the template emits it verbatim.
  _Source: `defaultHistoryHref` — `internal/gateway/handlers/handlers.go`._
- **`Dashboard` calls `vehiclesFor` before selecting; `DashboardFragment` does not.** The full
  page may need to seed a first-time user's vehicles from Tesla; by fragment time that seed has
  already happened. Do not "simplify" this asymmetry away.
  _Source: `Handler.Dashboard` doc comment._
