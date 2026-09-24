# reference Specification

## Purpose

Hold external reference values that belong to no vehicle and no user — values the platform
needs but does not itself observe from a car or a person. The first such value is the price
of a gallon of gasoline, by calendar month, used elsewhere to turn a vehicle's charging cost
into a Colombian cost-parity comparison (km per gallon).

This capability owns exactly one fact per month: what a gallon of gasoline cost. It does not
compute or store anything derived from that fact — a consuming capability reads the price
and does its own arithmetic. It has no notion of region or fuel grade; one national price of
regular gasoline is what the platform currently needs.

## ADDED Requirements

### Requirement: A Gasoline Price Is Stored Per Calendar Month

The capability SHALL store at most one gasoline price per calendar month, identified by the
first day of that month, together with the currency the price is expressed in.

The capability SHALL NOT provide any command, form, or automated process to add or change a
price. Every price SHALL be entered as a one-time data change, by hand, outside any running
process.

The capability SHALL NOT distinguish prices by region or by fuel product. A stored price
SHALL apply to the whole country and to regular gasoline only.

#### Scenario: A price is recorded for one calendar month

- **GIVEN** no price recorded for a given calendar month
- **WHEN** a price for that month is added
- **THEN** exactly one price exists for that month
- **AND** it carries the currency it was recorded in

#### Scenario: A calendar month accepts at most one price

- **GIVEN** a price already recorded for a given calendar month
- **WHEN** a second price is added for that same calendar month
- **THEN** the addition is rejected, and the existing price is unchanged

### Requirement: A Month With No Price Resolves To The Newest Earlier Price

The capability SHALL resolve a gasoline price for any requested calendar month, even one
with no price recorded of its own, by using the newest recorded price at or before that
month.

The capability SHALL NOT resolve a price for a requested month using any price recorded
after that month.

The capability SHALL NOT invent or assume a price for a month that has no recorded price at
or before it. Such a month SHALL resolve to no price at all, distinguishable from a
resolved price of zero.

#### Scenario: A month with its own recorded price resolves to that price

- **GIVEN** a price recorded for a given calendar month
- **WHEN** a price is resolved for that same month
- **THEN** the month's own recorded price is returned

#### Scenario: A month with no price of its own resolves to the newest earlier price

- **GIVEN** a price recorded for an earlier calendar month, and no price recorded for a
  later month
- **WHEN** a price is resolved for that later month
- **THEN** the earlier month's price is returned

#### Scenario: A month before every recorded price resolves to no price

- **GIVEN** the earliest recorded price is for a given calendar month
- **WHEN** a price is resolved for a calendar month before that one
- **THEN** no price is resolved for that month
- **AND** this is distinguishable from a resolved price of zero

#### Scenario: A price recorded after a month never affects that month's resolution

- **GIVEN** a price recorded for a given calendar month, and a later price recorded for a
  more recent calendar month
- **WHEN** a price is resolved for the earlier month
- **THEN** the earlier month's own price is returned, never the later one

### Requirement: A Range Of Months Resolves In One Request

The capability SHALL resolve prices for an arbitrary range of calendar months in a single
request, applying the newest-price-at-or-before rule independently to each month in the
range.

The result SHALL include only the months in the range that resolve to a price. A month in
the range that resolves to no price SHALL be absent from the result, never present with an
empty or zero value.

#### Scenario: Every month in a range resolves

- **GIVEN** a price recorded at or before the start of a requested range of calendar months
- **WHEN** prices are resolved for that range
- **THEN** every month in the range appears in the result, each with its own resolved price

#### Scenario: A leading month with no resolvable price is left out

- **GIVEN** a requested range of calendar months whose earliest months fall before any
  recorded price, and whose later months fall at or after the earliest recorded price
- **WHEN** prices are resolved for that range
- **THEN** the result contains an entry only for the months at or after the earliest
  recorded price
- **AND** the leading months are simply absent from the result, not present with a zero or
  empty value
