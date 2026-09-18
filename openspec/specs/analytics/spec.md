# analytics Specification

## Purpose
Derived analytics over stored telemetry — the platform's metrics layer, sitting between
what `telemetry` captures and what the dashboard renders. It owns no database and no capture: it
reads sibling modules' public ports and computes values none of them store. It derives the
per-day battery-consumed percentage, distance, battery level and tyre-pressure figures the
dashboard and history charts read, stores each one's day-over-day change alongside it, and flags
the days whose battery maths does not add up.
## Requirements
### Requirement: No Cross-Module Database Access

The analytics capability SHALL own no database of its own and SHALL access telemetry and
manual charge data exclusively through those modules' public read ports — never through a
shared database connection, another module's generated query package, or any other bypass of
the module boundary.

#### Scenario: The capability owns no database

- **GIVEN** the analytics capability's implementation
- **WHEN** its data dependencies are inspected
- **THEN** it imports only the public `Reader` interface of `internal/telemetry` and the public
  `Reader` and `SuperchargerSessionAnalyticsReader` interfaces of `internal/charging` — never
  `internal/telemetry/db`, `internal/charging/db`, `internal/account`, or `internal/account/db`

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

### Requirement: Gap Detection on Corrected Daily Consumption

The analytics capability SHALL flag a day's corrected consumed percentage as a data gap
when the corrected value is negative, or when it is exactly zero while the vehicle's
distance travelled that day exceeds a fixed threshold. It SHALL NOT flag a day whose
corrected value is exactly zero when the distance travelled that day is at or below that
threshold. For a flagged day, the capability SHALL infer which charging source is
suspected missing: the Supercharger source when a Supercharger session matched to that
day exists with a NULL start or end battery percentage, and the manual source otherwise.

#### Scenario: A negative corrected value is flagged
- **GIVEN** a day whose corrected consumed percentage computes to a negative value
- **WHEN** the capability derives that day's result
- **THEN** the day is flagged
- **AND** the inferred missing-charging-source is Supercharger when a Supercharger
  session matched to that day has a NULL percentage, and manual otherwise

#### Scenario: A zero corrected value with meaningful distance is flagged
- **GIVEN** a day whose corrected consumed percentage computes to exactly zero
- **AND** the vehicle's distance travelled that day exceeds the platform's flag-distance
  threshold
- **WHEN** the capability derives that day's result
- **THEN** the day is flagged

#### Scenario: A zero corrected value with negligible distance is NOT flagged
- **GIVEN** a day whose corrected consumed percentage computes to exactly zero
- **AND** the vehicle's distance travelled that day is at or below the platform's
  flag-distance threshold
- **WHEN** the capability derives that day's result
- **THEN** the day is NOT flagged

### Requirement: A Day With No Usable Predecessor Is Skipped, Never Flagged

The analytics capability SHALL omit a calendar day from its per-day consumption result
entirely — never include it with a fabricated or zero-valued consumed percentage —
when that day's underlying telemetry snapshot has **no predecessor snapshot stored for
that vehicle at all** (the vehicle's first-ever capture). A predecessor that merely
falls outside the window a recalculation fetched SHALL NOT satisfy this condition: the
capability retrieves such a predecessor through the telemetry capability's
preceding-snapshot read port and computes the day normally.

#### Scenario: A vehicle's first-ever snapshot day is omitted, not flagged
- **GIVEN** a telemetry snapshot that is the vehicle's first-ever capture, with no
  predecessor snapshot stored anywhere for that vehicle
- **WHEN** the capability derives per-day consumption for a range including that day
- **THEN** that day does not appear in the result at all
- **AND** it is not counted as flagged

#### Scenario: A day whose predecessor is outside the fetched window is NOT skipped
- **GIVEN** a vehicle whose predecessor snapshot is seven calendar days before the day
  being derived, well outside the recalculation's own fetch window
- **WHEN** the capability derives per-day consumption for a range including that day
- **THEN** that day DOES appear in the result, with the true multi-day figures
- **AND** it is not treated as having no usable predecessor

### Requirement: Multi-Day Spans Are Represented By A Single Entry

The analytics capability SHALL represent a multi-day span — consecutive telemetry snapshots
more than one calendar day apart, caused by a missed poll — as exactly one result entry,
dated the later of the two snapshots' calendar days, whose corrected consumed percentage
sums every charge event matched anywhere within the span. The capability SHALL NOT
produce a result entry for any of the span's intervening calendar days.

#### Scenario: A three-day gap between polls yields exactly one entry
- **GIVEN** two consecutive telemetry snapshots three calendar days apart (a missed poll),
  with charge events matched at points throughout the span
- **WHEN** the capability derives per-day consumption for a range including that span
- **THEN** exactly one result entry is returned for the span, dated the later snapshot's
  calendar day
- **AND** every charge event matched within the span is summed into that single entry's
  corrected consumed percentage
- **AND** no result entry exists for either of the span's two intervening calendar days

### Requirement: Charge-to-Day Matching Is Source-Specific

The analytics capability SHALL match a Supercharger session to a day using the session's
stop instant against the interval between that day's telemetry capture instant and its
predecessor's capture instant (inclusive of the interval's start, exclusive of its end).
The capability SHALL match a manually-logged charge entry to a day using the entry's
logged calendar date, inclusive.

#### Scenario: A Supercharger session stopping exactly at the interval's start is matched
- **GIVEN** a day's underlying interval starting at instant `T0` (the predecessor
  snapshot's capture instant)
- **AND** a Supercharger session whose stop instant equals `T0` exactly
- **WHEN** the capability matches charge events to that day
- **THEN** that session is matched to the day

#### Scenario: A Supercharger session stopping exactly at the interval's end is NOT matched to that day
- **GIVEN** a day's underlying interval ending at instant `T1` (that day's own snapshot
  capture instant)
- **AND** a Supercharger session whose stop instant equals `T1` exactly
- **WHEN** the capability matches charge events to that day
- **THEN** that session is not matched to that day (it belongs to the following day's
  interval instead)

### Requirement: Multi-Tenant Scoping On Every Underlying Read

The analytics capability SHALL scope every underlying read it performs when deriving
per-day consumption (telemetry snapshot history, Supercharger sessions, and
manually-logged charge entries) to the given vehicle identifier alone. The capability
SHALL NOT take or require an account identifier for this derivation — a vehicle
belongs to exactly one account at a time, so scoping by vehicle identity already
gives the same isolation an account identifier would have added. Proving that a
specific account may see a specific vehicle's derived consumption SHALL happen before
this capability is called, not inside it.

#### Scenario: Every underlying read for per-day consumption is scoped to the given vehicle
- **GIVEN** a request to derive per-day consumption for a specific vehicle
- **WHEN** the capability performs its underlying reads
- **THEN** every one of those reads is scoped to that vehicle's identifier

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

The label each source's cursor is stored under is an implementation detail (which physical
table it names), not part of this requirement's contract — but retiring a label (because
the table it names was renamed) SHALL follow the same no-prior-cursor-means-full-backfill
rule as a vehicle's very first reconciliation: the capability SHALL treat a source whose
label was just retired as having no prior cursor, never as continuing from a value
recorded under the old label.

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

#### Scenario: Retiring a source's cursor label resets that source to a full backfill, once
- **GIVEN** a vehicle whose Supercharger-sessions cursor was stored under a label that
  named a table which has since been renamed
- **WHEN** the capability's underlying vocabulary for that label is migrated to the
  table's new name
- **THEN** the vehicle's existing cursor for that source is gone, exactly as if it had
  never been reconciled for that source before
- **AND** the vehicle's own precomputed daily metrics and flagged charge gaps for every
  other source are unaffected — only the retired source's cursor is reset
- **WHEN** the capability next reconciles that vehicle
- **THEN** it recomputes that source's entire history for that vehicle in one pass, under
  the new label, exactly as the "no prior cursor" scenario above already specifies for a
  vehicle's very first reconciliation

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

### Requirement: Analytics-Owned Derived Consumption Figures

The analytics capability SHALL itself derive, for each vehicle-day it recalculates,
five consumption figures from the raw observations the telemetry capability stores —
the odometer reading, the battery level, and the capture calendar day — of that day's
snapshot and its predecessor snapshot: the distance travelled in kilometres, the
battery percentage consumed, the number of whole calendar days the comparison spans,
and — only when the battery percentage consumed is **strictly greater than zero** —
the resulting kilometres-per-percent figure and the equivalent estimated full-charge
range in kilometres. The capability SHALL NOT read these figures from any other
capability's stored data.

Distance travelled and battery percentage consumed SHALL always be recorded as the
true, signed difference between the two observations, without averaging, clamping or
discarding a negative value. The two efficiency figures SHALL be absent whenever the
battery percentage consumed is zero or negative — zero is excluded exactly as a
negative value is, because neither yields a truthful ratio. All five figures SHALL be
absent when the vehicle has no predecessor snapshot at all.

#### Scenario: A normal drive day derives positive distance, positive battery used, and both efficiency figures
- **GIVEN** a vehicle whose predecessor snapshot reports an odometer of 1,000 km and a
  battery level of 80%
- **AND** whose next snapshot, one calendar day later, reports an odometer of 1,050 km
  and a battery level of 65%
- **WHEN** the capability recalculates that day
- **THEN** the recorded distance travelled is 50 km
- **AND** the recorded battery percentage used is 15
- **AND** the recorded days spanned is 1
- **AND** the recorded kilometres-per-percent figure is approximately 3.33
- **AND** the recorded estimated range is approximately 333 km

#### Scenario: A net-charge day records a negative battery-used figure and leaves both efficiency figures absent
- **GIVEN** a vehicle whose predecessor snapshot reports a battery level of 40% and
  whose next snapshot reports 85% (charged overnight)
- **WHEN** the capability recalculates that day
- **THEN** the recorded battery percentage used is −45, stored as observed and not
  clamped
- **AND** both efficiency figures are absent
- **AND** the recorded distance travelled still reflects the true odometer difference,
  unaffected by the negative battery-used figure

#### Scenario: A parked day with zero battery change leaves both efficiency figures absent but records zero as a value
- **GIVEN** a vehicle whose predecessor snapshot and next snapshot report an identical
  battery level and an identical odometer reading
- **WHEN** the capability recalculates that day
- **THEN** the recorded battery percentage used is 0 and the recorded distance
  travelled is 0 — both present, neither absent
- **AND** both efficiency figures are absent, because the divisor guard excludes zero
  exactly as it excludes a negative value
- **AND** the day still appears in the per-day consumption result, since a recorded
  zero is a value and not an absence

#### Scenario: A vehicle's first-ever snapshot day has all five figures absent
- **GIVEN** a vehicle with no snapshot preceding the day being recalculated
- **WHEN** the capability recalculates that day
- **THEN** all five figures are absent
- **AND** the day is still persisted with its raw battery level, odometer reading and
  range, and is recorded as not gap-flagged

### Requirement: The Predecessor Is The True Preceding Snapshot, However Old

The analytics capability SHALL derive each day's consumption figures against that
vehicle's **true** immediately-preceding stored snapshot, obtained through the
telemetry capability's preceding-snapshot read port, regardless of how many calendar
days separate the two. The capability SHALL NOT treat a predecessor as absent merely
because it falls outside the window the recalculation happened to fetch, and SHALL NOT
impose any maximum lookback of its own.

When the predecessor lies outside the recalculation's own fetch window, the capability
SHALL also extend the window over which it matches charge events (Supercharger
sessions and manually-logged entries) back to cover the whole span between the two
snapshots, so the day's corrected consumed percentage accounts for every charge that
occurred inside the gap.

A failure to look up the predecessor SHALL abort the recalculation with an error and
SHALL NOT be treated as "no predecessor exists" — otherwise a transient storage fault
would silently blank a real vehicle's figures.

#### Scenario: A seven-day capture gap yields one entry with the true multi-day totals
- **GIVEN** a vehicle whose snapshots are seven calendar days apart (the collector
  missed six nights), with an odometer difference of 210 km and a battery-level
  difference of 35 points across the gap
- **WHEN** the capability recalculates the later snapshot's day
- **THEN** the recorded distance travelled is 210 km — the true total across the gap,
  not a per-day average
- **AND** the recorded battery percentage used is 35
- **AND** the recorded days spanned is 7
- **AND** that day appears in both the per-day consumption result and the per-day
  distance result — it is not omitted

#### Scenario: A charge event inside the gap is counted
- **GIVEN** the same seven-day gap
- **AND** a manually-logged charge entry dated inside the gap that added 20 battery
  percentage points
- **WHEN** the capability recalculates the later snapshot's day
- **THEN** that day's corrected consumed percentage is 55 (the 35-point raw difference
  plus the 20 points charged), not 35
- **AND** the day is not gap-flagged

#### Scenario: A predecessor lookup failure is an error, never a blank result
- **GIVEN** a vehicle whose predecessor lookup fails for a transient storage reason
- **WHEN** the capability recalculates a day for that vehicle
- **THEN** the recalculation returns an error
- **AND** no row is persisted with the five figures blanked out

### Requirement: Derived Figures Reflect The Current Snapshot Readings, Not The Readings At Capture Time

The analytics capability SHALL derive each day's consumption figures from the
observations the two snapshots hold **at recalculation time**. When a snapshot is
replaced by a later same-day re-capture, the capability SHALL, on the next
recalculation covering the affected days, produce figures for the **following** day
that reflect the replacing observation — not the replaced one.

#### Scenario: Replacing a day's snapshot updates the next day's derived figures
- **GIVEN** a vehicle with snapshots on three consecutive calendar days, already
  recalculated
- **AND** the middle day's snapshot is then replaced by a same-day re-capture with a
  different odometer and battery reading
- **WHEN** the capability recalculates the affected day range
- **THEN** the third day's recorded distance travelled and battery percentage used are
  computed against the middle day's **replacing** observation
- **AND** they no longer reflect the replaced observation

### Requirement: Charge Gap Ledger

The analytics capability SHALL provide a durable ledger recording, per vehicle-day,
that a charge record is missing or incomplete — a signal the analytics capability's
own per-day consumption derivation computes. Each ledger row SHALL identify the
vehicle (its Tesla id and VIN), the flagged calendar day, and which charge source is
suspected missing for that day (a manual charge entry not captured by the vehicle API,
or a Supercharger session missing its battery-percentage readings). There SHALL be at
most one ledger row per vehicle/day: a day's shortfall is a single aggregate
observation and is never split across multiple rows for the same day.

The ledger SHALL expose a write operation that reconciles a caller-supplied set of
currently-flagged days against a caller-supplied date window for one vehicle: every
flagged day in the set SHALL be stored (inserted if new, or updated in place if its
suspected source changed since a prior reconciliation), and every previously-stored
day within that same window that is absent from the newly-supplied set SHALL be
removed from the ledger. The ledger SHALL NOT retain any record of a removed day —
there is no resolved/soft-deleted state, only present (still flagged) or absent (not
flagged, or never flagged). Days outside the reconciled window SHALL be unaffected by
a reconciliation call, regardless of their own flagged state. A reconciliation call
SHALL either fully apply (every insert, update, and removal it makes) or have no
effect at all.

The ledger SHALL reject a reconciliation call, without applying any part of it, if
any supplied flagged day does not belong to the call's own vehicle, or if any supplied
flagged day's date falls outside the call's own window.

#### Scenario: A newly-flagged day is stored
- **GIVEN** no ledger row exists for a given vehicle and day
- **WHEN** a reconciliation is performed for a window containing that day, with the
  day present in the flagged set and attributed to a specific suspected charge source
- **THEN** the ledger stores exactly one row for that vehicle and day
- **AND** the stored row's suspected charge source matches what was supplied

#### Scenario: Re-flagging the same day on a later reconciliation does not duplicate it
- **GIVEN** a ledger row already exists for a given vehicle and day
- **WHEN** a later reconciliation is performed for a window containing that day, with the
  day still present in the flagged set
- **THEN** the ledger still contains exactly one row for that vehicle and day
- **AND** the row's suspected charge source reflects the later reconciliation's value

#### Scenario: A day that stops flagging is removed from the ledger
- **GIVEN** a ledger row exists for a given vehicle and day, within some window
- **WHEN** a reconciliation is performed for that same window, with the day absent from the
  flagged set
- **THEN** the ledger no longer contains any row for that vehicle and day
- **AND** no trace of the removed day (such as a resolved marker) remains in the ledger

#### Scenario: An empty flagged set clears every previously-flagged day in the window
- **GIVEN** the ledger holds multiple flagged days for a vehicle within a window
- **WHEN** a reconciliation is performed for that window with an empty flagged set
- **THEN** the ledger contains no rows for that vehicle within that window afterward

#### Scenario: Reconciliation only affects the reconciled window
- **GIVEN** a ledger row exists for a vehicle on a day OUTSIDE a window about to be
  reconciled
- **WHEN** a reconciliation is performed for that window, regardless of what its flagged set
  contains
- **THEN** the row outside the window is unaffected — neither removed nor altered

#### Scenario: A reconciliation attempting to write another vehicle's day is rejected entirely
- **GIVEN** a reconciliation call scoped to one vehicle
- **WHEN** its flagged set contains an entry belonging to a different vehicle
- **THEN** the reconciliation is rejected
- **AND** no part of that call's flagged set is stored, including entries that were
  correctly scoped

#### Scenario: A reconciliation attempting to flag a day outside its own window is rejected entirely
- **GIVEN** a reconciliation call scoped to a specific window
- **WHEN** its flagged set contains an entry whose day falls outside that window
- **THEN** the reconciliation is rejected
- **AND** no part of that call's flagged set is stored

#### Scenario: Ledger rows for different vehicles are independent
- **GIVEN** two vehicles, each with a ledger row for the same calendar day
- **WHEN** a reconciliation is performed for one vehicle that removes its row for that day
- **THEN** the other vehicle's ledger row for the same day is unaffected

### Requirement: Reconciliation Cutoff Uses a UTC Calendar Day, Not the Platform Default Time Zone
The analytics capability SHALL bound its incremental reconciliation window so it never recomputes a day whose data may still be in progress, using a calendar day computed in UTC — never the platform's default time zone, and never a host process's own local time zone.

#### Scenario: Reconciliation never recomputes today or a future day
- **GIVEN** an incremental reconciliation pass observes changed data
- **WHEN** the pass computes the affected date range to recompute
- **THEN** the upper bound of that range is clamped to no later than the UTC calendar day before the current instant

#### Scenario: The reconciliation cutoff is computed independently of the platform default time zone
- **GIVEN** the platform's default time zone is `America/Bogota`
- **WHEN** the incremental reconciliation pass computes its cutoff day
- **THEN** the cutoff day is the same regardless of what the platform default time zone is currently set to

### Requirement: The Analytics Module Holds No Time-Zone Configuration Of Its Own
The analytics capability SHALL compute every calendar day it derives, stores, or compares using either a caller-supplied bare UTC-midnight date or UTC-bucketed arithmetic — it SHALL NOT introduce or consult any `*time.Location` of its own, and it SHALL NOT fall back to a configurable or host-local time zone anywhere in its write path.

#### Scenario: Per-day metric derivation buckets in UTC regardless of the platform default
- **GIVEN** a vehicle snapshot with a given captured date
- **WHEN** the analytics module derives the calendar day that snapshot's metrics belong to
- **THEN** the derived day is computed in UTC
- **AND** the result does not change when the platform's default time zone changes

#### Scenario: No configuration seam exists for overriding the module's bucketing zone
- **GIVEN** the analytics module's recalculation and reconciliation write paths
- **WHEN** those write paths are invoked with no time-zone-related configuration supplied
- **THEN** every calendar-day computation on those paths still succeeds using UTC bucketing
- **AND** no error or fallback branch related to a missing or invalid time zone is reachable, because none of this module's calendar-day computations accept a configurable zone

### Requirement: Precomputed Vehicle Status Observations

The analytics capability SHALL persist, on each precomputed daily metrics row, eight
additional raw vehicle-status observations copied verbatim from that day's telemetry
capture: whether the vehicle was locked, whether sentry mode was engaged, the
installed software version, interior and exterior temperature, the current charging
state, the configured charge limit, and the exact instant of capture. These eight
observations SHALL be populated from the day's own capture on EVERY row the
capability persists for that day, regardless of whether that day has a computable
predecessor — unlike the capability's derived per-day figures (distance travelled,
battery percentage used, and the corrected consumed-percentage figure), which remain
absent on a day with no computable predecessor.

#### Scenario: A normal day's row carries all eight status observations
- **GIVEN** a telemetry capture reporting the vehicle locked, sentry mode off, a
  software version, interior and exterior temperature, a charging state, a charge
  limit, and a capture instant
- **WHEN** the capability recalculates that day
- **THEN** the persisted row for that day carries all eight values exactly as
  reported

#### Scenario: A vehicle's first-ever day still carries all eight status observations
- **GIVEN** a telemetry capture that is a vehicle's first-ever capture, with no
  predecessor
- **WHEN** the capability recalculates that day
- **THEN** the persisted row for that day carries all eight status observations from
  that capture
- **AND** that row's derived distance-travelled, battery-percentage-used, and
  corrected consumed-percentage figures remain absent, exactly as they already are
  for a predecessor-less day

#### Scenario: Existing rows are never retroactively populated
- **GIVEN** a precomputed metrics row persisted before the capability began tracking
  these eight observations
- **WHEN** that row is read, without any new capture having triggered a
  recalculation of that same day since
- **THEN** all eight status observations on that row remain absent
- **AND** this absence is indistinguishable, for every observation except sentry
  mode, from "not yet computed" — no fabricated value is ever substituted

### Requirement: Latest Vehicle Status Per Account

The analytics capability SHALL expose, for a given set of vehicle
identifiers, the most recently computed status of each vehicle in that set —
combining the raw battery, range and odometer observations the capability
already persisted with the eight status observations, the four tyre-pressure
observations, the two travel-progress figures (distance travelled and
corrected consumed-percentage), the three travel-progress day-over-day deltas,
and the four tyre-pressure day-over-day deltas on that vehicle's most recently
computed day — as the capability's own domain result, never exposing another
module's capture-record type. For a given set of vehicles, the capability
SHALL return exactly one result per vehicle in the set, describing that
vehicle's own most-recently-computed day, never an older day for that vehicle
and never a result for a vehicle outside the given set. The capability SHALL
NOT take or require an account identifier for this read — proving that every
identifier in the given set belongs to the requesting account SHALL happen
before this capability is called, not inside it.

#### Scenario: A single vehicle's latest status is returned
- **GIVEN** a set containing one vehicle identifier, whose most recently
  computed day carries a full set of status, tyre-pressure, travel-progress,
  travel-progress-delta, and tyre-pressure-delta values
- **WHEN** the capability is asked for the latest status of that vehicle set
- **THEN** it returns exactly one result for that vehicle, carrying that
  day's battery, range, odometer, all eight status observations, all four
  tyre-pressure observations, both travel-progress figures, all three
  travel-progress deltas, and all four tyre-pressure deltas

#### Scenario: Each vehicle's own latest day is returned, not another vehicle's latest day
- **GIVEN** a set containing two vehicle identifiers, whose most recently
  computed days fall on two different calendar days
- **WHEN** the capability is asked for the latest status of that vehicle set
- **THEN** the result contains one entry per vehicle in the set, each
  carrying that specific vehicle's own most recently computed day — never
  one vehicle's entry describing the other vehicle's more recent day

#### Scenario: A pre-migration latest row reports absent travel-progress deltas, never fabricated ones
- **GIVEN** a vehicle whose most recently computed day predates the
  capability's travel-progress-delta tracking
- **WHEN** the capability is asked for the latest status of a set containing
  that vehicle
- **THEN** the returned result for that vehicle carries its existing battery,
  range, odometer, status, tyre-pressure, and travel-progress values
- **AND** all three travel-progress deltas on that result are absent, not a
  fabricated default such as `0`

#### Scenario: A predecessor-less latest day reports absent travel-progress and tyre-pressure deltas
- **GIVEN** a vehicle whose most recently computed day has no computable
  predecessor
- **WHEN** the capability is asked for the latest status of a set containing
  that vehicle
- **THEN** the returned result for that vehicle carries its raw battery,
  range, odometer, status, and tyre-pressure observations
- **AND** all three travel-progress deltas and all four tyre-pressure deltas
  on that result are absent, exactly as the two existing travel-progress
  figures already are for a predecessor-less day

#### Scenario: An empty vehicle set returns no results, not an error
- **GIVEN** an empty set of vehicle identifiers, or a set of vehicle
  identifiers with no precomputed metrics rows
- **WHEN** the capability is asked for the latest status of that vehicle set
- **THEN** it returns an empty result and no error

#### Scenario: A result never includes a vehicle outside the given set
- **GIVEN** precomputed metrics rows exist for a vehicle that is NOT included
  in the requested set
- **WHEN** the capability is asked for the latest status of the requested set
- **THEN** the result contains no entry for the vehicle outside the set

### Requirement: Per-Day Battery Level and Range Read

The analytics capability SHALL expose, for a given vehicle and closed calendar-day
date range, the vehicle's precomputed daily battery-level percentage and estimated
battery range for every day in that range for which a precomputed observation
exists. Unlike the capability's derived per-day figures (distance travelled, battery
percentage used, and the corrected consumed-percentage figure), a day's battery-level
and range observations SHALL be reported regardless of whether that day has a
computable predecessor, because they are raw per-day observations, not a delta
against a prior day. The capability SHALL NOT take or require an account identifier
for this read — it is scoped by vehicle identity alone.

#### Scenario: A day's battery level and range are returned exactly as observed

- **GIVEN** a precomputed daily observation for a vehicle reporting a battery level
  percentage and an estimated range for a given calendar day
- **WHEN** the capability is asked for that vehicle's battery level over a range
  containing that day
- **THEN** the result includes exactly one entry for that day, carrying the battery
  level percentage and range exactly as observed

#### Scenario: A day with no computable predecessor is still reported

- **GIVEN** a vehicle's first-ever tracked day, or any day immediately following a
  gap in tracking, for which the capability's derived distance-travelled and
  battery-percentage-used figures are absent because no predecessor day exists
- **WHEN** the capability is asked for that vehicle's battery level over a range
  containing that day
- **THEN** the result includes an entry for that day, carrying its battery level
  percentage and range
- **AND** this is true even though the same day is excluded from the capability's
  derived per-day consumption and distance results

#### Scenario: A day with no precomputed observation at all is absent, not zero

- **GIVEN** a calendar day within the requested range for which the capability has
  not yet computed any daily observation for the vehicle
- **WHEN** the capability is asked for that vehicle's battery level over a range
  containing that day
- **THEN** the result contains no entry for that day
- **AND** no fabricated or zero-valued entry is substituted for the missing day

#### Scenario: An empty range or a vehicle with no observations yields an empty result, not an error

- **GIVEN** a date range for which the vehicle has no precomputed observations at all
- **WHEN** the capability is asked for that vehicle's battery level over that range
- **THEN** it returns an empty result and no error

### Requirement: Module-Scoped Database Schema

The analytics module's `vehicle_metrics`, `vehicle_metric_watermarks`, and `charge_gaps` tables SHALL live in a PostgreSQL schema named `analytics`, distinct from the `public` schema and from
every other module's schema. This SHALL be a namespacing change only: it SHALL NOT alter any
stored data, any constraint (primary key, unique, or check), any index, or any behavior of the
module's public interface (`Reader`, `Recalculator`, `GapWriter`). No other module SHALL be
granted access to the `analytics` schema's tables — the module boundary
(`ai/architecture.md` §2, "no cross-module database leaks") is enforced identically before and
after this requirement, now additionally checkable at the database catalog level.

`vehicle_metric_watermarks.source`'s stored vocabulary (`'vehicle_snapshots'`,
`'charge_sessions'`, `'manual_charge_entries'`) and its CHECK constraint SHALL remain unchanged
by this requirement — those values are data naming other modules' tables by convention, not
schema-qualified references, and this schema move SHALL NOT alter, rewrite, or reinterpret them.

(The vocabulary listed above is the one in force when THIS requirement was written. It was
migrated afterwards, by `RM39-analytics-fix-watermark-vocabulary`: `'charge_sessions'` was
retired in favour of `'supercharger_sessions'` once `internal/charging` renamed the table it
names. That later change is governed by "Incremental Recompute Via An Analytics-Owned
Watermark" above; this requirement's own claim — that the *schema move* left the vocabulary
untouched — remains true and is deliberately not rewritten.)

**This requirement and its migration are historical and unaffected by
`RM61-analytics-remove-dead-efficiency-branch`.** Only the "public interface is unaffected"
scenario's example method list is corrected below, because it named `RecentEfficiency`, a
method that change deletes.

#### Scenario: The three tables resolve under the analytics schema
- **GIVEN** the analytics module's migrations have been applied
- **WHEN** the database catalog is queried for `analytics.vehicle_metrics`,
  `analytics.vehicle_metric_watermarks`, and `analytics.charge_gaps`
- **THEN** all three resolve to their table (a non-null relation)
- **AND** none of `public.vehicle_metrics`, `public.vehicle_metric_watermarks`, or
  `public.charge_gaps` resolves to a relation any longer

#### Scenario: Existing metrics, watermarks, and gap rows, constraints, and indexes survive the schema move
- **GIVEN** vehicle metrics, recompute watermarks, and flagged charge gaps already stored for
  one or more accounts
- **WHEN** the schema-move migration is applied
- **THEN** every row in all three tables is preserved unchanged
- **AND** the `UNIQUE (account_id, tesla_id, metric_date)` / `UNIQUE (account_id, tesla_id,
  source)` / `UNIQUE (account_id, tesla_id, gap_date)` constraints, every CHECK constraint
  (including `vehicle_metric_watermarks_source_check`, with its vocabulary byte-identical), and
  the `idx_vehicle_metrics_latest` / `idx_charge_gaps_account` indexes continue to be enforced
  exactly as before

#### Scenario: The module's public interface is unaffected by the schema move

- **GIVEN** a caller of the analytics module's public interface (e.g. `ConsumedByDay`,
  `OdometerDeltaByDay`, `BatteryLevelByDay`, `LatestMetricsForVehicles`, `Recalculate`,
  `Reconcile`, `ReconcileWindow`)
- **WHEN** the schema move is applied
- **THEN** every exported type name, method name, and method signature is unchanged
- **AND** the returned data is identical to what the same call returned before the move
- **AND** no caller (`internal/app`, `internal/gateway`) needs to change to keep working

#### Scenario: The watermark source vocabulary is unaffected by the schema move
- **GIVEN** `vehicle_metric_watermarks` rows whose `source` column holds `'vehicle_snapshots'`,
  `'charge_sessions'`, or `'manual_charge_entries'` (the vocabulary in force at the time of the
  schema move; `'charge_sessions'` was retired later — see this requirement's note above)
- **WHEN** the schema-move migration is applied
- **THEN** every row's `source` value is byte-identical to its pre-migration value
- **AND** the `vehicle_metric_watermarks_source_check` constraint still accepts exactly the same
  three values and rejects every other value, unchanged

### Requirement: Precomputed Tyre Pressure Observations

The analytics capability SHALL persist, on each precomputed daily metrics row, four
additional raw tyre-pressure observations copied verbatim from that day's telemetry
capture — one value per wheel (front-left, front-right, rear-left, rear-right), in
PSI. These four observations SHALL be populated from the day's own capture on EVERY
row the capability persists for that day, regardless of whether that day has a
computable predecessor — the same rule the capability's other raw status observations
already follow, and the opposite rule to the capability's derived per-day figures
(distance travelled, battery percentage used, the corrected consumed-percentage
figure), which remain absent on a day with no computable predecessor.

#### Scenario: A normal day's row carries all four tyre-pressure observations
- **GIVEN** a telemetry capture reporting a pressure reading for all four wheels
- **WHEN** the capability recalculates that day
- **THEN** the persisted row for that day carries all four values exactly as
  reported, in PSI, with no conversion applied

#### Scenario: A vehicle's first-ever day still carries all four tyre-pressure observations
- **GIVEN** a telemetry capture that is a vehicle's first-ever capture, with no
  predecessor
- **WHEN** the capability recalculates that day
- **THEN** the persisted row for that day carries all four tyre-pressure observations
  from that capture
- **AND** that row's derived distance-travelled, battery-percentage-used, and
  corrected consumed-percentage figures remain absent, exactly as they already are
  for a predecessor-less day

#### Scenario: A wheel with no reported pressure is absent, not a fabricated reading
- **GIVEN** a telemetry capture whose reading for one wheel is absent (no sensor, or
  no report from the vehicle for that wheel at capture time)
- **WHEN** the capability recalculates that day
- **THEN** the persisted row's observation for that wheel is absent
- **AND** the other three wheels' observations, if reported, are persisted exactly as
  reported

#### Scenario: Existing rows gain these observations only through the one-time backfill
- **GIVEN** a precomputed metrics row persisted before the capability began tracking
  these four observations
- **WHEN** that row is read, without either a new capture triggering a
  recalculation of that same day, or the platform's one-time historical backfill
  having run
- **THEN** all four tyre-pressure observations on that row remain absent
- **AND** this absence is never replaced with a fabricated reading

### Requirement: Precomputed Tyre Pressure Day-Over-Day Deltas

The analytics capability SHALL persist, on each precomputed daily metrics row, four
derived tyre-pressure deltas — one per wheel (front-left, front-right, rear-left,
rear-right), in PSI — each computed as that day's raw tyre-pressure observation for that
wheel minus the immediately preceding day's raw observation for the same wheel and the
same vehicle. A delta SHALL be absent whenever either day's own raw observation for that
wheel is absent, or whenever the day itself has no computable predecessor — the same
absence rule the capability's other derived per-day figures (distance travelled, battery
percentage used) already follow. This delta is expected to partially reflect ambient air
temperature change alongside any genuine pressure change; that is an accepted property of
the reading, not a defect, and the capability SHALL NOT apply a threshold or a
target-pressure comparison to suppress it.

#### Scenario: A normal day with a predecessor gets all four wheel deltas
- **GIVEN** two consecutive days' telemetry captures for the same vehicle, both
  reporting all four wheels' tyre pressure
- **WHEN** the capability recalculates the second day
- **THEN** the persisted row for the second day carries, for each wheel, that day's
  pressure minus the first day's pressure for the same wheel

#### Scenario: A predecessor-less day has all four deltas absent
- **GIVEN** a telemetry capture that is a vehicle's first-ever capture, with no
  predecessor
- **WHEN** the capability recalculates that day
- **THEN** the persisted row's four tyre-pressure deltas are absent, exactly as its
  other derived per-day figures already are for a predecessor-less day

#### Scenario: A wheel missing on either day yields an absent delta for that wheel only
- **GIVEN** two consecutive days' telemetry captures for the same vehicle, where one
  wheel's pressure is absent on one of the two days
- **WHEN** the capability recalculates the second day
- **THEN** the persisted delta for that one wheel is absent
- **AND** the other three wheels' deltas, when both days report them, are computed
  normally

#### Scenario: A delta is never fabricated as zero
- **GIVEN** a day whose predecessor day, or whose own capture, lacks a wheel's raw
  pressure reading
- **WHEN** the capability recalculates that day
- **THEN** that wheel's persisted delta is absent
- **AND** it is never replaced with a fabricated `0`, even though `0` is itself a
  possible genuine reading for a different day

#### Scenario: Existing rows gain these deltas only through the one-time backfill
- **GIVEN** a precomputed metrics row persisted before the capability began tracking
  these four deltas
- **WHEN** that row is read, without either a new capture triggering a recalculation
  of that same day, or the platform's one-time historical backfill having run
- **THEN** all four tyre-pressure deltas on that row remain absent
- **AND** this absence is never replaced with a fabricated reading

### Requirement: Nightly-Path Query Logging

The analytics capability SHALL log, for every call made through its precomputed-metrics
write path, its charge-gap write path, and the one read method the nightly cycle uses,
the call's identifying and date-filtering arguments, and — for the read method — how
many records were returned and how many of them were flagged.

This closes an observability gap: without it, which date range the nightly cycle passed
into a given analytics call cannot be determined from a successful run's log output.

#### Scenario: A metrics recalculation logs its vehicle and window
- **GIVEN** a call to recalculate one vehicle's precomputed metrics over a specific date
  window
- **WHEN** the call is made
- **THEN** a log line records the vehicle identity and the window boundaries supplied

#### Scenario: A metrics reconciliation logs its vehicle
- **GIVEN** a call to reconcile one vehicle's precomputed metrics against its stored
  watermarks
- **WHEN** the call is made
- **THEN** a log line records the vehicle identity

#### Scenario: A per-day consumption read logs its vehicle, window, and flagged count
- **GIVEN** a call to read one vehicle's per-day corrected battery-consumed figures over
  a specific date window
- **WHEN** the call completes
- **THEN** a log line records the vehicle identity, the window boundaries supplied, the
  number of days returned, and how many of those days were flagged

#### Scenario: A charge-gap reconciliation logs its vehicle, window, and flagged count
- **GIVEN** a call to reconcile one vehicle's charge-gap ledger against a freshly
  computed set of flagged days for a specific date window
- **WHEN** the call is made
- **THEN** a log line records the vehicle identity, the window boundaries supplied, and
  the number of flagged days supplied

### Requirement: Dashboard-Only Reads Are Not Logged

The analytics capability SHALL NOT log calls to a `Reader` method that has no caller on
the nightly cycle — a method reached only from a live dashboard or history request.

This keeps the added logging scoped to the nightly cycle's own path, so no page load
served by the gateway gains a new log line from this capability.

#### Scenario: A dashboard-only read produces no new log line
- **GIVEN** a call to a `Reader` method other than the per-day consumption read
- **WHEN** the call completes
- **THEN** no log line attributable to this capability is produced for that call

### Requirement: The Nightly Manual-Entry Read Is Logged By Its Caller

The analytics capability SHALL log, when it reads a vehicle's manual charge entries for
a date window while recomputing that vehicle's precomputed metrics, the vehicle
identity, the window boundaries it supplied, and how many entries came back.

The charging capability cannot log this read selectively. Its manual-entry read port is
built by one shared constructor, so logging the read there would also log it for the two
gateway callers that run on every page render and every form submission. Logging it from
this capability, at the one place this capability makes the call, records the nightly
read and leaves those gateway reads silent.

#### Scenario: A metrics recalculation logs the manual entries it read

- **GIVEN** a call to recalculate one vehicle's precomputed metrics over a date window
- **WHEN** the manual charge entries for that window have been read
- **THEN** a log line records the vehicle identity, the window boundaries supplied, and
  the number of entries returned

#### Scenario: A gateway manual-entry read is not logged by this capability

- **GIVEN** a manual-entry read made by a gateway page render or form submission,
  without recalculating any vehicle's precomputed metrics
- **WHEN** the read completes
- **THEN** no log line is produced by this capability for that read

### Requirement: Precomputed Travel-Progress Day-Over-Day Deltas

The analytics capability SHALL persist, on each precomputed daily metrics row,
three derived day-over-day deltas: the change in distance travelled, the
change in the corrected consumed-percentage figure, and the change in the
kilometres-per-percent efficiency figure — each computed as that day's own
value of the underlying figure minus the value the capability most recently
computed for the same vehicle within the same recalculation pass. A delta
SHALL be absent whenever either day's own underlying figure is absent, or
whenever the day being recalculated is the first day considered within its
recalculation pass, since no earlier day's value is available for comparison
within that pass. A recalculation pass that later includes an earlier day
alongside it SHALL produce that day's delta normally.

#### Scenario: A normal day with a predecessor in the same pass gets all three deltas
- **GIVEN** two consecutive days' precomputed rows for the same vehicle, both
  already carrying their own distance-travelled, consumed-percentage, and
  efficiency figures
- **AND** the capability recalculates both days together, in calendar order,
  within the same pass
- **WHEN** the capability recalculates the second day
- **THEN** the persisted row for the second day carries the change in each of
  the three figures, computed as that day's value minus the first day's value

#### Scenario: The first day considered in a recalculation pass has all three deltas absent
- **GIVEN** a vehicle whose stored history already includes a day immediately
  before the day now being recalculated
- **WHEN** a recalculation pass considers only the later day, without the
  earlier day also being part of that same pass
- **THEN** the persisted row for the later day carries its own
  distance-travelled, consumed-percentage, and efficiency figures normally
- **AND** all three of its day-over-day deltas are absent for this pass, even
  though the earlier day exists in storage
- **AND** a later recalculation pass that considers both days together
  produces the later day's deltas normally

#### Scenario: A predecessor-less day has all three deltas absent
- **GIVEN** a telemetry capture that is a vehicle's first-ever capture, with no
  predecessor
- **WHEN** the capability recalculates that day
- **THEN** the persisted row's three day-over-day deltas are absent, exactly
  as the capability's other derived per-day figures already are for a
  predecessor-less day

#### Scenario: A day whose predecessor lacks the underlying figure yields an absent delta
- **GIVEN** two consecutive days' precomputed rows for the same vehicle, where
  the earlier day is itself a predecessor-less day and so carries no
  distance-travelled, consumed-percentage, or efficiency figure of its own
- **WHEN** the capability recalculates the later day within the same pass as
  the earlier one
- **THEN** all three of the later day's day-over-day deltas are absent
- **AND** none is replaced with a fabricated value derived from the later
  day's own figure alone

#### Scenario: A delta is never fabricated as zero
- **GIVEN** a day whose own underlying figure, or whose comparison day's
  underlying figure, is absent
- **WHEN** the capability recalculates that day
- **THEN** the corresponding day-over-day delta is absent
- **AND** it is never replaced with a fabricated `0`, even though `0` is
  itself a possible genuine value for a day that truly had no change

#### Scenario: Existing rows gain these deltas only through the one-time backfill
- **GIVEN** a precomputed metrics row persisted before the capability began
  tracking these three deltas
- **WHEN** that row is read, without either a new capture triggering a
  recalculation of that same day, or the platform's one-time historical
  backfill having run
- **THEN** all three day-over-day deltas on that row remain absent
- **AND** this absence is never replaced with a fabricated reading

