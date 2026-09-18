## ADDED Requirements

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

## MODIFIED Requirements

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
