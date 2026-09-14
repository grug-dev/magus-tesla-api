# charging Specification

## Purpose

The `charging` capability owns the platform's charging records and the database schema that
holds them. It covers two deliberately separate tables: `manual_charge_entries`, the
user-asserted home/work/third-party sessions Tesla's Fleet API cannot attribute to a
vehicle, and `supercharger_sessions`, the dense nightly mirror of `internal/telemetry`'s
Supercharger history plus the human-owned battery-percentage verification channel only this
module writes.

Both tables live in the module's own PostgreSQL schema, `charging` — this capability is
where the modular-monolith boundary stops being a convention in Go and becomes checkable in
the database catalog. It is scoped to storage, naming, and the module's public ports
(`Writer`, `Reader`, `SessionWriter`, and the session read/verification ports); the
telemetry→charging mapping belongs to `internal/app`, and the watermark vocabulary the
Supercharger rename touches belongs to `analytics`.
## Requirements
### Requirement: Module-Scoped Database Schema
The charging module's `manual_charge_entries` and `supercharger_sessions` tables SHALL live
in a PostgreSQL schema named `charging`, distinct from the `public` schema and from every
other module's schema. This SHALL be a namespacing change only with respect to
`manual_charge_entries`: it SHALL NOT alter any stored data, any constraint, any index, or
any behavior of the module's public interface (`Writer`, `Reader`) for that table. No other
module SHALL be granted access to the `charging` schema's tables — the module boundary
(`ai/architecture.md` §2, "no cross-module database leaks") is enforced identically before
and after this requirement, now additionally checkable at the database catalog level.

The `vehicle_metric_watermarks.source` vocabulary collision this requirement's companion
table rename (see "Supercharger Sessions Table Renamed" below) creates on
`internal/analytics`'s own table is explicitly OUT OF SCOPE for this requirement — it is
analytics' table, not charging's. Its resolution is a separate analytics-owned change,
`RM39-analytics-fix-watermark-vocabulary` (roadmap tier 3b, D15), analysed in this change's
`design.md` "D8 — Boundary" section.

#### Scenario: The manual_charge_entries and supercharger_sessions tables resolve under the charging schema
- **GIVEN** the charging module's migrations have been applied
- **WHEN** the database catalog is queried for `charging.manual_charge_entries` and
  `charging.supercharger_sessions`
- **THEN** both resolve to their table (a non-null relation)
- **AND** neither `public.manual_charge_entries` nor `public.charge_sessions` nor
  `charging.charge_sessions` resolves to a relation any longer

#### Scenario: Existing manual charge entries survive the schema move unchanged
- **GIVEN** manual charge entries already stored for one or more accounts
- **WHEN** the schema-move migration is applied
- **THEN** every row is preserved unchanged
- **AND** the table's constraints and both indexes
  (`idx_manual_charge_entries_vehicle_time`, `idx_manual_charge_entries_account_time`)
  continue to be enforced exactly as before

#### Scenario: The manual_charge_entries public interface is unaffected by the schema move
- **GIVEN** a caller of `charging.Writer` or `charging.Reader`
- **WHEN** the schema move is applied
- **THEN** every exported type name, method name, and method signature is unchanged
- **AND** the returned data is identical to what the same call returned before the move
- **AND** no caller (`internal/gateway`, `internal/analytics`) needs to change to keep working

### Requirement: Supercharger Sessions Table Renamed
The table previously named `charge_sessions` SHALL be renamed to `supercharger_sessions` in
the same migration that moves it into the `charging` schema, because it is a dense,
Supercharger-only mirror and its old name over-claimed coverage of all charging activity.
Every row SHALL survive unchanged, and every constraint and index SHALL preserve its exact
prior definition. EVERY catalog object still carrying the old table name SHALL be renamed to
follow it (`design.md`'s "Rename scope (D16)") — the index
`idx_charge_sessions_vehicle_stop`, the named CHECK `charge_sessions_pct_source_required`,
the primary key `charge_sessions_pkey`, the unique constraint
`charge_sessions_account_session_unique`, and the five CHECK constraints Postgres auto-named
from inline column constraints (`charge_sessions_battery_pct_source_check`,
`charge_sessions_start_battery_pct_check`, `charge_sessions_end_battery_pct_check`,
`charge_sessions_start_battery_pct_est_check`, `charge_sessions_end_battery_pct_est_check`).
The completeness criterion is the catalog, not a list: after this migration no relation,
index or constraint owned by this module SHALL have a name beginning `charge_sessions`. Postgres renames none of these automatically,
so leaving any behind would print the retired name in a duplicate-key or check-violation
error against a table the rest of the system calls `supercharger_sessions`.
The sqlc-generated Go model SHALL be renamed from `ChargeSession` to `SuperchargerSession`
with an identical field list — this is a deliberate exception to this module's general
schema-move-preserves-Go-names rule, because the rename's whole purpose is to retire the
`charge_sessions` name everywhere, including in the code that reads it most. The two sqlc
query names that embedded the old table name, `MirrorChargeSession` and `VerifyChargeSession`,
SHALL be renamed to `MirrorSuperchargerSession` and `VerifySuperchargerSession`, with no
change to either query's parameters, WHERE-scoping, or column effects.

**This requirement's catalog scenarios are CHANGED from their prior revision, which
asserted the account-keyed shapes this rename preserved at the time.** The re-key to the
vehicle (see "Supercharger Session Vehicle Keying") replaced both of them: the unique
constraint now covers the session identifier alone, and the vehicle index no longer leads
with an account column. The rename requirement itself is unchanged — only the shapes the
scenarios check.

#### Scenario: The renamed table resolves and the old name does not
- **GIVEN** this tier's migration has been applied
- **WHEN** the database catalog is queried for `charging.supercharger_sessions`
- **THEN** it resolves to the same table `public.charge_sessions` (later `charging.charge_sessions`)
  identified before this migration, by unchanged primary key values on every existing row
- **AND** `charge_sessions` resolves to no relation, under `public` or under `charging`

#### Scenario: The renamed index and constraint preserve their definitions
- **GIVEN** this tier's migration has been applied
- **WHEN** the database catalog is queried for indexes and constraints on
  `charging.supercharger_sessions`
- **THEN** `idx_supercharger_sessions_vehicle_stop` exists, covers
  `(tesla_id, charge_stop_date_time)`, and `idx_charge_sessions_vehicle_stop`
  does not exist
- **AND** `supercharger_sessions_pct_source_required` exists with the identical CHECK
  expression `charge_sessions_pct_source_required` had, which no longer exists
- **AND** the primary key constraint is named `supercharger_sessions_pkey` and still covers
  `id`, and the session-identifier uniqueness constraint is named
  `supercharger_sessions_session_id_unique` and covers `session_id` alone, with no
  constraint named `supercharger_sessions_account_session_unique` remaining
- **AND** `pg_constraint` and `pg_indexes` return NO name beginning `charge_sessions` for
  this table — the completeness criterion is the catalog, not a fixed list

#### Scenario: The renamed Go type carries an identical field set
- **GIVEN** `make sqlc` has regenerated `internal/charging/db/models.go` against this tier's
  migration
- **WHEN** the generated `SuperchargerSession` struct is compared to the pre-migration
  `ChargeSession` struct
- **THEN** every field name, type, and declaration order is identical — only the struct's
  own type identifier changed
- **AND** no other generated struct (`ManualChargeEntry`) changed at all

### Requirement: Supercharger Mirror Watermark Storage

The charging module SHALL own a per-vehicle cursor table recording the
highest last-modified instant its Supercharger mirror has already
synchronized from the source it mirrors. Each vehicle SHALL have at most
one such cursor. When no cursor exists yet for a vehicle, the module SHALL
report this as the epoch — no error — so a caller can treat "no cursor" and
"never mirrored" identically. The cursor SHALL NOT identify an account or a
specific source table: the mirror it bounds reads one vehicle's data, from
exactly one upstream source.

**This is a CHANGE from the prior revision of this requirement, under which the cursor was
per account and was explicitly forbidden from identifying a vehicle.** That shape existed
so an account-wide read could recover a session whose vehicle was not currently
registered. The source no longer stores such a session at all, so there is nothing left to
recover, and a per-account cursor would now bound a read that is made per vehicle — one
cursor could be advanced past another vehicle's unread rows, losing them silently. The
cursors stored under the old shape SHALL NOT be carried over: one account cursor cannot be
split into one cursor per vehicle without claiming progress that was never made, and an
absent cursor already means "re-read everything once", which is safe because the mirror is
idempotent.

No other module SHALL be granted access to this cursor table directly
(`ai/architecture.md` §2, "no cross-module database leaks") — every read and
write SHALL go through the module's own public port.

#### Scenario: A vehicle with no cursor reports the epoch, not an error
- **GIVEN** a vehicle for which the mirror has never advanced a cursor
- **WHEN** that vehicle's cursor is read
- **THEN** the epoch (the earliest possible instant) is returned
- **AND** no error is returned

#### Scenario: A cursor advance is retained exactly
- **GIVEN** a vehicle's cursor is advanced to a specific instant
- **WHEN** that vehicle's cursor is read afterward
- **THEN** the exact instant it was advanced to is returned

#### Scenario: A later advance replaces an earlier one
- **GIVEN** a vehicle's cursor already holds one instant
- **WHEN** the cursor is advanced to a later instant
- **THEN** reading the cursor afterward returns the later instant, not the
  earlier one

#### Scenario: Cursors are isolated per vehicle
- **GIVEN** two vehicles, each with the mirror having advanced their cursor
  to different instants
- **WHEN** each vehicle's cursor is read
- **THEN** each returns only its own instant, never the other vehicle's

#### Scenario: Two vehicles of one account no longer share a cursor
- **GIVEN** one account holding two registered vehicles
- **WHEN** the mirror advances the cursor of one of them
- **THEN** the other vehicle's cursor is unchanged
- **AND** reading the other vehicle's cursor still reports the epoch if it has never been
  advanced

### Requirement: Supercharger Session Vehicle Keying

The charging capability's Supercharger session store SHALL identify every stored session
by the vehicle that charged, and SHALL NOT record an owning account. Which vehicles a user
may see is already recorded by the account capability's own vehicle registry, so repeating
it on every session added nothing and split one car's history across accounts.

Every stored session SHALL carry a vehicle identifier; the store SHALL NOT hold a session
without one. With no account identifier stored, a session with no vehicle identifier could
not be reached by any read this capability offers, so storing one would only keep rows
nobody can read.

A session identifier SHALL identify at most one stored session, across the whole store
rather than within one account. A Supercharger session happened to exactly one vehicle, so
two stored copies of one session are two records of one event. Where the previous
account-scoped rule permitted such a pair — one copy per account for a vehicle two users
had both registered — the capability SHALL keep exactly one of them, preferring a copy
that carries human-entered battery percentages, because every other value on the record is
re-derived from the mirrored source on the next synchronization.

#### Scenario: A mirrored session is stored once, identified by its vehicle

- **GIVEN** an empty Supercharger session store
- **WHEN** one session for a registered vehicle is mirrored
- **THEN** exactly one session is stored
- **AND** it carries that vehicle's identifier
- **AND** it carries no account identifier

#### Scenario: The same session mirrored again under a different vehicle does not duplicate

- **GIVEN** a stored session identified by a session identifier and one vehicle
- **WHEN** the same session identifier is mirrored again naming a different vehicle
- **THEN** the store still holds exactly one session for that session identifier
- **AND** that session names the most recently mirrored vehicle

#### Scenario: A session read returns only the requested vehicle's sessions

- **GIVEN** stored sessions for two different vehicles
- **WHEN** a caller requests sessions for one of them
- **THEN** only that vehicle's sessions are returned
- **AND** no session belonging to the other vehicle appears, regardless of which account
  either vehicle belongs to

### Requirement: Supercharger Port Vehicle Scoping

Every public port the charging capability exposes over its Supercharger session store SHALL
identify its scope by the vehicle's Tesla numeric identifier alone, and SHALL NOT take an
account identifier. This covers the mirror write, the three session reads, and the
battery-percentage verification write.

The verification write SHALL keep a scope rather than lose one: it SHALL match a session
by BOTH its own identifier and the caller-supplied vehicle identifier, so a caller that
names a vehicle the session does not belong to changes nothing. A verification write
naming a mismatched vehicle SHALL be reported to the caller exactly as an unknown session
identifier is, so that "not yours" and "does not exist" are indistinguishable from outside.

The verification write's vehicle identifier SHALL be a value that can only exist once the
caller has already proven it owns that vehicle — never a bare identifier a caller could
supply without that proof. This is stricter than the scoping rule above asks of the mirror
write and the three session reads, which still accept a bare vehicle identifier: the
verification write is the only Supercharger port that mutates a human-entered record, so it
is the one port this capability requires proof, rather than an unverified claim, for.

The mirror write SHALL NOT validate an owning account across the sessions it is given,
because no session carries one; an empty set of sessions SHALL remain a successful no-op.

#### Scenario: A verification write for the wrong vehicle changes nothing

- **GIVEN** a stored session belonging to one vehicle, with no battery percentages recorded
- **WHEN** a caller asks to verify that session's percentages while naming a different
  vehicle
- **THEN** an error indistinguishable from "no such session" is returned
- **AND** the stored session's battery percentages and lifecycle status are unchanged

#### Scenario: A verification write for the right vehicle succeeds

- **GIVEN** the same stored session
- **WHEN** a caller asks to verify its percentages while naming the vehicle the session
  belongs to
- **THEN** the percentages are stored
- **AND** the returned session carries the vehicle's identifier as a plain value, never an
  absent one

#### Scenario: A verification write cannot be issued without proof of ownership

- **GIVEN** a caller that has not established which vehicles it may act on behalf of
- **WHEN** it attempts to issue a verification write
- **THEN** it has no way to construct the vehicle identifier the write requires
- **AND** the attempt does not compile, rather than reaching the store and failing there

#### Scenario: Callers never reach the Supercharger tables directly

- **GIVEN** any caller that needs to read or write a Supercharger session
- **WHEN** it obtains or changes that data
- **THEN** it does so exclusively through this capability's public ports
- **AND** it imports no package from `internal/charging/db`

