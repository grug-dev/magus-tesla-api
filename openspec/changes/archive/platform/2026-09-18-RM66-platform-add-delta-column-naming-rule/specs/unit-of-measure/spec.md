## MODIFIED Requirements

### Requirement: Unit Suffix Naming
Every persisted column that carries a unit SHALL be named with a suffix
identifying that unit, and the domain field it maps to SHALL carry the same
suffix. The unit suffix SHALL be the last **unit-bearing** segment of the name: it
MAY be followed by exactly one trailing derivation marker — `_calc` (a computed
value) or `_delta_calc` (a day-over-day change) — neither of which is itself a
unit, but no other suffix SHALL follow the unit suffix or its marker. A column or
field carrying a unit SHALL NOT be named in a way that leaves its unit implicit or
discoverable only from a comment. Values that carry no unit — identifiers,
timestamps, dates, counts, names, states, and flags — SHALL NOT be given a unit
suffix.

#### Scenario: A unit-bearing column declares its unit in its name
- **GIVEN** a new column that stores a value with a unit
- **WHEN** the column is named
- **THEN** the name ends with a suffix identifying the unit, such as `_km`, `_kmh`, `_c`, `_psi`,
  `_kwh`, `_kw`, `_v`, `_a`, or `_pct`
- **AND** a reader can determine the value's unit from the column name alone, without opening a
  migration or a comment

#### Scenario: A derived column's unit suffix precedes its derivation marker
- **GIVEN** a column storing a computed value or a day-over-day change, and the
  value carries a unit
- **WHEN** the column is named
- **THEN** the unit suffix appears immediately after the quantity it names and
  immediately before a trailing `_calc` or `_delta_calc` marker
- **AND** no other segment follows that marker
- **AND** a reader can still determine the value's unit by reading the name up to
  the marker — for example `distance_traveled_km_calc` (a computed value in
  kilometres) and `tpms_pressure_fl_psi_delta_calc` (a day-over-day change in PSI)

#### Scenario: The domain field mirrors the column's unit suffix
- **GIVEN** a persisted unit-bearing column and the domain struct field it maps to
- **WHEN** the field is named
- **THEN** the field name carries the same unit as the column
- **AND** a caller reading the field knows its unit without consulting the schema

#### Scenario: Values without a unit take no suffix
- **GIVEN** a column storing an identifier, a timestamp, a date, a count, a name, a state, or a
  boolean flag
- **WHEN** the column is named
- **THEN** it carries no unit suffix, because it carries no unit
