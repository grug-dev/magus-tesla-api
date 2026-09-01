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

## Flow

1. `Handler.DashboardHistoryFragment` — `internal/gateway/handlers/history.go` — resolves the
   user; redirects to `/login` when absent. Resolves `today` **once** per request.
2. `parseHistoryRange` — `internal/gateway/handlers/history.go` — parses/validates
   `?start=&end=`, defaulting to the 6-day window. Invalid ⇒ render the empty state at `400`
   and stop.
3. `Handler.resolveSelectedVehicle` — `internal/gateway/handlers/handlers.go` — no vehicle ⇒
   all three charts empty, but **with** the preset selector, at `200`.
4. `telemetry.Reader.SnapshotsByVehicleBetween` — the telemetry module's read port — snapshots
   for the window, read from `readStart = start - 1 day`. **See the boundary gotcha below.**
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
| 1 | READ | `vehicle_snapshots` | `telemetry.Reader.SnapshotsByVehicleBetween` |
| 2 | READ | `vehicle_metrics` | `analytics.Reader.OdometerDeltaByDay` |
| 3 | READ | `vehicle_metrics` | `analytics.Reader.ConsumedByDay` |

## Entities involved

- `entities/vehicle-metrics/guide.md` — `vehicle_metrics` and its `_calc` columns, the
  precomputed read model behind both analytics series
- `architecture/telemetry-data-hub.md` — `telemetry.Snapshot` / `vehicle_snapshots`

## Related use cases

- `use-case/gateway/read-dashboard-bento.md` — the Vehicle Status + battery bento on the same
  page; a separate request

## Conventions & gotchas

- **The gateway must stop depending on `internal/telemetry`.** `make boundary-guard` fails on
  any `internal/telemetry` import under `internal/gateway/`, and
  `SnapshotsByVehicleBetween` here is one of the remaining violations. Do not add another.
  _Source: `ai/architecture.md` §"Exception: the gateway may not depend on `telemetry` at all"._
- **The snapshot read starts one day early.** `readStart = start - 1 day`, because a per-day
  delta needs the day *before* the window's first day. Narrowing this to `start` silently
  drops the first bar.
  _Source: `Handler.buildHistoryView` comment — `internal/gateway/handlers/history.go`._
- **`?start=&end=`, never `?days=N`.** Every gateway date filter on this project uses explicit
  calendar dates.
  _Source: `ai/architecture.md`; `internal/gateway/AGENTS.md` §"HTTP date-filter"._
- **The default window ends YESTERDAY, not today.** The nightly batch captures today's data
  tomorrow, so `end=today` would always render an empty last bar.
  _Source: `defaultHistoryHref` — `internal/gateway/handlers/handlers.go`._
- **The two analytics series are PRECOMPUTED and SPARSE.** They read `vehicle_metrics`; they do
  not compute on the fly, and a day with no data is simply absent from the slice — the builder
  must tolerate gaps rather than assume one row per day.
  _Source: `analytics.Reader` doc comments — `internal/analytics/analytics.go`._
- **`today` is resolved once per request** and threaded through `parseHistoryRange`,
  `buildHistoryPresets`, and the view builder. Repeated `browserToday(c)` calls re-run
  `time.LoadLocation` and open a day-boundary race.
  _Source: MAG-7 review finding R1-7 — `internal/gateway/handlers/history.go`._
- **The two error paths render differently on purpose.** A malformed window ⇒ `400`, empty
  charts, **no** selector. No vehicle ⇒ `200`, empty charts, **with** the selector so the user
  can still change the window.
  _Source: `Handler.DashboardHistoryFragment` — design D1._
