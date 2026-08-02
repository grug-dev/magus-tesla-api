## Why

The dashboard port (`apex-dashboard-from-stitch`) renders a single-vehicle bento.
The Stitch "Vehicle Status" hero shows four mini-stats; the port rendered four
**real** fields where the snapshot already extracts typed columns — Odometer,
Interior, Exterior, and Status. The Stitch hero's **Tire Pressure** ("41 PSI")
was deliberately **dropped** during the port rather than fabricated, because the
telemetry `Snapshot` does not expose tire pressure as typed fields today.

The pressure values **are** in the stored payload: `vehicle_snapshots.raw_data`
is the lossless `vehicle_data` JSONB (the "Nightly Vehicle Snapshot Capture"
requirement's data-fidelity invariant), and Tesla's `vehicle_state` carries
`tpms_pressure_fl`, `tpms_pressure_fr`, `tpms_pressure_rl`, and `tpms_pressure_rr`
(in bar). So the data exists on disk — only the typed-column extraction is missing.
This mirrors exactly the situation the prior `RM2-telemetry-add-charging-stats`
change resolved for charge telemetry: values were in `raw_data`, and the fix was to
extract typed nullable columns consistent with every other extracted field, with
`NULL` for pre-migration rows (the back-fill-from-raw_data escape hatch already
documented in the spec).

This change extracts the four TPMS pressure columns so tire pressure flows through
the **existing** `telemetry.Reader.LatestSnapshotsByAccount` read port — no new
read method — and the dashboard's hero mini-stat grid can show the parked vehicle's
tire pressures as a faithful translation of the Stitch design.

Decided in the 2026-07-28 dashboard-port review (leader ↔ user):
- **Typed columns over a JSONB-extraction read port.** Every other extracted field
  on `Snapshot` is a typed nullable column (battery, range, odometer, temps, the
  six charge-enrichment fields). A separate "extract tpms from raw_data at query
  time" read method would be the lone exception, paying a JSON parse on every read
  and fragmenting the extraction convention. Consistency wins.
- **Unit: bar (API-native), with a PSI companion.** Per `ai/go-conventions.md`
  every API-native unit has a companion derivation; Tesla sends bar, so bar is
  stored and a `PSI()` value-receiver computes PSI for display (the dashboard shows
  PSI). This mirrors the miles→km pattern already on `Snapshot`.
- **No new read method.** The fields ride on the existing `Snapshot` returned by
  `LatestSnapshotsByAccount` (and, once landed, by `SnapshotsByVehicleSince`); the
  gateway maps them to display strings the same way it maps battery/odometer today.

## What Changes

Primary module: **`internal/telemetry/`** (extraction + read port return type).
No new read method; this is a storage + return-type enrichment that flows through
existing ports. Gateway display wiring is a follow-on; the **telemetry side** is
this change's scope.

### (a) New typed columns on `vehicle_snapshots`

Four nullable columns added via a goose migration under
`internal/telemetry/db/migrations`:

```
tpms_pressure_fl  REAL  NULL,
tpms_pressure_fr  REAL  NULL,
tpms_pressure_rl  REAL  NULL,
tpms_pressure_rr  REAL  NULL
```

`NULL` semantics match the charge-enrichment fields: NULL means the vehicle did
not report TPMS at capture (some vehicles have no TPMS sensors, or the reading
was absent), OR the row predates this extraction. A reported 0.0 bar is stored
non-NULL (truthful reading), exactly as the charge fields treat 0. This preserves
the "NULL is reserved exclusively for pre-migration rows / not reported" invariant
already in the spec.

### (b) Extract on the write path (`snapshotFrom`)

Extend `snapshotFrom` (the DTO→domain mapping in the telemetry collector) to read
the four `tpms_pressure_*` values from `vehicle_state` and store them pointer-wrapped
so an absent field becomes NULL (boundary-nil convention, same as `AccessType` and
`SentryMode`). No change to the raw_data JSONB storage — the lossless payload is
unaffected.

### (c) Return-type enrichment on `Snapshot`

Add four `*float64` fields to `telemetry.Snapshot` (`TpmsPressureFL/FR/RL/RR`) and
a `PSI()` value-receiver companion on whichever single-field shape the convention
dictates (design.md decides: one helper per corner vs a single `TpmsPSI() [4]float64`
intermediate — recommendation is four small `*float64` fields + the gateway formats
directly with a shared `barToPSI` constant exposed for tests, mirroring how
`OdometerKm()` lives next to `Odometer`). Miles→km precedent: `milesToKm` is the
package-level factor; `barToPSI` is the analogous factor (`1 bar = 14.503773773 psi`).

### (d) Flow through the existing read port — no new read method

`LatestSnapshotsByAccount` (and `SnapshotsByVehicleOnce` once the history port
lands) SELECT the four new columns; the sqlc row scan maps them to the new `Snapshot`
fields. The gateway reads them as it reads battery/odometer today — no new `Reader`
method, no new query shape, no new index. The dashboard hero's tire-pressure tile
becomes a real value instead of an omitted/dropped stat.

## Breaking

Minimal and contained. Adding columns + struct fields is non-breaking at the
interface level (`Reader` is unchanged). The migration adds nullable columns with no
default and no NOT-NULL constraint, so existing rows are simply NULL (the
documented pre-migration state). Compile impact: the `Snapshot` struct gains four
fields; any struct literal that names fields is unaffected (named fields, not
positional), and the `fakeReader`/test snapshots that construct `Snapshot` literals
keep compiling (they omit the new optional fields, which default to nil). No change
to the `Collector` interface, the scheduler, or `cmd/poller`.

## Modules affected

- `internal/telemetry/` — primary: the migration, `snapshotFrom` extraction, the
  four new `Snapshot` fields + `PSI()` companion, the sqlc query edits to SELECT the
  new columns. No capture cadence change, no new table, no new read method.
- `internal/gateway/` — secondary, follow-on **only**: the dashboard hero mini-stat
  grid adds a Tire Pressure tile reading `snap.TpmsPressureFL` etc. That display
  wiring is a separate gateway change (kept out of this telemetry change so this
  change stays module-scoped, per the config rule against one-big-change).
- No other `internal/` module.

## Database Changes

Four nullable `REAL` columns on `vehicle_snapshots` (a table owned solely by
`internal/telemetry/db`), plus zero new indexes (the existing indexes serve the
existing read paths; tire pressure is read alongside other columns on the same row,
no new predicate). design.md is REQUIRED and includes: the exact `ALTER TABLE` DDL,
the NULL semantics rationale (matching the charge-enrichment precedent), the
no-index justification (no new query predicate; tpms is read on the same row as
battery/odometer), and the rejection note for the alternative "JSONB extraction at
query time" read port (rejected: lone exception to the typed-column convention,
per-read JSON parse cost, fragmented extraction). The database design gate
triggers and passes.

## Read Paths Affected

**None new, none changed in shape.** `LatestSnapshotsByAccount` and
`SnapshotsByVehicleSince` return four more columns per row (same `DISTINCT ON` /
range-scan plans; the extra columns ride on the same row fetch). No new predicate
means no new index and no plan change. The dashboard render path that already
fetches the latest snapshot now also receives tire pressure for free — same one
query, same one round-trip, no N+1.

## Capabilities

### Added / Modified Capabilities

- **`telemetry`** — extends "Nightly Vehicle Snapshot Capture" (the four TPMS
  fields are part of captured telemetry) and extends "Latest Snapshot Read Port"
  (the returned `Snapshot` now carries the four fields, NULL-preserved just like
  sentry-mode and the charge fields). Behavioral spec delta authoring deferred to
  the design step per the user's "create the proposals" ask; will cover: TPMS stored
  when reported, NULL when not reported OR pre-migration, sentry-style nil fidelity
  preserved on read, callers never touch the telemetry DB.

### Consumed Capabilities (no change to their specs)

- None — this enriches telemetry's own capture + return type.

## Resolved decisions

None yet — **proposal only** (per the user's 2026-07-28 "create the proposals"
ask). Open questions for the `grill-me` pass before `design.md` authoring
(cited by ID from design.md):

- **D1** — PSI companion shape: four `*float64` fields + a package-level
  `barToPSI` constant the gateway formats against, vs a `TpmsPSI()` receiver
  returning `[4]float64`. Recommendation: four `*float64` + `barToPSI` constant,
  mirroring the `Odometer`/`OdometerKm()` precedent closest (single field, single
  companion). A 4-element array helper is over-abstraction for a display value.
- **D2** — Display unit on the dashboard: PSI (Stitch shows "41 PSI") vs bar
  (API-native). Recommendation: PSI on the dashboard (faithful to Stitch); the port
  returns bar (stored) and the gateway converts, exactly as odometer returns miles
  and the gateway converts via `formatKm`. Confirm the existing `apex` design.
- **D3** — Which corner to surface in the hero mini-stat (the tile shows one
  number). Recommendation: an average of the four non-NULL corners (gates: if all
  four NULL, render "—"; if some NULL, average the non-NULL). The gateway computes
  the average from the four returned fields — no telemetry-side aggregation.

### Out of scope (explicitly deferred)

- **Tire-pressure history charts** — not in the dashboard today; the history port
  (`telemetry-add-snapshot-history-read-port`) returns these columns too once they
  exist, so a future tread-wear/leak trend chart is unlocked for free.
- **Low-pressure alerting** — the dashboard is read-only; any threshold/alert is a
  later analytics feature, not a read port.