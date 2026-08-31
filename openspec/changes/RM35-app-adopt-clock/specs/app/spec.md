## ADDED Requirements

### Requirement: Scheduler Default Time Zone
The app capability SHALL determine the daily collection schedule's time zone using the
platform's default time zone, `America/Bogota`, whenever the scheduler is constructed with
no explicit time zone — never the host process's own local time zone. When the scheduler IS
constructed with an explicit time zone, that configured zone SHALL continue to determine the
schedule, unaffected by the platform default.

#### Scenario: An explicitly configured time zone determines the daily schedule
- **GIVEN** the scheduler is constructed with an explicit time zone
- **WHEN** it computes the next daily run time
- **THEN** the next run time is computed by observing the target hour:minute in that
  configured time zone
- **AND** the platform default time zone plays no part in the computation

#### Scenario: No explicitly configured time zone falls back to the platform default
- **GIVEN** the scheduler is constructed with no explicit time zone
- **WHEN** it computes the next daily run time
- **THEN** the next run time is computed by observing the target hour:minute in the
  platform's default time zone, `America/Bogota`
- **AND** the next run time is NOT computed by observing the target hour:minute in the host
  process's own local time zone
