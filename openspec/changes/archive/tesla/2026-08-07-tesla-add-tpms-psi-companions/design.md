# Design — tesla-add-tpms-psi-companions

## Context

`internal/tesla` is the vendor adapter (`ai/architecture.md` §6): its `...Tesla`-suffixed DTOs
mirror the Fleet API payload field-for-field, in the API's native units, and it exposes
value-receiver companion methods for the converted equivalents. Today it has four:

| Method | Receiver | Source field | Factor |
|---|---|---|---|
| `BatteryRangeKm()` | `ChargeStateTesla` | `BatteryRange` (miles) | `milesToKm` |
| `ChargeRateKmh()` | `ChargeStateTesla` | `ChargeRate` (mph) | `milesToKm` |
| `SpeedKmh()` | `DriveStateTesla` | `Speed` (`*float64`, mph) | `milesToKm`, nil-safe |
| `OdometerKm()` | `VehicleStateTesla` | `Odometer` (miles) | `milesToKm` |

`VehicleStateTesla` also carries the four TPMS pressures in bar
(`internal/tesla/types.go:129-132`), added by the 2026-08-02 TPMS change — but with **no**
companion, because at the time the only consumer that needed PSI was `internal/telemetry`, which
grew its own `barToPSI` constant and four `Snapshot` methods instead.

RM7 tier 2 removes those telemetry-side methods (the domain field names collide with them once
suffixed) and needs the conversion at write time. This tier puts it where the other three factors
already live.

## Goals / Non-Goals

**Goals:**
- Expose bar→PSI conversion on the adapter DTO that owns the bar values.
- Own the `barToPSI` factor as a named package-level constant, never inline.
- Stay additive: no existing caller changes behavior, and the module compiles and ships alone.

**Non-Goals:**
- Changing `telemetry`'s `Snapshot` methods or fields — that is tier 2.
- Introducing pointer/nil semantics at the adapter layer (see D2).
- Any Fleet API call, scope, migration, or route change.
- Converting the DTO fields themselves — the DTO mirrors the API and stays in bar.

## Decisions

### D1 — Companions live on `VehicleStateTesla`, one per corner

Four separate methods rather than one returning a struct or a `[4]float64`. Rationale: it mirrors
the existing one-method-per-value pattern exactly (`OdometerKm()`), keeps call sites readable at
the `snapshotFrom` mapping site where each corner maps to its own column, and means an agent
looking for "how do I get FL pressure in PSI" finds `TpmsPressureFLPSI()` by the same naming rule
that produced `OdometerKm()`. **Rejected:** a `TpmsPressuresPSI() [4]float64` — fewer methods, but
introduces an index-order convention (which slot is RL?) that has to be looked up, which is
exactly the kind of hidden vocabulary the AI-efficiency rule warns against.

### D2 — Return plain `float64`, not `*float64`

The existing spec requirement says companion values for *optional* fields must be nil-safe, and
`SpeedKmh()` honours that because `DriveStateTesla.Speed` is `*float64`. The TPMS DTO fields are
**not** optional at this layer — they are plain `float64` (`types.go:129-132`), documented there as
"the Fleet API includes these in `vehicle_state` when the vehicle has TPMS sensors". So there is no
nil to preserve, and returning `*float64` would manufacture an absence the DTO cannot express.

The nil/not-reported distinction is real, but it is created one layer up: `telemetry.snapshotFrom`
pointer-wraps with `ptr()` so a truthful `0.0` is stored non-NULL while a pre-migration row stays
NULL. That stays exactly as it is; tier 2 simply converts before wrapping.

### D3 — `barToPSI = 14.503773773`, beside `milesToKm`

Same value the telemetry module uses today (`internal/telemetry/telemetry.go:34`), so the migration
in tier 2 and the runtime conversion agree to the digit. Declared as a named constant next to
`milesToKm` for the same reason that one is named: the factor must never be typed inline at a call
site. Tier 2 deletes the telemetry-side copy, leaving exactly one definition in the repo.

### D4 — No `raw.go` / explorer change

`CLAUDE.md` requires that every new Fleet API **call** added to `internal/tesla` gets a `Raw*`
sibling and explorer coverage. This change adds no call — only a derived accessor over data
`RawVehicleData` already returns in full. The rule does not trigger, and adding explorer surface
for a pure computation would put a paid, car-waking command behind something with no API cost.

## Risks / Trade-offs

- **[Two PSI implementations coexist until tier 2 lands]** → Both use the identical factor
  (`14.503773773`) over the same source value, so they cannot disagree. Tier 2 deletes the
  telemetry copy; RM7 Decision 8 already binds tiers 2–4 to the same branch, so the window is
  a single roadmap run. If RM7 is abandoned after tier 1, the leftover is four unused adapter
  methods — dead but harmless and spec-backed.
- **[A future vehicle that genuinely omits TPMS unmarshals to `0.0`, not absent]** → Pre-existing
  behavior of the DTO, unchanged by this tier and explicitly handled downstream by telemetry's
  pointer-wrap. Flagged rather than fixed: making the DTO fields `*float64` is a real change to
  vendor-mirroring semantics and belongs in its own change if the payload ever proves it necessary.

## Migration Plan

None — additive Go code with no persisted state, no schema object, and no external contract.
Rollback is deleting the four methods and the constant.

## Open Questions

None.
