## ADDED Requirements

### Requirement: On-Demand Single Collection Cycle
The telemetry capability SHALL be invokable for exactly one immediate collection cycle via the
poller command's one-shot mode, distinct from its scheduled nightly mode. In one-shot mode the
capability SHALL run the same collection cycle the scheduled mode runs — enumerating every
registered vehicle across all accounts, per-vehicle wake-if-needed, capture, and attempt recording
— and SHALL persist the captured snapshots and attempts to the same tables the scheduled mode uses.
The one-shot mode SHALL NOT arm or wait on the daily schedule; it SHALL run the cycle immediately
and then the process SHALL exit. The one-shot mode SHALL share the identical per-cycle report
formatter with the scheduled mode, so the operational summary logged for a one-shot cycle is
indistinguishable from the summary logged for a scheduled cycle.

#### Scenario: A one-shot cycle runs immediately and exits
- **GIVEN** the poller is started with the one-shot flag
- **WHEN** the process starts
- **THEN** a single collection cycle runs immediately without waiting for any scheduled time
- **AND** the cycle enumerates every registered vehicle across all accounts and processes each
- **AND** the cycle's per-vehicle outcome summary is logged using the same report format the
  scheduled path uses
- **AND** the process exits without arming or waiting on the daily schedule

#### Scenario: A one-shot cycle persists data to the same tables as a scheduled cycle
- **GIVEN** one or more registered vehicles with valid connections
- **WHEN** a one-shot collection cycle runs
- **THEN** each successfully captured vehicle has a snapshot row in the same table the scheduled
  cycle writes
- **AND** each vehicle has an attempt row in the same table the scheduled cycle writes
- **AND** no new database object is created, altered, or dropped by the one-shot mode

#### Scenario: A one-shot cycle is cancellable by a termination signal
- **GIVEN** the poller is running a one-shot collection cycle that is mid-flight (e.g. waiting for
  a sleeping vehicle to wake)
- **WHEN** the process receives a termination signal
- **THEN** the in-flight cycle's context is cancelled
- **AND** the cycle stops without starting further per-vehicle work
- **AND** the process exits

#### Scenario: A one-shot cycle with a whole-cycle failure exits non-zero
- **GIVEN** the poller is started with the one-shot flag and the cycle fails at the whole-cycle
  level (the account enumeration itself fails)
- **WHEN** the one-shot collection cycle returns
- **THEN** the whole-cycle error is logged
- **AND** the process exits with a non-zero status

#### Scenario: A one-shot cycle that completes with per-vehicle failures exits zero
- **GIVEN** the poller is started with the one-shot flag and the cycle completes but some
  individual vehicles fail to be captured
- **WHEN** the one-shot collection cycle returns
- **THEN** the per-vehicle failures are recorded as attempts and reflected in the logged cycle
  summary
- **AND** the process exits with a zero status, because the cycle ran to completion

### Requirement: Scheduled Nightly Mode Is Unchanged By The One-Shot Flag
The telemetry capability's scheduled nightly mode SHALL remain the default behavior of the poller
command when the one-shot flag is absent or false. The nightly mode SHALL arm the in-app daily
schedule, block until the scheduled time, run one cycle per day, log the same per-cycle summary
the one-shot mode logs, and shut down gracefully on a termination signal — exactly as before the
one-shot flag existed. The presence of the one-shot flag SHALL NOT alter the scheduling, the wake
behavior, the persistence, or the shutdown semantics of the nightly mode.

#### Scenario: The nightly scheduler runs unchanged when the one-shot flag is not set
- **GIVEN** the poller is started without the one-shot flag
- **WHEN** the process starts
- **THEN** the nightly scheduler is armed at the configured hour, minute, and timezone
- **AND** the poller blocks until the scheduled time runs a cycle, then reschedules for the next day
- **AND** each cycle's outcome is logged with the same report format the one-shot path uses
- **AND** a termination signal stops the scheduler and the process exits cleanly without starting a
  new cycle