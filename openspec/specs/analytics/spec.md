# analytics Specification

## Purpose
Derived analytics over stored telemetry — the platform's metrics layer, sitting between
what `telemetry` captures and what the dashboard renders. It owns no database and no capture: it
reads sibling modules' public ports and computes values none of them store. Its first metric is
rolling energy-per-kilometre (Wh/km).
## Requirements
### Requirement: Recent Energy-Per-Kilometre Derivation
The analytics capability SHALL derive a rolling energy-per-kilometre (Wh/km) value for a given
vehicle over a fixed window (default 30 days, fixed at construction time), from that vehicle's
stored telemetry snapshots plus its Supercharger sessions and manually-logged charge entries
within the window. The capability SHALL consume the distance values in the unit the telemetry read
port already provides them in, and SHALL NOT perform any unit conversion of its own. The
capability SHALL compute the energy numerator as measured charging energy (kWh) minus a
pack-capacity correction for net SoC drift over the window, using whichever of
`UsableBatteryLevel` or `BatteryLevel` is consistently available at BOTH window endpoints (never a
mixed pair). The capability SHALL scope every underlying read to the given account, even when the
vehicle identifier alone would suffice, as defense-in-depth tenant isolation.

#### Scenario: A vehicle with two or more snapshots, known capacity, and net consumption gets a computed value
- **GIVEN** a vehicle with at least two telemetry snapshots inside the window, whose Fleet API
  `car_type` is present in the platform's pack-capacity reference table
- **AND** the vehicle's net battery state-of-charge decreased over the window (it consumed more
  than it charged)
- **WHEN** the capability derives recent efficiency for that vehicle
- **THEN** it returns a Wh/km value derived from measured charging energy corrected by the
  pack-capacity SoC-drift term, `ok=true`, and `Approximate=false`

#### Scenario: Distance is read, not converted
- **GIVEN** a vehicle with at least two telemetry snapshots inside the window
- **WHEN** the capability computes the distance travelled over the window
- **THEN** it takes the kilometre values directly from the snapshots returned by the telemetry
  read port
- **AND** it applies no unit-conversion factor and calls no conversion method to obtain them
- **AND** the derived Wh/km value is the same as it was when the conversion was performed on read

#### Scenario: Usable battery level is used only when present at both window endpoints
- **GIVEN** a vehicle whose first snapshot in the window has a non-nil `UsableBatteryLevel` and
  whose last snapshot in the window has a nil `UsableBatteryLevel`
- **WHEN** the capability computes the state-of-charge delta for that window
- **THEN** it uses `BatteryLevel` at BOTH endpoints (never usable at one endpoint and nominal at
  the other)

#### Scenario: Usable battery level is used when present at both window endpoints
- **GIVEN** a vehicle whose first and last snapshots in the window both have a non-nil
  `UsableBatteryLevel`
- **WHEN** the capability computes the state-of-charge delta for that window
- **THEN** it uses `UsableBatteryLevel` at both endpoints

### Requirement: Unknown Pack Capacity Yields an Approximate Value, Never a Blank Tile
The analytics capability SHALL NOT refuse to return an efficiency value solely because the
vehicle's pack capacity is unknown (its `car_type` is absent, or not present in the
pack-capacity reference table). In that case the capability SHALL compute energy from measured
charging energy alone (no SoC-drift correction) and SHALL mark the result `Approximate=true`.

#### Scenario: Unknown car_type still returns a value, marked approximate
- **GIVEN** a vehicle whose registered `CarType` is nil, or whose `CarType` is not present in the
  pack-capacity reference table
- **AND** the vehicle otherwise has enough snapshots and positive net consumption to compute a
  value
- **WHEN** the capability derives recent efficiency for that vehicle
- **THEN** it returns a Wh/km value computed from measured charging energy without any
  capacity correction, `ok=true`, and `Approximate=true`

#### Scenario: Known car_type applies the capacity correction and is not marked approximate
- **GIVEN** a vehicle whose registered `CarType` is present in the pack-capacity reference table
- **WHEN** the capability derives recent efficiency for that vehicle
- **THEN** the returned value's `Approximate` field is `false`

### Requirement: Insufficient Data Returns ok=false, Never a Fabricated Value
The analytics capability SHALL return `ok=false` with no error — never a fabricated or
divide-by-near-zero value — in each of these cases: fewer than two telemetry snapshots exist in
the window; the vehicle's odometer reading did not increase over the window (parked, or a
non-increasing reading); or the derived energy consumed is zero or negative (net charge over the
window exceeded consumption).

#### Scenario: Fewer than two snapshots in the window
- **GIVEN** a vehicle with zero or exactly one telemetry snapshot inside the window
- **WHEN** the capability derives recent efficiency for that vehicle
- **THEN** it returns `ok=false` and no error

#### Scenario: No distance moved over the window
- **GIVEN** a vehicle with at least two snapshots in the window whose odometer reading at the
  window's end is not greater than its odometer reading at the window's start
- **WHEN** the capability derives recent efficiency for that vehicle
- **THEN** it returns `ok=false` and no error

#### Scenario: Net charge exceeds consumption over the window
- **GIVEN** a vehicle with at least two snapshots and positive distance moved over the window
- **AND** the derived energy consumed (measured charging energy minus the SoC-drift correction,
  or measured charging energy alone when capacity is unknown) is zero or negative
- **WHEN** the capability derives recent efficiency for that vehicle
- **THEN** it returns `ok=false` and no error

### Requirement: Multi-Tenant Scoping on Every Underlying Read
The analytics capability SHALL pass the given account identifier to every underlying port call it
makes (telemetry snapshot history, Supercharger sessions, manually-logged charge entries, and
vehicle registration lookup), so that a caller can never retrieve another account's data by
supplying a vehicle identifier alone.

#### Scenario: Every underlying read is scoped to the given account
- **GIVEN** a request to derive recent efficiency for a specific account and vehicle
- **WHEN** the capability performs its underlying reads
- **THEN** every one of those reads is scoped to the given account identifier

### Requirement: No Cross-Module Database Access
The analytics capability SHALL own no database of its own and SHALL access telemetry, manual
charge, and account data exclusively through those modules' public read ports — never through a
shared database connection, another module's generated query package, or any other bypass of the
module boundary.

#### Scenario: The capability owns no database
- **GIVEN** the analytics capability's implementation
- **WHEN** its data dependencies are inspected
- **THEN** it imports only the public `Reader`/`SuperchargerReader` interfaces of
  `internal/telemetry`, the public `Reader` interface of `internal/manualcharge`, and the public
  `Service` interface of `internal/account` — never `internal/telemetry/db`,
  `internal/manualcharge/db`, or `internal/account/db`

### Requirement: Per-Day Battery-Consumed Derivation

The analytics capability SHALL derive, for each calendar day in a caller-supplied date
range that has a computable value, a corrected battery-consumed percentage equal to the
day's raw battery-level delta plus the sum of `(end_battery_pct − start_battery_pct)`
across every charge event matched to that day from both charging-cost sources the
platform stores (Supercharger sessions and manually-logged charge entries). The
capability SHALL sum across ALL matched charge events for a day, never a single
("latest" or "first-to-last") charge event, when more than one charge event matches the
same day.

#### Scenario: A single matched charge event corrects the raw delta
- **GIVEN** a vehicle whose raw battery-level delta for a day is −51 (battery went from
  22% down to 73%... i.e. the predecessor read 22% higher usage is reversed by charging)
- **AND** exactly one charge event matched to that day added back 62 percentage points
  (`end_battery_pct − start_battery_pct = 80 − 18 = 62`)
- **WHEN** the capability derives that day's consumed percentage
- **THEN** it returns 11 (`−51 + 62`)

#### Scenario: Multiple matched charge events on the same day are all summed
- **GIVEN** a vehicle whose raw battery-level delta for a day is −45
- **AND** two charge events matched to that same day: one contributing +30 percentage
  points and one contributing +20 percentage points
- **WHEN** the capability derives that day's consumed percentage
- **THEN** it returns 5 (`−45 + 30 + 20`)
- **AND** it does NOT return −25 (the result of using only the later charge event's
  delta) or 15 (the result of treating the two events as one span from the first event's
  start to the second event's end)

### Requirement: Gap Detection on Corrected Daily Consumption

The analytics capability SHALL flag a day's corrected consumed percentage as a data gap
when the corrected value is negative, or when it is exactly zero while the vehicle's
distance travelled that day exceeds a fixed threshold. It SHALL NOT flag a day whose
corrected value is exactly zero when the distance travelled that day is at or below that
threshold. For a flagged day, the capability SHALL infer which charging source is
suspected missing: the Supercharger source when a Supercharger session matched to that
day exists with a NULL start or end battery percentage, and the manual source otherwise.

#### Scenario: A negative corrected value is flagged
- **GIVEN** a day whose corrected consumed percentage computes to a negative value
- **WHEN** the capability derives that day's result
- **THEN** the day is flagged
- **AND** the inferred missing-charging-source is Supercharger when a Supercharger
  session matched to that day has a NULL percentage, and manual otherwise

#### Scenario: A zero corrected value with meaningful distance is flagged
- **GIVEN** a day whose corrected consumed percentage computes to exactly zero
- **AND** the vehicle's distance travelled that day exceeds the platform's flag-distance
  threshold
- **WHEN** the capability derives that day's result
- **THEN** the day is flagged

#### Scenario: A zero corrected value with negligible distance is NOT flagged
- **GIVEN** a day whose corrected consumed percentage computes to exactly zero
- **AND** the vehicle's distance travelled that day is at or below the platform's
  flag-distance threshold
- **WHEN** the capability derives that day's result
- **THEN** the day is NOT flagged

### Requirement: A Day With No Usable Predecessor Is Skipped, Never Flagged

The analytics capability SHALL omit a calendar day from its result entirely — never include
it with a fabricated or zero-valued consumed percentage — when that day's underlying
telemetry snapshot has no predecessor to compute a raw delta from (the vehicle's
first-ever captured snapshot).

#### Scenario: A vehicle's first-ever snapshot day is omitted, not flagged
- **GIVEN** a telemetry snapshot that is the vehicle's first-ever capture, with no
  predecessor snapshot to compute a raw battery-level delta from
- **WHEN** the capability derives per-day consumption for a range including that day
- **THEN** that day does not appear in the result at all
- **AND** it is not counted as flagged

### Requirement: Multi-Day Spans Are Represented By A Single Entry

The analytics capability SHALL represent a multi-day span — consecutive telemetry snapshots
more than one calendar day apart, caused by a missed poll — as exactly one result entry,
dated the later of the two snapshots' calendar days, whose corrected consumed percentage
sums every charge event matched anywhere within the span. The capability SHALL NOT
produce a result entry for any of the span's intervening calendar days.

#### Scenario: A three-day gap between polls yields exactly one entry
- **GIVEN** two consecutive telemetry snapshots three calendar days apart (a missed poll),
  with charge events matched at points throughout the span
- **WHEN** the capability derives per-day consumption for a range including that span
- **THEN** exactly one result entry is returned for the span, dated the later snapshot's
  calendar day
- **AND** every charge event matched within the span is summed into that single entry's
  corrected consumed percentage
- **AND** no result entry exists for either of the span's two intervening calendar days

### Requirement: Charge-to-Day Matching Is Source-Specific

The analytics capability SHALL match a Supercharger session to a day using the session's
stop instant against the interval between that day's telemetry capture instant and its
predecessor's capture instant (inclusive of the interval's start, exclusive of its end).
The capability SHALL match a manually-logged charge entry to a day using the entry's
logged calendar date, inclusive.

#### Scenario: A Supercharger session stopping exactly at the interval's start is matched
- **GIVEN** a day's underlying interval starting at instant `T0` (the predecessor
  snapshot's capture instant)
- **AND** a Supercharger session whose stop instant equals `T0` exactly
- **WHEN** the capability matches charge events to that day
- **THEN** that session is matched to the day

#### Scenario: A Supercharger session stopping exactly at the interval's end is NOT matched to that day
- **GIVEN** a day's underlying interval ending at instant `T1` (that day's own snapshot
  capture instant)
- **AND** a Supercharger session whose stop instant equals `T1` exactly
- **WHEN** the capability matches charge events to that day
- **THEN** that session is not matched to that day (it belongs to the following day's
  interval instead)

### Requirement: No Cache — Every Result Is Recomputed On Read

The analytics capability SHALL NOT store, cache, or persist any per-day consumed-percentage
result. Every call SHALL recompute its result from the underlying telemetry snapshot,
Supercharger session, and manual charge entry data available at call time.

#### Scenario: Fixing an underlying charge record changes the next call's result with no invalidation step
- **GIVEN** a day previously flagged as a data gap because no charge event was matched to
  it
- **AND** a matching charge event is subsequently added to one of the two charging-cost
  sources for that same day
- **WHEN** the capability derives that day's result again
- **THEN** the day is no longer flagged, reflecting the newly added charge event, with no
  separate invalidation or refresh step required

### Requirement: Multi-Tenant Scoping On Every Underlying Read

The analytics capability SHALL pass the given account identifier to every underlying read
it performs when deriving per-day consumption (telemetry snapshot history, Supercharger
sessions, and manually-logged charge entries), so that a caller can never retrieve another
account's data by supplying a vehicle identifier alone.

#### Scenario: Every underlying read for per-day consumption is scoped to the given account
- **GIVEN** a request to derive per-day consumption for a specific account and vehicle
- **WHEN** the capability performs its underlying reads
- **THEN** every one of those reads is scoped to the given account identifier

