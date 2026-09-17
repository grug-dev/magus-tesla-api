# Sync proposal — process-vehicle-data

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `architecture/nightly-cycle.md`
Source spec:  `openspec/specs/process-vehicle-data/spec.md`
Generated:    `2026-09-17`
Status: APPLIED 2026-09-17

---

<!--
Delta scope: the spec has six requirements. Five already have bullets in the target guide's
`## Conventions & gotchas`. Only "Analytics Recalculation Logs Are Attributable Per Vehicle And
Per Half" (added by RM62 tier 1) is new, so this proposal carries one block.

No `[index]` block: `gap reconciliation` and `metrics reconciliation` already have INDEX rows
pointing at their own contract guides. The concept `nightly cycle` already routes here.

No `## Component map` block — a spec carries behavior, not file paths.
-->

## [guide] ## Conventions & gotchas — APPEND

- **Step 3 logs per vehicle and per half — one line before each half's queries.** The step's
  two halves are metrics reconciliation (`Recalculator.Reconcile`) and gap reconciliation
  (`ConsumedByDay` + `ReconcileWindow`). Each logs its own line naming the vehicle, right
  before the work; the gap line also carries the window. So every query line in the log
  belongs to a known vehicle and a known half. Before this, one line printed before the
  vehicle loop, and the queries that followed it read as if they belonged to the gap half
  when they belonged to `Reconcile`. Do not move either line back out of the loop, and do not
  merge them: a single line cannot say which half caused the query that follows.
  _Source: spec process-vehicle-data — Requirement: Analytics Recalculation Logs Are Attributable Per Vehicle And Per Half._
