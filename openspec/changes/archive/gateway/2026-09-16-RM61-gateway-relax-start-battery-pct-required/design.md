# Design — RM61-gateway-relax-start-battery-pct-required

## Context

Tier 1 (`RM61-charging-add-manual-entry-start-derivation`, archived 2026-09-16) gave
`internal/charging` everything it needs to fill in a missing starting battery percentage:

- `charging.Entry.StartBatteryPct *int` already accepts `nil`.
- `charging.Entry.StartBatterySource *StartBatterySource` (nil exactly when
  `StartBatteryPct` is nil) records whether the value came from the person
  (`charging.StartBatterySourceUser`) or from this module's own derivation
  (`charging.StartBatterySourceEstimated`).
- `Writer.Create` and `Writer.Update` both derive the percentage on write, whenever it
  arrives nil and the end percentage plus the resolved energy are both present.

None of that is reachable from `/external-charges` today, because the gateway itself
refuses to send a nil value. `handlers/external_charges.go`'s `parseExternalChargeForm`
rejects an empty `start_battery_pct` with a 422 before `charging.Writer` is ever called
(`:1403-1420`, verified against the current file — the roadmap's own line numbers matched
within a few lines), and both `.templ` forms carry `Required: true` on the input.

This change removes that gateway-side refusal and gives the edit form the one behavior
tier 1's design already assumes: RD3, that a *derived* stored value renders as a
`placeholder`, never as a filled `Value`, so an unedited re-submission arrives empty and
gets derived again — instead of coming back looking user-typed and permanently entering
`monthly_effective_capacity`'s pool of "real" measurements.

## Goals / Non-Goals

**Goals**
- `start_battery_pct` becomes optional on both `/external-charges` forms.
- The edit form shows a derived (`ESTIMATED`) stored value as a placeholder; a
  user-typed (`USER`) or absent value renders exactly as it does today.
- One new bilingual help line under the input explains the empty-means-computed rule.
- Every test currently asserting the old unconditional-required rule is corrected — a
  stale green test asserting last month's rule is worse than a missing one, per this
  module's own testing conventions.

**Non-Goals**
- No change to `internal/charging` — the derivation, the column, and the capacity-query
  exclusion are tier 1's finished work.
- No change to the create form's existing telemetry-suggestion placeholder
  (`StartBatteryPctSuggestion`, D2 of an earlier change) — that mechanism is unrelated to
  `StartBatterySource` and keeps working exactly as it does today.
- No new port, no new `Deps` field. `charging.Entry.StartBatterySource` already comes
  back on every `charging.Reader` read the gateway already makes.

## No Database Changes

This change touches no table, column, index, constraint, view, or migration. It edits two
`.templ` files, one handler file, one view-model file, and one `ui/` kit file, plus their
tests. `openspec/config.yaml` §design requires design.md for any DB-touching change; this
one is required by the dispatch prompt's own instruction, not by that trigger — recorded
here so a reader does not go looking for a migration that does not exist. The `database`
design gate (`CLAUDE.md` §Pipeline config → Design-Gates) does not apply, and no owner
confirmation of a schema is needed before implementation starts.

## Decisions

### D1 — `ui.FieldProps.Help`: a new kit prop, not an inlined `fieldset-label`

RD5 asks for "the field help under the input" — DaisyUI's own vocabulary for this is the
`fieldset-label` class, used inside a `fieldset` for a muted line after the control (the
same element family `Field` already owns: `fieldset`/`fieldset-legend`). Two ways to add
it:

| Option | Verdict |
|---|---|
| Inline `<p class="fieldset-label">` directly in the two `.templ` forms | Rejected — `fieldset-label` is a raw DaisyUI component class; inlining it in a page/fragment is exactly what `ui-guard` exists to catch, and what `ai/htmx-conventions.md` §Styling forbids regardless of the guard. |
| Add `Help string` to `ui.FieldProps`, rendered by `Field` itself | **Chosen.** `Field` already owns the one other per-field text slot (`Error`) and the one other hint (`Optional`'s "(optional)" suffix on the legend). A third, structurally identical slot belongs beside them, not duplicated ad hoc in a page. |

`Help` is deliberately independent of `Optional`: `Optional` marks the field as not
required, on the **label**; `Help` explains a rule about the field's **value**, under the
**input**. `start_battery_pct` uses both — `Optional: true` for the "(opcional)" suffix
(unconditionally true after this change, so the existing "only for an UNCONDITIONALLY
optional field" rule in `ui.FieldProps`'s own doc comment is satisfied) and `Help` for the
new sentence. A future field could use either alone.

`FieldProps` gains:

```go
// Help renders a muted line under the control (DaisyUI's fieldset-label
// class) explaining a rule about the field's VALUE — e.g. that an empty
// field will be computed instead of left blank. Distinct from Optional,
// which marks the field as not required, on the LABEL. A field may carry
// either, both, or neither. Empty renders nothing (mirrors Error).
Help string
```

`Field` renders it right after the control and before `Error`, so a validation error (rare)
still reads as the more urgent line:

```templ
{ children... }
if p.Help != "" {
    <p class="fieldset-label">{ p.Help }</p>
}
if p.Error != "" {
    <p class="text-error text-sm">{ p.Error }</p>
}
```

`fieldset-label` lives inside `templates/ui/field.templ` only — `ui-guard` scans
`templates/pages` and `templates/fragments`, never `templates/ui`, so this is the
sanctioned place for the class, not an exception to the rule.

### D2 — The new help copy (RD5, one bilingual key)

New catalogue key, both languages on the same line (catalogue convention, D1 of
`RM24-gateway-add-i18n-foundation`):

```go
KeyChargesFormStartBatteryPctHelp Key = "charges_form.start_battery_pct_help"
...
KeyChargesFormStartBatteryPctHelp: {ES: "Opcional. Déjalo vacío y lo calculamos con la energía y el % final.", EN: "Optional. Leave it empty and we calculate it from the energy and the end %."},
```

Used on **both** forms' `start_battery_pct` `ui.Field`, via `Help:
i18n.T(ctx, i18n.KeyChargesFormStartBatteryPctHelp)`.

### D3 — `ExternalChargeEntryVM.StartBatterySource` is a plain string, not `charging.StartBatterySource`

The view-model file's own package doc comment states: "View models are pure Go structs —
no charging.*, no pgtype.*, no vendor-suffixed types". `charging.StartBatterySource` is not
a vendor DTO, but it is still a domain type the VM must not import, exactly the reasoning
that already made `Status`/`RawStatus` plain strings instead of `charging.Status`. The new
field mirrors that precedent:

```go
// StartBatterySource is "USER" (the person typed the stored percentage),
// "ESTIMATED" (this entry's module-computed derivation), or "" when
// RawStartBatteryPct is itself empty. A plain string, not
// charging.StartBatterySource — see the package doc comment; Status/RawStatus
// already follow the same rule for charging.Status.
StartBatterySource string
```

Placed on `ExternalChargeEntryVM` next to `RawStartBatteryPct`, not next to `Status` — it
describes the same field's provenance, not the entry's lifecycle.

`externalChargeEntryVMFromEntry` (`handlers/external_charges.go`) maps it:

```go
startBatterySource := ""
if e.StartBatterySource != nil {
    startBatterySource = string(*e.StartBatterySource)
}
```

…and passes `StartBatterySource: startBatterySource` in the returned literal, next to
`RawStartBatteryPct`.

**`externalChargeEntryVMFromRawValues`** (the 4xx/5xx re-render builder, used only by the
edit row) sets no `StartBatterySource` — it has no persisted entry to read one from, so the
field stays `""` (Go's zero value), which the render rule below treats as "not ESTIMATED":
whatever the user just typed (possibly empty) is echoed back as a normal `Value`, matching
this function's existing job of preserving exactly what was submitted, valid or not.

### D4 — The edit form's placeholder-vs-value branch (RD3)

`external_charge_row_edit.templ`'s `start_battery_pct` field becomes:

```templ
@ui.Field(ui.FieldProps{
    Label:    i18n.T(ctx, i18n.KeyChargesFormStartBatteryPct),
    Error:    validationErrors["start_battery_pct"],
    Optional: true,
    Help:     i18n.T(ctx, i18n.KeyChargesFormStartBatteryPctHelp),
}) {
    if vm.StartBatterySource == "ESTIMATED" {
        @ui.Input(ui.InputProps{Type: "number", Name: "start_battery_pct", Attrs: templ.Attributes{"min": "0", "max": "100", "step": "1", "placeholder": vm.RawStartBatteryPct}})
    } else {
        @ui.Input(ui.InputProps{Type: "number", Name: "start_battery_pct", Value: vm.RawStartBatteryPct, Attrs: templ.Attributes{"min": "0", "max": "100", "step": "1"}})
    }
}
```

`Required: true` is dropped entirely (not replaced by `Required: false` — the zero value
already means "not required"). The `else` branch (`USER`, or `StartBatteryPct` absent
altogether) is byte-for-byte what the field already renders today, minus `Required`.

An input with `Value` unset renders `value=""` (`ui.Input` always renders the `value`
attribute — see `templates/ui/input.templ`) alongside a non-empty `placeholder`; this is
the exact pattern the create form's own telemetry-suggestion field already uses
(`StartBatteryPctSuggestion`), so it needs no new mechanism.

### D5 — The create form gets no placeholder-vs-value branch

The create form has no persisted row and therefore no `StartBatterySource` to branch on —
RD3 is an edit-form-only rule. Its `start_battery_pct` field only loses `Required: true`
and gains `Optional: true` plus the new `Help`:

```templ
@ui.Field(ui.FieldProps{
    Label:    i18n.T(ctx, i18n.KeyChargesFormStartBatteryPct),
    Error:    validationErrors["start_battery_pct"],
    Optional: true,
    Help:     i18n.T(ctx, i18n.KeyChargesFormStartBatteryPctHelp),
}) {
    @ui.Input(ui.InputProps{
        Type:  "number",
        Name:  "start_battery_pct",
        Value: d.FormValues.StartBatteryPct,
        Attrs: templ.Attributes{"min": "0", "max": "100", "step": "1", "placeholder": d.StartBatteryPctSuggestion},
    })
}
```

The existing `d.StartBatteryPctSuggestion` placeholder (telemetry-sourced, unrelated to
RD3) is untouched.

### D6 — `parseExternalChargeForm`: `startPct` becomes `*int`, absent means nil, no error

`handlers/external_charges.go`, replacing the block whose own comment currently states the
rule this change reverses (verified at the block starting `// start_battery_pct stays
UNCONDITIONALLY required`, roughly `:1403-1420` in the file read on 2026-09-16 — the
roadmap's cited `:1403-1412`/`:1416` line numbers land inside the same block, off by a few
lines from small formatting, not a different location):

```go
// start_battery_pct is now OPTIONAL — the charging module derives it from the
// energy added and the end battery percentage when it is left empty
// (Writer.Create/Update). The 0-100 bound check still applies to a value the
// user DID type; only a malformed or out-of-range value is rejected, an
// absent one is not. No relative-order check (start < end): a partial charge
// with prior driving can legitimately start above the previous end; the
// user-asserted entry is the user's truth.
startPctStr := strings.TrimSpace(raw.StartBatteryPct)
var startPct *int
if startPctStr != "" {
    n, perr := strconv.Atoi(startPctStr)
    if perr != nil || n < 0 || n > 100 {
        errs["start_battery_pct"] = i18n.T(c.Request.Context(), i18n.KeyChargesErrorStartBatteryPctRange)
    } else {
        startPct = &n
    }
}
```

The struct literal further down changes from `StartBatteryPct: &startPct,` to
`StartBatteryPct: startPct,` — `startPct` is now already `*int`, so no address-of. No other
line in `parseExternalChargeForm` changes: `location_kind`, `end_battery_pct`, `ended_at`
and every other field keep their own independent rules untouched.

`i18n.KeyChargesErrorBatteryPctRequired` is NOT deleted — it stays in the catalogue and
in use for `end_battery_pct` (unaffected by this change, at `:1429` in the same function).
Only its `start_battery_pct` call site is removed.

### D7 — Two pieces of copy this change makes false if left alone

Both were found reading the catalogue and the KB while designing this change, not asked
for by the roadmap directly — but leaving them would ship a form whose own on-page copy
contradicts its new behavior.

1. **`KeyChargesFormCreateHint`** (`internal/gateway/i18n/catalog.go:579`) currently ends:
   *"Una carga En progreso solo requiere Fecha, Ubicación y % de batería inicial." / "An
   In progress charge only requires Date, Location and Start battery %."* — this sentence
   states the very rule this change removes. After this change the IN_PROGRESS required
   set is `charged_on` and `location_kind` only. New text for the second sentence:
   - ES: `"Una carga En progreso solo requiere Fecha y Ubicación."`
   - EN: `"An In progress charge only requires Date and Location."`

   The first sentence (about the energy-from-battery-delta estimate) is untouched — that
   rule does not change here.

2. **`kkpa/context/input-port/charging/external-charges.md`**, "Form layout & field rules"
   section, states: *"the per-field `ui.FieldProps.Optional` hint marks the optional
   fields that live in the MAIN grid (`energy_added_kwh`, `price`, `started_at`,
   `location_label`)"*. `start_battery_pct` joins that list after this change (D1/D4/D5 both
   set `Optional: true` on it). This is exactly the KB-drift `CLAUDE.md` §"Docs track
   structural change" names — fixed in the same change, not left for a later sweep.

### Roadmap-decision mapping

| Design decision | Roadmap decision | Compliance |
|---|---|---|
| D1/D2 | RD5 | The help text lives under the input, one new bilingual key, exact wording RD5 specifies. |
| D3/D4 | RD3 | Derived (`ESTIMATED`) renders as placeholder; `USER`/absent renders as value — `Update` then sees empty exactly when the module should re-derive. |
| D5 | — | Create form has no persisted row; RD3 only ever applied to the edit form. |
| D6 | (the roadmap's own tier-2 scope line) | `start_battery_pct` optional, range check kept, derivation itself untouched. |
| — | RD4 | Not implemented here — it is a `charging`-side guarantee (tier 1's `resolveStartBatteryPct` never recomputes a caller-supplied value) that this tier does not touch or need to re-assert. |

## Specs Affected

`openspec/specs/gateway/spec.md` — three requirements change (delta in `specs/gateway/spec.md`
of this change):

- **Create Charge Entry** — `start_battery_pct` is no longer unconditionally required; the
  "Start battery percentage is required regardless of status" scenario is replaced.
- **Inline Row Editing** — gains the placeholder-vs-value scenario (RD3).
- **location_kind Visible Without Expanding "More Details"** — its cross-reference to
  `start_battery_pct` as "required" is corrected to "optional".

## Test Contract

Fixed here, before any implementation, per `ai/go-conventions.md` §Testing authoring order.
All of these are offline `httptest`/pure-Go tests — no DB, no `TEST_DATABASE_URL` gate; this
tier touches no schema.

### Group A — `ui.FieldProps.Help` (new, `templates/ui/field_test.go` or nearest existing
Field test file)

| Case | Input | Expected |
|---|---|---|
| A1 | `Help: "x"`, no `Error` | rendered output contains `<p class="fieldset-label">x</p>` |
| A2 | `Help: ""` | no `fieldset-label` element in the output |
| A3 | `Help: "x"`, `Error: "y"` | both `fieldset-label` (with `x`) and the error `<p class="text-error text-sm">y</p>` (with `y`) are present, `Help` appearing before `Error` in the output |

### Group B — `externalChargeEntryVMFromEntry` maps `StartBatterySource` (extend or add
beside `TestExternalChargeEntryVMFromEntry`, `handlers/external_charges_test.go`)

| Case | `e.StartBatterySource` | Expected `vm.StartBatterySource` |
|---|---|---|
| B1 | `nil` | `""` |
| B2 | `ptr(charging.StartBatterySourceUser)` | `"USER"` |
| B3 | `ptr(charging.StartBatterySourceEstimated)` | `"ESTIMATED"` |

### Group C — edit-form render chooses placeholder vs. value (new, alongside the existing
`renderEditRow` helper)

| Case | `vm.StartBatterySource` | `vm.RawStartBatteryPct` | Expected `start_battery_pct` input |
|---|---|---|---|
| C1 | `"ESTIMATED"` | `"64"` | `placeholder="64"`, `value=""` (via `tagAttrsFor`: attrs contain `placeholder="64"` and do NOT contain `value="64"`) |
| C2 | `"USER"` | `"50"` | `value="50"`, no `placeholder` attribute at all |
| C3 | `""` (e.g. `StartBatteryPct` was never set) | `""` | `value=""`, no `placeholder` attribute at all |

### Group D — create form (extend the existing suggestion tests)

| Case | Fixture | Expected |
|---|---|---|
| D1 | Fresh render, any `StartBatteryPctSuggestion` | `start_battery_pct` input carries **no** `required` attribute (was asserted present before this change; now asserted absent) |
| D2 | Fresh render | the input still carries `Optional`'s `(opcional)`/`(optional)` legend suffix and the new help text (`KeyChargesFormStartBatteryPctHelp`'s resolved string) |

### Group E — `parseExternalChargeForm` accepts an absent `start_battery_pct` (rewrite of
the tests the old required rule broke)

| Case | Form | Expected |
|---|---|---|
| E1 (was `TestExternalChargeCreate_MissingBatteryPct_Rejected`) | `start_battery_pct` omitted, `energy_added_kwh=10.5`, `end_battery_pct=80`, every other required field valid, status `IN_PROGRESS` | HTTP 200; `writer.createEntry.StartBatteryPct == nil` (the gateway passes `nil` through — it does not derive; `fakeChargeWriter` does not either) |
| E2 (was `TestExternalChargeCreate_A8_StartBatteryPctRequired_BothStatuses`) | `start_battery_pct` omitted, for **both** `IN_PROGRESS` and `DONE` (DONE also supplies `ended_at`/`end_battery_pct`, its own still-required fields) | HTTP 200 for both; `writer.createCalls == 1` for both; `writer.createEntry.StartBatteryPct == nil` for both |
| E3 (out-of-range, unchanged behavior — `TestExternalChargeCreate_OutOfRangeBatteryPct_Rejected`) | `start_battery_pct = "101"` or `"-1"` | HTTP 422, `KeyChargesErrorStartBatteryPctRange`'s message, `Writer.Create` not called — **no change to this test**, listed here only to record that it must still pass unmodified |
| E4 (`TestExternalChargeForms_C1_OptionalFieldsCarryNoRequired_UnconditionalFieldsDo`) | both forms, fresh render | `start_battery_pct` moves from the "unconditional, must carry `required`" list to the "optional, must NOT carry `required`" list, alongside `energy_added_kwh`/`price` |
| E5 (`TestExternalChargeRowUpdate_D3b_ValidationFailureKeepsThePostedWindow`) | the fixture currently omits `start_battery_pct` to force the 422 this test needs; that omission no longer fails | change the trigger to `start_battery_pct = "150"` (out of range) instead, comment updated to say so — the test's actual subject (a failed save must not move the posted window) is otherwise unchanged |

### Group F — comment/name hygiene (not a behavior test; a checklist for the implementer)

These test names/comments assert or narrate the *old* rule and must be corrected in the same
change so a future reader does not learn a false rule from a passing test:

- `TestExternalChargeCreate_A8_StartBatteryPctRequired_BothStatuses` — rename to
  `TestExternalChargeCreate_StartBatteryPctOptional_BothStatuses` (see Group E2).
- `TestExternalChargePage_BatterySuggestionFromTelemetry` — its comment "Start AND end
  battery % must be Required (D6)" no longer describes what the loop below it actually
  asserts (input presence only, never `required`) — correct the comment; the assertion
  itself needs no change.
- `TestExternalChargeCreate_MissingRequiredField`'s comment ("deliberately omit … every
  required field") — drop `start_battery_pct` from that list; the test itself still
  passes unmodified (`charged_on`/`location_kind` remain required).
- `TestExternalChargeCreate_ValidInput` and `TestExternalChargeRowUpdate_ValidInput` — the
  `// D6: required battery fields persisted non-nil` comments overstate the new rule;
  reword to say these are *supplied* values being persisted, not required ones.

## Risks

- **A stale test that still passes for the wrong reason.** Mitigated by Group F above —
  every comment naming the old rule is corrected, not just the assertions that would
  actually fail.
- **`fieldset-label` styling drift on a future DaisyUI major.** Same exposure `Error`
  already carries inside `Field`; no new risk class introduced.
- **None of this reaches the derivation itself.** If a user reports a wrong derived
  number, the bug is in tier 1 (`internal/charging`), not here — this tier only stops
  blocking the nil value from reaching `Writer`.
