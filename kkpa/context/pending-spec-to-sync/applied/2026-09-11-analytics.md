# Sync proposal — analytics

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `architecture/charge-record-mutation.md`
Source spec:  `openspec/specs/analytics/spec.md`
Generated:    2026-09-11
Status: APPLIED 2026-09-11

---

## Scope of this proposal — read before applying

This is a **narrow delta**, not a re-sync of the whole `analytics` capability. The capability
spec holds 27 requirements; only **one** changed, and only that one is proposed here.

The change was `analytics-rekey-charge-gaps-on-tesla-id` (MAG-64), archived 2026-09-11. It
rewrote the **Charge Gap Ledger** requirement: the ledger now keys on **vehicle and day**, with
no account. The other 26 requirements are untouched, so proposing them would only add churn.

**Already done inside that change, do NOT re-apply:**

- `entities/vehicle-metrics/guide.md` — the `charge_gaps` column list was corrected there:
  `account_id` removed, `UNIQUE (tesla_id, gap_date)` / `charge_gaps_tesla_date_unique`, and the
  `idx_charge_gaps_account` sentence replaced with why no second index exists.
- `docs/battery-consumed-graph.md` — same two corrections, outside the KB.

**What is still missing, and is what this proposal adds:** `architecture/charge-record-mutation.md`
documents the charge-gap write flow but records no rule for the reconcile call's own rejection and
atomicity guarantees. Those are spec-level behavior, so they belong in its gotchas.

The `## Component map` is deliberately NOT proposed. The spec carries no file paths, and the
guide's existing map is still correct — `analytics/gap_writer.go` and `app/processor.go` are
unchanged in name and role.

---

## [guide] ## Conventions & gotchas — APPEND

- **The gap ledger is keyed on vehicle and day, never on account.** There is at most one row per
  vehicle-day. A day's shortfall is one aggregate observation, so it is never split across two
  rows for the same day. A car can change hands; the flagged day belongs to the car.
  _Source: spec analytics — Requirement: Charge Gap Ledger._
- **A reconciliation is all-or-nothing.** Every insert, update, and removal it makes either fully
  applies or has no effect. Do not add a write to this path that can land on its own.
  _Source: spec analytics — Requirement: Charge Gap Ledger._
- **A mis-scoped flagged entry rejects the WHOLE call, writing nothing.** If any supplied day
  does not belong to the call's own vehicle, or falls outside the call's own window, nothing is
  stored — including the entries that were correctly scoped. This is a caller bug, not data to
  accept quietly.
  _Source: spec analytics — Requirement: Charge Gap Ledger._
- **Removal leaves no trace.** A day that stops flagging is deleted. There is no resolved marker
  and no soft delete: a row is either present (still flagged) or absent. Do not add a
  `resolved_at` column to make history queryable — the ledger is a live worklist by design.
  _Source: spec analytics — Requirement: Charge Gap Ledger._
- **Days outside the reconciled window are never touched**, whatever their own flagged state.
  This is why a correction older than the nightly window is never healed: the pass simply does
  not look there.
  _Source: spec analytics — Requirement: Charge Gap Ledger._

## [index] ## Architecture — ADD ROWS

| `charge gap ledger` | synonym of `charge gaps` — the reconcile contract → `architecture/charge-record-mutation.md` |
| `gap reconciliation` | `analytics.GapWriter.ReconcileWindow`'s own contract → `architecture/charge-record-mutation.md` |
