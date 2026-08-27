# Design — RM31-charging-add-session-verification-port

> Numbering note: this document's decisions are **D1, D2, …**, scoped to this change only —
> distinct from the roadmap's own **D1–D7** in
> `openspec/roadmaps/RM31-supercharger-session-verification.md` (cited as **roadmap D1**,
> …) and from RM29 tier 6's / RM30 tier 1's archived decisions (cited as **RM29 D1**, …/
> **RM30 D1**, …). **D1–D5 transcribe binding instructions from the leader's dispatch**
> (which itself transcribes the roadmap's tier-1 proposal prompt); each says so in its
> heading. **D6–D11 are decisions this artifacts pass had to make** to turn those into a
> buildable change — these are the five "open questions the design MUST settle explicitly"
> from the dispatch, plus one implementation detail (D11) the others depend on.

## Context

Five facts about the existing schema and code shape everything below.

1. **The five verification columns already exist and have never been written by
   anything in this repository.** RM29 tier 6's migration
   (`20260823000001_add_charge_sessions.sql`) created `start_battery_pct`,
   `end_battery_pct`, `battery_pct_source`, `start_battery_pct_est`, `end_battery_pct_est`
   as charging-owned columns, deliberately unwritable by the nightly sync
   (`SessionMirror` has no field for them — RM29 design.md D6). No later tier gave them a
   writer either: RM30 tier 1 added only `SessionReader`. This tier is the first writer
   for any of the five.

2. **`MirrorChargeSession` is the precedent for "protection by query shape, not by
   comment," and this port must apply the identical discipline in reverse.** Its own
   `LOAD-BEARING` comment (`db/query.sql`) states the five percentage columns are absent
   from its `INSERT` column list and its `ON CONFLICT DO UPDATE SET` clause — the sync
   *cannot* touch them because the query never names them, not because a rule asks it
   not to. This port's `UPDATE ... SET` clause must earn the same property for the
   *other* sixteen columns: it must name only `start_battery_pct`, `end_battery_pct`,
   `battery_pct_source`, and `updated_at`, so that even `start_battery_pct_est` /
   `end_battery_pct_est` — the two columns most tempting to "complete the pattern" with —
   are structurally unreachable from this query, not merely undocumented as targets.

3. **`Writer.Update` (`manual_charge_entries`) is the sibling port the dispatch names to
   mirror for scoping and not-found semantics.** Its `UPDATE` scopes with
   `WHERE id = @id AND account_id = @account_id` (the table's primary key plus the tenant
   column) and returns `RETURNING *` as a sqlc `:one` query; when zero rows match — wrong
   id, wrong account, or both — pgx's `RETURNING`-with-no-match behavior surfaces as
   `pgx.ErrNoRows`, which `writerService.Update` wraps with no special-casing:
   `fmt.Errorf("charging: update entry: %w", err)`. `TestUpdate_CrossAccountIsNoOp`
   (`db_integration_test.go`) is the existing proof this is deliberate, not an oversight —
   its own comment reads "Expected: error (pgx returns pgx.ErrNoRows when RETURNING *
   finds 0 rows)." `charge_sessions` carries the identical shape: a UUID primary key
   (`id`) plus a leading `account_id` column, so the same `WHERE id = @id AND account_id
   = @account_id` scoping applies unchanged.

4. **`charge_sessions_pct_source_required` governs which combinations of the three
   target columns are legal states, and the port — not the caller — must guarantee it.**
   The migration's `CHECK`:
   ```sql
   CONSTRAINT charge_sessions_pct_source_required CHECK (
       (start_battery_pct IS NULL AND end_battery_pct IS NULL)
       OR battery_pct_source IS NOT NULL
   )
   ```
   means any row with at least one percentage set must carry a non-null source, and a row
   with both percentages null may carry either a null or non-null source (the CHECK does
   not forbid a stray non-null source on an all-null row, but nothing in this design ever
   produces one — see D7). The dispatch is explicit the port takes no `source` parameter
   at all, so the three-column write must derive `battery_pct_source` from
   `startBatteryPct`/`endBatteryPct` inside the Go layer, in the same call, not leave a
   caller to compute it and risk violating the CHECK (D2, D7).

5. **No CHECK constrains the relative order of `start_battery_pct` and
   `end_battery_pct`, and this is legible as a deliberate design choice, not an
   oversight.** The migration's own comment block explains the table's minimal-constraint
   philosophy for the columns it mirrors ("a mirror stricter than its source could not
   repair a source row it refuses to accept") — but the battery-percentage columns are
   **not** mirrored, they are charging-owned from birth, so that specific rationale does
   not extend to them directly. What does extend: the migration had complete freedom to
   add a `start_battery_pct <= end_battery_pct`-shaped CHECK to these charging-owned
   columns (nothing about "no stricter than source" would have blocked it, since there is
   no source to be stricter than) and chose not to. The dispatch surfaces this as an open
   question for this design to resolve explicitly rather than silently either enforcing or
   ignoring it (D8).

## Goals / Non-Goals

**Goals**

- `internal/charging` exposes a write port that updates exactly `start_battery_pct`,
  `end_battery_pct`, `battery_pct_source`, and `updated_at` on one account-scoped
  `charge_sessions` row — structurally incapable of touching any other column, including
  the two `_est` columns (**D1**).
- `battery_pct_source` is always computed by the port, never supplied by a caller:
  `"user_verified"` when either percentage is set, `NULL` when both are cleared (**D2**,
  **D7**).
- Each non-nil percentage is validated to `[0, 100]` in Go before the query runs — the DB
  `CHECK` is the backstop (**D5**).
- The updated `Session` is returned so a caller can re-render without a second read
  (**D4**).
- Not-found and wrong-account are indistinguishable, exactly matching `Writer.Update`
  (**D5**, restated as D9's scoping detail).

**Non-Goals**

- Any database migration, index, column, or constraint. All five columns already exist;
  the existing primary key already serves this query's `WHERE` clause (**D11**).
- Enforcing `start_battery_pct <= end_battery_pct`, or any other cross-field ordering
  (**D8**).
- Wiring anything into `cmd/web` or `internal/gateway`. Tier 4 is the consumer; this tier
  has no caller of its own new port.
- Any change to `SessionWriter`, `SessionMirror`, `MirrorChargeSession`, or
  `SessionReader`. The nightly sync path and the existing read path are not touched.
- Adding `start_battery_pct_est` / `end_battery_pct_est` write support. Roadmap Decision 3
  keeps them permanently `NULL` until an estimator exists — out of scope for every tier of
  this roadmap.

---

## Decisions

### D1 — The `UPDATE`'s `SET` clause names exactly three columns plus `updated_at` (binding — dispatch)

`VerifyChargeSession`'s `SET` clause is:

```sql
SET
    start_battery_pct  = @start_battery_pct,
    end_battery_pct    = @end_battery_pct,
    battery_pct_source = @battery_pct_source,
    updated_at         = now()
```

No other column appears — not `start_battery_pct_est`/`end_battery_pct_est`, not any of
the eleven mirrored columns. This is the mirror image of `MirrorChargeSession`'s own
protection (Context #2): that query cannot write these three (plus the two `_est`
columns) because they are absent from its clause; this query cannot write anything *but*
these three (plus `updated_at`) for the identical reason, applied to the complementary set
of columns. A future edit that tried to "also refresh the `_est` snapshot while we're
here" would have to add a new column to this `SET` clause explicitly — a visible, reviewable
diff — not flip a flag or remove a comment.

### D2 — `battery_pct_source` is computed by the port; the method takes no source parameter (binding — dispatch)

`SessionVerifier.VerifySession`'s signature is
`VerifySession(ctx, accountID, id uuid.UUID, startBatteryPct, endBatteryPct *int) (Session, error)`
— no third `*string` for source. Inside the implementation:

```go
var source *string
if startBatteryPct != nil || endBatteryPct != nil {
    s := "user_verified"
    source = &s
}
```

`source` is then mapped through the existing `stringPtrToPgText` helper (`service.go`) —
`nil` becomes SQL `NULL`, non-nil becomes `'user_verified'`. `"polled"` is a documented
future value (the column comment: "a future measured-SOC path, not implemented") that
literally cannot be written by any code path that exists after this change — there is no
parameter to carry it.

### D3 — Range validation happens before the query, not instead of the CHECK (binding — dispatch)

```go
if startBatteryPct != nil && (*startBatteryPct < 0 || *startBatteryPct > 100) {
    return Session{}, fmt.Errorf("charging: start_battery_pct %d out of range [0,100]", *startBatteryPct)
}
if endBatteryPct != nil && (*endBatteryPct < 0 || *endBatteryPct > 100) {
    return Session{}, fmt.Errorf("charging: end_battery_pct %d out of range [0,100]", *endBatteryPct)
}
```

Both checks run before `v.q.VerifyChargeSession` is called, so an out-of-range value never
reaches the database — the dispatch's "the DB CHECK is the backstop, not the error
message" instruction, applied literally: the DB's `SMALLINT CHECK (... BETWEEN 0 AND 100)`
still exists and still protects the column, but a caller sees a clear Go error naming the
offending field and value instead of a raw `23514 check_violation` SQLSTATE.

### D4 — The updated `Session` is returned via the existing `rowToSession` mapper (binding — dispatch)

`VerifyChargeSession` is a sqlc `:one` query with `RETURNING *`, returning the same
generated `chargingdb.ChargeSession` row type `ListSessionsByVehicleBetween` already
returns. `sessionVerifier.VerifySession` reuses `rowToSession` (`session_reader.go`,
same package) to map it to a domain `Session` — no new mapping code, no duplicated
`pgtype` conversion logic. This is the same "no second round-trip" contract
`writerService.Create`/`Update` already give their callers for `manual_charge_entries`.

### D5 — Scoping and not-found semantics mirror `Writer.Update` exactly, with no NotFound wrapping (binding — dispatch)

`WHERE id = @id AND account_id = @account_id`, `:one` with `RETURNING *`. Zero rows
matched — whether because `id` does not exist at all, or exists under a different account
— surfaces as `pgx.ErrNoRows`, wrapped identically to `writerService.Update`:
`fmt.Errorf("charging: verify session: %w", err)`. No sentinel error, no distinguishing
"not found" from "wrong account" — `Writer.Update` makes neither distinction and the
dispatch says to mirror it, not to improve on it. `TestUpdate_CrossAccountIsNoOp` is the
existing precedent this design's own T6/T7 (below) reproduce for `charge_sessions`.

---

### D6 — A partial verification (one percentage set, the other left `NULL`) is legal, and the port writes exactly what it is given (open question 1)

**Decision: legal.** `charge_sessions_pct_source_required`'s `CHECK` only requires a
non-null source when **at least one** percentage is set — it does not require both. A
human correcting a session mid-charge (e.g. they only know the start reading right now)
is a real, expected use of this port, not an edge case to reject.

**Consequence for the method's contract:** every call supplies the row's **final** state
for both parameters, exactly like `Writer.Update` requires every mutable `Entry` field on
every call — this is not a partial-patch method. A caller wanting to add
`end_battery_pct` to a session that already has `start_battery_pct` verified must
re-supply the existing `start_battery_pct` value (fetched via `SessionReader`) alongside
the new `end_battery_pct`, or that column is overwritten to `NULL` (D7 explains why that
overwrite is itself the correct, intentional behavior for the all-`NULL` case — the same
mechanism, not a special case). This mirrors `writerService.Update`'s own contract
precisely: it has never supported "change only `price`, leave the rest as stored" either.

### D7 — Clearing both percentages to `NULL` also `NULL`s the source, as the port's own contract, not the caller's responsibility (open question 2)

**Decision: the port computes this, unconditionally, in the same branch that computes
`"user_verified"` (D2's code block).** Calling `VerifySession(ctx, accountID, id, nil,
nil)` writes `battery_pct_source = NULL` in the identical statement that writes both
percentages `NULL` — there is no intermediate state where a caller could observe or
create a row with both percentages `NULL` and a non-null source through this port. This
was named as a required contract, not a suggestion, precisely because
`charge_sessions_pct_source_required`'s `CHECK` would reject any statement that tried to
`NULL` both percentages while leaving a stale `'user_verified'` behind, and pushing that
computation onto the gateway caller in tier 4 would duplicate a rule this module alone
should own (RM29 D1's general principle — this module owns the write semantics of columns
it owns).

### D8 — No ordering between `start_battery_pct` and `end_battery_pct` is enforced by the port (open question 3)

**Decision: unenforced, deliberately, matching the table's own deliberate absence of such
a CHECK** (Context #5). Three reasons, none of them "it was easier not to":

1. **The table had every opportunity to enforce this and did not.** These columns are not
   mirrored — nothing forced the migration's minimal-constraint philosophy onto them the
   way it did onto `charge_start_date_time`/`charge_stop_date_time`. Their CHECK set
   (range `[0,100]` each, provenance-required) was chosen deliberately and an ordering
   CHECK was not among them. A port stricter than the schema it sits in front of would be
   inventing a rule the schema's own author chose not to make.
2. **A legitimate correction workflow can transiently pass through an "inverted" pair.**
   A human fixing a data-entry mistake — say `end_battery_pct` was mistyped lower than
   reality — corrects one field at a time across two separate `VerifySession` calls (D6's
   contract: every call needs both values, so a two-step fix is: call 1 sets the wrong
   pair as an intermediate state matching what's currently stored plus one corrected
   field, call 2 finishes it). Rejecting an intermediate state that happens to have
   `start > end` would block exactly the correction workflow this port exists to serve,
   for a case the table itself does not consider invalid.
3. **A reversed charge is not necessarily wrong.** Nothing about Tesla's own session data
   guarantees `end >= start` in every real-world case (the migration's own comment on the
   session-time-window pair notes "the source permits a reversed window" for the same
   underlying reason: it isn't this module's job to reject data the source might
   legitimately report). Battery percentage is a proxy the same way — the module has no
   authority to declare a human-entered pair impossible.

**What this decision does not do:** it does not weaken the `[0, 100]` range check (D3,
DB-backed) or the provenance requirement (D2/D7, DB-backed) — both remain enforced by two
independent layers. Only the *relative* ordering of the two fields is left unconstrained,
exactly matching the schema.

### D9 — `SessionVerifier` is a new, separate interface — not a method added to `SessionWriter` (open question 5)

**Decision: separate interface, `SessionVerifier`, its own file
(`session_verifier.go`).** The alternative — adding a `VerifySession`-shaped method
directly to `SessionWriter` — was rejected for three reasons:

1. **`SessionWriter`'s doc comment names its caller and that caller is not this port's
   caller.** `charging.go`: *"SessionWriter is the synchronization port called by the
   nightly orchestrator... The gateway never calls this."* Adding a gateway-called,
   human-triggered method to an interface whose doc comment states outright that the
   gateway never calls it would make that sentence false the moment this tier lands,
   forcing an edit to a comment that currently earns its confidence from being true by
   construction (only one method, one documented caller). A second interface keeps that
   sentence true forever.
2. **The whole point of RM29 D6 is a *compile-time* separation between the nightly-sync
   surface and everything else — this port continues that separation instead of muddying
   it.** RM29 D6's mechanism was "`SessionMirror` has no field for these columns, so the
   sync cannot bind one even by accident" — a statement about the **type**, not the
   interface name. Adding `VerifySession` to `SessionWriter` would not break that specific
   guarantee (the mirror's own method still takes `SessionMirror`, still has no field), but
   it would hand `cmd/web`'s nightly-orchestrator wiring path and the gateway's
   human-edit wiring path the *same* Go type to depend on, which is exactly the kind of
   "one fat interface, two callers with different trust models" the AI-efficiency
   "closed, small vocabularies" principle (`CLAUDE.md` §Non-negotiables) argues against:
   the type itself should communicate "which caller may hold this," not just "which
   fields are absent from the struct it accepts."
3. **This module's own precedent is one port per access pattern, not one port per
   table.** `charge_sessions` already has two interfaces or (`SessionWriter`,
   `SessionReader`) — a table entirely reasonably having a *third*, narrower interface for
   a third access pattern (human write, as distinct from batch write and from read) is
   consistent with, not a departure from, how this module already treats
   `manual_charge_entries`'s own `Writer`/`Reader` split.

`NewSessionVerifier(pool *pgxpool.Pool) SessionVerifier`, declared in `charging.go`,
implemented in `session_verifier.go`, follows `NewSessionWriter`/`NewSessionReader`'s
exact pattern (forward-declared constructor calling an unexported `newSessionVerifier`).

### D10 — `battery_pct_source`'s literal, `"user_verified"`, is a package-level constant, not a magic string repeated at each call site (implementation detail, not asked for explicitly but follows from D2)

```go
const batteryPctSourceUserVerified = "user_verified"
```

declared once in `session_verifier.go`, used in the single branch D2 describes. This is
the same "closed, small vocabulary" reasoning D9 invokes, applied at the string-literal
level: a single named constant is what makes "this port can never write anything but
`user_verified`" a property with exactly one place to verify, rather than a claim that
happens to be true today because nobody has typed the literal wrong yet.

### D11 — No database object is created or altered; the existing primary key already serves this query (open question 4's scoping half, plus the blanket "no new DB object" instruction)

The dispatch states plainly: "No new DB object — no migration," and instructs that if
this design concluded otherwise, it must be reported as blocked rather than implemented,
since a new/changed database object trips the project's `database` design gate
(`CLAUDE.md` §Pipeline config → Design-Gates). That conclusion is not reached here.
`VerifyChargeSession`'s `WHERE id = @id AND account_id = @account_id` is a point lookup on
the table's own `PRIMARY KEY (id)` — every Postgres table with a declared primary key
carries its supporting unique B-tree index automatically; no `CREATE INDEX` of any kind is
needed for a single-row equality match on that column (see §"Database Changes" below for
the full justification). This also settles which identifier the port scopes by: the row's
UUID primary key (`id`), matching `Writer.Update`'s own use of `manual_charge_entries.id`
— **not** the Tesla `session_id`, which is only unique per-account via a separate
`UNIQUE (account_id, session_id)` constraint and would require a different `WHERE` shape
for no benefit, since every caller of this port already holds a `Session` (with its `.ID`)
from a prior `SessionReader` read.

---

## Database Changes

**None.** No migration file is added or edited. This section exists — per
`openspec/config.yaml`'s blanket rule that "design.md is REQUIRED for any DB-touching
change" — to prove the existing schema already serves the new query, not to record a
schema change.

### The query (`internal/charging/db/query.sql`, appended)

```sql
-- name: VerifyChargeSession :one
-- Update the human-owned verification channel on one account-scoped charge session:
-- start_battery_pct, end_battery_pct, and battery_pct_source — plus updated_at. No other
-- column is in this SET clause, INCLUDING start_battery_pct_est/end_battery_pct_est —
-- this is the mirror image of MirrorChargeSession's protection (that query cannot touch
-- these three; this query cannot touch anything else), by the query's shape, not by a
-- comment a reviewer has to notice (design.md D1).
--
-- @battery_pct_source is COMPUTED IN GO (design.md D2/D7), never accepted from a caller:
-- "user_verified" when either percentage is non-nil, NULL when both are nil — satisfying
-- charge_sessions_pct_source_required in the same statement that clears or sets the
-- percentages, so no intermediate row state can violate it.
--
-- WHERE id = @id AND account_id = @account_id mirrors UpdateEntry's scoping exactly
-- (design.md D5/D11): a point lookup on the table's PRIMARY KEY plus its leading tenant
-- column. Zero rows matched — unknown id or wrong account, indistinguishable — surfaces
-- to the caller as pgx.ErrNoRows, exactly like UpdateEntry's own not-found behavior
-- (TestUpdate_CrossAccountIsNoOp is the existing precedent for this shape).
UPDATE charge_sessions
SET
    start_battery_pct  = @start_battery_pct,
    end_battery_pct    = @end_battery_pct,
    battery_pct_source = @battery_pct_source,
    updated_at         = now()
WHERE id = @id
  AND account_id = @account_id
RETURNING *;
```

**Go-side call shape** (`internal/charging/session_verifier.go`):

```go
func (v *sessionVerifier) VerifySession(ctx context.Context, accountID uuid.UUID, id uuid.UUID, startBatteryPct, endBatteryPct *int) (Session, error) {
	if startBatteryPct != nil && (*startBatteryPct < 0 || *startBatteryPct > 100) {
		return Session{}, fmt.Errorf("charging: start_battery_pct %d out of range [0,100]", *startBatteryPct)
	}
	if endBatteryPct != nil && (*endBatteryPct < 0 || *endBatteryPct > 100) {
		return Session{}, fmt.Errorf("charging: end_battery_pct %d out of range [0,100]", *endBatteryPct)
	}

	var source *string
	if startBatteryPct != nil || endBatteryPct != nil {
		s := batteryPctSourceUserVerified
		source = &s
	}

	row, err := v.q.VerifyChargeSession(ctx, chargingdb.VerifyChargeSessionParams{
		ID:               id,
		AccountID:        accountID,
		StartBatteryPct:  intPtrToPgInt2(startBatteryPct),
		EndBatteryPct:    intPtrToPgInt2(endBatteryPct),
		BatteryPctSource: stringPtrToPgText(source),
	})
	if err != nil {
		return Session{}, fmt.Errorf("charging: verify session: %w", err)
	}
	return rowToSession(row), nil
}
```

Reuses three existing helpers with zero new mapping code: `intPtrToPgInt2` (`service.go`),
`stringPtrToPgText` (`service.go`), `rowToSession` (`session_reader.go`) — all already in
package `charging`, none of them touched by this change.

### Why no index is needed — point lookup on the primary key

`charge_sessions`'s `CREATE TABLE` declares `id UUID PRIMARY KEY DEFAULT
gen_random_uuid()`. Postgres creates and maintains a unique B-tree index on every declared
primary key automatically, at table-creation time — this is not something RM29 tier 6's
migration had to ask for separately, and it is not something this change needs to add.
`WHERE id = @id AND account_id = @account_id` therefore executes as:

| Query clause | Served by | Role |
|---|---|---|
| `WHERE id = @id` | the implicit `charge_sessions_pkey` unique index | Single-row equality lookup — at most one match, by definition of `PRIMARY KEY`. |
| `AND account_id = @account_id` | residual filter on the one fetched row | A defense-in-depth tenant check, not a filter that needs its own index — the `id` equality already narrows to zero or one row before this predicate is even evaluated. |

This is a `:one` point-write, not a range scan — there is no `ORDER BY`, no multi-row
result, and no read-heavy dashboard access pattern to optimize (`ai/architecture.md` §7's
read/write asymmetry does not apply to a single-row point update triggered by an explicit
human action). No new index, column, or constraint is required (**D11**).

---

## Test Contract (expected values authored before implementation, per `ai/go-conventions.md`)

All tests are `DATABASE_URL`-gated integration tests in `internal/charging`'s existing
external test package (`package charging_test`), in a new
`db_session_verifier_integration_test.go`. Fixtures use fresh `uuid.New()` account ids and
`session_id`s in the **950001–950099** range — disjoint from RM29 tier 6's 920001–920099,
RM30 tier 1's 940001–940099, and the real backfilled `734860294`. Sessions are seeded via
`SessionWriter.MirrorSessions`, verified via `SessionVerifier.VerifySession`, and read back
either through the returned `Session` or through `SessionReader.ListSessionsByVehicleBetween`
/ direct SQL — never through `chargingdb.ChargeSession` (`pgtype` never appears in this
file, `internal/charging/AGENTS.md` §Testing Notes).

Baseline fixture **V1**: one session, `session_id = 950001`, seeded under a fresh
`acctA`/`TeslaID = 950001`, with no battery percentages (mirroring RM30's baseline shape:
non-nil `SiteLocationName`, `EnergyKWh`, `TotalCost`, `Currency`, `IsPaid`, all five
percentage columns `NULL`).

**T1. A normal verify sets both percentages and the source, and returns them.** Call
`VerifySession(ctx, acctA, v1.ID, ptr(20), ptr(80))`. Expected: no error;
`Session.StartBatteryPct != nil && *StartBatteryPct == 20`;
`Session.EndBatteryPct != nil && *EndBatteryPct == 80`;
`Session.BatteryPctSource != nil && *BatteryPctSource == "user_verified"`;
`Session.StartBatteryPctEst == nil` and `Session.EndBatteryPctEst == nil` (untouched —
D1); `Session.UpdatedAt` is strictly after V1's `CreatedAt`/pre-call `UpdatedAt`.

**T2. A partial verify — start only — leaves end `NULL` and still sets the source.** On a
fresh session (`session_id = 950002`, `NULL`/`NULL` baseline), call
`VerifySession(ctx, acctA, v2.ID, ptr(35), nil)`. Expected: no error;
`*StartBatteryPct == 35`; `EndBatteryPct == nil`;
`*BatteryPctSource == "user_verified"` (D6 — a non-nil source is written even though only
one percentage is set).

**T3. A partial verify — end only — leaves start `NULL` and still sets the source
(symmetric to T2).** On a fresh session (`session_id = 950003`), call
`VerifySession(ctx, acctA, v3.ID, nil, ptr(90))`. Expected: no error; `StartBatteryPct ==
nil`; `*EndBatteryPct == 90`; `*BatteryPctSource == "user_verified"` — proving the source
computation is not conditioned specifically on `startBatteryPct` being the one that is
non-nil.

**T4. Clearing both previously-set percentages also clears the source.** Starting from
T1's session (`v1`, now `20`/`80`/`"user_verified"`), call `VerifySession(ctx, acctA,
v1.ID, nil, nil)`. Expected: no error (proves `charge_sessions_pct_source_required` is
satisfiable through this exact call shape); `StartBatteryPct == nil`; `EndBatteryPct ==
nil`; `BatteryPctSource == nil` (D7 — the source is cleared in the same call, not left
stale).

**T5. An out-of-range percentage is rejected before the query runs, and the row is
unchanged.** On a fresh session (`session_id = 950005`), capture its `UpdatedAt`. Call
`VerifySession(ctx, acctA, v5.ID, ptr(101), ptr(50))`. Expected: non-nil error, message
contains `"start_battery_pct"` and `"101"`; a direct-SQL re-fetch of the row shows
`start_battery_pct`/`end_battery_pct`/`battery_pct_source`/`updated_at` all **identical**
to before the call. Repeat with `VerifySession(ctx, acctA, v5.ID, ptr(50), ptr(-1))`:
non-nil error, message contains `"end_battery_pct"` and `"-1"`; row again unchanged. Both
sub-cases prove validation happens strictly before any database round-trip.

**T6. A wrong-account id is rejected exactly like `Writer.Update`'s own precedent.** Seed
a session under `acctA` (`session_id = 950006`) and a distinct `acctB`. Call
`VerifySession(ctx, acctB, v6.ID, ptr(10), ptr(20))` (v6's id belongs to `acctA`, called
with `acctB`'s account id). Expected: non-nil error (wraps `pgx.ErrNoRows`); a direct-SQL
re-fetch of v6's row under `acctA` shows all three target columns and `updated_at`
unchanged — the call had zero effect, mirroring `TestUpdate_CrossAccountIsNoOp`.

**T7. An unknown id produces the identical error shape as T6 — not-found and
wrong-account are indistinguishable (design.md D5).** Call
`VerifySession(ctx, acctA, uuid.New(), ptr(10), ptr(20))` with a fresh random id that
belongs to no session at all. Expected: non-nil error, `errors.Is(err, pgx.ErrNoRows)`
after unwrapping — same underlying sentinel as T6, proving the port makes no attempt to
distinguish the two cases.

**T8. A successful verify changes only the three target columns and `updated_at` — every
other column is bit-identical before and after.** On a fresh session (`session_id =
950008`) carrying non-nil `SiteLocationName`, `EnergyKWh`, `TotalCost`, `Currency`,
`IsPaid`, and a known `TeslaID`, capture every column via direct SQL, call
`VerifySession(ctx, acctA, v8.ID, ptr(15), ptr(95))`, then re-fetch via direct SQL.
Expected: `id`, `account_id`, `vin`, `tesla_id`, `session_id`, `charge_start_date_time`,
`charge_stop_date_time`, `site_location_name`, `energy_kwh`, `total_cost`, `currency`,
`is_paid`, `start_battery_pct_est`, `end_battery_pct_est`, and `created_at` are **all
identical** before and after; only `start_battery_pct`, `end_battery_pct`,
`battery_pct_source`, and `updated_at` differ. This is the concrete proof of D1's
structural claim, not just an assertion that the query text looks right.

**T9. `updated_at` strictly advances on every successful call, including a
clear-to-`NULL` call that changes no visible percentage value.** Reuses T4's fixture:
capture `v1`'s `UpdatedAt` immediately after T1 (its first verify), then again after T4's
clear-to-`NULL` call. Expected: T4's `UpdatedAt` is strictly later than T1's — proving
`updated_at = now()` runs unconditionally in this query, the same way `UpdateEntry`'s does,
even when the net effect on the percentage columns is "no longer set."

### What must NOT change

- RM29 tier 6's existing `db_session_integration_test.go` (Groups B and C) and
  `db_backfill_integration_test.go` (Group A): not one assertion, fixture, or name.
- RM30 tier 1's existing `db_session_reader_integration_test.go`: unaffected — this
  change adds no file that touches it and renames no shared helper.
- `internal/charging`'s `manual_charge_entries` tests (`db_integration_test.go`,
  `charging_test.go`): unaffected.

---

## Risks / Trade-offs

- **`VerifySession` ships with zero callers this tier.** Standard for a roadmap's first
  tier (RM29 tier 6 shipped `SessionWriter` the same way; RM30 tier 1 shipped
  `SessionReader` the same way). The port is exercised only by its own integration tests
  until tier 4 lands; `go vet` and `make build` still cover it structurally.
- **Every call must supply both percentages' final values, not a delta (D6).** A gateway
  caller in tier 4 that wants to edit only one field must first read the current `Session`
  (via `SessionReader`) to get the other field's current value before calling
  `VerifySession` — this is one extra read on an already-rare, human-triggered write path,
  not a hot-path cost, and it is the same contract `writerService.Update` already imposes
  on every caller of `manual_charge_entries`'s `Writer.Update`.
- **No ordering enforcement means a caller can store `start_battery_pct > end_battery_pct`
  (D8).** Accepted deliberately — see D8's three reasons. If tier 4's UI wants to warn a
  human about this rather than silently accept it, that is a **display-layer** decision
  for the gateway module to make (e.g. a non-blocking inline hint), not a port-level
  rejection; this design does not preclude that UI choice, it only declines to enforce it
  at the write boundary.
- **`session_verifier.go` is a third small file alongside `session_writer.go`/
  `session_reader.go` that all wrap the same `chargingdb.Queries`.** Accepted as the
  correct cost of D9's separation — three narrow, single-purpose files are more legible
  and more safely wired than one file whose type serves two callers with different trust
  models.

## Verification signals

Per the binding `Test-Execution-Policy`, the assistant runs and reports: `go build ./...`,
`go vet ./...`, `gofmt -l .`, `make build`, `make vet`, `make bins`, `make sqlc` (after the
new query is appended), and the three standalone guards — `make ui-guard` (no-op: no
gateway markup), `make i18n-guard` (no-op: no user-facing string), `make money-guard`
(no-op: this change touches no gateway file). It also runs
`openspec validate --changes --strict`.

The owner alone runs the suite:

```
go test ./internal/charging/...
```

or the full suite:

```
go test ./...
```

Until the owner runs these and reports the results, this tier's implementation status is
**awaiting-user-verification**, never "done".
