# charge-session-log Specification Delta

## ADDED Requirements

### Requirement: Supercharger Mirror Synchronization Is Bounded By An Account Watermark

The charging capability's synchronization of Supercharger sessions SHALL
read only sessions the source reports as modified at or after the account's
own recorded watermark, rather than the source's entire history, every
time it runs. This bound SHALL widen slightly to tolerate a source write
that commits a short time after the watermark was last read, so a session
committed near the boundary of a prior run is never permanently missed.

**A synchronization run whose bounded read returns no session SHALL leave
the account's watermark unchanged.** Advancing the watermark on an empty
read would be indistinguishable, later, from a session that genuinely never
changed — and if the watermark advanced anyway, a session that the source
commits moments after the read would fall permanently behind the watermark
and never be picked up by a later run. This is the capability's single
most important synchronization guarantee.

**A synchronization run whose bounded read returns at least one session
SHALL advance the account's watermark to the highest last-modified instant
actually observed among those sessions — never to the instant the run
itself takes place.** Advancing to the run's own instant, rather than to
the highest instant actually seen, risks skipping a session the source
commits between the read and the advance.

An account with no recorded watermark SHALL be treated as never having been
synchronized: its first synchronization run SHALL read the source's entire
history for that account once, after which its watermark advances normally.

#### Scenario: An unchanged period between two runs is not re-synchronized
- **GIVEN** an account whose Supercharger sessions were fully synchronized
  as of a given instant, with the watermark advanced to that instant
- **WHEN** a later synchronization run finds no session modified since that
  instant, allowing for the tolerance window
- **THEN** the account's watermark is unchanged after the run
- **AND** no session is re-mirrored

#### Scenario: The watermark advances to the highest instant actually seen, not to the run's own instant
- **GIVEN** a synchronization run whose bounded read returns several
  sessions with different last-modified instants, all earlier than the
  moment the run itself executes
- **WHEN** the run completes successfully
- **THEN** the account's watermark advances to exactly the highest
  last-modified instant among the returned sessions
- **AND** not to the instant the run executed

#### Scenario: A session committed just after the read is still recovered by a later run
- **GIVEN** an account's watermark was last advanced to a given instant
- **AND** a session's data changes at the source shortly after that instant,
  within the tolerance window a later run's bounded read still covers
- **WHEN** the next synchronization run executes
- **THEN** that session's change is included in the run's read
- **AND** the account's watermark advances to reflect it

#### Scenario: A never-synchronized account backfills its whole history once
- **GIVEN** an account for which the Supercharger mirror has never
  recorded a watermark
- **WHEN** the first synchronization run executes for that account
- **THEN** every Supercharger session the source currently reports for that
  account is mirrored
- **AND** the account's watermark advances to reflect the sessions
  observed

#### Scenario: A failed mirror does not advance the watermark
- **GIVEN** a synchronization run whose bounded read returns sessions, but
  the mirroring step itself fails before completing
- **WHEN** the run ends
- **THEN** the account's watermark is unchanged from before the run
- **AND** the next run's bounded read still covers the sessions the failed
  run did not successfully mirror

#### Scenario: A session for a currently-unregistered vehicle is still recovered
- **GIVEN** a Supercharger session whose vehicle is not currently registered
  to the account, modified at or after the account's watermark
- **WHEN** the next synchronization run executes
- **THEN** the session is included in that run's bounded read
- **AND** it is mirrored like any other session in the run
