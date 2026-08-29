# Sync proposal — gateway

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `workflows/manual-charge-crud.md`
Source spec:  `openspec/specs/gateway/spec.md`
Generated:    2026-08-29
Status: PENDING REVIEW

---

<!--
Scope of this proposal — read before applying.

Derived from the gateway capability spec after archiving RM33 tier 2
(`RM33-gateway-update-charge-form`, Linear MAG-18). The spec delta that produced it modified
`Requirement: Create Charge Entry` and `Requirement: Inline Row Editing`, and added three
requirements: the odometer field, the AC/DC option text, and the date/time-of-day sync.

Two blocks are deliberately NOT emitted:

- `## Glossary` — the spec introduces no new synonym for the concept itself. `manual charge`,
  `charges form`, `charge row` and `Manual Records` already resolve here. Status, odometer and
  currency are ATTRIBUTES of the concept, not aliases, so per the curation rules they belong in
  the guide body and never in the glossary or INDEX.
- `## [index]` rows — for the same reason: every term a reader would arrive with already routes
  to this guide. Adding attribute rows would force INDEX to be maintained per field.

`## Component map` is untouched, as always for `from-spec` without `--with-filemap` — the spec
carries behavior, not file paths.

NOTE: the guide already documents the CHARGING-side half of RM33 (status-conditional required
fields via `charging.RequiredFieldsFor`, optional/derived energy, the 62 kWh placeholder). The
bullets below are the GATEWAY-side half that tier 2 added and the guide does not yet cover.
Avoid re-stating the charging-side rules when applying.
-->

## [guide] ## How maintenance works — APPEND

- **Status is parsed FIRST on both write paths.** `parseChargeForm` resolves the submitted `status` to a `charging.Status` before any field-presence check, then drives the required set from `charging.RequiredFieldsFor(status)`. A missing or unrecognized status is itself a validation error, so it short-circuits before the per-field checks ever run — which is why every form fixture in a test must carry an explicit `status`.
- **Error re-render (422/500) preserves the whole submission.** Both `ChargeCreate` and `ChargeRowUpdate` rebuild the fragment from the raw submitted values rather than from fresh-load defaults: create overwrites `ChargesPageData.FormValues` (plus the three date defaults) and calls `applyRawRequiredState`; the edit row goes through `chargeEntryVMFromRawValues`. Both recompute the `Required*` pair from the **submitted** status, so the served HTML's `required` attributes match the status being rendered. Keep both halves in step when touching either handler.
- **Completing an in-progress entry is an inline-edit-row action.** Moving the edit row's status control `IN_PROGRESS` → `DONE` (supplying `ended_at` + `end_battery_pct`) is the ONLY UI that completes an entry, and `DONE` → `IN_PROGRESS` the only one that reopens it. There is no separate "complete" button or endpoint.

## [guide] ## Conventions & gotchas — APPEND

- **The gateway renders `required` from `charging.RequiredFieldsFor`, never from its own rule** — `ChargesPageData` (create) and `ChargeEntryVM` (edit row) each carry a `RequiredEndedAt` / `RequiredEndBatteryPct` pair the HANDLER computes; templates hold no business logic. On an error re-render the pair must be recomputed from the SUBMITTED status, or the form comes back with `required` attributes describing a status the user is no longer on. _Source: spec gateway — Requirement: Create Charge Entry._
- **`start_battery_pct` is required at EVERY status; `ended_at` / `end_battery_pct` only at `DONE`** — the first has no `charging.Field` constant and is therefore outside `RequiredFieldsFor`'s domain, so it stays unconditionally required in the gateway. Do not "unify" it into the status-gated set. _Source: spec gateway — Requirement: Create Charge Entry._
- **`energy_added_kwh` and `price` are optional at the gateway, and their empty cases differ** — empty energy is stored as ABSENT (never a fabricated zero, so the charging module's derivation can fire); empty price is stored as `0`. A supplied `energy_added_kwh` of `0` and a supplied `price` of `-1` are both still validation errors. _Source: spec gateway — Requirement: Create Charge Entry._
- **There is no Currency field on either form — `COP` is a suffix on the price input** — rendered via `ui.InputProps.Suffix` (DaisyUI v5's compound-`label` idiom). `currency` remains fixed to `COP` server-side regardless of anything submitted. Do not reintroduce a Currency input, disabled or otherwise. _Source: spec gateway — Requirement: Create Charge Entry._
- **The odometer field is optional and lives inside "More details"** — grouped with charging type, location label and notes, as a whole-kilometre integer; negative or non-integer values are field-level validation errors. _Source: spec gateway — Requirement: Charge form odometer field._
- **AC/DC options carry descriptive text, but the submitted values stay `AC` / `DC`** — the labels explain slow home/destination vs fast Supercharger charging and exist in both ES and EN. Changing the label text must never change the option `value`. _Source: spec gateway — Requirement: Charging type option text explains AC and DC._
- **Changing the charge date rewrites only the DATE half of the session timestamps** — the time-of-day portion of a non-empty `started_at` / `ended_at` is preserved, and an EMPTY one stays empty rather than being populated. This is client-side JS (recorded as RD12 in `internal/gateway/AGENTS.md`), one of the module's few sanctioned exceptions to the no-client-side-JS rule. _Source: spec gateway — Requirement: Charge date change keeps the time of day on start and end timestamps._
- **A second sanctioned JS exception (RD13) toggles `required` live when the status control changes** — it fires with no network request, and the server-side `RequiredFieldsFor` gate remains authoritative. The JS is a UX affordance, never the validation. Both RD12 and RD13 live in the single shared `internal/gateway/static/app.js`. _Source: spec gateway — Requirement: Create Charge Entry; `internal/gateway/AGENTS.md` RD12/RD13._
- **Writing a negative test for these forms: assert the specific field message, not just the 422** — because a missing `status` is itself a validation error, a fixture that omits it produces a 422 for the WRONG reason, and a test asserting only the status code stays green even if the check it names is deleted. Supply a valid `status` and assert the field's own i18n message. _Source: spec gateway — Requirement: Create Charge Entry (status parsed first); RM33 tier-2 test-contract fixture convention._
