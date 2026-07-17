## ADDED Requirements

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
