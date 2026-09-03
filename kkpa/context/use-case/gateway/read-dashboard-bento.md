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

- The rendered Vehicle Status card's status-bearing outputs are: locked badge (present /
  absent), sentry badge (present / absent), staleness badge (present / absent), the subtitle's
  charging-derived status word, and a three-tile stat row. Each badge is independently
  driven by its own `*bool`; absence of a value means absence of the badge.

## Flow

1. `Handler.Dashboard` / `Handler.DashboardFragment` — `internal/gateway/handlers/handlers.go` —
   resolve the session user; redirect to `/login` when absent. `Dashboard` additionally calls
   `vehiclesFor` first, so a first-time user's vehicles are seeded before selection runs.
2. `Handler.resolveSelectedVehicle` — `internal/gateway/handlers/handlers.go` — reads the
   session's selected Tesla id, auto-selecting the first OWNER vehicle when unset.
3. `Handler.dashboardFor` — `internal/gateway/handlers/handlers.go` — the core below.
4. `account.Service.RegisteredVehicles` — the account module's port — the account's vehicles.
   Empty ⇒ `NeedsConnect`, return early.
5. `analytics.Reader.LatestMetricsByAccount` — the analytics module's read port — the latest
   precomputed `vehicle_metrics` row per vehicle for the whole account, in one query. This
   read no longer touches `internal/telemetry` at all (see the resolved gotcha below).
6. `mergeVehicleStatuses` — `internal/gateway/handlers/handlers.go` — indexes the
   `[]analytics.VehicleStatus` slice by Tesla id and picks the selected vehicle's status.
   Missing ⇒ `HasSnapshot=false`, return early. (Renamed from `mergeSnapshots` by
   `RM38-gateway-read-dashboard-from-metrics`; same shape, new source type.)
7. `mapDashboardSnapshot` — `internal/gateway/handlers/handlers.go` — formats every display
   string (`formatKm`, `°C`, `%`, `km`, charge limit) and computes `IsStale` via `isStale`.
   `dashStatus` collapses the Tesla charging state into `Charging` / `Parked`. Eight of
   `VehicleStatus`'s fields are pointers (`InsideTempC`, `OutsideTempC`, `CarVersion`,
   `ChargeLimitSocPct`, `ChargingState`, `CapturedAt`, `Locked`, `SentryMode`) — nil never
   fabricates a value, it omits the corresponding display field (see gotchas).
8. `Handler.vehicleImage` — `internal/gateway/vehicle_image.go` — maps
   (`CarType`, `ExteriorColor`) to a `/static/img/*.png` URL, falling back to `defaultCar.png`.

## Database

The gateway performs **no** database access. Both rows below happen inside the owning module,
behind its interface.

| # | Op | Table / entity | Where |
|---|---|---|---|
| 1 | READ | account vehicles | `account.Service.RegisteredVehicles` |
| 2 | READ | `vehicle_metrics` | `analytics.Reader.LatestMetricsByAccount` — `DISTINCT ON (tesla_id) … ORDER BY tesla_id, metric_date DESC` |

Columns behind the Vehicle Status tiles: `odometer_km`, `inside_temp_c`, `outside_temp_c`,
`charging_state`; plus `car_version`, `captured_at`, and the battery card's
`battery_level_pct`, `battery_range_km`, `charge_limit_soc_pct`.

## Entities involved

- `entities/vehicle-metrics/guide.md` — `analytics.VehicleStatus` / `vehicle_metrics`, the
  module that owns this data and who else reads it. `vehicle_metrics` is itself populated
  from `telemetry.Snapshot` by the nightly recompute — see
  `architecture/telemetry-ingest-only.md` for that upstream data, which this use case no
  longer reads directly.

## Related use cases

- `use-case/gateway/read-dashboard-history.md` — the `#dashboard-history` region on the same
  page; a separate request with its own window parameters

- `GET /ui/nav-header` — the navigation vehicle header reads the **same**
  `analytics.Reader.LatestMetricsByAccount` port and shares this use case's nil-`CapturedAt`
  rule (absent capture instant ⇒ Asleep, never Connected, and no relative "last seen" label).
  Not yet curated as its own use-case file.

## Conventions & gotchas

- **This use case's `internal/telemetry` dependency is resolved** (`make boundary-guard`'s
  general rule against a gateway→telemetry import still stands project-wide — see
  `ai/architecture.md` §"Exception: the gateway may not depend on `telemetry` at all" — but
  this specific read no longer violates it). `RM38-gateway-read-dashboard-from-metrics`
  repointed this use case from `telemetry.Reader.LatestSnapshotsByAccount` onto
  `analytics.Reader.LatestMetricsByAccount`, so `dashboardFor`'s own read path is
  `telemetry`-free. The sibling use case `use-case/gateway/read-dashboard-history.md`
  (`SnapshotsByVehicleBetween`) is a **different**, untouched use case that still reads
  `telemetry` directly — do not assume it is also resolved.
  _Source: `internal/gateway/AGENTS.md` §`Deps.AnalyticsReader` / §`Deps.TelemetryReader`._
- **`vehicle_metrics` is a calendar-day grain, one day behind the capture.** Unlike
  `vehicle_snapshots` (one row per poll instant), `vehicle_metrics`'s `metric_date` is the
  snapshot's *effective day*, written by the nightly recompute — so "latest" here typically
  means **yesterday's** row, not today's. A `nil` `CapturedAt` on the returned
  `VehicleStatus` means the row predates the `RM38-analytics-add-vehicle-status-columns`
  migration and has not been recomputed since — `mapDashboardSnapshot` leaves
  `LastUpdated`/`IsStale` at their zero value in that case (never fabricated), and
  `navHeaderFor` treats it as forced-Asleep, never Connected (roadmap D9).
  _Source: `internal/analytics/analytics.go`'s `LatestMetricsByAccount` doc comment;
  `openspec/changes/RM38-gateway-read-dashboard-from-metrics/design.md` D2/D8._
- **Nil pointer fields omit, never fabricate.** Eight `VehicleStatus` fields are pointers
  (`InsideTempC`, `OutsideTempC`, `CarVersion`, `ChargeLimitSocPct`, `ChargingState`,
  `CapturedAt`, `Locked`, `SentryMode`). A nil value means "not yet computed since the
  migration" and the corresponding display field is left empty/omitted (e.g. `"—"` for a nil
  temperature, no Locked/Sentry badge) — it is never defaulted to a fabricated `false`/`0`/
  `""`. See design.md D2's per-field table for the exact rule per field.
  _Source: `openspec/changes/RM38-gateway-read-dashboard-from-metrics/design.md` D2/D3/D7._
- **Four distinct degradation states, never a 500.** `NeedsConnect` (no registered vehicles ⇒
  connect prompt), `TelemetryUnavailable` (reader error ⇒ warning `Alert` + identity-only
  bento — the field name is a historical holdover, the read is now against analytics), `HasSnapshot=false` (registered but no
  stored `vehicle_metrics` row yet ⇒ `—` tiles, **no** alert, it is not an error), and
  `IsStale` (snapshot older than the threshold ⇒ warning badge). An account-read error
  returns a notice-only shell.
  _Source: `Handler.dashboardFor` doc comment._
- **One batch read, never N+1.** `LatestMetricsByAccount` fetches every vehicle's latest
  status in one query even though the page shows one vehicle. Do not replace it with a
  per-vehicle call in a loop.
  _Source: `internal/analytics/db/query.sql`; `ai/architecture.md` §read-heavy profile._
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

- **The Vehicle Status card shows locked/sentry as header badges, and has no "Status" stat
  tile.** The card header row carries the locked badge, the sentry badge and the staleness
  badge together; none suppresses the others, and each appears purely on its own value. The
  mini-stat grid is a single row of exactly three tiles — odometer, interior temp, exterior
  temp. The card **subtitle keeps** the charging-derived status word ("Parked • Software
  v11.1.2"); only the tile was removed. Do not "restore" a Status tile, and do not drop the
  subtitle's status word.
  _Source: spec gateway — Requirement: Dashboard Vehicle Status Card Shows Locked and Sentry-Mode Badges, Not a Status Tile._
- **Badge styles are fixed and three-state.** Locked → success (green); unlocked → error
  (red); sentry on → warning; sentry off → ghost; **absent → no badge at all**. Absence is
  never rendered as "Unlocked" or "Sentry: Off" — the two are different facts and must stay
  visually distinct.
  _Source: spec gateway — Requirement: Dashboard Vehicle Status Card Shows Locked and Sentry-Mode Badges, Not a Status Tile._
- **Badge text, colour and show/hide are computed in Go, never in the template.** A helper
  returns the triple before the template runs; the template only conditionally renders a
  pre-built `ui.Badge`. No nil-check-driven text or colour selection inside the template.
  This is the same "templates contain no business logic" rule the card's number formatting,
  staleness computation and timestamp formatting already follow.
  _Source: spec gateway — Requirement: Dashboard Vehicle Status Card … / Scenario: Templates contain no business logic for badge selection._
- **The badges introduced no new catalogue keys, and must not.** Their text resolves through
  keys that already existed for the vehicle-list card (locked / unlocked / sentry-on /
  sentry-off / not-reported), with `ES` and `EN` both already complete. If you extend the
  badges, reuse before you add.
  _Source: spec gateway — Requirement: Dashboard Vehicle Status Card … / Scenario: No new translation keys are introduced._
- **Both freshness limits are named constants, not magic numbers.** Staleness is 36 h (one
  missed nightly cycle: 24 h + 12 h buffer) and the nav header's connected-freshness window
  is 48 h. The spec requires them to stay named constants in handler code.
  _Source: spec gateway — Requirements: Dashboard renders enriched vehicle cards… / Navigation Vehicle Header._
- **"Row predates status tracking" is NOT the same state as "no row yet".** A row that exists
  but whose status observations are all absent still renders a **normal** card — battery,
  range and odometer show as usual, and only the absent fields degrade individually. It must
  not fall back to the `HasSnapshot=false` placeholder card. Conflating the two hides data the
  row actually has.
  _Source: spec gateway — Requirement: Dashboard renders enriched vehicle cards… / Scenario: A precomputed row that predates status-observation tracking degrades per field, not per card._
- **The gateway performs no unit conversion, in the handler or the template.** Every value
  arrives from the analytics read port already in its display unit (km, °C), because the
  conversion happened once on write. Adding a conversion here is a bug, not a fix.
  _Source: spec gateway — Requirement: Dashboard renders enriched vehicle cards from stored telemetry._
- **No `pgtype` type may appear in any gateway file, and no `db` package may be imported.**
  Neither `internal/telemetry/db` nor `internal/analytics/db`. All access is through the
  `analytics.Reader` and `account.Service` public interfaces.
  _Source: spec gateway — Scenario: Gateway never imports telemetrydb or analyticsdb for this read._
