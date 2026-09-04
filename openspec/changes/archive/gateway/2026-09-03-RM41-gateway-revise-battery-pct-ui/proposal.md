Source: MAG-36 — https://linear.app/magus-monitor/issue/MAG-36/supercharger-session-battery-start-calculated
Roadmap: openspec/roadmaps/RM41-supercharger-battery-pct-cleanup.md
Tier: 1 of 3 (`gateway`; no dependency — the gateway must stop naming `BatteryPctEst`
before tier 2 (`charging`) can drop the columns it currently reads)
Unit tests: excluded (new); existing tests repaired where this change breaks them
(roadmap D7). The owner's standing default is no new unit tests.
`handlers/supercharger_test.go` stops compiling/asserting correctly the moment
`SuperchargerRowVM` loses its two `*Est*` fields and the rendered table drops two
columns; reworking those fixtures/assertions is repair, not new coverage. design.md
authors the exact expected values these repaired fixtures must produce, up front,
per `ai/go-conventions.md`'s "author expected values first" rule.

## Why

`RM41-supercharger-battery-pct-cleanup` (roadmap, read in full before this proposal)
finishes MAG-36's remaining two parts. This tier does the first part end to end and
sets up the second: `/supercharger-stats` gains one bilingual info alert telling the
user Tesla does not supply battery percentages and that supplying only the end
percentage lets the system derive the start (roadmap D4/D5); and the gateway stops
rendering the two dead `start_battery_pct_est`/`end_battery_pct_est` columns
entirely — they have rendered `"—"` on every row since the columns shipped, nothing
in the repo writes them, and the estimator that was meant to fill them
(`derivedStartBatteryPct`) landed in `charging-add-derived-start-battery-pct`
writing the real `start_battery_pct` column instead, not a snapshot column
(roadmap "What is already done").

The roadmap's own investigation (its findings table) established the gateway is the
correct FIRST tier: `handlers/supercharger.go` is the only reader of
`charging.Session.StartBatteryPctEst`/`EndBatteryPctEst` outside the `charging`
module itself, so `charging` cannot drop the two columns (tier 2) while the gateway
still names the fields backing them (roadmap D3). This tier removes every such
name from `internal/gateway/` — the acceptance bar is `grep -rn
"BatteryPctEst\|StartEstimate\|EndEstimate" internal/gateway/` returning nothing,
test files included.

## What Changes

- **`internal/gateway/i18n/catalog.go`** — add one new key,
  `KeySuperchargerBatteryPctHelp = "supercharger.battery_pct_help"`, with the
  owner's own corrected copy in both `ES` and `EN` (roadmap D5, verbatim, see
  design.md D2 for the exact strings). Remove
  `KeySuperchargerStartEstimate`/`KeySuperchargerEndEstimate` (both the `Key`
  constants and their catalogue entries).
- **`internal/gateway/templates/fragments/supercharger_stats.templ`** —
  `SuperchargerStatsContent` renders one `ui.Alert{Kind: "info", Class: "mb-4"}`
  wrapping the new key, unconditionally, as the first element of the region (design
  decision D1 below — "above the sessions table" is satisfied by "above
  everything," the simplest render site, mirroring `dashboard.templ`'s existing
  page-level `ui.Alert` shape exactly, per roadmap D4). `superchargerTable`'s
  `ui.Table` headers drop the two `KeySuperchargerStartEstimate`/
  `KeySuperchargerEndEstimate` entries — the table goes from 9 columns to 7.
- **`internal/gateway/templates/fragments/supercharger_row.templ`** — `SuperchargerRow`
  drops its two `StartBatteryPctEstLabel`/`EndBatteryPctEstLabel` `<td>`s.
  `SuperchargerRowError`'s `colspan` changes from `9` to `7` (6 data cells + Actions,
  down from 8 + Actions), with its doc comment updated to match.
- **`internal/gateway/templates/fragments/supercharger_row_edit.templ`** —
  `SuperchargerRowEdit` drops its two read-only `ui.Field` blocks that displayed
  `StartBatteryPctEstLabel`/`EndBatteryPctEstLabel`. Its `<td colspan="9">` becomes
  `<td colspan="7">`.
- **`internal/gateway/templates/fragments/supercharger_vm.go`** — `SuperchargerRowVM`
  drops the `StartBatteryPctEstLabel`/`EndBatteryPctEstLabel` fields and their doc
  comments.
- **`internal/gateway/handlers/supercharger.go`** — `superchargerRowVMFromSession`
  drops its two `StartBatteryPctEstLabel: formatBatteryPct(s.StartBatteryPctEst)` /
  `EndBatteryPctEstLabel: formatBatteryPct(s.EndBatteryPctEst)` assignments.
  `charging.Session.StartBatteryPctEst`/`EndBatteryPctEst` themselves are untouched
  (owned by `charging`, dropped in tier 2) — this tier only stops *reading* them.
- **`internal/gateway/handlers/supercharger_test.go`** — repaired per design.md's
  Test Contract: every fixture/assertion naming `StartBatteryPctEstLabel`,
  `EndBatteryPctEstLabel`, `StartBatteryPctEst`, `EndBatteryPctEst`, `"Estimación
  inicial"`, `"Estimación final"`, `"Start estimate"`, or `"End estimate"` is
  updated so the file both compiles and matches the new rendered output — including
  removing `StartBatteryPctEst`/`EndBatteryPctEst` from `charging.Session{}`
  fixture literals even though `charging.Session` itself keeps those fields (tier
  2's job); a test fixture naming them would fail this tier's own acceptance grep.
- **Regenerate `*_templ.go`** — run `templ generate` (or `make templ`) after every
  `.templ` edit above; the generated files are never hand-edited.
- **`kkpa/context/workflows/supercharger-stats-read.md`** — two bullets describing
  the gateway's rendered table ("The four battery columns are…", "The two `_est`
  columns render `"—"` today…") go stale the moment the table renders two columns,
  not four. Corrected in this same change (`CLAUDE.md` "docs track structural
  change") — see design.md "Docs" for the exact replacement text. The **separate**
  gotcha in `kkpa/context/use-case/charging/verify-session-battery.md` ("await an
  estimator that does not exist yet") is a `charging`/write-path fact, not a
  gateway one; the roadmap explicitly assigns its correction to tier 2 — this
  change does NOT touch that file.

**NOT in this change:**
- Any database object, migration, `sqlc` regeneration, or change to
  `internal/charging` or `internal/telemetry` — those are tiers 2 and 3.
- `charging.Session.StartBatteryPctEst`/`EndBatteryPctEst` themselves, or any other
  field on that struct.
- The `kkpa/context/use-case/charging/verify-session-battery.md` gotcha (tier 2's
  own doc fix, per the roadmap).

## Breaking

**No — externally.** No HTTP route, request shape, or response shape changes in a
way any external caller depends on. `SuperchargerRowVM` loses two fields, which IS
a breaking change to that internal Go type — but its only two constructors
(`superchargerRowVMFromSession`, and the row templates that read it) are both
edited in this same change, and no code outside `internal/gateway/` references
`fragments.SuperchargerRowVM`.

## Modules Affected

- **`internal/gateway/`** — the only module whose files this worker edits:
  `i18n/catalog.go`, `templates/fragments/supercharger_stats.templ`,
  `templates/fragments/supercharger_row.templ`,
  `templates/fragments/supercharger_row_edit.templ`,
  `templates/fragments/supercharger_vm.go`, `handlers/supercharger.go`,
  `handlers/supercharger_test.go`, plus the regenerated `*_templ.go` siblings of
  the three edited `.templ` files.
- **`kkpa/context/`** — `workflows/supercharger-stats-read.md` only (doc fix, see
  above); not a code module.
- **`internal/charging/`** — read only (`charging.Session.StartBatteryPctEst`/
  `EndBatteryPctEst` still exist and this tier's handler code stops calling them);
  not written by this worker.

## Read Paths Affected — performance-sensitive, per `openspec/config.yaml`'s proposal rule

None. This tier adds and removes only presentation-layer formatting on data the
existing single `charging.SessionReader.ListSessionsByVehicleBetween` read already
returns — no new read, no changed read, no new index, no schema change
(Performance-Profile: read-heavy, unaffected).

## Capabilities

### Modified Capabilities

- **Supercharger Stats session table displays battery percentages (`gateway`)** —
  the table now displays TWO battery columns (start, end), not four; the two
  estimate columns and their catalogue keys are removed. See
  `specs/gateway/spec.md` for the full modified requirement.
- **Supercharger session battery percentages are correctable inline (`gateway`)** —
  the edit row's read-only display no longer includes the two estimate fields
  (there is nothing left to display read-only for them). The two editable inputs
  (`start_battery_pct`/`end_battery_pct`) and the rest of the write contract are
  unchanged.

### Added Capabilities

- **Supercharger Stats battery percentage guidance (`gateway`)** — a new,
  always-rendered bilingual info alert on `/supercharger-stats` explaining that
  Tesla does not supply battery percentages, that the user should try to record
  them, and that the system derives the start percentage from the end percentage
  when only the end is known. See `specs/gateway/spec.md` for the new requirement.

### Out of scope (explicitly deferred)

- **Dropping `start_battery_pct_est`/`end_battery_pct_est` from
  `charging.supercharger_sessions`** — tier 2, `RM41-charging-drop-estimate-columns`
  (depends on this tier).
- **Dropping the same two columns from `telemetry.supercharger_history`** — tier 3,
  `RM41-telemetry-drop-estimate-columns` (independent of tiers 1/2).
- **Correcting the `kkpa/context/use-case/charging/verify-session-battery.md`
  gotcha about the estimator** — tier 2's own doc fix (roadmap, "Future work").

## Testing

Per the Test-Execution-Policy: the implementing worker writes tests and runs `go
build ./...`, `go vet ./...`, `gofmt -l`, `make build`, `make vet`, `make bins`, and
the standalone guards `make i18n-guard`/`make ui-guard` (this change's own relevant
guards — no money, time-zone, migration, or boundary concern is touched) — never
`go test ./...`, `make test`, `make test-with-db`, or `make check`. The owner runs
`go test ./internal/gateway/...` and reports results; until then this tier's
implementation status is **awaiting-user-verification**, never "done." See
design.md "Test Contract" for the concrete fixtures and expected values authored
before implementation.

## Resolved decisions

Roadmap D1 (drop directly, no verify-then-drop — not this tier's concern, restated
only for context), D2 (one migration per module — not this tier's concern), D3
(tier order — THIS tier is the one real dependency edge: `charging` cannot drop the
columns while the gateway still names them), D4 (one page-level
`ui.Alert{Kind:"info"}` above the sessions table — THIS tier implements it), D5
(the exact bilingual copy — THIS tier implements it verbatim), D6 (the `analytics`
integration-test seed is tier 2's granted path, not this tier's), D7 (no new unit
tests; existing tests repaired — THIS tier's `supercharger_test.go` rework), D8 (the
RM27 reservation is now obsolete — not this tier's concern, restated only for
context), D9 (removing the two visible columns is confirmed, not a side effect —
THIS tier does it) are all carried verbatim from the roadmap and not re-litigated
here. This design's own local decisions (D1/D2 below) cover only what the roadmap
left to the implementer: exactly where the alert renders, and the repaired test
file's concrete expected values.

**No database design gate applies to this tier.** This change touches no database
object at all — no table, column, index, constraint, view, or migration.
