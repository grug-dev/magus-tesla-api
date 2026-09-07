# config Specification

## Purpose
Load and type the platform's process configuration from the environment, and supply the
defaults for anything unset. Notably it owns the `POLLER_TIMEZONE` default, which resolves to
the platform's default zone from `internal/clock` rather than the host's local zone.
## Requirements
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

### Requirement: Missing .env File Is Not Fatal
The configuration loader SHALL NOT fail when no `.env` file is present in the process's
working directory. When `.env` is absent, the loader SHALL proceed using whatever
values are already present in the process environment. When `.env` is present, the
loader SHALL still load it, and any variable it sets SHALL NOT override a
same-named variable already present in the process environment.

#### Scenario: A missing .env file does not fail configuration loading
- **GIVEN** no `.env` file exists in the process's working directory
- **AND** the required configuration values are present as real environment variables
- **WHEN** the configuration is loaded
- **THEN** loading succeeds
- **AND** the resulting configuration reflects the real environment variables

#### Scenario: A missing .env file and missing required values still fails
- **GIVEN** no `.env` file exists in the process's working directory
- **AND** a required configuration value (the Tesla client id or client secret) is also
  absent from the process environment
- **WHEN** the configuration is loaded
- **THEN** loading fails with the existing "must be set" error
- **AND** the failure is attributed to the missing required value, not to the missing
  `.env` file

#### Scenario: A present .env file still loads
- **GIVEN** a `.env` file exists in the process's working directory with a value set
  for a given configuration key
- **AND** that key is not otherwise set in the process environment
- **WHEN** the configuration is loaded
- **THEN** the resulting configuration reflects the value from `.env`

#### Scenario: A real environment variable always wins over a .env value
- **GIVEN** a `.env` file exists in the process's working directory with a value set
  for a given configuration key
- **AND** a real process environment variable is also set for that same key, to a
  different value
- **WHEN** the configuration is loaded
- **THEN** the resulting configuration reflects the real environment variable's value,
  not the `.env` file's value

#### Scenario: A .env read error other than "file not found" still fails
- **GIVEN** a `.env` file exists in the process's working directory but cannot be read
  or parsed for a reason other than not existing (for example, a permission error or a
  malformed file)
- **WHEN** the configuration is loaded
- **THEN** loading fails
- **AND** the failure reports the underlying read/parse error

