## ADDED Requirements

### Requirement: Inactive Account Is Blocked At Login
The gateway SHALL refuse to establish an authenticated session for an account whose activation
status is not Active. When the Google OAuth callback resolves an account whose status is not
Active, the gateway SHALL respond with a dedicated page at HTTP 403 stating that the account is
deactivated and naming a contact address for reactivation, and SHALL NOT set any session identity
value. The contact address SHALL be a fixed, hardcoded value in the translation catalogue, not a
configuration value. The blocked page SHALL be presented in both supported languages.

#### Scenario: An Inactive account is refused a session at login
- **GIVEN** a visitor whose Google identity resolves to an account with status Inactive
- **WHEN** the OAuth callback completes
- **THEN** the gateway responds with HTTP 403 and a page stating the account is deactivated and
  naming the contact address
- **AND** no session identity is set for that request or any request using its cookie

#### Scenario: An Active account is signed in normally
- **GIVEN** a visitor whose Google identity resolves to an account with status Active
- **WHEN** the OAuth callback completes
- **THEN** the gateway establishes an authenticated session for that account
- **AND** the visitor is sent to the dashboard

#### Scenario: The blocked page is available in both supported languages
- **GIVEN** the blocked page rendered for an Inactive account
- **WHEN** it is rendered in either supported language
- **THEN** its title, message, and contact address text are all present and non-empty in that
  language

## MODIFIED Requirements

### Requirement: Google Sign-In

The gateway SHALL let a visitor sign in with Google. On a successful callback it SHALL provision
or resolve the account for the Google identity (through the account module) and, **only when that
account's activation status is Active**, establish an authenticated session for that account. When
the callback request carries an explicitly-present `lang` cookie and the account is Active, the
gateway SHALL persist that language to the resolved account via `account.Service.SetLanguage` so a
language chosen before authentication carries into the signed-in session. An account whose status
is not Active is handled per the "Inactive Account Is Blocked At Login" requirement instead of
being signed in.

#### Scenario: Starting the login flow

- **GIVEN** an anonymous visitor
- **WHEN** they begin Google login
- **THEN** the gateway redirects them to Google's consent screen with a state parameter

#### Scenario: Successful callback provisions the account and signs in an Active account

- **GIVEN** a visitor returning from Google with a valid authorization code and matching state,
  whose resolved account has status Active
- **WHEN** the gateway handles the callback
- **THEN** it resolves the Google identity (id, email, name)
- **AND** provisions or resolves the account via the account module
- **AND** establishes an authenticated session for that account

#### Scenario: A pre-login language cookie carries into the account, only when the account is Active

- **GIVEN** a visitor who selected `en` on the login page (setting a `lang=en` cookie) before
  authenticating, whose resolved account has status Active
- **WHEN** the Google callback establishes their session
- **THEN** the gateway calls `account.Service.SetLanguage` with `"en"` for the resolved account

#### Scenario: A callback with no language cookie leaves the account's language untouched

- **GIVEN** a visitor whose callback request carries no `lang` cookie
- **WHEN** the Google callback establishes their session
- **THEN** the gateway does NOT call `account.Service.SetLanguage`

#### Scenario: A callback resolving an Inactive account never reaches the language sync or session steps

- **GIVEN** a visitor returning from Google with a valid authorization code and matching state,
  whose resolved account has status Inactive
- **WHEN** the gateway handles the callback
- **THEN** it does not call `account.Service.SetLanguage`
- **AND** it does not establish a session
