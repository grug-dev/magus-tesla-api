# Design — RM44-platform-add-mirror-watermark

## Context

`internal/app.processChargingData` mirrors every Supercharger session an
account has ever had into `internal/charging` every night, so it can compose
the two modules' ports without either module knowing about the other. Tiers
2–3 fixed `updated_at` to mean "this row's data changed" — but the READ that
feeds the mirror is still unbounded: `SuperchargerHistoryByAccount(ctx,
accountID, 0)` resolves `limit 0` to `math.MaxInt32`, so every night reads
and re-mirrors an account's entire Supercharger history, however old.

This tier bounds that read with a cursor: a watermark holding the highest
`updated_at` telemetry has reported for an account, owned by
`internal/charging` (the module that reads, per roadmap D4's rule), advanced
only when the bounded read actually returns something.

This design is written against the roadmap's binding decisions D4–D6, D10,
and D20–D24 (`openspec/roadmaps/RM44-incremental-supercharger-sync.md` and
its `progress.json`). Those are not re-opened here. This design's own
decisions are numbered D1–Dn below, independent of the roadmap's numbering.

## Goals / Non-Goals

**Goals:**
- The nightly mirror read is bounded to sessions changed since the
  account's own cursor, minus a 24-hour commit-skew overlap.
- An empty bounded read leaves the cursor untouched — never advances it to
  `now()` (roadmap D5, the highest-risk rule in this tier).
- A missing cursor means "epoch" — a first-ever run (or a months-idle
  account) backfills the whole account once, then goes quiet.
- The orphan-recovery path (a session whose vehicle re-registers, flipping
  `tesla_id` from `NULL` to a value) keeps working under a bounded,
  account-wide read.
- `charging.SessionWriter.MirrorSessions`'s signature is unchanged.

**Non-Goals:**
- No change to `internal/analytics` (roadmap D1).
- No change to `MirrorSuperchargerSession`'s comparison logic — tier 3
  already shipped that.
- No gateway, route, or UI change.
- This design does not implement `internal/app`'s wiring edit — that is
  leader-owned (see "Cross-Module Wiring" below) — but it specifies the
  exact shape so the leader implements it without re-deriving anything.

## Database Design (design gate — `database`)

### Full schema: `charging.mirror_watermarks`

```sql
-- +goose Up
-- mirror_watermarks: one cursor per account, holding the highest
-- telemetry.supercharger_history.updated_at internal/charging's nightly
-- mirror has already synchronized (RM44-platform-add-mirror-watermark,
-- MAG-48, roadmap D4/D20-D22). Owned by internal/charging; no other module
-- may import the generated chargingdb package (ai/architecture.md §2).
--
-- NO tesla_id column: the mirror read this cursor bounds is account-wide,
-- not per vehicle (roadmap D20) — telemetry's only per-vehicle
-- updated-since query filters tesla_id = X, which can never return a
-- tesla_id IS NULL row, defeating the orphan-recovery path this table
-- exists to keep working (roadmap D3). A per-account cursor matches the
-- per-account read exactly.
--
-- NO source column: unlike analytics.vehicle_metric_watermarks, this
-- table's owning module (charging) mirrors exactly one upstream table
-- (telemetry.supercharger_history). A second mirrored source would be an
-- additive migration adding the column then, not a speculative one now
-- (roadmap D21, "do not over-abstract" — CLAUDE.md §Non-negotiables).
--
-- Two alternatives were considered and rejected (roadmap D4, cited here,
-- not re-derived):
--   1. Reuse or move analytics.vehicle_metric_watermarks — rejected for
--      CORRECTNESS. That table tracks a different read (analytics reads
--      charging.supercharger_sessions; this cursor bounds a read of
--      telemetry.supercharger_history). Sharing one date across both loses
--      data on a normal night: at 05:15 the mirror writes new charging
--      rows and sets a shared cursor to 05:15; at 05:16 analytics asks
--      charging "what changed since 05:15?" and is told nothing, skipping
--      the rows the mirror just wrote. The rule: a cursor belongs to the
--      module that READS, never the module that is read — telemetry must
--      not own a table describing how far a consumer has read it.
--   2. A source_updated_at column on charging.supercharger_sessions
--      (cursor = MAX() per account) — rejected on DESIGN, not correctness.
--      It works, including the months-idle case. Rejected because it puts
--      mirror bookkeeping inside a domain table, and the watermark-table
--      shape is already proven (this table follows the identical shape
--      analytics.vehicle_metric_watermarks already established).
CREATE TABLE charging.mirror_watermarks (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id        UUID NOT NULL,
    source_updated_at TIMESTAMPTZ NOT NULL,

    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT mirror_watermarks_account_unique UNIQUE (account_id)
);

COMMENT ON TABLE charging.mirror_watermarks IS
    'One Supercharger-mirror cursor per account (RM44-platform-add-mirror-watermark, '
    'MAG-48). Holds the highest telemetry.supercharger_history.updated_at this '
    'module''s nightly mirror has already synchronized for that account. No row '
    'yet for an account means "epoch": the next mirror run backfills that '
    'account''s whole history once. Owned by internal/charging; no other module '
    'reads this table directly.';

COMMENT ON COLUMN charging.mirror_watermarks.source_updated_at IS
    'The maximum updated_at internal/charging''s mirror has observed from '
    'telemetry.supercharger_history for this account, as of its last run. The '
    'mirror queries SuperchargerHistoryByAccountUpdatedSince(source_updated_at - '
    '24h) and advances this column only when that query returns at least one row '
    '-- a run that returns zero rows leaves this column UNTOUCHED (roadmap D5: '
    'advancing it to now() on an empty read would permanently and silently lose '
    'any row that commits a moment late).';

-- Index Plan: the sole read pattern this table serves is a single-row
-- lookup, WHERE account_id = $1, which mirror_watermarks_account_unique's
-- own index serves entirely. No separate CREATE INDEX.

-- +goose Down
DROP TABLE IF EXISTS charging.mirror_watermarks;
```

Migration file: `internal/charging/db/migrations/20260906000001_add_mirror_watermarks.sql`
— sorts after the latest existing charging migration
(`20260903000004_add_session_status.sql`). Lives inside the existing
`charging` schema (`CREATE SCHEMA IF NOT EXISTS charging` already ran in
`20260902000003_move_charging_to_own_schema.sql`) — this migration issues no
`CREATE SCHEMA` of its own.

### The two new sqlc queries (`internal/charging/db/query.sql`)

Mirror `analytics`'s `GetVehicleMetricWatermark` / `UpsertVehicleMetricWatermark`
shape exactly, minus the columns this table does not have:

```sql
-- name: GetMirrorWatermark :one
-- Single-row cursor lookup for one account (roadmap D20-D22). Returns
-- pgx.ErrNoRows when no watermark exists yet, which charging's
-- MirrorWatermarkStore.MirrorWatermark treats as "epoch": the account has
-- never been mirrored under the bounded read, so the caller backfills the
-- account's full Supercharger history in one pass. Served entirely by
-- mirror_watermarks_account_unique's own index — no separate CREATE INDEX.
SELECT source_updated_at
FROM charging.mirror_watermarks
WHERE account_id = @account_id;

-- name: UpsertMirrorWatermark :exec
-- Advance one account's cursor (roadmap D5/D22). Called only when the
-- caller's bounded telemetry read returned at least one row, advanced to
-- the max updated_at observed on that run -- a call with zero rows never
-- reaches this query at all (the caller's own responsibility; see
-- design.md "Cross-Module Wiring"). created_at is DELIBERATELY ABSENT from
-- the SET clause -- it must record when this account's cursor was FIRST
-- created, not the most recent advance, mirroring
-- UpsertVehicleMetricWatermark's identical convention.
INSERT INTO charging.mirror_watermarks (
    account_id, source_updated_at
) VALUES (
    @account_id, @source_updated_at
)
ON CONFLICT (account_id) DO UPDATE SET
    source_updated_at = EXCLUDED.source_updated_at,
    updated_at         = now();
```

### The new telemetry query (`internal/telemetry/db/query.sql`)

```sql
-- name: SuperchargerHistoryByAccountUpdatedSince :many
-- Return every Supercharger session for one account whose updated_at is at
-- or after @since, ordered oldest-first by updated_at. Used by
-- SuperchargerHistoryReader.SuperchargerHistoryByAccountUpdatedSince
-- (RM44-platform-add-mirror-watermark, roadmap D20) to bound
-- internal/app's nightly Supercharger mirror read.
--
-- UNLIKE SuperchargerHistoryByVehicleUpdatedSince, this query takes no
-- tesla_id and filters on account_id alone -- so it is the only
-- updated-since query that CAN return a row whose tesla_id IS NULL (a
-- session for a vehicle that is not currently registered). That is
-- deliberate: the mirror this bounds reads per account precisely because a
-- per-vehicle read can never surface such a row, breaking the
-- orphan-recovery path that lets a session get mirrored once its vehicle
-- re-registers (roadmap D3, carried into this tier by D20).
--
-- Index: idx_supercharger_history_account_updated (account_id,
-- updated_at), added by this change's own telemetry migration. It matches
-- this query exactly -- account_id prunes to the tenant, and updated_at
-- ASC satisfies both the range predicate and the ORDER BY in one index
-- scan, with no sort step. Do NOT confuse it with the pre-existing
-- idx_supercharger_history_account_time (account_id,
-- charge_start_date_time DESC), which shares only the account_id prefix
-- and would leave updated_at as a residual filter plus an in-memory sort.
SELECT * FROM telemetry.supercharger_history
WHERE account_id = @account_id
  AND updated_at >= @since
ORDER BY updated_at ASC;
```

### Full schema — the new telemetry index

The owner confirmed the design gate on 2026-09-06 with ONE change: build the
index now instead of only documenting it as a revisit trigger.

The index is on `telemetry.supercharger_history`, so it ships in a
**telemetry** migration, never in the charging one — a module may only alter
its own tables (`ai/architecture.md` §2).

```sql
-- +goose Up
-- idx_supercharger_history_account_updated: serves
-- SuperchargerHistoryByAccountUpdatedSince, the account-wide updated-since
-- read that bounds internal/charging's nightly Supercharger mirror
-- (RM44-platform-add-mirror-watermark, MAG-48, roadmap D20).
--
-- (account_id, updated_at) matches that query exactly: account_id prunes to
-- the tenant, and updated_at ASC satisfies both the `updated_at >= $2`
-- range predicate AND the `ORDER BY updated_at ASC` in one index scan, so
-- the planner needs no sort step. The pre-existing
-- idx_supercharger_history_account_time (account_id,
-- charge_start_date_time DESC) shares only the account_id prefix and is not
-- sorted on updated_at, so it would leave updated_at as a residual filter
-- plus an in-memory sort.
--
-- Owner's call at the design gate: pay a small, permanent write cost on
-- every supercharger_history upsert to remove the chance of a slow nightly
-- batch read later. The alternative -- document the index as a revisit
-- trigger and add it only if a slow-query log ever showed it -- was the
-- design's original recommendation and was NOT chosen.
CREATE INDEX idx_supercharger_history_account_updated
    ON telemetry.supercharger_history (account_id, updated_at);

-- +goose Down
DROP INDEX IF EXISTS telemetry.idx_supercharger_history_account_updated;
```

### Index Plan

**`charging.mirror_watermarks`: no index beyond `UNIQUE (account_id)`.** The
only read this table serves — `GetMirrorWatermark`'s single-row lookup — is
a point read on that exact column, which the UNIQUE constraint's own B-tree
index already serves. A second index would exist for a read pattern nobody
runs.

**`SuperchargerHistoryByAccountUpdatedSince`: ONE new index,
`idx_supercharger_history_account_updated (account_id, updated_at)`.**

The owner chose this at the design gate, over the design's original
recommendation of no index. The reasoning on both sides is recorded so a
later reader sees the trade that was made.

*Why the index is right:* it matches the query exactly. `account_id` prunes
to the tenant; `updated_at` ASC satisfies both the `updated_at >= $2` range
predicate and the `ORDER BY updated_at ASC` in a single index scan, so the
planner needs no sort step at all. Neither existing index does this:
`idx_supercharger_history_account_time` is sorted on
`charge_start_date_time`, and `idx_supercharger_history_vehicle_time` needs
a `tesla_id` this query does not filter on.

*What it costs:* every `INSERT` and every `ON CONFLICT DO UPDATE` on
`telemetry.supercharger_history` now maintains one more B-tree, forever.
That write path runs at the nightly poll. Tier 2 already made those updates
conditional, so an unchanged row no longer writes at all — which shrinks
this cost rather than growing it.

*What was rejected:* shipping no index and documenting a revisit trigger.
That was the design's original recommendation, on the grounds that this is a
batch read (once per account per night, never behind `internal/gateway`) over
a small set (~36 sessions/account/year, roadmap D4), and that the identical
per-vehicle precedent `SuperchargerHistoryByVehicleUpdatedSince` shipped
without one. The owner chose certainty now over a possible slow-query
investigation later. Both positions are defensible; this records which was
taken and why.

*Verification:* task 1a.7 asserts via `EXPLAIN` that the query uses this
index and needs no sort step (T-tel-7), the same way the per-vehicle
precedent verified its own planner behavior.

## Interfaces

### `internal/telemetry` — one method added to `SuperchargerHistoryReader`

```go
// SuperchargerHistoryByAccountUpdatedSince returns every stored Supercharger
// session for the given account whose updated_at is at or after `since`,
// ordered oldest-first by updated_at. Unlike
// SuperchargerHistoryByVehicleUpdatedSince, this method takes no teslaID and
// so is the only updated-since method that CAN return a session whose
// TeslaID is nil -- deliberately, so a bounded per-account mirror read
// still recovers a session once its vehicle re-registers
// (RM44-platform-add-mirror-watermark, roadmap D3/D20). Returns a non-nil
// empty slice and nil error when nothing for the account has been updated
// at or after `since` (parity with every other SuperchargerHistoryReader
// method's empty-result contract). Served by
// idx_supercharger_history_account_updated (account_id, updated_at), added
// by this change: account_id prunes and updated_at both bounds the range and
// gives the ordering, so the read needs no sort step (design.md Index Plan).
// Reuses the existing rowToSuperchargerHistory mapper.
SuperchargerHistoryByAccountUpdatedSince(ctx context.Context, accountID uuid.UUID, since time.Time) ([]SuperchargerHistory, error)
```

Declared in `telemetry.go`'s `SuperchargerHistoryReader` interface, right
after `SuperchargerHistoryByVehicleUpdatedSince`. Implemented in `reader.go`
(`superchargerHistoryReader.SuperchargerHistoryByAccountUpdatedSince`),
mirroring `SuperchargerHistoryByAccount`'s exact shape (account-only params,
`rowToSuperchargerHistory` mapper, no per-vehicle scoping). One new explicit
method added to `loggingSuperchargerHistoryReader` in `query_log.go`
(tier 1's non-embedding decorator, D7) — logging AFTER delegating, same as
its three siblings, printing `account`, `since`, and `rows`.
`NewSuperchargerHistoryReader`'s existing wrapping of the concrete reader in
`newLoggingSuperchargerHistoryReader` means the new method is logged
automatically once added there — no change to the constructor itself.

### `internal/charging` — one new port, `MirrorWatermarkStore`

```go
// MirrorWatermarkStore is the cursor port for the Supercharger mirror read
// (RM44-platform-add-mirror-watermark, MAG-48). Both methods share ONE
// caller and ONE trust model -- internal/app reads the cursor, bounds its
// telemetry read by it, mirrors, then advances it -- unlike
// SessionWriter/SessionReader/SessionVerifier's three-way split, which
// exists because those ports serve callers with genuinely different trust
// models (nightly sync vs. dashboard read vs. human edit). One port here
// keeps the vocabulary closed rather than splitting for its own sake
// (CLAUDE.md's "do not over-abstract" AI-efficiency rule).
type MirrorWatermarkStore interface {
	// MirrorWatermark returns the stored cursor for accountID: the highest
	// telemetry updated_at the mirror has already synchronized. No stored
	// row means "epoch" -- the zero time.Time, not an error -- so the
	// caller's first-ever read for this account is unbounded and backfills
	// the account's whole history once. Mirrors
	// analytics.recalculator.watermark's identical
	// "pgx.ErrNoRows -> time.Time{}, nil" translation exactly (design.md
	// D5, copying rather than re-deriving analytics.Recalculator.Reconcile's
	// own rule).
	MirrorWatermark(ctx context.Context, accountID uuid.UUID) (time.Time, error)

	// AdvanceMirrorWatermark upserts accountID's cursor to observed, the
	// highest updated_at the caller actually saw on this run. This method
	// MUST be called only when the caller's bounded telemetry read
	// returned at least one row (roadmap D5) -- it performs no such check
	// itself and trusts the caller completely, mirroring
	// analytics.recalculator.advanceWatermark's identical division of
	// responsibility (Reconcile decides whether to call it; the method
	// itself just upserts). Calling this with observed == time.Time{} (the
	// zero value) on a call the caller should not have made is a caller
	// bug, not a case this method guards against, by design -- see
	// design.md "Cross-Module Wiring" for why the guard lives one layer up.
	AdvanceMirrorWatermark(ctx context.Context, accountID uuid.UUID, observed time.Time) error
}

// NewMirrorWatermarkStore -- the only publicly exported factory function
// for this port.
func NewMirrorWatermarkStore(pool *pgxpool.Pool) MirrorWatermarkStore
```

Interface + constructor declared in `charging.go`, alongside this module's
other ports. Implementation in a new file, `mirror_watermark.go`
(`mirrorWatermarkStore` concrete type, `newMirrorWatermarkStore` internal
constructor — mirroring `sessionWriter`/`newSessionWriter`'s exact pattern
in `session_writer.go`). `pgtype.Timestamptz` conversion stays confined to
this one file, per `internal/charging/AGENTS.md`'s `pgtype`-boundary rule —
`MirrorWatermark` translates `pgx.ErrNoRows` to `(time.Time{}, nil)`;
`AdvanceMirrorWatermark` builds `pgtype.Timestamptz{Time: observed, Valid:
true}` inline, exactly like `analytics.recalculator.advanceWatermark` does
for its own upsert.

## Cross-Module Wiring (leader-owned — not a task for either module worker)

`internal/app.processChargingData` (`internal/app/processor.go:159`)
changes from this:

```go
sessions, err := p.superchargerHistoryReader.SuperchargerHistoryByAccount(ctx, v.AccountID, 0)
...
if err := p.sessionWriter.MirrorSessions(ctx, v.AccountID, mirrored); err != nil { ... }
```

to this shape:

```go
cursor, err := p.mirrorWatermarks.MirrorWatermark(ctx, v.AccountID)
if err != nil {
    log.Printf("session mirror: account %s: reading watermark: %v", v.AccountID, err)
    continue
}

sessions, err := p.superchargerHistoryReader.SuperchargerHistoryByAccountUpdatedSince(ctx, v.AccountID, cursor.Add(-24*time.Hour))
if err != nil {
    log.Printf("session mirror: account %s: reading sessions: %v", v.AccountID, err)
    continue
}
if len(sessions) == 0 {
    continue // roadmap D5: zero rows -> do NOT advance the watermark
}

mirrored := make([]charging.SessionMirror, 0, len(sessions))
var maxUpdated time.Time
for _, s := range sessions {
    mirrored = append(mirrored, charging.SessionMirror{ /* unchanged field-by-field mapping */ })
    if s.UpdatedAt.After(maxUpdated) {
        maxUpdated = s.UpdatedAt
    }
}

if err := p.sessionWriter.MirrorSessions(ctx, v.AccountID, mirrored); err != nil {
    log.Printf("session mirror: account %s: %v", v.AccountID, err)
    continue
}
if err := p.mirrorWatermarks.AdvanceMirrorWatermark(ctx, v.AccountID, maxUpdated); err != nil {
    log.Printf("session mirror: account %s: advancing watermark: %v", v.AccountID, err)
    continue
}
log.Printf("session mirror: account %s: %d session(s)", v.AccountID, len(mirrored))
```

Notes for the leader:
- `24*time.Hour` is `recalcOverlap`'s twin (roadmap D6) — a **new**
  package-level constant in `internal/app`, not a shared import from
  `internal/analytics` (that constant is unexported there, and the two
  overlaps protect different reads; importing one module's private overlap
  into another would be exactly the coupling roadmap D4 rejected for the
  watermark table itself).
- `p.mirrorWatermarks` is a new field on `processor` (type
  `charging.MirrorWatermarkStore`), threaded through the same constructor
  path as `p.sessionWriter`/`p.superchargerHistoryReader` today
  (`internal/app/app.go`'s `New` function and `cmd/web`'s wiring).
  `charging.NewMirrorWatermarkStore(pool)` is constructed alongside
  `charging.NewSessionWriter(pool)` wherever that already happens.
- `maxUpdated` must be computed the same way
  `analytics.recalculator.Reconcile` computes its own `max*Updated`
  variables: a plain loop over the fetched rows' `UpdatedAt`, taking the
  latest — never `clock.Now()`, which would defeat D5's entire purpose.
- The per-account `continue` on any error (reading the watermark, reading
  sessions, mirroring, or advancing the watermark) preserves this
  function's existing per-account isolation — one account's failure never
  aborts another's, and a missed mirror or a missed watermark advance
  self-heals next cycle (the read is idempotent either way).
- `internal/app/processor_test.go`'s `fakeSuperchargerHistoryReader` gains
  a `SuperchargerHistoryByAccountUpdatedSince` method (empty by default, or
  parameterized like its siblings) or the whole change fails to compile —
  see proposal.md "Breaking" and "Findings" below.

## Test Contract

These are the exact test cases and their expected values, authored before
any implementation exists.

### `internal/telemetry` — `SuperchargerHistoryByAccountUpdatedSince`

| # | Case | Setup | Expected result |
|---|---|---|---|
| T-tel-1 | Returns sessions updated at or after `since`, across vehicles | Two vehicles in one account, each with one session whose `updated_at` is at or after `since`, one with `updated_at` before `since` | The two at-or-after sessions, both included; the earlier one excluded |
| T-tel-2 | Includes a session with `tesla_id IS NULL` | A session whose VIN belongs to no currently-registered vehicle (`tesla_id` NULL), `updated_at` at or after `since` | The orphaned session IS included |
| T-tel-3 | Excludes another account's session | Two accounts, each with a qualifying session | Only the requested account's session is returned |
| T-tel-4 | Ordered oldest-first by `updated_at` | Three qualifying sessions with different `updated_at` values, inserted out of order | Returned ascending by `updated_at` |
| T-tel-5 | Empty result, no error | An account with no session updated at or after `since` | Empty, non-nil slice; nil error |
| T-tel-6 | Exact boundary is inclusive | A session whose `updated_at` equals `since` exactly | Included |
| T-tel-7 | The query uses the new index and needs no sort step | An account with several qualifying sessions; run `EXPLAIN` on the query | The plan uses `idx_supercharger_history_account_updated` and contains no `Sort` node |

### `internal/charging` — `MirrorWatermarkStore`

| # | Case | Setup | Expected result |
|---|---|---|---|
| T-cw-1 | No row yet returns the epoch | An account with no `mirror_watermarks` row | `MirrorWatermark` returns `time.Time{}` (zero value) and `nil` error |
| T-cw-2 | A stored cursor is returned exactly | A row with `source_updated_at = X` | `MirrorWatermark` returns exactly `X` |
| T-cw-3 | `AdvanceMirrorWatermark` creates a first row | No prior row; `AdvanceMirrorWatermark(ctx, accountID, X)` | A subsequent `MirrorWatermark` call returns exactly `X` |
| T-cw-4 | `AdvanceMirrorWatermark` overwrites an existing row | A prior row at `X`; `AdvanceMirrorWatermark(ctx, accountID, Y)` where `Y` is later than `X` | A subsequent `MirrorWatermark` call returns exactly `Y`, not `X` |
| T-cw-5 | Cursors are isolated per account | Two accounts, each advanced to different values | Each account's `MirrorWatermark` returns only its own value |
| T-cw-6 | `created_at` is set once, not on every advance | Advance once at `T1`; read `created_at`; advance again at `T2` | `created_at` is unchanged across the second advance; `updated_at` (bookkeeping column) changes |

### D5, the critical rule — proven at the `internal/app` composition level

The pinned expected behavior (implemented by the leader, per "Cross-Module
Wiring" above, but stated here as the design's binding contract so an
`internal/app`-level test can assert it directly):

| # | Case | Input | Expected watermark after the run |
|---|---|---|---|
| T-app-1 | A run whose bounded read returns ZERO rows leaves the watermark untouched | `MirrorWatermark` returns `X` before the run; `SuperchargerHistoryByAccountUpdatedSince(accountID, X - 24h)` returns an empty slice | `MirrorWatermark` still returns exactly `X` after the run — `AdvanceMirrorWatermark` is never called |
| T-app-2 | A run that returns rows advances to the MAXIMUM `updated_at` observed, never to `now()` | The bounded read returns three sessions with `updated_at` values `A < B < C`, where `C` is earlier than `clock.Now()` | `MirrorWatermark` returns exactly `C` after the run — not `clock.Now()`, and not `A` or `B` |
| T-app-3 | A partial-failure run does not advance the watermark | The bounded read returns rows, but `MirrorSessions` returns an error | `MirrorWatermark` is unchanged from before the run — advancing happens only after a successful mirror, so a retry next cycle re-reads the same window rather than skipping it |

## Local Decisions

### D1 — Table shape and columns: exactly roadmap D21, no more, no less

`charging.mirror_watermarks(id, account_id UNIQUE, source_updated_at,
created_at, updated_at)`. No `tesla_id`, no `source`. Compliance: see "Full
schema" above — the migration ships exactly these five columns and one
constraint.

### D2 — Both rejected alternatives are roadmap D4's, cited not re-derived

Per the dispatch's explicit instruction, this design does not re-argue
either alternative — both are quoted in the migration's own comment block
("Full schema" above) and summarized in this section for the design gate.
Compliance: the migration file itself carries both, so a future reader
hits the reasoning at the point of the schema, not only in an OpenSpec
change folder.

### D3 — `MirrorWatermark`'s epoch translation copies `analytics.recalculator.watermark` exactly

Per roadmap D5's explicit instruction ("copy it rather than re-deriving
it"). Compliance: `MirrorWatermark`'s doc comment names the analytics
method it mirrors; the Go shape (`pgx.ErrNoRows` -> `(time.Time{}, nil)`)
is identical, field-for-field.

### D4 — `MirrorWatermarkStore` is one interface, not a split

Deliberately different from `SessionWriter`/`SessionReader`/`SessionVerifier`'s
three-way split, because that split exists to keep three DIFFERENT callers
(nightly sync, dashboard/analytics read, human edit) each depending on only
what they need. This port has exactly one caller
(`internal/app.processChargingData`) needing both methods in the same call
sequence, so a split here would be indirection with no consumer asking for
it — the "do not over-abstract" half of CLAUDE.md's AI-efficiency rule.
Compliance: one interface, two methods, one constructor.

### D5 — No validation inside `AdvanceMirrorWatermark`; the empty-read guard lives in the caller

Mirrors `analytics.recalculator.advanceWatermark`'s identical division of
labor: `Reconcile` (the caller) decides whether to call `advanceWatermark`
at all (only when `len(rows) > 0`); the method itself performs no
row-count check. This design keeps that same split rather than adding a
defensive check inside `AdvanceMirrorWatermark` (e.g. rejecting a zero
`observed`), because `internal/app` already computes `maxUpdated` only
from rows it fetched, and a genuine caller bug that skips the `len(sessions)
== 0` guard would still be visible as a symptom (the watermark jumping to
whatever `maxUpdated`'s zero-initialized value produces, which is the zero
`time.Time` — an epoch, not `now()` — so even a caller bug here fails safe
rather than silently losing data). Compliance: `AdvanceMirrorWatermark`'s
doc comment states this division explicitly; no length/zero check is added
to the implementation.

### D6 — `SuperchargerHistoryByAccountUpdatedSince` is intentionally the one method that can return a `tesla_id IS NULL` row

This is the single reason the per-account method exists rather than
looping the per-vehicle one (roadmap D20). Compliance: the query has no
`tesla_id` predicate at all; T-tel-2 pins this behavior with a test.

### D7 — One new index, on the telemetry side only

REVISED at the design gate by the owner on 2026-09-06. The original text
read "no new index anywhere in this tier"; the owner chose to build the
index now rather than document it as a revisit trigger. See "Index Plan"
above for both positions and the trade that was made.

Compliance: `charging.mirror_watermarks` ships one `UNIQUE` constraint and
no `CREATE INDEX` — unchanged. `telemetry.supercharger_history` gains
`idx_supercharger_history_account_updated (account_id, updated_at)`, in its
own telemetry migration (task 1a.0), never in the charging one — a module
may only alter its own tables (`ai/architecture.md` §2). T-tel-7 asserts via
`EXPLAIN` that the new query actually uses it and needs no sort step.

### D8 — `internal/app`'s wiring is specified here, implemented by the leader

Per the dispatch's explicit statement that cross-module wiring in
`internal/app` is leader-owned. This design gives the exact before/after
code shape, the new constant, the new field, and the wiring path so the
leader implements it without re-deriving the composition. Compliance: see
"Cross-Module Wiring" above.

## Findings — reported per the dispatch's explicit request

**`internal/app/processor_test.go`'s `fakeSuperchargerHistoryReader` fully
implements `telemetry.SuperchargerHistoryReader` today** (leader-corrected
count: it has **four** explicit, non-embedding methods —
`SuperchargerHistoryByAccount`, `SuperchargerHistoryByVehicle`,
`SuperchargerHistoryByVehicleBetween` and
`SuperchargerHistoryByVehicleUpdatedSince` — plus a compile-time assertion
`var _ telemetry.SuperchargerHistoryReader = fakeSuperchargerHistoryReader{}`
at `internal/app/processor_test.go:215`. It is that assertion that breaks.)
Adding `SuperchargerHistoryByAccountUpdatedSince` to the interface breaks
this fake's compile-time satisfaction of the interface, which breaks
`internal/app`'s existing test suite (`processor_test.go`), not just the new
wiring this tier adds.

This file sits in neither the telemetry worker's nor the charging worker's
sandbox — it lives in `internal/app`, which the dispatch states is
leader-owned for this tier. **Neither D20 nor D22 name this file.** This is
a genuine gap in the roadmap's decisions, found the same way the dispatch
warned two earlier tiers found holes here: the leader must add a
`SuperchargerHistoryByAccountUpdatedSince` method to
`fakeSuperchargerHistoryReader` in the SAME change that adds the interface
method, or `go vet ./internal/app/...` (a signal the assistant IS allowed
to run) fails immediately. This is flagged in tasks.md as an explicit
 leader-owned task, not left implicit.

**Leader sweep (2026-09-06).** The leader swept the whole repo for every
implementor of `SuperchargerHistoryReader`, because narrow greps have missed
references in this repo before. The complete list is four, and all four are
covered by tasks.md:

| Implementor | File | Owner |
|---|---|---|
| `superchargerHistoryReader` | `internal/telemetry/reader.go:137` | telemetry worker |
| `loggingSuperchargerHistoryReader` | `internal/telemetry/query_log.go:211` | telemetry worker |
| `fakeQueryLogSCHReader` | `internal/telemetry/query_log_test.go:119` | telemetry worker |
| `fakeSuperchargerHistoryReader` | `internal/app/processor_test.go:215` | **leader** (task 3.3) |

`internal/analytics/db_integration_test.go:795` names the interface in a
comment only. It implements nothing and needs no change.

No other finding: D21's table shape, D22's port shapes, D23/D24's
two-module-one-change-under-`platform`-prefix shape, and the two rejected
alternatives from D4 all check out against the real code with no
contradiction found.
