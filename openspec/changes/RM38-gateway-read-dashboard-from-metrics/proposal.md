Source: MAG-12 — https://linear.app/magus-monitor/issue/MAG-12/show-fields-on-vehicle-status-section
Roadmap: openspec/roadmaps/RM38-dashboard-vehicle-status-from-metrics.md
Tier: 2 of 2 (depends on tier 1, `RM38-analytics-add-vehicle-status-columns`, archived —
`analytics.Reader.LatestMetricsByAccount` and `analytics.VehicleStatus` already exist)
Unit tests: characterization + contract-first — the four call sites being repointed have
existing handler tests (`handlers_test.go`, `charges_test.go`) that currently exercise the
`telemetry.Reader` path; this change rewrites their fakes/fixtures to the `analytics.Reader`
port and adds new cases for the nil-pointer degraded states D2/D9 introduce. design.md
authors every expected value (badge matrix, nil-`CapturedAt` behavior, full/nil
`VehicleStatus` fixtures) before implementation, per `ai/go-conventions.md`'s
"author expected values first" rule.

## Why

`RM38-dashboard-vehicle-status-from-metrics` (roadmap, read in full before this proposal)
moves the gateway's "latest state of a vehicle" reads off `telemetry.vehicle_snapshots` and
onto `analytics.vehicle_metrics`, now that tier 1 made `analytics.Reader.LatestMetricsByAccount`
a complete substitute for `telemetry.Reader.LatestSnapshotsByAccount` (twelve fields, three
pre-existing + eight new via tier 1, plus `TeslaID`). This tier is the gateway-side half: all
four call sites (`dashboardFor`, `vehiclesFor`, `navHeaderFor`, `charges.go`'s battery-suggestion
lookup — roadmap D3) switch, and the dashboard's Vehicle Status card gains Locked/Sentry badges
while its Status stat tile is removed (roadmap D4/D5).

The gateway already has `analyticsReader analytics.Reader` on `Handler` and `Deps` (wired since
`RM28-gateway-add-consumed-graph`) — this tier adds **no** new dependency, no `Deps` field, no
`cmd/web` change. `h.telemetryReader` stays on `Handler` unchanged: `history.go:321`'s
`SnapshotsByVehicleBetween` is the only remaining telemetry read after this tier lands.

Every decision below was settled with the owner (roadmap D1–D7 plus D8/D9, the tier-boundary
decisions recorded there) before this proposal was written. This design pass adds its own
further decisions where the roadmap left implementation specifics open — most notably how the
now-nullable `VehicleStatus` pointer fields degrade at each of the four call sites, and what
becomes of the `mergeSnapshots` helper.

## What Changes

- **`dashboardFor` / `mapDashboardSnapshot`** (`handlers.go`) read
  `h.analyticsReader.LatestMetricsByAccount` instead of
  `h.telemetryReader.LatestSnapshotsByAccount`. `DashboardData` gains `Locked *bool` and
  `SentryMode *bool`; `dashboard.templ` drops the Status stat tile (grid goes 2×2 → one row of
  three), adds two `ui.Badge` pills (Locked/Unlocked, Sentry: On/Off) to the card header next to
  the existing `[Stale]` badge, per the exhaustive badge matrix in roadmap D5. `dashSubtitle`
  keeps reading `StatusLabel` unchanged (roadmap D4). A nil `CapturedAt` renders no date line and
  no `[Stale]` badge (roadmap D9) — achieved with **zero template changes**: the handler simply
  leaves `LastUpdated`/`IsStale` at their zero values, and the template's existing
  `if d.HasSnapshot && d.LastUpdated != ""` / `if d.HasSnapshot && d.IsStale` guards already skip
  rendering them.
- **`vehiclesFor` / `mapVehicles` / `fragments.Vehicle`** (`handlers.go`,
  `templates/fragments/vehicles.templ`) switch to the same port. This code path has no route
  (`VehiclesList`/`vehicles.templ` is not mounted in `gateway.go`; `vehiclesFor`'s only live
  effect is the one-time Tesla seed side effect inside `Handler.Dashboard`, its `VehiclesData`
  return value discarded there) — see design.md D3 for why it is adapted rather than deleted.
  `Vehicle.Locked` changes from `bool` to `*bool` (mirroring `SentryMode`'s existing three-state
  pattern) since the source is now nullable.
- **`navHeaderFor`** (`handlers.go`) reads the same port. A nil `CapturedAt` forces `Asleep` with
  no "Last seen" label (roadmap D9) — again zero template change, since `nav_header.templ`
  already omits the label when it is empty.
- **`charges.go`'s battery-suggestion lookup** (`buildChargesPage`) switches its snapshot scan to
  `[]analytics.VehicleStatus`; `BatteryLevelPct` stays a plain `int` on both types, so this is a
  type-level change only.
- **`mergeSnapshots` → `mergeVehicleStatuses`**: renamed and retyped from
  `(map[int64]telemetry.Snapshot)` to `(map[int64]analytics.VehicleStatus)`. Kept, not deleted —
  see design.md D6 for why the one-row-per-`tesla_id` guarantee `LatestMetricsByAccount` already
  provides does not remove the need to index the returned slice by `TeslaID` for O(1) lookup at
  three of the four call sites.
- **No new i18n keys.** Reuses the six existing `vehicles.*` catalogue keys (roadmap D5).
- **Docs**: `internal/gateway/AGENTS.md`'s `Deps.AnalyticsReader` bullet gains
  `LatestMetricsByAccount`; its `Deps.TelemetryReader` bullet is updated to say
  `SnapshotsByVehicleBetween` (history only) is its sole remaining caller.
  `kkpa/context/use-case/gateway/read-dashboard-bento.md` is corrected to name
  `analytics.Reader.LatestMetricsByAccount` / `vehicle_metrics` instead of
  `telemetry.Reader.LatestSnapshotsByAccount` / `vehicle_snapshots` (roadmap D6).

## Breaking

**No — externally.** No route is added, removed, or resigned; the same two page/fragment
endpoints (`GET /dashboard`, `GET /ui/dashboard`) and the same nav-header/vehicle-select
fragments serve the same URLs. A signed-in user sees the same page shape, minus the Status tile,
plus two badges — this is the ticket's intended visible change, not an accidental break.

**No — internally, mostly additively.** `fragments.DashboardData` gains two fields
(`Locked`, `SentryMode`); no existing field is removed (`StatusLabel` stays, per roadmap D4).
**One narrow internal break**: `fragments.Vehicle.Locked`'s type changes from `bool` to `*bool`
— this is an unrouted, gateway-internal presentation struct with no external consumer, but any
existing test literal `fragments.Vehicle{Locked: true}` needs updating to `Locked: boolPtr(true)`
(or equivalent) in the same change. `mergeSnapshots` is renamed; its only callers are inside this
same module and are updated in this same change.

## Modules Affected

- **`internal/gateway/`** — the only module touched. `handlers/handlers.go`,
  `handlers/charges.go`, `templates/pages/dashboard.templ` + `dashboard.go`,
  `templates/fragments/vehicles.templ`, `AGENTS.md`.
- **`internal/analytics/`, `internal/telemetry/`** — read, not written; both already expose the
  ports this tier consumes (analytics) or continues to consume for a narrower purpose
  (telemetry, history only).
- **`kkpa/context/`** — one use-case guide corrected (D6), explicitly granted to this worker
  alongside the module sandbox.

## Read Paths Affected — performance-sensitive, per `openspec/config.yaml`'s proposal rule

- **`GET /dashboard` / `GET /ui/dashboard`** — `dashboardFor`'s read moves from
  `telemetry.Reader.LatestSnapshotsByAccount` (`vehicle_snapshots`, `DISTINCT ON (tesla_id) ...
  ORDER BY tesla_id, captured_at DESC`) to `analytics.Reader.LatestMetricsByAccount`
  (`vehicle_metrics`, same `DISTINCT ON` shape, served by tier 1's
  `idx_vehicle_metrics_latest (account_id, tesla_id, metric_date DESC)`). Still exactly one
  batched read per render, same as today — no N+1 introduced.
- **`GET /ui/nav-header`** — same read-path substitution, same batching (one call per render,
  already shared with the dashboard's own read pattern — each handler calls the port
  independently today and continues to do so; this tier does not introduce or remove any
  cross-handler read sharing).
- **`GET /ui/charges` (create-form default)** — same substitution for the one-vehicle battery
  suggestion lookup; still a single batched read, one row picked out by `TeslaID`.
- **`vehiclesFor`'s read** — unrouted (see "What Changes"); included here only because the
  Performance-Profile rule asks every proposal to name affected read paths, and this one, while
  dead in production, still executes on every `GET /dashboard` render as vehicle-seed plumbing.

## Capabilities

### Added Capabilities

- **Locked/Sentry status badges on the dashboard's Vehicle Status card** — the ticket's visible
  ask. See `specs/gateway/spec.md`.

### Changed Capabilities

- **The dashboard, vehicle list (unrouted), nav header, and charge-form battery suggestion all
  read vehicle state from `analytics.vehicle_metrics` instead of `telemetry.vehicle_snapshots`.**
  See `specs/gateway/spec.md`.
- **The dashboard's Status stat tile is removed**; the card subtitle's status word is unchanged.
  See `specs/gateway/spec.md`.

### Out of scope (explicitly deferred)

- **Adding `metric_date` to the analytics port, or retuning the 36 h/48 h staleness/freshness
  thresholds.** Roadmap D8 (owner's call) — the gateway keeps using `CapturedAt` exactly as
  before; a NULL `CapturedAt` is an accepted, self-healing (one nightly cycle) degraded state.
- **Backfilling historical `vehicle_metrics` rows.** Tier 1's D2, deferred to
  `openspec/roadmaps/backlog.md` §22.
- **Wiring `/ui/vehicles` to a route, or otherwise reviving the vehicle-list page.** Out of
  scope — this change only keeps that dead code compiling and correctly typed; it does not
  change its reachability.

## Testing

Per the Test-Execution-Policy: the assistant writes tests and runs `go build ./...`,
`go vet ./...`, `gofmt -l`, `make templ`, `make css`, and the standalone guards — never
`go test ./...`. The owner runs the suite; until they do, this tier's implementation status is
**awaiting-user-verification**, never "done." See design.md "Test Contract" for the concrete
fixtures and expected values authored before implementation.

## Resolved decisions

Roadmap D1–D7 (carried from the roadmap, settled before either tier's artifacts existed), D8
(owner: no `metric_date`, thresholds unchanged) and D9 (leader: nil-`CapturedAt` degraded
rendering) are all carried verbatim and not re-litigated here. This design pass's own new
decisions — D1–D11 in design.md — settle implementation specifics the roadmap left open: the
per-call-site nil-handling for every newly-pointer field, the `mergeSnapshots` rename/retype,
and the exact Go helper shape for the two new badges.

**Database design gate — does not apply.** This tier changes no database object (no migration,
no new table/column/index/constraint/view). Tier 1 already did that work and is archived. This
change is Go-code-and-templates only.
