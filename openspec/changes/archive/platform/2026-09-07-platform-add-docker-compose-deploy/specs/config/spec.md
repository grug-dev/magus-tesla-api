## ADDED Requirements

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
