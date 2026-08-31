## ADDED Requirements

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
