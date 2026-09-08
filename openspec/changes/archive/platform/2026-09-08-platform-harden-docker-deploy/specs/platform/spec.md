## ADDED Requirements

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
