## Why

`cmd/poller` writes a new `vehicle_snapshots` row on **every** successful capture — the
scheduler's nightly tick, and every manual `go run ./cmd/poller --once`. Running `--once`
twice in one day (e.g. re-running after fixing a bad night, or testing locally) stores
**two** rows for the same vehicle on the same day. `vehicle_snapshots` has no unique
constraint; the init migration says verbatim: "one immutable row per SUCCESSFUL capture
(design D1) … Never overwritten or deleted (append-only): no updated_at, no UNIQUE that
would block a second capture of the same vehicle."

Verified live-DB impact as of this proposal: 44 snapshot rows, exactly 2 duplicate pairs,
both dated 2026-08-04 (one per vehicle) — both from a same-day `--once` re-run.

The user wants exactly **one row per (vehicle, captured date)**. Decided with the user in
the pipeline's grill step (binding, see design.md for the full rationale of each):

- **D1 — Conflict rule: REPLACE, latest wins.** `InsertVehicleSnapshot` becomes an upsert
  keyed on `(account_id, tesla_id, captured_date)`; a same-day re-run refreshes every
  typed column, `raw_data`, and `captured_at` to the newer capture's values. A re-run is
  normally the user correcting a bad or partial nightly capture, so the freshest data
  should win.
- **D2 — `captured_date` is a new `DATE` column, computed in Go.** A Postgres UNIQUE index
  cannot depend on the runtime `POLLER_TIMEZONE` env var, so the date must be derived in
  Go (in `snapshotFrom`, from `captured_at` in the poller's configured `*time.Location`)
  and written as a plain column — never a DB expression index tied to a fixed zone.
- **D3 — Backfill dedupe: keep the row with the latest `captured_at` per duplicate group.**
  The migration adds the column, backfills it, deletes the older row of each duplicate
  group, then adds the UNIQUE constraint and NOT NULL. The 2 known stale rows are deleted
  — expected and authorized.
- **D4 — Scope is `vehicle_snapshots` only.** `poll_attempts` stays append-only, one row
  per (vehicle, run) — it exists to record every attempt including failures/retries, the
  availability/sleep-behavior signal a daily collapse would destroy.
- **D5 — This reverses the append-only invariant, and the reversal is documented.** This
  change explicitly **supersedes design D1 of `RM1-telemetry-add-nightly-snapshots`**. An
  `updated_at TIMESTAMPTZ` column is added so an overwritten row is auditable — the
  original rationale for omitting it ("never overwritten") no longer holds.

## What Changes

Primary module: **`internal/telemetry/`** (schema, write path, domain type, tests, module
docs). Secondary, thin-wiring-only: **`cmd/poller/main.go`** (must load `POLLER_TIMEZONE`
unconditionally — today it is loaded only for the nightly scheduled path — and thread the
resulting `*time.Location` into `telemetry.Config.Location`, a new field). No new module,
no new runnable.

### (a) Schema: `captured_date DATE` + UNIQUE constraint + `updated_at`

One goose migration on `vehicle_snapshots` (full DDL in design.md):
- `captured_date DATE NOT NULL` — the calendar day, Go-computed, backing the dedupe key.
- `UNIQUE (account_id, tesla_id, captured_date)` — the conflict target for the upsert.
- `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()` — refreshed to `now()` on every
  same-day replace; audit trail for the reversed append-only invariant.
- Backfill: derive `captured_date` for the 44 existing rows, delete the 2 duplicates
  (older row of each pair), then enforce NOT NULL + UNIQUE.

### (b) Write path: `InsertVehicleSnapshot` becomes an upsert

`ON CONFLICT (account_id, tesla_id, captured_date) DO UPDATE SET` every typed column,
`raw_data`, `captured_at`, and `updated_at = now()`.

### (c) Go seam: `Config.Location` carries the poller's timezone into the mapping code

`telemetry.Config` gains `Location *time.Location` (nil → `time.Local`, mirroring
`NewScheduler`'s own "nil loc falls back to time.Local" convention). `snapshotFrom` gains
a `loc` parameter and computes `Snapshot.CapturedDate` from `capturedAt.In(loc)`.
`cmd/poller/main.go` loads `POLLER_TIMEZONE` once, before building `telemetry.Config`, and
uses that single `*time.Location` for both the `Scheduler`'s run time AND
`Config.Location` — so the day a snapshot is dated always agrees with the day the
scheduler considers "today."

### (d) Domain type: `Snapshot.CapturedDate`

A new `time.Time` field (calendar-date semantics, UTC-midnight normalized — the
`pgtype.Date` convention), set on write by `snapshotFrom` and round-tripped on read by
`rowToSnapshot`, so tests can assert on it directly through the store.

### (e) Existing test blast radius

Every DB integration test file that inserts more than one snapshot for the same
`(account_id, tesla_id)` at different `captured_at` values must set `CapturedDate`
explicitly and consistently with each capture's intended day — otherwise every such
literal collapses onto the same zero-value date and silently replaces itself under the
new constraint. This touches `db_integration_test.go` (including the append-only test,
which is renamed/rewritten), `db_read_integration_test.go`, `db_sourcea_integration_test.go`,
and `db_tpms_integration_test.go`. Full detail and task breakdown in design.md/tasks.md.

## Breaking

Yes, in two bounded, intentional ways — not an oversight:

1. **The `vehicle_snapshots` append-only/immutable invariant is reversed.** This
   supersedes `RM1-telemetry-add-nightly-snapshots` design D1. The effect is contained to
   same-calendar-day re-runs (a row is replaced only when a NEW capture lands on a day
   that already has one); `poll_attempts` is completely unaffected (D4) and remains
   strictly append-only. The reversal is documented in the new migration's header comment,
   `internal/telemetry/AGENTS.md`, and this proposal.
2. **`cmd/poller --once` now validates `POLLER_TIMEZONE` on every run.** Previously only
   the nightly scheduled path loaded/validated it (`--once` never touched it). Since
   `Config.Location` now drives `captured_date` on both paths, `--once` will fatal on an
   invalid `POLLER_TIMEZONE` where it previously ignored the setting entirely. This is the
   direct, intended consequence of D2 (both paths must agree on "what day is it"), not a
   defect.

Otherwise non-breaking at the interface level: the `Collector` and `Reader` port
signatures are unchanged; `Snapshot` gains one named field (`CapturedDate`), which is
additive and compile-compatible with existing named-field struct literals (same precedent
as the TPMS change). The 2 known duplicate rows are deleted by the migration and are
**not** recoverable via the Down migration — documented and authorized (D3).

## Modules affected

- **`internal/telemetry/`** — primary: migration, `InsertVehicleSnapshot` upsert,
  `Config.Location`, `snapshotFrom`/`dbStore.insertSnapshot`/`rowToSnapshot` wiring,
  `Snapshot.CapturedDate`, `AGENTS.md` "Data ownership" rewrite (D5), new + updated tests.
- **`cmd/poller/`** — secondary, thin wiring only (`ai/go-conventions.md`: zero business
  logic in `cmd/`): move the existing `time.LoadLocation(cfg.PollerTimezone)` call so it
  runs unconditionally (today it runs only inside the nightly-path branch) and set
  `telemetry.Config.Location` from it. **This file is outside `internal/telemetry`'s
  sandbox** — the leader must either grant it explicitly to the telemetry worker or
  dispatch it separately; see design.md's Scope Boundary section.
- No other `internal/` module. No change to `internal/account`, `internal/tesla`, or the
  gateway.

## Database Changes

One migration on `vehicle_snapshots` (a table owned solely by `internal/telemetry/db`):
one new nullable-then-NOT-NULL `DATE` column, one new `UNIQUE` constraint (which is also
the ON CONFLICT target), one new `TIMESTAMPTZ NOT NULL DEFAULT now()` column, and a
one-time backfill + dedupe `DELETE` of exactly 2 known rows. design.md is REQUIRED (this
change touches the DB) and includes: the full `Up`/`Down` DDL in migration order, the
backfill's timezone literal and why it is the correct one-time choice for already-stored
rows, the rejected alternatives for D2 (expression index on a fixed/hardcoded timezone),
and an index plan justified against the two actual `query.sql` read queries. The database
design gate triggers and passes.

## Read Paths Affected

**No read-path shape or plan changes.** `LatestSnapshotsByAccount` (`DISTINCT ON`) and
`SnapshotsByVehicleSince` (forward range scan) both continue to be served entirely by the
existing `idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at)` index —
neither query orders or filters by `captured_date`, so the new UNIQUE index does not
replace it (full justification in design.md). Both queries pick up 2 more columns
(`captured_date`, `updated_at`) riding along on the same row fetch, for explicit-column-list
consistency with the shared `telemetrydb.VehicleSnapshot` struct — no new predicate, no
plan change. The **write** path is what actually changes shape: `InsertVehicleSnapshot`
goes from a blind `INSERT` to an `INSERT ... ON CONFLICT ... DO UPDATE`, and that upsert's
conflict target is exactly the new UNIQUE index (a structural requirement for the ON
CONFLICT clause to exist at all, not merely a read optimization).

## Capabilities

### Added / Modified Capabilities

- **`telemetry`** — modifies "Nightly Vehicle Snapshot Capture": at most one stored
  snapshot per (account, vehicle, calendar day); a same-day re-capture replaces the
  existing row (latest wins); a new-day capture always inserts; the calendar day is
  computed in the poller's configured timezone; `poll_attempts` is explicitly unaffected.
  This SUPERSEDES the requirement's prior "immutable"/"append-only" framing.

### Consumed Capabilities (no change to their specs)

- None — this changes telemetry's own write semantics only.

## Resolved decisions

All five binding decisions (D1–D5) were settled with the user in the pipeline's grill
step before this proposal was authored (see the dispatch's "BINDING DECISIONS" and the
"Why" section above for the full text). design.md records them as D1–D5 verbatim, plus the
implementation-seam sub-decisions needed to realize them (the `Config.Location` seam, the
backfill timezone literal, the index-redundancy analysis) as D2a/D3a/D6.

### Out of scope (explicitly deferred)

- **Gateway/dashboard changes.** No consumer of `Reader.LatestSnapshotsByAccount` /
  `SnapshotsByVehicleSince` needs to change: `Snapshot` gains a field, nothing is removed.
- **Collapsing the two vehicle_snapshots indexes into one.** Now that at most one row per
  vehicle per day exists going forward, a future change could in principle re-order
  `LatestSnapshotsByAccount`'s `ORDER BY` to use `captured_date` and retire
  `idx_vehicle_snapshots_vehicle_time`. That is a read-query redesign, not required by any
  binding decision, and is explicitly out of scope here (see design.md's index plan).
- **`.env.example` / README documentation of `POLLER_TIMEZONE` itself.** It is currently
  undocumented outside archived change designs; that gap predates this change and is not
  introduced by it, so fixing it is out of scope.
