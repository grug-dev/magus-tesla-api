## ADDED Requirements

### Requirement: Platform Display Units
The platform SHALL standardise on one unit per physical quantity for every persisted value and
every domain type, and SHALL NOT store the same quantity in different units in different places.
Distance and range SHALL be expressed in kilometres, speeds and rates in kilometres per hour,
temperature in degrees Celsius, tire pressure in PSI, energy in kilowatt-hours, power in
kilowatts, voltage in volts, electric current in amperes, and proportions as a percentage.

#### Scenario: A newly persisted distance value uses kilometres
- **GIVEN** a module that persists a distance, range, or odometer value
- **WHEN** the value is stored
- **THEN** it is stored in kilometres
- **AND** no other unit of distance appears in any persisted column or domain field

#### Scenario: A newly persisted temperature value uses degrees Celsius
- **GIVEN** a module that persists a temperature value
- **WHEN** the value is stored
- **THEN** it is stored in degrees Celsius

#### Scenario: A newly persisted pressure value uses PSI
- **GIVEN** a module that persists a tire-pressure value
- **WHEN** the value is stored
- **THEN** it is stored in PSI

### Requirement: Unit Suffix Naming
Every persisted column that carries a unit SHALL be named with a suffix identifying that unit, and
the domain field it maps to SHALL carry the same suffix. A column or field carrying a unit SHALL
NOT be named in a way that leaves its unit implicit or discoverable only from a comment. Values
that carry no unit — identifiers, timestamps, dates, counts, names, states, and flags — SHALL NOT
be given a unit suffix.

#### Scenario: A unit-bearing column declares its unit in its name
- **GIVEN** a new column that stores a value with a unit
- **WHEN** the column is named
- **THEN** the name ends with a suffix identifying the unit, such as `_km`, `_kmh`, `_c`, `_psi`,
  `_kwh`, `_kw`, `_v`, `_a`, or `_pct`
- **AND** a reader can determine the value's unit from the column name alone, without opening a
  migration or a comment

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

### Requirement: Conversion Happens On Write, Never On Read
The platform SHALL convert a value into its display unit exactly once, on the path that persists
it, and SHALL NOT convert units on any read path. A read port SHALL return values already
expressed in their display units, and SHALL NOT require or offer a conversion method for a
persisted field. This ordering is required because the platform's workload is read-heavy: a value
is written once per collection cycle and read on every render.

#### Scenario: A stored value is converted once, at capture
- **GIVEN** an external source that reports a value in a unit other than the platform's display
  unit
- **WHEN** that value is persisted
- **THEN** the conversion is applied on the write path before the value is stored
- **AND** the stored value is in the platform display unit

#### Scenario: A consumer reads a value without converting it
- **GIVEN** a consumer that reads a persisted unit-bearing value through a module's read port
- **WHEN** it uses the value
- **THEN** the value is already in its display unit
- **AND** the consumer applies no conversion factor and calls no conversion method

#### Scenario: Formatting is presentation, and the stored value carries none
- **GIVEN** a unit-bearing value that will be displayed to a user
- **WHEN** it is stored
- **THEN** it is stored as a plain unformatted number, with no rounding, thousands separators, or
  unit symbol embedded in it
- **AND** rounding, thousands separators, and unit labels are applied only when the value is
  rendered

### Requirement: Vendor Adapter DTOs Are Exempt And Own The Conversion
A vendor adapter's data-transfer objects SHALL mirror the external service's payload in the units
that service reports, and SHALL NOT be converted into platform display units. Every unit
conversion factor the platform uses SHALL be defined in the vendor adapter that owns the
corresponding external values, exposed as companion accessors, so that a conversion factor exists
in exactly one module. A persisting module SHALL obtain converted values by calling those
companions rather than by performing its own arithmetic.

#### Scenario: An adapter DTO keeps the external service's units
- **GIVEN** an external service that reports a value in a unit other than the platform display
  unit
- **WHEN** the adapter's DTO exposes that value
- **THEN** the DTO reports it in the unit the service reported
- **AND** the DTO also exposes a companion accessor returning the platform display unit

#### Scenario: Conversion factors live in exactly one module
- **GIVEN** any unit conversion the platform performs
- **WHEN** the conversion factor is located
- **THEN** it is defined once, as a named constant in the vendor adapter module
- **AND** no other module defines its own copy of that factor and no call site writes it inline

#### Scenario: A persisting module converts by delegation
- **GIVEN** a module that persists a value obtained from a vendor adapter in a non-display unit
- **WHEN** it maps the adapter's DTO onto its own stored type
- **THEN** it obtains the display-unit value from the adapter's companion accessor
- **AND** it performs no unit arithmetic of its own

### Requirement: Monetary Amounts Are Exempt And Require A Currency Column
A persisted monetary amount SHALL NOT carry a unit suffix, because its unit is determined at
runtime rather than at schema-design time. Every persisted monetary amount SHALL instead be
accompanied by a column recording the currency the amount is expressed in, and a consumer SHALL
NOT interpret a monetary amount without reading its currency.

#### Scenario: A monetary column is paired with a currency column
- **GIVEN** a table that stores a price, cost, or other monetary amount
- **WHEN** the schema is defined
- **THEN** the monetary column carries no unit suffix
- **AND** the table also stores the currency that amount is expressed in

#### Scenario: A monetary amount is never assumed to be one currency
- **GIVEN** a consumer displaying or aggregating a stored monetary amount
- **WHEN** it presents the value
- **THEN** it reads the accompanying currency rather than assuming a default
