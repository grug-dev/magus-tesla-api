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

### Requirement: Per-Request Account Activation Gate
The gateway SHALL check a signed-in visitor's account activation status on every request to a
non-exempt route, so that an account deactivated after a session was established stops being able
to use that session starting with its very next request — not merely on its next login. A request
to an exempt route (the sign-in and sign-out entry points, the OAuth login/callback routes, the
health check, static assets, and the blocked-account page itself) SHALL NOT be checked. A request
carrying no signed-in session SHALL NOT be checked. When the check finds the account is not
Active, the gateway SHALL end that session and send the visitor to the blocked-account page — as a
standard redirect for an ordinary page load, and as a client-side navigation instruction (not an
ordinary redirect) for an htmx-issued request, so that the blocked page is never partially injected
into a smaller region of an already-loaded page. When the check itself cannot be completed due to
a transient failure, the gateway SHALL allow the request to proceed rather than block every
signed-in visitor's traffic on that failure.

#### Scenario: A session is cut off on the next request after deactivation
- **GIVEN** a visitor with an established, currently-working session
- **WHEN** their account's status changes to Inactive and they then make any further request to a
  non-exempt route
- **THEN** that request is not allowed to proceed to its normal handler
- **AND** their session is ended
- **AND** they are sent to the blocked-account page

#### Scenario: An Active account's session is unaffected
- **GIVEN** a visitor with an established session whose account remains Active
- **WHEN** they make a request to a non-exempt route
- **THEN** the request proceeds normally

#### Scenario: An anonymous request is never checked
- **GIVEN** a request carrying no signed-in session
- **WHEN** it is made to any route, exempt or not
- **THEN** the gateway performs no account-status check for that request
- **AND** any route-specific sign-in requirement is unaffected by this behavior

#### Scenario: Exempt routes are never checked, even for a deactivated session
- **GIVEN** a visitor whose account has just become Inactive, still holding their existing session
  cookie
- **WHEN** they request the sign-in page, the sign-out action, the OAuth login or callback routes,
  the health check, a static asset, or the blocked-account page itself
- **THEN** none of those requests are blocked by the account-status check

#### Scenario: A deactivated session hitting an ordinary page load is redirected
- **GIVEN** a visitor with a since-deactivated session making an ordinary (non-fragment) page
  request to a non-exempt route
- **WHEN** the account-status check finds the account is not Active
- **THEN** the visitor's browser is redirected to the blocked-account page as a normal navigation

#### Scenario: A deactivated session hitting an htmx fragment request is redirected without corrupting the page
- **GIVEN** a visitor with a since-deactivated session making an htmx-issued request that targets
  a small region of an already-loaded page
- **WHEN** the account-status check finds the account is not Active
- **THEN** the visitor's browser is navigated to the blocked-account page as a full page load
- **AND** the blocked page's content is never swapped into the smaller region the original request
  targeted

#### Scenario: A transient failure of the check does not block the visitor
- **GIVEN** a visitor with an established, Active session
- **WHEN** the account-status check cannot be completed due to a transient failure (not "account
  not found" and not "account is Inactive")
- **THEN** the request is allowed to proceed rather than being blocked

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
