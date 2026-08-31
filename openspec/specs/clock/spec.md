# clock Specification

## Purpose
TBD - created by archiving change RM35-clock-add-bogota-time-package. Update Purpose after archive.
## Requirements
### Requirement: Platform Default Time Zone
The platform SHALL define a single default time zone, `America/Bogota`, used wherever a time
zone is needed and none has been explicitly configured or supplied by the caller. Exactly one
capability SHALL own this default, and every other part of the platform SHALL obtain it through
that capability rather than hard-coding the zone name itself.

#### Scenario: The platform default zone is Bogota
- **GIVEN** the platform's clock capability
- **WHEN** its default time zone is resolved
- **THEN** the zone identifies as `America/Bogota`

### Requirement: Current Time In The Platform's Default Zone
The platform SHALL provide a way to obtain the current moment expressed in its default time
zone, so that no other part of the platform needs to call the underlying system clock and
convert the result itself.

#### Scenario: The current time reflects the default zone
- **GIVEN** the platform's clock capability
- **WHEN** the current time is requested through it
- **THEN** the returned time is expressed in the platform's default zone (`America/Bogota`)
- **AND** the returned instant matches the real current time, within ordinary execution latency

### Requirement: Time Zone Name Resolution With Fallback
The platform SHALL resolve an IANA time zone name to its corresponding zone. When the supplied
name cannot be resolved to a known zone, the platform SHALL fall back to its default zone rather
than surfacing an error to the caller.

#### Scenario: A valid IANA zone name resolves to that zone
- **GIVEN** a valid IANA time zone name (for example, `America/New_York`)
- **WHEN** the platform resolves it through the clock capability
- **THEN** the returned zone identifies as that same name

#### Scenario: An unrecognized zone name falls back to the platform default
- **GIVEN** a string that is not a valid IANA time zone name
- **WHEN** the platform attempts to resolve it as a zone through the clock capability
- **THEN** the returned zone is the platform's default zone (`America/Bogota`)
- **AND** no error is surfaced to the caller

### Requirement: Calendar Day Normalization
The platform SHALL provide a single way to normalize any moment in time to the calendar day it
falls on when observed in a given time zone, and SHALL express the result using the platform's
calendar-day storage representation (UTC midnight, unchanged by this capability). The calendar
day SHALL be determined by the moment's wall-clock date in the zone the caller supplies, not by
the moment's date in UTC, so that a moment near a day boundary is attributed to the correct
local day rather than the day UTC happens to be in at that instant.

#### Scenario: A moment already at the start of its UTC day normalizes to itself
- **GIVEN** a moment in time and the UTC zone
- **WHEN** it is normalized to its calendar day in the UTC zone
- **THEN** the result is that same calendar date, expressed at UTC midnight

#### Scenario: A moment normalizes to a different calendar day in a non-UTC zone
- **GIVEN** a moment whose calendar date in UTC differs from its calendar date in another zone
  (for example, a late-evening moment in a zone behind UTC that has already crossed into the
  next UTC calendar day)
- **WHEN** it is normalized to its calendar day in that other zone
- **THEN** the result reflects the calendar day of the moment's wall-clock time in that zone, not
  its UTC calendar day

#### Scenario: A daylight-saving transition does not change the normalized day
- **GIVEN** two moments in the same zone, one shortly before and one shortly after a
  daylight-saving transition, both falling on the same local calendar day in a zone that
  observes daylight saving time
- **WHEN** both moments are normalized to their calendar day in that zone
- **THEN** both normalize to the same calendar day

