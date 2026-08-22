## REMOVED Requirements

### Requirement: Charge Gap Ledger

**Reason**: MAG-26 / roadmap `RM29-modular-monolith-boundaries` tier 5
(`RM29-analytics-own-charge-gaps`). This ledger records a conclusion the analytics
(derived-metrics) capability reaches — that a vehicle-day's battery math does not add
up against stored charge records — but **no part of the telemetry capability ever
computed or read that conclusion**. Tier 4 already moved the derivation feeding this
conclusion (the five per-day consumption figures) into analytics; this ledger was the
one remaining piece of the same domain still stored by telemetry, on behalf of
another capability, exactly the boundary blur MAG-26 exists to remove.

**Migration**: Every behavior this requirement specified is preserved, verbatim and
unchanged, by the analytics capability's new `Requirement: Charge Gap Ledger` (same
title — an ownership move, not a redesign). Same schema, same one-row-per-vehicle-day
rule, same reconcile-a-window semantics (upsert every currently-flagged day, delete
every previously-stored day in the window no longer flagged), same
no-resolved-at/no-soft-delete rule, same tenant-isolation and out-of-window rejection
behavior, same all-or-nothing transaction guarantee. Every scenario below moves with
identical GIVEN/WHEN/THEN wording to the analytics capability's delta.

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
