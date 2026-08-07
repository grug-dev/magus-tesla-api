# tesla Specification

## Purpose

The `tesla` capability is an anti-corruption adapter over the external Tesla Fleet API
(`ai/architecture.md` §6). It lets any caller list an account's vehicles, fetch a vehicle's
full state snapshot, and wake a sleeping vehicle on behalf of caller-supplied credentials. It
holds no user identity of its own, signals expired credentials distinctly so callers can
refresh, and exposes metric-converted companion values for the miles/mph data the Fleet API
returns.
## Requirements
### Requirement: Vehicle Inventory Listing

The tesla adapter SHALL return every vehicle associated with the account behind the supplied
credentials, each carrying its identity (identifier, VIN, display name), current state, and
access type. The access type reflects whether the authenticated account is the vehicle's
owner or has driver-level access, as reported by the Fleet API.

#### Scenario: Listing an account's vehicles
- **GIVEN** valid credentials for a Tesla account
- **WHEN** a caller requests the vehicle inventory
- **THEN** the adapter returns each vehicle associated with that account
- **AND** each vehicle carries its identifier, VIN, display name, and state

#### Scenario: Access type decoded for each vehicle
- **GIVEN** valid credentials for a Tesla account
- **AND** the Fleet API response includes an `access_type` field on one or more vehicles
- **WHEN** a caller requests the vehicle inventory
- **THEN** each vehicle in the result carries the `access_type` value as decoded from the
  Fleet API response
- **AND** a vehicle with `access_type: "OWNER"` in the response carries `AccessType` equal
  to `"OWNER"` in the returned DTO
- **AND** a vehicle with `access_type: "DRIVER"` in the response carries `AccessType` equal
  to `"DRIVER"` in the returned DTO

#### Scenario: Access type is empty when Tesla omits it
- **GIVEN** valid credentials for a Tesla account
- **AND** the Fleet API response omits the `access_type` field on a vehicle
- **WHEN** a caller requests the vehicle inventory
- **THEN** that vehicle's `AccessType` is the empty string
- **AND** the caller is not required to handle a nil or error for the missing field

### Requirement: Full Vehicle Snapshot
The tesla adapter SHALL fetch a single vehicle's full state snapshot with exactly one Fleet API
request and return BOTH the parsed typed snapshot AND the raw, unmodified payload of that same
response, so a caller can store the payload losslessly while reading typed fields. The typed
snapshot SHALL expose charge, climate, drive, and vehicle-state data, including the charge limit
and sentry-mode status.

#### Scenario: Fetching a snapshot for an online vehicle
- **GIVEN** valid credentials and the identifier of a vehicle that is online
- **WHEN** a caller requests that vehicle's data snapshot
- **THEN** the adapter returns the vehicle's full typed state
- **AND** the snapshot includes charge data (battery level, range, charging state, charge rate, charge limit)
- **AND** the snapshot includes climate data (inside and outside temperature, climate on/off)
- **AND** the snapshot includes drive data (speed, latitude, longitude, heading)
- **AND** the snapshot includes vehicle-state data (locked, odometer, software version, sentry mode)

#### Scenario: Raw payload returned alongside the typed snapshot
- **GIVEN** valid credentials and the identifier of a vehicle that is online
- **WHEN** a caller requests that vehicle's data snapshot
- **THEN** the adapter also returns the raw, unmodified vehicle-data payload from the same response
- **AND** parsing that raw payload yields the same values the typed snapshot exposes

#### Scenario: One request serves both results
- **GIVEN** valid credentials and the identifier of a vehicle that is online
- **WHEN** a caller requests that vehicle's data snapshot
- **THEN** the adapter makes exactly one Fleet API data request
- **AND** both the typed snapshot and the raw payload derive from that single response

### Requirement: Vehicle Wake
The tesla adapter SHALL request that a sleeping vehicle come online and return the vehicle's
reported state after the request.

#### Scenario: Waking a sleeping vehicle
- **GIVEN** valid credentials and the identifier of a sleeping vehicle
- **WHEN** a caller issues a wake request
- **THEN** the adapter asks the vehicle to come online
- **AND** returns the vehicle's reported state

### Requirement: Wake-Before-Data Ordering
Fetching a full vehicle snapshot SHALL require the vehicle to be online. The documented path is
to wake the vehicle and poll its state until it reports online before requesting the snapshot.

#### Scenario: Snapshot needed for a sleeping vehicle
- **GIVEN** a vehicle that is not online
- **WHEN** a caller needs that vehicle's full snapshot
- **THEN** the caller wakes the vehicle and polls the inventory until the vehicle reports online
- **AND** only then requests the snapshot

### Requirement: Distinct Unauthorized Signal
The tesla adapter SHALL surface a distinct, detectable unauthorized error (not a generic
failure) when the Fleet API rejects the supplied credentials as expired or invalid (HTTP 401),
so that callers can trigger a token refresh and retry.

#### Scenario: Expired access token is signalled distinctly
- **GIVEN** credentials whose access token has expired
- **WHEN** any adapter operation calls the Fleet API and receives an HTTP 401
- **THEN** the adapter returns a distinct unauthorized error that a caller can detect
- **AND** the caller can use that signal to refresh the token and retry

### Requirement: Stateless Per-Call Credentials
The tesla adapter SHALL hold no user identity of its own; callers SHALL supply the credentials
to use on every operation, so a single shared adapter instance can serve any user.

#### Scenario: One instance serves multiple users
- **GIVEN** a single shared adapter instance
- **WHEN** operations are invoked for different users, each with its own credentials
- **THEN** each call acts on behalf of the account behind the credentials supplied to that call
- **AND** the adapter retains no identity between calls

### Requirement: Metric Conversion of Distance Values
For every distance or speed value the adapter exposes in miles or miles-per-hour, it SHALL also
expose a companion value in kilometres or kilometres-per-hour. Companion values for optional
fields SHALL be nil-safe — absent when the source value is absent.

#### Scenario: Range and odometer available in kilometres
- **GIVEN** a vehicle snapshot reporting battery range and odometer in miles
- **WHEN** a caller reads the companion metric values
- **THEN** the adapter provides the range and odometer converted to kilometres

#### Scenario: Optional speed is nil-safe
- **GIVEN** a snapshot for a parked vehicle that reports no speed
- **WHEN** a caller reads the companion metric speed
- **THEN** the adapter reports no speed rather than a converted zero

### Requirement: Charging History
The tesla adapter SHALL return the account's Tesla-billed charging session history from the
`GET /api/1/dx/charging/history` Fleet API endpoint. The call is **account-scoped** (no
vehicle id required) and **server-side** (the vehicle does not need to be awake). The
adapter SHALL support optional date-range filtering and SHALL return the complete history
across all pages in a single Go call. The result SHALL be typed — every session, fee, and
invoice field from the live payload is represented as a Go struct field; nothing is
discarded to a `json.RawMessage`.

Only **Tesla-billed sessions** (Supercharger / Tesla-operated DC fast charging) are
returned by this endpoint. Home, Wall Connector, Mobile Connector, destination L2, and
non-Tesla-billed third-party sessions do NOT appear in this history. Callers and downstream
modules MUST NOT assume this represents complete vehicle charging history.

#### Scenario: Retrieving complete charging history
- **GIVEN** valid credentials for a Tesla account with `vehicle_charging_cmds` scope
- **WHEN** a caller requests the account's charging history with no filter params
- **THEN** the adapter calls `GET /api/1/dx/charging/history` on the Fleet API
- **AND** returns a typed result containing every session in the account's history
- **AND** each session carries its session identifier, VIN, site location name, country
  code, billing type, vehicle make type, charge start/stop/unlatch datetimes, a fees array,
  and an invoices array

#### Scenario: Session fees are fully typed
- **GIVEN** valid credentials and a charging session that has fees
- **WHEN** a caller reads the session's fees
- **THEN** each fee carries its fee type, currency code, pricing type, rate tiers
  (base + tiers 1–4), usage tiers (base + tiers 1–4), totals (base + tiers 1–4 + total
  due + net due), unit of measure, paid status, and payment status
- **AND** rate and usage tier 3 and tier 4 fields that are null in the Fleet API response
  are represented as nil pointers, not as zero values

#### Scenario: Session invoices are fully typed
- **GIVEN** valid credentials and a charging session that has an invoice
- **WHEN** a caller reads the session's invoices
- **THEN** each invoice carries its file name, content identifier, and invoice type

#### Scenario: Complete history fetched across pages
- **GIVEN** valid credentials and an account with more sessions than a single page holds
- **WHEN** a caller requests the history with no PageNo set in params
- **THEN** the adapter fetches all pages automatically
- **AND** the returned result contains every session across all pages
- **AND** TotalResults reflects the total count reported by the Fleet API

#### Scenario: Single-page fetch via PageNo
- **GIVEN** valid credentials and a non-zero PageNo in the request params
- **WHEN** a caller requests the charging history
- **THEN** the adapter fetches only the specified page
- **AND** returns the sessions from that page alongside TotalResults

#### Scenario: Date-range filtering
- **GIVEN** valid credentials and non-empty StartTime and/or EndTime in the request params
- **WHEN** a caller requests the charging history
- **THEN** the adapter passes the date-range params to the Fleet API
- **AND** returns only sessions within the specified date range

#### Scenario: Unauthorized credential rejected distinctly
- **GIVEN** credentials whose access token is expired or invalid
- **WHEN** a caller requests the charging history
- **THEN** the adapter returns `ErrUnauthorized` (detectable with `errors.Is`)
- **AND** no session data is returned

#### Scenario: Missing scope rejected distinctly
- **GIVEN** credentials that lack the `vehicle_charging_cmds` scope
- **WHEN** a caller requests the charging history
- **THEN** the adapter returns `ErrForbidden` (detectable with `errors.Is`)
- **AND** no session data is returned

#### Scenario: No vehicle wake required
- **GIVEN** valid credentials and a vehicle that is asleep
- **WHEN** a caller requests the charging history
- **THEN** the call succeeds — no wake is performed, no vehicle id is passed
- **AND** the returned sessions belong to the account, not to any single vehicle state

### Requirement: Companion Conversion of Tire Pressure Values
The adapter SHALL expose a companion value in PSI (pounds per square inch) for every tire-pressure
value it exposes in bar, the Fleet API's native pressure unit. The conversion factor SHALL be a
single named package-level constant, never written inline at a call site, and SHALL be the same
factor used anywhere else in the platform that converts bar to PSI. The companion SHALL be a
value-receiver method on the type that carries the bar value, following the same naming and
receiver conventions as the adapter's existing distance and speed companions.

#### Scenario: Tire pressure available in PSI for all four corners
- **GIVEN** a vehicle snapshot reporting tire pressure in bar for the front-left, front-right,
  rear-left, and rear-right corners
- **WHEN** a caller reads the companion pressure values
- **THEN** the adapter provides each of the four pressures converted to PSI
- **AND** each converted value is the corresponding bar value multiplied by the adapter's single
  bar-to-PSI conversion constant

#### Scenario: A truthfully reported zero bar converts to zero PSI
- **GIVEN** a vehicle snapshot reporting a tire pressure of exactly 0.0 bar for one corner
  (for example, a flat tire)
- **WHEN** a caller reads that corner's companion pressure value
- **THEN** the adapter reports 0.0 PSI
- **AND** the adapter does not treat the zero as an absent reading

#### Scenario: The bar values the adapter exposes are unchanged
- **GIVEN** a vehicle snapshot reporting tire pressure in bar
- **WHEN** a caller reads the adapter's tire-pressure values directly rather than the companions
- **THEN** the values are still expressed in bar, exactly as the Fleet API reported them
- **AND** the adapter performs no conversion on the values it mirrors from the payload

