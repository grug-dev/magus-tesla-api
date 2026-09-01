# RM38 — Dashboard Vehicle Status from `analytics.vehicle_metrics`

Source ticket: MAG-12 — https://linear.app/magus-monitor/issue/MAG-12/show-fields-on-vehicle-status-section

## Intention

The gateway stops reading `telemetry.vehicle_snapshots` for "the latest state of a
vehicle" and reads the analytics module's precomputed `vehicle_metrics` read model
instead. `vehicle_metrics` gains the eight columns that makes it a complete substitute,
and the dashboard's Vehicle Status card shows whether the car is **locked** and whether
**sentry mode** is on.

## Findings that shaped this roadmap (read before touching anything)

The ticket's three numbered sections are each slightly narrower than the code requires.
All three gaps were found by reading the code and settled with the owner before any
artifact was written.

| Ticket says | Code actually requires | Settled in |
|---|---|---|
| mirror **5** fields | `mapDashboardSnapshot` reads **nine** `telemetry.Snapshot` fields; three of them (`ChargingState`, `ChargeLimitSocPct`, `CapturedAt`) are neither in the ticket nor in `vehicle_metrics` | **D1** |
| repoint `dashboardFor` | `LatestSnapshotsByAccount` has **four** gateway call sites, not one | **D3** |
| "Remove the Status field" | `StatusLabel` feeds **two** places — the stat tile *and* the card subtitle `dashSubtitle` ("Parked • Software v11.1.2") | **D4** |

Two further facts constrain the design:

- **`vehicle_metrics` is a daily read model, not a snapshot table.** Its grain is
  `(account_id, tesla_id, metric_date)`, where `metric_date` is the snapshot's
  *effective* day — `CalendarDay(CapturedDate, UTC) − 1` (`recalculate.go:143`). "Latest
  state" therefore means the row with the greatest `metric_date`, and it describes
  *yesterday*, exactly as the current dashboard already does.
- **A backfill may not be written in SQL.** An `internal/analytics` migration that reads
  `telemetry.vehicle_snapshots` is precisely the cross-module database access
  `ai/architecture.md` §2 forbids. The repo's established, boundary-clean alternative is
  a watermark reset (`20260822000002_reset_vehicle_metric_watermarks.sql`) — considered
  and **declined** here under D2.

## Decisions (binding — settled with the user before any artifact was written)

**D1 — Add eight columns, not five.** `vehicle_metrics` gains the ticket's five
(`locked`, `sentry_mode`, `car_version`, `inside_temp_c`, `outside_temp_c`) **plus**
`charging_state`, `charge_limit_soc_pct` and `captured_at`. The extra three are what
`mapDashboardSnapshot` needs for the card subtitle, the Battery card's "Limit 80%" line,
and the `LastUpdated` / `IsStale` badge respectively.

Rejected — *"add only the ticket's five"*: `GET /dashboard` would then have to call both
the analytics port **and** the telemetry port and merge them by `tesla_id`, permanently,
which defeats section 2's stated purpose ("read from `vehicle_metrics` **instead of**
`telemetryReader`"). Rejected — *"add five plus `captured_at` and delete the UI that
needs the other two"*: removes working UI the ticket never asked to remove.

The ticket's `outsite_temp_c` is a typo. The column is **`outside_temp_c`**, matching
the `_c` display-unit suffix convention (`CLAUDE.md` §Non-negotiables) and the source
field `telemetry.Snapshot.OutsideTempC`.

**D2 — No backfill. All eight columns are nullable and existing rows stay NULL.**
No second migration, no watermark reset, no recompute of history.

Consequence, accepted knowingly: for up to one nightly cycle after deploy the latest
`vehicle_metrics` row predates the migration, so the dashboard renders "—" for the
temperatures, no software version, no locked/sentry badge and no charge limit. It
self-heals on the next `Reconcile` run. `battery_level_pct`, `odometer_km` and
`battery_range_km` are pre-existing `NOT NULL` columns and are unaffected.

Consequence, must be documented in the migration's column comments: **NULL on
`sentry_mode` is now ambiguous** — it means *either* "the vehicle did not report sentry"
(its meaning on `vehicle_snapshots`) *or* "this row was written before this migration".
This mirrors the identical, already-documented ambiguity on telemetry's Source A
enrichment fields. The other seven columns' NULL means only the latter.

Rejected — *watermark reset*: the mechanism works and is proven, but the owner chose the
minimal migration. Deferred to `openspec/roadmaps/backlog.md` §22.
Rejected — *`NOT NULL` + SQL backfill joining `vehicle_snapshots`*: cross-module
database read, forbidden by `ai/architecture.md` §2.

**D3 — All four gateway call sites move to the analytics port, not just the dashboard.**
`LatestSnapshotsByAccount` is called by `dashboardFor` (`handlers.go:446`), `vehiclesFor`
(`:244`), the nav header (`:712`) and `charges.go` (`:671`). All four switch. The
telemetry port keeps `SnapshotsByVehicleBetween`, still used by `history.go:321`.

Verified before adopting: the union of `Snapshot` fields those four sites read is
`BatteryLevelPct`, `BatteryRangeKm`, `OdometerKm`, `InsideTempC`, `OutsideTempC`,
`Locked`, `SentryMode`, `CarVersion`, `ChargingState`, `ChargeLimitSocPct`, `CapturedAt`,
`TeslaID` — **fully covered** by the three existing columns plus D1's eight. No further
column is required by D3.

This is wider than the ticket, and is the owner's explicit call.

**D4 — Locked and sentry are `ui.Badge` pills in the card header; the Status stat tile is
removed; the stat grid becomes three columns.** The header row already hosts the `[Stale]`
badge, so the two new pills join it there. The stat grid loses its Status tile and renders
Odometer / Interior / Exterior as a clean row of three (a 2-column grid with three tiles
would leave an orphan).

`dashSubtitle` **keeps** `StatusLabel` — the subtitle continues to read "Parked • Software
v11.1.2". "Remove the Status field" means the stat tile only. This is why D1 must carry
`charging_state`.

Rejected — *adding a `Kind`/color prop to `ui.StatTile`*: it modifies the shared `ui/`
kit, and therefore every existing `StatTile` caller and `make ui-guard`, to serve one
page. `ui.Badge` already has `Kind` (`success`/`warning`/`error`/`ghost`).

**D5 — Badge states, exhaustively.**

| Value | Badge | Kind |
|---|---|---|
| `locked = true` | "Locked" | `success` (green) |
| `locked = false` | "Unlocked" | `error` (red) |
| `locked` NULL | *no badge* | — |
| `sentry_mode = true` | "Sentry: On" | `warning` |
| `sentry_mode = false` | "Sentry: Off" | `ghost` |
| `sentry_mode` NULL | *no badge* | — |

`locked` is a `bool` on `telemetry.Snapshot` but must be modelled as `*bool` on the
analytics side, because D2 makes the column nullable.

**No new i18n keys.** The catalogue already carries all six strings, bilingual:
`vehicles.locked`, `vehicles.unlocked`, `vehicles.sentry_label`, `vehicles.on`,
`vehicles.off`, `vehicles.not_reported`. Reusing a `vehicles.*` key from the dashboard has
precedent — `dashboardFor` already reuses `KeyVehiclesNoticeTelemetryUnavailable`.

**D6 — The KB update is in scope** (ticket §4). `kkpa/context/` must stop saying the
dashboard reads `telemetry.vehicle_snapshots`. It is done in tier 2, alongside the change
it documents, not as a follow-up.

**D7 — Tier order is forced, not chosen.** Tier 2 consumes the port and columns tier 1
creates. There is no ordering for the user to pick.

## Tiers

| Status | Change | Module | Scope | depends_on | Proposal prompt |
|---|---|---|---|---|---|
| `[ ]` | `RM38-analytics-add-vehicle-status-columns` | `analytics` | Migration adding the eight nullable columns (D1) to `vehicle_metrics` + `sqlc generate`; extend `UpsertVehicleMetric` and the `vehicleMetricRow` derivation to copy them verbatim from the day's `telemetry.Snapshot` (no re-derivation); add a new `Reader` port method returning the latest metric row per vehicle for an account, with its own domain type. Update `internal/analytics/AGENTS.md` + `README.md` for the new public surface. | — | Add the eight columns of RM38 D1 to `vehicle_metrics` (all nullable, D2), copy them through `Recalculator` from each day's `telemetry.Snapshot`, and expose a `LatestMetricsByAccount`-shaped read port returning an analytics-owned domain type (never `telemetry.Snapshot`). Index plan: the read is `WHERE account_id = $1`, latest row per `tesla_id` — justify against the existing `vehicle_metrics_account_tesla_date_unique` index and add none if it is served. Document NULL's new ambiguity on `sentry_mode` (D2) in the column comment. |
| `[ ]` | `RM38-gateway-read-dashboard-from-metrics` | `gateway` | Switch all four `LatestSnapshotsByAccount` call sites (D3) to the analytics port; rewrite `mapDashboardSnapshot` / `mapVehicles` / nav-header / charges against the new type; `DashboardData` gains locked + sentry fields; `dashboard.templ` removes the Status tile, adds the two header badges and goes to a 3-column stat grid (D4/D5); update `kkpa/context/` (D6). | 1 | Repoint the gateway's four latest-state reads onto the analytics port from tier 1 and apply RM38 D4/D5 to the Vehicle Status card. Reuse the existing `vehicles.*` i18n keys — add none. Keep `dashSubtitle`'s "Parked • …" intact. `internal/gateway/` is the sandbox, plus the explicitly granted `kkpa/context/`. |

Legend: `[ ]` pending (change not yet created) · `[~]` in progress (change exists, not archived) · `[x]` done (archived).

## Future work

Backfilling the eight new columns for historical `vehicle_metrics` rows is deferred —
see `openspec/roadmaps/backlog.md` §22.
