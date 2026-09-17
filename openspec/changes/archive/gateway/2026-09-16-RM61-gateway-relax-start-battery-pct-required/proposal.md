# Proposal — RM61-gateway-relax-start-battery-pct-required

Source: MAG-40 — https://linear.app/magus-monitor/issue/MAG-40/recalculated-battery-start

Roadmap: `openspec/roadmaps/RM61-manual-charge-start-derivation.md` — **tier 2 of 3**, module
`gateway`, depends on tier 1 (`RM61-charging-add-manual-entry-start-derivation`, archived
2026-09-16). Tier 1 already ships `charging.Entry.StartBatterySource`, the two constants
`charging.StartBatterySourceUser` / `charging.StartBatterySourceEstimated`, and the derivation
inside `Writer.Create` / `Writer.Update`. This change only stops the gateway from forcing a
value the module can now compute on its own.

Design gate: **does not apply.** This change adds no table, column, index, or migration. See
"No Database Changes" in design.md for the explicit statement `openspec/config.yaml` §design
requires.

Unit tests: **included** — the roadmap header says so (the owner asked for them on
2026-09-15), and this tier's own tests cover the VM mapping, the placeholder-vs-value choice,
and every test that currently asserts `start_battery_pct` is unconditionally required.

---

## Why

`/external-charges` still forces every user to type a starting battery percentage, even
though `internal/charging` can now derive it from the energy added and the ending percentage
(tier 1). This is a UI-only gap: the module's `Writer.Create` / `Writer.Update` already accept
a nil `StartBatteryPct` and fill it in. Nothing stops the gateway from passing that nil through
except its own required-field check.

## What this change does

- Removes `Required: true` from the `start_battery_pct` input on both the create form
  (`external_charge_create_form.templ`) and the inline edit form
  (`external_charge_row_edit.templ`), and removes the matching unconditional-required check in
  `handlers/external_charges.go`'s `parseExternalChargeForm`. The 0–100 range check on a
  **present** value is unchanged.
- Adds a `Help` prop to `ui.FieldProps` (`templates/ui/field.templ`) — a muted line under the
  input, DaisyUI's `fieldset-label` class — and uses it to show one new bilingual line
  explaining that an empty value gets computed. This is a kit addition (not a page inline),
  following the same closed-vocabulary rule as `Error` and `Optional`.
- Gives the edit form the placeholder-vs-value choice roadmap decision RD3 asks for: when the
  stored entry's `StartBatterySource` is `ESTIMATED`, the input renders the stored percentage
  as a `placeholder`, so a re-submission without edits arrives empty and the module derives it
  again. When it is `USER` (or absent), the input renders the stored percentage as a normal
  `Value`, exactly as today.
- Adds `StartBatterySource string` to `fragments.ExternalChargeEntryVM` (`"USER"`,
  `"ESTIMATED"`, or `""`) and maps it in `externalChargeEntryVMFromEntry`
  (`handlers/external_charges.go`) from `charging.Entry.StartBatterySource`. The VM stores a
  plain string, not a `charging.*` type — the VM's own doc comment already bans a domain type
  leaking in, the same rule `Status`/`RawStatus` already follow for `charging.Status`.

## Breaking?

No. Every change either widens what the form accepts (start percentage becomes optional) or
adds a new, empty-by-default field (`ExternalChargeEntryVM.StartBatterySource`,
`ui.FieldProps.Help`). No existing caller, port signature, or persisted column changes.
`go build ./...` / `go vet ./...` should stay green everywhere, including outside
`internal/gateway`, once this tier lands — `internal/charging`'s tier-1 work is already merged
and untouched by this change.

## Modules affected

`internal/gateway` only. Reads `charging.Entry.StartBatterySource` (already public, from
tier 1) through the existing `charging.Reader`/`charging.Writer` ports the gateway already
depends on — no new port, no new `Deps` field.

## Read paths affected

- `GET /external-charges` and `GET /ui/external-charges` (the create form, part of the page
  and its list fragment) — the `start_battery_pct` input's `required` attribute and help text
  change; no read query changes.
- `GET /ui/external-charges/row/:id` (the inline edit form fragment) — same input change, plus
  the new placeholder-vs-value branch driven by `StartBatterySource`, which the fragment
  already receives at no extra read cost (it rides along on the same `charging.Reader` row
  tier 1 already returns).

Neither is a new query and neither changes its row count, filter, or index usage — this is a
form-rendering and validation change only.

## Out of scope (tier 1 owns it, not re-opened here)

The derivation itself — `derivedStartBatteryPct`, `resolveStartBatteryPct`, the
`start_battery_source` column, and the `monthly_effective_capacity` exclusion filter — is
tier 1's work, already archived. This change does not touch `internal/charging` and does not
duplicate any of its maths.
