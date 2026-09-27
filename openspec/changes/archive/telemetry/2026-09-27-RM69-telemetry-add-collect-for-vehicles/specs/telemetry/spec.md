## ADDED Requirements

### Requirement: Retry Collection For A Specified Vehicle Subset

The telemetry capability SHALL expose a collection method that runs the same
election, per-account grouping, wake-and-fetch, snapshot storage, and
Supercharger history fetch as its full nightly collection, but scoped to a
caller-supplied set of vehicle identifiers rather than every registered
vehicle. A vehicle identifier in the caller-supplied set that is not
currently registered to any account SHALL be silently absent from every
outcome of the call: no Fleet API request SHALL be made for it, and no
attempt record SHALL be written for it. An empty caller-supplied set SHALL
make zero Fleet API requests and SHALL return a cycle outcome with every
count at zero.

The Supercharger history fetch for an account touched by this method SHALL
still cover that account's full currently-registered vehicle list, not only
the vehicles named in the caller-supplied set — a session belonging to one
of that account's other registered vehicles SHALL be upserted normally, and
SHALL NOT be counted as belonging to an unregistered vehicle merely because
that vehicle was outside the caller-supplied set for this call.

This method SHALL use the same account-election rule as the full nightly
collection: a vehicle registered to more than one account is polled by
exactly the account that rule would elect for it, never by a different
account.

**Reason**: MAG-98 / `RM69-nightly-retry-unfinished-vehicles` tier 1. A
vehicle with no internet at the nightly collection time gets no snapshot
that night, and the Fleet API has no endpoint to recover a past day's state.
A later tier retries such vehicles periodically until the end of the local
day; this requirement is the collection primitive that retry needs.

#### Scenario: A vehicle registered to two accounts is retried through its elected account
- **GIVEN** a vehicle registered to two accounts, one with OWNER access type
  and one with DRIVER access type
- **WHEN** this method is called with a set containing that vehicle's
  identifier
- **THEN** the vehicle is collected through the OWNER account
- **AND** no Fleet API request for that vehicle is made through the DRIVER
  account

#### Scenario: An unregistered vehicle identifier in the set produces no trace
- **GIVEN** a caller-supplied set containing one currently-registered vehicle
  identifier and one identifier that is not registered to any account
- **WHEN** this method is called
- **THEN** an attempt is recorded for the registered vehicle
- **AND** no attempt is recorded for the unregistered identifier
- **AND** no error is returned

#### Scenario: An account's other registered vehicles are not scoped out of its Supercharger fetch
- **GIVEN** an account with two registered vehicles, and the caller-supplied
  set names only one of them
- **WHEN** this method is called
- **THEN** only the named vehicle's snapshot is collected
- **AND** a Supercharger session belonging to the account's other, unnamed
  vehicle is upserted normally
- **AND** that session is NOT counted among sessions skipped for an
  unregistered vehicle

#### Scenario: An empty caller-supplied set makes no Fleet API request
- **GIVEN** an empty caller-supplied set of vehicle identifiers
- **WHEN** this method is called
- **THEN** no Fleet API request of any kind is made
- **AND** the returned cycle outcome has every count at zero
- **AND** no error is returned

### Requirement: Retry Trigger Attribution

The telemetry capability SHALL support recording an attempt or a run summary
as triggered by a periodic retry of previously unfinished vehicles, distinct
from the nightly scheduler and from a manual re-run. This attribution SHALL
require no change to how the trigger value is stored — the existing storage
already accepts any such value.

**Reason**: MAG-98 / `RM69-nightly-retry-unfinished-vehicles` tier 1. Tier 2
stamps every attempt and run summary written by the retry schedule with this
attribution, so an operator can tell a retry-triggered row from a
scheduler-triggered one.

#### Scenario: A retry-triggered attempt is stored and read back unchanged
- **GIVEN** an attempt recorded with the retry attribution
- **WHEN** that attempt is read back
- **THEN** its trigger attribution is the retry value, distinguishable from
  the nightly-scheduler and manual-re-run values
