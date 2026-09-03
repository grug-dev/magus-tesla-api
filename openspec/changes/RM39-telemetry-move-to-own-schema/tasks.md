> **Scope.** Additive, non-breaking migration (tier 4 of roadmap `RM39-schema-per-module`,
> MAG-31). Moves `vehicle_snapshots`, `supercharger_sessions`, `poll_attempts`, `poll_runs`
> into a new `telemetry` Postgres schema via **one** goose migration, AND renames
> `supercharger_sessions` → `supercharger_history` (roadmap D5a) in design.md D1's mandatory
> statement order. **Every** catalog object still carrying the old table name is renamed — the
> catalog is the completeness criterion, not this file's list (roadmap D18; design.md D7's
> owner-run query found **9** objects: 7 constraints + 2 standalone indexes). All 15 table
> references in `db/query.sql` become schema-qualified (forced by sqlc, design.md D2) and the
> 5 query names embedding the old table name are renamed (D4). `sqlc.yaml`'s telemetry entry
> gains a 4-key `gen.go.rename` block — 3 preserving (`PollAttempt`, `PollRun`,
> `VehicleSnapshot`), 1 new (`SuperchargerHistory`) — **verified by diffing `models.go`, never
> trusted from the config** (a wrong key fails silently at exit 0). The hand-written domain
> type `telemetry.SuperchargerSession` becomes `SuperchargerHistory` (D5).
>
> **The public port is deliberately left half-renamed (D6).** `SuperchargerReader`, its four
> `SuperchargerSessions*` methods, `NewSuperchargerReader`, `superchargerReader`,
> `rowToSuperchargerSession` and `upsertSuperchargerSession` **keep their current names** while
> returning/taking `SuperchargerHistory`. That is roadmap tier 5's work
> (`RM39-telemetry-rename-supercharger-port`, 88 external references). **No task here renames
> any of them, and no reviewer may flag the mismatch as an omission.** The rule: if the
> identifier is reachable from outside `internal/telemetry`, it is tier 5's — except
> `SuperchargerSession` → `SuperchargerHistory` itself, which is this tier's.
>
> **Roadmap D12's `search_path` fix is FORBIDDEN here (design.md D10).** A bare
> `supercharger_sessions` resolved through a search path that includes `charging` would find
> **charging's** table — a different table with a different row population — and succeed while
> reading the wrong data. Qualify explicitly, everywhere, always.
>
> **Historic migrations are never edited (roadmap D1).** The one exception in this change is
> **comment-only** text in `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql`
> (roadmap D25 / design.md D12) — not one character of its SQL.

## Ownership

Every task below is labelled with its owner. **`[telemetry]`** tasks are inside the telemetry
worker's sandbox. **`[leader-owned]`** tasks are outside it — another module's files, or repo
docs — and must be picked up by the leader (or dispatched to that module's own worker); **no
telemetry worker may touch them**, and no worker on this change may touch another module's
production code at all.

- `[telemetry]` — T1, T2, T3, T4, T5, T9, T11
- `[leader-owned]` — T6 (`internal/analytics`), T7 (`internal/app`), T8 (`internal/charging`),
  T10 (repo docs + `kkpa/context/` KB)

## Dependencies / parallelism

- **T1** (goose migration) — no dependencies. Disjoint files from T2; MAY run in parallel.
- **T2** (`query.sql` qualification + 5 query-name renames) — no dependencies. Disjoint files
  from T1; MAY run in parallel.
- **T3** (`sqlc.yaml` `rename:` + `make sqlc` + the mandatory `models.go` diff) — depends on
  **both T1 and T2**. sqlc resolves names against the migration files at generate time, so it
  cannot run before T1; and it regenerates from `query.sql`, so it cannot verify before T2.
- **T4** (hand-written `SuperchargerSession` → `SuperchargerHistory` + the renamed query-function
  call sites in `reader.go`/`mapping.go`/`service.go`) — depends on **T3** (needs the
  regenerated `telemetrydb.SuperchargerHistory` model and the 5 renamed query functions to
  compile against).
- **T5 / T6 / T7 / T8** (every `_test.go` file) — **FINAL WAVE. This is a dependency, not a
  preference.** Each of these files carries BOTH raw SQL strings (compile-independent) and
  `telemetry.SuperchargerSession` Go type references, which cannot compile until T4 lands.
  Editing each file once, for both concerns, is the only sane pass. T5–T8 touch disjoint files
  and MAY run in parallel with each other.
  **Their expected values are already authored in design.md's "Test Contract" — write the test
  edits against that contract; do NOT derive an expectation by reading the implementation.**
- **T9** (`internal/telemetry/AGENTS.md`) — depends on T1 only; parallel-ok with T2–T8.
- **T10** (repo docs + KB) — depends on T1 only; parallel-ok with T2–T9. `[leader-owned]`.
- **T11** (verification) — depends on T1–T10.

**Leader-integrated step:** run `make sqlc` once T1 and T2 have both landed (T3.2). Do **not**
hand-edit `internal/telemetry/db/models.go` or `db/query.sql.go` — both are sqlc-generated.
Do **not** run `make migrate-up`, `make db-setup`, or any test suite from this change
(`Test-Execution-Policy`).

## T1. Goose migration (`internal/telemetry/db/migrations/`) — `[telemetry]` — no dependencies, parallel-ok with T2

- [ ] T1.1 Re-verify the timestamp is globally free **immediately before creating the file**,
      by listing every module's migrations directory rather than assuming
      (`make migration-guard` fails on a duplicate version *across* modules — they share one
      `goose_db_version` table). Command that produced this change's own figures:
      ```
      for d in internal/*/db/migrations; do echo -n "$d: "; ls "$d" | tail -1; done
      ```
      Result at design time: latest across all modules is
      `internal/analytics/db/migrations/20260902000004_migrate_vehicle_metric_watermarks_source_supercharger.sql`;
      this module's own latest is `20260830000002_add_poll_runs.sql`. **`20260903000001`
      collides with neither** (design.md D14). If another tier has claimed it in the meantime,
      take the next free number and record the change.
- [ ] T1.2 Create
      `internal/telemetry/db/migrations/20260903000001_move_telemetry_to_own_schema.sql`.
      **This file is the single source of truth for the DDL** — design.md D1 deliberately does
      NOT restate the SQL (tier 3's embedded "exact DDL" drifted three times). Follow D1's
      **mandatory Up order**:
      1. `CREATE SCHEMA IF NOT EXISTS telemetry`
      2. `ALTER TABLE … SET SCHEMA telemetry` ×4 — `vehicle_snapshots`,
         `supercharger_sessions`, `poll_attempts`, `poll_runs`
      3. `ALTER TABLE telemetry.supercharger_sessions RENAME TO supercharger_history`
      4. the **9** catalog renames of T1.3 (7 `RENAME CONSTRAINT`, then 2 `ALTER INDEX …
         RENAME TO`)
      5. the `COMMENT ON` refresh of T1.4

      `-- +goose Down` reverses in the **exact opposite order** and ends
      `DROP SCHEMA IF EXISTS telemetry`. Reversing the order is what makes `CASCADE`
      unnecessary, so a Down can never silently destroy an object it did not create
      (`make migrate-down` depends on this). Use `IF NOT EXISTS` / `IF EXISTS` per tiers 1–3's
      idempotency precedent.
      Acceptance: the file exists, contains no `CASCADE`, and edits no other migration.
- [ ] T1.3 Rename **all 9** catalog objects (design.md D7, derived from the owner's query
      against a migrated database — **not** from reading the `CREATE TABLE` text). One
      checkbox per object; a literal statement fails loudly if its object is absent, which is
      the property being bought here:
      - [ ] T1.3.1 `supercharger_sessions_pkey` → `supercharger_history_pkey`
            (`ALTER TABLE … RENAME CONSTRAINT`)
      - [ ] T1.3.2 `supercharger_sessions_session_id_unique` →
            `supercharger_history_session_id_unique` (`ALTER TABLE … RENAME CONSTRAINT`)
      - [ ] T1.3.3 `supercharger_sessions_battery_pct_source_check` →
            `supercharger_history_battery_pct_source_check` (auto-named CHECK)
      - [ ] T1.3.4 `supercharger_sessions_start_battery_pct_check` →
            `supercharger_history_start_battery_pct_check` (auto-named CHECK)
      - [ ] T1.3.5 `supercharger_sessions_end_battery_pct_check` →
            `supercharger_history_end_battery_pct_check` (auto-named CHECK)
      - [ ] T1.3.6 `supercharger_sessions_start_battery_pct_est_check` →
            `supercharger_history_start_battery_pct_est_check` (auto-named CHECK)
      - [ ] T1.3.7 `supercharger_sessions_end_battery_pct_est_check` →
            `supercharger_history_end_battery_pct_est_check` (auto-named CHECK)
      - [ ] T1.3.8 `idx_supercharger_sessions_vehicle_time` →
            `idx_supercharger_history_vehicle_time` (`ALTER INDEX … RENAME TO`)
      - [ ] T1.3.9 `idx_supercharger_sessions_account_time` →
            `idx_supercharger_history_account_time` (`ALTER INDEX … RENAME TO`)

      **Do NOT issue a separate `ALTER INDEX` for the pkey's or the unique constraint's
      backing index.** In PostgreSQL a constraint-backed index shares the constraint's name and
      `RENAME CONSTRAINT` renames both together; a follow-up `ALTER INDEX` would fail on a name
      that no longer exists (design.md D7). Only the two **standalone** `CREATE INDEX` objects
      (T1.3.8/T1.3.9) need `ALTER INDEX`.
      Acceptance (owner-run, after `make migrate-up`) — the **negative** assertion is the
      binding one, because it cannot go stale as the table gains objects:
      ```sql
      SELECT conname FROM pg_constraint WHERE conname LIKE 'supercharger\_sessions%'
      UNION ALL
      SELECT indexname FROM pg_indexes WHERE indexname LIKE '%supercharger\_sessions%'
                                         AND schemaname = 'telemetry';
      ```
      Expected: **zero rows** (design.md Test Contract point 3). This is the check tier 3 did
      not run before archiving, and it is why tier 3 shipped with five constraints misnamed.
- [ ] T1.4 In the **same new migration**, refresh the `COMMENT ON` text sqlc copies into
      `models.go` (design.md D8) — 1 `COMMENT ON TABLE` + 3 `COMMENT ON COLUMN`:
      the table comment on `supercharger_history`, and the column comments on
      `start_battery_pct`, `start_battery_pct_est` and `end_battery_pct_est` (all three name
      `UpsertSuperchargerSession`, a query T2.2 retires). The refreshed text is otherwise
      **verbatim** — same wording, same `R3`/`D6` citations, same NULL conventions — with only
      `supercharger_sessions` → `supercharger_history` and `UpsertSuperchargerSession` →
      `UpsertSuperchargerHistory` substituted. **This is not an editorial pass.** Issuing a NEW
      `COMMENT ON` is permitted; editing the historic migration that set the old one is not
      (roadmap D1) — a comment is catalog state, so the later statement simply wins.
      `Down` deliberately does **not** restore the superseded comment text — this module's
      stated precedent (`20260815000001`'s own Down section).
      Acceptance: after T3.2 no doc comment in `internal/telemetry/db/models.go` names
      `supercharger_sessions` or `UpsertSuperchargerSession`. This closes debt tier 3 left open
      for the owner to adjudicate.

## T2. Schema-qualify `db/query.sql` + rename 5 query names — `[telemetry]` — no dependencies, parallel-ok with T1

- [ ] T2.1 Qualify **all 15** table references across the 15 `-- name:` blocks in
      `internal/telemetry/db/query.sql` (design.md D2 — forced by sqlc, which resolves names
      statically at *generate* time and exits 1 with `relation "…" does not exist` once a table
      leaves `public`; a role-level `search_path` cannot help, because the failure is at
      generate time, not run time):
      - `vehicle_snapshots` → `telemetry.vehicle_snapshots` — **7** refs
        (`InsertVehicleSnapshot`, `ListSnapshotsByVehicle`, `SnapshotsByVehicleSince`,
        `SnapshotsByVehicleBetween`, `SnapshotsByVehicleUpdatedSince`,
        `LatestSnapshotsByAccount`, `SnapshotPrecedingDay`)
      - `poll_attempts` → `telemetry.poll_attempts` — **2** refs (`InsertPollAttempt`,
        `ListPollAttemptsByVehicle`)
      - `supercharger_sessions` → `telemetry.supercharger_history` (schema **and** new name)
        — **5** refs (`UpsertSuperchargerSession` + the four `SuperchargerSessionsBy*` SELECTs)
      - `poll_runs` → `telemetry.poll_runs` — **1** ref (`InsertPollRun`)

      Acceptance: re-run the quote-agnostic pattern over the file and get zero matches —
      ```
      grep -nE '(FROM|INTO|UPDATE|JOIN)[[:space:]]+(vehicle_snapshots|supercharger_sessions|poll_attempts|poll_runs)\b' \
        internal/telemetry/db/query.sql
      ```
- [ ] T2.2 Rename the **5** `-- name:` lines that embed the old table name. This is a
      **different sqlc mechanism** from `gen.go.rename` (which remaps only table-derived struct
      names), so it means editing the `-- name:` line itself (design.md D4):
      - [ ] `UpsertSuperchargerSession` → `UpsertSuperchargerHistory`
      - [ ] `SuperchargerSessionsByAccount` → `SuperchargerHistoryByAccount`
      - [ ] `SuperchargerSessionsByVehicle` → `SuperchargerHistoryByVehicle`
      - [ ] `SuperchargerSessionsByVehicleBetween` → `SuperchargerHistoryByVehicleBetween`
      - [ ] `SuperchargerSessionsByVehicleUpdatedSince` →
            `SuperchargerHistoryByVehicleUpdatedSince`

      `History` singular is deliberate — "history" is a mass noun, so
      `SuperchargerHistoryByAccount` reads correctly for a many-row query. Rejected:
      `…HistoryEntriesBy…` (invents an "entry" noun absent from the schema) and
      `…HistoriesBy…` (implies several separate histories). The `*Params` types follow
      automatically. **Do NOT rename the port methods** — the identically-shaped
      `SuperchargerSessionsBy*` names on `SuperchargerReader` are tier 5's (D6).
      Acceptance: `grep -c '^-- name: \(Upsert\)\?SuperchargerSessions\?' internal/telemetry/db/query.sql`
      returns 0, and a grep for the five new names returns 5.
- [ ] T2.3 Update `query.sql`'s **prose comments** that name the old table or the two old index
      names (`idx_supercharger_sessions_vehicle_time`, `idx_supercharger_sessions_account_time`)
      in the index-reuse notes. sqlc copies `-- name:` block comments into `query.sql.go`, so
      this is a real generated artifact, not just a source file, and a comment naming a
      non-existent index is exactly the stale vocabulary roadmap D5a exists to retire.
      Command that measured the work (11 matching lines at design time, comments + SQL
      combined — re-run it and re-count rather than trusting this number):
      ```
      grep -n 'supercharger_sessions\|idx_supercharger_sessions' internal/telemetry/db/query.sql
      ```
      Acceptance: after T2.1–T2.3 the command above returns zero matches.

## T3. `sqlc.yaml` rename block + regeneration + the mandatory `models.go` diff — `[telemetry]` — depends on T1 AND T2

- [ ] T3.1 Add a `rename:` map under the **telemetry** entry's existing `gen.go` block in the
      root `sqlc.yaml` (**not** the top-level `overrides:` block, which sqlc ignores for this
      purpose). The telemetry entry has no `rename:` block at all today:
      ```yaml
      rename:
        telemetry_poll_attempt:          "PollAttempt"
        telemetry_poll_run:              "PollRun"
        telemetry_supercharger_history:  "SuperchargerHistory"
        telemetry_vehicle_snapshot:      "VehicleSnapshot"
      ```
      Key form is the **singularised** `<schema>_<table>`, using the table's name **after**
      T1.2's rename. Three keys are pure preservation (roadmap D3) — without them the schema
      move alone would produce `TelemetryPollAttempt`, `TelemetryPollRun`,
      `TelemetryVehicleSnapshot`, churning ~144 non-test call sites for nothing. The fourth is
      the deliberate NEW mapping (roadmap D5c). **Do not touch the `account`, `charging` or
      `analytics` entries in this file.**
- [ ] T3.2 Run `make sqlc` (leader-integrated step). Regenerates
      `internal/telemetry/db/models.go`, `db.go` and `query.sql.go`.
- [ ] T3.3 **`telemetry_supercharger_history` is a special risk and MUST be verified, not
      assumed.** The roadmap's finding — a wrong `rename` key is ignored with **no error and
      exit 0** — was measured on a regular plural. `history` is an irregular noun whose plural
      is `histories`, so sqlc's inflector may or may not treat the singular as already-singular.
      **Do not reason about it.** After `make sqlc`, read `models.go`: if the struct is named
      `TelemetrySuperchargerHistory` the key did not match, and the correct key must be found
      empirically (try `telemetry_supercharger_histories` next) before this task is complete.
- [ ] T3.4 **Diff `internal/telemetry/db/models.go` and confirm the EXACT expected shape from
      design.md's Test Contract point 1.** This is the mandatory verification step, not a
      nicety — it is the *only* detector of a silently-wrong `rename` key.
      **Expected diff: the type identifier `SuperchargerSession` → `SuperchargerHistory`, plus
      T1.4's comment refresh, and NOTHING ELSE.** Specifically:
      - `PollAttempt`, `PollRun`, `VehicleSnapshot` — struct bodies **byte-identical**: same
        type name, same field names, same field types, same order.
      - `SuperchargerHistory` — an **identical** field list, types and order to the old
        `SuperchargerSession` (`ID uuid.UUID`, `SessionID int64`, `AccountID uuid.UUID`,
        `Vin string`, `TeslaID pgtype.Int8`, … `EndBatteryPctEst pgtype.Int2`).
      - **Zero field-level changes anywhere.**
      Any `Telemetry`-prefixed struct name in the output means a `rename` key did not match.
      Acceptance: `git diff internal/telemetry/db/models.go` matches that description exactly;
      paste the diff into the final report.
- [ ] T3.5 Confirm by inspection that `internal/telemetry/db/query.sql.go` regenerated with the
      5 new query function names and their `*Params` types (`UpsertSuperchargerHistoryParams`,
      `SuperchargerHistoryByAccountParams`, …), with unchanged parameter shapes. No hand edits
      to any generated file. Acceptance: `go build ./internal/telemetry/...` fails only at the
      T4 call sites (expected until T4 lands), or succeeds if T4 is done in the same pass.

## T4. Rename the hand-written domain type and follow it through the module — `[telemetry]` — depends on T3

- [ ] T4.1 `internal/telemetry/telemetry.go`: rename `type SuperchargerSession struct { … }`
      (~line 427) to `SuperchargerHistory`, and update its doc comment to name the new table
      (design.md D5). This type is **not** sqlc-generated, so `gen.go.rename` does not reach
      it. It is the element type of all four `SuperchargerReader` method return slices, which
      is why the rename has consumers outside the module (T6/T7 — 7 references, all in
      `_test.go` files, all caught by `go vet`, which compiles test files).
- [ ] T4.2 `internal/telemetry/reader.go`: update the 4 renamed query-function call sites
      (`SuperchargerHistoryBy{Account,Vehicle,VehicleBetween,VehicleUpdatedSince}` + their
      `*Params` types) and every `SuperchargerSession` type reference to `SuperchargerHistory`.
      **`SuperchargerReader`, its four method names, `NewSuperchargerReader` and
      `superchargerReader` KEEP THEIR NAMES** (D6 — tier 5).
- [ ] T4.3 `internal/telemetry/mapping.go`: `rowToSuperchargerSession` changes its
      **signature** (it now takes `telemetrydb.SuperchargerHistory` and returns
      `SuperchargerHistory`) but **keeps its name** (D5/D6 — tier 5).
- [ ] T4.4 `internal/telemetry/service.go`: update the `UpsertSuperchargerHistory` call site
      and its params type. `upsertSuperchargerSession` changes its signature but **keeps its
      name** (D5/D6 — tier 5).
- [ ] T4.5 Sweep the module's remaining `SuperchargerSession` / `supercharger_sessions`
      mentions in **non-test** Go files by rule, not by list: a comment describing the table's
      or type's **current** identity uses the new name; a comment narrating **history** (an
      event tied to the name it had at the time, an immutable migration filename, or the rename
      itself) keeps the old name; and a reference to the *type* is simply wrong once renamed.
      Command (re-run and re-count; do not trust a stale figure):
      ```
      grep -rn 'SuperchargerSession\|supercharger_sessions' --include='*.go' internal/telemetry \
        | grep -v '_test.go' | grep -v '/db/'
      ```
      **Exempt from this sweep, by design:** the six tier-5 identifiers named in the Scope
      block, and anything under `internal/telemetry/db/migrations/` (never edited).
      Acceptance: `go build ./internal/telemetry/...`, `go vet ./internal/telemetry/...` and
      `gofmt -l internal/telemetry/` are all clean; every remaining match is either a tier-5
      identifier or a historical mention, and the final report states which rule each falls
      under.

## T5. FINAL WAVE — telemetry's own `_test.go` files (design.md D9) — `[telemetry]` — depends on T4

> **Why this is the final wave and not a parallel task.** These files carry BOTH raw SQL
> strings and `telemetry.SuperchargerSession` Go type references. The type references cannot
> compile until T4 lands, so the file is edited once, for both concerns. **Write the edits
> against design.md's Test Contract, not against the implementation** — no expected value in
> any of these tests changes; this tier renames names, not behavior.
>
> **There is no assistant-runnable signal for the SQL half.** sqlc never parses a Go string,
> and `go vet` compiles the test while treating its SQL as an opaque string. Only the owner's
> suite catches a missed statement. The grep below IS the acceptance criterion.

- [ ] T5.1 Schema- and name-qualify every hand-written SQL statement in this module's
      `_test.go` files. Command (the quote-agnostic pattern — match TABLE NAMES, never an
      opening quote; a quote-anchored pattern sees only double-quoted single-line SQL and
      misses every backtick multi-line string, which is how tier 2's first count came out 5
      instead of 18):
      ```
      grep -rnE '(FROM|INTO|UPDATE|JOIN)[[:space:]]+(vehicle_snapshots|supercharger_sessions|poll_attempts|poll_runs)\b' \
        --include='*_test.go' internal/telemetry
      ```
      Measured by this change's design phase, and re-confirmed at artifact time:
      **41 statements across 7 files** —
      - [ ] `db_integration_test.go` — 6 (1 `vehicle_snapshots`, 5 `poll_attempts`)
      - [ ] `db_poll_run_integration_test.go` — 5 (`poll_runs`)
      - [ ] `db_preceding_snapshot_integration_test.go` — 1 (`vehicle_snapshots`; this one is
            inside the `EXPLAIN (FORMAT TEXT)` query — see T5.3)
      - [ ] `db_sourcea_integration_test.go` — 2 (`vehicle_snapshots`)
      - [ ] `db_supercharger_battery_pct_integration_test.go` — 17 (`supercharger_sessions`)
      - [ ] `db_supercharger_between_integration_test.go` — 3 (`supercharger_sessions`)
      - [ ] `db_supercharger_integration_test.go` — 7 (`supercharger_sessions`)

      Every reference gains `telemetry.`; every `supercharger_sessions` reference **also**
      takes the new name `supercharger_history`. **Never reach for `search_path` (D10).**
      Acceptance: re-running the command above returns **zero matches**.
- [ ] T5.2 Update the `SuperchargerSession` Go type references in the same files to
      `SuperchargerHistory` (compile-checked by `go vet`, unlike the SQL strings). Command:
      ```
      grep -rn '\bSuperchargerSession\b' --include='*_test.go' internal/telemetry
      ```
      **Exempt:** references to the tier-5 identifiers listed in the Scope block (e.g. a call
      to `SuperchargerSessionsByAccount` on the port, or to `rowToSuperchargerSession`) — those
      names do not change in this tier.
      Acceptance: `go vet ./internal/telemetry/...` clean; every surviving match is a tier-5
      identifier.
- [ ] T5.3 `db_preceding_snapshot_integration_test.go`'s
      `TestReader_SnapshotPrecedingDay_UsesIndexBackwardScan` runs a literal
      `EXPLAIN (FORMAT TEXT)` copy of `SnapshotPrecedingDay`'s SELECT and asserts on plan text.
      Its `FROM vehicle_snapshots` **must** be qualified (it is one of T5.1's 41). Its three
      plan assertions — `"Index Scan Backward"`, `"idx_vehicle_snapshots_vehicle_time"` and the
      negative `"Seq Scan"` — are **unchanged**: that index is on `vehicle_snapshots`, a table
      this tier **moves but does not rename**, so the index name is untouched (design.md D13).
      **This is an expected-value NON-change and must be stated as such in the report** —
      design.md's Test Contract point 6 asks for a recorded finding here, and the honest finding
      is "an EXPLAIN-text assertion exists in this module, and it names no renamed object",
      not "the grep is clean". Command:
      ```
      grep -rn 'EXPLAIN' --include='*_test.go' internal/telemetry
      ```
      Acceptance: no assertion string in this module names `idx_supercharger_sessions_*`; the
      three assertions above keep their exact literals; the SQL under them is qualified.

## T6. FINAL WAVE — `internal/analytics/db_integration_test.go` — `[leader-owned]` — depends on T4

> Outside the telemetry sandbox. **Test file only** — no analytics production file is touched.

- [ ] T6.1 Qualify the **4** raw SQL statements that seed/read telemetry's tables (design.md
      D9; **the roadmap under-counted this file at 2** — it named only the two
      `supercharger_sessions` statements, but `seedSnapshot`'s INSERT and the same-day-recapture
      UPDATE hit `vehicle_snapshots` and are equally affected and equally invisible to every
      runnable signal):
      - [ ] `INSERT INTO vehicle_snapshots` (`seedSnapshot`) → `telemetry.vehicle_snapshots`
      - [ ] `INSERT INTO supercharger_sessions` (`seedSuperchargerSession`) →
            `telemetry.supercharger_history`
      - [ ] `UPDATE supercharger_sessions …` (`reviseSuperchargerSession`) →
            `telemetry.supercharger_history`
      - [ ] `UPDATE vehicle_snapshots …` (same-day-recapture simulation) →
            `telemetry.vehicle_snapshots`

      Acceptance: the T5.1 grep command, run against `internal/analytics`, returns zero
      matches.
- [ ] T6.2 Update the **3** `telemetry.SuperchargerSession` Go type references to
      `telemetry.SuperchargerHistory` (the `seedSuperchargerSession` doc comment, its parameter
      type, and its one call site). Acceptance: `go vet ./internal/analytics/...` clean.
- [ ] T6.3 **Confirm, do not assume, that analytics' two watermark replay tests are
      unaffected** (design.md D10 / Test Contract point 7).
      `db_watermark_migration_integration_test.go` and
      `db_watermark_supercharger_migration_integration_test.go` drive a `goose.Provider` scoped
      to **analytics'** own `db/migrations`, replaying statements that name only
      `vehicle_metric_watermarks`. **Their `search_path` DSN must NOT be edited** — extending a
      search path here is exactly D10's hazard. Acceptance: `git diff` shows no change to
      either file; record the confirmation in the report.

## T7. FINAL WAVE — `internal/app/processor_test.go` — `[leader-owned]` — depends on T4

> Outside the telemetry sandbox. **Test file only.**

- [ ] T7.1 Update the **4** `telemetry.SuperchargerSession` references in
      `fakeSuperchargerReader`'s four methods to `telemetry.SuperchargerHistory`. The **method
      names stay `SuperchargerSessionsBy*`** — the fake implements the port, and the port is
      not renamed in this tier (D6). Acceptance: `go vet ./internal/app/...` clean; a grep for
      `telemetry.SuperchargerSession` in this file returns zero matches while the four method
      names are unchanged.

## T8. FINAL WAVE — `internal/charging` (D11 + D12) — `[leader-owned]` — depends on T4

> Outside the telemetry sandbox. **The single most likely way this tier ships broken.**

- [ ] T8.1 **`db_backfill_integration_test.go`'s `runBackfill` must map THREE names forward,
      not one** (design.md D11 — a finding this change adds to the roadmap's own list). The
      test extracts the shipped backfill statement from the historic migration at runtime
      (between `-- BACKFILL-BEGIN` / `-- BACKFILL-END`) and re-executes it, rewriting names
      forward because historic migrations are never edited. Today it rewrites exactly one (the
      `INSERT` target). Add the two missing mappings:
      - [ ] `to_regclass('public.supercharger_sessions')` →
            `to_regclass('telemetry.supercharger_history')` — **the dangerous one.** Miss it
            and the `DO` block's guard evaluates to `NULL` forever, `RETURN`s early, raises **no
            error** (just a `NOTICE`), inserts nothing, and A1/A2
            (`TestBackfill_RealFourRowDataset_OnePercentageBearing`,
            `TestBackfill_IdempotentOnRerun_OverwritesNothing`) fail on **empty assertions** —
            a failure that reads like a data bug rather than a name bug.
      - [ ] `FROM supercharger_sessions s` → `FROM telemetry.supercharger_history s`
      - [ ] (existing, unchanged) the `INSERT INTO charge_sessions (` →
            `INSERT INTO charging.supercharger_sessions (` target

      Each new mapping carries the same **"exactly one occurrence, else `t.Fatalf`"** guard the
      existing one uses, so a future change to the migration's shape fails loudly.
      The `NOTICE` string inside the guard also names the table; it is prose inside a `RAISE`,
      so rewriting it is **optional and not required** — leaving it avoids a fourth fragile
      string match. Record which was chosen.
      Acceptance: three guarded mappings present; `go vet ./internal/charging/...` clean.
- [ ] T8.2 Rewrite that file's **header comment**, which currently explains the ONE-mapping
      rule and states that `FROM supercharger_sessions` "is already correct and must be left
      alone" until RM39 tier 4 lands. It must now explain the THREE-mapping rule, including why
      `search_path` is still the wrong tool (D10 — a bare `supercharger_sessions` would resolve
      to **charging's** table and succeed while reading the wrong data). The existing
      `search_path` argument in that comment survives this tier intact.
- [ ] T8.3 Qualify the **3** ordinary D9 statements in the same file that seed/clean/read
      telemetry's table — `insertSuperchargerSessionFixture`'s `INSERT INTO
      supercharger_sessions`, `cleanupSuperchargerSessions`'s `DELETE FROM …`, and the
      source-row read-back's `FROM supercharger_sessions` — all to
      `telemetry.supercharger_history`. Acceptance: the T5.1 grep command, run against
      `internal/charging`, returns zero SQL matches (comment lines that merely mention the name
      are T8.5's).
- [ ] T8.4 **`internal/charging/testdb_test.go` needs no change** to its
      `ProvisionDirs(ctx, "../telemetry/db/migrations", "db/migrations")` call — it takes
      **directories**, not names, and the ordering it depends on is unchanged (design.md D11).
      Confirm and record; only its stale **comment** is in scope (T8.5).
- [ ] T8.5 Sweep the module's comments naming `telemetry.SuperchargerSession` or telemetry's
      old table name: `charging.go` (~L247, the field-mirroring note), `session_reader.go`,
      `testdb_test.go`, and `db_backfill_integration_test.go`'s remaining prose. Apply T4.5's
      rule — current identity gets the new name, a history-narrating comment keeps the old one.
      Command: `grep -rn 'telemetry\.SuperchargerSession\|supercharger_sessions' internal/charging`
      (expect matches for **charging's own** `charging.supercharger_sessions` too — those are a
      **different table** and must NOT be changed; this is the D10 hazard in prose form).
- [ ] T8.6 **Comment-only correction inside the shipped migration**
      `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql` (roadmap D25 /
      design.md D12). **Roadmap D1 forbids editing its SQL. Not one character of SQL changes.**
      - [ ] ~L213: the comment claiming *"In a real database the guard always passes:
            MIGRATIONS_DIRS runs telemetry before charging."* After this tier
            `to_regclass('public.supercharger_sessions')` is `NULL` permanently, so the guard
            **always skips**. The corrected comment must say so **and why it is harmless**: on
            a database that already ran this migration the backfill committed its rows long
            ago; on a fresh database there is nothing to copy, because telemetry's table is
            created empty in the same `goose up`.
      - [ ] ~L277 and ~L284 (the `-- +goose Down` header): two more comments name
            `telemetry.supercharger_sessions`, which ceases to exist. **The roadmap did not
            name these** — found by inspection during design (D12), recorded rather than
            silently fixed. They are pure prose about data safety, so the correction is a name
            substitution to `telemetry.supercharger_history`.
      - [ ] **Record the decision explicitly.** Design.md D12 says the leader may fold these two
            into this task or defer them, and that *either is defensible but choosing silently
            is not* — "I did not think about it" and "it is unaffected" are different findings,
            and only the second is a finding (`CLAUDE.md`, docs rule).
      Acceptance: `git diff` on this file shows changes on comment lines only; `make
      migration-guard` still passes.

## T9. Module docs — `[telemetry]` — depends on T1, parallel-ok with T2–T8

- [ ] T9.1 Update `internal/telemetry/AGENTS.md` §"Data ownership": state that this module's
      data lives in the **`telemetry` Postgres schema**, list the four tables under their
      current names, and record the `supercharger_sessions` → `supercharger_history` rename
      (roadmap D5a) with the Go type following (`SuperchargerSession` → `SuperchargerHistory`,
      roadmap D5c).
- [ ] T9.2 In the same file, update every place that names the old table or type — the
      §"Public interface" `SuperchargerReader` block, the §"Why nightly collection exists"
      dedup paragraph, the §"Battery-% verification columns" section, and the DTO/units notes.
      **State the tier-5 half-state explicitly and loudly**, so the next agent reading this
      file does not "tidy" it: the port is named `SuperchargerReader`, its four methods are
      named `SuperchargerSessionsBy*`, and they return `[]SuperchargerHistory` — by design,
      until `RM39-telemetry-rename-supercharger-port`.
- [ ] T9.3 Update the AGENTS.md mentions of `UpsertSuperchargerSession` (the write-exclusion
      convention for the five battery-% columns) to `UpsertSuperchargerHistory` — the query name
      T2.2 renames. The convention itself is unchanged: all five columns stay excluded from the
      `INSERT` column list and the `ON CONFLICT DO UPDATE SET` clause.
      Acceptance: `grep -n 'supercharger_sessions\|UpsertSuperchargerSession' internal/telemetry/AGENTS.md`
      returns only history-narrating sentences (migration filenames, "renamed from" notes).

## T10. Repo docs + knowledge base — `[leader-owned]` — depends on T1, parallel-ok with T2–T9

> `CLAUDE.md`'s docs rule makes this part of **this** change, never a follow-up, and names
> `kkpa/context/` explicitly as the doc most often forgotten and most expensive to leave wrong
> — `kkpa-context-fetch` presents it as authoritative, so a stale guide makes an agent trust it
> *instead of* reading the code. Outside the telemetry worker's sandbox (`README.md`, `ai/`,
> `docs/`, `kkpa/`).
>
> **The discrimination rule for every single occurrence** (this is the D10 hazard in prose
> form, and it is why this task is per-occurrence rather than per-file): a mention of
> `supercharger_sessions` prefixed `charging.` — or describing charging's mirror table — is
> **charging's own table and must NOT be changed**. Only telemetry's mentions become
> `telemetry.supercharger_history`. The two names are one word apart and adjacent in every one
> of these files.

- [ ] T10.1 Command that produced the file list below (re-run it; do not trust the counts):
      ```
      grep -rc 'supercharger_sessions' kkpa/context/ docs/ ai/ README.md | grep -v ':0$'
      ```
      Live KB + docs files with mentions (count at artifact time):
      - [ ] `kkpa/context/architecture/telemetry-ingest-only.md` (11) — **the primary one.**
            Resolve the file's top **PENDING banner**, which parks the
            `telemetry.supercharger_sessions` → `supercharger_history` half of the rename on
            "a separate, blocked boundary ticket (roadmap D6)". Roadmap D25 retired that block
            (design.md D15). Also fix the module-role table's table list. **No retitling is
            needed** — the roadmap's §"Knowledge-base debt" says a file
            `telemetry-data-hub.md` must be corrected and retitled; **that file no longer
            exists**, tier 3 already did that work (design.md D15 records the correction).
      - [ ] `kkpa/context/architecture/nightly-cycle.md` (8) — mixed telemetry/charging
            mentions; apply the discrimination rule per occurrence.
      - [ ] `kkpa/context/workflows/supercharger-stats-read.md` (12) — mixed; also check for
            index names.
      - [ ] `kkpa/context/INDEX.md` (5) — mixed.
      - [ ] `kkpa/context/entities/vehicle-metrics/guide.md` (5) — mixed; the
            watermark-vocabulary line is analytics' (tier 3b already settled it) — leave it.
      - [ ] `kkpa/context/architecture/charge-record-mutation.md` (4) — expected to be
            charging-side only; confirm and record if no edit is needed.
      - [ ] `kkpa/context/use-case/charging/verify-session-battery.md` (5),
            `update-manual-charge.md` (2), `delete-manual-charge.md` (1),
            `kkpa/context/workflows/manual-charge-crud.md` (2) — expected charging-side only;
            confirm per occurrence.
      - [ ] `README.md` (4) — includes the "Two different tables share the base name
            `supercharger_sessions`" note that **this tier makes obsolete**, plus the
            schema/table table, which now must show `telemetry` as this module's schema.
      - [ ] `docs/battery-consumed-graph.md` (3)
      - [ ] `ai/go-conventions.md` (1) — the §Testing paragraph on `MIGRATIONS_DIRS` ordering
            names `telemetry.supercharger_sessions` as the backfill's source table.
      **Excluded deliberately:** everything under `kkpa/context/pending-spec-to-sync/` — that
      is the curator's staging area, not a live guide; and `kkpa/context/**/applied/`, which is
      an archive of what was applied at the time. Record this exclusion rather than skipping
      silently.
      Acceptance: every file above is either edited or explicitly left with a recorded reason.
      **Do not edit speculatively** — an occurrence you cannot attribute to telemetry or
      charging gets reported, not guessed.
- [ ] T10.2 Sweep the pure **schema-move** mentions (`vehicle_snapshots`, `poll_attempts`,
      `poll_runs`) for any doc that asserts a specific schema. Tiers 1 and 2 both concluded no
      edit was needed, because prose names tables without a `public.` prefix. Confirm the same
      here and **record the conclusion** — the reverse-direction docs rule makes "I checked and
      it is unaffected" a finding, and "I did not think about it" not one.
      ```
      grep -rn 'public\.\(vehicle_snapshots\|poll_attempts\|poll_runs\|supercharger_sessions\)' \
        kkpa/context/ docs/ ai/ README.md
      ```

## T11. Verification — `[telemetry]` for the assistant-runnable half — depends on T1–T10

- [ ] T11.1 Claude-runnable signals, repo-wide: `go build ./...`, `go vet ./...`, `gofmt -l`
      (expect empty). `go vet` compiles `_test.go` files, so it is the signal that catches every
      `telemetry.SuperchargerSession` reference this tier renames — inside the module and all 7
      outside it. It catches **none** of the raw SQL work in T5.1/T6.1/T8.3, which is exactly
      why those tasks carry their own grep acceptance criteria.
- [ ] T11.2 `make migration-guard` passes (the new migration's version is globally unique across
      every module directory — they share one `goose_db_version` table).
- [ ] T11.3 `make boundary-guard` passes. It greps `internal/gateway/**` for the
      `internal/telemetry` **import path**, which this tier does not touch — `internal/gateway`
      is not edited by any task here.
- [ ] T11.4 **Makefile / tooling re-check** (`CLAUDE.md`'s reverse-direction docs rule).
      design.md §"Makefile / tooling re-check" already performed this and recorded the finding
      *"nothing in the build tooling requires a change for this tier"*. **Re-confirm each line
      rather than restating it**, and record any drift: `MIGRATIONS_DIRS` order and membership
      (`account → telemetry → charging → analytics`, unchanged — one file added to an
      already-listed directory, no new directory); `db-setup`/`db-reset` role-and-ownership
      (migrations run as `APP_ROLE`, so `CREATE SCHEMA telemetry` inside a migration makes the
      app role the schema **owner** — no `GRANT`, no `search_path` change); `sqlc.yaml`
      structure (the existing telemetry `sql:` entry edited **in place**; no new entry, no new
      `out:` path); `internal/testdb` (unchanged — telemetry uses `Provision(ctx, fsys)` over
      its own `//go:embed db/migrations/*.sql`, and `analytics`/`charging` use `ProvisionDirs`
      with **directory** arguments this tier does not rename or move); the other guards
      (`ui`, `i18n`, `money`, `tz`) are schema-agnostic.
- [ ] T11.5 Sandbox check: no file outside `internal/telemetry/` (plus the leader-granted
      `sqlc.yaml` telemetry entry and this change's `openspec/changes/…` folder) was touched by
      a `[telemetry]` task. Confirm no other module's `sqlc.yaml` entry, migrations directory
      or `query.sql` was touched.
- [ ] T11.6 **State in the final report the exact catalog-verification queries** from
      design.md's Test Contract points 2–5, ready for the owner to paste after
      `make migrate-up`:
      ```sql
      -- 2a. all four resolve under the new schema (expect 4 non-NULL)
      SELECT to_regclass('telemetry.vehicle_snapshots'),
             to_regclass('telemetry.supercharger_history'),
             to_regclass('telemetry.poll_attempts'),
             to_regclass('telemetry.poll_runs');

      -- 2b. neither the old location nor the old name survives (expect 5 NULL)
      SELECT to_regclass('public.vehicle_snapshots'),
             to_regclass('public.supercharger_sessions'),
             to_regclass('public.poll_attempts'),
             to_regclass('public.poll_runs'),
             to_regclass('telemetry.supercharger_sessions');

      -- 2c. charging's own table is UNTOUCHED (expect non-NULL) — the D10 confusion guard
      SELECT to_regclass('charging.supercharger_sessions');

      -- 3a. constraints (expect exactly the 7 supercharger_history_* names)
      SELECT conname FROM pg_constraint
      WHERE conrelid = 'telemetry.supercharger_history'::regclass ORDER BY conname;

      -- 3b. indexes (expect idx_supercharger_history_account_time,
      --     idx_supercharger_history_vehicle_time, supercharger_history_pkey,
      --     supercharger_history_session_id_unique)
      SELECT indexname FROM pg_indexes
      WHERE schemaname = 'telemetry' AND tablename = 'supercharger_history'
      ORDER BY indexname;

      -- 3c. THE BINDING ASSERTION (expect ZERO rows) — it cannot go stale as the
      --     table gains objects, and it is the check tier 3 did not run
      SELECT conname FROM pg_constraint WHERE conname LIKE 'supercharger\_sessions%'
      UNION ALL
      SELECT indexname FROM pg_indexes WHERE indexname LIKE '%supercharger\_sessions%'
                                         AND schemaname = 'telemetry';
      ```
      Also state Test Contract point 4 (**row counts byte-identical** across the migration: for
      each of the four tables, the count against `telemetry.<new name>` immediately after
      `make migrate-up` must equal the count against `public.<old name>` immediately before —
      manual verification, not a new automated test) and point 5 (**Down round-trips**: one
      `goose down` step restores all four tables to `public`, restores the
      `supercharger_sessions` name, restores all nine object names and drops the `telemetry`
      schema **without `CASCADE`**; re-running `goose up` lands the same catalog state as
      point 3).
- [ ] T11.7 **Report the exact test-suite commands the owner must run.** Per
      `Test-Execution-Policy` this change does **not** execute them; every task whose completion
      depends on the suite is `awaiting-user-verification`, never `done`, until the owner
      reports a result — and a passing suite is recorded as the **owner's** report, never
      claimed by the assistant. The commands:
      ```
      go test ./internal/telemetry/...
      go test ./internal/analytics/... ./internal/charging/... ./internal/app/...
      go test ./...
      make test            # disposable container — never the owner's live database
      make test-with-db    # runs against DATABASE_URL — throwaway/CI Postgres ONLY
      make check
      ```
      Design.md's Test Contract point 6 binds the expected outcome: **every existing test passes
      with its exact same expected values.** This tier changes names, not behavior. Two failure
      signatures to watch for and what each means:
      - `internal/charging` A1/A2 failing on **empty** results (not a missing relation) ⇒ a
        missed `to_regclass` guard mapping in T8.1.
      - A `relation "…" does not exist` in any DB-backed test ⇒ a raw SQL statement missed by
        T5.1 / T6.1 / T8.3.
- [ ] T11.8 `openspec validate RM39-telemetry-move-to-own-schema --strict` passes, and every
      checkbox above reflects real completion — **no task title or acceptance criterion may be
      edited, weakened or deleted to make the work look done.**
