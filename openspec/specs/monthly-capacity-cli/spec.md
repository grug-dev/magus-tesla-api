# monthly-capacity-cli Specification

## Purpose
An operator tool that measures a vehicle's effective pack capacity for one calendar month, on
demand. The nightly cycle already does this on the first day of each month. This capability
covers the by-hand path: re-running a month that failed, backfilling a past month, or checking
one vehicle. It is local only and needs nothing but a database connection.
## Requirements
### Requirement: A Person Can Run The Monthly Capacity Measurement By Hand

The platform SHALL provide a command-line tool that computes one calendar month's effective
pack capacity, for one vehicle or for every vehicle, on request. The tool SHALL default to
the calendar month immediately before the current one when no month is given, and SHALL
default to every vehicle with at least one usable charge record when no single vehicle is
given.

#### Scenario: Running the tool with no arguments measures the previous month, for every vehicle
- **GIVEN** the tool is run with neither a month nor a vehicle specified
- **WHEN** it runs
- **THEN** it measures the calendar month immediately before the current one
- **AND** it covers every vehicle with at least one usable charge record that month

#### Scenario: Running the tool with a month measures exactly that month
- **GIVEN** the tool is run with a specific calendar month given
- **WHEN** it runs
- **THEN** it measures exactly the given month, not the previous one

#### Scenario: Running the tool with a vehicle measures only that vehicle
- **GIVEN** the tool is run with a specific vehicle given
- **WHEN** it runs
- **THEN** it measures only that vehicle, not every vehicle

### Requirement: An Invalid Month Is Rejected Before Any Work Starts

The platform's monthly capacity tool SHALL reject a month value that is not a real calendar
month, before connecting to any database or performing any measurement, and SHALL exit
with a failure status.

#### Scenario: A month with an out-of-range value is rejected
- **GIVEN** the tool is run with a month value naming a month number that does not exist
- **WHEN** it runs
- **THEN** it exits with a failure status
- **AND** no measurement is attempted

#### Scenario: A month in the wrong shape is rejected
- **GIVEN** the tool is run with a month value that does not match the expected
  year-and-month shape
- **WHEN** it runs
- **THEN** it exits with a failure status
- **AND** no measurement is attempted

### Requirement: The Tool Reports What It Measured And Exits Accordingly

The platform's monthly capacity tool SHALL report, after a successful run, the month it
measured, how many vehicles it considered, how many received a measured capacity, and how
many were left thin. On any failure — an invalid input, a database problem, or a
measurement failure — it SHALL report the failure and exit with a failure status, and SHALL
NOT report a successful measurement.

#### Scenario: A successful run reports its own summary
- **GIVEN** the tool runs to completion without error
- **WHEN** it finishes
- **THEN** it reports the month it measured, the number of vehicles considered, the number
  measured, and the number left thin
- **AND** it exits with a success status

#### Scenario: A failed run reports the failure and exits accordingly
- **GIVEN** the tool encounters an error at any point — invalid input, a database problem,
  or a measurement failure
- **WHEN** it runs
- **THEN** it reports the failure
- **AND** it exits with a failure status
- **AND** it does not report a successful measurement

### Requirement: The Tool Needs Only A Database Connection, Never A Tesla Credential

The platform's monthly capacity tool SHALL start and run using only a database connection.
It SHALL NOT require any Tesla API credential, since it never calls the Tesla Fleet API.

#### Scenario: The tool starts with a database connection and no Tesla credential configured
- **GIVEN** a database connection is available and no Tesla API credential is configured
- **WHEN** the tool starts
- **THEN** it starts successfully, without error about a missing Tesla credential

#### Scenario: The tool cannot start without a database connection
- **GIVEN** no database connection is configured
- **WHEN** the tool starts
- **THEN** it fails to start
- **AND** it reports that a database connection is required

### Requirement: The Tool Is Not Part Of The Deployed Production Image

The platform's monthly capacity tool SHALL be usable only from a local checkout against a
database the operator can reach directly. It SHALL NOT be included in the platform's
deployed production container image.

#### Scenario: The production image does not contain the tool
- **GIVEN** the platform's production container image
- **WHEN** its contents are inspected
- **THEN** the monthly capacity tool is not among the binaries it contains

