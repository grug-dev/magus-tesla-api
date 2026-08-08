## ADDED Requirements

### Requirement: Snapshot Effective Date

The `Snapshot` domain type SHALL carry an `EffectiveDate time.Time` field representing the
calendar day the nightly snapshot **describes** — computed as `CapturedAt.AddDate(0, 0, -1)`
(calendar-day arithmetic, not a 24-hour duration, so DST does not shift it). `EffectiveDate` is
populated exactly once, in the single DB→domain mapper (`rowToSnapshot`), so both
`LatestSnapshotsByAccount` and `SnapshotsByVehicleSince` return `Snapshot`s that carry it without
any per-method duplication. `EffectiveDate` is a **full `time.Time`** preserving the
time-of-day component (same type and shape as `CapturedAt`); it is NOT a pre-formatted display
string — formatting (e.g. MM-DD) is a caller concern.

`EffectiveDate` is a read-derived field. There is NO matching database column and NO migration: it
is computed from `CapturedAt` at the read boundary and never persisted. The write path
(`insertSnapshot` / the poller) does not set `EffectiveDate`; a write-built `Snapshot` carries the
zero `time.Time` value, which is never read before storage. `CapturedAt` remains the authoritative
"when we read it"; `CapturedDate` remains the dedupe calendar day (capture's own date). `EffectiveDate`
is a distinct, separate concept from both: the day the row *represents*.

#### Scenario: EffectiveDate is one calendar day before CapturedAt on every returned Snapshot

- **GIVEN** a stored `vehicle_snapshots` row whose `captured_at` is `2026-08-08 03:30:00 UTC`
- **WHEN** a caller retrieves it through `LatestSnapshotsByAccount` or `SnapshotsByVehicleSince`
- **THEN** the returned `Snapshot.EffectiveDate` equals `2026-08-07 03:30:00 UTC`
- **AND** the field carries the full `time.Time` (year, month, day, and time-of-day), not a
  pre-formatted string
- **AND** `Snapshot.CapturedAt` is unchanged (`2026-08-08 03:30:00 UTC`)

#### Scenario: Both read methods populate EffectiveDate from the single mapper

- **GIVEN** a `telemetry.Reader` implementation backed by the production store
- **WHEN** `LatestSnapshotsByAccount(ctx, accountID)` and `SnapshotsByVehicleSince(ctx, accountID, teslaID, since)` return non-empty `Snapshot` slices
- **THEN** every `Snapshot` in both results has a non-zero `EffectiveDate`
- **AND** that `EffectiveDate` equals `CapturedAt.AddDate(0, 0, -1)` for that row
- **AND** the population happens in `rowToSnapshot` (no per-method duplication)

#### Scenario: EffectiveDate is read-derived, never persisted

- **GIVEN** the `vehicle_snapshots` table schema
- **WHEN** the nightly poller writes a snapshot via the write path
- **THEN** no `effective_date` column is written (no such column exists)
- **AND** no migration is introduced by this change
- **AND** the write-built `Snapshot`'s `EffectiveDate` stays the zero `time.Time` (it is not read
  before storage)

#### Scenario: Existing callers that ignore EffectiveDate are unaffected

- **GIVEN** any existing caller of `telemetry.Reader` (e.g. the dashboard card render path) that
  does not read `EffectiveDate`
- **WHEN** it receives `Snapshot`s after this change
- **THEN** its behavior is unchanged
- **AND** it is not forced to handle the new field
- **AND** the `telemetry.Reader` interface method signatures are unchanged

#### Scenario: DST does not shift EffectiveDate relative to CapturedAt

- **GIVEN** a `CapturedAt` that falls on a DST boundary day in the poller's timezone
- **WHEN** `EffectiveDate` is computed via `CapturedAt.AddDate(0, 0, -1)`
- **THEN** `EffectiveDate` is one calendar day earlier than `CapturedAt`'s calendar day
- **AND** the offset is a calendar-day step (not a fixed 24-hour duration), so a 23- or 25-hour DST
  day does not move `EffectiveDate` off by an hour