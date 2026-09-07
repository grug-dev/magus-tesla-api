# Sync proposal — telemetry

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `architecture/telemetry-ingest-only.md`
Source spec:  `openspec/specs/telemetry/spec.md`
Generated:    2026-09-03
Status: APPLIED 2026-09-05

Derived from the delta `RM41-telemetry-drop-estimate-columns` synced into the main spec on
2026-09-03 (2 requirements modified: **Supercharger Session Ledger**, **Supercharger Session
Read Port**). Both revisions REMOVE the frozen verification-time snapshot pair
(`start_battery_pct_est` / `end_battery_pct_est`) from `telemetry.supercharger_history`.

**Scope note (why this proposal is small).** The live guide was checked before drafting: it
never documented the snapshot pair, so there is nothing stale to delete — only the removal
itself is worth recording, so the next agent does not re-derive it from RM27's reservation.
No `## Component map` block is proposed (a spec carries behavior, not file paths), so the
live Component map is preserved untouched.

---

## [guide] ## Conventions & gotchas — APPEND

- **`telemetry.supercharger_history` has NO battery-percentage estimate columns — do not
  add them back.** The table once reserved a frozen verification-time snapshot pair
  (`start_battery_pct_est` / `end_battery_pct_est`, each `SMALLINT` 0–100) for a companion
  estimation capability. That capability was descoped before it ever shipped, so the pair
  was NULL in every row for its whole life. RM41 tier 3 dropped both columns, reversing
  RM27's design decision D6, which had kept them so a future estimator could land without a
  migration. The estimator that eventually shipped (MAG-36, `derivedStartBatteryPct`) writes
  the real `start_battery_pct` column instead, so there is nothing left for a snapshot to
  capture. `charging.supercharger_sessions` lost its own equivalent pair in the same
  roadmap (tier 2). _Source: spec telemetry — Requirement: Supercharger Session Ledger._

- **A session read through the telemetry port carries the verification TRIO only — start
  percentage, end percentage, source label.** There is no fourth and fifth estimate field.
  Every returned session exposes the trio exactly as stored, NULL when no override was set.
  Code or tests asserting a snapshot pair on a returned session are pre-RM41 and no longer
  compile. _Source: spec telemetry — Requirement: Supercharger Session Read Port._

- **The ledger NEVER persists a computed estimate under a source label.** The source label
  is a closed, explicitly extensible set identifying a human-owned or measured origin only;
  an estimate is a different capability's read-time concern. This rule survived the column
  drop unchanged and is the reason no replacement estimate column was introduced.
  _Source: spec telemetry — Requirement: Supercharger Session Ledger._

## [index] ## Architecture topics — ADD ROWS

| `supercharger history estimate columns` | **dropped** — RM41 tier 3 removed `start_battery_pct_est` / `end_battery_pct_est`; reverses RM27 D6 → `architecture/telemetry-ingest-only.md` |
| `verification-time snapshot pair` | synonym of `supercharger history estimate columns` → `architecture/telemetry-ingest-only.md` |
