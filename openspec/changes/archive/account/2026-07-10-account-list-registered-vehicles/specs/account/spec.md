## ADDED Requirements

### Requirement: All-Accounts Registered Vehicle Enumeration
The account module SHALL expose, through its public interface, a read that returns every registered
vehicle across ALL accounts, each tagged with its owning account id. For each vehicle the result
SHALL carry the owning `accountID`, the Tesla Fleet API vehicle id (`tesla_id`), the `vin`, and the
`display_name`. This enumeration is for server-side background collection jobs (e.g. nightly
telemetry) that must list every vehicle across every account without reading the account module's
tables. The result SHALL be empty (not an error) when no vehicle is registered under any account.
The enumeration SHALL return vehicles regardless of whether their owning account currently has a
usable Tesla connection — deciding whether a token is available and handling expired or revoked
tokens is the caller's responsibility (via the existing credential-access interface), not this
enumeration's.

#### Scenario: Enumerating vehicles across multiple accounts
- **GIVEN** account A with one registered vehicle and account B with two registered vehicles
- **WHEN** the account module is asked for all registered vehicles across all accounts
- **THEN** it returns three vehicles in total
- **AND** each vehicle carries the account id of the account it belongs to (the one from A tagged
  with A's id, the two from B tagged with B's id)
- **AND** each vehicle carries its `tesla_id`, `vin`, and `display_name`

#### Scenario: Enumerating when no vehicles are registered anywhere
- **GIVEN** a system in which no account has any vehicle registered
- **WHEN** the account module is asked for all registered vehicles across all accounts
- **THEN** it returns an empty result (not an error)

#### Scenario: A single account's vehicles are all returned with its account id
- **GIVEN** exactly one account with two registered vehicles
- **WHEN** the account module is asked for all registered vehicles across all accounts
- **THEN** it returns both vehicles
- **AND** both are tagged with that account's id

#### Scenario: Vehicles are returned even when the owning account has no usable Tesla connection
- **GIVEN** an account with a registered vehicle whose Tesla connection is missing, expired, or
  revoked
- **WHEN** the account module is asked for all registered vehicles across all accounts
- **THEN** that vehicle is still included in the result, tagged with its owning account id
- **AND** the enumeration does not attempt to obtain or validate a Tesla access token

#### Scenario: A consumer cannot read the registry table directly
- **GIVEN** a background collection job outside `internal/account` that needs every registered
  vehicle across all accounts
- **WHEN** it needs that list
- **THEN** it obtains it by calling the account module's public interface
- **AND** it does not query the account module's registry table directly
