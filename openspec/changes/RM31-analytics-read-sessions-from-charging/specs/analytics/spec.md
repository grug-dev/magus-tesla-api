## MODIFIED Requirements

### Requirement: No Cross-Module Database Access

The analytics capability SHALL own no database of its own and SHALL access telemetry, manual
charge, and account data exclusively through those modules' public read ports — never through a
shared database connection, another module's generated query package, or any other bypass of the
module boundary.

#### Scenario: The capability owns no database
- **GIVEN** the analytics capability's implementation
- **WHEN** its data dependencies are inspected
- **THEN** it imports only the public `Reader` interface of `internal/telemetry`, the public
  `Reader` and `SuperchargerSessionAnalyticsReader` interfaces of `internal/charging`, and the
  public `Service` interface of `internal/account` — never `internal/telemetry/db`,
  `internal/charging/db`, or `internal/account/db`
