> **DB-touching change — read design.md before starting.** This adds FIVE nullable
> columns to `supercharger_sessions`: the human-owned override trio
> (`start_battery_pct SMALLINT CHECK (0..100)`, `end_battery_pct SMALLINT CHECK (0..100)`,
> `battery_pct_source TEXT CHECK (IN ('user_verified','polled'))`) plus a **frozen,
> write-once verification-time snapshot pair** (`start_battery_pct_est SMALLINT CHECK
> (0..100)`, `end_battery_pct_est SMALLINT CHECK (0..100)`, design D6) — corrects the
> table's now-stale "no battery percentage" comment, and extends `SuperchargerSession` +
> `rowToSuperchargerSession` so `SuperchargerReader` surfaces all five. **The `_est` pair is
> NOT a revival of the rejected nightly-refreshed estimate shape** — it is written exactly
> once (by the future verification UI, out of scope here) and never refreshed again;
> staleness relative to a later, improved taper model is its correct, intended behavior, not
> a bug. It is never read back into `internal/battery`'s live computation. See design.md D6
> before touching T1/T2/T4/T6/T7 — do not "simplify" the `_est` pair into a nightly-refreshed
> column.
> **`UpsertSuperchargerSession`'s SQL text does NOT change functionally** — all five columns
> are deliberately absent from both its INSERT column list and its ON CONFLICT DO UPDATE SET
> clause (design D3/D6, R3-load-bearing). No new index. No estimator, no verification UI, no
> `internal/battery` change — all out of scope (R4, R7; tier 2 is a separate future change).
> Entire surface is `internal/telemetry/`.
>
> **Dependencies / parallelism:**
>
> - T1 (migration) has no dependencies.
> - T2 (`SuperchargerSession` struct fields) has no dependencies; may run in parallel with T1.
> - T3 (query.sql header-comment addition) depends on T1 (cites the migration filename).
> - T4 (`mapping.go`: new helper + `rowToSuperchargerSession` fields) depends on T2, T3.
> - T5 (`service.go`: documentation-only comment) depends on T2.
> - T6 (new DB-integration tests, the five test-contract scenarios) depends on T1, T3, T4.
> - T7 (`AGENTS.md` documentation) depends on T1.
> - Verification (V) depends on all tasks.
>
> **Leader-integrated step:** run `make sqlc` after T3.1 (query.sql edits) to regenerate
> `telemetrydb`. The existing `sql:` entry in `sqlc.yaml` already covers the telemetry
> module; no structural `sqlc.yaml` change is needed.

---

## T1. Goose migration (`internal/telemetry/db/migrations/`) — no dependencies

- [ ] T1.1 Create
      `internal/telemetry/db/migrations/20260815000001_add_supercharger_battery_pct.sql`
      with the exact DDL from design.md's "Schema" section: Up adds FIVE nullable columns —
      the trio (`start_battery_pct SMALLINT CHECK (start_battery_pct BETWEEN 0 AND 100)`,
      `end_battery_pct SMALLINT CHECK (end_battery_pct BETWEEN 0 AND 100)`,
      `battery_pct_source TEXT CHECK (battery_pct_source IN ('user_verified', 'polled'))`)
      AND the frozen snapshot pair (`start_battery_pct_est SMALLINT CHECK
      (start_battery_pct_est BETWEEN 0 AND 100)`, `end_battery_pct_est SMALLINT CHECK
      (end_battery_pct_est BETWEEN 0 AND 100)`) — sets all five `COMMENT ON COLUMN`
      statements (including the two new ones that explicitly state "FROZEN, write-once...
      NEVER refreshed... NEVER read back into internal/battery's live computation"), and
      **re-sets `COMMENT ON TABLE supercharger_sessions`** to the corrected text (mentions
      both the override trio and the frozen snapshot pair; no longer claims "no battery
      percentage"). Down drops all five columns in reverse-append order (comment restoration
      is explicitly not required — see design.md's Down rationale). Include the full header
      comment from design.md verbatim (why these columns exist, the R3 load-bearing
      exclusion covering all five, the "never 'estimated'" rule, and the "do NOT fix this
      into a nightly-refreshed pair" warning for the `_est` columns). Do not deviate from the
      exact column names, types, or CHECK expressions specified in design.md.
      Acceptance: `goose status` (or `make migrate-up`) shows the migration applied cleanly
      against the live DB; `\d supercharger_sessions` (or the pg catalog) shows all five
      columns nullable with no `DEFAULT`; all five CHECK constraints exist; `SELECT
      obj_description('supercharger_sessions'::regclass)` no longer contains "no battery
      percentage"; `goose down` (one step) removes all five columns without error and the
      table's other columns/indexes/constraints are unaffected.

## T2. `SuperchargerSession` struct (`internal/telemetry/telemetry.go`) — no dependencies

- [ ] T2.1 Add five new pointer fields to `SuperchargerSession`
      (`internal/telemetry/telemetry.go:293-313`): `StartBatteryPct *int`, `EndBatteryPct
      *int`, `BatteryPctSource *string`, `StartBatteryPctEst *int`, `EndBatteryPctEst *int`,
      placed at the end of the struct (mirroring the migration's physical column-append
      order, matching the module's established convention). Doc comments per design.md D5/D6
      (NULL convention; the explicit note that `BatteryPctSource` is NEVER `"estimated"` —
      that state is computed on read by `internal/battery` and never persisted here; AND the
      explicit note on `StartBatteryPctEst`/`EndBatteryPctEst` that they are a FROZEN,
      write-once snapshot, NEVER refreshed, NEVER read back into `internal/battery`'s live
      computation — not a cache).
      Acceptance: `go build ./...` green; every existing named-field
      `SuperchargerSession{...}` struct literal in the codebase remains compile-compatible
      (additive fields only).

## T3. `db/query.sql` header-comment addition — depends on T1

- [ ] T3.1 Add the header-comment block from design.md's "Write Path" section immediately
      above the existing `-- name: UpsertSuperchargerSession :exec` comment
      (`internal/telemetry/db/query.sql:305-311`). **Do NOT add `start_battery_pct`,
      `end_battery_pct`, `battery_pct_source`, `start_battery_pct_est`, or
      `end_battery_pct_est` to the `INSERT` column list, `VALUES`, or `ON CONFLICT ... DO
      UPDATE SET` clause** — the query's functional SQL text (columns, values, conflict
      target, update assignments) must be byte-for-byte identical before and after this
      task; only a comment is added. This is the direct implementation of design D3/D6/R3,
      covering all five columns, not just the original trio.
      After editing, the leader runs `make sqlc` to regenerate `telemetrydb` — this also
      picks up the five new columns on the generated `SuperchargerSession` row type used
      by `SuperchargerSessionsByAccount`/`SuperchargerSessionsByVehicle`'s `SELECT *`, with
      no edit needed to either of those two queries.
      Acceptance: `git diff` on `query.sql` shows only comment lines added — zero changes
      to any line inside `UpsertSuperchargerSession`'s `INSERT`, `VALUES`, or `ON CONFLICT`
      clauses; `grep -c 'start_battery_pct\|end_battery_pct\|battery_pct_source\|start_battery_pct_est\|end_battery_pct_est'`
      restricted to the byte range of the `UpsertSuperchargerSession` query body (excluding
      its header comment) returns `0`; after `make sqlc`, `telemetrydb.SuperchargerSession`
      has all five new fields (`StartBatteryPct pgtype.Int2`, `EndBatteryPct pgtype.Int2`,
      `BatteryPctSource pgtype.Text`, `StartBatteryPctEst pgtype.Int2`, `EndBatteryPctEst
      pgtype.Int2`) and `telemetrydb.UpsertSuperchargerSessionParams` does **not**.

## T4. `mapping.go`: new helper + `rowToSuperchargerSession` — depends on T2, T3

- [ ] T4.1 Add `pgNullableInt16AsInt(v pgtype.Int2) *int` to
      `internal/telemetry/mapping.go`, alongside the existing
      `pgNullableFloat64`/`pgNullableFloat4AsFloat64`/`pgNullableInt32AsInt`/
      `pgNullableText` helpers (lines 15-59), exact body from design.md's "Read Path"
      section (`{Valid: false}` → `nil`, `{Valid: true}` → `&int(v.Int16)`). This one
      helper is reused across all four `SMALLINT` columns in T4.2 — do not write a second,
      near-identical helper for the `_est` pair.
      Acceptance: `go build ./...` green; the function is a pure, allocation-minimal
      conversion with no DB/network dependency.

- [ ] T4.2 Extend `rowToSuperchargerSession` (`internal/telemetry/mapping.go:153-`) to map
      all five new fields into the returned `SuperchargerSession{...}` literal:
      `StartBatteryPct: pgNullableInt16AsInt(r.StartBatteryPct)`, `EndBatteryPct:
      pgNullableInt16AsInt(r.EndBatteryPct)`, `BatteryPctSource:
      pgNullableText(r.BatteryPctSource)`, `StartBatteryPctEst:
      pgNullableInt16AsInt(r.StartBatteryPctEst)`, `EndBatteryPctEst:
      pgNullableInt16AsInt(r.EndBatteryPctEst)` — reusing the **existing** `pgNullableText`
      helper for `BatteryPctSource` and T4.1's new `pgNullableInt16AsInt` for all four
      `SMALLINT` fields; no new helper needed for any of them. Placed last in the struct
      literal, mirroring the migration's physical column-append order.
      Acceptance: `go build ./...` and `go vet ./...` green once T3's `make sqlc` has run.

## T5. `service.go`: documentation-only comment — depends on T2

- [ ] T5.1 Add a one-line comment near `upsertSuperchargerSession`
      (`internal/telemetry/service.go:863-`) noting that `SuperchargerSession`'s
      `StartBatteryPct`/`EndBatteryPct`/`BatteryPctSource`/`StartBatteryPctEst`/
      `EndBatteryPctEst` fields are deliberately unread at this call site (design D3/D5/D6,
      R3) — no functional code change; the function body is not modified.
      Acceptance: `go build ./...` and `go vet ./...` green; `git diff` on `service.go`
      shows only a comment addition, zero logic changes.

## T6. New DB-integration tests — depends on T1, T3, T4

- [ ] T6.1 Add a test (new or existing DB-gated test file in `internal/telemetry/`)
      implementing design.md's test-contract scenario (a): a fresh
      `UpsertSuperchargerSession` call for a new `session_id` results in a stored row with
      `start_battery_pct`, `end_battery_pct`, `battery_pct_source`, `start_battery_pct_est`,
      `end_battery_pct_est` all NULL, verified by reading the row back (raw SQL query
      against the test pool is acceptable here, or via
      `SuperchargerSessionsByAccount`/`ByVehicle`).
      Acceptance: `go test ./internal/telemetry/...` (DB-gated) passes.

- [ ] T6.2 Add `TestStore_SuperchargerUpsert_LeavesVerifiedBatteryPctUntouched` (or similarly
      named) implementing design.md's test-contract scenario (b) — **the most important
      test in this change**: insert a session via `UpsertSuperchargerSession`; set its trio
      via a direct SQL `UPDATE` against the test pool (`start_battery_pct = 18,
      end_battery_pct = 76, battery_pct_source = 'user_verified'`); call
      `UpsertSuperchargerSession` again for the same `session_id` with changed mutable
      fields (`is_paid` flipped, different `raw_data`); assert the resulting row's mutable
      fields reflect the second upsert (`is_paid` changed, `updated_at` advanced) **and**
      the trio is byte-for-byte unchanged from the direct-SQL values set beforehand.
      Acceptance: `go test ./internal/telemetry/...` (DB-gated) passes. This test must FAIL
      if `start_battery_pct`/`end_battery_pct`/`battery_pct_source` are ever added to
      `UpsertSuperchargerSession`'s `ON CONFLICT DO UPDATE SET` clause in a future change —
      it is the regression guard for R3.

- [ ] T6.2b Add `TestStore_SuperchargerUpsert_LeavesVerificationSnapshotUntouched` (or
      similarly named) implementing design.md's test-contract scenario (b2) — the same R3
      regression shape as T6.2, applied to the frozen snapshot pair (design D6): insert a
      session via `UpsertSuperchargerSession`; set BOTH the trio AND
      `start_battery_pct_est`/`end_battery_pct_est` together via one direct SQL statement,
      with the snapshot values deliberately different from the verified values (e.g.
      verified `20`/`80`, snapshot `22`/`78` — "model said X, human said Y"); call
      `UpsertSuperchargerSession` again for the same `session_id` with changed mutable
      fields, at least twice (simulating two later nightly re-fetches); assert
      `start_battery_pct_est`/`end_battery_pct_est` are byte-for-byte unchanged after every
      re-upsert while `is_paid`/`updated_at` do advance. Do NOT test a second *verification*
      of the same session in this task (a second direct-SQL write that itself changes the
      `_est` pair) — per design.md D6, overwriting the snapshot on a genuine
      re-verification is accepted behavior, not a regression this test guards against.
      Acceptance: `go test ./internal/telemetry/...` (DB-gated) passes. This test must FAIL
      if `start_battery_pct_est`/`end_battery_pct_est` are ever added to
      `UpsertSuperchargerSession`'s `ON CONFLICT DO UPDATE SET` clause, or if a future
      change turns them into a nightly-refreshed pair — it is the regression guard for D6.

- [ ] T6.3 Add a test implementing design.md's test-contract scenario (c): each of the
      direct-SQL statements listed there — out-of-range checks on `start_battery_pct` (`=
      101`, `= -1`), `end_battery_pct` (`= 101`), `start_battery_pct_est` (`= 101`, `= -1`),
      `end_battery_pct_est` (`= 101`), the two rejected `battery_pct_source` values
      (`'estimated'`, `'bogus'`), and the sanity-check boundary values (`0`/`100` on all
      four `SMALLINT` columns plus `'polled'`) — is issued against the test pool; every
      invalid statement returns a Postgres `23514` (check_violation) error and modifies no
      row; the sanity-check statement succeeds.
      Acceptance: `go test ./internal/telemetry/...` (DB-gated) passes.

- [ ] T6.4 Add a test implementing design.md's test-contract scenario (d): after setting one
      session's trio (reusing T6.2's fixture or a fresh one) and a second session's trio
      plus frozen snapshot pair (reusing T6.2b's fixture or a fresh one), call both
      `SuperchargerSessionsByAccount` and `SuperchargerSessionsByVehicle` for the relevant
      account/vehicle and assert the returned `SuperchargerSession`'s
      `StartBatteryPct`/`EndBatteryPct`/`BatteryPctSource` match the values set for the
      first session, and `StartBatteryPctEst`/`EndBatteryPctEst` match the values set for
      the second; also assert a session with an untouched (NULL) trio and snapshot pair
      round-trips as `nil` for all five fields through both read methods.
      Acceptance: `go test ./internal/telemetry/...` (DB-gated) passes.

## T7. `internal/telemetry/AGENTS.md` documentation — depends on T1

- [ ] T7.1 Add all five new columns to the "Data ownership" `supercharger_sessions` bullet
      (column names, types, nullability, CHECK constraints) and add a note under "DTO /
      units conventions" (or a new subsection) documenting: the trio's NULL convention
      (NULL = no override, tier 2's `internal/battery` on-read estimate applies); that the
      trio and the snapshot pair are EXCLUDED from `UpsertSuperchargerSession`'s INSERT and
      ON CONFLICT DO UPDATE SET (R3) and that no writer for any of the five columns exists
      yet in this repository (R7); that `battery_pct_source` never stores `'estimated'` and
      why (R4/R6); and — **explicitly, in its own clearly marked paragraph so a future
      implementer of `internal/battery` cannot miss it** — that
      `start_battery_pct_est`/`end_battery_pct_est` are a FROZEN, write-once verification
      snapshot (design D6): written once by the future verification UI in the same write as
      the trio, NEVER refreshed afterward (staleness relative to a newer taper model is
      correct, not a bug), and NEVER to be read back into `internal/battery`'s live
      estimate computation as a cache. Add a one-line pointer to
      `RM27-telemetry-add-supercharger-battery-pct` as the change that introduced them
      (mirroring how the file already cites other introducing changes by name).
      Acceptance: the section reads correctly on its own — a future worker/agent reading
      only `AGENTS.md` understands what the trio and the snapshot pair mean, why neither is
      ever auto-written, why the snapshot pair is deliberately never refreshed, and where
      tier 2 (`internal/battery`) fits, without needing to open this change's design.md.

---

## Verification — depends on all tasks

- [ ] V1. `go build ./...` and `go vet ./...` pass after all tasks are complete.
- [ ] V2. `go test ./...` green and fast. DB integration tests self-skip without
      `DATABASE_URL`/Docker; with Docker the testcontainers helper provisions Postgres and
      applies goose migrations automatically, including the new
      `20260815000001_add_supercharger_battery_pct.sql`. NO Tesla API call fires.
- [ ] V3. Test-contract (a) fresh-insert-NULL correctness (all five columns): verified by
      T6.1.
- [ ] V4. Test-contract (b) R3 regression, trio (re-upsert leaves trio untouched): verified
      by T6.2 — the single most important acceptance check in this change.
- [ ] V4b. Test-contract (b2) R3 regression, frozen snapshot pair (re-upsert leaves
      `_est` pair untouched, design D6): verified by T6.2b.
- [ ] V5. Test-contract (c) CHECK constraint correctness (all four `SMALLINT` columns'
      range, both rejected `battery_pct_source` values, and the accepted boundary/`'polled'`
      case): verified by T6.3.
- [ ] V6. Test-contract (d) reader round-trip correctness (trio and snapshot pair):
      verified by T6.4.
- [ ] V7. Static R3 check: `UpsertSuperchargerSession`'s functional SQL text (INSERT column
      list, VALUES, ON CONFLICT DO UPDATE SET — excluding its header comment) contains zero
      references to `start_battery_pct`, `end_battery_pct`, `battery_pct_source`,
      `start_battery_pct_est`, or `end_battery_pct_est`. Verify by inspection or grep
      restricted to the query body, per T3.1's acceptance criteria.
- [ ] V8. Index plan confirmed: no `CREATE INDEX` appears anywhere in the migration diff;
      `EXPLAIN` on `SuperchargerSessionsByAccount`/`SuperchargerSessionsByVehicle` still
      uses `idx_supercharger_sessions_account_time`/`idx_supercharger_sessions_vehicle_time`
      respectively, unchanged from before this change.
- [ ] V9. Boundary check: `internal/telemetry` still imports only `account` + `tesla` public
      packages; no other module's internals. `pgtype` does not appear in any public type or
      interface (`pgNullableInt16AsInt`'s `pgtype.Int2` parameter stays confined to
      `mapping.go`, matching the module's existing helpers). No file outside
      `internal/telemetry/` was touched. `internal/battery` was not created, read, or
      referenced by any file this change creates or modifies — including no gateway/Writer
      plumbing for the trio or the snapshot pair (design D6's "legal writer" discussion is
      architecture-only in this tier, not implemented here per R7).
- [ ] V10. Docs: `internal/telemetry/AGENTS.md` accurately reflects all five new columns,
      including the frozen-snapshot warning (T7.1). Root `README.md` "Project
      Structure"/"Architecture" confirmed NOT to need changes (no module added/removed, no
      new runnable) — this confirmation itself is part of verification, not an assumption
      to skip.
- [ ] V11. `openspec validate RM27-telemetry-add-supercharger-battery-pct --strict` passes.
