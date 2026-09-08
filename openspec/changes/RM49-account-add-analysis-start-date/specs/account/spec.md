## ADDED Requirements

### Requirement: Per-Account Analysis Start Date
The account module SHALL persist an analysis start date on each account: the first calendar
day, in the platform's default time zone, that the platform analyzes that account's vehicle
data. The value SHALL be set exactly once, at the moment the account is created, from the
account's creation date. The account module SHALL expose a public port to read an account's
current analysis start date, and other modules SHALL NOT read this value by any other means.

The account module SHALL NOT expose a public port to change this value once set. There is no
requirement in this capability for the value to ever change after account creation.

Reading the value SHALL always return the exact date that was set at account creation and
SHALL NOT require the caller to distinguish "no value yet" from "value at its default" — every
account SHALL have this value from the moment it exists.

#### Scenario: A newly created account's analysis start date is its creation date
- **GIVEN** a new account is created via the social OAuth provisioning flow
- **WHEN** the account module is asked for that account's analysis start date
- **THEN** it returns the calendar day, in the platform's default time zone, on which the
  account was created
- **AND** no explicit call to set this value was made for this account

#### Scenario: An account created before this requirement existed still has an analysis start date
- **GIVEN** an account that was created before this capability existed
- **WHEN** the account module is asked for that account's analysis start date
- **THEN** it returns the calendar day, in the platform's default time zone, on which that
  account was originally created
- **AND** it does not report a missing-value error

#### Scenario: A consumer cannot change the analysis start date through this module's public interface
- **GIVEN** any consumer of the account module (for example, the gateway)
- **WHEN** it needs the effect of a different analysis start date for an account
- **THEN** the account module offers no public operation to change the stored value
- **AND** the value can only be changed by a means outside this capability's public interface

#### Scenario: The analysis start date is available together with an account's other preferences
- **GIVEN** an account with a non-default language, a non-default theme, and its analysis start
  date
- **WHEN** a caller asks the account module for that account's full set of preferences in a
  single call
- **THEN** it receives the language, the theme, and the analysis start date together
- **AND** it does not need to make a second call to obtain the analysis start date
