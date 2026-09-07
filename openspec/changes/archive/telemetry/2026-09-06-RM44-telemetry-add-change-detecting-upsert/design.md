# Design — RM44-telemetry-add-change-detecting-upsert

## Context

`telemetry.UpsertSuperchargerHistory` (`internal/telemetry/db/query.sql`) runs once
per Supercharger session, every night, for every account. On a conflict on the
`session_id` unique constraint, it refreshes six columns: `raw_data`, `energy_kwh`,
`total_cost`, `currency`, `is_paid`, `tesla_id`. Today it also always sets
`updated_at = now()`, even when none of those six columns actually changed.

That is the bug. `updated_at` should mean "this row's data changed". Right now it
means "the poller ran". `internal/analytics` reads `updated_at` to decide what to
recalculate, so a false "changed" signal makes it redo work it does not need to redo.
Measured on 2026-09-05: a 70-day window, 51 of 51 `vehicle_metrics` rows rewritten,
for 4 days of real change.

This tier fixes only this one query, in this one module. Tier 3
(`RM44-charging-add-change-detecting-mirror`) fixes the same bug in
`internal/charging`'s own copy of this upsert. `internal/analytics` is not touched —
its own read logic is already correct (roadmap D1).

The live database (verified by the leader) has 8 rows in
`telemetry.supercharger_history`, of which 3 already carry real values in
`start_battery_pct` / `end_battery_pct` / `battery_pct_source`. Those three columns
are human-owned: a person sets them by hand, and the nightly poller must never touch
them. They are not in the upsert's `INSERT` column list today, and this tier does not
add them.

## Goals / Non-Goals

**Goals:**
- `updated_at` advances only when the row's mirrored data actually changed.
- An unchanged nightly re-sync leaves `updated_at` at its old value, byte-identical.
- A real change — including `tesla_id` moving from NULL to a value, the
  orphan-recovery path — still advances `updated_at`.
- A human battery-% edit never gets treated as "no change happened" just because it
  sits on the same row. The comparison must ignore those columns completely.
- Adding a column to the `SET` clause later, and forgetting to add it to the
  comparison, must be **loud** — it should make `updated_at` advance every night
  (an easy thing to notice), never make it silently stop advancing forever.

**Non-goals:**
- Fixing `internal/charging`'s mirror. Tier 3, its own change.
- Bounding any read window. Tier 4.
- Any change to `internal/analytics`.
- Any schema change. No new column, index, or constraint.
- Adding a writer for the three human-owned columns. Out of scope (backlog entry 11).
- **Saving a write-once column's late value.** If Tesla later sends a real
  `unlatch_date_time` (or any of the other 9 write-once columns) for a row that
  currently stores NULL or an older value, this change does NOT start capturing it —
  the `SET` clause still does not write these 10 columns, exactly as today. The owner
  considered adding this and chose to keep today's behavior. Capturing late
  write-once values, if ever wanted, is a separate future ticket, not part of this
  change.

## Decisions

### D1 — The comparison is a `to_jsonb` deny-list, not a trigger, not a hand-listed `WHERE`, not a `ROW(...)` allow-list

The exact final SQL, replacing today's `ON CONFLICT (session_id) DO UPDATE SET`
clause in `internal/telemetry/db/query.sql`:

```sql
ON CONFLICT (session_id) DO UPDATE SET
    raw_data   = EXCLUDED.raw_data,
    energy_kwh = EXCLUDED.energy_kwh,
    total_cost = EXCLUDED.total_cost,
    currency   = EXCLUDED.currency,
    is_paid    = EXCLUDED.is_paid,
    tesla_id   = EXCLUDED.tesla_id,
    updated_at = CASE
        WHEN to_jsonb(supercharger_history.*) - '{id,session_id,account_id,vin,site_location_name,country_code,charge_start_date_time,charge_stop_date_time,unlatch_date_time,billing_type,vehicle_make_type,created_at,updated_at,start_battery_pct,end_battery_pct,battery_pct_source}'::text[]
             IS DISTINCT FROM
             to_jsonb(EXCLUDED.*) - '{id,session_id,account_id,vin,site_location_name,country_code,charge_start_date_time,charge_stop_date_time,unlatch_date_time,billing_type,vehicle_make_type,created_at,updated_at,start_battery_pct,end_battery_pct,battery_pct_source}'::text[]
        THEN now()
        ELSE supercharger_history.updated_at
    END;
```

Everything else in the query — the `INSERT` column list, the `VALUES` list, the six
lines above `updated_at` in the `SET` — stays exactly as it is today. Only the
`updated_at` line changes.

`to_jsonb(supercharger_history.*)` turns the row as it is stored right now into one
JSON object. `to_jsonb(EXCLUDED.*)` turns the row the `INSERT` would have written into
another JSON object. `- '{...}'::text[]` removes the deny-listed keys from each
object before comparing. `IS DISTINCT FROM` compares the two remaining objects and
treats NULL the same as any other value, so a column moving to or from NULL still
counts as a real change.

This was verified on the live Postgres 16 dev database (temp table, rolled back by
the leader before this design was written): an unchanged repeat upsert left
`updated_at` at its old value, even with the human-owned columns populated on the row.
A real `energy_kwh` change advanced it. `sqlc` v1.31.1 parses this SQL — including
`to_jsonb(EXCLUDED.*)` and the `- '{...}'::text[]` operator — without error, and the
generated `UpsertSuperchargerHistoryParams` struct is unchanged. Only the embedded SQL
string in `query.sql.go` changes. There is zero Go call-site change and zero test-fake
change to make.

### D2 — The governing rule, the deny-list, and why each entry is there

**The governing rule: the comparison covers EXACTLY the columns the `SET` clause
writes.** Nothing more, nothing less. This is what keeps the comparison correct as the
table changes, and it is the rule any future edit must follow:

- Add a column to the `SET` clause → remove it from the deny-list.
- Add a column to the table that the `SET` clause does NOT write → add it to the
  deny-list.

The deny-list is therefore the complement of the `SET` clause: every real column on
`telemetry.supercharger_history` **except** the six the `SET` clause writes
(`raw_data`, `energy_kwh`, `total_cost`, `currency`, `is_paid`, `tesla_id`). Today that
is 16 names:
`'{id,session_id,account_id,vin,site_location_name,country_code,charge_start_date_time,charge_stop_date_time,unlatch_date_time,billing_type,vehicle_make_type,created_at,updated_at,start_battery_pct,end_battery_pct,battery_pct_source}'::text[]`.

They fall into three groups, each excluded for its own reason:

**(a) Bookkeeping / not session data — `id`, `created_at`, `updated_at`.**
- `id` is the surrogate primary key. Comparing it would always match anyway
  (`EXCLUDED.id` resolves to the same row on a conflict), so it changes nothing
  either way — it is excluded because it is never "mirrored data".
- `created_at` is write-once, set by the column default. The `INSERT` column list
  does not name `created_at`, so `EXCLUDED.created_at` holds whatever the default
  expression would produce for this attempted insert — a fresh value, different from
  the row's real, original `created_at`. Leaving this in the comparison would make
  every single re-sync look "changed".
- `updated_at` is the column this `CASE` expression is computing. Comparing a column
  against the decision that sets it is circular and must be excluded.

**(b) Human-owned, never touched by the poller — `start_battery_pct`,
`end_battery_pct`, `battery_pct_source`.** The verification trio (RM27). They are
not in the `INSERT` column list, so `EXCLUDED.start_battery_pct` (and the other two)
is always NULL. On the live database, 3 of the 8 rows already hold real, non-NULL
values here. Without this exclusion, every nightly re-sync of those 3 rows would see
"populated on disk" vs. "NULL in `EXCLUDED`", read that as a change, and advance
`updated_at` — the exact bug this tier exists to fix, moved one layer down.
**Load-bearing today, on real data, not a defensive placeholder.**

**(c) Write-once columns the `SET` clause never refreshes — `session_id`,
`account_id`, `vin`, `site_location_name`, `country_code`,
`charge_start_date_time`, `charge_stop_date_time`, `unlatch_date_time`,
`billing_type`, `vehicle_make_type`.** These 10 columns ARE written on the first
`INSERT`, but the `SET` clause does not name them, so they are never refreshed on a
conflict. That is existing, unchanged behavior — this tier does not add or remove any
column from the `SET` clause. But it means `EXCLUDED.<column>` can legitimately differ
from the stored value forever, with no bug involved: Tesla sometimes sends a
different value for one of these fields later than the first sync, and the row simply
keeps its original value by design.

If these 10 columns stayed IN the comparison, that permanent difference would make
`updated_at` advance on **every single night, forever**, for any row where one of
them differs — the exact failure this tier exists to remove, just moved to a
different set of columns. This is not theoretical: on the live database, 2 of the 8
rows in `telemetry.supercharger_history` have `unlatch_date_time = NULL` — the field
Tesla fills in only once a session finalizes. If Tesla later reports a real
`unlatch_date_time` for one of those sessions, and these 10 columns were not
excluded, the comparison would see a permanent mismatch (`NULL` stored vs. a real
timestamp incoming) that never resolves, because the `SET` clause never writes
`unlatch_date_time` to close the gap. `updated_at` would advance every night from
then on — MAG-48 returning in a form nobody would connect back to this query.
Excluding bucket (c) removes this failure mode entirely: the comparison only ever
looks at columns the `SET` clause can actually make agree.

### D3 — Rejected alternative: a trigger

A Postgres trigger (`BEFORE UPDATE`, comparing `OLD` and `NEW`) could do the same
comparison. Rejected because it moves the logic out of `query.sql`, the one place
this module's schema and its comparisons already live together. A trigger would live
in its own migration file, run on every `UPDATE` to this table from any source — not
just this one upsert — and would be invisible to `sqlc`, so a future reader of
`query.sql` would see a plain `updated_at = now()` and have no way to know the real
behavior is different. This project keeps schema and query logic side by side in
`internal/telemetry/db/`; a trigger breaks that.

### D4 — Rejected alternative: a `ROW(...)` allow-list

An allow-list names only the columns considered "data" — the opposite of a deny-list,
which names only the columns excluded. For example:
`ROW(raw_data, energy_kwh, total_cost, currency, is_paid, tesla_id) IS DISTINCT FROM
ROW(EXCLUDED.raw_data, EXCLUDED.energy_kwh, ...)`.

Rejected because of how it fails. A deny-list fails **loud**: a column added to the
table later, and forgotten in the deny-list, gets swept into the comparison by
default, and `updated_at` starts advancing on every re-sync again — the exact symptom
MAG-48 already measures, easy to notice from a query log or a metrics count. An
allow-list fails **silent**: a column forgotten there is simply never compared, and
`updated_at` stops advancing for that column's changes forever, with no metric or log
line pointing at the cause. Roadmap D3 exists specifically to forbid this second
failure mode. The precedent already lives in this project:
`internal/charging/db/query.sql`'s `MirrorSuperchargerSession` comment documents that
an earlier, narrower version of this same predicate idea (a single-column `WHERE`
naming `tesla_id` only) was rejected there for the identical reason — a predicate that
must be updated by hand every time the `SET` clause grows is a predicate that will,
eventually, not be updated.

### D5 — Index plan: no index is added or affected

`ON CONFLICT (session_id)` already drives the conflict lookup, using the existing
`session_id` unique constraint (created in migration `20260716000001`, renamed
alongside the table by `RM39-telemetry-move-to-own-schema`). That lookup finds the one
row to update before the `SET` clause, including the new `CASE` expression, ever
runs. The `to_jsonb` comparison happens per-row, on a row Postgres has already
located — it reads column values already in memory for the row being updated, so it
adds computation, not a lookup, and needs no index of its own. No new index, column,
or constraint is added by this tier.

### D6 — Row-count and performance note

This is a nightly poller path, not a dashboard read. Today's live data has 8 rows in
`telemetry.supercharger_history`, single digits per account. `to_jsonb(...)` on a row
this narrow is cheap — a handful of columns, no large blobs compared field-by-field
(`raw_data` is compared as one JSON value inside the larger `to_jsonb` object, not
parsed separately). At the roadmap's own long-term estimate (~36 sessions per user per
year), this comparison runs at most a few dozen times per account per night. This
tier changes a nightly write's CPU cost by a small, fixed amount per row; it does not
change how many rows are read or written.

### D7 — Test contract, authored before any implementation exists

Per `ai/go-conventions.md` "contract-first authoring": these are the exact expected
values every test must assert. A test written after the code exists must still match
these values, not whatever the code happens to do.

All tests below are `DATABASE_URL`-gated integration tests (this behavior is a real
Postgres `ON CONFLICT` clause; no fake stands in for it), following the existing
pattern in `internal/telemetry/db_integration_test.go`.

1. **Unchanged re-upsert → `updated_at` byte-identical.** Insert a session. Read back
   its `updated_at`. Run the identical `UpsertSuperchargerHistory` call again, same
   values. Read `updated_at` again. **Expected: the two values are equal, down to the
   microsecond** — not just "close in time".

2. **`energy_kwh` changes → `updated_at` advances.** Insert a session. Re-upsert with
   a different `energy_kwh`, everything else the same. **Expected: the new
   `updated_at` is strictly greater than the old one.**

3. **`is_paid` false → true → `updated_at` advances.** Insert a session with
   `is_paid = false`. Re-upsert with `is_paid = true`, everything else the same.
   **Expected: `updated_at` advances.**

4. **`tesla_id` NULL → value (orphan recovery) → `updated_at` advances.** Insert a
   session with `tesla_id = NULL` (the VIN's vehicle is not currently registered).
   Re-upsert with a real `tesla_id`, everything else the same. **Expected: `updated_at`
   advances.** This is the path roadmap D3 calls out by name: a re-registered vehicle
   must not get stuck looking "unchanged".

5. **A human battery-% value on the row → an otherwise-unchanged re-upsert still
   leaves `updated_at` untouched.** Insert a session. Directly `UPDATE` the row (raw
   SQL, not through the upsert) to set `start_battery_pct`, `end_battery_pct`, and
   `battery_pct_source` to real values — simulating a human verification. Re-run the
   identical `UpsertSuperchargerHistory` call, same mirrored values as the original
   insert. **Expected: `updated_at` stays at its pre-verification value.** This is
   D2 bucket (b)'s load-bearing case, proven by test, not only by design.

6. **A write-once column differs at the source, but the six refreshed columns do
   not → `updated_at` must NOT move, twice in a row.** Insert a session with
   `unlatch_date_time = NULL`. Re-upsert with a real, non-NULL
   `unlatch_date_time`, keeping the six refreshed columns (`raw_data`, `energy_kwh`,
   `total_cost`, `currency`, `is_paid`, `tesla_id`) exactly the same as the first
   insert. **Expected: `updated_at` does NOT move, and the stored
   `unlatch_date_time` stays NULL** — the `SET` clause never writes this column, so
   the incoming value is silently not applied; this test only checks it does not also
   cause a false "changed" signal. Run the identical re-upsert a **second** time
   (same NULL-vs-real-value mismatch still present). **Expected: `updated_at` still
   does not move** — proving the mismatch does not resolve, and therefore does not
   need to resolve, because bucket (c) keeps it out of the comparison every night,
   not just once. This is D2 bucket (c)'s load-bearing case, proven by test.

7. **The self-checking schema test (roadmap D16).** At test run time, read
   `telemetry.supercharger_history`'s real column list from `information_schema`
   (never a hand-typed list in the test). Assert directly that this live column set is
   EQUAL to the union of (the six columns the `SET` clause writes) and (the 16-name
   deny-list) — the governing rule from D2 restated as a checkable set equality. If a
   column is ever added to the table without being added to one side or the other,
   this assertion fails immediately, naming the exact column left out. Then, on top of
   that equality check, run the existing behavioral poke test: for `created_at`
   (bucket a) and every column in bucket (c), write a non-default, distinguishable
   "sentinel" value into it via a direct SQL `UPDATE` on an existing row (a magic
   string for a text column, a magic number for a numeric column, a magic timestamp
   for a date/time column). Skip `id` and `updated_at` (bucket a) — `id` is
   structural row identity, not poll data, and `updated_at` is the column under test,
   so poking it directly proves nothing. Skip bucket (b), the human-owned trio —
   writing to it is exactly what a human verification does and must not look like a
   detected "change"; scenario 5 already covers it. Re-run the identical
   `UpsertSuperchargerHistory` call.
   **Expected: `updated_at` does NOT move.** Together, the two checks catch both ways
   a future column could break this design: landing on neither side of the union
   (caught by the equality assertion), or landing in the deny-list, but the deny-list
   itself has drifted from the `SET` clause (caught by the union check re-deriving
   both sides from live sources, never a hand-typed literal in the test).

## Testing

Per the Test-Execution-Policy: this tier writes tests but does not run the suite. All
seven scenarios in D7 are `DATABASE_URL`-gated integration tests, following the existing
`internal/telemetry/db_integration_test.go` pattern (`internal/testdb` provisioning,
`TestMain` auto-skip when no Postgres is reachable).

Exact commands for the owner to run once implementation lands:

```
go build ./...
go vet ./...
gofmt -l .
make sqlc
```

Owner-only, once ready: `go test ./internal/telemetry/...` (or `make test` /
`make test-with-db` — Docker or `DATABASE_URL` required for these tests to actually
run rather than skip).
