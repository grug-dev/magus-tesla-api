## ADDED Requirements

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

## MODIFIED Requirements

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
