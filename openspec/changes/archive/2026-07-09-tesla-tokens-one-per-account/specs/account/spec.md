## MODIFIED Requirements

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
