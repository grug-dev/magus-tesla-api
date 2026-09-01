# analytics Specification

## Purpose
Derived analytics over stored telemetry — the platform's metrics layer, sitting between
what `telemetry` captures and what the dashboard renders. It owns no database and no capture: it
reads sibling modules' public ports and computes values none of them store. Its first metric is
rolling energy-per-kilometre (Wh/km).
## Requirements
### Requirement: Recent Energy-Per-Kilometre Derivation
The analytics capability SHALL derive a rolling energy-per-kilometre (Wh/km) value for a given
vehicle over a fixed window (default 30 days, fixed at construction time), from that vehicle's
stored telemetry snapshots plus its Supercharger sessions and manually-logged charge entries
within the window. The capability SHALL consume the distance values in the unit the telemetry read
port already provides them in, and SHALL NOT perform any unit conversion of its own. The
capability SHALL compute the energy numerator as measured charging energy (kWh) minus a
pack-capacity correction for net SoC drift over the window, using whichever of
`UsableBatteryLevel` or `BatteryLevel` is consistently available at BOTH window endpoints (never a
mixed pair). The capability SHALL scope every underlying read to the given account, even when the
vehicle identifier alone would suffice, as defense-in-depth tenant isolation.

#### Scenario: A vehicle with two or more snapshots, known capacity, and net consumption gets a computed value
- **GIVEN** a vehicle with at least two telemetry snapshots inside the window, whose Fleet API
  `car_type` is present in the platform's pack-capacity reference table
- **AND** the vehicle's net battery state-of-charge decreased over the window (it consumed more
  than it charged)
- **WHEN** the capability derives recent efficiency for that vehicle
- **THEN** it returns a Wh/km value derived from measured charging energy corrected by the
  pack-capacity SoC-drift term, `ok=true`, and `Approximate=false`

#### Scenario: Distance is read, not converted
- **GIVEN** a vehicle with at least two telemetry snapshots inside the window
- **WHEN** the capability computes the distance travelled over the window
- **THEN** it takes the kilometre values directly from the snapshots returned by the telemetry
  read port
- **AND** it applies no unit-conversion factor and calls no conversion method to obtain them
- **AND** the derived Wh/km value is the same as it was when the conversion was performed on read

#### Scenario: Usable battery level is used only when present at both window endpoints
- **GIVEN** a vehicle whose first snapshot in the window has a non-nil `UsableBatteryLevel` and
  whose last snapshot in the window has a nil `UsableBatteryLevel`
- **WHEN** the capability computes the state-of-charge delta for that window
- **THEN** it uses `BatteryLevel` at BOTH endpoints (never usable at one endpoint and nominal at
  the other)

#### Scenario: Usable battery level is used when present at both window endpoints
- **GIVEN** a vehicle whose first and last snapshots in the window both have a non-nil
  `UsableBatteryLevel`
- **WHEN** the capability computes the state-of-charge delta for that window
- **THEN** it uses `UsableBatteryLevel` at both endpoints

### Requirement: Unknown Pack Capacity Yields an Approximate Value, Never a Blank Tile
The analytics capability SHALL NOT refuse to return an efficiency value solely because the
vehicle's pack capacity is unknown (its `car_type` is absent, or not present in the
pack-capacity reference table). In that case the capability SHALL compute energy from measured
charging energy alone (no SoC-drift correction) and SHALL mark the result `Approximate=true`.

#### Scenario: Unknown car_type still returns a value, marked approximate
- **GIVEN** a vehicle whose registered `CarType` is nil, or whose `CarType` is not present in the
  pack-capacity reference table
- **AND** the vehicle otherwise has enough snapshots and positive net consumption to compute a
  value
- **WHEN** the capability derives recent efficiency for that vehicle
- **THEN** it returns a Wh/km value computed from measured charging energy without any
  capacity correction, `ok=true`, and `Approximate=true`

#### Scenario: Known car_type applies the capacity correction and is not marked approximate
- **GIVEN** a vehicle whose registered `CarType` is present in the pack-capacity reference table
- **WHEN** the capability derives recent efficiency for that vehicle
- **THEN** the returned value's `Approximate` field is `false`

### Requirement: Insufficient Data Returns ok=false, Never a Fabricated Value
The analytics capability SHALL return `ok=false` with no error — never a fabricated or
divide-by-near-zero value — in each of these cases: fewer than two telemetry snapshots exist in
the window; the vehicle's odometer reading did not increase over the window (parked, or a
non-increasing reading); or the derived energy consumed is zero or negative (net charge over the
window exceeded consumption).

#### Scenario: Fewer than two snapshots in the window
- **GIVEN** a vehicle with zero or exactly one telemetry snapshot inside the window
- **WHEN** the capability derives recent efficiency for that vehicle
- **THEN** it returns `ok=false` and no error

#### Scenario: No distance moved over the window
- **GIVEN** a vehicle with at least two snapshots in the window whose odometer reading at the
  window's end is not greater than its odometer reading at the window's start
- **WHEN** the capability derives recent efficiency for that vehicle
- **THEN** it returns `ok=false` and no error

#### Scenario: Net charge exceeds consumption over the window
- **GIVEN** a vehicle with at least two snapshots and positive distance moved over the window
- **AND** the derived energy consumed (measured charging energy minus the SoC-drift correction,
  or measured charging energy alone when capacity is unknown) is zero or negative
- **WHEN** the capability derives recent efficiency for that vehicle
- **THEN** it returns `ok=false` and no error

### Requirement: Multi-Tenant Scoping on Every Underlying Read
The analytics capability SHALL pass the given account identifier to every underlying port call it
makes (telemetry snapshot history, Supercharger sessions, manually-logged charge entries, and
vehicle registration lookup), so that a caller can never retrieve another account's data by
supplying a vehicle identifier alone.

#### Scenario: Every underlying read is scoped to the given account
- **GIVEN** a request to derive recent efficiency for a specific account and vehicle
- **WHEN** the capability performs its underlying reads
- **THEN** every one of those reads is scoped to the given account identifier

### Requirement: No Cross-Module Database Access
The analytics capability SHALL own no database of its own and SHALL access telemetry, manual
charge, and account data exclusively through those modules' public read ports — never through a
shared database connection, another module's generated query package, or any other bypass of the
module boundary.

#### Scenario: The capability owns no database
- **GIVEN** the analytics capability's implementation
- **WHEN** its data dependencies are inspected
- **THEN** it imports only the public `Reader` interface of `internal/telemetry`, the public
  `Reader` and `SuperchargerSessionAnalyticsReader` interfaces of `internal/charging`, and the
  public `Service` interface of `internal/account` — never `internal/telemetry/db`,
  `internal/charging/db`, or `internal/account/db`

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

The analytics capability SHALL pass the given account identifier to every underlying read
it performs when deriving per-day consumption (telemetry snapshot history, Supercharger
sessions, and manually-logged charge entries), so that a caller can never retrieve another
account's data by supplying a vehicle identifier alone.

#### Scenario: Every underlying read for per-day consumption is scoped to the given account
- **GIVEN** a request to derive per-day consumption for a specific account and vehicle
- **WHEN** the capability performs its underlying reads
- **THEN** every one of those reads is scoped to the given account identifier

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
owning account, the vehicle (its Tesla id and VIN), the flagged calendar day, and
which charge source is suspected missing for that day (a manual charge entry not
captured by the vehicle API, or a Supercharger session missing its
battery-percentage readings). There SHALL be at most one ledger row per
account/vehicle/day: a day's shortfall is a single aggregate observation and is
never split across multiple rows for the same day.

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
any supplied flagged day does not belong to the call's own account and vehicle, or if
any supplied flagged day's date falls outside the call's own window.

#### Scenario: A newly-flagged day is stored
- **GIVEN** no ledger row exists for a given account, vehicle, and day
- **WHEN** a reconciliation is performed for a window containing that day, with the
  day present in the flagged set and attributed to a specific suspected charge source
- **THEN** the ledger stores exactly one row for that account, vehicle, and day
- **AND** the stored row's suspected charge source matches what was supplied

#### Scenario: Re-flagging the same day on a later reconciliation does not duplicate it
- **GIVEN** a ledger row already exists for a given account, vehicle, and day
- **WHEN** a later reconciliation is performed for a window containing that day, with the
  day still present in the flagged set
- **THEN** the ledger still contains exactly one row for that account, vehicle, and day
- **AND** the row's suspected charge source reflects the later reconciliation's value

#### Scenario: A day that stops flagging is removed from the ledger
- **GIVEN** a ledger row exists for a given account, vehicle, and day, within some window
- **WHEN** a reconciliation is performed for that same window, with the day absent from the
  flagged set
- **THEN** the ledger no longer contains any row for that account, vehicle, and day
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

#### Scenario: A reconciliation attempting to write another account or vehicle's day is rejected entirely
- **GIVEN** a reconciliation call scoped to one account and vehicle
- **WHEN** its flagged set contains an entry belonging to a different account or a different
  vehicle
- **THEN** the reconciliation is rejected
- **AND** no part of that call's flagged set is stored, including entries that were
  correctly scoped

#### Scenario: A reconciliation attempting to flag a day outside its own window is rejected entirely
- **GIVEN** a reconciliation call scoped to a specific window
- **WHEN** its flagged set contains an entry whose day falls outside that window
- **THEN** the reconciliation is rejected
- **AND** no part of that call's flagged set is stored

#### Scenario: Ledger rows for different vehicles are independent
- **GIVEN** two vehicles belonging to the same account, each with a ledger row for the same
  calendar day
- **WHEN** a reconciliation is performed for one vehicle that removes its row for that day
- **THEN** the other vehicle's ledger row for the same day is unaffected

#### Scenario: Ledger rows for different accounts are independent
- **GIVEN** two accounts, each with a vehicle carrying a ledger row for the same calendar
  day
- **WHEN** a reconciliation is performed for one account's vehicle
- **THEN** the other account's ledger row is unaffected, regardless of overlapping days or
  vehicle identifiers

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

The analytics capability SHALL expose, for a given account, the most recently
computed status of every vehicle registered to that account — combining the raw
battery, range and odometer observations the capability already persisted with the
eight status observations above — as the capability's own domain result, never
exposing another module's capture-record type. For an account with multiple
vehicles, the capability SHALL return exactly one result per vehicle, describing that
vehicle's own most-recently-computed day, never an older day for that vehicle and
never a result attributable to a different account's vehicle.

#### Scenario: A single vehicle's latest status is returned
- **GIVEN** an account with one registered vehicle, whose most recently computed day
  carries a full set of status observations
- **WHEN** the capability is asked for that account's latest vehicle status
- **THEN** it returns exactly one result for that vehicle, carrying that day's
  battery, range, odometer and all eight status observations

#### Scenario: Each vehicle's own latest day is returned, not the account's latest day overall
- **GIVEN** an account with two registered vehicles, whose most recently computed
  days fall on two different calendar days
- **WHEN** the capability is asked for that account's latest vehicle status
- **THEN** the result contains one entry per vehicle, each carrying that specific
  vehicle's own most recently computed day — never one vehicle's entry describing
  the other vehicle's more recent day

#### Scenario: A pre-migration latest row reports absent status observations, never fabricated ones
- **GIVEN** a vehicle whose most recently computed day predates the capability's
  status-observation tracking
- **WHEN** the capability is asked for that account's latest vehicle status
- **THEN** the returned result for that vehicle carries its existing battery, range
  and odometer values
- **AND** all eight status observations on that result are absent, not a fabricated
  default such as "unlocked" or "sentry off"

#### Scenario: An account with no computed vehicles yet returns no results, not an error
- **GIVEN** an account with no precomputed metrics rows for any vehicle
- **WHEN** the capability is asked for that account's latest vehicle status
- **THEN** it returns an empty result and no error

