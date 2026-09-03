Source: MAG-45 — https://linear.app/magus-monitor/issue/MAG-45/supercharger-sessions-status-column-in-progress-done-calculated-done
Roadmap: openspec/roadmaps/RM41-supercharger-battery-pct-cleanup.md
Tier: 4 of 5 (`charging`; depends on tier 2, `RM41-charging-drop-estimate-columns`,
already archived — the est columns are gone and `SessionVerifier.VerifySession`'s
current shape, including the MAG-36 derivation path, is what this tier builds on)
Unit tests: excluded (new); existing tests repaired where this change breaks them,
plus new integration-test CASES (not new unit-test files) per this design's Test
Contract — the dispatch's own hard rule. The owner's standing default is no new
unit tests; this tier adds no new offline/unit-test file. The status computation is
covered entirely through `db_session_verifier_integration_test.go`, extended with
new DATABASE_URL-gated cases, and a new migration-focused check — never a new
`_test.go` file with no DB.

## Why

`RM41-supercharger-battery-pct-cleanup` tier 4 starts MAG-45's scope, added
mid-roadmap 2026-09-03: give a Supercharger session a stored, auto-set lifecycle
status so `/supercharger-stats` can render it as a badge (tier 5, not this tier).
Today `charging.supercharger_sessions` has no notion of "is this session's battery
data complete" — a caller must infer it by reading both percentage columns and
knowing the rules itself. The roadmap's owner-confirmed design (D10/D11/D13) is a
STORED column, not a read-time computation, so every future reader — the gateway
today, anything else tomorrow — gets one flat string instead of re-deriving the
three-state logic.

`charging` is the only module that may own this column: it is the sole owner of
`supercharger_sessions` (`ai/architecture.md` §2, `internal/charging/AGENTS.md`
§Data Ownership), and the gateway may not touch another module's schema (D13).
Tier 5 (`gateway`) depends on this tier landing first — it cannot render a field
that does not exist.

## What Changes

- **New migration**
  `internal/charging/db/migrations/20260903000004_add_session_status.sql` —
  `ADD COLUMN status TEXT NOT NULL DEFAULT 'IN_PROGRESS' CHECK (status IN
  ('IN_PROGRESS','DONE_CALCULATED','DONE'))`, mirroring
  `20260829000002_add_entry_status.sql`'s shape (the manual-charge `status`
  column) with three codes instead of two. A second statement then backfills
  **every** pre-existing row to `DONE_CALCULATED` unconditionally — the owner's
  explicit decision (roadmap, confirmed twice), not a recompute-from-data `CASE`.
  Full SQL, rationale (including why the backfill needs its own `UPDATE` distinct
  from the column `DEFAULT`), index plan, and data-loss statement in design.md's
  "Database Design" section (this change trips the `database` design gate).
- **`internal/charging/charging.go`** — a new `SessionStatus string` type with
  three constants (`SessionStatusInProgress`, `SessionStatusDoneCalculated`,
  `SessionStatusDone`), a new `Status SessionStatus` field on `Session`, and a
  rewritten `SessionVerifier.VerifySession` doc comment: the method now updates
  **four** columns (`start_battery_pct`, `end_battery_pct`, `battery_pct_source`,
  `status`) plus `updated_at`, not three.
- **`internal/charging/session_verifier.go`** — `VerifySession` computes `status`
  from the same `startToStore`/`endBatteryPct`/derivation-outcome values it
  already computes `battery_pct_source` from (see design.md "Go-side call shape"
  for the exact rule and a new private helper), and passes it to
  `VerifySuperchargerSession`. No new database round-trip: the value is computed
  in Go from data already in hand before the existing `UPDATE` fires.
- **`internal/charging/session_reader.go`** — `rowToSession` gains
  `Status: SessionStatus(r.Status)`; the mapping-rules doc comment gains one
  bullet. `MirrorSuperchargerSession`, `ListSessionsByVehicleBetween`,
  `ListSessionsByVehicleUpdatedSince`, and `ListSessionsByVehicle` need **no SQL
  change** — the three reads already use `SELECT *` and the mirror INSERT already
  omits every percentage column, relying on the column `DEFAULT` exactly as it
  does today for the percentage columns themselves.
- **`internal/charging/db/query.sql`** — `VerifySuperchargerSession`'s `SET`
  clause gains `status = @status`; its doc comment is rewritten to describe four
  target columns instead of three. `MirrorSuperchargerSession`'s existing
  guarding comment gains one sentence noting `status` is excluded from its
  INSERT/SET the same way the three verification columns are — relying on the
  column `DEFAULT`, not a code path.
- **Regenerate `internal/charging/db/{models.go,query.sql.go}`** — `make sqlc`
  after the migration and `query.sql` edits land.
- **Extend `internal/charging/db_session_verifier_integration_test.go`** with new
  DATABASE_URL-gated cases covering the full state truth table (design.md "Test
  Contract" Group S) — no new file, no new offline unit test.
- **`internal/charging/AGENTS.md`** — document `SessionStatus`, the `Status`
  field, the state truth table, and the backfill decision (`CLAUDE.md`
  "docs track structural change").
- **Root `README.md`** — the `charging.supercharger_sessions` row (database-tables
  table) gains a mention of `status`; its stale "five human-verified
  battery-percentage columns" phrase (already wrong since tier 2 dropped the two
  `_est` columns — a pre-existing staleness this tier did not cause, but touches
  the exact same sentence) is corrected to "three" in the same edit.
- **`kkpa/context/use-case/charging/verify-session-battery.md`** — the Database
  table's row 1 (`VerifySuperchargerSession`'s SET clause) is corrected to name
  `status` as a fourth target column.
- **`openspec/specs/charge-session-log/spec.md`** — one NEW Requirement ("A
  Charge Session Carries A Lifecycle Status") plus MODIFIED Requirements
  ("Charge Sessions Are Retrievable For A Vehicle Within A Time Window" gains
  `status` to the "every fact" list; "A Charge Session's Battery Percentages Are
  Correctable By A Human" gains the status side-effect of a correction). See
  `specs/charge-session-log/spec.md` in this change and design.md "Spec delta".

**NOT in this change:**
- Anything under `internal/gateway/` — tier 5, depends on this tier, not started.
- `internal/telemetry/` — `telemetry.supercharger_history` has no status column and
  none is proposed; the status is charging-owned verification-channel metadata,
  not a mirrored fact (D13).
- `internal/analytics/` — no read path in this tier's scope filters, orders, or
  joins by `status`; nothing there needs a change.
- The `battery_pct_source` CHECK / value set — unchanged, per the leader's binding
  CALC MARKER decision: no `derived` value is added to it.

## Breaking

**No — externally.** No HTTP route or JSON shape changes (the gateway is a later,
dependent tier). `charging.Session` gains one field, which is additive to a Go
struct literal built with keyed fields everywhere it appears in this repository
(`grep -rn "charging\.Session{" .` — confirmed every call site uses `Field: value`
form, never positional) — no existing caller breaks by compiling. `SessionVerifier`
gains no new method and no new parameter; its existing signature is unchanged.

## Modules Affected

- **`internal/charging/`** — the only code module touched: the new migration,
  `charging.go`, `session_verifier.go`, `session_reader.go`, `db/query.sql`, the
  extended integration test, `AGENTS.md`.
- **`kkpa/context/`** and root **`README.md`** — doc fixes only, not a code
  module.

## Read Paths Affected — performance-sensitive, per `openspec/config.yaml`'s proposal rule

None new. `ListSessionsByVehicleBetween`, `ListSessionsByVehicleUpdatedSince`, and
`ListSessionsByVehicle` all read via `SELECT *` against
`idx_supercharger_sessions_vehicle_stop` — adding one `NOT NULL` column widens the
row returned by that same index scan by a few bytes; it adds no predicate, no join,
no new query, and needs no new index (Performance-Profile: read-heavy — see
design.md "Index Plan" for the full justification of "no index").

## Capabilities

### New Capabilities

- **A Charge Session Carries A Lifecycle Status (`charge-session-log`)** — every
  charge session record carries a status describing whether its battery-percentage
  data is complete, and whether a complete record's start percentage was supplied
  by a human or derived by the platform. See `specs/charge-session-log/spec.md`.

### Modified Capabilities

- **Charge Sessions Are Retrievable For A Vehicle Within A Time Window
  (`charge-session-log`)** — a retrieved record now also carries its lifecycle
  status. See `specs/charge-session-log/spec.md`.
- **A Charge Session's Battery Percentages Are Correctable By A Human
  (`charge-session-log`)** — a correction now also updates the record's lifecycle
  status, as a direct effect of the percentages it changes (not an unrelated
  fact). See `specs/charge-session-log/spec.md`.

### Out of scope (explicitly deferred)

- **Rendering the status as a badge** — tier 5,
  `RM41-gateway-add-session-status-column`, depends on this tier.

## Testing

Per the Test-Execution-Policy: the implementing worker writes/repairs tests and
runs `go build ./...`, `go vet ./...`, `gofmt -l`, `make build`, `make vet`,
`make bins`, and the standalone guards `make migration-guard`/`make
boundary-guard` (this change's own relevant guards — no UI, i18n, money, or
time-zone concern is touched) — never `go test ./...`, `make test`, `make
test-with-db`, or `make check`. The owner runs `go test ./internal/charging/...`
and reports results; until then this tier's implementation status is
**awaiting-user-verification**, never "done." See design.md "Test Contract" for
the concrete new test cases authored before implementation.

**Owner-only verification (post-`make migrate-up`):** confirm the migration
applies cleanly against the real database and that
`SELECT status, count(*) FROM charging.supercharger_sessions GROUP BY status`
shows every pre-existing row under `DONE_CALCULATED` (per the owner's backfill
decision) and any newly-mirrored session under `IN_PROGRESS`. See tasks.md's
final wave.

## Resolved decisions

Roadmap D10 (stored column, recomputed on every write touching either
percentage), D11 (three codes; `DONE_CALCULATED` means the end percentage is
recorded and the start was derived), D13 (this column belongs to `charging`; the
gateway renders it in a later, dependent tier) are carried from the roadmap and
not re-litigated here. Two decisions were resolved by the leader with the owner,
today, closing a gap the roadmap left open, and are equally binding on this
design:

- **CALC MARKER** — `VerifySession` sets the status code itself, at write time,
  from what `needsDerivedStartBatteryPct` already told it. `battery_pct_source`
  is NOT extended with a `derived` value; its CHECK stays exactly as it is.
- **BACKFILL** — the migration backfills ALL existing sessions to
  `DONE_CALCULATED`, unconditionally, regardless of what a recompute against their
  actual percentage columns would say — the owner's decision on provenance the
  data itself cannot show (see design.md "Rationale" for the full reasoning and
  the recompute-mismatch evidence the leader showed the owner before this was
  confirmed).

This design's own local decisions (see design.md) cover only what the roadmap and
the leader's interview left to the implementer: the full state truth table
(including the start-set/end-NULL corner), the exact migration SQL, the exact
`VerifySession` computation, the Go type shape, and the index plan.

**This tier trips the `database` design gate** (`openspec/config.yaml` — "any
new/changed database object ... MUST include the full schema, the rationale ...
and an index plan"; `CLAUDE.md` Pipeline config `Design-Gates: database`). See
design.md "Database Design" for the full gate content — migration SQL up/down,
rationale, index plan, and data-loss statement. This gate blocks implementation
until the owner explicitly confirms the design.
