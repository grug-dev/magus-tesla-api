## ADDED Requirements

### Requirement: Change-Detecting Supercharger-History Upsert

The telemetry capability SHALL advance a Supercharger-history row's `updated_at` only
when that row's mirrored data actually changed. A nightly re-sync that writes the same
mirrored values it already stored SHALL leave `updated_at` untouched. A nightly
re-sync that writes at least one different mirrored value SHALL advance `updated_at`
to the current time.

The comparison SHALL cover exactly the columns the nightly sync refreshes on a
conflict — no more, no less. Every other column SHALL be excluded from the
comparison: columns a human sets by hand outside the nightly sync, and columns the
nightly sync writes once at first capture but never refreshes afterward. Excluding a
column from the comparison SHALL be an explicit, visible decision, not the default
outcome of a column being merely absent from a hand-written comparison list.

This closes a false-signal problem: a downstream consumer reads `updated_at` to find
what changed since it last looked. Before this requirement, every nightly re-sync
advanced `updated_at` on every row, whether or not anything changed, making that
signal useless.

#### Scenario: An unchanged re-sync leaves `updated_at` untouched

- **GIVEN** a Supercharger-history row already stored with a given `updated_at`
- **WHEN** the nightly sync writes the same mirrored values for that session again
- **THEN** the row's `updated_at` stays exactly what it was before

#### Scenario: A real mirrored-value change advances `updated_at`

- **GIVEN** a Supercharger-history row already stored
- **WHEN** the nightly sync writes a different value for any column it mirrors
- **THEN** the row's `updated_at` advances to the current time

#### Scenario: A vehicle re-registration advances `updated_at`

- **GIVEN** a Supercharger-history row with no vehicle identity recorded, because the
  vehicle was not registered at the time of the session
- **WHEN** the nightly sync later resolves that session to a registered vehicle
- **THEN** the row's `updated_at` advances to the current time

#### Scenario: A human-entered value on the row does not block change detection, and is never mistaken for a change itself

- **GIVEN** a Supercharger-history row that a human has verified, so it carries a
  hand-entered value outside the nightly sync's mirrored columns
- **WHEN** the nightly sync re-writes the same mirrored values it already stored
- **THEN** the row's `updated_at` stays exactly what it was before, and the
  hand-entered value is left exactly as it was

#### Scenario: A column the nightly sync never refreshes never causes repeated `updated_at` advances

- **GIVEN** a Supercharger-history row whose stored value in a column the nightly
  sync writes only once at first capture no longer matches what the vendor now
  reports for that same column
- **WHEN** the nightly sync re-writes the same mirrored values it already stored,
  on any number of separate nights
- **THEN** the row's `updated_at` stays exactly what it was before, every time,
  because that mismatch is permanent and outside what the comparison covers

#### Scenario: A newly mirrored column that is not excluded from the comparison is detected

- **GIVEN** a Supercharger-history table with a column that is neither refreshed by
  the nightly sync nor excluded from the change comparison
- **WHEN** that column holds a value the nightly sync does not write
- **THEN** the sync's own change-detection test fails, so the omission is caught
  before it reaches production
