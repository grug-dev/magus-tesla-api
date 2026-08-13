## ADDED Requirements

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
