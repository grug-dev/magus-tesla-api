## MODIFIED Requirements

### Requirement: Recent Energy-Per-Kilometre Derivation

The analytics capability SHALL derive a rolling energy-per-kilometre (Wh/km) value for a given
vehicle over a fixed window (default 30 days, fixed at construction time), from that vehicle's
stored telemetry snapshots plus its Supercharger sessions and manually-logged charge entries
within the window. The capability SHALL consume the distance values in the unit the telemetry read
port already provides them in, and SHALL NOT perform any unit conversion of its own. The
capability SHALL compute the energy numerator as measured charging energy (kWh) minus a
pack-capacity correction for net SoC drift over the window, using whichever of
`UsableBatteryLevel` or `BatteryLevel` is consistently available at BOTH window endpoints (never a
mixed pair). The capability SHALL scope its telemetry, Supercharger, and manual-charge-entry
reads by the given vehicle identifier alone. The capability SHALL use the given account
identifier for exactly one purpose — resolving the vehicle's car type, the pack-capacity lookup —
and SHALL NOT use it to scope the telemetry, Supercharger, or manual-charge-entry reads.

**This is a CHANGE from the prior revision of this requirement, under which it stated that the
capability scoped every underlying read to the given account, even when the vehicle identifier
alone would suffice, as defense-in-depth tenant isolation.** That statement was never accurate:
the capability's telemetry, Supercharger, and manual-charge-entry reads
(`SnapshotsByVehicleSince`, `ListSessionsByVehicle`, `ListEntriesByVehicle`) have always taken a
vehicle identifier alone. The account identifier the capability receives is real and still used —
it resolves the vehicle's car type for the pack-capacity lookup — but it was never a scoping
parameter on the other three reads.

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

#### Scenario: The account identifier resolves car type only, never scopes the vehicle reads
- **GIVEN** a request to derive recent efficiency for a specific account and vehicle
- **WHEN** the capability performs its underlying reads
- **THEN** the telemetry, Supercharger, and manual-charge-entry reads are scoped to the vehicle
  identifier alone
- **AND** the account identifier is used only to resolve that vehicle's car type

## REMOVED Requirements

### Requirement: Multi-Tenant Scoping on Every Underlying Read

**Reason**: MAG-63 reversed the platform's tenancy rule: multi-tenant tables now key on vehicle
identity (`tesla_id` or `vin`), not `account_id`. This requirement predates that reversal — it
said the capability passes the given account identifier to every underlying read (telemetry
snapshot history, Supercharger sessions, manually-logged charge entries, and vehicle
registration lookup). That is no longer true: verified against `internal/analytics/reader.go`'s
`RecentEfficiency`, the account identifier reaches exactly one call, a car-type lookup — the
telemetry, Supercharger, and manual-entry reads all take a vehicle identifier alone.

A second requirement further down this spec, also named "Multi-Tenant Scoping On Every
Underlying Read" (capitalized "On"), already states the platform's current rule for this
capability's other derivation: scope by vehicle identity alone, and prove account ownership
before the capability is called. Keeping both left the spec teaching two contradictory rules
under near-identical names. This one is removed; the correct one is unchanged.
