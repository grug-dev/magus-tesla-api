## ADDED Requirements

### Requirement: Precomputed Daily Vehicle Metrics

The analytics capability SHALL persist one precomputed metrics row per vehicle for
every calendar day that has a telemetry snapshot, whether or not that day has a
computable predecessor — dense, one-to-one with the vehicle's stored snapshot days.
Every row SHALL carry the day's raw battery level, odometer reading and range as
reported by telemetry, regardless of predecessor. For a day that has a computable
predecessor, the row SHALL additionally carry the day's derived distance travelled,
battery percentage used, days spanned since the prior computed day, and the day's
corrected consumed-percentage figure with its gap-flag and inferred
missing-charging-source. For a day with NO computable predecessor (a vehicle's
first-ever telemetry capture), the capability SHALL still persist a row carrying
that day's raw battery level, odometer reading and range, and SHALL leave every
derived and consumed-percentage field on that row absent — never a fabricated
value — and SHALL record that day as not gap-flagged, since a gap-flag judgement
requires a computed consumed-percentage figure that does not exist for this row.

#### Scenario: A day with a predecessor gets a fully-populated persisted row
- **GIVEN** two consecutive telemetry snapshots for a vehicle, one calendar day apart
- **WHEN** the capability recalculates the later day
- **THEN** exactly one precomputed metrics row exists for that day, carrying its
  battery level, odometer reading, range, distance travelled, battery percentage
  used, and days spanned

#### Scenario: A vehicle's first-ever day still gets a row, with its derived fields absent
- **GIVEN** a telemetry snapshot that is a vehicle's first-ever capture, with no
  predecessor
- **WHEN** the capability recalculates the surrounding date range
- **THEN** a precomputed metrics row exists for that day, carrying its raw battery
  level, odometer reading, and range
- **AND** that row's distance travelled, battery percentage used, days spanned, and
  corrected consumed-percentage figure are all absent, not zero or any other
  fabricated value
- **AND** that row is not recorded as gap-flagged

#### Scenario: A vehicle's first-ever day is never mistaken for a charge gap
- **GIVEN** a vehicle's first-ever telemetry capture, on a day the vehicle also
  travelled a real, non-trivial distance
- **WHEN** the capability recalculates that day
- **THEN** the persisted row's gap-flag is false, never true — a zero-valued
  consumed-percentage figure is never substituted for the absent one, which is what
  would otherwise make this day indistinguishable from a genuine suspected missing
  charge record

### Requirement: Incremental Recompute Via An Analytics-Owned Watermark

The analytics capability SHALL track, for each vehicle and for each of its three
input sources (telemetry snapshots, Supercharger sessions, manually-logged charge
entries) independently, a cursor recording the latest source data it has already
incorporated. On each reconciliation the capability SHALL query each source for data
updated since that source's own cursor minus a fixed safety overlap, recompute every
calendar day affected by what it finds, and then advance that source's cursor —
never a different source's cursor — to reflect what it just incorporated. A source
with no prior cursor for a vehicle SHALL be treated as never having been
incorporated, so its very first reconciliation incorporates that source's entire
history for that vehicle.

#### Scenario: A revised Supercharger session weeks old is picked up
- **GIVEN** a vehicle whose Supercharger-sessions cursor was last advanced yesterday
- **AND** a Supercharger session from three weeks ago has its billing state revised
  today (its stored `updated_at` is refreshed)
- **WHEN** the capability next reconciles that vehicle
- **THEN** it recomputes the calendar day that session's revision affects, even
  though that day is well outside any trailing window measured from today

#### Scenario: One source's cursor advancing never rewinds or skips another source's
- **GIVEN** a vehicle whose manually-logged-charge-entries cursor advances because a
  user edited an entry
- **WHEN** the capability reconciles that vehicle
- **THEN** the telemetry-snapshots and Supercharger-sessions cursors for that vehicle
  are left exactly as they were before this reconciliation

#### Scenario: A vehicle with no prior cursor for a source backfills that source's full history
- **GIVEN** a vehicle with no stored cursor for the telemetry-snapshots source
- **WHEN** the capability first reconciles that vehicle
- **THEN** it recomputes every calendar day of that vehicle's stored telemetry
  history, persisting a row for each such day (with its derived fields absent on
  any day that itself has no computable predecessor)

### Requirement: Odometer Distance-Per-Day, Already Anomaly-Clamped

The analytics capability SHALL expose, for a given vehicle and date range, the
per-calendar-day distance travelled (the day's precomputed distance figure, floored
at zero) together with that day's absolute odometer reading. A day whose distance
figure is negative (a clock-skew or odometer-read anomaly) SHALL be reported as zero
distance, never a negative value. The result SHALL be sparse: one entry per day that
has a computed distance figure, no entry for a day that does not — this holds
whether the day has no precomputed metrics row at all, or has a row whose distance
figure is absent because that day had no computable predecessor (a persisted row
alone is not sufficient for a day to appear in this result).

#### Scenario: A normal day reports its travelled distance
- **GIVEN** a precomputed metrics row for a day whose stored distance figure is
  positive
- **WHEN** the capability is asked for that day's odometer distance
- **THEN** it returns that day's stored distance value and that day's absolute
  odometer reading

#### Scenario: A negative stored distance is reported as zero, never negative
- **GIVEN** a precomputed metrics row for a day whose stored distance figure is
  negative (an odometer read anomaly)
- **WHEN** the capability is asked for that day's odometer distance
- **THEN** it returns zero as that day's distance, and that day's absolute odometer
  reading unchanged

#### Scenario: A day with no precomputed row is absent from the result
- **GIVEN** a date range containing a day with no precomputed metrics row
- **WHEN** the capability is asked for that date range's odometer distance
- **THEN** the result contains no entry for that day

#### Scenario: A vehicle's first-ever day has a row but is still absent from this result
- **GIVEN** a precomputed metrics row for a vehicle's first-ever telemetry capture,
  whose distance figure is absent because that day has no computable predecessor
- **WHEN** the capability is asked for that day's odometer distance
- **THEN** the result contains no entry for that day, exactly as if no row existed
  at all
- **AND** this matches what the capability returned for this same day before the
  capability began persisting a row for every snapshot day — a persisted row with
  an absent distance figure is not new information for this particular read

### Requirement: Write-Path Freshness For Charge-Entry Changes

The analytics capability SHALL recompute a calendar day's precomputed metrics
whenever a manually-logged charge entry for that day is created, updated, or
deleted, before the write that triggered it is considered complete from the
caller's perspective. For an edit that changes the entry's date, the capability
SHALL recompute both the old and new day. This ensures a read immediately
following the write reflects the change with no separate refresh step.

#### Scenario: Logging a missing charge clears a previously-flagged day on the very next read
- **GIVEN** a calendar day currently flagged as a data gap because no matching charge
  event was found
- **WHEN** a user logs a manual charge entry for that day
- **THEN** the very next read of that day's precomputed metrics reflects the new
  entry and the day is no longer flagged, with no separate invalidation or refresh
  step required

## MODIFIED Requirements

### Requirement: Per-Day Battery-Consumed Derivation

The analytics capability SHALL derive, for each calendar day that has a computable
predecessor, a corrected battery-consumed percentage equal to the day's raw
battery-level delta plus the sum of `(end_battery_pct − start_battery_pct)` across
every charge event matched to that day from both charging-cost sources the platform
stores (Supercharger sessions and manually-logged charge entries), summing across ALL
matched charge events for a day, never a single one. This derivation SHALL happen at
write time (on recalculation, triggered by new or changed source data), and a read of
a given date range SHALL return the already-computed values for that range rather
than recomputing them from the underlying telemetry, Supercharger and manual-entry
data on every call.

#### Scenario: A single matched charge event corrects the raw delta
- **GIVEN** a vehicle whose raw battery-level delta for a day is −51
- **AND** exactly one charge event matched to that day added back 62 percentage
  points (`end_battery_pct − start_battery_pct = 80 − 18 = 62`)
- **WHEN** the capability's precomputed value for that day is read
- **THEN** it is 11 (`−51 + 62`)

#### Scenario: Multiple matched charge events on the same day are all summed
- **GIVEN** a vehicle whose raw battery-level delta for a day is −45
- **AND** two charge events matched to that same day: one contributing +30
  percentage points and one contributing +20 percentage points
- **WHEN** the capability's precomputed value for that day is read
- **THEN** it is 5 (`−45 + 30 + 20`), never −25 (only the later event) or 15 (the
  span from the first event's start to the second's end)

#### Scenario: A read reflects the last recalculation, not the state at read time
- **GIVEN** a day's precomputed value was last recalculated before a new charge
  event for that day was logged
- **AND** the write path has not yet recalculated that day for the new event (a
  hypothetical delay in the write-path freshness requirement)
- **WHEN** the day's precomputed value is read
- **THEN** it reflects the data available at the LAST recalculation, not data logged
  since — recalculation, not the read itself, is what keeps the value current

#### Scenario: A day with no computable predecessor is absent from a read, even though a row exists for it
- **GIVEN** a vehicle's first-ever telemetry capture, a day with no computable
  predecessor, for which the capability persists a row carrying only that day's raw
  observations (see `Requirement: Precomputed Daily Vehicle Metrics`)
- **WHEN** a caller reads per-day battery-consumed values for a date range including
  that day
- **THEN** the result contains no entry for that day
- **AND** this is unchanged from the capability's behavior before it began
  persisting a row for every snapshot day — the day was never included in this read
  before, and it is not included now

## REMOVED Requirements

### Requirement: No Cache — Every Result Is Recomputed On Read

**Reason**: Roadmap D1 (MAG-26, `RM29-modular-monolith-boundaries`) makes analytics a
precomputed read model — `vehicle_metrics` now stores each day's derived result, and
`ConsumedByDay` reads that stored value instead of recomputing it from telemetry,
Supercharger and manual-entry data on every call. A precomputed read model and
"recomputed on every read" are mutually exclusive by definition, so this requirement
cannot survive roadmap D1 unchanged.

**Migration**: The user-visible guarantee this requirement protected — editing a
charge record changes the very next chart render, with no separate invalidation step
— is now covered by the new `Requirement: Write-Path Freshness For Charge-Entry
Changes` (write-time recalculation) together with the
modified `Requirement: Per-Day Battery-Consumed Derivation` (values are computed at
write time, not read time). No consumer relied on the OLD mechanism (live
recomputation) itself — only on the value staying current, which the new mechanism
still delivers.
