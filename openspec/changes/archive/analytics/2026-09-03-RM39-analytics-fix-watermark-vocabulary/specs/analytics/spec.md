## MODIFIED Requirements

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
