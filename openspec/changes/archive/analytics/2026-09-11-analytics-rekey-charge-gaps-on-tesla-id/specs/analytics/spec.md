## MODIFIED Requirements

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
