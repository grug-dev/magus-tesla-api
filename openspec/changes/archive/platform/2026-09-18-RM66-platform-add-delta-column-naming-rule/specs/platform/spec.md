## ADDED Requirements

### Requirement: Delta Column Naming Guard
The platform SHALL provide a deterministic, automated check that fails the local
build gate whenever a new column or Go field storing a day-over-day change (a
value derived by subtracting yesterday's value of a metric from today's value of
the same metric) is named with a bare `_calc`/`Calc` suffix instead of
`_delta_calc`/`DeltaCalc`, unless that name is either a pre-existing name recorded
in the guard's own baseline or explicitly marked as a deliberate, reviewed
exception.

#### Scenario: A new SQL column named with a bare `_calc` suffix fails the check
- **GIVEN** a Go migration file defining a new column whose name ends in `_calc`
- **WHEN** that name is neither in the guard's baseline nor followed by a
  deliberate-exception marker on the same line
- **THEN** the platform's delta column naming guard fails
- **AND** it reports the offending file and line

#### Scenario: A new Go field named with a bare `Calc` suffix fails the check
- **GIVEN** a non-test Go source file declaring a new struct field whose name ends
  in `Calc`
- **WHEN** that name is neither in the guard's baseline nor followed by a
  deliberate-exception marker on the same line
- **THEN** the platform's delta column naming guard fails
- **AND** it reports the offending file and line

#### Scenario: A name already in the baseline warns instead of failing
- **GIVEN** a column or Go field name already recorded in the guard's baseline of
  pre-rule names
- **WHEN** the platform's delta column naming guard runs
- **THEN** the guard prints a warning naming that occurrence
- **AND** the guard does not fail because of it

#### Scenario: A marked deliberate exception does not fail the check
- **GIVEN** a new bare `_calc`/`Calc` name that is not a day-over-day delta
- **WHEN** that line carries the guard's deliberate-exception marker as a trailing
  comment
- **THEN** the platform's delta column naming guard does not flag that line

#### Scenario: A correctly named `_delta_calc`/`DeltaCalc` column or field does not fail the check
- **GIVEN** a column or Go field whose name ends in `_delta_calc` or `DeltaCalc`
- **WHEN** the platform's delta column naming guard runs
- **THEN** the guard does not flag it

#### Scenario: A reference to an existing column outside its own definition is not scanned
- **GIVEN** a `query.sql` file, a `COMMENT ON` statement, or a `_test.go` file that
  mentions an existing `_calc`/`Calc` name
- **WHEN** the platform's delta column naming guard runs
- **THEN** it does not flag that mention, because the guard scans only where a
  column or field is defined, never where it is only used or described

#### Scenario: The baseline only shrinks
- **GIVEN** a change renames a baselined `_calc`/`Calc` name to `_delta_calc`/`DeltaCalc`
- **WHEN** that rename lands
- **THEN** the renamed name's entry is removed from the guard's baseline
- **AND** no name is ever added back to the baseline once removed

#### Scenario: The check is part of the full local gate
- **GIVEN** the platform's full local verification gate
- **WHEN** that gate runs
- **THEN** the delta column naming guard runs as one of its steps
