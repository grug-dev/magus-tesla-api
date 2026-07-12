## Context

Tier 5 of `openspec/roadmaps/nightly-vehicle-telemetry.md`. All prerequisites are
archived:
- Tier 3 (`telemetry-add-nightly-snapshots`): `vehicle_snapshots` table exists and is
  populated nightly.
- Tier 4 (`telemetry-add-snapshot-read-port`): `telemetry.Reader` interface and
  `telemetry.NewReader(pool)` constructor exist; `LatestSnapshotsByAccount` query is
  live.

This tier wires the existing `Reader` port into the gateway so the dashboard renders
enriched vehicle cards from stored snapshot data.

## No Database Object — Design Gate Does Not Apply

This change introduces **NO new database object**: no table, no column, no index, no
constraint, no view, no migration. It is purely a read-path change in the gateway
layer. The `vehicle_snapshots` table (owned by `internal/telemetry/db`) already exists
with all required columns. The `telemetry.Reader` interface (which wraps a
`DISTINCT ON` query over that table) already exists. The gateway reads through that
interface and never touches a database directly.

The OpenSpec `design.md` database gate (`config.yaml rules.design`) is therefore not
triggered.

## Goals / Non-Goals

**Goals:**
- Enrich the dashboard vehicle card with 10 fields from the latest nightly snapshot.
- Join `account.RegisteredVehicles` and `telemetry.LatestSnapshotsByAccount` in the
  handler by `TeslaID` — no N+1, two batch queries.
- Placeholder card for registered vehicles with no snapshot yet.
- Stale marker when `CapturedAt` is older than 36 h.
- Graceful degradation: if `Reader` errors, show the registry vehicles only.
- All formatting/derivation (km conversion, stale computation, sentry-nil rendering)
  in the Go handler — templates stay logic-light.

**Non-Goals:**
- Any write to the database.
- Live Tesla API calls on a normal dashboard render.
- Pagination or historical data display.
- Changing the telemetry module (it is frozen for this change).
- HTTP/JSON API surface.

## Decisions

### D1 — Merge by TeslaID; registered-but-no-snapshot renders a placeholder card

`account.RegisteredVehicles` and `telemetry.Reader.LatestSnapshotsByAccount` both scope
by `accountID`. The handler calls both in sequence, builds a map
`map[int64]telemetry.Snapshot` from the snapshot slice (keyed by `TeslaID`), then
iterates the registered vehicles and does a map lookup per vehicle. A vehicle whose
`TeslaID` is absent from the map gets `HasSnapshot: false`, causing the template to
render a placeholder card ("no data yet — awaiting first nightly snapshot").

No N+1: two queries total per render, both indexed and batch-scoped.

**Rejected alternative — single JOIN query through a new interface method:** would push
join logic into the telemetry module, violating its scope (it owns snapshots, not
account+snapshot pairs). The handler-side merge is the correct locus per
`architecture.md` §2 (cross-module data flows only through public interfaces; no module
reads another module's tables).

### D2 — Units: km via Snapshot companions; temperatures °C as stored (user decision)

`telemetry.Snapshot` stores `BatteryRange float64` and `Odometer float64` in miles
(Tesla-native), with pre-existing companion methods `BatteryRangeKm()` and
`OdometerKm()` that multiply by `milesToKm = 1.609344`. The handler calls these
companions when building the view model — never the raw miles fields. No km field is
added to any domain type (follows `go-conventions.md` miles→km rule).

Temperatures (`InsideTemp`, `OutsideTemp`) are already stored in degrees Celsius by the
Tesla Fleet API. No conversion, no companion method needed.

**Why companions, not a new km column on the view model:** the gateway view model
carries display-ready values, so it stores `BatteryRangeKm float64` as a plain field.
The domain `Snapshot` retains the miles fields (as required by the telemetry module's
immutable contract). The handler bridges the two at the mapping boundary — exactly the
gateway's job.

### D3 — Field set: Extended (user decision)

Each enriched vehicle card displays all of the following, sourced from the latest
`telemetry.Snapshot`:

| Display field | Source on `telemetry.Snapshot` | Notes |
|---|---|---|
| Battery % | `BatteryLevel int` | Direct; integer percent |
| Range (km) | `BatteryRangeKm()` | Companion method on Snapshot |
| Charge state | `ChargingState string` | Direct; e.g. "Charging", "Disconnected" |
| Odometer (km) | `OdometerKm()` | Companion method on Snapshot |
| Inside temp (°C) | `InsideTemp float64` | Direct; Celsius as stored |
| Outside temp (°C) | `OutsideTemp float64` | Direct; Celsius as stored |
| Locked | `Locked bool` | Direct |
| Sentry mode | `SentryMode *bool` | Three states: nil / false / true |
| Last updated | `CapturedAt time.Time` | Formatted as relative time + absolute |
| Stale flag | derived from `CapturedAt` | `CapturedAt` older than `stalenessThreshold` |

**Not included:** `Latitude`, `Longitude`, `CarVersion`, `ChargeLimitSoc` — deferred to
a future change that adds dedicated location/version sections.

### D4 — Staleness threshold ~36 h; always show last-updated; stale marker when older (user decision)

A named constant in the handler package:

```go
// stalenessThreshold is the duration after which a snapshot is considered stale.
// At ~36 h a missed 03:30 nightly poll has elapsed (24 h cycle + 12 h buffer).
const stalenessThreshold = 36 * time.Hour
```

The `vehiclesFor` / `mergeSnapshots` helper computes `IsStale` as:
```
IsStale = time.Since(snap.CapturedAt) > stalenessThreshold
```

Every vehicle card with a snapshot always shows the formatted `CapturedAt` ("last
updated X ago"). When `IsStale` is true the template additionally renders a visible
stale indicator (e.g. a badge or highlighted text). A no-snapshot placeholder never
shows a stale marker.

**Why 36 h:** the nightly poll fires at 03:30. A missed run means the most recent
snapshot is ≥ 24 h + 12 h buffer = 36 h old. This gives one full missed-cycle grace
before alerting.

### D5 — Reader failure degrades gracefully — registry still renders

`vehiclesFor` calls `account.RegisteredVehicles` first. If that fails, the existing
hard-notice path applies (unchanged). If it succeeds but `telemetry.Reader.LatestSnapshotsByAccount`
returns an error, the handler:
1. Logs the error (standard Go `log` / project logging pattern).
2. Continues with an empty snapshot map — all vehicles render with `HasSnapshot: false`.
3. The template renders the vehicle list with a non-fatal notice: "Telemetry unavailable
   — showing vehicle identity only."

This matches `htmx-go-integration.md`: errors surface as a swappable fragment, never
as a raw 500 or leaked Go error string.

### D6 — No new live Tesla call on normal render; first-connect seed retained; no DB object / migration (read-only)

The existing `vehiclesFor` logic: if `account.RegisteredVehicles` returns zero vehicles,
it calls `account.SeedVehicles` (one-time first-connect seed via Tesla
`ListVehicles`). This path is retained unchanged.

On every subsequent render (registered vehicles present): the handler reads only stored
data — `account.RegisteredVehicles` + `telemetry.Reader.LatestSnapshotsByAccount`. No
Tesla Fleet API call fires. This satisfies the "Read-only at request time" invariant in
`internal/gateway/AGENTS.md`.

No new table, column, index, constraint, view, or migration is introduced by this
change. The design gate does not apply (see "No Database Object" section above).

### D7 — All formatting/derivation in the handler (Go); templates stay logic-light (htmx-conventions)

Per `ai/htmx-conventions.md` §"No business logic in templates":

- The `mergeSnapshots` helper and `mapVehicles` mapper run in the handler; the template
  receives a fully-computed `fragments.Vehicle` view struct.
- km conversion (`BatteryRangeKm()`, `OdometerKm()`), stale computation
  (`time.Since > stalenessThreshold`), sentry-nil handling, and `CapturedAt` formatting
  all happen in Go.
- The template uses only `if`, `for`, and display logic — no arithmetic, no method
  calls on domain types, no time parsing.
- `fragments.Vehicle` carries display-ready fields: `BatteryRangeKm float64`,
  `OdometerKm float64`, `IsStale bool`, `SentryMode *bool` (three-state), `HasSnapshot
  bool`, `LastUpdated string` (pre-formatted).

This keeps `.templ` files readable (the learning codebase rationale from
`htmx-conventions.md`), compile-time safe, and fully testable via the handler's
`dataFor` / `mapVehicles` pure-Go path.

**Round-1 rework (accepted finding R1-002):** The `fragments.Vehicle` view struct's numeric
display fields (`BatteryRangeKm float64`, `OdometerKm float64`, `BatteryLevel int`,
`InsideTempC float64`, `OutsideTempC float64`) were replaced with pre-formatted display
strings (`Battery string`, `BatteryRange string`, `Odometer string`, `InsideTemp string`,
`OutsideTemp string`). The `fmt.Sprintf(...)` formatting that was previously in the
`.templ` file now lives exclusively in `mapVehicles` in `handlers.go`, so the template
reads only plain string fields — no `fmt` import, no method calls, no arithmetic anywhere
in the `.templ` file. This fully satisfies the "no business logic in templates" rule.

## Performance Profile Compliance

Read path: `GET /dashboard` / `GET /ui/vehicles`:
1. `account.RegisteredVehicles(ctx, uid)` — one indexed SELECT scoped by `account_id`.
2. `telemetry.Reader.LatestSnapshotsByAccount(ctx, uid)` — one `DISTINCT ON` SELECT
   scoped by `account_id`, using the existing `(account_id, tesla_id, captured_at)`
   index. Single range scan, no sort, no N+1.

Total: 2 indexed queries per render. No runtime aggregation. No Tesla API call.
Matches the read-heavy profile requirement: mandatory-fast reads, writes deferred to the
nightly batch.

## Summary of New Files and Modifications

| Path | Action | Notes |
|---|---|---|
| `internal/gateway/gateway.go` | Modify | Add `TelemetryReader telemetry.Reader` to `Deps` |
| `internal/gateway/handlers/handlers.go` | Modify | Add `TelemetryReader` to `Deps`/`Handler`/`New()`; extend `vehiclesFor`; add `mergeSnapshots`; extend `mapVehicles` |
| `internal/gateway/templates/fragments/vehicles.templ` | Modify | Extend `Vehicle` struct; update `VehiclesList` component; run `templ generate` |
| `internal/gateway/AGENTS.md` | Modify (append) | Note `telemetry.Reader` in Deps |
| `cmd/web/main.go` | Modify (leader-integrated) | Construct `telemetry.NewReader(pool)` and pass into `gateway.Deps` |
