## ADDED Requirements

### Requirement: Poller Time Zone Default
The platform's configuration loader SHALL resolve `POLLER_TIMEZONE` to the platform's default
time zone when the environment variable is unset (empty), rather than to the host process's
own local zone. When `POLLER_TIMEZONE` is set to a non-empty value, the loader SHALL pass that
value through unchanged, without validating it.

#### Scenario: An unset POLLER_TIMEZONE resolves to the platform default
- **GIVEN** the `POLLER_TIMEZONE` environment variable is unset
- **WHEN** the configuration is loaded
- **THEN** the resolved poller time zone is the platform's default zone, `America/Bogota`

#### Scenario: A set, valid POLLER_TIMEZONE passes through unchanged
- **GIVEN** the `POLLER_TIMEZONE` environment variable is set to a valid IANA zone name (for
  example, `America/New_York`)
- **WHEN** the configuration is loaded
- **THEN** the resolved poller time zone is that same value, unmodified

#### Scenario: A set, invalid POLLER_TIMEZONE passes through unchanged
- **GIVEN** the `POLLER_TIMEZONE` environment variable is set to a value that is not a valid
  IANA zone name
- **WHEN** the configuration is loaded
- **THEN** the resolved poller time zone is that same value, unmodified
- **AND** no validation error is raised by the configuration loader itself — rejection of an
  unresolvable zone name is the responsibility of the process that consumes the value at
  startup
