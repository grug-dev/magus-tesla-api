# Tasks — RM29-charging-add-charge-sessions

Ownership legend: **[module: charging worker]** — inside `internal/charging/` only.
**[leader]** — outside every module's sandbox (`cmd/`, `ai/`, root docs, `openspec/`).
No sub-task below edits a file outside its own owner's scope, and **no sub-task edits
anything under `internal/telemetry/`** — that module is untouched by this change
(design.md **D4**). See design.md D1–D9 for the rationale behind each group.

**This change is PHASED, not atomic** — the opposite of tier 5. Because nothing is
removed and no consumer is re-pointed (design.md D4/D9), the tree compiles and the
existing suite passes at the end of **every** wave below. Each wave can be committed on
its own.

**Ordering constraints:**

- Wave 1 (schema) before Wave 2 (Go): `sqlc` generates `chargingdb.MirrorChargeSessionParams`
  from `query.sql` validated against this module's migration directory, so the table must
  exist there first.
- Wave 2 before Wave 3 (tests): the `DATABASE_URL`-gated tests cannot compile until
  `charging.SessionMirror` / `SessionWriter` exist (`ai/go-conventions.md` §Testing
  authoring order — their expected values are already fixed in design.md §Test Contract).
- Wave 3 before Wave 4 (`cmd/poller`): the wiring calls the port the tests prove.
- `make sqlc` runs **twice** in this change: once after 1.1 (catalog only — no query yet)
  and once after 1.2. Both must be run; only the second generates the query methods.

---

## Wave 1 — schema (module: charging worker)

- [x] **1.1** **[module: charging worker]** Create
  `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql`, transcribing
  design.md §"Database Changes" **verbatim** — the full `CREATE TABLE charge_sessions`
  with every column, both named constraints (`charge_sessions_account_session_unique`,
  `charge_sessions_pct_source_required`) and all five value CHECKs, the `COMMENT ON
  TABLE` and all eleven `COMMENT ON COLUMN` statements (`tesla_id`,
  `site_location_name`, `energy_kwh`, `total_cost`, `currency`, `is_paid`,
  `start_battery_pct`, `end_battery_pct`, `battery_pct_source`,
  `start_battery_pct_est`, `end_battery_pct_est`), `CREATE INDEX
  idx_charge_sessions_vehicle_stop`, and the guarded `DO $$ … $$` backfill wrapped in
  `-- +goose StatementBegin` / `-- +goose StatementEnd` **and** in the
  `-- BACKFILL-BEGIN` / `-- BACKFILL-END` sentinels (task 3.3 extracts the block between
  them at runtime — do not rename or drop the sentinels). Then the `-- +goose Down`
  block. **Do not simplify or re-word the comments** — they carry the load-bearing
  rationale (why no `raw_data`, why no FK, why no `stop >= start` CHECK, why stop-time
  leads the index, why the backfill is guarded).
  **Then run `make sqlc` and report the result — as a regression check, not an
  experiment.** design.md D8(b) records that this is **already verified by probe**: the
  leader wrote this exact migration into `internal/charging/db/migrations/` before Apply,
  ran `make sqlc`, confirmed **exit 0** with `chargingdb` gaining a correct `ChargeSession`
  model carrying every column and **no `supercharger_sessions` reference leaking** into
  charging's generated code, then reverted the tree. A PL/pgSQL body is opaque to the SQL
  parser, so sqlc ignores the `DO $$ … $$` block entirely and there is **no fallback to
  design for**. The `EXECUTE`-dynamic-SQL escape hatch a pre-gate draft carried has been
  **deleted as dead weight** — do not add it, reference it, or "restore" it here even if
  `make sqlc` behaves unexpectedly; if it does, stop and report the discrepancy instead of
  patching around it, since it would contradict a fact the leader already confirmed.
  `depends_on`: — · `parallel_ok`: no (blocks everything)

- [x] **1.2** **[module: charging worker]** `internal/charging/db/query.sql` — append
  `-- name: MirrorChargeSession :exec` exactly as specified in design.md §"The sync
  query", including its full doc comment. The LOAD-BEARING paragraph (the five
  percentage columns absent from both the INSERT list and the `ON CONFLICT DO UPDATE
  SET`) and the `IS DISTINCT FROM` note are part of the deliverable, not decoration.
  Run `make sqlc` — this generates `chargingdb.MirrorChargeSessionParams` and the
  `MirrorChargeSession` method.
  `depends_on`: 1.1 · `parallel_ok`: no

---

## Wave 2 — the Go port (module: charging worker)

- [x] **2.1** **[module: charging worker]** `internal/charging/charging.go` — add
  `SessionMirror` (eleven fields: `AccountID`, `VIN`, `TeslaID`, `SessionID`,
  `ChargeStartDateTime`, `ChargeStopDateTime`, `SiteLocationName`, `EnergyKWh`,
  `TotalCost`, `Currency`, `IsPaid` — no battery-percentage field), the `SessionWriter`
  interface (one method, `MirrorSessions`), and the forward-declaring constructor
  `func NewSessionWriter(pool *pgxpool.Pool) SessionWriter`, exactly as specified in
  design.md **D6**. `SessionMirror` **must not** gain a battery-percentage field — its
  absence is the write-protection (design.md D6); a doc comment on the type says so, so
  a future reader does not "complete" it. Follow this file's existing conventions: no
  `pgtype` anywhere in it, `*T` for optional values, doc comments on every exported
  symbol. Does not compile until 2.2 supplies the constructor's body.
  `depends_on`: 1.2 · `parallel_ok`: with 2.2 (authoring only — they land together)

- [x] **2.2** **[module: charging worker]** `internal/charging/session_writer.go` (new
  file) — implement the port, mirroring `internal/analytics/gap_writer.go`'s shape:
  an unexported `sessionWriter` struct over `*pgxpool.Pool` + `*chargingdb.Queries`, an
  unexported `newSessionWriter`, the compile-time
  `var _ SessionWriter = (*sessionWriter)(nil)` assertion, and `MirrorSessions` with a
  **validate-then-transact** body in that order:
  1. Validate every entry's `AccountID == accountID` before `tx.Begin` — a single
     mis-scoped entry returns an error and writes nothing (design.md D6, Test Contract
     B6).
  2. Return `nil` immediately for an empty/nil slice, before opening a transaction
     (Test Contract B7).
  3. One transaction, one `MirrorChargeSession` call per entry, commit-or-rollback.
  `pgtype` is confined to this file (per `internal/charging/AGENTS.md` §Allowed
  Imports — extend the `service.go`-only note to name this file too, in task 3.4).
  `SessionMirror`'s nullable fields need more conversions than `service.go` currently
  has: reuse `stringPtrToPgText` (`*string → pgtype.Text`, already exists, covers
  `Currency`) and `service.go`'s existing pattern for a nullable numeric/int wrapper to
  add whichever of these `service.go` does not already provide — `*int64 → pgtype.Int8`
  (`TeslaID`), `*float64 → pgtype.Float8` (`EnergyKWh`, `TotalCost`), and
  `*bool → pgtype.Bool` (`IsPaid`) — following the existing `intPtrToPgInt2` /
  `pgInt2ToIntPtr` naming and round-trip-pair shape rather than inventing a new one.
  `depends_on`: 2.1 · `parallel_ok`: with 2.1

---

## Wave 3 — tests + module docs (module: charging worker)

- [ ] **3.1** **[module: charging worker]** `internal/charging/testdb_test.go` — switch
  `testdb.Provision(ctx, subFS)` to
  `testdb.ProvisionDirs(ctx, "../telemetry/db/migrations", "db/migrations")`, telemetry
  **first** (design.md D8c). Remove the now-unused `//go:embed`/`embed`/`io/fs` plumbing
  **only if** task 3.3 does not need it — 3.3 reads the migration file through that same
  embed to extract the backfill block, so coordinate: keep `migrationsFS` and drop only
  the `fs.Sub` call. Update the file's header comment to say which directories are
  applied and why (it currently claims "migrations are embedded under db/migrations/").
  Every existing `manual_charge_entries` test must still pass unchanged.
  `depends_on`: 2.2 · `parallel_ok`: with 3.4

- [ ] **3.2** **[module: charging worker]** `internal/charging/db_session_integration_test.go`
  (new file, `package charging_test`) — implement Test Contract **groups B and C**
  (B1–B11, C1–C7) exactly as design.md states them, with those expected values. B9–B11
  cover the nullable fee columns and account/vehicle scoping; C6–C7 cover
  `site_location_name` NOT NULL / fee-column nullability and pin that no CHECK is
  stricter than the source (design.md D5). Read rows
  back with direct SQL over `testPool` (there is no reader port — design.md D9) and add
  the small helpers this needs (`fetchChargeSession`, `countChargeSessions`,
  `cleanupChargeSessions`) as this file's own test helpers, mirroring tier 5's
  `fetchChargeGap`/`countChargeGaps` precedent. Use `session_id`s in `920001`–`920099`
  and fresh `uuid.New()` account ids. Assert against `charging.SessionMirror` domain
  fields and raw SQL columns only — **no `pgtype` in any assertion**
  (`internal/charging/AGENTS.md` §Testing Notes).
  `depends_on`: 3.1 · `parallel_ok`: with 3.3, 3.4

- [ ] **3.3** **[module: charging worker]** `internal/charging/db_backfill_integration_test.go`
  (new file, `package charging_test`) — implement Test Contract **group A** (A1, A2).
  Extract the backfill statement at runtime from the embedded migration, slicing between
  the `-- BACKFILL-BEGIN` and `-- BACKFILL-END` sentinels task 1.1 wrote, and execute
  that text — **do not** copy the SQL into the test as a const (design.md D8c: two copies
  drift, and the copy that drifts is the one nobody runs in production). Seed
  `supercharger_sessions` with direct `INSERT`s reproducing the live 4-row dataset
  (`vin = 'LRWYGCFJ8TC495757'`, `tesla_id = 3744327027802250`,
  `site_location_name = 'Medellín, Colombia'` on all four, all four carrying non-null
  `energy_kwh`, non-null `total_cost`, `currency = 'COP'` and `is_paid = true`; one of
  them `session_id = 734860294` with `29`/`100`/NULL source and both `_est` columns NULL,
  the other three with all five percentage columns NULL) — Test Contract A1's exact
  fixture. Direct `INSERT`s into another
  module's table from a `_test.go` file are the sanctioned form here
  (`ai/go-conventions.md` §Testing, RM29 decision D19) — **do not** import
  `internal/telemetry`.
  `depends_on`: 3.1 · `parallel_ok`: with 3.2, 3.4

- [ ] **3.4** **[module: charging worker]** `internal/charging/AGENTS.md` — update for the
  module's new scope (docs-track-structural-change, `CLAUDE.md` §Non-negotiables):
  - §Responsibility — the module now owns Supercharger charge sessions too; replace the
    "will also own `charge_sessions` … starting at tier 6" future-tense sentence with
    what is now true.
  - §Public Interface — add `SessionMirror`, `SessionWriter`, `NewSessionWriter`, and
    state plainly that `SessionMirror` has no percentage fields **by design** and why.
  - §Allowed Imports — `pgtype` is now allowed in `session_writer.go` as well as
    `service.go`. The `MUST NOT import internal/telemetry` / `internal/account` rules are
    **unchanged** and still hold: the mirror's data arrives already mapped, from
    `cmd/poller`.
  - §Data Ownership — add `charge_sessions` and its migration file as a second owned
    table; note that `telemetry.supercharger_sessions` still carries its own copy of the
    five percentage columns until the deferred contract change (design.md D9).
  - §Testing Notes — the package now provisions **two** migration directories via
    `testdb.ProvisionDirs` (telemetry first), why, and that this is a path dependency,
    not an import.
  `depends_on`: 2.2 · `parallel_ok`: with 3.1, 3.2, 3.3

---

## Wave 4 — orchestration + project docs (leader)

- [ ] **4.1** **[leader]** `cmd/poller/main.go` — wire the nightly mirror
  (design.md **D7**). Build `chargingSessionWriter := charging.NewSessionWriter(pool)`
  alongside the existing ports, and add a mirror step that runs **before** the existing
  reconcile step inside `reconcilingCollector.CollectAll`'s post-cycle work, so both the
  scheduled path and `--once` get it by construction. Per distinct account (deduplicated
  from `acct.AllRegisteredVehicles`, the enumeration the reconciler already uses):
  `superchargerReader.SuperchargerSessionsByAccount(ctx, accountID, 0)` → map each
  `telemetry.SuperchargerSession` to `charging.SessionMirror` (eleven fields,
  field-name-for-field-name — no renames, no derivation; design.md D7) →
  `chargingSessionWriter.MirrorSessions(ctx, accountID, mirrored)`. Per-account
  isolation: log and continue on error, never fatal, mirroring the reconciler's shape;
  prefix log lines `"session mirror:"` so they stay greppable. **Do not touch** the
  existing reconcile step's body, ordering, or error handling.
  **Read this task's diff at review — do not trust the signals.** `cmd/poller` has no
  tests and a call that is never made still compiles; RM29 tier 3's review caught exactly
  that failure here.
  `depends_on`: 3.2 · `parallel_ok`: with 4.2

- [ ] **4.2** **[leader]** Project docs (docs-track-change, `CLAUDE.md` §Non-negotiables):
  - `ai/go-conventions.md` §Testing — the sentence "Ordering between directories matters
    only where one module's schema depends on another's. Today none do" is now false.
    Correct it: `internal/charging`'s migration reads `telemetry.supercharger_sessions`
    in a **guarded** backfill, so `telemetry` must precede `charging` for the data to
    land — but the guard means ordering affects data completeness, never migration
    success (design.md D8a). `MIGRATIONS_DIRS` already has the right order.
  - Root `README.md` — the "Architecture" table / module responsibilities line for
    `charging` gains `charge_sessions` alongside `manual_charge_entries`. Check the
    "Project Structure" tree for a migrations listing that needs the new file.
  `depends_on`: 1.1 · `parallel_ok`: with 4.1

- [ ] **4.3** **[leader]** `openspec/roadmaps/RM29-modular-monolith-boundaries.md` — flip
  the **T6** row from `[ ]` to `[~]` when this change's artifacts are created, and to
  `[x]` at archive; update §Status (T6 in flight / archived; T7 remains). Mirror the same
  status into `RM29-modular-monolith-boundaries.progress.json`. **Do not** edit the D4
  decision text or any tier's scope.
  `depends_on`: — · `parallel_ok`: with everything

---

## Not in this change — do not do these

Listed so no worker "completes the pattern" and so review can reject them fast:

- **Any edit under `internal/telemetry/`.** Not a column, index, query, comment, type or
  test. Tier 6 is expand-only (design.md D4); a DROP here runs before the backfill and
  destroys the data.
- **A reader port on `charge_sessions`.** It would have no caller (design.md D9).
- **Re-pointing `internal/analytics/consumed.go`** (`sumSuperchargerPctBetween`,
  `inferMissingChargingType`) or changing `deriveVehicleMetrics`' signature. That is the
  deferred contract change; design.md D9 spells out its six parts.
- **Merging `charge_sessions` with `manual_charge_entries`.** Roadmap D4 defers
  convergence to backlog item 12.
- **Writing a battery percentage from anywhere.** The verification UI is backlog item 11.
- **Adding a `charge_stop_date_time >= charge_start_date_time` CHECK.** Deliberately
  omitted — design.md D5 explains why a mirror must not be stricter than its source on
  mirrored columns.
