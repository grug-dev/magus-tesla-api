# platform Specification

## Purpose
TBD - created by archiving change RM35-platform-add-tz-guard. Update Purpose after archive.
## Requirements
### Requirement: Time Zone Convention Guard
The platform SHALL provide a deterministic, automated check that fails the local build gate
whenever code outside `internal/clock` hand-rolls the current moment, a "midnight of some day"
time construction, or a hardcoded time-zone name, unless that occurrence is explicitly marked
as a deliberate, reviewed exception.

#### Scenario: A raw call to obtain the current moment outside the clock package fails the check
- **GIVEN** a Go source file outside `internal/clock` and outside any test file
- **WHEN** that file calls the standard library's current-time function directly, with no
  deliberate-exception marker on the same line
- **THEN** the platform's time zone convention guard fails
- **AND** it reports the offending file and line

#### Scenario: A hand-rolled "midnight of a day" construction outside the clock package fails the check
- **GIVEN** a Go source file outside `internal/clock` and outside any test file
- **WHEN** that file constructs a specific moment whose time-of-day components are all zeroed
  out, with no deliberate-exception marker on the same line
- **THEN** the platform's time zone convention guard fails
- **AND** it reports the offending file and line

#### Scenario: A hardcoded time-zone name outside the clock package fails the check
- **GIVEN** a Go source file outside `internal/clock` and outside any test file
- **WHEN** that file contains a literal time-zone identifier string, with no deliberate-exception
  marker on the same line
- **THEN** the platform's time zone convention guard fails
- **AND** it reports the offending file and line

#### Scenario: A marked deliberate exception does not fail the check
- **GIVEN** a line that would otherwise be flagged by the guard
- **WHEN** that line carries the guard's deliberate-exception marker as a trailing comment
- **THEN** the platform's time zone convention guard does not flag that line

#### Scenario: The clock package's own implementation does not fail the check
- **GIVEN** a file inside `internal/clock`
- **WHEN** that file contains any of the constructs the guard otherwise flags
- **THEN** the platform's time zone convention guard does not flag it

#### Scenario: Test files do not fail the check
- **GIVEN** a Go test file, anywhere in the codebase
- **WHEN** that file contains any of the constructs the guard otherwise flags
- **THEN** the platform's time zone convention guard does not flag it

#### Scenario: A comment describing the convention does not fail the check
- **GIVEN** a source-code comment line that mentions one of the guarded constructs only in
  prose, not as executable code
- **WHEN** the platform's time zone convention guard runs
- **THEN** that comment line is not flagged

#### Scenario: The composition root is out of scope
- **GIVEN** a file under the platform's command-line entry points (`cmd/`)
- **WHEN** that file contains any of the constructs the guard otherwise flags
- **THEN** the platform's time zone convention guard does not flag it, because entry-point files
  are never scanned by the guard

#### Scenario: The check is part of the full local gate
- **GIVEN** the platform's full local verification gate
- **WHEN** that gate runs
- **THEN** the time zone convention guard runs as one of its steps

