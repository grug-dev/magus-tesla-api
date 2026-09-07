# RM44 — Incremental Supercharger sync

Source ticket: MAG-48 — https://linear.app/magus-monitor/issue/MAG-48/nightly-reconcile-recalculates-the-whole-vehicle-metrics-history-on

Make the nightly Supercharger path incremental. Today every poller run rewrites the whole
`analytics.vehicle_metrics` history — measured on 2026-09-05 as a **70-day** window and
**51 of 51** rows rewritten, where the snapshot source alone needed **4 days**. The cost
grows with total history, not with new data.

The cause is one rule violated at three layers: **`updated_at` must mean "this row's data
changed"**, not "the last pass touched this row". Layers 1 (`internal/telemetry`) and 2
(`internal/charging`) both set `updated_at = now()` unconditionally, so layer 3
(`internal/analytics`) — whose cursor logic is already correct — gets every session back
on every run and can never narrow its window. **Layer 3 is not changed by this roadmap.**

Along the way the path gets the instrument it never had: nothing in the codebase prints a
query's date filters, which is why this ran unnoticed for months.

## Tier table

Status legend: `[ ]` pending (change not created) · `[~]` in progress (change exists, not archived) · `[x]` done (archived)

| Status | Change | Module | Scope | depends_on | Proposal prompt |
|---|---|---|---|---|---|
| `[x]` | `RM44-telemetry-add-query-logging` | `internal/telemetry` | Log every LIVE query's input arguments via four interface decorators — `store` (3 writes), `Reader` (5), `SuperchargerHistoryReader` (4), `RunWriter` (1). Each logs account, `tesla_id`, date filters (`start`/`end`/`since`/`day`), `limit`, and row count. Extend `callCounter` so all 4 `tesla.VehicleService` Fleet API methods log their parameters. Delete the 2 dead queries (D13). | — | Create the OpenSpec artifacts for `RM44-telemetry-add-query-logging`. Binding decisions D7–D14 in this roadmap. **The open question from MAG-48 is dissolved — do not re-open it:** `internal/telemetry/service.go:42` already declares a `store` interface covering `insertSnapshot`, `insertPollAttempt` and `upsertSuperchargerHistory`, so every live query is already behind an interface and NO call-site logging is needed. Mirror `internal/telemetry/call_counter.go` exactly: explicit per-method implementation, NO interface embedding, plus a compile-time assertion, so a method added later is a compile error rather than a silently unlogged call. Adding a decorator must not change any interface, so the existing test fakes keep compiling. D9 (never log credentials or `raw_data`) is a hard security constraint, not a style note. |
| `[x]` | `RM44-telemetry-add-change-detecting-upsert` | `internal/telemetry` | Layer 1. `UpsertSuperchargerHistory` advances `updated_at` only when the row's mirrored data actually changed. Refresh set today: `raw_data, energy_kwh, total_cost, currency, is_paid, tesla_id`. Structural comparison per D3 — a future column added to the `SET` cannot be silently omitted from the comparison. | tier 1 | Create the OpenSpec artifacts for `RM44-telemetry-add-change-detecting-upsert`. Binding decisions D1–D3 and D10 in this roadmap. The `database` design gate applies — `design.md` MUST carry the comparison mechanism, its rationale, why the hand-listed `WHERE` predicate was rejected, and the index/plan impact. Prove the change with tier 1's log lines: an unchanged re-sync must leave every `updated_at` untouched. |
| `[x]` | `RM44-charging-add-change-detecting-mirror` | `internal/charging` | Layer 2. `MirrorSuperchargerSession` advances `updated_at` only on a real data change. Refresh set today: `energy_kwh, total_cost, currency, is_paid, tesla_id`. Same structural comparison as tier 2. This is the tier that actually stops the `vehicle_metrics` blow-up. | tier 1 | Create the OpenSpec artifacts for `RM44-charging-add-change-detecting-mirror`. Binding decisions D1–D3, D10. The `database` design gate applies. D3's exclusions are load-bearing: `start_battery_pct`, `end_battery_pct`, `battery_pct_source` and `status` are human-owned (RM31/RM41) and MUST stay out of the comparison — a human verification must not look like a mirror change, nor the reverse. `tesla_id` MUST be in the comparison or the orphan-recovery path breaks (D3). |
| `[x]` | `RM44-platform-add-mirror-watermark` | `platform` (`internal/telemetry` + `internal/charging`) | Bound the `telemetry` → `charging` mirror read. **telemetry** adds one account-wide read method `SuperchargerHistoryByAccountUpdatedSince(accountID, since)` (D20). **charging** adds `charging.mirror_watermarks`, one row per account, holding **telemetry's** `updated_at` (D21), plus the ports `MirrorWatermark` / `AdvanceMirrorWatermark` (D22). `processChargingData` stops asking for the full history (`limit 0` → `MaxInt32`) and asks for `updated_at >= cursor - 24h`. Cross-module wiring in `internal/app` is leader-owned. | tier 2, tier 3 | Create the OpenSpec artifacts for `RM44-platform-add-mirror-watermark`. Binding decisions D4–D6, D10, and **D20–D23** (recorded in this roadmap's `progress.json`). The `database` design gate applies — full schema, index plan, and BOTH rejected alternatives with their reasons (they are already written in D4; do not re-derive or re-open them). **D5 is the highest-risk rule in this roadmap**: advancing the watermark to `now()` on an empty read loses data permanently and silently. Copy `analytics.Recalculator.Reconcile`'s existing rule rather than re-inventing it. **The tier spans two modules (D23/D24)**: ONE change, `platform`-prefixed per `openspec/config.yaml`'s cross-cutting escape, implemented by one telemetry worker and one charging worker, with one reviewer per module. Archives under `openspec/changes/archive/platform/`. |

## Decisions (binding on every tier)

Settled with the user on 2026-09-05, before any artifact was written. Workers treat these
as given and never re-open them. Full evidence, measurements and the rejected alternatives
live in ticket MAG-48.

- **D1 — The defect is a three-layer cascade; layer 3 is NOT changed.** Layer 1
  (`telemetry.UpsertSuperchargerHistory`) and layer 2 (`charging.MirrorSuperchargerSession`)
  both set `updated_at = now()` unconditionally. Layer 3 (`analytics.Recalculator.Reconcile`)
  already has the correct filter shape (`updated_at >= cursor - 24h`) and is only ever fed
  lies. No change to `internal/analytics` is in scope.

  Measured on the 2026-09-05 05:15 run: all 6 real sessions in `telemetry.supercharger_history`
  and all 6 in `charging.supercharger_sessions` carried `updated_at = 2026-09-05 05:15:58.28…`
  — identical to the microsecond, i.e. one mirror transaction, not six real changes. The
  control case in the same run: the vehicle WITH sessions rewrote 51/51 metric rows, a second
  vehicle with ZERO sessions rewrote only 2.

- **D2 — `updated_at` must mean "this row's data changed".** On both
  `telemetry.supercharger_history` and `charging.supercharger_sessions`. An unchanged
  mirror pass must leave it untouched. A genuine change — including a human battery-%
  verification via `charging.SessionVerifier` (RM31) on a weeks-old session — must still
  advance it, because that is the mechanism by which such an edit reaches `vehicle_metrics`.

- **D3 — The change comparison is STRUCTURAL, never a hand-listed `WHERE` predicate.**
  A predicate naming each refreshable column was the original design and was rejected
  (see the comment in `internal/charging/db/query.sql`): a future column added to the `SET`
  but forgotten in the `WHERE` would silently stop advancing `updated_at`, with nothing in
  the project able to catch it. Whatever mechanism is chosen (a row-signature comparison,
  `IS DISTINCT FROM (ROW(...))`, or equivalent), **adding a column to the `SET` must not be
  able to silently leave it out of the comparison.**

  Two exclusion rules are load-bearing, not cosmetic:
  - `tesla_id` **MUST** be in the comparison. `processChargingData` justifies its unbounded
    read partly so "a vehicle that is unregistered and later re-registered does not leave a
    hole in the ledger". Under a bounded read that still works — but only because
    re-registration flips `tesla_id` from NULL to a value, which the comparison detects.
  - The human-owned columns `start_battery_pct`, `end_battery_pct`, `battery_pct_source`
    and `status` **MUST NOT** be in it. They are already absent from the mirror's INSERT
    and SET on purpose (RM31, RM41).

- **D4 — The mirror's cursor is a watermark table owned by `internal/charging`**, keyed by
  account, holding **telemetry's** `updated_at`. Two alternatives were considered and
  rejected:

  - *Reuse or move `analytics.vehicle_metric_watermarks`* — **rejected for correctness.**
    The two cursors track different tables: charging's mirror reads
    `telemetry.supercharger_history`; analytics reads `charging.supercharger_sessions`.
    One shared date loses data on a normal night — at `05:15` the mirror writes new charging
    rows and sets the cursor to `05:15`; at `05:16` analytics asks charging "what changed
    since 05:15?" and is told nothing, skipping the rows the mirror just wrote. Sharing the
    *table* with one row per consumer buys nothing and inverts ownership: `telemetry` is
    ingest-only and must not own a table describing how far its consumers have read.
    **The rule: a cursor belongs to the module that READS, never to the module that is read.**
  - *A `source_updated_at` column on `charging.supercharger_sessions`* (cursor = `max()` per
    account) — **rejected on design, not correctness.** It works, including the months-idle
    case. Rejected because it puts mirror bookkeeping inside a domain table, and because the
    watermark-table shape is already proven here with its rules written down.

  Why bound the read at all: the cost is `users × history`, not `history`. The read is per
  account and `MirrorSessions` runs one UPSERT statement per row — making `updated_at`
  conditional stops those statements *writing*, not *running*. At ~36 sessions/user/year,
  1,000 users at year 10 is ~360,000 rows read and ~360,000 no-op UPSERTs every night.

- **D5 — CRITICAL: never advance the watermark to `now()` on an empty read.** It advances
  only to the **highest `updated_at` actually observed** in that run, and is **left
  untouched** when the read returns zero rows. If it jumped to `now()`, a row Postgres
  committed one second late would sit permanently behind the cursor and would never be
  mirrored — silent, undetectable data loss. `analytics.Recalculator.Reconcile` already
  follows exactly this rule ("a source with zero returned rows leaves its own watermark row
  untouched"); copy it rather than re-deriving it. A missing watermark row means the epoch,
  so a first run backfills the whole history once and then goes quiet.

- **D6 — Reuse the 24h overlap guard.** The bounded read uses `updated_at >= cursor - 24h`,
  for the same commit-skew reason `recalcOverlap` exists in `internal/analytics`. The mirror
  upsert is idempotent, so a re-read of an unchanged row is a no-op.

- **D7 — Logging is an explicit, non-embedding decorator.** Mirror
  `internal/telemetry/call_counter.go`: every method implemented explicitly, NO interface
  embedding, plus a compile-time assertion. That file's own doc comment explains why —
  embedding would let a method added later satisfy the type by promotion and go silently
  unlogged. Do NOT scatter `log.Printf` inside method bodies. Do NOT modify the Fleet API
  client in `internal/tesla`; decorate it from `internal/telemetry`, where `callCounter`
  already wraps the same interface.

- **D8 — Keep stdlib `log.Printf`; do NOT introduce `slog`.** The repo has zero `slog`
  today and 40+ `log.Printf` lines with greppable prefixes (`session mirror:`,
  `gap reconciliation:`, `metrics reconciliation:`). New prefixes: `telemetry query:` and
  `fleet api:`. Introducing a second logging library inside a bug fix is out of scope; if
  structured logging is wanted later, that is its own ticket.

- **D9 — Never log a credential or a `raw_data` blob.** Every `tesla.VehicleService` method
  takes `creds tesla.Credentials`, so a naive "log the arguments" implementation leaks a
  live bearer token into the log file. This must be enforced structurally — the decorator
  must offer no path by which `creds` reaches the log — and a test asserts it. `raw_data` is
  a full Fleet API JSON blob per snapshot and per session: log its size if anything, never
  its content.

- **D10 — Unit tests ARE included.** The user was asked and chose to include them, changing
  the standing default of "no unit tests". Every tier's `tasks.md` carries test tasks. The
  two rules that most need pinning are D3's structural comparison and D5's empty-read
  watermark behaviour. `Test-Execution-Policy` still applies in full: the assistant writes
  the tests and never runs the suite; the owner runs it and reports, and that report is
  recorded as theirs.

- **D12 — Every LIVE telemetry query is already behind an interface.** MAG-48 stated that five
  queries sit behind no interface and asked whether to extract a seam or log at the call site.
  That premise was wrong: `internal/telemetry/service.go:42` already declares a private `store`
  interface covering `insertSnapshot`, `insertPollAttempt` and `upsertSuperchargerHistory`. So the
  instrumentation is **four decorators over existing interfaces** — `store`, `Reader`,
  `SuperchargerHistoryReader`, `RunWriter` — and no call-site logging anywhere. A decorator adds no
  method, so every existing test fake keeps compiling unchanged.

- **D13 — `ListSnapshotsByVehicle` and `ListPollAttemptsByVehicle` are DELETED.** Neither has a
  production caller; their only users are **10** call sites across 3 DB-integration test files
  (`db_integration_test.go` ×5, `db_sourcea_integration_test.go` ×3, `db_tpms_integration_test.go` ×2),
  each reading a row back to verify a write. *(The leader first wrote 8; the tier-1 worker
  recounted and found 10. The real count is 10 — it changes the size of the task, not its shape.)* The user chose deletion over leaving them uninstrumented.

  **Constraint on the rewrite:** those 8 assertions are replaced with **raw SQL in the test**, never
  with another sqlc reader query. Verifying a write by calling one of the module's own live readers
  makes the test pass through the very path it is testing; raw SQL keeps the assertion independent
  of the code under test. This is why the deletion is a real task, not a one-line removal.

- **D14 — Fleet API logging EXTENDS `callCounter`; no sibling decorator.** One type, one
  compile-time assertion, one place a new `tesla.VehicleService` method must be added, and no
  chaining order to get wrong. The cost — counting and logging share a type — was accepted
  deliberately over doubling the number of places a future method must be registered.

- **D11 — Tier order: logging FIRST.** Chosen by the user over "fix first, log last". The
  instrument installed in tier 1 is what makes tiers 2–4 provable: run the poller before
  tier 2 and the log shows a 70-day window and `limit=2147483647` with no date bound; run it
  after tier 4 and it shows ~4 days and a watermark-bounded `since`. The cost is that the
  bug stays live one tier longer, which the user accepted.

  The `updated_at` work is split across tiers 2 and 3 because it touches two modules and a
  pipeline worker is sandboxed to exactly one. Same scope, same order, two dispatches.

## Future work

Not in this roadmap. Recorded in `openspec/roadmaps/backlog.md`:

- `internal/analytics` has zero log statements, so the recalculation window it *derives*
  (`Recalculate(start, end)`) is still invisible on a successful run — it appears only
  inside an error string. This roadmap instruments the telemetry side, which is enough to
  see the *inputs*; logging the derived window would show the *output* directly.
