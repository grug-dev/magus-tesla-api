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

### Requirement: Bounded Container Log Growth
The platform's deployment stack SHALL cap the on-disk size of each service's
container logs, so no single service can grow its log data without limit.

#### Scenario: A service that logs continuously does not fill the disk
- **GIVEN** the deployment stack is running with log rotation configured
- **WHEN** a service writes log output continuously, for example during a crash-restart
  loop
- **THEN** that service's on-disk log data stops growing once it reaches the
  configured cap
- **AND** older log data is discarded to make room for newer log data

### Requirement: Per-Service Resource Limits
The platform's deployment stack SHALL apply a CPU limit and a memory limit to every
service, so that one service consuming excessive CPU or memory cannot deprive
another service, in particular the database, of the resources it needs to keep
running.

#### Scenario: A runaway service is capped instead of starving others
- **GIVEN** the deployment stack is running with resource limits configured
- **WHEN** one service attempts to consume more CPU or memory than its configured
  limit
- **THEN** that service's consumption is capped at its configured limit
- **AND** the other services continue to receive the resources their own limits
  allow

#### Scenario: Resource limits are configurable without a source code change
- **GIVEN** the deployment stack's resource limits are set from configuration values
- **WHEN** an operator changes a service's configured CPU or memory limit
- **THEN** the new limit takes effect on the next start of that service
- **AND** no source file changes

### Requirement: Least-Privilege Container Execution
The platform's deployment stack SHALL run every service with no more Linux
capabilities, filesystem write access, and privilege-escalation ability than that
service needs to perform its function.

#### Scenario: A service starts successfully under its hardened privilege set
- **GIVEN** a service configured with its minimum required capabilities and
  filesystem access
- **WHEN** that service starts
- **THEN** it starts successfully and performs its function
- **AND** it holds no Linux capability, and no filesystem write access, beyond what
  its function requires

#### Scenario: A privilege-escalation attempt is blocked
- **GIVEN** any service in the deployment stack
- **WHEN** a process inside that service's container attempts to gain more
  privilege than the container started with
- **THEN** the attempt is blocked

### Requirement: Cached Go Builds On Repeated Deploys
The platform's container image build process SHALL reuse the Go module download
cache and the Go build cache across separate builds on the same host, so that an
unchanged dependency set is not re-downloaded and an unchanged package is not
recompiled.

#### Scenario: A rebuild with no dependency change skips re-downloading modules
- **GIVEN** a container image was already built once on a host, and the module cache
  from that build is still present on the host
- **WHEN** the image is built again on the same host with `go.mod` and `go.sum`
  unchanged
- **THEN** the build does not re-download any Go module already present in the
  cache

#### Scenario: A rebuild with only one changed source file skips recompiling unchanged packages
- **GIVEN** a container image was already built once on a host, and the Go build
  cache from that build is still present on the host
- **WHEN** the image is built again on the same host with only one source file
  changed
- **THEN** the build does not recompile a package whose own source is unchanged
  since the cached build

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

### Requirement: Nightly Cycle Reference Diagrams

The platform SHALL provide diagrams of the nightly cycle's steps, its
cross-module call chain, and its analytics derivation, stored under
`kkpa/docs/diagrams/nightly-job/`, and SHALL reference every one of them from
the nightly-cycle knowledge-base guide
(`kkpa/context/architecture/nightly-cycle.md`), so a reader can find and open
each diagram from the guide without searching the repository.

#### Scenario: A reader finds the workflow diagram from the guide

- **GIVEN** the nightly-cycle knowledge-base guide
- **WHEN** a reader opens its "Rendered view" section
- **THEN** it links to a local diagram file showing the cycle's four steps, in
  order, including the step-1 failure short-circuit and the step-4
  first-of-the-month gate

#### Scenario: A reader finds the sequence diagram from the guide

- **GIVEN** the nightly-cycle knowledge-base guide
- **WHEN** a reader opens its "Rendered view" section
- **THEN** it links to a local diagram file showing the cross-module call
  chain, with every call marked as either a paid Fleet API call or a database
  read or write

#### Scenario: A reader finds the derivation diagram from the guide

- **GIVEN** the nightly-cycle knowledge-base guide
- **WHEN** a reader opens its "Rendered view" section
- **THEN** it links to a local diagram file showing what the analytics
  recalculation step derives for one day's `vehicle_metrics` row, including
  the charge-corrected consumed percentage and the two figures derived from
  it

#### Scenario: The guide states a precedence order for every rendering it links

- **GIVEN** the nightly-cycle knowledge-base guide's "Rendered view" section
- **WHEN** a reader compares a rendering it links against the code
- **THEN** the section states that the code is authoritative first, the guide
  second, and any rendering — local diagram or published artifact — last

### Requirement: Named And Located Deploy Logs
The platform's deployment stack SHALL make the web gateway's, the poller's,
and the reverse proxy's log output available as a named file at a fixed,
predictable location, rather than requiring an operator to discover a
per-container internal storage path before reading a service's log. A named
log file SHALL be readable by the host user who operates the deployment,
without elevated privileges — a file the operator cannot open does not satisfy
this requirement merely by existing under the right name. Where a service's
container runs as a different user than the one owning the named-log location,
the stack SHALL configure both the location's access and the file's mode so
that the service can write it and the host user can read it. The database
service and the one-shot migration step are exempt from this requirement and
MAY continue to expose their log output only through the container runtime's
own log inspection command.

#### Scenario: An operator finds the web gateway's log by name
- **GIVEN** the deployment stack is running
- **WHEN** an operator looks for the web gateway's log output
- **THEN** it is available at a fixed, named location on the host
- **AND** the operator does not need to look up any per-container internal
  identifier first

#### Scenario: An operator finds the poller's log by name
- **GIVEN** the deployment stack is running
- **WHEN** an operator looks for the poller's log output
- **THEN** it is available at a fixed, named location on the host
- **AND** the operator does not need to look up any per-container internal
  identifier first

#### Scenario: An operator finds the reverse proxy's log by name
- **GIVEN** the deployment stack is running
- **WHEN** an operator looks for the reverse proxy's log output
- **THEN** it is available at a fixed, named location on the host
- **AND** the operator does not need to look up any per-container internal
  identifier first

#### Scenario: An operator reads a named log without elevated privileges
- **GIVEN** a service writes its log to the named location
- **WHEN** the host user who operates the deployment reads that file
- **THEN** it is readable without `sudo` or any other privilege escalation

#### Scenario: A service running as a different user still produces a readable file
- **GIVEN** a service whose container runs as a different user than the one
  owning the named-log location
- **WHEN** it writes its named log file
- **THEN** the location grants that service write access
- **AND** the file is created with a mode the host user can read

#### Scenario: The database and migration services keep their existing log access
- **GIVEN** the deployment stack is running
- **WHEN** an operator looks for the database service's or the migration
  step's log output
- **THEN** it remains available through the container runtime's own log
  inspection command
- **AND** this requirement does not obligate a named file for either service

### Requirement: Time-Bounded Named Log Retention
The platform SHALL purge named deploy log data automatically once it exceeds
a bounded retention period, so that named log files do not accumulate
without limit on the host.

#### Scenario: A named log file older than the retention period is purged
- **GIVEN** a named deploy log has accumulated data older than the
  configured retention period
- **WHEN** the retention mechanism next runs
- **THEN** the data older than the retention period is removed
- **AND** data within the retention period is preserved

#### Scenario: A named log file growing quickly is bounded before its next scheduled check
- **GIVEN** a named deploy log is growing continuously, for example during a
  noisy or repeating failure
- **WHEN** that log's data reaches the configured size threshold before its
  next scheduled retention check
- **THEN** the retention mechanism rotates it immediately rather than
  waiting for the next scheduled check

#### Scenario: A self-rotating log is not also purged by the host retention mechanism
- **GIVEN** a named deploy log that performs its own rotation and retention
  internally
- **WHEN** the host-level retention mechanism runs
- **THEN** it does not act on that log file
- **AND** exactly one rotation mechanism governs that file at any time

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

