# platform Specification

## Purpose
TBD - created by archiving change RM35-platform-add-tz-guard. Update Purpose after archive.
## Requirements
### Requirement: Time Zone Convention Guard
The platform SHALL provide a deterministic, automated check that fails the local build gate
whenever code outside `internal/clock` hand-rolls the current moment, a "midnight of some day"
time construction, or a hardcoded time-zone name, unless that occurrence is explicitly marked
as a deliberate, reviewed exception.

#### Scenario: A raw call to obtain the current moment outside the clock package fails the check
- **GIVEN** a Go source file outside `internal/clock` and outside any test file
- **WHEN** that file calls the standard library's current-time function directly, with no
  deliberate-exception marker on the same line
- **THEN** the platform's time zone convention guard fails
- **AND** it reports the offending file and line

#### Scenario: A hand-rolled "midnight of a day" construction outside the clock package fails the check
- **GIVEN** a Go source file outside `internal/clock` and outside any test file
- **WHEN** that file constructs a specific moment whose time-of-day components are all zeroed
  out, with no deliberate-exception marker on the same line
- **THEN** the platform's time zone convention guard fails
- **AND** it reports the offending file and line

#### Scenario: A hardcoded time-zone name outside the clock package fails the check
- **GIVEN** a Go source file outside `internal/clock` and outside any test file
- **WHEN** that file contains a literal time-zone identifier string, with no deliberate-exception
  marker on the same line
- **THEN** the platform's time zone convention guard fails
- **AND** it reports the offending file and line

#### Scenario: A marked deliberate exception does not fail the check
- **GIVEN** a line that would otherwise be flagged by the guard
- **WHEN** that line carries the guard's deliberate-exception marker as a trailing comment
- **THEN** the platform's time zone convention guard does not flag that line

#### Scenario: The clock package's own implementation does not fail the check
- **GIVEN** a file inside `internal/clock`
- **WHEN** that file contains any of the constructs the guard otherwise flags
- **THEN** the platform's time zone convention guard does not flag it

#### Scenario: Test files do not fail the check
- **GIVEN** a Go test file, anywhere in the codebase
- **WHEN** that file contains any of the constructs the guard otherwise flags
- **THEN** the platform's time zone convention guard does not flag it

#### Scenario: A comment describing the convention does not fail the check
- **GIVEN** a source-code comment line that mentions one of the guarded constructs only in
  prose, not as executable code
- **WHEN** the platform's time zone convention guard runs
- **THEN** that comment line is not flagged

#### Scenario: The composition root is out of scope
- **GIVEN** a file under the platform's command-line entry points (`cmd/`)
- **WHEN** that file contains any of the constructs the guard otherwise flags
- **THEN** the platform's time zone convention guard does not flag it, because entry-point files
  are never scanned by the guard

#### Scenario: The check is part of the full local gate
- **GIVEN** the platform's full local verification gate
- **WHEN** that gate runs
- **THEN** the time zone convention guard runs as one of its steps

### Requirement: Containerized Deploy Stack
The platform SHALL provide a Docker Compose deployment stack that runs the web
gateway, the telemetry poller, a PostgreSQL database, a one-shot schema-migration
step, and a reverse proxy providing automatic HTTPS, as a set of orchestrated
services. The database's data SHALL persist across container restarts and recreation
of the other services. Every long-running service SHALL restart automatically after
a crash or a host reboot, except the one-shot migration step, which SHALL run to
completion once and SHALL NOT be restarted automatically after a successful exit.

#### Scenario: The database's data survives a stack restart
- **GIVEN** the deployment stack is running with data already stored in the database
- **WHEN** the database service is restarted or recreated
- **THEN** the previously stored data is still present afterward

#### Scenario: A crashed long-running service restarts automatically
- **GIVEN** the deployment stack is running
- **WHEN** the web gateway, the poller, the database, or the reverse proxy service
  exits unexpectedly
- **THEN** that service is restarted automatically without manual intervention

#### Scenario: The migration step does not restart after a successful run
- **GIVEN** the one-shot migration service has completed successfully
- **WHEN** the deployment stack is otherwise running
- **THEN** the migration service is not restarted automatically

### Requirement: Ordered Startup — Database, Then Migrations, Then Application
The platform's deployment stack SHALL start the database and wait for it to report
healthy before applying schema migrations, and SHALL start the web gateway and the
poller only after the schema migrations have completed successfully. The web gateway
and the poller SHALL NOT be started while the database is unreachable or while
migrations have not yet completed successfully.

#### Scenario: The application waits for a healthy database before migrating
- **GIVEN** the deployment stack is starting for the first time
- **WHEN** the database has not yet reported healthy
- **THEN** the migration step has not yet started

#### Scenario: The application waits for migrations to finish before starting
- **GIVEN** the database has reported healthy and the migration step is running
- **WHEN** the migration step has not yet exited successfully
- **THEN** the web gateway and the poller have not yet started

#### Scenario: The application starts once migrations succeed
- **GIVEN** the migration step has exited successfully
- **WHEN** the deployment stack proceeds
- **THEN** the web gateway and the poller are started

### Requirement: No Secret Is Baked Into a Built Image
The platform's deployment stack SHALL supply every secret (database credentials,
Tesla and Google OAuth credentials, the session secret) to its containers as
environment variables at container start, and SHALL NOT embed any secret value
inside a built container image.

#### Scenario: The build context excludes local secret files
- **GIVEN** the platform's container image build process
- **WHEN** the image is built
- **THEN** the local `.env` file and any private key file are excluded from the build
  context and are absent from the resulting image

### Requirement: A Single Configuration Value Selects the Database Host
The platform's deployment stack SHALL determine which PostgreSQL instance the web
gateway, the poller, and the migration step connect to from a single configuration
value, such that pointing that value at a different reachable PostgreSQL instance
requires no source code change.

#### Scenario: Pointing at an external database requires no code change
- **GIVEN** the deployment stack is configured to use its own bundled database
  service
- **WHEN** the single database configuration value is changed to point at a different,
  externally hosted PostgreSQL instance and the bundled database service is removed
  from the deployment
- **THEN** the web gateway, the poller, and the migration step connect to the
  externally hosted instance without any source code change

