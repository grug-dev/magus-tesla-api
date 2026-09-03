Source: MAG-36 — https://linear.app/magus-monitor/issue/MAG-36/supercharger-session-battery-start-calculated
Roadmap: openspec/roadmaps/RM41-supercharger-battery-pct-cleanup.md
Tier: 2 of 3 (`charging`; depends on tier 1, `RM41-gateway-revise-battery-pct-ui`,
already archived — the gateway no longer names `StartBatteryPctEst`/`EndBatteryPctEst`)
Unit tests: excluded (new); existing tests repaired where this change breaks them
(roadmap D7). The owner's standing default is no new unit tests. Four `charging`
integration tests plus the granted path `internal/analytics/db_integration_test.go`
stop compiling the moment `charging.Session` loses its two `*Est` fields and
`charging.supercharger_sessions` loses its two columns; design.md authors the exact
expected edits up front, per `ai/go-conventions.md`'s "author expected values first"
rule.

## Why

`RM41-supercharger-battery-pct-cleanup` (roadmap, read in full before this proposal)
finishes MAG-36's remaining scope. Tier 1 (`gateway`, archived) removed every gateway
read of `charging.Session.StartBatteryPctEst`/`EndBatteryPctEst`, which is what
unblocks this tier: `charging` can now drop the two columns the gateway no longer
names (roadmap D3).

`start_battery_pct_est` and `end_battery_pct_est` on `charging.supercharger_sessions`
have been permanently `NULL` since they shipped in
`RM29-charging-add-charge-sessions` — nothing in this repository writes them.
`MirrorSuperchargerSession`'s INSERT/ON CONFLICT clauses exclude them by design (a
guarding comment says so), `VerifySuperchargerSession`'s SET clause excludes them by
design (a second guarding comment says so), and `SessionMirror`/`SessionVerifier`
have no field to bind one to even by mistake — the RM29/RM31 "protection by compile
error" pattern. The estimator that was meant to eventually fill them,
`derivedStartBatteryPct`, shipped in `charging-add-derived-start-battery-pct`
(2026-09-01) writing the real `start_battery_pct` column instead of a frozen snapshot
column. The two columns are now permanently dead schema (roadmap D1).

## What Changes

- **New migration**
  `internal/charging/db/migrations/20260903000002_drop_supercharger_est_columns.sql`
  — `ALTER TABLE charging.supercharger_sessions DROP COLUMN start_battery_pct_est,
  DROP COLUMN end_battery_pct_est`. `DROP COLUMN` also drops each column's inline
  `CHECK` constraint and its `COMMENT ON COLUMN` — no separate statement needed
  (roadmap D2). Full SQL, rationale, index plan, and data-loss statement in
  design.md's "Database Design" section (this change trips the `database` design
  gate).
- **`internal/charging/charging.go`** — `Session` loses its
  `StartBatteryPctEst`/`EndBatteryPctEst` fields (was 20 fields, now 18); doc
  comments naming "the five ... columns" become "three", and `SessionVerifier`'s doc
  comment drops its now-meaningless "structurally unreachable" clause about the two
  `_est` columns (they no longer exist to be unreachable from).
- **`internal/charging/session_reader.go`** — `rowToSession` drops its two
  `StartBatteryPctEst: pgInt2ToIntPtr(...)` / `EndBatteryPctEst: pgInt2ToIntPtr(...)`
  mappings; the mapping-rules doc comment above it drops the two names.
- **`internal/charging/db/query.sql`** — the two guarding comments on
  `MirrorSuperchargerSession` and `VerifySuperchargerSession` that name
  `start_battery_pct_est`/`end_battery_pct_est` as excluded columns are rewritten:
  once the columns don't exist, "excluded from the SET clause" is meaningless —
  there is nothing left to guard against. No SQL statement in this file changes
  (every query either omits these columns already or uses `SELECT *`, which
  shrinks automatically).
- **Regenerate `internal/charging/db/{models.go,query.sql.go}`** — run `make sqlc`
  (or `sqlc generate`) after the migration and `query.sql` edits land; these files
  are never hand-edited.
- **Repair four `charging` integration tests** —
  `db_session_integration_test.go`, `db_session_reader_integration_test.go`,
  `db_backfill_integration_test.go`, `db_session_verifier_integration_test.go` — see
  design.md's Test Contract for the exact per-file, per-line edits.
- **Repair the granted path `internal/analytics/db_integration_test.go`** (roadmap
  D6) — `seedChargeSession`'s direct `INSERT INTO charging.supercharger_sessions`
  drops the two `start_battery_pct_est, end_battery_pct_est` columns from its column
  list and the two `pgInt2FromIntPtr(s.StartBatteryPctEst)`/
  `pgInt2FromIntPtr(s.EndBatteryPctEst)` argument expressions (18 params → 16). No
  other file under `internal/analytics/` is touched.
- **`internal/charging/AGENTS.md`** — five locations describing the "five
  battery-percentage columns" / `SessionMirror`'s exclusion list / the `Session`
  struct code block / `SessionVerifier`'s doc comment / the Data Ownership
  column-by-column table are corrected to "three" and the two `_est` bullets are
  removed (`CLAUDE.md` "docs track structural change" — the module's own `AGENTS.md`
  is part of that rule, same as the root README). See design.md "Docs" for the exact
  before/after of each location.
- **`kkpa/context/use-case/charging/verify-session-battery.md`** — the gotcha
  bullet stating the est columns "await an estimator that does not exist yet" is
  corrected. The roadmap's own "Future work" section assigns this correction to
  this tier explicitly (`CLAUDE.md` docs-track-change rule).
- **`kkpa/context/workflows/supercharger-stats-read.md`** — one sentence tier 1
  left in place on purpose ("`charging.Session` still carries the two fields until
  tier 2 ... drops the columns") is now stale the moment this tier lands; corrected
  to the past tense.
- **`kkpa/context/architecture/charge-record-mutation.md`** — a bullet stating the
  est columns "are never written ... and always show `—`" is corrected. **This file
  was NOT in the roadmap's Findings table or its 7-file test count** — found by this
  worker while re-verifying the Findings before writing artifacts (see "Findings
  correction" below). It is a KB consumer-map fact about `charging`, so its
  correction belongs in this tier under the same docs-track-change rule as the other
  two KB files.
- **`openspec/specs/charge-session-log/spec.md`** — three existing Requirements
  ("Battery Percentage Verification On A Charge Session", "Charge Sessions Are
  Retrievable For A Vehicle Within A Time Window", "A Charge Session's Battery
  Percentages Are Correctable By A Human") each describe "an optional frozen pair of
  estimated percentages" as part of the currently-specified capability behavior —
  "these five values" becomes "these three values", the frozen-estimate clauses are
  removed from the requirement text and from four scenarios. See
  `specs/charge-session-log/spec.md` in this change and design.md "Spec delta" for
  the exact before/after of every paragraph and scenario touched. `openspec/specs/charging/spec.md`
  is NOT touched — its one `_est` mention is a historical fact about what the
  September rename migration renamed, unaffected by this drop (see design.md
  "Spec delta" for why).

**NOT in this change:**
- Anything under `internal/telemetry/` — `telemetry.supercharger_history` keeps its
  own copy of both columns until tier 3, `RM41-telemetry-drop-estimate-columns`
  (independent of this tier).
- Anything under `internal/gateway/` — tier 1, already archived.
- MAG-45 (session status column) — tiers 4-5 of this roadmap, not started.
- Any file under `internal/analytics/` other than `db_integration_test.go`.

## Findings correction (re-verified against the real code, not copied from the roadmap)

The roadmap's Findings table counted "7 test files reference the columns" from a
`grep -rn ... internal/ cmd/` — correctly scoped to code, so it could not have found
KB prose. Re-deriving the file list for this tier by reading the actual code and
grepping `kkpa/context/` (required by `CLAUDE.md`'s docs-track-change rule) surfaced
one additional file the roadmap never named:
`kkpa/context/architecture/charge-record-mutation.md`, lines 145-149. It describes
the two `_est` columns as "never written ... rendered as `StartBatteryPctEstLabel` /
`EndBatteryPctEstLabel` ... always show `—`" — a KB consumer-map fact this tier's
drop invalidates, exactly the shape `CLAUDE.md` warns about (the RM38/RM40
precedent it names). Corrected in this change; see "What Changes" above and
design.md.

Everything else in the roadmap's Findings table was re-confirmed exactly as stated:
the two guarding comments in `charging/db/query.sql` (lines 161 and 267 at the time
of writing), the four charging integration tests, and the single `analytics` INSERT
site. No other discrepancy found.

## Breaking

**No — externally.** No HTTP route or JSON shape changes. `charging.Session` loses
two fields, which IS a breaking change to that Go type, but its only two consumers
are `session_reader.go` (edited in this same change) and the gateway, which stopped
naming these fields in tier 1 — `grep -rln "StartBatteryPctEst\|EndBatteryPctEst"
internal/` outside this change's own edits returns only `internal/telemetry` (tier
3's concern) after this tier lands.

## Modules Affected

- **`internal/charging/`** — the primary module: the new migration, `charging.go`,
  `session_reader.go`, `db/query.sql`, the four integration tests, `AGENTS.md`.
- **`internal/analytics/`** — one file only, `db_integration_test.go`, an
  explicitly granted path (roadmap D6); nothing else under `internal/analytics/` is
  touched.
- **`kkpa/context/`** — three files (doc fixes only, not a code module):
  `use-case/charging/verify-session-battery.md`,
  `workflows/supercharger-stats-read.md`,
  `architecture/charge-record-mutation.md`.
- **`internal/telemetry/`** — not touched. `telemetry.supercharger_history` keeps
  both columns until tier 3.
- **`internal/gateway/`** — not touched (tier 1, already archived).

## Read Paths Affected — performance-sensitive, per `openspec/config.yaml`'s proposal rule

None new. `ListSessionsByVehicleBetween`, `ListSessionsByVehicleUpdatedSince`, and
`ListSessionsByVehicle` all read via `SELECT *` against
`idx_supercharger_sessions_vehicle_stop` — dropping two columns shrinks the row width
returned by that same index scan; it adds no read, changes no predicate, and needs
no new index (Performance-Profile: read-heavy — see design.md "Index Plan" for the
full justification of "no index change").

## Capabilities

### Modified Capabilities

- **Battery Percentage Verification On A Charge Session (`charge-session-log`)** —
  a charge session record now carries three verification values (verified start
  percentage, verified end percentage, provenance), not five; the frozen estimate
  pair is removed from the capability entirely. See
  `specs/charge-session-log/spec.md`.
- **Charge Sessions Are Retrievable For A Vehicle Within A Time Window
  (`charge-session-log`)** — a retrieved record no longer carries a frozen estimate
  pair. See `specs/charge-session-log/spec.md`.
- **A Charge Session's Battery Percentages Are Correctable By A Human
  (`charge-session-log`)** — the "other facts a correction never touches" list drops
  the frozen estimate pair (there is no longer one to leave untouched). See
  `specs/charge-session-log/spec.md`.

### Out of scope (explicitly deferred)

- **Dropping the same two columns from `telemetry.supercharger_history`** — tier 3,
  `RM41-telemetry-drop-estimate-columns` (independent of this tier).
- **MAG-45's session status column** — tiers 4-5, not started.

## Testing

Per the Test-Execution-Policy: the implementing worker writes/repairs tests and runs
`go build ./...`, `go vet ./...`, `gofmt -l`, `make build`, `make vet`, `make bins`,
and the standalone guards `make migration-guard`/`make boundary-guard` (this
change's own relevant guards — no UI, i18n, money, or time-zone concern is
touched) — never `go test ./...`, `make test`, `make test-with-db`, or `make check`.
The owner runs `go test ./internal/charging/... ./internal/analytics/...` and
reports results; until then this tier's implementation status is
**awaiting-user-verification**, never "done." See design.md "Test Contract" for the
concrete per-file edits authored before implementation.

**Owner-only verification (post-`make migrate-up`):** confirm the migration applies
cleanly against the real database and that `SELECT column_name FROM
information_schema.columns WHERE table_schema = 'charging' AND table_name =
'supercharger_sessions' AND column_name IN ('start_battery_pct_est',
'end_battery_pct_est')` returns zero rows. See tasks.md's final wave.

## Resolved decisions

Roadmap D1 (drop directly, no verify-then-drop — THIS tier executes it), D2 (one
migration per module, timestamp sorts after the newest existing migration — THIS
tier's migration), D3 (tier order — this tier depends on tier 1, already satisfied;
restated only for context), D6 (the `analytics` integration-test seed is THIS tier's
granted path — implemented here), D7 (no new unit tests; existing tests repaired —
THIS tier's five test-file reworks) are all carried from the roadmap and not
re-litigated here. This design's own local decisions (see design.md) cover only what
the roadmap left to the implementer: the exact migration SQL, the exact guarding-
comment rewrites, and the repaired tests' concrete expected values.

**This tier trips the `database` design gate** (`openspec/config.yaml` — "any
new/changed database object ... MUST include the full schema, the rationale ...
and an index plan"; `CLAUDE.md` Pipeline config `Design-Gates: database`). See
design.md "Database Design" for the full gate content — migration SQL up/down,
rationale, index plan, and data-loss statement. This gate blocks implementation
until the owner explicitly confirms the design.
