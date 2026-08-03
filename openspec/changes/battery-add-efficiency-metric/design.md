## Context

`internal/battery/` is a brand-new module — the platform's first **derived-metrics** module
(no DB, no `Collector`, just a `Reader` port over other modules' stores). It exists because
`internal/telemetry/` explicitly does not render data ("dashboards read this module's stored
data through a port later") and `internal/gateway/` owns presentation only — neither owns the
Wh/km analytics the Stitch "Vehicle Status" hero's Efficiency mini-stat needs.

The module consumes exactly three sibling ports, all already shipped:
- `telemetry.Reader.SnapshotsByVehicleSince(ctx, accountID, teslaID, since) ([]Snapshot, error)`
  — oldest-first, non-nil empty slice, capped at 400 rows (`internal/telemetry/telemetry.go:251`,
  `internal/telemetry/AGENTS.md`).
- `telemetry.SuperchargerReader.SuperchargerSessionsByVehicle(ctx, accountID, teslaID, limit) ([]SuperchargerSession, error)`
  — `charge_start_date_time DESC`, non-nil empty slice (`internal/telemetry/telemetry.go:360`).
- `manualcharge.Reader.ListEntriesByVehicle(ctx, accountID, teslaID, limit) ([]Entry, error)`
  — non-nil empty slice (`internal/manualcharge/manualcharge.go:111`).

Plus a narrow read of `account.Service.RegisteredVehicles(ctx, accountID) ([]Vehicle, error)`
(`internal/account/account.go:138`), to resolve one vehicle's `CarType *string` for the
pack-capacity lookup.

This change ships the module + its `Reader` port + its tests only. Wiring it into
`gateway.Deps` is the separate follow-on change `apex-dashboard-efficiency-tile`
(proposal.md "Breaking").

Performance profile: **read-heavy** (`ai/architecture.md` §7). See "Read Paths Affected" below.

## Goals / Non-Goals

**Goals:**
- Derive a rolling Wh/km efficiency value for one vehicle over a fixed window, from telemetry
  snapshots plus the two charging-cost sources the platform already stores.
- Never fabricate a value: return `ok=false` (no error) whenever the inputs cannot support a
  meaningful number (D-ok below), and never silently blank the dashboard just because the
  pack-capacity table has a gap (D1b — `Approximate=true` instead).
- Multi-tenant defense-in-depth: every port call this module makes is `accountID`-scoped, even
  though the caller (gateway) already resolved `teslaID` from the same account (D4).
- Zero new database objects, zero N+1: one bounded read per port, per dashboard render.

**Non-Goals:**
- Any other battery metric (capacity degradation, range degradation, forecast) — future changes
  extending `internal/battery/Reader` (proposal.md "Out of scope").
- Trim-exact pack capacity (needs `trim_badging` extraction, not captured today) — see D1b and
  the backlog entry it adds.
- Gateway wiring — the follow-on `apex-dashboard-efficiency-tile` change.
- A per-vehicle capacity override user setting — future DB-touching change, deferred.

## Database Changes

**None.** `internal/battery/` owns no database — no migration, no `internal/battery/db` package,
no `sqlc.yaml` entry, no index plan. It is a pure read-side derivation over `internal/telemetry/`'s
and `internal/manualcharge/`'s stores, consumed exclusively through their public `Reader` ports,
plus one narrow read of `account.Service.RegisteredVehicles`. The pack-capacity table
(`internal/battery/capacity.go`) is an in-package Go `map[string]float64` — a human-maintained
reference constant, not a durable entity ledger — so it does not trigger the `database` design
gate (`openspec/config.yaml` "design" rules; `CLAUDE.md` Design-Gates). No DB object is added,
changed, or removed by this change.

## Read Paths Affected

One new read path, run **once per dashboard render** once the gateway follow-on wires it
(`battery.Reader.RecentEfficiency`):

1. `telemetry.Reader.SnapshotsByVehicleSince` — one indexed range scan on
   `idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at)`, bounded ~30 rows
   (30-day window, one snapshot/night) and hard-capped at 400 by the port itself.
2. `telemetry.SuperchargerReader.SuperchargerSessionsByVehicle` — one indexed lookup, bounded by
   `chargingSourceLimit` (D6 below).
3. `manualcharge.Reader.ListEntriesByVehicle` — one indexed lookup, same bound.
4. `account.Service.RegisteredVehicles` — one indexed lookup already paid by most gateway
   handlers per render (`internal/gateway/AGENTS.md` "Public interface"); this module adds one
   more call site, not a new query shape.

All four are existing, already-indexed queries reused as-is — **no new SQL is introduced by this
change**. Derivation (SoC delta, capacity correction, Wh/km) happens entirely in Go over the four
in-memory results. No N+1: one fetch per port, one in-memory derivation. This matches the
read-heavy performance profile — the only "write" this module could be said to do is the
human-maintained edit to `capacity.go`, which is a deploy-time code change, not a runtime write.

## Decisions

### D1 — Energy numerator: hybrid (measured kWh + capacity SoC correction)

```
kWh_in   = Σ supercharger.EnergyKWh + Σ manualcharge.EnergyAddedKWh   (over the window, this vehicle)
ΔSoC     = SoC_end − SoC_start                                        (percent; D2 decides which field pair)
energy   = kWh_in − (capacity_kWh × ΔSoC / 100)
distance = end.OdometerKm() − start.OdometerKm()
Wh/km    = energy × 1000 / distance
```

**Rationale.** Pack capacity is only known at MODEL granularity (D1b), carrying a ~25% error
band. Scoping capacity to only the leftover SoC-drift correction term (rather than the whole
numerator) means the same ~25% capacity error only ever applies to a small correction, not the
full energy figure. Measured `kWh_in` comes from data the platform already stores
(`telemetry.SuperchargerSession.EnergyKWh`, `manualcharge.Entry.EnergyAddedKWh`) — no new
capture path.

**Rejected:**
- **(a) Pure `capacity × ΔSoC`** (the proposal's original) — ~25% error on the *whole* number,
  and silently wrong whenever the vehicle charged at all inside the window (SoC drift no longer
  reflects consumption alone).
- **(b) Pure measured `kWh_in`, no correction** — accurate only when start SoC ≈ end SoC;
  systematically overstates efficiency whenever the window happens to end on (or near) a full
  charge, because kWh delivered to the battery that never left as driving energy still counts as
  "consumed."

### D1b — Capacity source: in-package table keyed on `car_type`; unknown → drop the correction, still return a value

```go
capacity, known := capacityFor(carType)
var energy float64
approximate := false
if known {
    energy = kWhIn - capacity*deltaSoC/100
} else {
    energy = kWhIn
    approximate = true
}
```

`capacityFor` looks up an in-package `map[string]float64` (`internal/battery/capacity.go`) of
usable kWh keyed on the Fleet API's `vehicle_config.car_type` code (`"model3"`, `"modely"`,
`"models"`, `"modelx"`, …), sourced from public spec sheets and human-maintained — **not** a
database object (see "Database Changes"). `account.Vehicle.CarType` is `*string`, NULL for any
vehicle the nightly collector has not re-polled since `RM6-telemetry-capture-vehicle-config`
landed — so the unknown case is common at first and MUST NOT blank the dashboard. When capacity
is unknown, the SoC correction term is dropped (not zeroed-and-hidden) and
`Efficiency.Approximate` is set `true` so the gateway can render a marker (e.g. an asterisk or
tooltip — gateway's call, out of this change's scope).

**IMPORTANT — model-coarse, not trim-exact.** The live `vehicle_config` payload carries
`trim_badging` (~47 keys total) but `internal/tesla/types.go`'s `VehicleConfigTesla` extracts only
`exterior_color` + `car_type` (`RM6-telemetry-capture-vehicle-config`, archived). A "Model 3
Standard Range" and a "Model 3 Long Range" share `car_type: "model3"` but have different pack
capacities — this table cannot distinguish them. Extracting `trim_badging` is a separate
`tesla` + `account` + `telemetry` capture change, **explicitly out of scope here** — recorded as
backlog item 7 (see "Migration Plan" step 0).

**Rejected:**
- **(a) unknown → `ok=false`** (the proposal's original) — blanks the tile over a gap in a
  human-maintained table, for potentially every vehicle until the collector's next config-capture
  cycle re-polls it. Directly contradicts the "never fabricate, but also never gratuitously blank"
  goal.
- **(b) a single platform-wide nominal constant** — removes the `car_type` dependency entirely,
  but is needlessly less accurate for the common case where `car_type` IS known (most vehicles,
  most of the time, once RM6's capture has run once).

### D2 — SoC endpoints: consistent pair, never mixed

```go
func socReadings(start, end telemetry.Snapshot) (socStart, socEnd float64) {
    if start.UsableBatteryLevel != nil && end.UsableBatteryLevel != nil {
        return float64(*start.UsableBatteryLevel), float64(*end.UsableBatteryLevel)
    }
    return float64(start.BatteryLevel), float64(end.BatteryLevel)
}
```

**Rationale.** `telemetry.Snapshot.UsableBatteryLevel` is `*int`, NULL on every
pre-`RM2-telemetry-add-charging-stats` row. A *per-endpoint* fallback (usable where present, else
nominal, decided independently at each endpoint) can pick nominal `BatteryLevel` at the window
start and usable `UsableBatteryLevel` at the end. `UsableBatteryLevel` runs 1–3 points below
nominal in cold weather, so a mixed pair manufactures a phantom ΔSoC of a few percent —
roughly 1.5 kWh of fabricated energy in the numerator at a 75 kWh pack. Choosing the field family
**once**, for the whole computation, based on whether *both* endpoints have `UsableBatteryLevel`,
eliminates this by construction.

**Rejected:**
- **Per-endpoint fallback** (the proposal's literal original D2) — the phantom-ΔSoC bug above.
- **`BatteryLevel`-only, always** — gives up the more accurate reading whenever both endpoints
  do have `UsableBatteryLevel` (which is the common case for any window entirely after RM2).

### D3 — Window: a constructor-time duration, default 30 days

`NewReader`'s signature carries a `window time.Duration` parameter, supplied once at
construction (`cmd/web` wiring time), not per `RecentEfficiency` call. `DefaultWindow = 30 *
24 * time.Hour` is an exported constant matching the dashboard's 30-day history cards and the
Stitch "Avg" framing (proposal.md "What Changes" (b)); deployment code passes it (or a different
duration, for a future analytics page) at construction. `RecentEfficiency` itself stays a pure
two-argument accessor (`ctx, accountID, teslaID`) — the window is deployment-time tuning, not
caller-time state.

### D4 — Port signature carries `accountID`

```go
RecentEfficiency(ctx context.Context, accountID uuid.UUID, teslaID int64) (Efficiency, bool, error)
```

The proposal's original `RecentEfficiency(ctx, teslaID)` cannot compile against the ports this
module actually consumes: every one of them is `accountID`-scoped —
`telemetry.Reader.SnapshotsByVehicleSince(ctx, accountID, teslaID, since)`,
`telemetry.SuperchargerReader.SuperchargerSessionsByVehicle(ctx, accountID, teslaID, limit)`,
`manualcharge.Reader.ListEntriesByVehicle(ctx, accountID, teslaID, limit)`,
`account.Service.RegisteredVehicles(ctx, accountID)`. `accountID` is mandatory input, not an
optional isolation nicety — it is defense-in-depth tenant scoping even though the gateway already
resolves `teslaID` from within the calling user's own `account.RegisteredVehicles` result before
ever reaching this port.

### D5 — Return the raw `float64`; the gateway formats

`Efficiency.WhPerKm` is unrounded `float64`. Mirrors `telemetry.Snapshot.OdometerKm()` returning
raw km with the gateway's `formatKm` doing the rounding — formatting stays in the presentation
layer (`internal/gateway/`), and a future chart (or a future stricter metric) can consume the
value at full precision instead of a pre-rounded display string.

### D6 — Bounded query limit for the two charging-cost sources (implementation detail, this design step)

`SuperchargerSessionsByVehicle` and `ListEntriesByVehicle` take a `limit`, not a `since` — there
is no server-side date filter on either port. `internal/battery/reader.go` therefore fetches
`chargingSourceLimit = 200` rows from each (DESC-ordered by date, per both ports' documented
contract) and filters in Go to entries at or after the window's `since` boundary before summing.
200 is a deliberately generous cap: even a vehicle supercharging or manually logging multiple
times a day would need >6 events/day sustained for 30 days to exceed it, which is implausible for
a single vehicle. If it were ever exceeded, the *oldest* in-window entries would be silently
excluded from the sum (both ports return the newest rows first) — accepted as a negligible,
extremely-unlikely-in-practice risk (see "Risks / Trade-offs"), not worth a second round-trip or a
port signature change for `since` support on two ports this module does not own.

### D-ok — `ok=false` conditions (no error)

The module returns `ok=false`, no error — the gateway renders "—" — in exactly these cases:
- **Fewer than 2 snapshots** in the window (no interval to measure across).
- **`distance <= 0`** — parked the whole window, or a non-increasing odometer (should not happen,
  guarded defensively).
- **`energy <= 0`** — net charge over the window exceeded consumption (the vehicle ended up with
  more usable energy than it took in minus what it used — no meaningful "energy consumed for
  driving" figure exists).

**Unknown pack capacity is explicitly NOT an `ok=false` case** (D1b) — the module still returns
`ok=true` with `Approximate=true` and the SoC-correction term dropped.

## Public Surface

```go
package battery

// DefaultWindow is the recommended NewReader window — 30 days, matching the dashboard's
// 30-day history cards (D3). Deployment code may pass a different duration for a future
// analytics page without changing the port.
const DefaultWindow = 30 * 24 * time.Hour

// Reader is the battery module's public port — the only mandatory contract
// (ai/go-conventions.md interface-first). The gateway follow-on change depends on this
// interface, never on the concrete implementation.
type Reader interface {
    // RecentEfficiency returns the rolling energy-per-kilometre for the given vehicle over
    // the window NewReader was constructed with, derived from stored telemetry plus the two
    // charging-cost sources this platform stores. It returns ok=false (no error) when there
    // is not enough data to compute a meaningful value (D-ok) — never a fabricated number.
    RecentEfficiency(ctx context.Context, accountID uuid.UUID, teslaID int64) (Efficiency, bool, error)
}

// Efficiency is one computed rolling-efficiency result — our own domain model, no vendor
// suffix (ai/architecture.md §6). FromKm/ToKm are already km-native (derived from
// telemetry.Snapshot.OdometerKm()), so no further Km() companion applies here.
type Efficiency struct {
    WhPerKm         float64 // raw, unrounded (D5) — the gateway formats for display
    FromKm          float64 // odometer at window start, km
    ToKm            float64 // odometer at window end, km
    BatteryDeltaPct float64 // net SoC over the window: start − end (negative = net charge)
    Approximate     bool    // true when pack capacity was unknown and the SoC correction was dropped (D1b)
}

// NewReader constructs a Reader over the three sibling ports it consumes plus a narrow
// account lookup, with window fixed at construction time (D3).
func NewReader(
    telemetry telemetry.Reader,
    supercharger telemetry.SuperchargerReader,
    manual manualcharge.Reader,
    account vehicleLookup, // unexported narrow interface, see below — any *account.service value satisfies it
    window time.Duration,
) Reader
```

`vehicleLookup` (unexported, `internal/battery/reader.go`) is a **narrow consumer interface**
over `account.Service`, covering only the one method this module needs:

```go
// vehicleLookup is a narrow consumer interface over account.Service — this module needs
// only CarType resolution for one vehicle, not the full account port (ai/go-conventions.md
// "accept interfaces"). Any account.Service implementation satisfies this automatically
// (structural typing); cmd/web wires the real account.Service in without any adapter.
type vehicleLookup interface {
    RegisteredVehicles(ctx context.Context, accountID uuid.UUID) ([]account.Vehicle, error)
}
```

## Implementation Notes (file layout)

Mirrors the existing split conventions (`telemetry.go`/`reader.go`,
`manualcharge.go`/`service.go`) — small, single-purpose files so a change to one concern touches
one file (AI-efficiency: change-locality):

- **`internal/battery/battery.go`** — package doc, `Reader` interface, `Efficiency` struct,
  `DefaultWindow` const. No imports beyond stdlib + `uuid` + `time`/`context` — this file has zero
  knowledge of telemetry/manualcharge/account internals.
- **`internal/battery/capacity.go`** — `packCapacityKWh map[string]float64` + `capacityFor(carType
  string) (kWh float64, known bool)`. Isolated so updating the human-maintained table is a
  one-file diff (D1b).
- **`internal/battery/derive.go`** — the pure derivation function(s): `socReadings` (D2),
  `deriveEfficiency(snapshots []telemetry.Snapshot, kWhIn float64, capacity float64, capacityKnown
  bool) (Efficiency, bool)` (D1/D1b/D-ok math). Zero port dependencies — takes plain values in,
  returns `Efficiency` + `ok` out. This is what makes the arithmetic trivially unit-testable
  without any fake port (see "Testing").
- **`internal/battery/reader.go`** — the `reader` struct (unexported), `vehicleLookup` interface,
  `NewReader` constructor, `RecentEfficiency` method: fetches from the three ports + the account
  lookup, filters the two charging-cost sources to the window (D6), resolves capacity via
  `capacityFor`, and calls `deriveEfficiency`. This is the only file that imports
  `internal/telemetry`, `internal/manualcharge`, and `internal/account`.

## Testing

`internal/battery/` owns no database — **every test in this module is an offline unit test**, no
`DATABASE_URL`, no Docker, no live Tesla call. Mirrors
`internal/telemetry/reader_test.go`'s fake-store pattern exactly, one level up (fake *ports*
instead of a fake *store*, since this module has no store of its own):

- **`derive_test.go`** tests `deriveEfficiency`/`socReadings` directly with plain `[]Snapshot` /
  `float64` inputs — no fakes needed at all, since D1/D1b/D2/D-ok are pure functions.
- **`reader_test.go`** tests `RecentEfficiency` against hand-written fakes of the three port
  interfaces (`telemetry.Reader`, `telemetry.SuperchargerReader`, `manualcharge.Reader`) plus
  `vehicleLookup`, following the `fakeReadStore`/`newFakeReader` shape in
  `internal/telemetry/reader_test.go`: a fake per interface, constructed inline in each test,
  asserting `RecentEfficiency`'s output and any pass-through error propagation.

No `TestMain`/`testdb` dependency — this module has nothing analogous to
`internal/telemetry/testdb_test.go` or `internal/account/testdb_test.go`, and must not gain one.

## Risks / Trade-offs

- **Model-coarse capacity (D1b) is a known accuracy ceiling**, not a bug — `Approximate` communicates
  it to the gateway; trim-exact capacity is out of scope (backlog item 7).
- **`chargingSourceLimit` (D6) could theoretically truncate** an extremely high-frequency
  vehicle's charging history within the window. Accepted as implausible in practice (see D6); if
  it ever becomes a real problem, the fix is a `since`-aware method added to
  `telemetry.SuperchargerReader`/`manualcharge.Reader` in a follow-on change — not something
  `internal/battery/` can fix unilaterally, since it does not own those ports.
- **Empty-string vs. nil `CarType`**: `account.Vehicle.CarType` is `*string`; `capacityFor` is
  called with `""` when either the pointer is nil or the vehicle is not found in
  `RegisteredVehicles` at all (should not happen for a `teslaID` the gateway already resolved from
  the same account, but handled defensively) — `capacityFor("")` naturally returns `known=false`
  since `""` is never a map key, so this collapses into the same D1b "unknown" path with no special
  casing needed.

## Migration Plan (implementation order for the workers)

0. **Backlog entry** (this artifacts step, not an implementation task): append item 7 to
   `openspec/roadmaps/backlog.md` under "Pending to be picked up" — trim-exact pack capacity,
   triggered by "when trim-exact pack capacity is wanted" (D1b).
1. `internal/battery/battery.go` — `Reader`, `Efficiency`, `DefaultWindow` (no deps).
2. `internal/battery/capacity.go` — `packCapacityKWh`, `capacityFor` (no deps; parallel with 1).
3. `internal/battery/derive.go` — `socReadings`, `deriveEfficiency` (depends on 1, for `Efficiency`).
4. `internal/battery/reader.go` — `vehicleLookup`, `reader`, `NewReader`, `RecentEfficiency`
   (depends on 1, 2, 3).
5. `internal/battery/derive_test.go` — pure-function tests (depends on 3; parallel with 4/6).
6. `internal/battery/reader_test.go` — fake-port tests (depends on 4).
7. `internal/battery/AGENTS.md` (depends on nothing functionally; author alongside 1).
8. `go build ./...`, `go vet ./...`, `go test ./...` (offline; no DB, no live Tesla call, no
   Docker dependency introduced by this module). `openspec validate
   battery-add-efficiency-metric --strict`.

**Rollback:** delete `internal/battery/` and revert the `backlog.md` append; no migration to
reverse (none was created), nothing else in the repo references this module yet (proposal.md
"Breaking": "no consumers in this change").

## Open Questions

None — D1, D1b, D2, D3, D4, D5 (the roadmap-level binding decisions resolved in the 2026-08-03
grill-me pass) are settled and implemented as designed above. D6 is an implementation detail
resolved during this design step, not a re-litigated interview decision.
