## ADDED Requirements

### Requirement: Derived Consumption Metrics

The telemetry capability SHALL, when capturing a nightly snapshot for a vehicle, compute
and store five derived consumption values against that vehicle's previous stored snapshot
(the row for the same account and vehicle with the greatest capture time strictly before
the start of the incoming snapshot's local calendar day): the distance traveled in
kilometres, the battery percentage consumed, the number of calendar days the comparison
spans, and — only when the battery percentage consumed is strictly greater than zero — the
resulting kilometres-per-percent efficiency and the equivalent estimated full-charge range
in kilometres. Distance traveled and battery percentage consumed SHALL always be stored as
the true, signed difference between the two records, without averaging, clamping, or
discarding a negative value. The two efficiency values SHALL be NULL whenever the battery
percentage consumed is zero or negative, and SHALL also be NULL, along with the distance
traveled, battery percentage consumed, and days-spanned values, when the vehicle has no
prior stored snapshot. A snapshot that replaces an existing same-day snapshot (a same-day
re-capture) SHALL have all five derived values recomputed against the correct predecessor
and refreshed, not left at their previously stored values. The capability SHALL also
compute these five values for every snapshot stored before this capability existed, in the
same schema change that introduces the columns, producing the same results the ongoing
capture-time computation would produce for the same pair of consecutive records.

#### Scenario: A normal drive day computes positive distance, positive battery used, and both efficiency values

- **GIVEN** a vehicle with a previously stored snapshot reporting an odometer of 42,350 km
  and a battery level of 82%
- **AND** its next nightly snapshot reports an odometer of 42,390 km and a battery level of
  70%, captured the following calendar day
- **WHEN** the new snapshot is captured and stored
- **THEN** the stored distance traveled is 40 km
- **AND** the stored battery used is 12%
- **AND** the stored days spanned is 1
- **AND** the stored km-per-percent value is approximately 3.33
- **AND** the stored estimated range is approximately 333 km

#### Scenario: A charging day stores a negative battery-used value and leaves both efficiency values NULL

- **GIVEN** a vehicle with a previously stored snapshot reporting a battery level of 60%
- **AND** its next nightly snapshot reports a battery level of 75% (the vehicle was charged
  overnight, net battery increased) and some non-negative distance traveled
- **WHEN** the new snapshot is captured and stored
- **THEN** the stored battery used is -15 (a negative value, stored as reported, not clamped)
- **AND** the stored km-per-percent value is NULL
- **AND** the stored estimated range value is NULL
- **AND** the stored distance traveled reflects the true odometer difference, unaffected by
  the negative battery-used value

#### Scenario: A parked day with zero battery change leaves both efficiency values NULL

- **GIVEN** a vehicle with a previously stored snapshot and a next nightly snapshot whose
  battery level is identical to the previous snapshot's battery level
- **WHEN** the new snapshot is captured and stored
- **THEN** the stored battery used is 0
- **AND** the stored km-per-percent value is NULL
- **AND** the stored estimated range value is NULL
- **AND** the stored distance traveled is still computed and stored normally (it may be
  zero or non-zero independent of the battery-used value)

#### Scenario: A multi-day gap stores the true multi-day delta and the number of days it spans

- **GIVEN** a vehicle whose previous stored snapshot is from two calendar days before the
  next nightly capture (the poller missed an intervening night)
- **AND** the vehicle traveled 80 km and consumed 20% battery across that whole gap
- **WHEN** the new snapshot is captured and stored
- **THEN** the stored distance traveled is 80 km (the true total across the gap, not
  averaged into a per-day figure)
- **AND** the stored battery used is 20%
- **AND** the stored days spanned is 2
- **AND** the stored km-per-percent and estimated range values are computed from the full
  80 km / 20% figures, not from any per-day approximation

#### Scenario: The first-ever snapshot of a vehicle has all five derived values NULL

- **GIVEN** a vehicle with no previously stored snapshot
- **WHEN** its first nightly snapshot is captured and stored
- **THEN** all five derived values (distance traveled, battery used, days spanned,
  km-per-percent, estimated range) are NULL on the stored snapshot
- **AND** this is not treated as a capture failure — the snapshot is stored successfully
  and the attempt is recorded as a success

#### Scenario: A same-day re-capture recomputes and refreshes all five derived values

- **GIVEN** a vehicle with a stored snapshot for calendar day N (itself computed against a
  predecessor from day N-1) and a stored snapshot for day N-1 before that
- **WHEN** a second capture for day N runs later the same day, replacing day N's stored
  snapshot with different odometer and battery readings
- **THEN** the replaced day-N row's five derived values are recomputed against the day N-1
  predecessor (not against the first day-N capture that was just replaced)
- **AND** the stored derived values reflect the second capture's odometer and battery
  readings, not the first capture's

#### Scenario: Existing history is backfilled when the capability is introduced

- **GIVEN** a sequence of previously stored snapshots for a vehicle across several
  consecutive calendar days, captured before this capability existed
- **WHEN** the schema change that introduces the five derived columns is applied
- **THEN** every row except the vehicle's oldest stored snapshot has all five derived
  values populated
- **AND** each populated row's values equal what capture-time computation would produce for
  that row and its immediate predecessor
- **AND** the vehicle's oldest stored snapshot has all five derived values NULL (it has no
  predecessor)
