## MODIFIED Requirements

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
