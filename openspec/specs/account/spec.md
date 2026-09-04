# account Specification

## Purpose

The `account` capability is the platform's multi-tenant identity boundary. It provisions app
users from a social OAuth identity (no passwords), stores each user's Tesla connection tokens
(at most one per user), and owns refreshing/rotating those tokens. Other modules obtain a
user's current Tesla credentials only through its public interface — never by reading its
tables — so the module boundary stays real.
## Requirements
### Requirement: Social OAuth Account Provisioning
The system SHALL create or resolve a user account from a social OAuth identity, storing the
account's email, provider, provider id, and display name. The system SHALL NOT store passwords.

#### Scenario: First login creates an account
- **GIVEN** a person authenticating via a supported social provider for the first time
- **WHEN** the OAuth callback returns their verified identity
- **THEN** a new account is created keyed by (provider, provider_id)
- **AND** their email and display name are stored

#### Scenario: Returning login resolves the same account
- **GIVEN** an existing account for a (provider, provider_id)
- **WHEN** the same identity authenticates again
- **THEN** the existing account is returned rather than a duplicate created

### Requirement: Unique Account Identity
The system SHALL guarantee at most one account per (provider, provider_id) pair.

#### Scenario: Duplicate identity resolves to the existing account
- **GIVEN** an account already exists for a (provider, provider_id)
- **WHEN** account creation is attempted again for the same pair
- **THEN** the operation resolves to the existing account
- **AND** no duplicate account is created

### Requirement: Per-Account Tesla Token Storage
The system SHALL store an account's Tesla connection tokens — access token, refresh token, and
access-token expiry — in a store owned by the account module, keeping at most one Tesla connection
per account. Completing the Tesla OAuth connect flow again SHALL replace the account's stored
tokens in place rather than create an additional connection.

#### Scenario: Storing a new Tesla connection
- **GIVEN** an authenticated account with no Tesla connection
- **WHEN** the account completes the Tesla OAuth connect flow
- **THEN** the account's Tesla access token, refresh token, and access expiry are persisted, linked to the account

#### Scenario: Reconnecting replaces the stored connection
- **GIVEN** an account that already has a stored Tesla connection
- **WHEN** the account completes the Tesla OAuth connect flow again
- **THEN** the account's single stored connection is updated in place with the new tokens and expiry
- **AND** no additional Tesla connection row is created for the account

### Requirement: Credential Access Through the Module Interface
The account module SHALL expose a public interface for other modules to obtain an account's
current Tesla credentials, and other modules SHALL NOT read the account module's tables directly.

#### Scenario: A caller obtains credentials to call the Tesla adapter
- **GIVEN** a request scoped to a specific account
- **WHEN** a caller needs that account's Tesla credentials
- **THEN** the account module returns the credentials through its interface
- **AND** the caller passes them to the tesla adapter without accessing the account database

### Requirement: Tesla Token Refresh Ownership
The account module SHALL own refreshing and rotating each account's Tesla tokens and SHALL
persist the rotated pair.

#### Scenario: Expired access token is refreshed on request
- **GIVEN** a stored Tesla connection whose access token has expired
- **WHEN** that account's Tesla credentials are requested
- **THEN** the account module uses the refresh token to obtain a new access token
- **AND** persists the rotated access and refresh tokens before returning the credentials

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

### Requirement: Per-Account Language Preference
The account module SHALL persist a language preference on each account, defaulting to `es` for
every account. The supported vocabulary SHALL be exactly two codes: `es` and `en`. The account
module SHALL expose a public port to read an account's current language and a public port to
persist a language change, and other modules SHALL NOT read or write the preference by any other
means.

Reading the preference SHALL always return one of the two supported codes and SHALL NOT fail
because a stored value is outside that set: any such value (a legacy row, a value written outside
this module's write path) SHALL be treated as `es`. Persisting a change SHALL reject any value
outside the two supported codes and SHALL leave the previously stored value unchanged when
rejected.

#### Scenario: A newly created account defaults to Spanish
- **GIVEN** a new account is created via the social OAuth provisioning flow
- **WHEN** the account module is asked for that account's language preference
- **THEN** it returns `es`
- **AND** no explicit language-setting call was made for this account

#### Scenario: Reading the language preference after it has been set
- **GIVEN** an account whose language preference was previously set to `en`
- **WHEN** the account module is asked for that account's language preference
- **THEN** it returns `en`

#### Scenario: Setting the language preference to a supported code
- **GIVEN** an account with its language preference at the default `es`
- **WHEN** the account module is asked to set that account's language preference to `en`
- **THEN** the change is persisted
- **AND** subsequently reading that account's language preference returns `en`

#### Scenario: Setting the language preference to an unsupported code is rejected
- **GIVEN** an account with its language preference at `es`
- **WHEN** the account module is asked to set that account's language preference to a code outside
  `{es, en}` (e.g. `fr`)
- **THEN** the request is rejected and nothing is persisted
- **AND** subsequently reading that account's language preference still returns `es`

#### Scenario: An unrecognized stored value never breaks a read
- **GIVEN** an account whose stored language value is not one of `{es, en}` (e.g. a legacy value
  written before this vocabulary was fixed, or a value written outside the account module's write
  path)
- **WHEN** the account module is asked for that account's language preference
- **THEN** it returns `es`
- **AND** no error is raised

#### Scenario: A consumer cannot persist a language change by writing the accounts table directly
- **GIVEN** the gateway module needs to change a signed-in user's language preference
- **WHEN** it processes the user's language-switch action
- **THEN** it persists the change by calling the account module's public interface
- **AND** it does not write to the `accounts` table directly

### Requirement: Module-Scoped Database Schema
The account module's `accounts` and `tesla_tokens` tables SHALL live in a PostgreSQL schema named
`account`, distinct from the `public` schema and from every other module's schema. This SHALL be a
namespacing change only: it SHALL NOT alter any stored data, any constraint (primary key, foreign
key, unique, or check), any index, or any behavior of the module's public interface. No other
module SHALL be granted access to the `account` schema's tables — the module boundary
(`ai/architecture.md` §2, "no cross-module database leaks") is enforced identically before and
after this requirement, now additionally checkable at the database catalog level.

#### Scenario: The accounts and tesla_tokens tables resolve under the account schema
- **GIVEN** the account module's migrations have been applied
- **WHEN** the database catalog is queried for `account.accounts` and `account.tesla_tokens`
- **THEN** both resolve to their table (a non-null relation)
- **AND** neither `public.accounts` nor `public.tesla_tokens` resolves to a relation any longer

#### Scenario: Existing data, constraints, and indexes survive the schema move
- **GIVEN** accounts and Tesla connections that existed before the schema move
- **WHEN** the schema-move migration is applied
- **THEN** every account and Tesla connection row is preserved unchanged
- **AND** every primary key, foreign key, unique constraint, and check constraint on both tables
  continues to be enforced exactly as before

#### Scenario: The module's public interface is unaffected by the schema move
- **GIVEN** a caller of the account module's public interface (e.g. `AccessTokenFor`,
  `SaveTeslaTokens`, `UpsertFromOAuth`)
- **WHEN** the schema move is applied
- **THEN** every exported type name, method name, and method signature is unchanged
- **AND** no caller needs to change to keep working

### Requirement: Per-Account Theme Preference
The account module SHALL persist a UI theme preference on each account, defaulting to `graphite`
for every account. The supported vocabulary SHALL be exactly three codes: `apex`, `graphite`, and
`halloween`. The account module SHALL expose a public port to read an account's current theme and
a public port to persist a theme change, and other modules SHALL NOT read or write the preference
by any other means.

Reading the preference SHALL always return one of the three supported codes and SHALL NOT fail
because a stored value is outside that set: any such value (a legacy row, a value written outside
this module's write path) SHALL be treated as `graphite`. Persisting a change SHALL reject any
value outside the three supported codes and SHALL leave the previously stored value unchanged when
rejected.

#### Scenario: A newly created account defaults to the Graphite theme
- **GIVEN** a new account is created via the social OAuth provisioning flow
- **WHEN** the account module is asked for that account's theme preference
- **THEN** it returns `graphite`
- **AND** no explicit theme-setting call was made for this account

#### Scenario: Reading the theme preference after it has been set
- **GIVEN** an account whose theme preference was previously set to `apex`
- **WHEN** the account module is asked for that account's theme preference
- **THEN** it returns `apex`

#### Scenario: Setting the theme preference to a supported code
- **GIVEN** an account with its theme preference at the default `graphite`
- **WHEN** the account module is asked to set that account's theme preference to `halloween`
- **THEN** the change is persisted
- **AND** subsequently reading that account's theme preference returns `halloween`

#### Scenario: Setting the theme preference to an unsupported code is rejected
- **GIVEN** an account with its theme preference at `graphite`
- **WHEN** the account module is asked to set that account's theme preference to a code outside
  `{apex, graphite, halloween}` (e.g. `cyberpunk`)
- **THEN** the request is rejected and nothing is persisted
- **AND** subsequently reading that account's theme preference still returns `graphite`

#### Scenario: An unrecognized stored theme value never breaks a read
- **GIVEN** an account whose stored theme value is not one of `{apex, graphite, halloween}` (e.g.
  a value written outside the account module's write path)
- **WHEN** the account module is asked for that account's theme preference
- **THEN** it returns `graphite`
- **AND** no error is raised

#### Scenario: A consumer cannot persist a theme change by writing the settings table directly
- **GIVEN** the gateway module needs to change a signed-in user's theme preference
- **WHEN** it processes the user's theme-switch action
- **THEN** it persists the change by calling the account module's public interface
- **AND** it does not write to the account module's settings table directly

### Requirement: Settings Row Guaranteed At Account Creation
The account module SHALL create exactly one settings row for an account at the moment the account
itself is created, in the same atomic operation as the account's creation. Every account that
existed before this requirement was introduced SHALL be backfilled with exactly one settings row.
Consequently, reading either preference for any existing account SHALL never require the account
module to distinguish a "no settings row yet" case from a "preference at its default" case — both
are the same state.

The account module SHALL expose a public port that returns an account's language AND theme
preference together, in a single underlying lookup, so a caller that needs both values for one
render never pays for two separate lookups.

#### Scenario: A newly provisioned account has a settings row immediately
- **GIVEN** a brand-new social OAuth identity with no prior account
- **WHEN** the account module provisions the account for that identity
- **THEN** a settings row exists for the new account immediately afterward
- **AND** that settings row holds the default language and the default theme

#### Scenario: An account created before this requirement existed still has a settings row
- **GIVEN** an account that was created before per-account settings existed
- **WHEN** the account module is asked for that account's language or theme preference
- **THEN** it returns a value (the account's previously stored language, and the default theme)
- **AND** it does not report a missing-settings error

#### Scenario: Both preferences are readable together in one call
- **GIVEN** an account with a non-default language and a non-default theme
- **WHEN** a caller asks the account module for that account's full set of preferences
- **THEN** it receives both the language and the theme from that single call
- **AND** it does not need to make a second call to obtain either value

