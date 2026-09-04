## ADDED Requirements

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
