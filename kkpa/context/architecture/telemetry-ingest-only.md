# Telemetry is ingest-only — consumer map — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.

> **RESOLVED — RM39 finished both renames.** `charging.charge_sessions` →
> `charging.supercharger_sessions` (D5b) landed in RM39 tier 3 (`charging-move-to-own-schema`,
> MAG-31). `telemetry.supercharger_sessions` → `telemetry.supercharger_history` (D5a) landed in
> RM39 tier 4 (`telemetry-move-to-own-schema`), which also moved this module's four tables out
> of `public` into schema `telemetry`. Roadmap D25 retired the "separate blocked boundary
> ticket" (D6) this banner used to point at — no such ticket ever existed.
>
> **The two tables no longer share a base name**, so a bare `supercharger_sessions` below is
>
> always **charging's** mirror; telemetry's is `telemetry.supercharger_history`. The port's
> half-renamed state is **closed**: RM39 tier 5 renamed it to
> `telemetry.SuperchargerHistoryReader`, its four methods to `SuperchargerHistoryBy*` and its
> constructor to `NewSuperchargerHistoryReader`, so the port, the table and the domain type now
> share one vocabulary. See `openspec/roadmaps/RM39-schema-per-module.md`.

## What this module is (read this before the map)

`internal/telemetry` **fetches Tesla Fleet API data and writes what it fetched. Nothing more.**
It is a *source*, not a hub — it holds no read model, serves no page, and answers no
user-facing request. Anything the app shows a user comes from a module that mirrors or derives
telemetry's rows, never from telemetry itself.

| Module | Role | Who may read it |
|---|---|---|
| `telemetry` | **Ingest only.** Nightly Fleet API collection → schema `telemetry`: `vehicle_snapshots`, `supercharger_history`, `poll_attempts`, `poll_runs`. | `internal/analytics` (snapshots only) and `internal/app` (the Supercharger mirror step). **Never `internal/gateway`.** |
| `charging` | Mirrors telemetry's Supercharger rows into `supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3), **and originates** `manual_charge_entries` (a human types those). Part mirror, part owner — not a pure mirror. | gateway, analytics |
| `analytics` | Derived read model. Recomputes `vehicle_metrics` from three independent watermark sources (`vehicle_snapshots`, `supercharger_history`, `manual_charge_entries` — `supercharger_sessions` reused from before RM31 by `RM39-analytics-fix-watermark-vocabulary`, roadmap tier 3b; see that change's `design.md` §6). | gateway |

The gateway therefore reads **`charging` and `analytics` only**. That is enforced, not merely
documented — see the boundary gotcha below.

## Glossary

- **Known as:** `telemetry module`, `vehicle snapshots`, `supercharger history`, `nightly collection`, `telemetry schema`, `who reads telemetry`, `telemetry vs analytics`, `can the gateway read telemetry`, `ingest module`, `poll run`, `run summary`, `telemetry query logging`, `fleet api logging`
- **Internal name:** `internal/telemetry` — ports `telemetry.Reader`, `telemetry.SuperchargerHistoryReader` (reads), `telemetry.Collector` (write), `telemetry.RunWriter` (run summary write) — tables (all in schema `telemetry`) `vehicle_snapshots`, `supercharger_history`, `poll_attempts`, `poll_runs`

## Component map

Files involved, grouped by layer. Each row: the file's role in this concept.

### The owning module — internal/telemetry

| File | Role |
|---|---|
| `internal/telemetry/telemetry.go` | Package doc (the purpose statement) + public ports: `Reader` (snapshot reads), `SuperchargerHistoryReader` (Supercharger history reads), `Collector` (nightly write). Domain types (`Snapshot`, `SuperchargerHistory`, `CycleReport`, `RunContext`) — no vendor suffix. |
| `internal/telemetry/service.go` | `NewService(pool, acct, tsla, cfg) Collector` — the ONLY writer (nightly collection, wake logic, upserts incl. `UpsertSuperchargerSession`). |
| `internal/telemetry/reader.go` | `NewReader(pool)` / `NewSuperchargerHistoryReader(pool)` read impls; pgtype→domain mapping stays here. |
| `internal/telemetry/db/queries.sql` → `query.sql.go` | sqlc source of truth; every read method the ports expose has its query here. |
| `internal/telemetry/wake.go`, `report.go`, `scheduler.go`(relocated) | Collection support: vehicle wake, cycle reporting. The scheduler now lives in `internal/app/scheduler.go`. |

### Consumers — who reads telemetry data

| File | Port calls | Use case |
|---|---|---|
| ~~`internal/gateway/**`~~ — **NO LONGER A CONSUMER, and now forbidden** | — | The gateway's four snapshot call sites were repointed onto `analytics.Reader` by **RM38** (`LatestMetricsByAccount` — dashboard, vehicle cards, nav header, charges battery suggestion) and **RM40** (`BatteryLevelByDay` — the history battery chart). The supercharger page had already moved to `charging.SessionReader` in RM30. `make boundary-guard` now fails the build on any `internal/telemetry` import under `internal/gateway/`, with **zero** `// boundary:allow:` escape hatches. See the boundary gotcha below. |
| `internal/analytics/analytics.go` + `reader.go` | `SnapshotsByVehicleSince` | Derived metrics: `ConsumedByDay`, `OdometerDeltaByDay` over `vehicle_metrics`. Telemetry supplies **snapshots only** — `NewReader`'s `supercharger` argument is `charging.SuperchargerSessionAnalyticsReader`, not a telemetry port (**changed by RM31**). Verified: no file under `internal/analytics/` names `telemetry.SuperchargerHistoryReader`. |
| `internal/analytics/recalculate.go` | `SnapshotPrecedingDay`, `SnapshotsByVehicleUpdatedSince`, `SnapshotsByVehicleBetween` | `Recalculator` re-derives `vehicle_metrics` rows (nightly + after manual-charge writes — see `entities/vehicle-metrics/guide.md`). **Snapshot reads only since RM31** — its Supercharger source moved to `charging.SuperchargerSessionAnalyticsReader` over `charging.supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3). |
| `internal/app/processor.go` | `SuperchargerHistoryByAccount` (limit 0 = every row) | Step 2 of `ProcessVehicleData`: mirrors Supercharger sessions into `charging.supercharger_sessions` (renamed from `charging.charge_sessions`, RM39 tier 3) via `charging.SessionWriter` — what the Supercharger Stats page reads (RM30) **and, since RM31, what `internal/analytics` derives from**. This is now the **only remaining caller of `telemetry.SuperchargerHistoryReader` repo-wide**. See `architecture/nightly-cycle.md`. |

### Driving adapters (the write side's callers)

| File | Role |
|---|---|
| `internal/app/scheduler.go` | Daily timer; calls `Processor.ProcessVehicleData` (the scheduler is a peer adapter, NOT inside the Processor). |
| `cmd/poller/main.go` | Constructs `telemetry.NewService(...)`, `app.NewScheduler(...)`; thin composition only. |
| `cmd/web/main.go` | Builds **one** `telemetry.NewReader(pool)` and passes it to `analytics.NewReader` / `analytics.NewRecalculator` — **never into `gateway.Deps`**. Its own comment at the call site says "gateway never calls that". |

## How maintenance works

- **Add a new telemetry read consumer:** construct `telemetry.NewReader(pool)` / `NewSuperchargerHistoryReader(pool)` in the consumer's composition root (`cmd/web/main.go` or the module's constructor), accept the PORT interface in `Deps`/constructor — never import `internal/telemetry/db`. Gateway handlers go through `resolveSelectedVehicle` for per-vehicle reads.
- **Add a new read method:** `internal/telemetry/db/queries.sql` → `make sqlc` → implement on `Reader`/`SuperchargerHistoryReader` in `reader.go` + declare in `telemetry.go`. Bounded windows follow the platform `?start=&end=` convention (see `SuperchargerHistoryByVehicleBetween`).
- **Add a new collected field:** capture path only — `telemetry.Collector`/`service.go` + `db/queries.sql` (+ migration). Units convert exactly once at capture time (display units, RM7 D1/D3); never add read-time conversion.
- **Change the nightly cycle:** `internal/app/processor.go` (`ProcessVehicleData` 3-step flow) — never re-add orchestration to `cmd/poller`. Full step/port/table map: `architecture/nightly-cycle.md`.

- **Record a new run-level fact:** add the field to `telemetry.CycleReport` (populated inside `CollectAll`), add the column to `poll_runs` via a migration, extend `telemetry.PollRun` + the `InsertPollRun` query, and map it in `RunWriter.RecordRun`. The caller in `internal/app` passes the whole `CycleReport` — it gains no pool and no table.
- **Read `poll_runs`:** there is **no read port yet**. Direct SQL is the only way to see a row today; adding a `Reader`-style method is deferred backlog work, not an existing surface.

## Conventions & gotchas

- **One writer, many readers.** Only `telemetry.Collector` (via `NewService`) writes; every other module reads through `Reader`/`SuperchargerHistoryReader`. No user-facing request ever writes telemetry. _Source: `internal/telemetry/telemetry.go` package doc; `internal/telemetry/AGENTS.md`._
- **Reads are hot-path — keep them indexed and bounded.** The workload profile is read-heavy (dashboards) vs one nightly write batch; every new read method must be bounded (limit or `[start,end]` window). _Source: `ai/architecture.md` §7, `ai/go-conventions.md` §"Read optimization"._
- **NEVER import `internal/telemetry/db` outside the module** — consumers take the port interface; pgtype never escapes. _Source: `internal/gateway/AGENTS.md`, `internal/app/AGENTS.md` → allowed imports._
- **`EffectiveDate` vs `CapturedDate` vs `CapturedAt`** — three distinct time fields on `Snapshot` (the day the data describes / dedupe-UNIQUE day / precise read instant); mixing them up is the classic bug. `EffectiveDate` is read-derived, never persisted. _Source: `internal/telemetry/telemetry.go` `Snapshot` doc._
- **Same-day captures dedupe** — `UNIQUE (account_id, tesla_id, captured_date)`, latest wins; repeated same-day collection is not duplicate data. _Source: `telemetry-dedupe-daily-snapshots` design D1/D2._
- **The scheduler lives in `internal/app`, not telemetry** (RM29 RD8) — `Processor` never consults a clock; `Scheduler` is a peer adapter holding a `Processor`. _Source: `internal/app/AGENTS.md`._
- **`internal/app` owns NO data** — `poll_attempts` (incl. `run_id`/`triggered_by`) stays telemetry's. _Source: `internal/app/AGENTS.md` → Data Ownership._
- **The gateway may not depend on `telemetry` AT ALL — not even `telemetry.Reader`.** Stronger than the usual "ports only" rule: the whole module is outside the gateway's vocabulary, and `telemetry.*` types must not appear in gateway code. `make boundary-guard` enforces it repo-wide — it **fails** on a non-test file, warns on a `_test.go` — and the gateway carries **zero** `// boundary:allow:` escape hatches. A new `internal/telemetry` import in the gateway is a **regression, not known debt**. Route the read through `analytics` or `charging` instead. _Source: `ai/architecture.md` §"Exception: the gateway may not depend on `telemetry` at all"; `internal/gateway/AGENTS.md`._
- **Telemetry is a source, not a hub — it serves no user-facing read.** If a page needs telemetry data, the correct move is to add it to `analytics`' derived model or `charging`'s mirror, never to open a telemetry port to the gateway. RM38 and RM40 exist precisely because that shortcut was taken once and had to be undone. _Source: `ai/architecture.md`; roadmaps RM38/RM40._
- **`charging` is NOT a pure mirror.** It mirrors telemetry's Supercharger rows into `supercharger_sessions` (renamed from `charge_sessions`, RM39 tier 3), but `manual_charge_entries` originates in the module — a human types those, and telemetry never sees them. Treating `charging` as read-only-derived will lose the manual half. _Source: `workflows/manual-charge-crud.md`; `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql` header._

- **A poll run is recorded exactly once, and a duplicate is an error — never an upsert.** A second summary for a run identity that already has one is rejected and leaves the first record unchanged; recording a run twice is never a legitimate outcome. _Source: spec telemetry — Requirement: Run-Level Poll Summary Storage._
- **A run that fails before touching a single vehicle still records a summary.** Its account and vehicle counts are all zero, but its start/finish times and duration are real. This is the whole point of the table: before it, a failed run left no trace at all, because zero `poll_attempts` rows were written. _Source: spec telemetry — Requirement: Run-Level Poll Summary Storage._
- **Every Tesla Fleet API request counts, including the ones that fail.** A rejected request still consumes a request against the vendor's quota, so the counter increments before the error is checked — never after. _Source: spec telemetry — Requirement: Tesla API Call Counting._
- **"Whole-account failure" has exactly two causes.** An account counts as failed only when it cannot obtain a usable Tesla access token, or when its account-wide vehicle-list request is rejected as unauthorized. No other failure mode marks an account failed — per-vehicle failures never do. _Source: spec telemetry — Requirement: Account-Level Attempt And Outcome Counts._
- **Succeeded accounts is derived, not counted:** attempted minus failed. Do not increment it independently or the two will drift. _Source: spec telemetry — Requirement: Account-Level Attempt And Outcome Counts._
- **The cycle log line mixes two grains, so every label says which.** Vehicle-grain counts are labelled as vehicles (`vehicles_attempted`/`vehicles_succeeded`); account-grain counts and the Tesla API call count are reported alongside them. The unlabelled `attempted`/`succeeded` pair was the exact ambiguity MAG-35 was filed about. _Source: spec telemetry — Requirement: Nightly Cycle Log Summary._

- **All four telemetry tables live in the `telemetry` schema, never `public`.** Every query naming one must be schema-qualified; the module boundary is now checkable in the database catalog, not only by Go import guards. `goose_db_version` deliberately stays in `public` as the one shared ledger across modules.
  _Source: spec telemetry — Requirement: Module-Scoped Database Schema._
- **`telemetry.supercharger_history` and `charging.supercharger_sessions` are different tables.** The rename exists to retire that ambiguity: telemetry's is the raw vendor upsert, charging's is the dense human-correctable mirror. Never resolve either through a `search_path` — a bare `supercharger_sessions` reached through a path including `charging` finds the WRONG table and succeeds while reading the wrong rows.
  _Source: spec telemetry — Requirement: Module-Scoped Database Schema, Scenario: The charging module's identically-named table is untouched._
- **`supercharger_history` is UPSERTED, not append-only — do not reason about it like `vehicle_snapshots`.** Tesla settles fees after a session ends, so a row is never final on first insert. That difference is why the table is named `_history` and not `_snapshots`; treating both as append-only invites a query that double-counts.
  _Source: spec telemetry — Requirement: Supercharger History Table Renamed._
- **The port's half-renamed state is CLOSED — the vocabulary is now uniform.** The tier-4 spec deliberately left `SuperchargerReader`, its four `SuperchargerSessions*` methods and its constructor on the old names while they returned `[]SuperchargerHistory`, and told reviewers not to flag it. RM39 tier 5 finished the rename: the port is `SuperchargerHistoryReader`, its methods are `SuperchargerHistoryBy*`, its constructor is `NewSuperchargerHistoryReader`. Read the tier-4 spec's "SHALL NOT rename" clause as history, not as a live instruction.
  _Source: spec telemetry — Requirement: Supercharger History Table Renamed._
- **When renaming a table here, the completeness criterion is the CATALOG, never a hand-written list.** PostgreSQL auto-names one CHECK per inline column constraint and renames nothing automatically when a table is renamed; those names exist only in `pg_constraint`, where no grep reaches. The binding check is that a catalog query for any constraint or index named `<old_table>%` in this schema returns zero rows.
  _Source: spec telemetry — Requirement: Supercharger History Table Renamed, Scenario: No catalog object survives under the old table name._
- **A schema move preserves Go names; a table rename does not.** Moving a table into a schema changes sqlc's default struct name, so `sqlc.yaml` carries preserving `rename:` keys to hold `PollAttempt`, `PollRun` and `VehicleSnapshot` steady. The `SuperchargerHistory` rename is the deliberate exception. A `rename:` key that does not match is ignored **silently, at exit 0** — the `models.go` diff is the only detector.
  _Source: spec telemetry — Requirement: Supercharger History Table Renamed._
- **`SuperchargerReader` is TWO unrelated identifiers — never grep-and-rename it repo-wide.** Telemetry's port is `SuperchargerHistoryReader` since RM39 tier 5. `internal/gateway`'s `Deps.SuperchargerReader` is a *different* field, typed `charging.SessionReader` and wired from `cmd/web/main.go` — the gateway cannot import telemetry at all (`make boundary-guard`). `internal/analytics` adds a third: local test fakes satisfying `charging.SuperchargerSessionAnalyticsReader`. A blind sweep of the old name corrupts working gateway code; the planning estimate of "88 references" was this collision, and the real cross-module surface was 4 files.
  _Source: spec telemetry — Requirement: Supercharger History Table Renamed, Scenario: An unrelated, same-named identifier in another module is left untouched._
- **Renaming this port is compiler-checked, so the compiler is the acceptance test — not a grep.** Every method signature, predicate, ordering and returned element type is byte-identical across the rename, so any consumer left on an old name fails to build rather than binding to something subtly different. `go build ./...` and `go vet ./...` (which compiles `_test.go` too) are the authority; a repo-wide grep must be triaged file-by-file, because the collisions above guarantee it can never return zero.
  _Source: spec telemetry — Requirement: Supercharger History Table Renamed, Scenario: A real consumer outside the module is broken only by the identifier, and visibly._
- **Raw SQL inside `_test.go` files is invisible to sqlc and to `go vet`.** No assistant-runnable signal catches an unqualified table name there — only the suite does. After any schema or table change in this module, re-run a grep over the test files as the acceptance step; a green build proves nothing about them.
  _Source: spec telemetry — Requirement: Module-Scoped Database Schema (namespacing-only guarantee across all module reads)._

- **`telemetry.supercharger_history` has NO battery-percentage estimate columns — do not
  add them back.** The table once reserved a frozen verification-time snapshot pair
  (`start_battery_pct_est` / `end_battery_pct_est`, each `SMALLINT` 0–100) for a companion
  estimation capability. That capability was descoped before it ever shipped, so the pair
  was NULL in every row for its whole life. RM41 tier 3 dropped both columns, reversing
  RM27's design decision D6, which had kept them so a future estimator could land without a
  migration. The estimator that eventually shipped (MAG-36, `derivedStartBatteryPct`) writes
  the real `start_battery_pct` column instead, so there is nothing left for a snapshot to
  capture. `charging.supercharger_sessions` lost its own equivalent pair in the same
  roadmap (tier 2). _Source: spec telemetry — Requirement: Supercharger Session Ledger._
- **A session read through the telemetry port carries the verification TRIO only — start
  percentage, end percentage, source label.** There is no fourth and fifth estimate field.
  Every returned session exposes the trio exactly as stored, NULL when no override was set.
  Code or tests asserting a snapshot pair on a returned session are pre-RM41 and no longer
  compile. _Source: spec telemetry — Requirement: Supercharger Session Read Port._
- **The ledger NEVER persists a computed estimate under a source label.** The source label
  is a closed, explicitly extensible set identifying a human-owned or measured origin only;
  an estimate is a different capability's read-time concern. This rule survived the column
  drop unchanged and is the reason no replacement estimate column was introduced.
  _Source: spec telemetry — Requirement: Supercharger Session Ledger._

- **Every live query and every Fleet API request logs its arguments** — each call through the
  module's persistence write seam, its snapshot-read port, its Supercharger-history read port
  and its poll-run write port emits one log line carrying the account and vehicle identity, the
  date filters supplied, and — for a method returning a collection or an optional record — how
  many records came back. Prefixes are `telemetry query:` and `fleet api:`, greppable.
  _Source: spec telemetry — Requirement: Query And Fleet-API Argument Logging._
- **A Supercharger-history read logs the limit the query ACTUALLY runs with, not the caller's
  placeholder** — a caller passing "no limit" must appear in the log as the concrete resolved
  value. Logging the placeholder would make a full-history read indistinguishable from a
  bounded one, which is the exact blindness that let MAG-48 run unnoticed for months.
  _Source: spec telemetry — Requirement: Query And Fleet-API Argument Logging._
- **An account-wide Supercharger-history read states explicitly that it applied no date bound** —
  absence of a filter must be visible in the log, not inferred from a missing field.
  _Source: spec telemetry — Requirement: Query And Fleet-API Argument Logging._
- **No log line may ever contain a vendor access credential** — every Fleet API method takes
  credentials as an argument, so a naive "log the arguments" leaks a live bearer token. The
  guarantee is structural, and a test asserts it.
  _Source: spec telemetry — Requirement: Structural Exclusion Of Credentials And Raw Payloads From Logs._
- **A raw external-API payload is logged as a byte size, never as content** — record the size
  where it is useful; never the blob. A test asserts the content never appears.
  _Source: spec telemetry — Requirement: Structural Exclusion Of Credentials And Raw Payloads From Logs._
- **The instrumentation is an explicit, non-embedding decorator per port** — a method added to a
  decorated interface later must become a compile error rather than a silently unlogged call.
  Embedding would satisfy the type by promotion and defeat that.
  _Source: spec telemetry — Requirement: Query And Fleet-API Argument Logging (implementation shape carried by roadmap decision D7/D8)._

- **`updated_at` on `telemetry.supercharger_history` means "this row's data changed", not
  "the nightly sync ran".** An unchanged re-sync leaves it untouched. A re-sync that writes at
  least one different mirrored value advances it. Before MAG-48 every pass advanced it on
  every row, which made the signal useless to the consumer that reads it.
  _Source: spec telemetry — Requirement: Change-Detecting Supercharger-History Upsert._

- **The comparison covers exactly the columns the sync refreshes on a conflict — no more, no
  less.** Everything else is excluded on purpose: columns a human sets by hand, and columns
  written once at first capture and never refreshed. The spec requires exclusion to be an
  explicit, visible decision, never the accidental result of a name missing from a
  hand-written list.
  _Source: spec telemetry — Requirement: Change-Detecting Supercharger-History Upsert._

- **A column the sync never refreshes must never enter the comparison.** If it did, and the
  vendor later reported a different value, the mismatch could never resolve — the sync does not
  write that column — so `updated_at` would advance every night forever. This is the trap the
  requirement's last scenario pins, and it is why the exclusion list is the complement of the
  refresh set rather than a short list of "obvious" columns.
  _Source: spec telemetry — Requirement: Change-Detecting Supercharger-History Upsert._

- **A vehicle re-registration DOES advance `updated_at`.** A row with no vehicle identity,
  later resolved to a registered vehicle, counts as a real change. Excluding the vehicle
  identity from the comparison would silently break that recovery path.
  _Source: spec telemetry — Requirement: Change-Detecting Supercharger-History Upsert._

- **A human-entered value on the row neither blocks change detection nor counts as a change.**
  A verified row still reports "unchanged" on an unchanged re-sync, and the hand-entered value
  is left exactly as it was.
  _Source: spec telemetry — Requirement: Change-Detecting Supercharger-History Upsert._

- **Guard when you change this:** `internal/telemetry/db_change_detection_schema_test.go`
  reads the table's live columns and fails if any column is in neither the refresh set nor the
  exclusion list. Its failure message names the column and says which list to add it to. Add a
  column to the table and this test tells you what to decide.
  _Source: spec telemetry — Requirement: Change-Detecting Supercharger-History Upsert._

- **There are TWO updated-since read ports for Supercharger sessions, and the difference is
  load-bearing.** The per-vehicle port filters on the vehicle identifier. The account-wide port
  takes no vehicle at all. Only the account-wide one can return a session whose vehicle is not
  currently registered, because a per-vehicle filter can never match a row with no vehicle
  identity. A caller that must recover such a session once its vehicle re-registers has to use
  the account-wide port. Picking the per-vehicle one there loses rows, silently.
  _Source: spec telemetry — Requirement: Supercharger History Account-Wide Updated-Since Read Port._

- **The account-wide port returns oldest-first by `updated_at`, and an empty result is not an
  error.** Nothing updated in the window returns an empty collection with no error, exactly like
  every other read port on this module.
  _Source: spec telemetry — Requirement: Supercharger History Account-Wide Updated-Since Read Port._

- **A session updated at exactly the requested instant is included.** The bound is inclusive at
  both ports. A caller that treats it as exclusive will skip a row on every boundary.
  _Source: spec telemetry — Requirement: Supercharger History Account-Wide Updated-Since Read Port._

- **No caller reaches these rows any other way.** Both updated-since ports are the only route to
  this data for another module; nothing outside `internal/telemetry` imports
  `internal/telemetry/db`. `make boundary-guard` enforces it.
  _Source: spec telemetry — Requirement: Supercharger History Account-Wide Updated-Since Read Port._

## Related KB

- Features: (none yet)
- Workflows: `workflows/manual-charge-crud.md` (analytics recalc after writes), `workflows/supercharger-stats-read.md` (read-only page over the `supercharger_sessions` mirror)
- Architecture: `architecture/nightly-cycle.md` (the 3-step `ProcessVehicleData` cycle that drives telemetry's write path)
