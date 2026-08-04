## Why

The dashboard port (`apex-dashboard-from-stitch`) renders a single-vehicle bento.
The Stitch "Vehicle Status" hero's fourth mini-stat is **Efficiency ("153 Wh/km")**.
During the port the tile was deliberately **dropped** rather than fabricated, because
energy-per-kilometre is a **derived metric** the platform does not compute today, and
the platform has **no module that owns derived metrics**. `internal/telemetry/` owns
capture + storage (it explicitly "does not render [data] — dashboards read this
module's stored data through a port later"); `internal/gateway/` owns presentation
only. Efficiency is neither — it is analytics that sits between stored telemetry and
the dashboard, exactly the kind of "derived metrics over raw API values" the project
mission names as primary value (`AGENTS.md` "Primary Objectives — Energy per
kilometer", "Metrics — Energy per kilometer").

So this change creates **`internal/battery/`**, the platform's first **derived-metrics
module**, owned by the "Metrics / Battery" capability, and implements its first
metric: **energy-per-kilometre over a recent window** (the rolling efficiency the
dashboard hero shows). The module consumes stored telemetry through the
`telemetry.Reader` port (after `telemetry-add-snapshot-history-read-port` lands so a
window of snapshots is available) and exposes its own read port the gateway renders
through — the same interface/DB-free/owned-data discipline every other module follows.

Decided in the 2026-07-28 dashboard-port review (leader ↔ user):
- **New module over telemetry-extraction.** Efficiency is not stored telemetry; it is
  analytics derived from telemetry. Putting it on `telemetry.Snapshot` would violate
  the "telemetry stores; analytics derives" boundary and create the cross-cutting
  coupling the config rule against
  "one-big-change" exists to prevent. A new `internal/battery/` module owns the
  derivation, the same way a future `internal/drives/` would own trip detection.
- **Start with Wh/km only.** AGENTS.md lists many battery metrics (capacity
  degradation, range degradation, battery efficiency, forecast…). Landing the module
  skeleton with the dashboard's actual consumer (Wh/km) is the thin end of the wedge;
  later metrics are separate changes extending `internal/battery/Service`. Avoid
  scaffolding metrics nobody reads yet.
- **Consume the history port (depends on
  `telemetry-add-snapshot-history-read-port`).** Negligible efficiency over a single
  snapshot is undefined (no denominator over zero distance); the metric needs the
  30-day snapshot window the history port returns. This change is therefore a
  **dependent** change; it cannot land before the history port.

## What Changes

Primary module: **`internal/battery/`** (new). Gateway consumption is a follow-on;
the **new module + its first metric read port** is this change's scope.

### (a) New module `internal/battery/` — capability "battery" (Metrics / Battery)

The module owns derived battery/efficiency analytics. Public surface is a Go
interface (`ai/go-conventions.md` interface-first), the same discipline as
`account.Service` / `telemetry.Reader`:

```go
package battery

type Reader interface {
	// RecentEfficiency returns the rolling energy-per-kilometre consumption for the
	// given vehicle over the supplied window, derived from stored telemetry. It is
	// a pre-computed display value (Wh/km as a float64, rounded to whole) plus a
	// From/To pair for the dashboard subtitle. It returns ok=false (no error) when
	// there is not enough telemetry to compute a meaningful value (window too short,
	// no distance moved, no usable battery readings) — the dashboard then renders
	// the same "—" placeholder it already renders for missing snapshot fields.
	RecentEfficiency(ctx context.Context, teslaID int64) (Efficiency, bool, error)
}

type Efficiency struct {
	WhPerKm   float64  // whole Wh/km, rounded; display via the gateway's formatter
	FromKm    float64  // odometer at window start (km, derived)
	ToKm      float64  // odometer at window end (km, derived)
	BatteryDeltaPct float64 // net battery % over the window (may be negative — net charge)
}
```

`NewReader(telemetry telemetry.Reader, window time.Duration) Reader` is the
constructor. The window is a constructor const (default 30 days, matching the
dashboard's 30-day charts), not a per-call argument — the port is a pure accessor
and the window is a deployment-time tuning. The module owns **no database** (it is a
read-side derivation over telemetry's store); `internal/battery/db` does not exist,
and this change triggers **no migration, no SQL, design.md's DB section is "n/a —
no DB object"** (still required by config, but records the deliberate no-op).

### (b) The Wh/km derivation (the analytics the module owns)

Energy consumed over the window = (battery usable % at window start − battery
usable % at window end) × battery pack capacity (kWh). Distance = odometer at end
(km) − odometer at start (km). Wh/km = (energy_kWh × 1000) / distance_km.

The two unknowns that demand design:
- **Usable battery %** — `Snapshot.UsableBatteryLevel` (nullable, added by
  RM2). The module uses `UsableBatteryLevel` when present, falling back to
  `BatteryLevel` when not (documented approximation; design.md quantifies the
  error). The first and last snapshots in the window bound the percent delta.
- **Battery pack capacity (kWh)** — **not stored anywhere today.** This is the
  one genuinely missing input. Three sources, decided in design.md:
  1. A small **`internal/battery/` reference table** of known pack capacities
     keyed by vehicle model/trim (Model S Plaid 100 kWh, Model 3 LR 75 kWh, …).
     Maintained by humans; sourced from public spec sheets. This is owned data
     (a read-side constant table), not telemetry, and is the cleanest fit for a
     "metrics module owns analytics" boundary.
  2. A per-vehicle override the user sets (UI later) — deferred; not needed for
     the dashboard's first cut.
  3. Infer from the largest observed `ChargeEnergyAdded` / `BatteryDeltaPct`
     across charge sessions — clever but fragile; rejected as the primary source
     (kept as a future auto-calibration back-fill).

Recommendation: source 1 (reference table) for the first cut; design.md confirms
the table vs. a derived capacity's trade-off, the fallback when a model is
unknown ("—" placeholder, not a fabricated number), and the honest error band.

### (c) Negatives and edge cases (the modules' domain — not the gateway's)

- **Net positive charge over the window** (charged more than drove): the %
  delta is negative → no meaningful Wh/km. The module returns `ok=false` ("not
  enough driving to measure"); the dashboard renders "—". The gateway never sees
  a negative denominator or a fabricated value.
- **Zero distance moved** (vehicle parked the whole window): returns `ok=false`.
- **Window shorter than 2 snapshots**: returns `ok=false` (no interval).
- **Unknown pack capacity** (model not in the reference table): returns
  `ok=false` with a log line, so the reference-table gap is visible without
  breaking the dashboard. Auto-calibration (source 3) is a later enhancement.

## Breaking

No. A new module + a new `Reader` interface it exposes. Nothing depends on
`internal/battery/` today (it is brand new), so the additive interface has zero
compile impact on existing code. The gateway's `Deps` gains an optional `BatteryReader
battery.Reader` field in a **follow-on gateway change** (not this change) — this
change lands the module + its tests; wiring into `gateway.Deps` /
`handlers.dashboardFor` is the separate `apex-dashboard-efficiency-tile` gateway
change, mirroring how `telemetry-add-snapshot-history-read-port` precedes its own
gateway-wiring follow-on. No DB change (no migration). The one new package,
`internal/battery/`, has no consumers in this change — that is intentional (the
port is the deliverable; the consumer is a separate change for change-locality).

## Modules affected

- `internal/battery/` (new) — primary, sole scope: the module, the `Reader`
  interface, the `NewReader` constructor, the Wh/km derivation, the reference
  pack-capacity table, and the unit tests (fake `telemetry.Reader` — the module
  is tested offline, no DB, mirroring how `internal/telemetry/`'s logic is tested).
- `internal/gateway/` — **not in this change**; the follow-on gateway change
  `apex-dashboard-efficiency-tile` adds `BatteryReader battery.Reader` to `Deps`,
  passes it to `handlers.dashboardFor`, and renders the Efficiency mini-stat tile.
- **Depends on** `telemetry-add-snapshot-history-read-port` (must land first — the
  derivation needs `Reader.SnapshotsByVehicleSince`; the latest-only port is
  insufficient). Listed as a prerequisite in the change's `progress.json` when
  authoring begins.

## Database Changes

**None.** `internal/battery/` owns no database; it is a pure read-side derivation
over `internal/telemetry/`'s store (via the `telemetry.Reader` port). The
pack-capacity reference data is a small in-package Go table (a `map[string]float64`
keyed by model string, populated from public spec sheets), not a DB object — the
module owns analytics, not a durable entity ledger. design.md is still REQUIRED
per config (its "Database Changes" section records "n/a — no DB object; rationale:
the module is a pure derivation over telemetry's store; no migration, no sqlc, no
DB design gate trigger"). A future "per-vehicle capacity override" user setting
would land a `battery_vehicle_config` table in a later change — explicitly out of
scope here.

## Read Paths Affected

One new read path, run **once per dashboard render** (when the gateway follow-on
wires it): `battery.Reader.RecentEfficiency` calls
`telemetry.Reader.SnapshotsByVehicleSince(teslaID, now-window)` — the single range
scan added by `telemetry-add-snapshot-history-read-port`, bounded at ~30 rows —
then computes Wh/km in Go over the returned slice. No new telemetry query is
introduced by *this* change; it reuses the history port's query. No N+1: one
history fetch → one in-memory derivation. The reference-capacity lookup is an
in-memory map hit — no DB.

## Capabilities

### Added / Modified Capabilities

- **`battery`** (new capability, "Metrics / Battery") — first requirement "Recent
  Energy-Per-Kilometre": derive and expose rolling Wh/km for a vehicle from stored
  telemetry, returning a not-enough-data signal instead of a fabricated value when
  the window, distance, or capacity is insufficient. Behavioral spec
  (`openspec/changes/battery-add-efficiency-metric/specs/battery/spec.md`) authoring
  deferred to the design step per the user's "create the proposals" ask; will cover:
  correct derivation when capacity + 2+ snapshots + positive distance, ok=false on
  insufficient data, per-vehicle scoping (inherited from the telemetry port),
  callers never access a DB (the module has none), and the capacity-source
  fallback chain (reference table → unknown → ok=false).

### Consumed Capabilities (no change to their specs)

- **Nightly Vehicle Snapshot Capture** (unchanged) — reads stored snapshots.
- **Latest Snapshot Read Port / Snapshot History Read Port** (the latter from
  `telemetry-add-snapshot-history-read-port`) — consumed read-only; no change.

## Resolved decisions

Resolved in the 2026-08-03 grill-me pass (leader ↔ user). Full rationale + rejected
alternatives for each: `design.md`.

- **D1 — Energy numerator: hybrid (measured kWh + capacity SoC correction).**
  `energy = kWh_in − (capacity_kWh × ΔSoC / 100)`, where `kWh_in` sums
  `telemetry.SuperchargerSession.EnergyKWh` + `manualcharge.Entry.EnergyAddedKWh`
  over the window, and `ΔSoC = SoC_end − SoC_start`. NOT the proposal's original
  "capacity × ΔSoC" for the whole numerator — pack capacity is only known at MODEL
  granularity (D1b), so confining it to the leftover SoC-drift correction term
  keeps its ~25% error band applied to a small term, not the whole result.
- **D1b — Capacity source: in-package table keyed on `car_type`; unknown → drop
  the correction, still return a value.** A human-maintained
  `map[string]float64` in `internal/battery/capacity.go` (public spec sheets),
  NOT a database object. When `car_type` is nil or not in the table, the SoC
  correction is skipped and `Efficiency.Approximate=true` — the dashboard is
  never blanked over a table gap. Model-coarse, not trim-exact (`trim_badging`
  is not extracted anywhere in the platform today); trim-exact capacity is
  explicitly out of scope, deferred to `openspec/roadmaps/backlog.md` item 7.
- **D2 — SoC endpoints: consistent pair, never mixed.** Uses
  `UsableBatteryLevel` at BOTH window endpoints when both are non-nil, else
  `BatteryLevel` at BOTH — never usable at one endpoint and nominal at the
  other (a per-endpoint fallback manufactures a phantom ΔSoC of a few percent
  in cold weather).
- **D3 — Window: a constructor-time duration, default 30 days.** `NewReader`
  takes `window time.Duration`; `DefaultWindow = 30 * 24 * time.Hour` is an
  exported constant matching the dashboard's 30-day history cards. Not a
  per-call argument to `RecentEfficiency` — the window is deployment-time
  tuning.
- **D4 — Port signature carries `accountID`.**
  `RecentEfficiency(ctx, accountID uuid.UUID, teslaID int64) (Efficiency, bool, error)`.
  The original `RecentEfficiency(ctx, teslaID)` cannot compile: every port this
  module consumes (`telemetry.Reader.SnapshotsByVehicleSince`,
  `telemetry.SuperchargerReader.SuperchargerSessionsByVehicle`,
  `manualcharge.Reader.ListEntriesByVehicle`, `account.Service.RegisteredVehicles`)
  is `accountID`-scoped; this is mandatory defense-in-depth tenant isolation, not
  an optional nicety.
- **D5 — Return the raw `float64`; the gateway formats.** `Efficiency.WhPerKm`
  is unrounded — mirrors `Snapshot.OdometerKm()` returning raw km with the
  gateway's `formatKm` doing the rounding. Formatting stays in the presentation
  layer; a future chart can use the value at full precision.

### Out of scope (explicitly deferred)

- **Other battery metrics** (capacity degradation, range degradation, forecast)
  — separate changes extending `internal/battery/Service`. The module skeleton
  ships one metric that an actual reader consumes; the rest are added when they
  have a consumer.
- **Per-vehicle capacity override UI** — a user setting landing a
  `battery_vehicle_config` table is its own DB-touching change, deferred.
- **The dashboard's Efficiency tile wiring** — follow-on gateway change
  `apex-dashboard-efficiency-tile`, proposed after this module and the history
  port both land.