## ADDED Requirements

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
