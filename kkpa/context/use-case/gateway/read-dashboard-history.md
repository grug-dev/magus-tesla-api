# Read dashboard history charts — `GET /ui/dashboard/history`

> One external entry point, one output. **Backend only** — the adapter/gateway side lives in the
> input-port file that links here. Paths + symbols only; ask CodeGraph for signatures, never
> record line numbers. All KB links below are relative to `kkpa/context/`.

## Entry point

- **Symbol:** `Handler.DashboardHistoryFragment` — `internal/gateway/handlers/history.go`
- **Trigger:** `GET /ui/dashboard/history?start=YYYY-MM-DD&end=YYYY-MM-DD`
- **Module:** `gateway`

## Triggered by

- `input-port/gateway/dashboard.md` — the `#dashboard-history` region of the `/dashboard`
  page. Self-loads on first render (`hx-trigger="load"`), then re-fires on each window-preset
  click.

## Input / output

- **Input:** the session user id; `?start=` and `?end=` calendar dates (both omitted ⇒ a
  default 6-day window, `historyRangeWindowDays`); the session's selected vehicle;
  "browser today" from `browserToday(c)`.
- **Output:** the `"dashboard-history"` fragment — three bar charts (odometer km/day,
  battery %/day, consumed %/day) plus the window-preset selector, as
  `fragments.HistoryView`. `200` normally; `400` with the empty-state placeholder and **no**
  preset selector when the window is malformed.
- **All three charts read exclusively through `analytics.Reader`** — battery via
  `BatteryLevelByDay`, odometer via `OdometerDeltaByDay`, consumed via `ConsumedByDay`.
  Since RM40 the gateway does not name `internal/telemetry` anywhere, for any purpose.

## Flow

1. `Handler.DashboardHistoryFragment` — `internal/gateway/handlers/history.go` — resolves the
   user; redirects to `/login` when absent. Resolves `today` **once** per request.
2. `parseHistoryRange` — `internal/gateway/handlers/history.go` — parses/validates
   `?start=&end=`, defaulting to the 6-day window. Invalid ⇒ render the empty state at `400`
   and stop.
3. `Handler.resolveSelectedVehicle` — `internal/gateway/handlers/handlers.go` — no vehicle ⇒
   all three charts empty, but **with** the preset selector, at `200`.
4. `analytics.Reader.BatteryLevelByDay` — the analytics module's read port — precomputed
   per-day battery level + range, read over exactly `[start, end]` (**no lookback** — RM40).
5. `analytics.Reader.OdometerDeltaByDay` — the analytics module's read port — precomputed
   per-day distance.
6. `analytics.Reader.ConsumedByDay` — the analytics module's read port — precomputed per-day
   battery-consumed percentage.
7. `buildHistoryPresets` + the view builder — `internal/gateway/handlers/history.go` — turn the
   three series into `fragments.HistoryChart` / `HistoryBar` values with pre-computed labels,
   heights, and axis ticks.

## Database

The gateway performs **no** database access. Every row happens inside the owning module,
behind its interface.

| # | Op | Table / entity | Where |
|---|---|---|---|
| 1 | READ | `vehicle_metrics` | `analytics.Reader.BatteryLevelByDay` |
| 2 | READ | `vehicle_metrics` | `analytics.Reader.OdometerDeltaByDay` |
| 3 | READ | `vehicle_metrics` | `analytics.Reader.ConsumedByDay` |

## Entities involved

- `entities/vehicle-metrics/guide.md` — `vehicle_metrics`, its `_calc` columns and its raw
  per-day observations: the single precomputed read model behind **all three** series
  (`BatteryLevelByDay`, `OdometerDeltaByDay`, `ConsumedByDay`)
- `architecture/telemetry-data-hub.md` — `vehicle_snapshots`, the upstream source
  `vehicle_metrics` is derived FROM. Background only: since RM40 this use case does not read
  it and the gateway cannot reach it.

## Related use cases

- `use-case/gateway/read-dashboard-bento.md` — the Vehicle Status + battery bento on the same
  page; a separate request

## Conventions & gotchas

- **The gateway does NOT depend on `internal/telemetry` — and `make boundary-guard` now
  enforces a clean state, not a migration.** RM40 removed the last reference; a new
  `internal/telemetry` import under `internal/gateway/` is a regression, not known debt.
  Never silence the guard with `// boundary:allow:`.
  _Source: `ai/architecture.md` §"Exception: the gateway may not depend on `telemetry` at all"._
- **There is NO gateway-level lookback for any of the three charts.** The battery fetch used to
  compute `readStart = start - 1 day` when it read raw snapshots; it no longer does, because
  `vehicle_metrics.metric_date` is already the effective day. Re-introducing a lookback would
  fetch a row nothing renders.
  _Source: `Handler.buildHistoryView` — `internal/gateway/handlers/history.go` (RM40 D5)._
- **`?start=&end=`, never `?days=N`.** Every gateway date filter on this project uses explicit
  calendar dates.
  _Source: `ai/architecture.md`; `internal/gateway/AGENTS.md` §"HTTP date-filter"._
- **The default window ends YESTERDAY, not today.** The nightly batch captures today's data
  tomorrow, so `end=today` would always render an empty last bar.
  _Source: `defaultHistoryHref` — `internal/gateway/handlers/handlers.go`._
- **All THREE analytics series are PRECOMPUTED and SPARSE.** They read `vehicle_metrics`; they do
  not compute on the fly, and a day with no data is simply absent from the slice — the builder
  must tolerate gaps rather than assume one row per day. (Was "two" before RM40 moved the
  battery series onto `analytics.Reader` as well.)
  _Source: `analytics.Reader` doc comments — `internal/analytics/analytics.go`._
- **`today` is resolved once per request** and threaded through `parseHistoryRange`,
  `buildHistoryPresets`, and the view builder. Repeated `browserToday(c)` calls re-run
  `time.LoadLocation` and open a day-boundary race.
  _Source: MAG-7 review finding R1-7 — `internal/gateway/handlers/history.go`._
- **The two error paths render differently on purpose.** A malformed window ⇒ `400`, empty
  charts, **no** selector. No vehicle ⇒ `200`, empty charts, **with** the selector so the user
  can still change the window.
  _Source: `Handler.DashboardHistoryFragment` — design D1._
- **The battery chart reads `analytics.Reader.BatteryLevelByDay`, NOT telemetry** — changed by
  RM40 (MAG-41). The rejected alternative was a gateway-local interface still backed by
  telemetry: that satisfies `make boundary-guard`'s letter while keeping the runtime dependency
  the guard exists to prevent.
  _Source: spec gateway — Requirement: Dashboard History Charts._
- **A day the nightly recalculation watermark has not reached renders as the SAME empty bar as a
  day with no data at all** — an accepted, bounded, self-healing, typically single-day-wide gap
  from reading precomputed `vehicle_metrics` instead of raw `vehicle_snapshots`. Deliberately
  not backfilled, and **not a new UI state**. Do not "fix" it by widening the window or falling
  back to raw snapshots.
  _Source: spec gateway — Requirement: Dashboard History Charts._
- **The three reads fail INDEPENDENTLY** — a battery-read error empties only the battery chart,
  an odometer-read error only the odometer chart, a consumed-read error only the consumed chart.
  None may blank a sibling that already succeeded.
  _Source: spec gateway — Requirement: Dashboard History Charts._
- **Bucket on each port's returned date VERBATIM** — never re-project through `effectiveDayUTC`.
  The port's date is already a final bucket key. This already governed the consumed and odometer
  charts; RM40 brought the battery chart under it too.
  _Source: spec gateway — Requirement: Dashboard History Charts._
- **The battery bar's value, its absolute 0–100 scale, and the absence of any delta or clamp are
  unchanged by RM40** — only the port changed. A moved battery bar is a regression, not an
  intended consequence of the port swap.
  _Source: spec gateway — Requirement: Dashboard History Charts._
- **The i18n key `KeyHistoryNoSnapshotTooltip` now reads slightly wrong** — it says "no
  snapshot", but an empty bar means "no precomputed metric row". Flagged deliberately, NOT
  renamed: renaming a bilingual catalogue key was outside MAG-41's scope.
  _Source: spec gateway — Requirement: Dashboard History Charts._
