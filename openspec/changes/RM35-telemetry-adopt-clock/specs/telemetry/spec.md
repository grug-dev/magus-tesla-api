## ADDED Requirements

### Requirement: Snapshot Calendar Day Default Time Zone
When nightly collection is configured with no explicit collection time zone, the telemetry
capability SHALL determine the calendar day a snapshot belongs to using the platform's default
time zone, `America/Bogota`, rather than the host process's own local time zone. When
collection IS configured with an explicit collection time zone, that configured zone SHALL
continue to determine the calendar day, unaffected by the platform default.

#### Scenario: An explicitly configured collection time zone determines the calendar day
- **GIVEN** nightly collection is configured with an explicit collection time zone
- **WHEN** a collection cycle captures a snapshot
- **THEN** the snapshot's calendar day is computed by observing the capture instant in that
  configured time zone
- **AND** the platform default time zone plays no part in the computation

#### Scenario: No explicitly configured collection time zone falls back to the platform default
- **GIVEN** nightly collection is configured with no explicit collection time zone
- **WHEN** a collection cycle captures a snapshot
- **THEN** the snapshot's calendar day is computed by observing the capture instant in the
  platform's default time zone, `America/Bogota`
- **AND** the snapshot's calendar day is NOT computed by observing the capture instant in the
  host process's own local time zone
