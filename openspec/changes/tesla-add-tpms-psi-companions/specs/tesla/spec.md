## ADDED Requirements

### Requirement: Companion Conversion of Tire Pressure Values
The adapter SHALL expose a companion value in PSI (pounds per square inch) for every tire-pressure
value it exposes in bar, the Fleet API's native pressure unit. The conversion factor SHALL be a
single named package-level constant, never written inline at a call site, and SHALL be the same
factor used anywhere else in the platform that converts bar to PSI. The companion SHALL be a
value-receiver method on the type that carries the bar value, following the same naming and
receiver conventions as the adapter's existing distance and speed companions.

#### Scenario: Tire pressure available in PSI for all four corners
- **GIVEN** a vehicle snapshot reporting tire pressure in bar for the front-left, front-right,
  rear-left, and rear-right corners
- **WHEN** a caller reads the companion pressure values
- **THEN** the adapter provides each of the four pressures converted to PSI
- **AND** each converted value is the corresponding bar value multiplied by the adapter's single
  bar-to-PSI conversion constant

#### Scenario: A truthfully reported zero bar converts to zero PSI
- **GIVEN** a vehicle snapshot reporting a tire pressure of exactly 0.0 bar for one corner
  (for example, a flat tire)
- **WHEN** a caller reads that corner's companion pressure value
- **THEN** the adapter reports 0.0 PSI
- **AND** the adapter does not treat the zero as an absent reading

#### Scenario: The bar values the adapter exposes are unchanged
- **GIVEN** a vehicle snapshot reporting tire pressure in bar
- **WHEN** a caller reads the adapter's tire-pressure values directly rather than the companions
- **THEN** the values are still expressed in bar, exactly as the Fleet API reported them
- **AND** the adapter performs no conversion on the values it mirrors from the payload
