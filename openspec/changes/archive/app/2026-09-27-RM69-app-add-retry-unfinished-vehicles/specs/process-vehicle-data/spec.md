## ADDED Requirements

### Requirement: Processing A Vehicle-Data Cycle For A Specified Vehicle Subset

The platform SHALL provide an operation that runs the same five steps, in the
same order, with the same whole-cycle-failure short-circuit rule as
processing a vehicle-data cycle for every registered vehicle, but scoped to a
caller-supplied set of vehicle identifiers. A step that iterates registered
vehicles SHALL iterate only the vehicles named in the caller-supplied set
that are still currently registered; a vehicle identifier in the set that is
not currently registered to any account SHALL be silently excluded from
every step, with no error. This operation SHALL be identified by a freshly
generated run identifier and SHALL record what triggered it, exactly as the
whole-fleet operation does.

**Reason**: MAG-98 / `RM69-nightly-retry-unfinished-vehicles` tier 2. A
periodic retry of the vehicles the nightly run did not finish must not wake
or re-process a vehicle that already finished — this operation is the
subset-scoped cycle the retry calls.

#### Scenario: A subset cycle processes only the named vehicles
- **GIVEN** three registered vehicles, and a caller-supplied set naming only
  one of them
- **WHEN** the subset cycle is processed
- **THEN** fleet data is synchronized for the named vehicle only
- **AND** charging data is processed for the named vehicle only
- **AND** analytics are recalculated for the named vehicle only
- **AND** monthly vehicle metrics are synced for the named vehicle only

#### Scenario: An unregistered vehicle identifier in the set produces no trace
- **GIVEN** a caller-supplied set containing one currently-registered vehicle
  identifier and one identifier that is not registered to any account
- **WHEN** the subset cycle is processed
- **THEN** the registered vehicle is processed
- **AND** no step processes anything for the unregistered identifier
- **AND** no error is returned

#### Scenario: A whole-cycle sync failure stops a subset cycle the same way it stops the full cycle
- **GIVEN** a subset cycle whose fleet-data synchronization step fails as a
  whole
- **WHEN** the subset cycle is processed
- **THEN** charging data is not processed for that cycle
- **AND** analytics are not recalculated for that cycle
- **AND** monthly vehicle capacity is not measured for that cycle
- **AND** monthly vehicle metrics are not synced for that cycle
- **AND** the cycle reports the failure

#### Scenario: Monthly vehicle capacity, when measured, is scoped to the named vehicles only
- **GIVEN** a subset cycle running on the first calendar day of the month,
  naming two registered vehicles
- **WHEN** monthly vehicle capacity is measured for that cycle
- **THEN** it is measured for each of the two named vehicles individually
- **AND** it is not measured for any registered vehicle outside the named set

### Requirement: A Vehicle-Data Cycle May Be Triggered By A Periodic Retry

The platform SHALL support recording a vehicle-data cycle, whole-fleet or
subset-scoped, as triggered by a periodic retry of previously unfinished
vehicles, distinct from the scheduler and from a manual re-run.

**Reason**: MAG-98 / `RM69-nightly-retry-unfinished-vehicles` tier 2. An
operator reading `poll_runs`/`poll_attempts` needs to tell a retry-triggered
cycle apart from the nightly scheduler's own tick.

#### Scenario: A retry-triggered subset cycle is recorded with that attribution
- **GIVEN** a subset cycle is processed with the retry attribution
- **WHEN** its run summary is recorded
- **THEN** the summary's trigger attribution is the retry value
