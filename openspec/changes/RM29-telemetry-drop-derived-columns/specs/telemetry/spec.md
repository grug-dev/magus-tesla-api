## ADDED Requirements

### Requirement: Preceding-Snapshot Read Port

The telemetry capability SHALL expose a read port through which another module can
retrieve, for one vehicle within one account, the single most recently captured
snapshot whose capture calendar day is strictly before a caller-supplied calendar day
— without accessing the telemetry module's database tables directly. The port SHALL
identify the vehicle by its account and its Tesla numeric id, SHALL take the boundary
as a whole calendar day (never an instant), and SHALL return the existing `Snapshot`
domain type. When the vehicle has no snapshot captured before that day, the port SHALL
return an absent result and no error — "no predecessor exists" is a normal answer, not
a failure. A genuine lookup failure SHALL be reported as an error and SHALL NOT be
represented as an absent result, so a transient storage fault can never be mistaken by
a caller for "this vehicle has no earlier snapshot".

The boundary SHALL be evaluated against the snapshot's stored capture calendar day,
not against its precise capture instant. Consequently a snapshot captured on the
boundary day itself is never returned as its own predecessor, regardless of the
timezone the collector runs in.

The port SHALL reach the true predecessor however old it is — there SHALL be no
maximum lookback, no trailing-window limit and no fixed number of days beyond which
the predecessor is reported absent.

#### Scenario: The immediately preceding day's snapshot is returned
- **GIVEN** a vehicle with snapshots captured on three consecutive calendar days
- **WHEN** a caller requests the snapshot preceding the third day
- **THEN** the second day's snapshot is returned

#### Scenario: A predecessor many days older is still returned
- **GIVEN** a vehicle whose most recent snapshot was captured seven calendar days
  after its previous one (the collector missed six nights)
- **WHEN** a caller requests the snapshot preceding the later capture's day
- **THEN** the snapshot from seven days earlier is returned, not an absent result

#### Scenario: A vehicle's first-ever snapshot has no predecessor
- **GIVEN** a vehicle with exactly one stored snapshot
- **WHEN** a caller requests the snapshot preceding that snapshot's own capture day
- **THEN** an absent result is returned
- **AND** no error is returned

#### Scenario: A same-day re-capture is never its own predecessor
- **GIVEN** a vehicle with a snapshot for calendar day N−1
- **AND** a snapshot for calendar day N that was later replaced by a second capture on
  the same day N (the existing "latest capture for a calendar day wins" rule)
- **WHEN** a caller requests the snapshot preceding day N
- **THEN** the day N−1 snapshot is returned
- **AND** neither the replaced nor the replacing day-N snapshot is returned

#### Scenario: A vehicle with no snapshots at all returns an absent result
- **GIVEN** a vehicle for which no snapshot has ever been stored
- **WHEN** a caller requests the snapshot preceding any calendar day
- **THEN** an absent result and no error are returned

## REMOVED Requirements

### Requirement: Derived Consumption Metrics

**Reason**: MAG-26 / roadmap `RM29-modular-monolith-boundaries` tier 4. These five
derived values were computed by the telemetry capability at capture time and stored on
its own snapshot table, but **no part of the telemetry capability ever read them
back** — their only consumer was the derived-metrics (analytics) capability. A
capability that stores a derivation solely for another capability's benefit is the
boundary blur MAG-26 exists to remove; roadmap D1 and D6 place derived figures with
the capability that consumes them, pulled through ports that capability defines.

Two further properties made the arrangement untenable rather than merely untidy.
First, the values could go stale silently: the snapshot table stopped being append-only
when same-day re-capture replacement was introduced, and the derivation ran only for
the row *being written*, so a successor row's stored values kept describing a
predecessor reading that no longer existed. Second, computing at capture time locked
the figures to whatever predecessor was visible at that instant, with no way to
recompute them.

**Migration**: Every behaviour this requirement specified is preserved, with identical
numbers, by the analytics capability's new `Requirement: Analytics-Owned Derived
Consumption Figures` — same five figures, same signed-not-clamped rule for distance
and battery-percentage-used, same "strictly greater than zero" divisor guard leaving
both efficiency figures absent otherwise, same all-five-absent result when the vehicle
has no prior snapshot, and the same multi-day-span totals. The figures are now derived
during recalculation from the raw observations telemetry still stores (odometer,
battery level, capture calendar day), so no observation is lost and no re-fetch from
the vehicle API is needed. The predecessor lookup this derivation needs is served by
the new `Requirement: Preceding-Snapshot Read Port` above.

Two behaviours improve rather than transfer, and are specified as such on the
analytics side: a day whose predecessor is older than the recalculation window is now
computed instead of silently omitted, and a day whose predecessor row was replaced by
a same-day re-capture is now recomputed against the replacement instead of retaining a
stale figure.

## Notes (implementation detail, not a new or changed Requirement)

### `SnapshotsByVehicleBetween`'s safety cap raised: 400 → 4000 (task 4.9)

This method's result cap (originally justified by the HTTP-bounded ~91-row gateway
window, design D3 of `RM8-telemetry-between-range-port`) is also the query
`internal/analytics`' `Reconcile` uses for its epoch backfill window. This tier's
`Requirement: Preceding-Snapshot Read Port` above removes the analogous silent-failure
class for the *predecessor* side of that backfill (Decision D2 rejected any bounded
lookback specifically because a silently wrong delta is worse than an error). Decision
**D3** of this change's `design.md`, prompted by decision **I3** (the watermark reset
that makes `Reconcile` rebuild a vehicle's entire snapshot history in one window), found
that the *forward* side carried the identical failure class at 400: a vehicle with more
than 400 days of history had its newest rows silently dropped by
`ORDER BY captured_at ASC LIMIT 400`, and `Reconcile` advanced its watermark past days it
never recomputed — permanently wrong metrics, no error, no log line. The cap is raised to
4000 (≈11 years at one snapshot/vehicle/day) to close that gap; it remains a
runaway-query guard only, not a load-bearing behavior, so no `Requirement`/`Scenario` is
added or changed here — the caller-supplied `[start, end]` window is still the actual
correctness boundary. See `internal/telemetry/db/query.sql`'s `SnapshotsByVehicleBetween`
comment for the full rationale.
