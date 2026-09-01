## ADDED Requirements

### Requirement: Run-Level Poll Summary Storage

The telemetry capability SHALL persist exactly one summary record for every
invocation of its collection cycle, identified by that invocation's own run
identity. The record SHALL capture what triggered the invocation, when it started
and finished, how long it took, how many accounts and vehicles were attempted and
with what outcomes, and how many Tesla Fleet API requests the invocation spent.

The record SHALL be written even when the invocation fails before attempting a
single vehicle, so that a failed invocation still leaves a durable, queryable trace
of having run at all — the absence of any earlier record for a failed invocation is
the exact gap this requirement closes.

A second attempt to record a summary for a run identity that already has one SHALL
be rejected as an error, and SHALL NOT alter the existing record — recording a run
twice is never a legitimate outcome.

#### Scenario: A fully successful run records complete counts
- **GIVEN** a collection cycle that completes across two accounts with no
  whole-account failures
- **WHEN** the run summary is recorded
- **THEN** the stored record's account and vehicle counts match the cycle's actual
  outcomes exactly

#### Scenario: A run that fails before attempting any vehicle still leaves a trace
- **GIVEN** a collection cycle whose vehicle enumeration fails before any account or
  vehicle is attempted
- **WHEN** the run summary is recorded
- **THEN** a record for that run's identity exists
- **AND** its account and vehicle counts are all zero
- **AND** its start and finish times and duration are recorded, reflecting how
  quickly the failure occurred

#### Scenario: Recording the same run twice is rejected
- **GIVEN** a run summary has already been recorded for a given run identity
- **WHEN** a second summary is recorded for the same run identity
- **THEN** the second attempt is rejected as an error
- **AND** the originally recorded summary is unchanged

### Requirement: Tesla API Call Counting

The telemetry capability SHALL count every request it makes to the Tesla Fleet API
during one collection cycle and SHALL surface that count as part of the cycle's
outcome. Every request SHALL be counted regardless of whether it succeeds or fails —
a rejected or failed request still consumes a request against the vendor's API.

#### Scenario: A cycle with a mix of online and sleeping vehicles counts every request
- **GIVEN** a collection cycle covering one account with two vehicles, one already
  reporting online and one requiring a wake
- **WHEN** the cycle completes
- **THEN** the reported Tesla API call count equals the exact number of vehicle-list,
  wake, vehicle-data, and charging-history requests the cycle made

#### Scenario: A failed request still counts
- **GIVEN** a collection cycle whose account-wide vehicle-list request is rejected
  by the Tesla API
- **WHEN** the cycle completes
- **THEN** the reported Tesla API call count includes that rejected request

### Requirement: Account-Level Attempt And Outcome Counts

The telemetry capability SHALL report, for each collection cycle, how many accounts
were attempted, how many completed without a whole-account failure, and how many
failed as a whole. An account counts as a whole-account failure only when it cannot
obtain a usable Tesla access token for the cycle, or when its account-wide vehicle
list request is rejected as unauthorized — no other failure mode counts an account
as failed.

#### Scenario: An account with no usable Tesla connection counts as failed
- **GIVEN** a collection cycle covering an account with no usable stored Tesla
  connection
- **WHEN** the cycle completes
- **THEN** that account counts toward the cycle's failed-account total
- **AND** every vehicle belonging to that account is recorded with an unauthorized
  outcome

#### Scenario: An account whose vehicle list is rejected as unauthorized counts as failed
- **GIVEN** a collection cycle covering an account whose account-wide vehicle list
  request is rejected as unauthorized
- **WHEN** the cycle completes
- **THEN** that account counts toward the cycle's failed-account total

#### Scenario: Succeeded accounts is attempted minus failed
- **GIVEN** a collection cycle covering several accounts, some of which are
  whole-account failures
- **WHEN** the cycle completes
- **THEN** the reported succeeded-account count equals the attempted-account count
  minus the failed-account count

### Requirement: Nightly Cycle Log Summary

The telemetry capability's per-cycle operational log line SHALL label its
vehicle-grain counts unambiguously as counting vehicles, and SHALL additionally
report the cycle's account-grain attempt/outcome counts and its Tesla API call
count.

**Reason**: MAG-35 / `RM36-poll-run-tracking` roadmap D7. The prior log line mixed a
vehicle-grain count (`attempted`/`succeeded`) with an account-grain count
(`charging_failures`) under headings that did not say which grain each counter used,
which is the exact ambiguity the source ticket asked about.

#### Scenario: The log line labels vehicle counts as vehicle counts
- **GIVEN** a completed collection cycle
- **WHEN** the cycle's summary is logged
- **THEN** the printed line labels the attempted and succeeded counts as counting
  vehicles, not accounts

#### Scenario: The log line reports account-level counts and the API call count
- **GIVEN** a completed collection cycle
- **WHEN** the cycle's summary is logged
- **THEN** the printed line includes the attempted, succeeded, and failed account
  counts and the total Tesla API call count for that cycle

## Notes (implementation detail, not a new or changed Requirement)

### `CycleReport.Attempted`/`.Succeeded` field names are unchanged

The log line's relabeling (above) is a presentation change only. The underlying
`CycleReport` Go struct keeps its existing `Attempted`/`Succeeded` field names —
renaming them would require editing a test file in `internal/app`, outside this
capability's module boundary and this change's scope (design.md D8). No behavior or
requirement in this spec depends on the field names themselves, only on the
capability's outputs (the persisted record and the printed line).

### `poll_runs` has no read requirement in this change

This change adds only the write side (`RunWriter.RecordRun`) and the underlying
storage. No requirement above describes reading `poll_runs` back, because nothing in
this roadmap does — a future change (backlog: a gateway poll-runs read surface) will
add that capability and its own requirements when a real consumer exists.
