# gateway Specification

## Purpose

The `gateway` capability is the web presentation layer — the single place HTML lives
(`ai/architecture.md` §2). It serves HTML pages and htmx fragments over HTTP, runs cookie-based
sessions, and serves its static assets from the binary. It is a translator: it calls domain
module interfaces in-process and renders their clean structs into Templ HTML; it holds no domain
state and hosts all `/ui` routes.

## Requirements

### Requirement: Web Landing Page
The gateway SHALL serve an HTML landing page at the web root, rendered from a shared base layout
that loads the gateway's pinned htmx asset.

#### Scenario: Visitor opens the site root
- **GIVEN** the web gateway is running
- **WHEN** a browser requests `GET /`
- **THEN** the gateway responds with an HTML page built from the base layout
- **AND** the page references the gateway's pinned htmx asset under `/static`

### Requirement: Health Check Endpoint
The gateway SHALL expose a health endpoint that reports whether the service and its database are
reachable.

#### Scenario: Database reachable
- **GIVEN** the database is reachable
- **WHEN** `GET /healthz` is requested
- **THEN** the gateway responds with a 200 status indicating healthy

#### Scenario: Database unreachable
- **GIVEN** the database cannot be reached
- **WHEN** `GET /healthz` is requested
- **THEN** the gateway responds with a non-200 status indicating unhealthy

### Requirement: htmx Fragment Rendering
The gateway SHALL serve htmx-targeted updates as HTML fragments — not full pages — from its `/ui`
routes.

#### Scenario: htmx requests a partial update
- **GIVEN** the landing page contains an htmx-driven region
- **WHEN** htmx issues a `GET` to that region's `/ui` route
- **THEN** the gateway returns only that fragment's HTML
- **AND** the surrounding page shell is not included in the response

### Requirement: Cookie-Based Session Foundation
The gateway SHALL run signed, encrypted cookie-based session middleware on all routes, persisting
per-visitor state in the browser cookie with no server-side session storage.

#### Scenario: Session value persists across requests and restarts
- **GIVEN** the session middleware is active
- **WHEN** a handler stores a value in the session and saves it
- **THEN** the value is returned to the browser in a signed, encrypted cookie
- **AND** it is available to handlers on the visitor's subsequent requests
- **AND** it remains valid after the server restarts, because no session state is held server-side

### Requirement: Embedded Pinned Static Assets
The gateway SHALL serve its static assets — including a pinned htmx — from files embedded in the
binary, under a `/static` path.

#### Scenario: Fetching the htmx asset
- **GIVEN** the running web gateway binary
- **WHEN** a browser requests the pinned htmx asset under `/static`
- **THEN** the gateway serves it from the embedded filesystem
- **AND** no external or CDN request is required

### Requirement: Google Sign-In
The gateway SHALL let a visitor sign in with Google. On a successful callback it SHALL provision
or resolve the account for the Google identity (through the account module) and establish an
authenticated session for that account.

#### Scenario: Starting the login flow
- **GIVEN** an anonymous visitor
- **WHEN** they begin Google login
- **THEN** the gateway redirects them to Google's consent screen with a state parameter

#### Scenario: Successful callback provisions the account and signs in
- **GIVEN** a visitor returning from Google with a valid authorization code and matching state
- **WHEN** the gateway handles the callback
- **THEN** it resolves the Google identity (id, email, name)
- **AND** provisions or resolves the account via the account module
- **AND** establishes an authenticated session for that account

### Requirement: OAuth State Validation
The gateway SHALL generate a state value for each login attempt and SHALL reject a callback whose
state does not match the value stored for that visitor, establishing no session.

#### Scenario: Mismatched state is rejected
- **GIVEN** a login attempt that stored a state value
- **WHEN** the callback arrives with a missing or different state
- **THEN** the gateway rejects the login
- **AND** no authenticated session is established

### Requirement: Authenticated Session Reflection
The gateway SHALL reflect authentication state in the UI: a signed-in visitor sees their identity
and a way to log out; an anonymous visitor sees a way to sign in.

#### Scenario: Signed-in visitor sees their identity
- **GIVEN** a visitor with an authenticated session
- **WHEN** they load the landing page
- **THEN** the page shows their account email and a log-out control

#### Scenario: Anonymous visitor is offered sign-in
- **GIVEN** a visitor with no authenticated session
- **WHEN** they load the landing page
- **THEN** the page offers a way to sign in with Google

### Requirement: Logout
The gateway SHALL let a signed-in visitor log out, clearing their session so subsequent requests
are anonymous.

#### Scenario: Logging out clears the session
- **GIVEN** a signed-in visitor
- **WHEN** they log out
- **THEN** the gateway clears their session
- **AND** subsequent requests are treated as anonymous

### Requirement: Tesla Account Connection
The gateway SHALL let a signed-in user connect a Tesla account through Tesla OAuth, and on a
successful callback SHALL store the connection's tokens (access token, refresh token, and access
expiry) via the account module.

#### Scenario: Starting the Tesla connect flow
- **GIVEN** a signed-in user
- **WHEN** they begin connecting their Tesla
- **THEN** the gateway redirects them to Tesla's authorization page with a state parameter

#### Scenario: Successful callback stores the tokens
- **GIVEN** a signed-in user returning from Tesla with a valid code and matching state
- **WHEN** the gateway handles the callback
- **THEN** it exchanges the code for Tesla tokens
- **AND** stores the tokens for the user's account via the account module

### Requirement: Tesla Connect Requires Authentication
The gateway SHALL require an authenticated session to begin or complete a Tesla connection; an
anonymous visitor SHALL be directed to sign in instead.

#### Scenario: Anonymous visitor cannot connect
- **GIVEN** a visitor with no authenticated session
- **WHEN** they request the Tesla connect route
- **THEN** the gateway redirects them to sign in
- **AND** does not start the Tesla OAuth flow

### Requirement: Tesla Connect State Validation
The gateway SHALL validate a CSRF state on the Tesla callback and SHALL reject a mismatch without
storing any tokens.

#### Scenario: Mismatched state is rejected
- **GIVEN** a Tesla connect attempt that stored a state value
- **WHEN** the callback arrives with a missing or different state
- **THEN** the gateway rejects the callback
- **AND** no tokens are stored

### Requirement: Vehicle Dashboard
The gateway SHALL show a signed-in user the Tesla vehicles registered to their account, reading
them through the account module's public interface (never directly from the registry table). It
SHALL render the vehicles as an htmx page and a refreshable fragment, mapping the account module's
clean vehicle data to a presentation model that shows each vehicle's `display_name` and `vin` only.
The gateway SHALL request a Tesla `ListVehicles` call through the account module — which persists
the result into the per-account registry — only when the account has no vehicles registered.
Thereafter the dashboard SHALL read the registered vehicles from the account module and SHALL NOT
call Tesla. The presentation model SHALL NOT include the volatile vehicle `state`.

#### Scenario: Account with registered vehicles shows them without calling Tesla
- **GIVEN** a signed-in user whose account has working Tesla vehicles already registered
- **WHEN** they open the dashboard
- **THEN** the gateway reads the account's registered vehicles through the account module
- **AND** lists each vehicle with its display name and VIN
- **AND** it does not call the Tesla adapter

#### Scenario: Account with no registered vehicles triggers a one-time Tesla list and registration
- **GIVEN** a signed-in user whose account has a working Tesla connection but no vehicles registered
- **WHEN** they open the dashboard
- **THEN** the gateway obtains the account's current Tesla access token
- **AND** calls `tesla.ListVehicles`
- **AND** persists the returned vehicles through the account module as a one-time registration
- **AND** displays those vehicles' display names and VINs

#### Scenario: Vehicles are shown without the volatile state field
- **GIVEN** a signed-in user with vehicles registered
- **WHEN** the dashboard renders the vehicle list
- **THEN** each rendered vehicle shows its display name and VIN only
- **AND** no online/asleep state value is shown for any vehicle

#### Scenario: Refreshing the vehicle list returns only the fragment
- **GIVEN** the dashboard page is open
- **WHEN** htmx requests the vehicles refresh route
- **THEN** the gateway returns only the vehicle-list fragment
- **AND** not the surrounding page shell
- **AND** when the account already has registered vehicles it does not call Tesla

#### Scenario: No Tesla connection prompts to connect instead of listing
- **GIVEN** a signed-in user whose account has no Tesla connection and no registered vehicles
- **WHEN** they open the dashboard
- **THEN** the gateway shows the Tesla connect prompt
- **AND** it does not call `tesla.ListVehicles`

### Requirement: Dashboard Requires Authentication
The gateway SHALL require an authenticated session to view the dashboard or its vehicle fragment;
an anonymous visitor SHALL be directed to sign in.

#### Scenario: Anonymous visitor cannot view the dashboard
- **GIVEN** a visitor with no authenticated session
- **WHEN** they request the dashboard
- **THEN** the gateway redirects them to sign in

### Requirement: Dashboard Connect and Reconnect States
The gateway SHALL show a connect prompt when the signed-in user has no Tesla connection, and a
reconnect prompt when the stored credentials are expired or invalid, rather than a blank list or a
raw error.

#### Scenario: No Tesla connection prompts to connect
- **GIVEN** a signed-in user whose account has no Tesla connection
- **WHEN** they open the dashboard
- **THEN** the gateway shows a prompt to connect their Tesla

#### Scenario: Expired credentials prompt to reconnect
- **GIVEN** a signed-in user whose stored Tesla credentials are expired or invalid
- **WHEN** the tesla adapter rejects the request as unauthorized
- **THEN** the gateway shows a prompt to reconnect their Tesla
- **AND** does not leak a raw error to the page
