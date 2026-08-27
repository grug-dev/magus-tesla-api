Source: MAG-19 — https://linear.app/magus-monitor/issue/MAG-19/allow-editing-supercharger-sessions
Roadmap: openspec/roadmaps/RM31-supercharger-session-verification.md
Tier: 1 of 4 (`charging` gains a `SessionVerifier` write port for the human-owned
battery-percentage columns; tier 2 is `analytics`'s Supercharger-source switch, tier 3 is
`gateway`'s display-only columns, tier 4 is the gateway edit itself, which depends on this
tier and tier 2). Tier 1 has no dependency on any other tier (roadmap D7 — "Tiers 1, 2 and
3 have no dependency on one another and could be done in any order or in parallel; only
tier 4 needs all three").
Design gate: **not tripped.** This change creates or alters no database object — no
migration, no table, no column, no index, no constraint. All five verification columns
already exist (`20260823000001_add_charge_sessions.sql`, RM29 tier 6). `openspec/config.yaml`'s
blanket "design.md is REQUIRED for any DB-touching change" still applies — this change adds
a new `UPDATE` query — so design.md documents the full query and the existing index that
already serves its `WHERE` clause, without requiring the owner's gate confirmation a schema
change would need.
Unit tests: **integration-only**, matching RM29 tier 6 / RM30 tier 1's precedent for this
module. This tier's one new piece of Go-side logic — rejecting an out-of-range percentage
before the query runs — is validated the same way `writerService.Create`/`Update`'s existing
"`location_kind` is required" Go-side check already is: through the `DATABASE_URL`-gated
integration suite (Test Contract T5), not a separate offline unit test. `Session` gains no
new value-receiver method, so there is nothing here for `charging_test.go`'s existing
offline-unit-test file to cover.

## Why

RM29 tier 6 gave `charge_sessions` five battery-percentage columns and protected them from
the nightly mirror by construction — `SessionMirror` has no field for them, so the sync
cannot bind one even by accident (RM29 design.md D6). But nothing in this repository has
ever been able to **write** them either: the columns have sat unwritable since RM29
shipped, waiting for the verification UI that is backlog item 11 (RM27 D6, carried
forward). MAG-19 names that UI's first requirement — a human must be able to correct a
session's start/end battery percentage — and RM31's roadmap Decision 1 makes that
correction land where `analytics` will read it (`charge_sessions`, not
`telemetry.supercharger_sessions`), so the edit is only useful once this module exposes a
port to make it.

This tier builds only the port: an account-scoped write that touches exactly the three
columns a human owns (`start_battery_pct`, `end_battery_pct`, `battery_pct_source`),
protected by the same "absent from the query's shape" discipline that
`MirrorChargeSession` already established for the opposite direction — a query that cannot
touch the other sixteen columns because they are not in its `SET` clause, not because a
comment asks a caller not to.

## What Changes

**Additive only — no schema change.**

- **ADDED** — `charging.SessionVerifier`, a one-method write port:
  `VerifySession(ctx, accountID, id uuid.UUID, startBatteryPct, endBatteryPct *int) (Session, error)`.
  Deliberately a **new, separate interface** from `SessionWriter` — see design.md D9 — not
  a method added to it: `SessionWriter` is documented as "the synchronization port called
  by the nightly orchestrator" and its whole RM29 D6 point is that it has no field for
  these columns; a human-triggered, gateway-called method living on that same interface
  would blur two callers with different trust models onto one type.
- **ADDED** — `charging.NewSessionVerifier(pool *pgxpool.Pool) SessionVerifier`, declared
  in `charging.go`, implemented in the new `internal/charging/session_verifier.go`
  (mirroring how `NewSessionWriter`/`session_writer.go` and
  `NewSessionReader`/`session_reader.go` are already split from `charging.go`).
- **ADDED** — `chargingdb.VerifyChargeSession`, one new sqlc query in
  `internal/charging/db/query.sql`, regenerated via `make sqlc`. Its `UPDATE ... SET`
  clause names **exactly** `start_battery_pct`, `end_battery_pct`, `battery_pct_source`,
  and `updated_at` — every other column, including `start_battery_pct_est` and
  `end_battery_pct_est`, is absent from the clause (design.md D1). No migration accompanies
  it — the existing primary key already serves the point-lookup `WHERE` clause (design.md
  §"Database Changes").
- **ADDED** — Go-side validation that each non-nil percentage is within `[0, 100]`,
  checked before the query runs; the DB's own `SMALLINT CHECK (... BETWEEN 0 AND 100)` is
  the backstop, not the error message (design.md D5).
- **ADDED** — Go-side computation of `battery_pct_source`: `"user_verified"` whenever
  either percentage is non-nil, `NULL` whenever both are nil (satisfying
  `charge_sessions_pct_source_required`). The port takes no `source` parameter — no
  caller can write `"polled"` (design.md D2).
- **CHANGED** — `internal/charging/AGENTS.md` — documents the new port under §Public
  Interface, updates §Data Ownership → `charge_sessions` (the verification columns are no
  longer unwritable by anything in this repository), and notes the new integration test
  file under §Testing Notes (docs-track-structural-change, `CLAUDE.md` §Non-negotiables).
- **UNCHANGED** — the migration file, `SessionMirror`, `SessionWriter`,
  `MirrorChargeSession`, `SessionReader`, `manual_charge_entries`, and every file outside
  `internal/charging/`. No column, index, constraint, port, or type is removed anywhere.

**Breaking:** no. Purely additive — a new type, a new interface, and a new query with no
existing caller yet.

**Modules affected:** `charging` only. No other module's code changes in this tier.
**`cmd/web` wiring** (constructing `charging.NewSessionVerifier(pool)` and injecting it
into `gateway.Deps`) is explicitly **not** part of this tier — it has no caller until
tier 4's gateway edit exists, so it is deferred to that change's own leader-owned
integration step, exactly as RM30 tier 1 deferred `NewSessionReader`'s wiring to its
tier 2 (precedent: RM30 proposal.md §"Modules affected").

## Read paths affected

Per `openspec/config.yaml` §proposal ("performance-sensitive proposals must name the read
path(s) they affect"):

- **No read path is added or changed.** This tier adds one **write** query
  (`VerifyChargeSession`), not a read. It is scoped by the table's primary key (`id`) plus
  `account_id` — a point lookup, not a range scan — and runs only when a human explicitly
  corrects one session, an operation with negligible frequency next to the nightly mirror
  or any dashboard read.
- **Existing read paths are untouched and unaffected in shape.**
  `SessionReader.ListSessionsByVehicleBetween` (`internal/charging`), `Reader`'s four
  methods (`manual_charge_entries`), and `analytics`'s current Supercharger read
  (`telemetry.SuperchargerReader`, unchanged until tier 2) keep the exact same query plans
  and indexes before and after this change. They will simply return the corrected
  percentages once this port has been called and (after tier 2 lands) once `analytics`
  reads from `charging` — neither consequence requires any interface or query change of
  its own in this tier.

## Impact

- **Affected specs:** `charge-session-log` (existing capability; **ADDED** requirement for
  the new correction behavior only — nothing about the existing collection/synchronization
  requirements changes).
- **Affected code:** `internal/charging/` (`charging.go`, `session_verifier.go` (new),
  `db/query.sql`, a new `db_session_verifier_integration_test.go`, `AGENTS.md`).
- **Design gate:** not tripped — see header. design.md still carries the query, the
  point-lookup justification, and the Test Contract, per `openspec/config.yaml`'s blanket
  design.md requirement for any DB-touching change.
- **Deferred, explicitly NOT in scope:** `analytics`'s source switch (tier 2), any gateway
  rendering or edit UI (tiers 3–4), `cmd/web` wiring (no caller until tier 4), any change
  to `SessionMirror`, `SessionWriter`, `SessionReader`, or the migration file, and any
  ordering constraint between `start_battery_pct` and `end_battery_pct` (design.md D8 —
  the table itself deliberately carries none).
