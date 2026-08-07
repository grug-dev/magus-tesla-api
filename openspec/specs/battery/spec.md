# battery Specification

## Purpose
Derived battery analytics over stored telemetry — the platform's metrics layer, sitting between
what `telemetry` captures and what the dashboard renders. It owns no database and no capture: it
reads sibling modules' public ports and computes values none of them store. Its first metric is
rolling energy-per-kilometre (Wh/km).
## Requirements
### Requirement: Recent Energy-Per-Kilometre Derivation
The battery capability SHALL derive a rolling energy-per-kilometre (Wh/km) value for a given
vehicle over a fixed window (default 30 days, fixed at construction time), from that vehicle's
stored telemetry snapshots plus its Supercharger sessions and manually-logged charge entries
within the window. The capability SHALL consume the distance values in the unit the telemetry read
port already provides them in, and SHALL NOT perform any unit conversion of its own. The
capability SHALL compute the energy numerator as measured charging energy (kWh) minus a
pack-capacity correction for net SoC drift over the window, using whichever of
`UsableBatteryLevel` or `BatteryLevel` is consistently available at BOTH window endpoints (never a
mixed pair). The capability SHALL scope every underlying read to the given account, even when the
vehicle identifier alone would suffice, as defense-in-depth tenant isolation.

#### Scenario: A vehicle with two or more snapshots, known capacity, and net consumption gets a computed value
- **GIVEN** a vehicle with at least two telemetry snapshots inside the window, whose Fleet API
  `car_type` is present in the platform's pack-capacity reference table
- **AND** the vehicle's net battery state-of-charge decreased over the window (it consumed more
  than it charged)
- **WHEN** the capability derives recent efficiency for that vehicle
- **THEN** it returns a Wh/km value derived from measured charging energy corrected by the
  pack-capacity SoC-drift term, `ok=true`, and `Approximate=false`

#### Scenario: Distance is read, not converted
- **GIVEN** a vehicle with at least two telemetry snapshots inside the window
- **WHEN** the capability computes the distance travelled over the window
- **THEN** it takes the kilometre values directly from the snapshots returned by the telemetry
  read port
- **AND** it applies no unit-conversion factor and calls no conversion method to obtain them
- **AND** the derived Wh/km value is the same as it was when the conversion was performed on read

#### Scenario: Usable battery level is used only when present at both window endpoints
- **GIVEN** a vehicle whose first snapshot in the window has a non-nil `UsableBatteryLevel` and
  whose last snapshot in the window has a nil `UsableBatteryLevel`
- **WHEN** the capability computes the state-of-charge delta for that window
- **THEN** it uses `BatteryLevel` at BOTH endpoints (never usable at one endpoint and nominal at
  the other)

#### Scenario: Usable battery level is used when present at both window endpoints
- **GIVEN** a vehicle whose first and last snapshots in the window both have a non-nil
  `UsableBatteryLevel`
- **WHEN** the capability computes the state-of-charge delta for that window
- **THEN** it uses `UsableBatteryLevel` at both endpoints

### Requirement: Unknown Pack Capacity Yields an Approximate Value, Never a Blank Tile
The battery capability SHALL NOT refuse to return an efficiency value solely because the
vehicle's pack capacity is unknown (its `car_type` is absent, or not present in the
pack-capacity reference table). In that case the capability SHALL compute energy from measured
charging energy alone (no SoC-drift correction) and SHALL mark the result `Approximate=true`.

#### Scenario: Unknown car_type still returns a value, marked approximate
- **GIVEN** a vehicle whose registered `CarType` is nil, or whose `CarType` is not present in the
  pack-capacity reference table
- **AND** the vehicle otherwise has enough snapshots and positive net consumption to compute a
  value
- **WHEN** the capability derives recent efficiency for that vehicle
- **THEN** it returns a Wh/km value computed from measured charging energy without any
  capacity correction, `ok=true`, and `Approximate=true`

#### Scenario: Known car_type applies the capacity correction and is not marked approximate
- **GIVEN** a vehicle whose registered `CarType` is present in the pack-capacity reference table
- **WHEN** the capability derives recent efficiency for that vehicle
- **THEN** the returned value's `Approximate` field is `false`

### Requirement: Insufficient Data Returns ok=false, Never a Fabricated Value
The battery capability SHALL return `ok=false` with no error — never a fabricated or
divide-by-near-zero value — in each of these cases: fewer than two telemetry snapshots exist in
the window; the vehicle's odometer reading did not increase over the window (parked, or a
non-increasing reading); or the derived energy consumed is zero or negative (net charge over the
window exceeded consumption).

#### Scenario: Fewer than two snapshots in the window
- **GIVEN** a vehicle with zero or exactly one telemetry snapshot inside the window
- **WHEN** the capability derives recent efficiency for that vehicle
- **THEN** it returns `ok=false` and no error

#### Scenario: No distance moved over the window
- **GIVEN** a vehicle with at least two snapshots in the window whose odometer reading at the
  window's end is not greater than its odometer reading at the window's start
- **WHEN** the capability derives recent efficiency for that vehicle
- **THEN** it returns `ok=false` and no error

#### Scenario: Net charge exceeds consumption over the window
- **GIVEN** a vehicle with at least two snapshots and positive distance moved over the window
- **AND** the derived energy consumed (measured charging energy minus the SoC-drift correction,
  or measured charging energy alone when capacity is unknown) is zero or negative
- **WHEN** the capability derives recent efficiency for that vehicle
- **THEN** it returns `ok=false` and no error

### Requirement: Multi-Tenant Scoping on Every Underlying Read
The battery capability SHALL pass the given account identifier to every underlying port call it
makes (telemetry snapshot history, Supercharger sessions, manually-logged charge entries, and
vehicle registration lookup), so that a caller can never retrieve another account's data by
supplying a vehicle identifier alone.

#### Scenario: Every underlying read is scoped to the given account
- **GIVEN** a request to derive recent efficiency for a specific account and vehicle
- **WHEN** the capability performs its underlying reads
- **THEN** every one of those reads is scoped to the given account identifier

### Requirement: No Cross-Module Database Access
The battery capability SHALL own no database of its own and SHALL access telemetry, manual
charge, and account data exclusively through those modules' public read ports — never through a
shared database connection, another module's generated query package, or any other bypass of the
module boundary.

#### Scenario: The capability owns no database
- **GIVEN** the battery capability's implementation
- **WHEN** its data dependencies are inspected
- **THEN** it imports only the public `Reader`/`SuperchargerReader` interfaces of
  `internal/telemetry`, the public `Reader` interface of `internal/manualcharge`, and the public
  `Service` interface of `internal/account` — never `internal/telemetry/db`,
  `internal/manualcharge/db`, or `internal/account/db`

