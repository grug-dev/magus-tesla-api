## ADDED Requirements

### Requirement: Account Activation Status
Every account SHALL carry an explicit status of exactly `Active` or `Inactive`. A newly
provisioned account SHALL default to `Inactive`. Existing accounts are NOT retroactively changed
to `Active` when this status is introduced — an account created before this requirement existed
becomes `Inactive` the moment the status column exists, exactly as if it had always been
`Inactive`. Provisioning an account from a social OAuth identity SHALL succeed and return the
account's current status regardless of that status; it SHALL NOT be blocked, filtered, or altered
by this requirement. Every other account read (a language-preference lookup or write, a lookup by
provider identity) SHALL behave as though an `Inactive` account does not exist.

#### Scenario: A newly provisioned account starts Inactive
- **GIVEN** a person authenticating via a supported social provider for the first time
- **WHEN** the OAuth callback provisions their account
- **THEN** the account is created with status `Inactive`
- **AND** provisioning succeeds and returns the account, including its `Inactive` status

#### Scenario: An account predating this requirement is Inactive once it applies
- **GIVEN** an account that existed before account status was introduced
- **WHEN** account status is introduced into the system
- **THEN** that account's status is `Inactive`
- **AND** no automatic process changes it to `Active`

#### Scenario: Provisioning is unaffected by an account's status
- **GIVEN** an existing account whose status is `Inactive`
- **WHEN** the same social identity authenticates again
- **THEN** provisioning resolves to the existing account and returns it, including its `Inactive`
  status
- **AND** the resolution is not blocked or altered because the account is `Inactive`

#### Scenario: An Inactive account is invisible to every other account read
- **GIVEN** an account whose status is `Inactive`
- **WHEN** any account read other than provisioning is attempted for that account (a language
  preference lookup, a language preference change, or a lookup by provider identity)
- **THEN** the read behaves as though no such account exists

#### Scenario: An Active account is visible to every account read
- **GIVEN** an account whose status is `Active`
- **WHEN** an account read is attempted for that account
- **THEN** the read returns the account's data normally

## MODIFIED Requirements

### Requirement: Social OAuth Account Provisioning
The system SHALL create or resolve a user account from a social OAuth identity, storing the
account's email, provider, provider id, and display name. The system SHALL NOT store passwords.
Provisioning SHALL succeed and return the account's current activation status regardless of that
status — provisioning is the one account operation that is never filtered by status.

#### Scenario: First login creates an account
- **GIVEN** a person authenticating via a supported social provider for the first time
- **WHEN** the OAuth callback returns their verified identity
- **THEN** a new account is created keyed by (provider, provider_id)
- **AND** their email and display name are stored
- **AND** the returned account's status is `Inactive`

#### Scenario: Returning login resolves the same account
- **GIVEN** an existing account for a (provider, provider_id)
- **WHEN** the same identity authenticates again
- **THEN** the existing account is returned rather than a duplicate created
- **AND** the returned account carries its current status, whatever it is

### Requirement: Per-Account Language Preference
The account module SHALL persist a language preference on each account, defaulting to `es` for
every account. The supported vocabulary SHALL be exactly two codes: `es` and `en`. The account
module SHALL expose a public port to read an account's current language and a public port to
persist a language change, and other modules SHALL NOT read or write the preference by any other
means. Both the read and the write port SHALL act only on accounts whose status is `Active`: an
`Inactive` account's language preference SHALL NOT be readable, and an attempt to change it SHALL
have no effect.

Reading the preference SHALL always return one of the two supported codes and SHALL NOT fail
because a stored value is outside that set: any such value (a legacy row, a value written outside
this module's write path) SHALL be treated as `es`. Persisting a change SHALL reject any value
outside the two supported codes and SHALL leave the previously stored value unchanged when
rejected.

#### Scenario: A newly created account defaults to Spanish
- **GIVEN** a new, active account
- **WHEN** the account module is asked for that account's language preference
- **THEN** it returns `es`
- **AND** no explicit language-setting call was made for this account

#### Scenario: Reading the language preference after it has been set
- **GIVEN** an active account whose language preference was previously set to `en`
- **WHEN** the account module is asked for that account's language preference
- **THEN** it returns `en`

#### Scenario: Setting the language preference to a supported code
- **GIVEN** an active account with its language preference at the default `es`
- **WHEN** the account module is asked to set that account's language preference to `en`
- **THEN** the change is persisted
- **AND** subsequently reading that account's language preference returns `en`

#### Scenario: Setting the language preference to an unsupported code is rejected
- **GIVEN** an active account with its language preference at `es`
- **WHEN** the account module is asked to set that account's language preference to a code outside
  `{es, en}` (e.g. `fr`)
- **THEN** the request is rejected and nothing is persisted
- **AND** subsequently reading that account's language preference still returns `es`

#### Scenario: An unrecognized stored value never breaks a read
- **GIVEN** an active account whose stored language value is not one of `{es, en}` (e.g. a legacy
  value written before this vocabulary was fixed, or a value written outside the account module's
  write path)
- **WHEN** the account module is asked for that account's language preference
- **THEN** it returns `es`
- **AND** no error is raised

#### Scenario: A consumer cannot persist a language change by writing the accounts table directly
- **GIVEN** the gateway module needs to change a signed-in user's language preference
- **WHEN** it processes the user's language-switch action
- **THEN** it persists the change by calling the account module's public interface
- **AND** it does not write to the `accounts` table directly

#### Scenario: Reading the language preference of an inactive account fails
- **GIVEN** an account whose status is `Inactive`
- **WHEN** the account module is asked for that account's language preference
- **THEN** the read fails as though no such account exists

#### Scenario: Setting the language preference of an inactive account has no effect
- **GIVEN** an account whose status is `Inactive`
- **WHEN** the account module is asked to set that account's language preference to a supported
  code
- **THEN** the operation does not report an error
- **AND** the account's stored language value is unchanged

### Requirement: All-Accounts Registered Vehicle Enumeration
The account module SHALL expose, through its public interface, a read that returns every
**active** registered vehicle across ALL accounts, each tagged with its owning account id. For
each vehicle the result SHALL carry the owning `accountID`, the Tesla Fleet API vehicle id
(`tesla_id`), the `vin`, and the `display_name`. This enumeration is for server-side background
collection jobs (e.g. nightly telemetry) that must list every active vehicle across every account
without reading the account module's tables. The result SHALL be empty (not an error) when no
active vehicle is registered under any account. An `Inactive` vehicle SHALL NOT appear in this
enumeration regardless of its owning account's status. The enumeration SHALL return active
vehicles regardless of whether their owning account currently has a usable Tesla connection —
deciding whether a token is available and handling expired or revoked tokens is the caller's
responsibility (via the existing credential-access interface), not this enumeration's.

#### Scenario: Enumerating vehicles across multiple accounts
- **GIVEN** account A with one active registered vehicle and account B with two active registered
  vehicles
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
- **GIVEN** exactly one account with two active registered vehicles
- **WHEN** the account module is asked for all registered vehicles across all accounts
- **THEN** it returns both vehicles
- **AND** both are tagged with that account's id

#### Scenario: Vehicles are returned even when the owning account has no usable Tesla connection
- **GIVEN** an active account with a registered vehicle whose Tesla connection is missing,
  expired, or revoked
- **WHEN** the account module is asked for all registered vehicles across all accounts
- **THEN** that vehicle is still included in the result, tagged with its owning account id
- **AND** the enumeration does not attempt to obtain or validate a Tesla access token

#### Scenario: A consumer cannot read the registry table directly
- **GIVEN** a background collection job outside `internal/account` that needs every active
  registered vehicle across all accounts
- **WHEN** it needs that list
- **THEN** it obtains it by calling the account module's public interface
- **AND** it does not query the account module's registry table directly

#### Scenario: An inactive vehicle is excluded from the enumeration
- **GIVEN** an account with one active registered vehicle and one inactive registered vehicle
- **WHEN** the account module is asked for all registered vehicles across all accounts
- **THEN** only the active vehicle is returned
