Source: MAG-45 — https://linear.app/magus-monitor/issue/MAG-45/supercharger-sessions-status-column-in-progress-done-calculated-done
Roadmap: openspec/roadmaps/RM41-supercharger-battery-pct-cleanup.md
Tier: 5 of 5 (`gateway`; depends on tier 4, `RM41-charging-add-session-status`,
archived — `charging.Session.Status` / `charging.SessionStatus` exist and already
flow through `SessionReader.ListSessionsByVehicleBetween`)
Unit tests: excluded (new) — roadmap's standing default (D7). Existing
`handlers/supercharger_test.go` assertions are field-by-field (no
`reflect.DeepEqual`/struct-literal comparison against `SuperchargerRowVM`), so
adding one field does not break compilation; no repair task is required, but the
worker MUST verify this at implementation time, not assume it (see tasks.md 4.1).

## Why

`RM41-supercharger-battery-pct-cleanup` (roadmap, read in full before this
proposal) tier 4 added a stored, auto-computed lifecycle `status` column to
`charging.supercharger_sessions` — three codes, `IN_PROGRESS` / `DONE_CALCULATED` /
`DONE` — and exposed it on `charging.Session.Status` (`charging.SessionStatus`).
Nothing reads or renders it yet. This tier (MAG-45, added mid-roadmap 2026-09-03)
closes that gap: the Supercharger Stats sessions table gains a Status badge column,
mirroring how `/charges` already renders its own lifecycle status (`charge_row.templ`),
plus a second bilingual alert telling the user how to resolve an `IN_PROGRESS`
session (fill in its end percentage).

This tier is READ-ONLY against `charging`: it consumes the `Status` field the
existing `SessionReader.ListSessionsByVehicleBetween` call already returns (that
port's query is `SELECT *`, so tier 4 needed no gateway-side wiring change to
surface the new column) and does no new read, write, or database-shape change of
its own.

## What Changes

- **`internal/gateway/i18n/catalog.go`** — five new keys, all ES+EN, all appended
  to the existing `supercharger` groups (design.md "i18n keys" has the exact
  strings and insertion points):
  - `KeySuperchargerStatus` — the new column header ("Estado"/"Status").
  - `KeySuperchargerBadgeInProgress` / `KeySuperchargerBadgeDoneCalculated` /
    `KeySuperchargerBadgeDone` — the three badge labels (roadmap D11, copied
    verbatim: "En progreso"/"In progress", "Finalizada (calculada)"/"Done
    (calculated)", "Finalizada"/"Done").
  - `KeySuperchargerStatusHelp` — the second guidance alert's copy (owner-approved
    2026-09-04, copied verbatim into design.md and this proposal's Decisions).
  Deliberately NOT reusing `KeyChargesBadgeInProgress`/`KeyChargesBadgeDone` — same
  "different capability, own catalogue entry" precedent the `charges_list` header
  block already documents in this file (a table column header or badge label is a
  distinct semantic role per surface, even when the English word matches).
- **`internal/gateway/templates/fragments/supercharger_vm.go`** — `SuperchargerRowVM`
  gains one new field, `RawStatus string`, mirroring `ChargeEntryVM.RawStatus`
  exactly (name, type, and the "raw code, not a pre-picked label" convention) — the
  template resolves the Kind+label mapping from this raw code, the same shape
  `charge_row.templ` uses.
- **`internal/gateway/handlers/supercharger.go`** — `superchargerRowVMFromSession`
  gains one new assignment, `RawStatus: string(s.Status)`. No other change to this
  function or to any other handler in the file — this is a pure read of a field the
  existing `charging.Session` result already carries.
- **`internal/gateway/templates/fragments/supercharger_row.templ`** —
  `SuperchargerRow` gains a Status `<td>` as the row's **2nd** cell (right after
  Date), rendering a `ui.Badge` whose Kind and Text are picked from `vm.RawStatus`
  (design.md "Badge Kind mapping" table). `SuperchargerRowError`'s `colspan`
  changes from `7` to `8` (7 data cells + Actions, up from 6 + Actions).
- **`internal/gateway/templates/fragments/supercharger_row_edit.templ`** —
  `SuperchargerRowEdit`'s `<td colspan="7">` ALSO changes to `colspan="8"`. **This
  is a finding beyond the dispatch's own list**: the edit row is a single
  full-width `<td>` spanning the entire row, exactly like `SuperchargerRowError`'s,
  and it was missed by the dispatch's explicit "don't miss this" callout, which
  named only the error row. Both colspans must move together or the edit row's
  form renders one column too narrow relative to the (now 8-column) header/static
  rows — see design.md "Finding: a second colspan.").
- **`internal/gateway/templates/fragments/supercharger_stats.templ`** —
  `superchargerTable`'s `ui.Table` `Headers` slice gains `KeySuperchargerStatus` as
  its **2nd** entry (Date, Status, Site, Energy, Cost, Start Battery, End Battery,
  Actions — 8 headers, up from 7). `SuperchargerStatsContent` gains a SECOND
  `ui.Alert{Kind: "info", Class: "mb-4"}` immediately below the existing
  `KeySuperchargerBatteryPctHelp` alert, rendering `KeySuperchargerStatusHelp`
  (owner decision, verbatim copy in design.md). Both alerts render unconditionally,
  mirroring the first alert's existing unconditional placement (roadmap D4) — no
  new conditional logic, no per-session lookup in the handler.
- **Regenerate `*_templ.go`** — run `make templ` after every `.templ` edit above;
  the generated files are never hand-edited.
- **`kkpa/context/`** — grepped for `supercharger` / gateway module references per
  `CLAUDE.md`'s docs-track-change rule; see design.md "Docs" for what was found and
  whether it needs a fix in this change.

**NOT in this change:**
- Any database object, migration, `sqlc` regeneration, or change to
  `internal/charging` — the `status` column, its computation, and its backfill are
  tier 4's, already archived and unmodified here. If anything about the column's
  shape or semantics were found to need changing, the correct response is to STOP
  and report it, not design a schema change in this tier (dispatch instruction).
- Any change to `SessionVerifier`, `SessionReader`'s method signature, or any other
  `charging` port. This tier reads a field the existing port already returns.
- A `ui.Dot` completeness indicator alongside the badge. `charge_row.templ` pairs a
  dot (form completeness) with a badge (lifecycle) because manual charge entries
  have a SEPARATE completeness signal (`vm.Complete`) the dot encodes; a
  Supercharger session has no equivalent separately-tracked completeness flag —
  its three-state `Status` already IS the complete signal (an `IN_PROGRESS` session
  is definitionally the incomplete one). Adding a dot here would be a second UI
  element encoding the same fact the badge already encodes, contradicting D14's own
  "mirrors `charge_row.templ` exactly" instruction about the badge itself, not
  about importing every element on that row regardless of fit.

## Breaking

**No — externally.** No HTTP route, request shape, or response shape changes.
`SuperchargerRowVM` gains a field (additive to a Go type nothing outside
`internal/gateway/` references). The rendered table gains a column, which is a
visible UI change but not a breaking one for any programmatic consumer — the
gateway's HTML fragments are not a versioned contract.

## Modules Affected

- **`internal/gateway/`** — the only module whose files this worker edits:
  `i18n/catalog.go`, `templates/fragments/supercharger_vm.go`,
  `templates/fragments/supercharger_row.templ`,
  `templates/fragments/supercharger_row_edit.templ`,
  `templates/fragments/supercharger_stats.templ`, `handlers/supercharger.go`,
  plus the regenerated `*_templ.go` siblings of the three edited `.templ` files.
- **`internal/charging/`** — read only (`charging.Session.Status` /
  `charging.SessionStatus` already exist, shipped by tier 4); not written by this
  worker. If this tier discovers the field's shape does not support what the
  design needs, that is reported back, not fixed here.
- **`kkpa/context/`** — grepped for staleness; see design.md "Docs" for the
  disposition (fixed here if anything is found, otherwise explicitly noted clean).

## Read Paths Affected — performance-sensitive, per `openspec/config.yaml`'s proposal rule

None. This tier adds only presentation-layer formatting on a field the existing
single `charging.SessionReader.ListSessionsByVehicleBetween` read already returns
(tier 4's migration added the column via `SELECT *`, so no gateway-side read
changed shape). No new read, no changed read, no new index, no schema change
(Performance-Profile: read-heavy, unaffected).

## Capabilities

### Modified Capabilities

- **Supercharger Stats session table displays battery percentages (`gateway`)** —
  the table gains an 8th column, Status, as the 2nd column (right after Date). See
  `specs/gateway/spec.md` for the full modified requirement.

### Added Capabilities

- **Supercharger Stats session status badge (`gateway`)** — a `ui.Badge` rendering
  one of three lifecycle labels per session, mirroring `/charges`'s own status
  column. See `specs/gateway/spec.md` for the new requirement.
- **Supercharger Stats in-progress session guidance (`gateway`)** — a second
  bilingual info alert telling the user to fill in the percentages of sessions
  still `IN_PROGRESS`. See `specs/gateway/spec.md` for the new requirement.

## Testing

Per the Test-Execution-Policy: the implementing worker writes tests (none new are
required by the roadmap's standing D7 default, but the worker verifies
`handlers/supercharger_test.go` still compiles and its existing assertions still
hold) and runs `go build ./...`, `go vet ./...`, `gofmt -l`, `make build`, `make
vet`, `make bins`, and the standalone guards `make i18n-guard`/`make ui-guard` —
never `go test ./...`, `make test`, `make test-with-db`, or `make check`. The owner
runs `go test ./internal/gateway/...` and reports results; until then this tier's
implementation status is **awaiting-user-verification**, never "done." See
design.md "Test Contract" for the concrete expected values authored before
implementation.

## Resolved decisions

Roadmap D10 (stored, auto-set column — tier 4's concern, restated only for
context), D11 (three states and their labels — THIS tier renders them verbatim),
D13 (split across tiers 4/5 by ownership — restated for context), D14 (badge
mirrors `/charges` exactly, 2nd column, `ui.Badge`, never success/warning Kinds —
THIS tier implements it) are carried verbatim from the roadmap. The Badge Kind
mapping (DONE→primary, DONE_CALCULATED→neutral, IN_PROGRESS→ghost) and the second
alert's placement/copy are the owner's own 2026-09-04 decisions, given verbatim in
the dispatch and restated in design.md — not re-litigated here.

**No database design gate applies to this tier.** This change touches no database
object at all — no table, column, index, constraint, view, or migration.
