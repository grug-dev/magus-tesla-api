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
  charging-derived status word, and the tile layout below it. Each badge is independently
  driven by its own `*bool`; absence of a value means absence of the badge. Since
  `RM50-gateway-add-travel-progress-subsection`, the tiles are no longer one flat row: a
  left column holds two lifetime tiles (odometer, the 100%-charge count), and a right
  column holds three named subsections, each its own two-tile (or 2×2) row — "Travel
  Progress" (distance travelled, battery used, both from the latest computed day),
  "Tire pressure" (four wheels — FL, FR, RL, RR — each a PSI reading, an up/down trend,
  and its day-over-day delta, added by `RM50-gateway-add-tire-pressure-subsection`),
  and "Interior / Exterior" (interior temp, exterior temp). All three subsections are
  now built; none is a placeholder.

## Flow

1. `Handler.Dashboard` / `Handler.DashboardFragment` — `internal/gateway/handlers/handlers.go` —
   resolve the session user; redirect to `/login` when absent. `Dashboard` additionally calls
   `vehiclesFor` first, so a first-time user's vehicles are seeded before selection runs.
2. `Handler.resolveSelectedVehicle` — `internal/gateway/handlers/handlers.go` — reads the
   session's selected Tesla id, auto-selecting the first OWNER vehicle when unset.
3. `Handler.dashboardFor` — `internal/gateway/handlers/handlers.go` — the core below.
4. `account.Service.RegisteredVehicles` — the account module's port — the account's vehicles.
   Empty ⇒ `NeedsConnect`, return early.
5. `analytics.Reader.LatestMetricsForVehicles` — the analytics module's read port — the latest
   precomputed `vehicle_metrics` row for each vehicle in the set you pass it, in one query.
   It takes `[]vehicleref.Ref`, not an account id: the port no longer filters by account, so
   the handler passes only the vehicles it already resolved for this account. This
   read no longer touches `internal/telemetry` at all (see the resolved gotcha below).
6. `mergeVehicleStatuses` — `internal/gateway/handlers/handlers.go` — indexes the
   `[]analytics.VehicleStatus` slice by Tesla id and picks the selected vehicle's status.
   Missing ⇒ `HasSnapshot=false`, return early. (Renamed from `mergeSnapshots` by
   `RM38-gateway-read-dashboard-from-metrics`; same shape, new source type.)
7. `mapDashboardSnapshot` — `internal/gateway/handlers/handlers.go` — formats every display
   string (`formatKm`, `°C`, `%`, `km`, charge limit) and computes `IsStale` via `isStale`.
   `dashStatus` collapses the Tesla charging state into `Charging` / `Parked`. This mapper
   reads twenty pointer fields of `VehicleStatus`: the nine from `RM38` (`InsideTempC`,
   `OutsideTempC`, `CarVersion`, `ChargeLimitSocPct`, `ChargingState`, `CapturedAt`,
   `Locked`, `SentryMode`, `MaxRangeChargeCounter`), two added by
   `RM50-gateway-add-travel-progress-subsection` tier 2 (`DistanceTraveledKmCalc`,
   `ConsumedPct`), and eight added by `RM50-gateway-add-tire-pressure-subsection` tier 4
   — the four raw readings (`TpmsPressureFLPSI`, `TpmsPressureFRPSI`, `TpmsPressureRLPSI`,
   `TpmsPressureRRPSI`) and the four day-over-day deltas (`TpmsPressureFLPSIDeltaCalc`,
   `TpmsPressureFRPSIDeltaCalc`, `TpmsPressureRLPSIDeltaCalc`, `TpmsPressureRRPSIDeltaCalc`,
   renamed from a bare `Calc` suffix) — nil never
   fabricates a value, it omits the corresponding display field (see gotchas).
   `dashCountOrDash` formats the counter: nil → `"—"`, a reported `0` → `"0"`.
   `dashDistanceOrDash`/`dashBatteryUsedOrDash` follow the same nil → `"—"` rule for the
   travel-progress fields, and `dashPSIOrDash`/`dashTireTrend`/`dashTireDelta` follow the
   equivalent rule for the tire fields (a real `0.0` delta is a known "no change" value
   and still renders its delta line, just with no trend icon — RD14).

   **Nineteen is what this mapper reads — that now matches what the type holds.**
   `RM50-gateway-add-tire-pressure-subsection` (tier 4) was the change that wired the
   eight TPMS fields in; before it, this mapper read only eleven. Read
   `internal/analytics/analytics.go` for the current field list; do not count from here.
8. `Handler.vehicleImage` — `internal/gateway/vehicle_image.go` — maps
   (`CarType`, `ExteriorColor`) to a `/static/img/*.png` URL, falling back to `defaultCar.png`.

## Database

The gateway performs **no** database access. Both rows below happen inside the owning module,
behind its interface.

| # | Op | Table / entity | Where |
|---|---|---|---|
| 1 | READ | account vehicles | `account.Service.RegisteredVehicles` |
| 2 | READ | `vehicle_metrics` | `analytics.Reader.LatestMetricsForVehicles` — `DISTINCT ON (tesla_id) … ORDER BY tesla_id, metric_date DESC` |

Columns behind the Vehicle Status tiles: `odometer_km`, `inside_temp_c`, `outside_temp_c`,
`max_range_charge_counter`, `charging_state`; plus `car_version`, `captured_at`, and the
battery card's `battery_level_pct`, `battery_range_km`, `charge_limit_soc_pct`.

## Entities involved

- `entities/vehicle-metrics/guide.md` — `analytics.VehicleStatus` / `vehicle_metrics`, the
  module that owns this data and who else reads it. `vehicle_metrics` is itself populated
  from `telemetry.Snapshot` by the nightly recompute — see
  `architecture/telemetry-ingest-only.md` for that upstream data, which this use case no
  longer reads directly.

## Related use cases

- `use-case/gateway/read-dashboard-history.md` — the `#dashboard-history` region on the same
  page; a separate request with its own window parameters

- `GET /ui/nav-header` — the sidebar vehicle block reads the **same**
  `analytics.Reader.LatestMetricsForVehicles` port, but it does **not** share this use case's
  `CapturedAt` handling: since MAG-44 it does no capture-instant branching at all. It renders
  the stored battery level and range whatever their age, and an em dash with no bar when there
  is no row. The connected/asleep vocabulary, its 48 h freshness window and the relative
  "last seen" label were deleted and replaced by a **calendar-day** data-age label
  ("hoy" / "ayer" / "hace N días", red for everything except "hoy" — `dataAgeStaleDays` is 1,
  because the nightly poller means today's date is the only healthy state) computed against `browserToday(c)`
  — see `internal/gateway/AGENTS.md` §"Chrome surfaces & the honest vehicle block". That
  label is the block's ONLY use of `CapturedAt`, and a nil value renders nothing. The
  `IsStale` badge on THIS page's Vehicle Status card is a different signal, measured in
  elapsed hours (36 h), and is unaffected.
  Not yet curated as its own use-case file.

## Conventions & gotchas

- **This use case's `internal/telemetry` dependency is resolved** (`make boundary-guard`'s
  general rule against a gateway→telemetry import still stands project-wide — see
  `ai/architecture.md` §"Exception: the gateway may not depend on `telemetry` at all" — but
  this specific read no longer violates it). `RM38-gateway-read-dashboard-from-metrics`
  repointed this use case off the telemetry latest-state port onto
  `analytics.Reader.LatestMetricsForVehicles`, so `dashboardFor`'s own read path is
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
  _Source: `internal/analytics/analytics.go`'s `LatestMetricsForVehicles` doc comment;
  `openspec/changes/RM38-gateway-read-dashboard-from-metrics/design.md` D2/D8._
- **Nil pointer fields omit, never fabricate.** Nineteen `VehicleStatus` fields this mapper
  reads are pointers — the nine from `RM38` (`InsideTempC`, `OutsideTempC`, `CarVersion`,
  `ChargeLimitSocPct`, `ChargingState`, `CapturedAt`, `Locked`, `SentryMode`,
  `MaxRangeChargeCounter`), the two from `RM50` tier 2
  (`DistanceTraveledKmCalc`, `ConsumedPct`), and the eight from `RM50` tier 4
  (`TpmsPressureFLPSI`/`FR`/`RL`/`RR` and `TpmsPressureFLPSIDeltaCalc`/`FR`/`RL`/`RR`). A nil
  value means "not yet computed since the migration" — for `SentryMode` and
  `MaxRangeChargeCounter` it can also mean "not reported this capture", for the two
  tier-2 fields it means the latest computed day has no prior day to derive them against,
  and for the four raw tire fields it means the vehicle did not report TPMS at capture —
  and the corresponding display field is left empty/omitted (e.g. `"—"` for a nil
  temperature, a nil 100%-charge count, a nil distance travelled, or a nil tire pressure,
  no Locked/Sentry badge) — it is never defaulted to a fabricated `false`/`0`/`""`. The
  counter's real `0` is a reading, not an absence, and renders as `"0"`; a real `0.0` tire
  delta is likewise a reading, not an absence — it renders its delta line, only its trend
  icon is suppressed (RD14). See design.md D2's per-field table for the exact rule per
  field.
  _Source: `openspec/changes/RM38-gateway-read-dashboard-from-metrics/design.md` D2/D3/D7;
  `openspec/changes/archive/gateway/2026-09-08-RM50-gateway-add-travel-progress-subsection/design.md` D2;
  `openspec/changes/RM50-gateway-add-tire-pressure-subsection/design.md` D3/D5._
- **Four distinct degradation states, never a 500.** `NeedsConnect` (no registered vehicles ⇒
  connect prompt), `TelemetryUnavailable` (reader error ⇒ warning `Alert` + identity-only
  bento — the field name is a historical holdover, the read is now against analytics), `HasSnapshot=false` (registered but no
  stored `vehicle_metrics` row yet ⇒ `—` tiles, **no** alert, it is not an error), and
  `IsStale` (snapshot older than the threshold ⇒ warning badge). An account-read error
  returns a notice-only shell.
  _Source: `Handler.dashboardFor` doc comment._
- **One batch read, never N+1.** `LatestMetricsForVehicles` fetches every vehicle's latest
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
  badge together; none suppresses the others, and each appears purely on its own value.
  Below the badge row, a 12-col inner grid splits the tiles: a left column (odometer,
  100%-charge count, stacked) beside the vehicle image, and a right column of named
  subsections — "Travel Progress" (distance travelled, battery used), "Tire pressure"
  (four wheels — FL, FR, RL, RR — each a PSI reading, an up/down trend, and its
  day-over-day delta), and "Interior / Exterior" (interior temp, exterior temp), each
  its own tile row (`RM50-gateway-add-travel-progress-subsection` added the first and
  third; `RM50-gateway-add-tire-pressure-subsection` filled in the middle one; MAG-47
  had added the fourth tile to the earlier flat row, since replaced by this layout).
  The card **subtitle keeps** the
  charging-derived status word ("Parked • Software v11.1.2"); only the tile was removed.
  Do not "restore" a Status tile, and do not drop the subtitle's status word.
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
- **All three Travel Progress tiles compute a real direction from their own day-over-day delta.** Distance travelled, Battery used, and Efficiency each read their own `*DeltaCalc` field and show "up" or "down" from its sign, the same mechanism tyre pressure already used. Only Efficiency's icon is coloured (green for good, red for bad) — Distance travelled and Battery used render the same direction in a shared neutral colour, because driving more or less is not itself good or bad news.
  _Source: spec gateway — Requirement: Dashboard Travel Progress Subsection._
- **Trend colours are semantic tokens, never a hardcoded value.** The "increasing" and "decreasing" indicators each resolve to their own semantic colour so the three themes (apex, graphite, halloween) all render correctly. `make ui-guard` fails a raw colour.
  _Source: spec gateway — Requirement: Dashboard Travel Progress Subsection._
- **A reserved subsection renders nothing at all — no heading, no description, no empty grid.** The layout may hold a position for a metric a later capability adds. Until that capability ships, the section is absent from the markup, not present and empty. The module has no exception for "not built yet". All three subsections are built today, so nothing is reserved right now; keep the rule for the next one.
  _Source: spec gateway — Requirement: Dashboard Vehicle Status Panel Groups Metrics Into Named Subsections._
- **No subsection adds a read of its own.** Travel Progress and all four tyre values come from the same `LatestMetricsForVehicles` row the card already fetches. No new query, no new port method, no Tesla Fleet API call. Keep it that way — this is the read-heavy path.
  _Source: spec gateway — Requirement: Dashboard Travel Progress Subsection; Requirement: Dashboard Tire Pressure Subsection._
- **A tyre tile has THREE states, and two of them look the same on purpose.** A positive change shows the "increased" icon; a negative change shows "decreased"; an **exactly zero** change and an **absent** change both show **no icon**. They are told apart by the numeric line instead: a zero change still prints its line, an absent change prints none. Never add a third "no change" glyph, and never print a fabricated `0.0` for an absent reading — that would erase the difference between "pressure held steady" and "we have no reading".
  _Source: spec gateway — Requirement: Dashboard Tire Pressure Subsection._
- **Every wheel is judged on its own.** One wheel missing its reading or its delta never changes what the other three render. Do not treat the four tiles as a group.
  _Source: spec gateway — Requirement: Dashboard Tire Pressure Subsection._
- **There is exactly one way to render a trend indicator on this page.** Tyre pressure reuses the mechanism Travel Progress introduced. A second, independent way of drawing an arrow is forbidden by the requirement itself, not merely discouraged.
  _Source: spec gateway — Requirement: Dashboard Tire Pressure Subsection._
