# Battery Sub-Agent

Agent-Name: battery

Per-module instructions for `internal/battery/` — merged with the global rules
(`CLAUDE.md`, `ai/*.md`) by any assistant working here (see `ai/agentic-workflow.md`).

## Doc-Pack (module)

Extends the project base Doc-Pack (`CLAUDE.md` → "Pipeline config") — never replaces it.
A dispatched worker/reviewer reads: base pack + this list + this file, before any write.

(No module-specific docs beyond the base pack today — this is a pure Go derivation
module with no persistence, no HTTP surface, and no external SDK of its own. If a
future metric needs an external doc, e.g. a battery-chemistry reference, add it here.)

## Responsibility

`internal/battery/` is the platform's first **derived-metrics** module. It owns
analytics computed FROM other modules' stored data, not the data itself. Its first
(and currently only) metric is rolling energy-per-kilometre (Wh/km) over a fixed
window, derived from `internal/telemetry/`'s snapshot history plus the two
charging-cost sources the platform stores (`telemetry.SuperchargerReader` and
`internal/manualcharge`), with a pack-capacity correction sourced from a small
in-package reference table keyed on the vehicle's `car_type`
(`internal/account.Vehicle.CarType`).

It does the derivation; it does not render it — the gateway consumes this module's
`Reader` port and formats the value for display (`apex-dashboard-efficiency-tile`,
a separate follow-on change; not yet wired as of `battery-add-efficiency-metric`).

Full design rationale (why hybrid energy, why consistent-pair SoC selection, why the
capacity table is model-coarse, why `Approximate` exists instead of refusing to
answer): `openspec/changes/battery-add-efficiency-metric/design.md`.

## Public interface (the port)

The module's mandatory contract is a Go interface (`ai/go-conventions.md` —
interface-first):

- `Reader` — `RecentEfficiency(ctx context.Context, accountID uuid.UUID, teslaID int64) (Efficiency, bool, error)`:
  returns the rolling Wh/km efficiency for one vehicle over the window `NewReader` was
  constructed with. Returns `ok=false` (no error) when there is not enough data to
  compute a meaningful value — never a fabricated number (`design.md` "D-ok"). Returns
  `Efficiency.Approximate=true` (still `ok=true`) when the vehicle's pack capacity is
  unknown — the SoC-drift correction term is dropped, not the whole computation
  (`design.md` D1b).
- `Efficiency` — the domain result: `WhPerKm` (raw `float64`, unrounded — the gateway
  formats it), `FromKm`/`ToKm` (read directly from `telemetry.Snapshot.OdometerKm` —
  already km-native at capture time, `telemetry-store-display-units` design D1/D3; this
  module performs no unit conversion of its own, per
  `battery-adopt-snapshot-unit-fields`), `BatteryDeltaPct` (net SoC over the window,
  `start − end`; negative means net charge), `Approximate`.
- `DefaultWindow` — exported `time.Duration` constant, 30 days. Deployment code passes
  it (or a different duration) to `NewReader` at construction; the window is NOT a
  per-call argument to `RecentEfficiency` (`design.md` D3).
- `NewReader(telemetry telemetry.Reader, supercharger telemetry.SuperchargerReader, manual manualcharge.Reader, account vehicleLookup, window time.Duration) Reader`
  is the constructor. `vehicleLookup` is an unexported narrow interface covering only
  `RegisteredVehicles` — any real `account.Service` satisfies it automatically
  (structural typing), no adapter needed at the call site.

No HTTP/JSON surface in this module (none required — `ai/architecture.md` §3).

## Allowed / forbidden imports

**May import (public ports only):**
- `internal/telemetry` — `telemetry.Reader` (`SnapshotsByVehicleSince`),
  `telemetry.SuperchargerReader` (`SuperchargerSessionsByVehicle`), and the domain
  types `telemetry.Snapshot`, `telemetry.SuperchargerSession`.
- `internal/manualcharge` — `manualcharge.Reader` (`ListEntriesByVehicle`) and the
  domain type `manualcharge.Entry`.
- `internal/account` — the narrow `RegisteredVehicles` method (satisfied by
  `account.Service`) and the domain type `account.Vehicle`.
- `github.com/google/uuid`, stdlib (`context`, `time`).

**Must NOT import:**
- `internal/telemetry/db` (`telemetrydb`), `internal/manualcharge/db`
  (`manualchargedb`), `internal/account/db` (`accountdb`), or `pgxpool`/`pgx` at all —
  this module owns no database connection. Cross-module data flows only through public
  ports (`ai/architecture.md` §2).
- `internal/gateway`, `html/template`, `templ` — no HTML in a domain module
  (`ai/architecture.md` §2).
- `internal/tesla` — this module never talks to the Fleet API directly; every value it
  needs (snapshots, charging sessions, vehicle config) has already been captured and
  stored by another module before `battery` ever runs.

## Data ownership

**None.** `internal/battery/` owns no database, no table, no migration, and no
`internal/battery/db` package. It is a pure read-side derivation over sibling
modules' stores, reached exclusively through their public `Reader` ports. The one
piece of module-local state is `capacity.go`'s `packCapacityKWh` — an in-package Go
`map[string]float64`, human-maintained from public Tesla spec sheets, NOT a database
object and NOT subject to the `database` design gate (`design.md` "Database
Changes"). Update that map directly (a code change) when a new `car_type` needs a
capacity entry; it does not require a migration.

## Testing

This module owns no DB, so **every test is an offline unit test** — no
`DATABASE_URL`, no Docker, no `TestMain`/`testdb` harness (unlike
`internal/telemetry` and `internal/account`, this module must never gain one):

- `derive_test.go` tests the pure derivation functions (`socReadings`,
  `deriveEfficiency`) directly with plain `[]telemetry.Snapshot` / `float64` inputs —
  no fakes needed, since they have zero I/O.
- `reader_test.go` tests `RecentEfficiency` against hand-written fakes of the four
  dependencies (`telemetry.Reader`, `telemetry.SuperchargerReader`,
  `manualcharge.Reader`, `vehicleLookup`), mirroring the `fakeReadStore`/
  `newFakeReader` pattern in `internal/telemetry/reader_test.go` one level up (fake
  *ports* instead of a fake *store*).

Run `go test ./internal/battery/...` — it must pass with `DATABASE_URL` unset and
Docker down; nothing in this module may ever self-skip for lack of a database.
