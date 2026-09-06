# Design — RM44-charging-add-change-detecting-mirror

## Context

`internal/charging` mirrors each Supercharger session into
`charging.supercharger_sessions`, one row per session, nightly. The mirror
query, `MirrorSuperchargerSession`, sets `updated_at = now()` on every row it
touches, every night — whether or not the row's data changed.

`internal/analytics.Recalculator.Reconcile` reads this same `updated_at`
column to decide which sessions are "new since last time." Because
`updated_at` always looks fresh, `Reconcile` always sees every session as new,
and recalculates a vehicle's full metrics history every night. Measured on
2026-09-05: a 70-day window, 51 of 51 rows rewritten, where only 4 days had
real new data.

This tier makes `updated_at` mean what it should mean: this row's data
actually changed. Tier 1 (`RM44-telemetry-add-query-logging`) already shipped
the log lines that will prove this works. A sibling tier
(`RM44-telemetry-add-change-detecting-upsert`) fixes the same problem one
layer up, in `internal/telemetry`. `internal/analytics` is not touched by
either tier — its read logic is already correct (roadmap D1).

This design is written against the roadmap's binding decisions D1–D3 and D10
(`openspec/roadmaps/RM44-incremental-supercharger-sync.md`). Those are not
re-opened here. This design's own decisions are numbered D1–D9 below,
independent of the roadmap's numbering.

## Goals / Non-Goals

**Goals:**
- `MirrorSuperchargerSession` advances `updated_at` only when the row's real
  data changed.
- The comparison catches every column mechanically. Adding a column to the
  table later must not be able to silently break this rule.
- No change to `MirrorSuperchargerSessionParams` and no new migration. The
  fix lives entirely inside one query's `ON CONFLICT DO UPDATE SET` clause.
- A human battery-percentage edit (`SessionVerifier.VerifySession`) keeps
  working exactly as it does today — untouched by this change.

**Non-Goals:**
- No change to `internal/telemetry` or `internal/analytics`.
- No new index, no schema change, no new migration file.
- No change to the watermark/bounded-read work — that is tier 4, a separate
  change, depending on this one.
- **This change does not start saving write-once columns when Tesla later
  reports a different value for one.** For example, if Tesla renames a
  Supercharger site after a session already recorded the old name, this
  change keeps today's behavior: the stored name never changes. The owner
  looked at changing this and chose to keep the write-once rule as-is. A
  future ticket can revisit it; it is not part of this change.

## Database Design (design gate — `database`)

### The exact final SQL

This is the full, final text of `MirrorSuperchargerSession` in
`internal/charging/db/query.sql`. Only the `ON CONFLICT DO UPDATE SET` clause
and the doc comment change. The `INSERT` and its column list are unchanged.

```sql
-- name: MirrorSuperchargerSession :exec
-- Upsert one Supercharger session's mirrorable subset. Called once per session, in
-- one transaction, by SessionWriter.MirrorSessions.
--
-- LOAD-BEARING: start_battery_pct, end_battery_pct, and battery_pct_source are ABSENT
-- from both the INSERT column list and the ON CONFLICT DO UPDATE SET clause. They
-- are human-owned; the nightly sync must never write, clear or overwrite one. Unlike
-- telemetry.UpsertSuperchargerSession — which relies on this comment alone —
-- charging.SessionMirror has no field for them either, so binding one here would not
-- even compile (RM29-charging-add-charge-sessions design.md D6). Do NOT "complete
-- the pattern" by adding them. The two frozen estimate columns formerly also excluded
-- here were dropped from the table entirely by RM41-charging-drop-estimate-columns —
-- there is no longer a column to guard.
--
-- The session's lifecycle status (added by RM41-charging-add-session-status, MAG-45)
-- is ALSO absent from both the INSERT column list and the ON CONFLICT DO UPDATE SET
-- clause — a freshly-mirrored session has no battery-percentage data yet, so it must
-- start IN_PROGRESS, which is exactly what the column's own DEFAULT provides with no
-- explicit value here. Only SessionVerifier.VerifySession ever writes this column
-- (see VerifySuperchargerSession's own doc comment).
--
-- THE REFRESH SET IS NOT A JUDGEMENT CALL. It is telemetry's own ON CONFLICT DO
-- UPDATE SET, minus raw_data (a column this table does not carry): energy_kwh,
-- total_cost, currency, is_paid, tesla_id, updated_at. Everything else mirrored —
-- charge_start_date_time, charge_stop_date_time, site_location_name, created_at — is
-- write-once at the source, so it is write-once here. site_location_name in
-- particular is NOT refreshed because telemetry does not refresh it, not because a
-- site name was judged unlikely to change (design.md D1's rule: a mirrored column
-- gets exactly its source column's write semantics).
--
-- UPDATED_AT NOW MEANS "THIS ROW'S DATA CHANGED" (RM44-charging-add-change-
-- detecting-mirror, MAG-48). An earlier version of this comment explained why a
-- hand-listed WHERE predicate was rejected: a column added to the SET clause later,
-- but forgotten in the WHERE, would silently stop being caught. The fix below answers
-- that objection instead of repeating it.
--
-- THE RULE: this comparison covers EXACTLY the columns this SET clause writes —
-- energy_kwh, total_cost, currency, is_paid, tesla_id — and nothing else. Every other
-- column is deny-listed below, because this query never refreshes it: comparing a
-- column this query does not write can only ever find a difference that never
-- resolves, which would make updated_at advance every night, forever, for no reason
-- (see design.md "The governing rule" for the live-tested failure this replaced).
--
-- Add a column to this SET clause later -> remove it from the deny-list below.
-- Add a column to the table that this SET clause does not write -> add it to the
-- deny-list. Forgetting either direction is caught by
-- db_mirror_schema_selfcheck_integration_test.go, which fails the moment a live
-- column belongs to neither list.
--
-- tesla_id stays INSIDE the comparison, deliberately: a NULL-to-value change on this
-- column is the signal that recovers a session once its vehicle re-registers (roadmap
-- D3). It must never be deny-listed.
INSERT INTO charging.supercharger_sessions (
    account_id, vin, tesla_id, session_id,
    charge_start_date_time, charge_stop_date_time,
    site_location_name, energy_kwh, total_cost, currency, is_paid
) VALUES (
    @account_id, @vin, @tesla_id, @session_id,
    @charge_start_date_time, @charge_stop_date_time,
    @site_location_name, @energy_kwh, @total_cost, @currency, @is_paid
)
ON CONFLICT (account_id, session_id) DO UPDATE SET
    energy_kwh = EXCLUDED.energy_kwh,
    total_cost = EXCLUDED.total_cost,
    currency   = EXCLUDED.currency,
    is_paid    = EXCLUDED.is_paid,
    tesla_id   = EXCLUDED.tesla_id,
    updated_at = CASE
        WHEN to_jsonb(supercharger_sessions.*) - '{id,account_id,vin,session_id,charge_start_date_time,charge_stop_date_time,site_location_name,start_battery_pct,end_battery_pct,battery_pct_source,created_at,updated_at,inferred_capacity_kwh_calc,status}'::text[]
             IS DISTINCT FROM
             to_jsonb(EXCLUDED.*) - '{id,account_id,vin,session_id,charge_start_date_time,charge_stop_date_time,site_location_name,start_battery_pct,end_battery_pct,battery_pct_source,created_at,updated_at,inferred_capacity_kwh_calc,status}'::text[]
        THEN now()
        ELSE supercharger_sessions.updated_at
    END;
```

This SQL shape was already run once against the live database (a temp table,
rolled back) and once through `sqlc generate` v1.31.1 against this exact
query. Both checks passed. `MirrorSuperchargerSessionParams` did not change.

### The governing rule, and the deny-list by bucket

**The comparison covers exactly the columns the SET clause writes, and
nothing else.** One sentence, two directions:
- Add a column to the SET clause later -> remove it from the deny-list.
- Add a column to the table that the SET clause does not write -> add it to
  the deny-list.

Today the SET clause writes 5 columns: `energy_kwh, total_cost, currency,
is_paid, tesla_id`. Only those 5 are compared. Every other live column —
14 of them — is deny-listed. The deny-list is not a judgment call about
which columns "seem unlikely to change." It is the complement of the SET
clause. Nothing more.

**Why this rule, stated this way, and not a smaller deny-list.** An
earlier version of this design deny-listed only 8 columns — the ones
nobody ever writes at all — and left the 6 write-once mirrored columns
(`account_id, vin, session_id, charge_start_date_time,
charge_stop_date_time, site_location_name`) inside the comparison. The
owner tested that version live against Postgres 16 and found it wrong:
`site_location_name` is write-once by design — this query never refreshes
it, even if Tesla later reports a different name for the same session. But
an 8-column deny-list still COMPARED it against the source. The moment the
source disagreed with the stored value, the comparison found a
"difference" — and because this query never rewrites a write-once column,
that difference NEVER resolves. `updated_at` would have advanced every
single night, forever, for that one row. That is MAG-48 returning in a new
shape, on the exact table this tier exists to fix, since
`internal/analytics` reads this column directly.

The fix is the rule above: compare ONLY what the SET clause actually
refreshes. A write-once column can never cause a permanent mismatch,
because it is simply never asked to match the source after its first
write.

**The three buckets, and why each belongs in the deny-list:**

**(a) Bookkeeping, not row data — `id`, `created_at`, `updated_at`.** `id`
is the primary key, not a fact about the session. `created_at` is
write-once by design (RM29 D1). `updated_at` is the column the `CASE`
computes — comparing it to itself is circular. Two of these three also
have a mechanical reason: neither `id` nor `created_at` is in this query's
`INSERT` column list, so `EXCLUDED` carries a FRESH default for both on
every single call (a new random UUID, a new `now()`). Leaving either out
would report "changed" on every call, forever, with no exception.

**(b) Human-owned, or derived from human-owned data, never touched by the
poller — `start_battery_pct`, `end_battery_pct`, `battery_pct_source`,
`status`, `inferred_capacity_kwh_calc`.** The first three are written only
by `SessionVerifier.VerifySession` (RM31); `status` is Go-computed by the
same method (RM41, MAG-45). None of the four is ever in this query's
`INSERT` or `SET`. Roadmap D3 requires this: a mirror pass must never look
like a human edit, and a human edit must never look like a mirror pass.
`inferred_capacity_kwh_calc` is `GENERATED ALWAYS AS (...) STORED` from
`energy_kwh` and the two percentages (MAG-25) — confirmed live: on a
verified session the STORED value is real (computed from the real
percentages), but `EXCLUDED`'s copy is always `NULL`, because `EXCLUDED`'s
percentages come from this query's own `INSERT`, which never supplies
them. Leaving this column out would make every verified session's
`updated_at` advance every night — the same permanent, unresolvable
mismatch as bucket (c), on a different column. **This column is missing
from the roadmap's own tier-3 text**, which names only the 5 SET-clause
columns and says nothing about the deny-list at all; this design adds it
after confirming the mismatch against the live schema.

**(c) Write-once columns the SET clause never refreshes — `account_id`,
`vin`, `session_id`, `charge_start_date_time`, `charge_stop_date_time`,
`site_location_name`.** This is the bucket the first draft of this design
got wrong (see above). All six are written once, at the first `INSERT`,
and never again — matching telemetry's own upsert, which does not refresh
them either (design.md D1's rule: a mirrored column gets exactly its
source column's write semantics). Because this query never rewrites them,
comparing them against the source can only ever find a difference that
never resolves. They are excluded for the identical mechanical reason as
bucket (a)'s fresh defaults — not because their values are expected to
stay stable, but because this query has no way to make them match even
when they do change at the source.

### Rationale — why a deny-list, and the two rejected alternatives

**Why a deny-list and not an allow-list.** The query's own comment, before
this change, already explains why a hand-listed predicate was rejected once:
a column added to the `SET` clause later, but forgotten in the predicate,
would silently stop being detected. An allow-list has the identical failure
mode: a newly-refreshed column not added to the allow-list is invisible to
the comparison forever. Nothing in the codebase would ever notice — the
column's changes simply stop reaching `updated_at`, silently, for good.

A deny-list fails the other way, and that is why it is the right choice: a
column forgotten on the deny-list is compared BY DEFAULT. If that column is
one this query never actually writes (a human-owned column, a generated
column, or the identity/audit columns), forgetting it makes `updated_at`
advance every single night for every row touching it — loud, and directly
visible through the query-log lines tier 1 already shipped. A loud failure
that shows up immediately beats a silent one that never shows up at all.

**Rejected alternative 1 — a trigger.** A `BEFORE UPDATE` trigger function
comparing `OLD` and `NEW` could do the same comparison. Rejected because:
- The comparison logic would live outside `query.sql`, invisible to `sqlc`
  and to a grep for `MirrorSuperchargerSession` — the opposite of this
  project's "discoverability where an agent already looks" rule
  (`CLAUDE.md` §Non-negotiables).
- A trigger fires on ANY `UPDATE` against the table, not just this query's
  own upsert. A future direct `UPDATE` (a manual data fix, a different
  writer) would silently run through the same comparison, whether or not
  that is wanted — a second, hidden execution path this project's guards
  (`migration-guard`, `archive-guard`) do not inspect.
- It adds a standing database object (the trigger, its function) for a rule
  that only one query needs. One `CASE` expression, in the one place that
  needs it, is less to track — the "do not over-abstract" half of the same
  `CLAUDE.md` rule.

**Rejected alternative 2 — a `ROW(...)` allow-list.** Compare
`ROW(energy_kwh, total_cost, currency, is_paid, tesla_id) IS DISTINCT FROM
ROW(EXCLUDED.energy_kwh, ...)`. This is the same failure mode the query's
own comment already rejected once, restated: every future refreshed column
must be added to this list by hand, and a forgotten one is never caught by
anything. The roadmap's D3 states this rule for the whole project; this
design applies it here rather than re-deriving it.

### Index Plan

**No index is added, changed, or needed.** `ON CONFLICT (account_id,
session_id)` already resolves through the existing unique index
(`supercharger_sessions_account_session_unique`, RM39 tier 3) — the same
index this query used before this change. The `CASE` comparison runs once
per row already found by that lookup; it reads no other row and touches no
other index. `to_jsonb(...)` builds a JSON value from columns already in
hand — no extra page reads, no extra I/O. This is a pure CPU cost added to
an already-located row, not a new access pattern.

### Downstream effect on `internal/analytics`

**No backfill and no migration are needed.** Here is what happens, night by
night, after this ships:

- **The first night after deploy:** every existing row still carries the
  `updated_at` value the OLD, unconditional code set on the last run before
  this deployed. `internal/analytics.Recalculator.Reconcile`'s own cursor
  for that account is already close to that same timestamp (it advanced
  there because every row looked "changed" on that same last run). So the
  first post-deploy `Reconcile` call still sees the full set of sessions as
  "within the lookback window" and recalculates the full history one more
  time — the last time this happens.
- **Every night after that:** a session whose data did not change keeps its
  OLD `updated_at`, now falling further and further behind
  `Reconcile`'s advancing cursor. A session whose data DID change gets a
  fresh `updated_at`. The window `Reconcile` recalculates narrows on its
  own, night by night, down to just the sessions that actually changed.

No data is wrong at any point in this sequence, and no manual fix or
migration is needed to reach the narrow steady state.

## Test Contract

These are the exact test cases and their expected values, authored before
any implementation exists. A test written later must assert THESE values.

**Setup shared by every case:** a session is first mirrored via
`SessionWriter.MirrorSessions` with known values, and its `updated_at`
(call it `T_prev`) is read back. Then a second `MirrorSessions` call is
made and `updated_at` is read back again.

| # | Case | Second call's input vs. first | Expected `updated_at` after the second call |
|---|---|---|---|
| T1 | Unchanged re-mirror | Identical `SessionMirror` values | Still exactly `T_prev` — unchanged |
| T2 | `energy_kwh` changes | `EnergyKWh` differs (e.g. `44.64` → `46.10`) | A new value, later than `T_prev` |
| T3 | `is_paid` false→true | `IsPaid` changes `false` → `true` | A new value, later than `T_prev` |
| T4 | `tesla_id` NULL→value (orphan recovery) | `TeslaID` changes `nil` → a value | A new value, later than `T_prev` |
| T5 | Human-owned columns survive an unchanged re-mirror | Identical `SessionMirror` values; the row already carries `start_battery_pct=20`, `end_battery_pct=80`, `battery_pct_source="user_verified"`, `status="DONE"` (set earlier via `SessionVerifier.VerifySession`, not via the mirror) | `updated_at` still exactly `T_prev`; AND all four of `start_battery_pct`, `end_battery_pct`, `battery_pct_source`, `status` are unchanged from before the second call |
| T6 | `inferred_capacity_kwh_calc` populated, unchanged re-mirror | Identical `SessionMirror` values; the row already carries the same percentages as T5, so `inferred_capacity_kwh_calc` is a non-`NULL` generated value | `updated_at` still exactly `T_prev` (this is the regression test for the missing-column bug the roadmap's tier text did not name — see bucket (b) in "The governing rule, and the deny-list by bucket" above) |
| T7 | A genuine human edit still advances `updated_at` | N/A — this exercises `SessionVerifier.VerifySession`, not the mirror, and this method is **unchanged by this design** | `VerifySession` sets `updated_at = now()` explicitly, exactly as it does today; a caller reading the session afterward sees a fresh `updated_at`, so the edit still reaches `analytics.Recalculator.Reconcile` through `ListSessionsByVehicleUpdatedSince` |
| T8 | A write-once column differs at the source, refreshed columns unchanged | `SiteLocationName` differs from the stored value (e.g. `"Supercharger - Denver"` → `"Supercharger - Denver West"`); `EnergyKWh`, `TotalCost`, `Currency`, `IsPaid`, `TeslaID` all identical to the first call | `updated_at` still exactly `T_prev`; AND the stored `site_location_name` STILL reads the ORIGINAL value, not the renamed one — the mismatch is real and permanent, and this comparison must never see it |

**T8 must run the same renamed-input call twice in a row** (three
`MirrorSessions` calls total: the initial mirror, then two identical
re-mirrors carrying the renamed site). Both re-mirrors must leave
`updated_at` at `T_prev` and the stored name unchanged. Running it only
once would not catch the bug the owner found live: the wrong 8-column
deny-list also passed a single-call version of this case, because — before
the fix — that first mismatch was interpreted as noise on its own, not as
a value that would then need to keep being wrongly flagged as "changed"
on every following night. Two consecutive identical calls prove the
`updated_at` freeze holds across more than one night, not just once.

### The self-checking schema test (D8)

One more test, not in the table above because it does not test one upsert
call — it tests that the deny-list stays correct as the schema evolves.

1. At runtime, query `information_schema.columns` for
   `table_schema = 'charging' AND table_name = 'supercharger_sessions'` and
   collect every column name.
2. Compare that live set against two sets hardcoded in the test, matching
   production: **written** = `{energy_kwh, total_cost, currency, is_paid,
   tesla_id}` (exactly this query's `SET` clause — 5 columns) and **deny**
   = the 14-column list above.
3. **Assert the live set equals `written ∪ deny`, exactly.** Under the
   governing rule this union is total: every live column falls into
   exactly one of the two sets, with no leftover bucket. A column found in
   neither set fails the test immediately, by name, with a message telling
   the reader to add it to one or the other. This is what makes the test
   self-checking: a future migration that adds a column makes this test
   fail on its own, with no code review needed to catch it.
4. Behavioral proof: seed one row via `MirrorSessions`. Directly (raw SQL,
   never through a chargingdb reader) set every settable deny-listed column
   to a sentinel: `created_at` and `updated_at` to a fixed past instant
   (e.g. `2020-01-01T00:00:00Z`), `start_battery_pct=55`,
   `end_battery_pct=90`, `battery_pct_source='user_verified'`,
   `status='DONE'`, `charge_start_date_time`/`charge_stop_date_time` to
   instants different from the seeded session, and `site_location_name` to
   a different name. Exemptions, both documented: `id`, `account_id`,
   `vin`, `session_id` are left alone — these identify the row and form the
   `ON CONFLICT` target; changing them would make the second call insert a
   NEW row instead of updating the same one, which would pass for the
   wrong reason and test nothing about the deny-list.
   `inferred_capacity_kwh_calc` cannot be set directly — it is
   `GENERATED` — but the percentage pokes already move it.
5. Re-run `MirrorSessions` with the SAME `SessionMirror` values used to seed
   the row.
6. Assert every sentinel from step 4 is UNCHANGED after step 5, including
   `updated_at` still reading the fixed past instant, not a fresh `now()`.

## Local Decisions

### D1 — Mechanism: `to_jsonb` deny-list inside `ON CONFLICT DO UPDATE SET`

Given by the roadmap (D15) and confirmed live against Postgres 16 and
`sqlc` v1.31.1. Implemented exactly as given — see "The exact final SQL"
above. `tesla_id` stays inside the 5-column comparison, so the
orphan-recovery path (roadmap D3) still works: a `NULL`-to-value change on
`tesla_id` is exactly the difference the comparison is built to catch.
Compliance: exact.

### D2 — The deny-list is the complement of the SET clause: 14 entries, not 8

The roadmap's tier-3 text names only the 5 SET-clause columns
(`energy_kwh, total_cost, currency, is_paid, tesla_id`) and says nothing
about the deny-list's contents. An earlier version of this design
deny-listed only the 8 columns nobody ever writes at all, leaving the 6
write-once mirrored columns inside the comparison. The owner tested that
version live and found it wrong: a write-once column that differs at the
source (a renamed `site_location_name`) creates a mismatch this query can
never resolve, so `updated_at` would advance forever — the exact bug this
tier exists to remove. The fix, and the rule now governing this query: the
comparison covers exactly what the SET clause writes (5 columns) and
nothing else; every other live column (14) is deny-listed. See "The
governing rule, and the deny-list by bucket" above for the full reasoning
and the three buckets. Compliance: the deny-list ships with 14 entries; the
compared set has exactly 5.

### D3 — The query's doc comment is fully rewritten, not just trimmed

The old comment's final paragraph explains why `updated_at` means "the last
pass touched this row," which becomes false the moment this ships. That
paragraph is replaced, not deleted — the replacement keeps the old
objection visible and states why the new design answers it, so a future
reader does not wonder whether the same rejected idea is being tried again.
Compliance: see "The exact final SQL" above for the full rewritten comment.

### D4 — Rejected: a trigger

See "Rejected Alternatives" above. Compliance: no trigger, no trigger
function, ships with this change.

### D5 — Rejected: a `ROW(...)` allow-list

See "Rejected Alternatives" above. Compliance: no allow-list form appears
anywhere in the shipped query.

### D6 — No index change

See "Index Plan" above: the existing unique index already drives the
`ON CONFLICT` lookup; the comparison itself needs no index. Compliance: no
migration, no index DDL, ships with this change.

### D7 — No backfill, no migration for the `analytics` transition

See "Downstream effect on `internal/analytics`" above. Compliance: this
change ships no migration and no one-time data fix; the transition is a
property of the new logic running over time, not a step anyone performs.

### D8 — Self-checking test shape

See "The self-checking schema test" above: a schema-completeness assertion
(closed-set check against `information_schema`, now a total partition —
`written` (5) ∪ `deny` (14) — with no leftover bucket) plus a behavioral
poke-and-verify test. `id`, `account_id`, `vin`, `session_id` are exempted
from the poke step (they identify the row / form the `ON CONFLICT` target;
poking them would just insert a new row) and `inferred_capacity_kwh_calc`
is exempted from direct poking (a `GENERATED` column cannot be set
directly) but is still exercised indirectly through the percentage pokes.
Compliance: both parts ship as one test file; a reviewer can add a column
to the live schema and watch part 1 fail without touching part 2.

### D9 — `internal/charging/AGENTS.md` scope of change

Only the paragraph describing the "NO WHERE PREDICATE" reasoning (in the
§Public Interface section's refresh-set discussion) is rewritten to match
this design. No other section changes: the refresh SET column list itself
(`energy_kwh, total_cost, currency, is_paid, tesla_id`) is unchanged by this
design — only `updated_at`'s assignment expression changes. Compliance: one
paragraph rewritten, cross-referencing this change; everything else in
`AGENTS.md` about this query stays as it is.
