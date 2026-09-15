## MODIFIED Requirements

### Requirement: Supercharger Mirror Synchronization Is Bounded By A Vehicle Watermark

The charging capability's synchronization of Supercharger sessions SHALL, for each vehicle in
turn, read only sessions the source reports as modified at or after that vehicle's own recorded
watermark, rather than the source's entire history, every time it runs. This bound SHALL widen
slightly to tolerate a source write that commits a short time after the watermark was last read,
so a session committed near the boundary of a prior run is never permanently missed.

**A synchronization run whose bounded read for a vehicle returns no session SHALL leave that
vehicle's watermark unchanged.** Advancing the watermark on an empty read would be
indistinguishable, later, from a session that genuinely never changed — and if the watermark
advanced anyway, a session that the source commits moments after the read would fall permanently
behind the watermark and never be picked up by a later run. This is the capability's single most
important synchronization guarantee.

**A synchronization run whose bounded read for a vehicle returns at least one session SHALL
advance that vehicle's watermark to the highest last-modified instant actually observed among
those sessions — never to the instant the run itself takes place.** Advancing to the run's own
instant, rather than to the highest instant actually seen, risks skipping a session the source
commits between the read and the advance.

A vehicle with no recorded watermark SHALL be treated as never having been synchronized: its
first synchronization run SHALL read the source's entire history for that vehicle once, after
which its watermark advances normally.

A vehicle registered to more than one account SHALL be synchronized exactly once per run, not
once per owning account — the watermark is per vehicle, not per account, so there is only one
cursor to advance regardless of how many accounts the vehicle is registered to.

**This is a CHANGE from the prior revision of this requirement, under which the watermark was
recorded per account rather than per vehicle.** An account with two vehicles held one cursor and
needed two — the stored instant could not say how far each car's synchronization had actually
progressed. The prior revision also included a guarantee that a session for a vehicle not
currently registered to the synchronizing account was still recovered by a later run; that
guarantee described account-scoped iteration, which no longer exists — synchronization now runs
once per distinct vehicle across the whole platform, not once per account, so there is no
"currently unregistered to this account" case left to guarantee against.

#### Scenario: An unchanged period between two runs is not re-synchronized
- **GIVEN** a vehicle whose Supercharger sessions were fully synchronized as of a given instant,
  with the watermark advanced to that instant
- **WHEN** a later synchronization run finds no session modified since that instant, allowing for
  the tolerance window
- **THEN** the vehicle's watermark is unchanged after the run
- **AND** no session is re-mirrored

#### Scenario: The watermark advances to the highest instant actually seen, not to the run's own instant
- **GIVEN** a synchronization run whose bounded read for a vehicle returns several sessions with
  different last-modified instants, all earlier than the moment the run itself executes
- **WHEN** the run completes successfully
- **THEN** the vehicle's watermark advances to exactly the highest last-modified instant among
  the returned sessions
- **AND** not to the instant the run executed

#### Scenario: A session committed just after the read is still recovered by a later run
- **GIVEN** a vehicle's watermark was last advanced to a given instant
- **AND** a session's data changes at the source shortly after that instant, within the
  tolerance window a later run's bounded read still covers
- **WHEN** the next synchronization run executes
- **THEN** that session's change is included in the run's read
- **AND** the vehicle's watermark advances to reflect it

#### Scenario: A never-synchronized vehicle backfills its whole history once
- **GIVEN** a vehicle for which the Supercharger mirror has never recorded a watermark
- **WHEN** the first synchronization run executes for that vehicle
- **THEN** every Supercharger session the source currently reports for that vehicle is mirrored
- **AND** the vehicle's watermark advances to reflect the sessions observed

#### Scenario: A failed mirror does not advance the watermark
- **GIVEN** a synchronization run whose bounded read for a vehicle returns sessions, but the
  mirroring step itself fails before completing
- **WHEN** the run ends
- **THEN** the vehicle's watermark is unchanged from before the run
- **AND** the next run's bounded read still covers the sessions the failed run did not
  successfully mirror

#### Scenario: A vehicle registered to two accounts is synchronized once, not twice
- **GIVEN** a vehicle currently registered to two different accounts
- **WHEN** a synchronization run executes
- **THEN** that vehicle's bounded read and watermark advance happen exactly once for the run
- **AND** no session is mirrored twice as a result of the vehicle's two registrations
