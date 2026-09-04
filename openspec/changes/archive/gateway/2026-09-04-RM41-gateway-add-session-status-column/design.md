# Design — RM41-gateway-add-session-status-column

Tier 5 of `RM41-supercharger-battery-pct-cleanup` (MAG-45). Renders the lifecycle
`Status` tier 4 (`RM41-charging-add-session-status`, archived) added to
`charging.Session` / `charging.supercharger_sessions`. Read-only against
`charging` — no database object, migration, or `charging` code changes here.

## Confirmed inputs (read from disk, not assumed)

- `charging.SessionStatus` (`internal/charging/charging.go:283-307`) — three
  constants: `SessionStatusInProgress = "IN_PROGRESS"`,
  `SessionStatusDoneCalculated = "DONE_CALCULATED"`, `SessionStatusDone = "DONE"`.
- `charging.Session.Status SessionStatus` (`charging.go:351-355`) — "ALWAYS
  COMPUTED by `SessionVerifier.VerifySession`... no port accepts a `Session` as
  input for this field."
- `charging.SessionReader.ListSessionsByVehicleBetween` — its query is `SELECT *`
  (confirmed via `internal/gateway/handlers/supercharger.go`'s existing
  `superchargerRowVMFromSession` mapper, which already reads other `Session`
  fields with no port signature change across tier 4). `Status` is already present
  on every `Session` the gateway's existing single read returns — **no gateway
  wiring change was needed to make the field reachable**, confirming the roadmap's
  own premise ("tier 5 depends on tier 4... the badge cannot read a field that
  does not exist yet" — it now does).
- `fragments.ChargeEntryVM.RawStatus string` (`charges_vm.go:41`) — the mirror
  target: "the raw ... RawStatus is the raw [status code]." Set by
  `RawStatus: string(e.Status)` in `charges.go:1028`.
- `fragments.charge_row.templ`'s `ChargeRow` — the gold standard: Status is the
  **2nd** `<td>`, right after the date `<td>`; the badge Kind/label is picked by
  an `if vm.RawStatus == "DONE" { primary } else { ghost }` branch **inline in the
  template**, not precomputed in Go. This design mirrors that shape exactly
  (three-way instead of two-way), so the VM stays a thin `RawStatus string` field
  and the Kind/label mapping decision lives in the `.templ` file, matching
  `charge_row.templ`'s own division of labor.
- `ui.Badge` / `badgeClass` (`templates/ui/badge.templ`, `templates/ui/ui.go:79-97`)
  — `Kind` accepts `"primary"|"success"|"warning"|"error"|"ghost"` and unknown
  values default to `"neutral"` (`badge-neutral`). `"neutral"` is a real, already
  wired Kind — no `ui/` kit change is needed for this design's mapping.
- `SuperchargerRow`'s 7 `<td>`s (Date, Site, Energy, Cost, StartBattery,
  EndBattery, Actions) and `SuperchargerRowError`'s `colspan="7"`
  (`supercharger_row.templ`).
- `SuperchargerRowEdit`'s `<td colspan="7">` (`supercharger_row_edit.templ:42`) —
  **same full-row-span shape as `SuperchargerRowError`, and it was NOT named in
  the dispatch's colspan callout.** See "Finding: a second colspan" below.
- `superchargerTable`'s 7-entry `Headers` slice (`supercharger_stats.templ`) and
  `SuperchargerStatsContent`'s existing single `ui.Alert{Kind:"info", Class:"mb-4"}`
  (the `KeySuperchargerBatteryPctHelp` alert, tier 1's work) as the template's
  first rendered element.
- `handlers/supercharger_test.go` — every assertion against `SuperchargerRowVM` /
  `charging.Session` fixtures is field-by-field (`rows[0].StartBatteryPctLabel !=
  "40%"`, etc.); `grep -n "DeepEqual\|reflect\." handlers/supercharger_test.go`
  returns nothing. Adding one field to `SuperchargerRowVM` and one to nothing on
  `charging.Session` (it already has `Status`) cannot break compilation via a
  struct-literal shape mismatch here.

## Badge Kind mapping (owner decision, 2026-09-04)

| `charging.SessionStatus` | `RawStatus` string | Badge `Kind` | Label key |
|---|---|---|---|
| `SessionStatusDone` | `"DONE"` | `"primary"` | `KeySuperchargerBadgeDone` |
| `SessionStatusDoneCalculated` | `"DONE_CALCULATED"` | `"neutral"` | `KeySuperchargerBadgeDoneCalculated` |
| `SessionStatusInProgress` (or any other/empty value) | `"IN_PROGRESS"` (or `""`) | `"ghost"` | `KeySuperchargerBadgeInProgress` |

**The IN_PROGRESS branch is the `else` catch-all, not a third `if`.** In
production `Status` is always one of the three constants (tier 4's migration
backfills every pre-existing row and defaults every new one). Test fixtures that
construct a bare `charging.Session{}` leave `Status` at its Go zero value (`""`),
and this design's template renders that exactly like `IN_PROGRESS` — a `ghost`
badge, `KeySuperchargerBadgeInProgress`'s label — rather than a fourth undefined
visual state. This is the same shape `charge_row.templ` already uses (its `else`
branch is reached by every `RawStatus` value that is not literally `"DONE"`,
including a fixture's zero value), so it introduces no new pattern.

**Never `success`/`warning`** — carried verbatim from roadmap D14 and the dispatch;
`primary`/`neutral`/`ghost` are the three Kinds used here, matching `charge_row.templ`'s
own restriction to `primary`/`ghost` (extended by one Kind because this column has
three states, not two).

## i18n keys (all appended to the existing `supercharger` groups in `catalog.go`)

Constants — inserted immediately after `KeySuperchargerDate` (header) and
immediately after `KeySuperchargerRowCancel` (badge group, its own new
`--- supercharger status badge ---` comment block, mirroring `charges_badge`'s
existing block shape) and after `KeySuperchargerBatteryPctHelp` (help alert):

```go
// --- supercharger status column header (fragments/supercharger_stats.templ) ---
KeySuperchargerStatus Key = "supercharger.status"

// --- supercharger status badge (fragments/supercharger_row.templ),
// RM41-gateway-add-session-status-column design.md, roadmap D11 — deliberately
// NOT reusing KeyChargesBadgeInProgress/KeyChargesBadgeDone: a different table's
// badge is its own semantic role, same precedent as charges_list's header block ---
KeySuperchargerBadgeInProgress     Key = "supercharger_badge.in_progress"
KeySuperchargerBadgeDoneCalculated Key = "supercharger_badge.done_calculated"
KeySuperchargerBadgeDone           Key = "supercharger_badge.done"

// --- supercharger in-progress-session guidance alert
// (fragments/supercharger_stats.templ), owner decision 2026-09-04 ---
KeySuperchargerStatusHelp Key = "supercharger.status_help"
```

Catalogue entries (verbatim — labels copied from roadmap D11, help text is the
owner's approved 2026-09-04 copy from the dispatch, unedited):

```go
KeySuperchargerStatus: {ES: "Estado", EN: "Status"},

KeySuperchargerBadgeInProgress:     {ES: "En progreso", EN: "In progress"},
KeySuperchargerBadgeDoneCalculated: {ES: "Finalizada (calculada)", EN: "Done (calculated)"},
KeySuperchargerBadgeDone:           {ES: "Finalizada", EN: "Done"},

KeySuperchargerStatusHelp: {
	ES: "Si una sesión aparece como «En progreso», te falta registrar sus porcentajes. Edítala y completa el porcentaje final para que el sistema pueda calcular el inicial.",
	EN: `If a session shows as "In progress", its percentages are still missing. Edit it and fill in the end percentage so the system can calculate the start one.`,
},
```

`TestCatalog_AllKeysHaveBothLanguages` (`i18n/catalog_test.go`) already iterates
every key in the `catalog` map — these five need no new test, the existing
completeness test covers them automatically the moment they land.

## View model change

`SuperchargerRowVM` (`supercharger_vm.go`) gains exactly one field:

```go
// RawStatus is the raw charging.SessionStatus code ("IN_PROGRESS" |
// "DONE_CALCULATED" | "DONE"), mirroring ChargeEntryVM.RawStatus's identical
// convention exactly — the template resolves the Badge Kind + label from this
// code (see SuperchargerRow's doc comment), not a precomputed label field.
RawStatus string
```

## Handler change

`superchargerRowVMFromSession` (`handlers/supercharger.go`) gains exactly one
assignment inside the existing `return fragments.SuperchargerRowVM{...}` literal:

```go
RawStatus: string(s.Status),
```

No other line in `handlers/supercharger.go` changes. `buildSuperchargerRows`,
`buildSuperchargerTiles`, `buildSuperchargerChart`, `fetchSuperchargerRowVM`, and
every write-path handler (`SuperchargerRowUpdate`, etc.) are untouched — they
already funnel through this one mapper (design.md D11 of
`RM31-gateway-add-session-battery-edit`, restated: "the ONE place that formats
these fields").

## Template changes

### `supercharger_row.templ` — `SuperchargerRow`

Insert a Status `<td>` as the row's 2nd cell (right after Date), mirroring
`ChargeRow`'s Status cell shape (badge only — no `ui.Dot`, see proposal.md "NOT in
this change" for why):

```templ
<td>{ vm.DateLabel }</td>
<td>
	if vm.RawStatus == "DONE" {
		@ui.Badge(ui.BadgeProps{Kind: "primary", Text: i18n.T(ctx, i18n.KeySuperchargerBadgeDone)})
	} else if vm.RawStatus == "DONE_CALCULATED" {
		@ui.Badge(ui.BadgeProps{Kind: "neutral", Text: i18n.T(ctx, i18n.KeySuperchargerBadgeDoneCalculated)})
	} else {
		@ui.Badge(ui.BadgeProps{Kind: "ghost", Text: i18n.T(ctx, i18n.KeySuperchargerBadgeInProgress)})
	}
</td>
<td>{ vm.SiteLabel }</td>
... (Energy, Cost, StartBattery, EndBattery, Actions unchanged)
```

`SuperchargerRowError`'s `colspan` changes `"7"` → `"8"`; its doc comment changes
"6 data cells + Actions" → "7 data cells + Actions".

### `supercharger_row_edit.templ` — `SuperchargerRowEdit`

**Finding: a second colspan.** The dispatch's own "do not miss this" callout named
only `SuperchargerRowError`'s `colspan="7"`. Reading the file (required doc-pack
step, done above) surfaced a SECOND full-row-span cell at line 42:
`<td colspan="7">` — the entire inline edit form (date/site/energy/cost read-only
fields plus the two editable battery-percentage inputs) lives inside one `<td>`
spanning the row, exactly like the error row's single cell. If only
`SuperchargerRowError` is updated, the edit row renders one column narrower than
the (now 8-column) header/static rows the moment a user clicks Edit — a rendering
bug the dispatch's explicit list would not have caught. This design updates BOTH:
`supercharger_row_edit.templ`'s `<td colspan="7">` also becomes
`<td colspan="8">`. No other line in this file changes — the edit row itself gains
no Status field (design.md D10/tier 4: `Status` is "ALWAYS COMPUTED," never
settable through any port, so there is nothing to make editable here, consistent
with the file's existing "only TWO fields are editable" doc comment).

### `supercharger_stats.templ`

`superchargerTable`'s `ui.Table{Headers: []string{...}}` gains
`i18n.T(ctx, i18n.KeySuperchargerStatus)` as the 2nd entry:

```go
Headers: []string{
	i18n.T(ctx, i18n.KeySuperchargerDate),
	i18n.T(ctx, i18n.KeySuperchargerStatus),
	i18n.T(ctx, i18n.KeySuperchargerSite),
	i18n.T(ctx, i18n.KeySuperchargerEnergy),
	i18n.T(ctx, i18n.KeySuperchargerCost),
	i18n.T(ctx, i18n.KeySuperchargerStartBattery),
	i18n.T(ctx, i18n.KeySuperchargerEndBattery),
	i18n.T(ctx, i18n.KeySuperchargerActions),
}
```

`SuperchargerStatsContent` gains a second `ui.Alert{Kind: "info", Class: "mb-4"}`
immediately below the existing `KeySuperchargerBatteryPctHelp` alert, rendering
`KeySuperchargerStatusHelp`:

```templ
@ui.Alert(ui.AlertProps{Kind: "info", Class: "mb-4"}) {
	{ i18n.T(ctx, i18n.KeySuperchargerBatteryPctHelp) }
}
@ui.Alert(ui.AlertProps{Kind: "info", Class: "mb-4"}) {
	{ i18n.T(ctx, i18n.KeySuperchargerStatusHelp) }
}
if v.Presets != nil {
	@superchargerMonthsSelector(v.Presets)
}
...
```

**Both alerts render unconditionally**, mirroring the first alert's existing
unconditional placement (roadmap D4: "the alert SHALL render unconditionally...
its presence SHALL NOT depend on whether the current window has any sessions").
The dispatch's "second recommendation" instruction gives no conditional-rendering
requirement (e.g. "only when an IN_PROGRESS session exists in the window"), and
adding one would require a new per-render scan of `v.Sessions` inside the
template — forbidden by "no business logic in templates" — or a new field on
`SuperchargerStatsView` computed in the handler, which is a bigger surface change
than the dispatch describes. Unconditional is simplest, consistent with the
existing alert, and correctly informative even on an empty-state render (a user
who filtered to a window with zero sessions still benefits from knowing what an
`IN_PROGRESS` badge would mean if they widened the window).

## Test Contract (authored before implementation, per `ai/go-conventions.md`
## "author expected values first")

No NEW test functions are required (roadmap's standing D7 default: no new unit
tests). The following are the expected values ANY test — new or existing —
touching this surface MUST produce, so a worker who later verifies or repairs
`supercharger_test.go` has a single source of truth to check against rather than
inferring correctness from the implementation:

1. **`superchargerRowVMFromSession` with `Status: charging.SessionStatusDone`** →
   `rows[0].RawStatus == "DONE"`.
2. **`superchargerRowVMFromSession` with `Status: charging.SessionStatusDoneCalculated`**
   → `rows[0].RawStatus == "DONE_CALCULATED"`.
3. **`superchargerRowVMFromSession` with `Status: charging.SessionStatusInProgress`**
   → `rows[0].RawStatus == "IN_PROGRESS"`.
4. **`superchargerRowVMFromSession` with a zero-value `charging.Session{}`
   (`Status` unset)** → `rows[0].RawStatus == ""`, and rendering that VM through
   `SuperchargerRow`/`SuperchargerStatsContent` produces a `badge-ghost` element
   containing the Spanish text `"En progreso"` (or `"In progress"` under the `en`
   language context) — the same visual result as case 3, per the mapping table's
   `else` branch.
5. **Rendering `SuperchargerRow` for a `DONE` row** → response body contains
   `badge-primary` and the ES string `"Finalizada"` (not `"Finalizada (calculada)"`).
6. **Rendering `SuperchargerRow` for a `DONE_CALCULATED` row** → response body
   contains `badge-neutral` and the ES string `"Finalizada (calculada)"`.
7. **Rendering `SuperchargerRow` for an `IN_PROGRESS` row** → response body
   contains `badge-ghost` and the ES string `"En progreso"`, and does NOT contain
   `badge-primary` or `badge-neutral` for that same row.
8. **`superchargerTable`'s rendered headers, in order** — `Fecha, Estado, Sitio,
   Energía, Costo, Batería inicial, Batería final, Acciones` (ES) /
   `Date, Status, Site, Energy, Cost, Start battery, End battery, Actions` (EN).
   `KeySuperchargerStatus` is the 2nd header, matching the 2nd `<td>`.
9. **`SuperchargerRowError`'s rendered `<td>`** — `colspan="8"`, never `"7"`.
10. **`SuperchargerRowEdit`'s rendered `<td>`** — `colspan="8"`, never `"7"` (the
    second-colspan finding above).
11. **`SuperchargerStatsContent`'s rendered body, in document order** — the
    `KeySuperchargerBatteryPctHelp` alert's text appears BEFORE the
    `KeySuperchargerStatusHelp` alert's text, which appears BEFORE either the
    month-preset selector or the empty-state paragraph.
12. **Existing behavior UNCHANGED** — `TestSuperchargerStatsFragment_RendersBatteryHeadersAndValues`
    and `TestSuperchargerStatsContent_RendersEnglishHeadersAndNilBatteryValues`'s
    existing assertions (battery labels, "—" counts, English headers, absence of
    "País") stay true verbatim; this design adds a column, it does not remove or
    reformat any existing cell.

If, at implementation time, any existing test in `handlers/supercharger_test.go`
fails to compile or produces a value contradicting items 1–12 above, that is a
repair task scoped to this design's Test Contract — never a silent change to the
contract itself.

## Docs — `kkpa/context/` grep result and disposition

`grep -rln "supercharger" kkpa/context/` surfaces
`kkpa/context/workflows/supercharger-stats-read.md` as the guide whose component
map and "how to add a column" sections describe this exact surface. Three
findings, all fixed in this same change (`CLAUDE.md` docs-track-change rule):

1. **Component map row for `supercharger_vm.go`** (currently: "`SuperchargerStatsView`
   (adds `CSRFToken`/`WindowStartStr`/`WindowEndStr`, RM31) and `SuperchargerRowVM`
   (adds `ID`/`RawStartBatteryPct`/`RawEndBatteryPct`, RM31)") — append
   ", and `RawStatus` (RM41 tier 5)" to the `SuperchargerRowVM` clause.
2. **Glossary intro sentence** (line 15, "Extended by RM31 tier 4: ... the session
   table carries the four `charging.Session` battery-percentage columns.") —
   append one more sentence: `"**Extended by RM41 tier 5**
   (`RM41-gateway-add-session-status-column`, MAG-45): the session table also
   carries a Status badge column (`ui.Badge`, mirroring `/charges`'s status
   column) rendering `charging.Session.Status` — `IN_PROGRESS`/`DONE_CALCULATED`/`DONE`
   — computed entirely by `charging.SessionVerifier.VerifySession`; the gateway
   only reads and renders it, adding no new read."`
3. **New bullet under "Conventions & gotchas"**, placed immediately after the
   existing "There is no longer an estimate column in the gateway" bullet (same
   paragraph family — both describe the sessions table's column set):
   `"**The session table's 2nd column is a Status badge, mirroring `/charges`.**
   `ui.Badge` Kind is `primary` for `DONE`, `neutral` for `DONE_CALCULATED`,
   `ghost` for `IN_PROGRESS` — never `success`/`warning` (same restriction
   `charge_row.templ` follows). The badge adds no read: `Status` is already
   present on every `charging.Session` the existing `ListSessionsByVehicleBetween`
   call returns. A second page-level `ui.Alert{Kind:"info"}`
   (`supercharger.status_help`) tells the user to fill in an `IN_PROGRESS`
   session's end percentage. _Source: spec gateway — Requirement: Supercharger
   Stats session status badge._"`

No other `kkpa/context/` file (`input-port/charging/supercharger-stats.md`,
`use-case/charging/verify-session-battery.md`) needs a change: the input-port
file's front-end component map is file-level, not field-level, and unaffected;
the use-case file already documents `status` as tier 4's write-path SET target
(added by tier 4's own change) and this tier adds no write.

## Local decisions (this design's own — everything else is carried from the roadmap)

- **D1 — Badge Kind/label resolution lives in the `.templ` file, not a precomputed
  VM field.** Matches `charge_row.templ`'s existing division of labor exactly (see
  "Confirmed inputs" above) — adding a `StatusLabel`/`StatusKind` field to the VM
  would duplicate logic already expressible as one `if/else if/else` in the
  template, at the cost of a second place that has to agree with the mapping
  table. Rejected: precomputing both a Kind and a label string in
  `superchargerRowVMFromSession` — bigger diff, no behavioral difference, and it
  would make `charge_row.templ` and `supercharger_row.templ` diverge in shape for
  no reason two rows away from each other in the same package.
- **D2 — the unset/zero-value `Status` case renders as `IN_PROGRESS`, not a fourth
  state.** See the Badge Kind mapping table's note above; this keeps existing bare
  `charging.Session{}` test fixtures rendering a defined, correct-by-construction
  badge instead of an unstyled "neutral" fallback that would misrepresent an
  actually-in-progress session as something else.
- **D3 — both guidance alerts render unconditionally.** See "Template changes"
  above for the full rationale (no new per-render session scan, no new handler
  field, matches the existing alert's own unconditional placement).
- **D4 — the second colspan (`SuperchargerRowEdit`) is fixed alongside the one the
  dispatch named.** See "Finding: a second colspan" above.

**No database design gate applies to this tier.** No table, column, index,
constraint, view, or migration is touched.
