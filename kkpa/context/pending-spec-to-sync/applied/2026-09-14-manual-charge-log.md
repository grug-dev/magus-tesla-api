# Sync proposal — manual-charge-log

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `workflows/manual-charge-crud.md`
Source spec:  `openspec/specs/manual-charge-log/spec.md`
Generated:    2026-09-14
Status: APPLIED 2026-09-14

Context for the reviewer: the guide was already updated by hand in the same change
(`RM58-charging-demote-manual-charge-account-id`, tier 1). Its `## Component map` and
`## How maintenance works` already say the reads are car-wide and the write guard is
transitional. So this proposal is deliberately small. It adds only the rules the spec states
that the guide does not yet carry as a convention bullet.

---

## [guide] ## Glossary — REPLACE

- **Known as:** `manual charge`, `charges form`, `charge row`, `editing a manual charge`, `External charges page`, `/external-charges`, `/ui/external-charges`, `inferred capacity`, `inferred pack capacity`, `entry status`, `charge status`, `in progress charge`, `energy source`, `energy provenance`, `price source`, `price provenance`, `zero price confirmation`, `odometer reading`, `charge authorship`, `entry author`, `created_by_account_id`
- **Internal name:** `ExternalChargeCreate` / `ExternalChargeRowUpdate` / `ExternalChargeRowDelete` (gateway handlers) → `charging.Writer` port → `analytics.Recalculator.Recalculate` hook — table `manual_charge_entries`, downstream table `vehicle_metrics`. The inferred pack capacity is `charging.Entry.InferredCapacityKWhCalc` (`*float64`), backed by the database column `manual_charge_entries.inferred_capacity_kwh_calc`. The lifecycle status is `charging.Status` (`StatusInProgress` / `StatusDone`) on `charging.Entry.Status`, its required-field rule is `charging.RequiredFieldsFor`, the energy provenance is `charging.EnergySource` (`EnergySourceUser` / `EnergySourceEstimated`) on `charging.Entry.EnergySource`, and the price provenance is `charging.PriceSource` (`PriceSourceUser` / `PriceSourceUnconfirmed`) on `charging.Entry.PriceSource`, driven by the write-only `charging.Entry.PriceConfirmed`. Authorship is `charging.Entry.CreatedByAccountID`, backed by `manual_charge_entries.created_by_account_id`.

## [guide] ## Conventions & gotchas — APPEND

- **Authorship is stored and returned, but no read may use it.** `created_by_account_id` records which account typed the entry. Every read returns it. No read may filter, order, group or join by it. A read decides its result from the vehicle asked for, never from who created a row.
  _Source: spec manual-charge-log — Requirement: Charge entry authorship is recorded but never scopes a read._
- **The column is kept because authorship cannot be re-derived.** A vehicle may be registered to more than one account, and the account vehicle registry records registration, not who typed a charge. That is why this table keeps its account column while the Supercharger session store dropped its own.
  _Source: spec manual-charge-log — Requirement: Charge entry authorship is recorded but never scopes a read._
- **Read isolation is the caller's job, not this capability's.** The capability scopes every read by vehicle and trusts the vehicle ids it is given. It runs no tenant check of its own. Which vehicles a caller may see is decided before the capability is reached. A caller that passes an unfiltered vehicle set gets an unfiltered read, and nothing here will stop it.
  _Source: spec manual-charge-log — Requirement: Multi-tenant isolation._
- **An empty vehicle set means no rows, never "no filter".** `ListEntriesByVehicles` with an empty or nil slice returns a non-nil, zero-length result. Reading an empty set as "no filter" would return every row in the table.
  _Source: spec manual-charge-log — Requirement: List entries for a set of vehicles._
- **The write guard is transitional, and it is narrower than the read.** `Update` and `Delete` still match on `created_by_account_id`, as the only guard they have until they can name the entry's vehicle. So a co-owner of a shared car can see another account's entry but cannot edit or delete it. The end state — authorship that nothing predicates on — arrives when both writes are re-keyed onto the vehicle.
  _Source: spec manual-charge-log — Requirement: Charge entry authorship is recorded but never scopes a read._

## [index] ## Glossary & routing — entities — ADD ROWS

| `charge authorship` | `charging.Entry.CreatedByAccountID` / `manual_charge_entries.created_by_account_id` — stored and returned, never a read filter | entity | `workflows/manual-charge-crud.md` |
| `entry author` | synonym of `charge authorship` | entity | `workflows/manual-charge-crud.md` |
