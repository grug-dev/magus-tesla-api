## ADDED Requirements

### Requirement: Query And Fleet-API Argument Logging

The telemetry capability SHALL log, for every call made through its existing
persistence write seam, its snapshot-read port, its Supercharger-history read port,
and its poll-run write port, the call's identifying and date-filtering arguments, and
— for a method that returns a collection or a single optional record — how many
records were returned. The telemetry capability SHALL also log, for every request it
makes to the vendor Fleet API, that request's non-credential parameters, including the
complete parameter set of any date-ranged request.

This closes an observability gap: without it, a query's actual filter — in particular
whether a request asked for a bounded, recent window or the vendor's entire history —
cannot be determined from a successful run's output.

#### Scenario: A snapshot read logs its account, vehicle, and window
- **GIVEN** a call to the snapshot read port for a specific account, vehicle, and time
  window
- **WHEN** the call completes
- **THEN** a log line records the account identity, the vehicle identity, the window
  boundaries supplied, and the number of records returned

#### Scenario: A Supercharger-history read with no date bound logs that fact explicitly
- **GIVEN** a call to the account-wide Supercharger-history read port that supplies no
  date boundary
- **WHEN** the call completes
- **THEN** a log line records the account identity, the limit the query actually
  executed with, an explicit indication that no date bound was applied, and the number
  of records returned

#### Scenario: A Supercharger-history read logs the limit the query will actually use
- **GIVEN** a call to a Supercharger-history read port with a caller-supplied limit
  that means "no limit"
- **WHEN** the call completes
- **THEN** the logged limit is the concrete value the underlying query executes with,
  not the caller's placeholder value

#### Scenario: A poll-run write logs its run identity and trigger
- **GIVEN** a call to the poll-run write port for a given run
- **WHEN** the call is made
- **THEN** a log line records that run's identity and what triggered it

#### Scenario: A Fleet API charging-history request logs its complete parameters
- **GIVEN** a request to the vendor Fleet API's charging-history endpoint with a
  specific date range and pagination
- **WHEN** the request is made
- **THEN** a log line records the complete set of parameters supplied to that request

### Requirement: Structural Exclusion Of Credentials And Raw Payloads From Logs

No log line produced by the telemetry capability's instrumentation SHALL ever contain
a vendor access credential, or the content of a stored raw external-API payload. Where
a raw payload's size is useful to record, the instrumentation SHALL record only its
byte size, never its content.

#### Scenario: A Fleet API call with a live credential never logs it
- **GIVEN** a request to the vendor Fleet API made with a specific access credential
- **WHEN** the request is logged
- **THEN** the logged line does not contain that credential's value anywhere

#### Scenario: A write carrying a raw payload logs only its size
- **GIVEN** a write of a record that includes a raw external-API payload
- **WHEN** the write is logged
- **THEN** the logged line contains the payload's byte size and does not contain the
  payload's content
