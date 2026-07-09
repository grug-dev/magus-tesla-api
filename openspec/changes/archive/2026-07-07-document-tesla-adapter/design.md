## Context

The `internal/tesla` adapter (`client.go`, `vehicles.go`, `types.go`) predates OpenSpec
adoption in this repo. It is the anti-corruption boundary over the Tesla Fleet API
(`ai/architecture.md` §6) and the port that later tiers (Tesla connect, vehicle dashboard)
will call. This change **records** the adapter's behavior; it makes no implementation
decisions, because the implementation already exists and is not being changed.

## Goals / Non-Goals

**Goals**
- A faithful behavioral spec of the adapter as it exists today.
- A durable WHAT that the gateway/dashboard tiers can build against without reading adapter
  internals.

**Non-Goals**
- Changing the adapter in any way.
- Adding tests (a later change may add characterization tests).
- Specifying transport mechanics (base URL, headers, JSON decoding) — that is HOW and stays in
  code + `ai/architecture.md`.

## Decisions

### WHAT vs HOW split
- **WHAT (this spec):** the operations offered, the wake-before-data ordering, the distinct
  unauthorized signal, the stateless per-call credential model, and the availability of
  metric-converted values.
- **HOW (stays in code / `ai/*.md`):** the Fleet API base URL, the `Bearer` auth header, JSON
  envelope decoding, the `...Tesla` DTO suffix convention (§6), the exact `milesToKm` constant,
  and `http.Client` construction.

### Assert the stateless credential model as behavior
"The adapter is stateless about identity" reads partly like architecture (§5), but it is also
an **observable, caller-facing contract**: a caller must pass `Credentials` on every call and
may safely share a single instance across users. We assert it so the multi-tenant gateway can
rely on it. *(Confirmed while grilling the design.)*

### Enumerate the snapshot's data categories
The snapshot requirement enumerates the four categories the adapter returns — charge, climate,
drive, vehicle-state — rather than staying abstract, so downstream consumers and the
metric-conversion scenarios can reference concrete values. *(Confirmed while grilling.)*

## Requirement → source-symbol map

Used at verify/archive time to confirm the spec still matches the code.

| Requirement | Source (package `tesla`) |
|---|---|
| Vehicle inventory listing | `Client.ListVehicles` (`vehicles.go`); `listResponseTesla`, `VehicleTesla` (`types.go`) |
| Full vehicle snapshot | `Client.VehicleData` (`vehicles.go`); `VehicleDataTesla` + `ChargeStateTesla` / `ClimateStateTesla` / `DriveStateTesla` / `VehicleStateTesla` (`types.go`) |
| Vehicle wake | `Client.WakeUp` (`vehicles.go`); `wakeResponseTesla` (`types.go`) |
| Wake-before-data ordering | Doc contract on `VehicleData` / `WakeUp` (`vehicles.go`) |
| Distinct unauthorized signal | `ErrUnauthorized` + 401 branch in `Client.do` (`client.go`) |
| Stateless per-call credentials | `Credentials`, `VehicleService`, `Client`, `NewClient` (`client.go`) |
| Metric conversion of distances | `BatteryRangeKm`, `ChargeRateKmh`, `SpeedKmh`, `OdometerKm` (`types.go`) |

## Risks / Trade-offs

- **Spec/code drift** — a retro-spec can fall out of sync with the code. Mitigated by the
  source map above and an archive-time review against the current adapter.
- **Overlap with architecture docs** — asserting the stateless-credential model duplicates a
  point in `ai/architecture.md` §5. Accepted, because it is a caller-facing contract that
  consumers must be able to rely on from the capability spec alone.
