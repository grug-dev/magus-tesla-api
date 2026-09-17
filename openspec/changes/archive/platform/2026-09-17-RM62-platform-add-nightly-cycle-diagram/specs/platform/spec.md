## ADDED Requirements

### Requirement: Nightly Cycle Reference Diagrams

The platform SHALL provide diagrams of the nightly cycle's steps, its
cross-module call chain, and its analytics derivation, stored under
`kkpa/docs/diagrams/nightly-job/`, and SHALL reference every one of them from
the nightly-cycle knowledge-base guide
(`kkpa/context/architecture/nightly-cycle.md`), so a reader can find and open
each diagram from the guide without searching the repository.

#### Scenario: A reader finds the workflow diagram from the guide

- **GIVEN** the nightly-cycle knowledge-base guide
- **WHEN** a reader opens its "Rendered view" section
- **THEN** it links to a local diagram file showing the cycle's four steps, in
  order, including the step-1 failure short-circuit and the step-4
  first-of-the-month gate

#### Scenario: A reader finds the sequence diagram from the guide

- **GIVEN** the nightly-cycle knowledge-base guide
- **WHEN** a reader opens its "Rendered view" section
- **THEN** it links to a local diagram file showing the cross-module call
  chain, with every call marked as either a paid Fleet API call or a database
  read or write

#### Scenario: A reader finds the derivation diagram from the guide

- **GIVEN** the nightly-cycle knowledge-base guide
- **WHEN** a reader opens its "Rendered view" section
- **THEN** it links to a local diagram file showing what the analytics
  recalculation step derives for one day's `vehicle_metrics` row, including
  the charge-corrected consumed percentage and the two figures derived from
  it

#### Scenario: The guide states a precedence order for every rendering it links

- **GIVEN** the nightly-cycle knowledge-base guide's "Rendered view" section
- **WHEN** a reader compares a rendering it links against the code
- **THEN** the section states that the code is authoritative first, the guide
  second, and any rendering — local diagram or published artifact — last
