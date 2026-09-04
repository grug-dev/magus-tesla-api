Source: MAG-36 — https://linear.app/magus-monitor/issue/MAG-36/supercharger-session-battery-start-calculated
Roadmap: openspec/roadmaps/RM41-supercharger-battery-pct-cleanup.md
Tier: 3 of 5 (`telemetry`; no `depends_on` in the roadmap's tier table — independent of
tiers 1 and 2, ordered last only for readability, per roadmap D3)
Unit tests: excluded (new); existing tests repaired where this change breaks them
(roadmap D7). The owner's standing default is no new unit tests. One telemetry
integration test file, plus the granted path `internal/charging/db_backfill_integration_test.go`,
stop compiling or fail at runtime the moment `telemetry.SuperchargerHistory` loses its
two `*Est` fields and `telemetry.supercharger_history` loses its two columns;
design.md authors the exact expected edits up front, per `ai/go-conventions.md`'s
"author expected values first" rule.

## Why

`RM41-supercharger-battery-pct-cleanup` (roadmap, read in full before this proposal)
finishes MAG-36's remaining scope. Tier 1 (`gateway`, archived) stopped the gateway
from reading either `*Est` field anywhere. Tier 2 (`charging`, archived) dropped
`start_battery_pct_est`/`end_battery_pct_est` from `charging.supercharger_sessions`.
This tier, tier 3, drops the **same two columns from telemetry's own, independent
copy** — `telemetry.supercharger_history` — closing the columns out everywhere in the
platform (roadmap Intention: "When this roadmap completes, `grep -rn
"battery_pct_est\|BatteryPctEst" internal/` returns only migration history").

`start_battery_pct_est`/`end_battery_pct_est` have been permanently `NULL` on
`telemetry.supercharger_history` since they shipped in
`RM27-telemetry-add-supercharger-battery-pct` (MAG-14, 2026-08-15) as a reserved,
write-once verification-time snapshot pair for a taper-curve SOC estimator RM27
originally planned. That estimator was **descoped by the owner the same day it was
proposed** (see the SCOPE NOTE in `internal/telemetry/AGENTS.md`) — no estimator,
no verification UI, and no Writer for either column has ever existed in this
repository. The columns were kept rather than dropped at the time specifically "so
that a future estimator can land without a migration" (`telemetry.go`'s RM27-D6
comment). That estimator eventually shipped as `derivedStartBatteryPct`
(`internal/charging/capacity.go`, 2026-09-01) — but it writes the real
`start_battery_pct` column, not a frozen snapshot. The reservation these two columns
existed for is now permanently obsolete (roadmap D1/D8).

## What Changes

- **New migration**
  `internal/telemetry/db/migrations/20260903000003_drop_supercharger_est_columns.sql`
  — `ALTER TABLE telemetry.supercharger_history DROP COLUMN start_battery_pct_est,
  DROP COLUMN end_battery_pct_est`. `DROP COLUMN` also drops each column's inline
  `CHECK` constraint and its `COMMENT ON COLUMN` — no separate statement needed
  (roadmap D2). Full SQL, rationale, index plan, and data-loss statement in
  design.md's "Database Design" section (this change trips the `database` design
  gate).
- **`internal/telemetry/telemetry.go`** — `SuperchargerHistory` loses its
  `StartBatteryPctEst`/`EndBatteryPctEst` fields and the entire "Reserved, currently
  unwritten (design D6)" comment block above them — this is the RM27-D6 reservation
  itself, now retired (roadmap D8). One trailing "(see below)" cross-reference in
  the surviving trio comment is corrected since there is no longer a block below it
  to point at.
- **`internal/telemetry/mapping.go`** — `rowToSuperchargerHistory` drops its two
  `StartBatteryPctEst: pgNullableInt16AsInt(...)` / `EndBatteryPctEst:
  pgNullableInt16AsInt(...)` mappings; the two doc comments above it (the mapping-rules
  bullet and the field-block comment) drop their references to the frozen snapshot
  pair.
- **`internal/telemetry/service.go`** — the stale comment on `upsertSuperchargerHistory`
  naming `s.StartBatteryPctEst`/`s.EndBatteryPctEst` as fields it deliberately does not
  read is rewritten: those fields no longer exist on `SuperchargerHistory`, so "does
  not read" no longer describes anything real.
- **`internal/telemetry/db/query.sql`** — the guarding comment on
  `UpsertSuperchargerHistory` that names `start_battery_pct_est`/`end_battery_pct_est`
  as excluded columns is rewritten: once the columns don't exist, "excluded from the
  SET clause" is meaningless — there is nothing left to guard against. No SQL
  statement in this file changes (all four Supercharger reads use `SELECT *`, which
  shrinks automatically).
- **Regenerate `internal/telemetry/db/{models.go,query.sql.go}`** — run `make sqlc`
  (or `sqlc generate`) after the migration and `query.sql` edits land; these files
  are never hand-edited.
- **Repair `internal/telemetry/db_supercharger_battery_pct_integration_test.go`** —
  the module's own battery-% integration test file. See design.md's Test Contract for
  the exact per-block edits, including one whole test function deleted outright
  (roadmap D7: "The two `telemetry` tests that assert the columns survive a
  re-UPSERT are deleted, not rewritten" — see design.md's Test Contract for why this
  resolves to one function, and the reasoning).
- **Repair the granted path `internal/charging/db_backfill_integration_test.go`**
  (explicitly granted by the leader's dispatch, mirroring roadmap D6's shape for
  tier 2) — `superchargerFixtureRow` drops its two `*Est` fields, and
  `insertSuperchargerSessionFixture`'s direct SQL `INSERT INTO
  telemetry.supercharger_history` drops the two `start_battery_pct_est,
  end_battery_pct_est` columns, their two `$19, $20` placeholders, and the two
  `r.StartBatteryPctEst, r.EndBatteryPctEst` args (20 params → 18). No other file
  under `internal/charging/` is touched. **This file was NOT named by the roadmap's
  tier 3 row** — the leader's dispatch found it and granted it explicitly; see
  "Findings correction" below.
- **`internal/telemetry/AGENTS.md`** — six locations correcting "five" to "three"
  (or removing the now-nonexistent pair entirely), matching tier 2's identical
  correction of `internal/charging/AGENTS.md`: the nightly-cycle intro sentence, the
  Data Ownership `supercharger_history` bullet's column-by-column list, the
  "Battery-% verification columns" section's field-count sentence, the SCOPE NOTE,
  the "Never auto-written (R3/R7)" bullet's three numeral substitutions, and the
  entire `StartBatteryPctEst`/`EndBatteryPctEst` RESERVED bullet (replaced with a
  short note recording the reversal, mirroring `telemetry.go`'s own D8 reversal
  note). See design.md "Docs" for the exact before/after of each
  location.
- **`internal/charging/AGENTS.md`** (granted path, one sentence) — the Data
  Ownership section's forward-looking sentence, written by tier 2, that says
  telemetry "keeps its own copy of the three remaining battery-percentage columns
  ... until tier 3 ... drops its own est-column pair — see that change once it
  lands," is now the past this sentence was written to describe. Corrected to past
  tense. Tier 2 deliberately left this forward reference for tier 3 to close, the
  same shape as tier 1's forward reference that tier 2 itself closed.
- **`internal/charging/db/migrations/20260823000001_add_charge_sessions.sql`**
  (granted path, comment-only — see "Findings correction" below) — this HISTORIC
  migration's own `-- +goose Down` comment explicitly names this exact future
  change and instructs it to update the comment: *"This stops being true once the
  deferred contract change drops telemetry's five percentage columns ... That
  change owns updating this comment; see design.md D9, step 6."* This tier is that
  change. The comment is corrected to reflect what actually happened — narrower
  than D9 anticipated — never its SQL, which is never touched (`ai/go-conventions.md`
  "historic migrations are never edited" governs the Up/Down statements; this is a
  text-only comment correction the file's own author pre-authorized for exactly
  this change).
- **`openspec/specs/telemetry/spec.md`** — two existing Requirements ("Supercharger
  Session Ledger", "Supercharger Session Read Port") each describe the frozen
  verification-time snapshot pair as current capability behavior, with two
  scenarios dedicated entirely to it and two more that reference it inside a
  broader scenario. See `specs/telemetry/spec.md` in this change and design.md
  "Spec delta" for the exact before/after of every paragraph and scenario touched.

**NOT in this change:**
- Anything under `internal/gateway/` — tier 1, already archived.
- Anything under `internal/charging/` other than the two explicitly granted paths
  named above (`db_backfill_integration_test.go`, one sentence in `AGENTS.md`, one
  comment in the historic `20260823000001` migration). `charging.supercharger_sessions`
  already lost its own `*Est` columns in tier 2; this tier does not touch that table.
- MAG-45 (session status column) — tiers 4-5 of this roadmap, not started.
- `openspec/specs/charging/spec.md` / `openspec/specs/charge-session-log/spec.md` —
  charging's own capability specs, already corrected by tier 2 (or, for
  `charging/spec.md`, deliberately left alone — its one `_est` mention is a
  historical fact, per tier 2's own design.md).

## Findings correction (re-verified against the real code, not copied from the roadmap)

The roadmap's tier 3 row lists only `telemetry.go`, `mapping.go`, `service.go`,
`db/query.sql`, and the module's own integration test. Re-deriving the file list for
this tier — grepping the whole repository (excluding `.claude/worktrees/`, which
`go build ./...` never touches) for `BatteryPctEst`/`battery_pct_est`, and grepping
`kkpa/context/` per `CLAUDE.md`'s docs-track-change rule — surfaced two things the
roadmap did not list, both already flagged by the leader's dispatch or discovered
independently:

1. **`internal/charging/db_backfill_integration_test.go`** (leader-flagged; confirmed
   by this worker) — `insertSuperchargerSessionFixture` INSERTs into
   `telemetry.supercharger_history` naming both columns this tier drops, as `$19,
   $20`. This is invisible to `go vet` (raw SQL inside a `_test.go` file — the exact
   gotcha `kkpa/context/pending-spec-to-sync/applied/2026-09-03-telemetry.md`
   documents: "No assistant-runnable signal catches an unqualified [or, here,
   dropped-column-naming] SQL statement there — only the suite does"). Confirmed by
   reading the file: none of the seeded fixture rows set either `*Est` field
   explicitly (Go zero-value nil for both), so dropping the two struct fields, the
   two INSERT columns, the two placeholders, and the two args is a pure mechanical
   edit with no fixture-value consequence.
2. **`internal/charging/db/migrations/20260823000001_add_charge_sessions.sql`**
   (found by this worker, NOT flagged by the roadmap or the dispatch) — this
   already-applied historic migration's own `-- +goose Down` comment names "the
   deferred contract change [that] drops telemetry's five percentage columns" and
   says "That change owns updating this comment; see design.md D9, step 6." Reading
   the referenced `RM29-charging-add-charge-sessions` design.md D9 confirms the
   plan it anticipated: a single future change dropping **all five** telemetry
   percentage columns at once and re-pointing `internal/analytics` off telemetry
   entirely. That never happened in that shape — RM31 already re-pointed analytics
   onto `charging.supercharger_sessions` independently, and RM41 (this roadmap)
   drops only the **two estimate columns**, split across tier 2 (charging) and this
   tier (telemetry), while the three-column trio stays in both tables permanently.
   The historic comment's specific claim — "charge_sessions is the only copy of
   [the five columns] and this Down becomes destructive" — does not hold for what
   actually happened: after this tier lands, neither table has the estimate pair,
   and the trio remains dual-stored exactly as the comment's original safety claim
   assumed. This tier corrects the comment's text only (never its SQL) to say so;
   see design.md "Docs" for the exact replacement. A separate, older staleness this
   worker also noticed while reading this file — the trio's safety claim has been
   imprecise since `RM31-charging-add-session-verification-port` gave `charging` a
   human write path onto its own trio that telemetry's copy never receives — is
   **not** corrected here: it predates RM41 entirely and is unrelated to this
   DROP; flagging it for the leader/owner rather than fixing it under this tier's
   database design gate.

A third file this worker checked and ruled OUT: `kkpa/context/architecture/nightly-cycle.md`
line 102 ("`MirrorSuperchargerSession` never names the five battery-percentage
columns") is stale — it should say "three" since tier 2 landed — but the staleness
is tier 2's gap (a charging-owned artifact, `charging.SessionMirror`/
`MirrorSuperchargerSession`, invalidated by tier 2's drop, not this tier's). Fixing
it here would be repairing a different tier's already-archived change under this
tier's design gate. Flagged for the leader; not actioned in this change.

## Breaking

**No — externally.** No HTTP route or JSON shape changes; `internal/gateway` never
imports `internal/telemetry` at all (`make boundary-guard`, RM40). `telemetry.SuperchargerHistory`
loses two fields, which IS a breaking change to that Go type, but its only consumers
after this change are `mapping.go` (edited in this same change) and the one granted
cross-module test fixture (also edited in this same change) — `grep -rln
"StartBatteryPctEst\|EndBatteryPctEst" internal/ --include=*.go` outside this
change's own edits returns nothing once this tier lands (roadmap's own completion
criterion).

## Modules Affected

- **`internal/telemetry/`** — the primary module: the new migration, `telemetry.go`,
  `mapping.go`, `service.go`, `db/query.sql`, the module's own integration test,
  `AGENTS.md`.
- **`internal/charging/`** — three explicitly granted paths only:
  `db_backfill_integration_test.go`, one sentence in `AGENTS.md`, one comment in
  the historic `20260823000001` migration. Nothing else under `internal/charging/`
  is touched.
- **`internal/gateway/`** — not touched (tier 1, already archived).
- **`internal/analytics/`** — not touched (tier 2's granted path; this tier has no
  reason to touch it — analytics does not read telemetry's `*Est` columns).

## Read Paths Affected — performance-sensitive, per `openspec/config.yaml`'s proposal rule

None new. `SuperchargerHistoryByAccount`, `SuperchargerHistoryByVehicle`,
`SuperchargerHistoryByVehicleBetween`, and `SuperchargerHistoryByVehicleUpdatedSince`
all read via `SELECT *` against one of `idx_supercharger_history_vehicle_time` or
`idx_supercharger_history_account_time` — dropping two columns shrinks the row width
returned by these already-indexed scans; it adds no read, changes no predicate, and
needs no new index (Performance-Profile: read-heavy — see design.md "Index Plan" for
the full justification of "no index change").

## Capabilities

### Modified Capabilities

- **Supercharger Session Ledger (`telemetry`)** — a stored session no longer carries a
  frozen verification-time snapshot pair; only the human-owned trio (start
  percentage, end percentage, source) remains, unchanged in shape. See
  `specs/telemetry/spec.md`.
- **Supercharger Session Read Port (`telemetry`)** — a retrieved session no longer
  carries a snapshot pair. See `specs/telemetry/spec.md`.

### Out of scope (explicitly deferred)

- MAG-45's session status column — tiers 4-5, not started.
- The pre-existing `kkpa/context/architecture/nightly-cycle.md` staleness (tier 2's
  gap, see "Findings correction" above) and the pre-existing trio-divergence
  staleness in the historic `20260823000001` Down comment (predates RM41, caused by
  RM31) — both flagged for the leader/owner, neither actioned here.

## Testing

Per the Test-Execution-Policy: the implementing worker writes/repairs tests and runs
`go build ./...`, `go vet ./...`, `gofmt -l`, `make build`, `make vet`, `make bins`,
and the standalone guards `make migration-guard`/`make boundary-guard` (this
change's own relevant guards — no UI, i18n, money, or time-zone concern is
touched) — never `go test ./...`, `make test`, `make test-with-db`, or `make check`.
The owner runs `go test ./internal/telemetry/... ./internal/charging/...` and
reports results; until then this tier's implementation status is
**awaiting-user-verification**, never "done." See design.md "Test Contract" for the
concrete per-file edits authored before implementation.

**Owner-only verification (post-`make migrate-up`):** confirm the migration applies
cleanly against the real database and that `SELECT column_name FROM
information_schema.columns WHERE table_schema = 'telemetry' AND table_name =
'supercharger_history' AND column_name IN ('start_battery_pct_est',
'end_battery_pct_est')` returns zero rows. See tasks.md's final wave.

## Resolved decisions

Roadmap D1 (drop directly, no verify-then-drop — THIS tier executes it for
telemetry's copy), D2 (one migration per module, timestamp sorts after the newest
existing migration — THIS tier's migration), D3 (no ordering constraint between
tiers 2 and 3; restated only for context), D7 (no new unit tests; existing tests
repaired, and the two re-UPSERT-survival telemetry tests are deleted outright — THIS
tier's test rework), D8 (this reverses RM27's design D6 — THIS tier's `telemetry.go`/
`AGENTS.md` edits) are all carried from the roadmap and not re-litigated here. This
design's own local decisions (see design.md) cover only what the roadmap left to the
implementer: the exact migration SQL, the exact guarding-comment rewrites, and the
repaired tests' concrete expected values.

**This tier trips the `database` design gate** (`openspec/config.yaml` — "any
new/changed database object ... MUST include the full schema, the rationale ...
and an index plan"; `CLAUDE.md` Pipeline config `Design-Gates: database`). See
design.md "Database Design" for the full gate content — migration SQL up/down,
rationale, index plan, and data-loss statement. This gate blocks implementation
until the owner explicitly confirms the design.
