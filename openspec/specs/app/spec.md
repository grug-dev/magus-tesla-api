# app Specification

## Purpose
TBD - created by archiving change RM35-app-adopt-clock. Update Purpose after archive.
## Requirements
### Requirement: Scheduler Default Time Zone
The app capability SHALL determine the daily collection schedule's time zone using the
platform's default time zone, `America/Bogota`, whenever the scheduler is constructed with
no explicit time zone — never the host process's own local time zone. When the scheduler IS
constructed with an explicit time zone, that configured zone SHALL continue to determine the
schedule, unaffected by the platform default.

#### Scenario: An explicitly configured time zone determines the daily schedule
- **GIVEN** the scheduler is constructed with an explicit time zone
- **WHEN** it computes the next daily run time
- **THEN** the next run time is computed by observing the target hour:minute in that
  configured time zone
- **AND** the platform default time zone plays no part in the computation

#### Scenario: No explicitly configured time zone falls back to the platform default
- **GIVEN** the scheduler is constructed with no explicit time zone
- **WHEN** it computes the next daily run time
- **THEN** the next run time is computed by observing the target hour:minute in the
  platform's default time zone, `America/Bogota`
- **AND** the next run time is NOT computed by observing the target hour:minute in the host
  process's own local time zone

### Requirement: A Periodic Retry Schedule Runs Every 30 Minutes From 04:00 Until The End Of The Local Day

The app capability SHALL run a second, independent schedule alongside the
daily collection schedule. This retry schedule SHALL fire every 30 minutes,
starting at 04:00 local time and continuing until the end of the local day,
observed in the same time zone rule as the daily collection schedule (the
platform default when none is explicitly configured, otherwise the
configured zone). It SHALL NOT fire between midnight and 04:00.

**Reason**: MAG-98 / `RM69-nightly-retry-unfinished-vehicles` tier 2. 04:00
gives the 03:30 nightly run room to start before the first retry check could
ever fire; the schedule stops at day's end because the next nightly run takes
over from there.

#### Scenario: The retry schedule fires at 30-minute intervals starting at 04:00
- **GIVEN** the retry schedule is running
- **WHEN** the local time reaches 04:00
- **THEN** a retry tick occurs
- **AND** the next retry tick occurs 30 minutes later, and every 30 minutes
  after that, until the end of the local day

#### Scenario: The retry schedule does not fire before 04:00
- **GIVEN** the retry schedule is running
- **WHEN** the local time is between midnight and 04:00
- **THEN** no retry tick occurs

#### Scenario: An explicitly configured time zone determines the retry schedule
- **GIVEN** the retry schedule is constructed with an explicit time zone
- **WHEN** it computes its next tick
- **THEN** the next tick is computed by observing the target times in that
  configured time zone, the same zone rule the daily collection schedule
  already follows

### Requirement: A Retry Tick Processes Only Vehicles Not Yet Done For The Previous Day

On each retry tick, the app capability SHALL determine which currently
registered vehicles have not yet produced their precomputed metrics for the
previous local calendar day, and SHALL process only that set. A retry tick
that finds every registered vehicle already done SHALL make no attempt to
process any vehicle and SHALL leave no trace of having run.

**Reason**: MAG-98 / `RM69-nightly-retry-unfinished-vehicles` tier 2. A car
with no internet at 03:30 gets no snapshot and no precomputed metrics for
that day; the retry exists to give it another chance without touching a car
that already succeeded, and a normal night — every car already done — must
add no noise to the operational record.

#### Scenario: A retry tick processes only the vehicles still not done
- **GIVEN** three registered vehicles, one of which has no precomputed
  metrics for the previous local day
- **WHEN** a retry tick occurs
- **THEN** only the one vehicle without metrics for the previous day is
  processed
- **AND** the other two vehicles are not processed

#### Scenario: A retry tick with nothing to do makes no attempt and leaves no trace
- **GIVEN** every registered vehicle already has precomputed metrics for the
  previous local day
- **WHEN** a retry tick occurs
- **THEN** no vehicle is processed
- **AND** no run summary is recorded for that tick

#### Scenario: A retry tick that succeeds is recorded as a retry
- **GIVEN** a retry tick finds at least one vehicle not yet done
- **WHEN** that vehicle is processed
- **THEN** the resulting run summary is attributed to the periodic retry,
  distinct from the daily schedule and from a manual re-run

### Requirement: A Retry Tick Never Overlaps Another Vehicle-Data Cycle

The app capability SHALL NOT allow a retry tick to process vehicles while the
daily collection schedule, a manual re-run, or another retry tick is already
processing. A retry tick that finds the platform already busy processing
SHALL be skipped without error, and the following retry tick SHALL still be
attempted on schedule.

**Reason**: MAG-98 / `RM69-nightly-retry-unfinished-vehicles` tier 2. Two
concurrent cycles racing to write the same vehicle's data would corrupt the
per-run accounting the platform already relies on for every other trigger.

#### Scenario: A retry tick is skipped while another cycle is already running
- **GIVEN** a vehicle-data cycle, triggered by any source, is already running
- **WHEN** a retry tick occurs
- **THEN** the retry tick makes no attempt to process any vehicle
- **AND** no error halts the retry schedule

#### Scenario: The retry schedule continues after a skipped tick
- **GIVEN** a retry tick was skipped because another cycle was running
- **WHEN** the next scheduled retry tick occurs
- **THEN** it is attempted normally, unaffected by the previous tick having
  been skipped

