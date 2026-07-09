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
