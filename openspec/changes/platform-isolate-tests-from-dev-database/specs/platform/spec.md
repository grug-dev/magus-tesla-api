## ADDED Requirements

### Requirement: Test Database Isolation
The platform's database-backed tests SHALL NOT use the application's own
configured database. Tests SHALL select the database they run against from a
setting dedicated to testing, kept separate from the application's own
database setting. When that test-only setting is absent or does not point at
a reachable database, tests SHALL provision a disposable database of their
own and apply the required schema to it before running. Running tests
against a persistent, real database SHALL require a developer to set the
test-only setting on purpose — it SHALL NOT happen as a side effect of the
application's own database being configured.

#### Scenario: The application's database is configured, but tests still use a disposable one
- **GIVEN** the application's own database setting is configured and points at a
  reachable database
- **AND** the dedicated test-only database setting is absent
- **WHEN** a database-backed test runs
- **THEN** the test provisions and uses a disposable database instead of the
  application's configured database
- **AND** no data is written to the application's configured database

#### Scenario: The test-only setting points tests at a real database
- **GIVEN** the dedicated test-only database setting names a database that is
  reachable
- **WHEN** a database-backed test runs
- **THEN** the test applies the required schema to that named database and
  runs against it, instead of provisioning a disposable one

#### Scenario: No database can be provisioned at all, and the run is skipped, not silently passed
- **GIVEN** the dedicated test-only database setting is absent or unreachable
- **AND** no mechanism exists on the machine to start a disposable database
- **WHEN** a database-backed test run starts
- **THEN** the run is skipped
- **AND** the skip is reported, so it is never mistaken for a pass

#### Scenario: A genuinely broken test database setup fails loudly instead of being skipped
- **GIVEN** a disposable database has been started successfully
- **WHEN** applying the required schema to it fails
- **THEN** the test run fails outright
- **AND** it is not silently skipped or treated as a pass, because a schema
  that fails to apply means the test setup itself is broken, not merely that
  no database was available
