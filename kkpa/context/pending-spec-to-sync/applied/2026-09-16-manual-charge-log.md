# Sync proposal — manual-charge-log

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `workflows/manual-charge-crud.md`
Source spec:  `openspec/specs/manual-charge-log/spec.md`
Generated:    2026-09-16
Status: APPLIED 2026-09-16

---

**Delta note.** The guide's `## Conventions & gotchas` already carries the derivation rule itself:
it was written by hand in the same change that added the behavior (RM61 tier 1, task 4.2). This
proposal adds only what the spec states and the guide does not: the two glossary entries, the
out-of-range rule, and the backfill rule. The `INDEX.md` rows for `start battery source` and
`start battery provenance` already exist, so no `[index]` block is emitted.

## [guide] ## Glossary — REPLACE

- **Known as:** `manual charge`, `charges form`, `charge row`, `editing a manual charge`, `External charges page`, `/external-charges`, `/ui/external-charges`, `inferred capacity`, `inferred pack capacity`, `entry status`, `charge status`, `in progress charge`, `energy source`, `energy provenance`, `price source`, `price provenance`, `zero price confirmation`, `start battery source`, `start battery provenance`, `derived start battery percentage`, `odometer reading`, `charge authorship`, `entry author`, `created_by_account_id`
- **Internal name:** `ExternalChargeCreate` / `ExternalChargeRowUpdate` / `ExternalChargeRowDelete` (gateway handlers) → `charging.Writer` port → `analytics.Recalculator.Recalculate` hook — table `manual_charge_entries`, downstream table `vehicle_metrics`. The inferred pack capacity is `charging.Entry.InferredCapacityKWhCalc` (`*float64`), backed by the database column `manual_charge_entries.inferred_capacity_kwh_calc`. The lifecycle status is `charging.Status` (`StatusInProgress` / `StatusDone`) on `charging.Entry.Status`, its required-field rule is `charging.RequiredFieldsFor`, the energy provenance is `charging.EnergySource` (`EnergySourceUser` / `EnergySourceEstimated`) on `charging.Entry.EnergySource`, and the price provenance is `charging.PriceSource` (`PriceSourceUser` / `PriceSourceUnconfirmed`) on `charging.Entry.PriceSource`, driven by the write-only `charging.Entry.PriceConfirmed`. The starting-percentage provenance is `charging.StartBatterySource` (`StartBatterySourceUser` / `StartBatterySourceEstimated`) on `charging.Entry.StartBatterySource` (`*StartBatterySource`), backed by `manual_charge_entries.start_battery_source` and computed by `resolveStartBatteryPct`. Authorship is `charging.Entry.CreatedByAccountID`, backed by `manual_charge_entries.created_by_account_id`.

## [guide] ## Conventions & gotchas — APPEND

- **An out-of-range derivation stores nothing, it does not clamp** — when the computed starting
  percentage falls outside 0 to 100, the module leaves the percentage absent rather than storing
  a wrong number. The provenance is then absent too. A caller cannot tell this apart from "no
  derivation was possible"; both look like an entry with no starting percentage.
  _Source: spec manual-charge-log — Requirement: A missing starting battery percentage may be derived on write._
- **Every row that existed before this column was backfilled to `USER`, and that is exact** — no
  derivation existed before the column, so every stored starting percentage was typed by a
  person. A row with no starting percentage was left with no provenance. Do not treat the
  backfill as an approximation when reasoning about old rows.
  _Source: spec manual-charge-log — Requirement: A missing starting battery percentage may be derived on write._
- **A derived value is stored on write, never computed on read** — the percentage is resolved
  once, at Create or Update, and read back as a plain stored value. There is no read-side
  derivation to keep in step, unlike the value-receiver methods on `charging.Entry`.
  _Source: spec manual-charge-log — Requirement: A missing starting battery percentage may be derived on write._
