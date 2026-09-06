# Tasks — RM44-telemetry-add-change-detecting-upsert

Ownership: every task below is **[module: telemetry worker]**, inside
`internal/telemetry/` only. No task touches `internal/charging`, `internal/analytics`,
`internal/app`, or `internal/gateway` (design.md confirms none of them need to change
for this tier).

**Hard ordering constraint:** the SQL change and its `sqlc` regenerate (Wave 1) must
land before any test task (Wave 2), because Wave 2's tests call
`UpsertSuperchargerHistory` and check the new `updated_at` behavior — they need the
new SQL to already be generated. Treat Wave 1 as one atomic step, done first, by
whoever starts this change.

---

## Wave 1 — Change the query and regenerate

- [ ] **1.1** `internal/telemetry/db/query.sql` — replace the `UpsertSuperchargerHistory`
  query's `ON CONFLICT (session_id) DO UPDATE SET` clause with the exact SQL in
  design.md D1: keep the six existing refreshed columns (`raw_data`, `energy_kwh`,
  `total_cost`, `currency`, `is_paid`, `tesla_id`) unchanged, and replace only the
  `updated_at = now()` line with the `CASE` expression comparing
  `to_jsonb(supercharger_history.*)` against `to_jsonb(EXCLUDED.*)`, both minus the
  16-name deny-list
  `'{id,session_id,account_id,vin,site_location_name,country_code,charge_start_date_time,charge_stop_date_time,unlatch_date_time,billing_type,vehicle_make_type,created_at,updated_at,start_battery_pct,end_battery_pct,battery_pct_source}'::text[]`.
  Update the query's doc comment to state the governing rule plainly (the comparison
  covers exactly the columns the `SET` clause writes — add a column to `SET`, remove
  it from the deny-list; add a column the `SET` clause does not write, add it to the
  deny-list) and to explain the three buckets from design.md D2 — bookkeeping
  (`id`, `created_at`, `updated_at`), human-owned (`start_battery_pct`,
  `end_battery_pct`, `battery_pct_source`), and write-once (the other 10 names) —
  so a future reader does not have to open design.md to know why a column is
  missing.
  `depends_on`: — · `parallel_ok`: no

- [ ] **1.2** Run `make sqlc` to regenerate `internal/telemetry/db/query.sql.go`.
  Confirm `UpsertSuperchargerHistoryParams` is unchanged (same fields, same types) —
  design.md's claim that this is a zero-call-site-churn change depends on this.
  `depends_on`: 1.1 · `parallel_ok`: no

---

## Wave 2 — Tests proving design.md's test contract (D7)

> Both files are new, disjoint files, so they can be implemented at the same time by
> separate agents once Wave 1 is done.

- [ ] **2.1** `internal/telemetry/db_change_detection_integration_test.go` (new file)
  — the six behavioral scenarios from design.md D7.1–D7.6, following the existing
  `DATABASE_URL`-gated pattern in `db_integration_test.go` (`internal/testdb`
  provisioning, `TestMain` auto-skip):
  - `TestUpsertSuperchargerHistory_UnchangedResync_LeavesUpdatedAtUntouched` (D7.1):
    insert, read `updated_at`, re-upsert with identical values, read `updated_at`
    again, assert equal down to the microsecond.
  - `TestUpsertSuperchargerHistory_EnergyChange_AdvancesUpdatedAt` (D7.2): insert,
    re-upsert with a different `energy_kwh`, assert the new `updated_at` is strictly
    greater.
  - `TestUpsertSuperchargerHistory_IsPaidChange_AdvancesUpdatedAt` (D7.3): insert with
    `is_paid = false`, re-upsert with `is_paid = true`, assert `updated_at` advances.
  - `TestUpsertSuperchargerHistory_TeslaIDRecovered_AdvancesUpdatedAt` (D7.4): insert
    with `tesla_id = NULL`, re-upsert with a real `tesla_id`, assert `updated_at`
    advances.
  - `TestUpsertSuperchargerHistory_HumanBatteryPctSet_UnchangedResyncStillLeavesUpdatedAtUntouched`
    (D7.5): insert, then directly `UPDATE` (raw SQL, not through the upsert) to set
    `start_battery_pct`, `end_battery_pct`, `battery_pct_source` on the row, then
    re-upsert with the original mirrored values unchanged, assert `updated_at` stays
    at its pre-verification value AND the three human-set columns still hold their
    set values (the upsert must not have cleared them either).
  - `TestUpsertSuperchargerHistory_WriteOnceColumnMismatch_NeverAdvancesUpdatedAt`
    (D7.6): insert a session with `unlatch_date_time = NULL`, re-upsert with a real,
    non-NULL `unlatch_date_time` and the six refreshed columns unchanged, assert
    `updated_at` does not move and the stored `unlatch_date_time` stays NULL. Run
    the identical re-upsert a **second** time (mismatch still present) and assert
    `updated_at` still does not move. This proves bucket (c) — write-once columns —
    never causes permanent nightly churn.
  `depends_on`: 1.2 · `parallel_ok`: with 2.2

- [ ] **2.2** `internal/telemetry/db_change_detection_schema_test.go` (new file) —
  the self-checking schema test from design.md D7.7 (roadmap D16). Two checks:
  1. `TestUpsertSuperchargerHistory_ColumnsMatchSetPlusDenylist`: query
     `information_schema.columns` for `telemetry.supercharger_history`'s real column
     list — never a hand-typed list in the test. Assert this set is EQUAL to the
     union of (the six `SET`-written columns: `raw_data, energy_kwh, total_cost,
     currency, is_paid, tesla_id`) and (the 16-name deny-list). A column present on
     neither side fails this assertion by name.
  2. `TestUpsertSuperchargerHistory_UndenylistedColumnBreaksChangeDetection`: insert a
     session. For `created_at` and every column in bucket (c) (the 10 write-once
     columns), write one non-default, type-appropriate sentinel value into it via a
     direct `UPDATE`. Skip `id`, `updated_at` (not meaningfully pokeable — see
     design.md D7.7) and bucket (b) (already covered by D7.5). Re-run the identical
     `UpsertSuperchargerHistory` call with the same mirrored values as the original
     insert. Assert `updated_at` did NOT move.
  Do not hardcode either column list as a literal Go slice for check 1; derive it
  from the schema query every run.
  `depends_on`: 1.2 · `parallel_ok`: with 2.1

---

## Wave 3 — Docs

- [ ] **3.1** `internal/telemetry/AGENTS.md` — add a short paragraph to "Testing
  notes" naming `db_change_detection_integration_test.go` and
  `db_change_detection_schema_test.go`, the governing rule (the comparison covers
  exactly the columns the `SET` clause writes) and the three deny-list buckets
  (bookkeeping, human-owned, write-once), and pointing at this change's design.md as
  the source of the test contract. Reference the change as archived once it is
  archived, mirroring how other "Testing notes" entries here point at their
  originating change.
  `depends_on`: 2.1, 2.2 · `parallel_ok`: no

---

## Verification (owner-run; see Test-Execution-Policy)

After every wave above is implemented:

```
go build ./...
go vet ./...
gofmt -l .
make sqlc
```

Owner-only, once ready: `go test ./internal/telemetry/...` (or `make test` /
`make test-with-db` — Docker or `DATABASE_URL` required for these tests to actually
run rather than skip; check the test output says `PASS`, not `SKIP`).
