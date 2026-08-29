# Sync proposal — analytics

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `entities/vehicle-metrics/guide.md`
Source spec:  `openspec/specs/analytics/spec.md`
Generated:    2026-08-28
Status: APPLIED 2026-08-28

Scope of this proposal: the requirement **modified by RM31 tier 3**
(`2026-08-28-RM31-analytics-read-sessions-from-charging`) — "No Cross-Module Database Access",
whose scenario now names `internal/charging`'s `SuperchargerSessionAnalyticsReader` where it
previously named `internal/telemetry`'s Supercharger port. The watermark-vocabulary consequence
is carried alongside it because the same change migrated the `vehicle_metric_watermarks.source`
value. The rest of the `analytics` capability spec is already reflected in the target guide and
is deliberately not restated here.

> **Reviewer note — a Component map row is now stale and this proposal CANNOT fix it.**
> `from-spec` never proposes `## Component map` edits (spec.md carries no file paths), but the
> guide's "Source data (read-only inputs)" table still reads
> `internal/telemetry (Reader, SuperchargerReader) | vehicle_snapshots + supercharger_sessions reads`.
> After RM31 tier 3 the Supercharger half of that row belongs to `internal/charging`
> (`SuperchargerSessionAnalyticsReader` over `charge_sessions`); `internal/telemetry` still
> supplies `vehicle_snapshots` only. Fix that row by hand when applying, or re-run this mode with
> `--with-filemap`.

---

<!--
HOW APPLY-SYNC READS THIS FILE — block grammar (apply each near-verbatim):

  ## [guide] ## <Section heading> — APPEND
      → append the bullets/lines below to that section of the Target guide.
  ## [guide] ## <Section heading> — REPLACE
      → replace that section's body with the content below.
  ## [index] ## <Table heading> — ADD ROWS
      → append the table rows below to that INDEX.md table, skipping any row whose first cell
        already exists.

Rules:
- Allowed [guide] sections: `## Glossary`, `## How maintenance works`, `## Conventions & gotchas`.
- Do NOT include a `## Component map` block — spec.md has no file paths, so the live Component map
  is preserved untouched. (Only `from-spec --with-filemap` may add one, and only via scoped lookups.)
- [index] rows: the concept + its true aliases only — one row per alias, never one per field.
- Each Conventions bullet should cite its origin: _Source: spec <capability> — Requirement: <title>._
- Delete any block you don't want applied; edit any block freely before applying.
-->

## [guide] ## Glossary — REPLACE

- **Known as:** `vehicle metrics`, `calc fields`, `calculated fields`, `metrics reconciliation`, `derived metrics`, `watermark source`
- **Internal name:** `analytics.Recalculator` (`Recalculate` / `Reconcile`) — table `vehicle_metrics` (analytics-owned), watermarks in `vehicle_metric_watermarks`. **Changed by RM31 tier 3:** the Supercharger input moved from `internal/telemetry`'s port over `supercharger_sessions` to `internal/charging`'s `SuperchargerSessionAnalyticsReader` over `charge_sessions`, and the watermark `source` vocabulary became `('vehicle_snapshots', 'charge_sessions', 'manual_charge_entries')`.

The `_calc` columns: `distance_traveled_km_calc`, `battery_used_pct_calc`, `km_per_pct_calc`,
`estimated_range_km_calc`, `days_spanned_calc` — plus charge-corrected `consumed_pct` derived
alongside them.

## [guide] ## How maintenance works — APPEND

- **Change where the Supercharger input comes from:** it is `internal/charging`'s `SuperchargerSessionAnalyticsReader`, **not** `internal/telemetry` — RM31 tier 3 moved it so that a human battery-% correction written to `charge_sessions` reaches `vehicle_metrics`. `internal/telemetry` still supplies `vehicle_snapshots` and nothing else for this concept. Analytics must import only those modules' public interfaces.
- **Change a watermark source label:** the closed vocabulary is `vehicle_snapshots`, `charge_sessions`, `manual_charge_entries`, enforced by a CHECK constraint on `vehicle_metric_watermarks.source` and mirrored in `recalculate.go`'s source labels. Renaming one means a migration that changes the CHECK **and** disposes of the existing rows — RM31 tier 3 DELETEd the retired `supercharger_sessions` rows rather than renaming them in place, because an absent cursor is defined as the epoch and the next nightly `Reconcile` rebuilds that source's history in one pass.

## [guide] ## Conventions & gotchas — APPEND

- **Analytics owns `vehicle_metrics` and `vehicle_metric_watermarks` and NOTHING else — every input arrives through another module's public read port.** It imports the public `Reader` of `internal/telemetry`, the public `Reader` **and `SuperchargerSessionAnalyticsReader`** of `internal/charging`, and the public `Service` of `internal/account`. Never `internal/telemetry/db`, `internal/charging/db`, or `internal/account/db`, and never a shared pool reaching into another module's tables. _Source: spec analytics — Requirement: No Cross-Module Database Access._
- **The Supercharger input is `internal/charging` over `charge_sessions`, not `internal/telemetry` over `supercharger_sessions`.** This is load-bearing, not cosmetic: `charge_sessions` is the table a human battery-% verification writes to, so reading anywhere else would make the correction invisible to `vehicle_metrics`. _Source: spec analytics — Requirement: No Cross-Module Database Access._
- **Three independent cursors, each advanced alone.** One cursor per (vehicle, source) over telemetry snapshots, Supercharger sessions, and manual charge entries; advancing one must never rewind or skip another. A source with no cursor is treated as never incorporated, so its first reconciliation backfills that source's whole history for the vehicle. _Source: spec analytics — Requirement: Incremental Recompute Via An Analytics-Owned Watermark._
- **An absent watermark row means epoch — which is why a source migration can safely DELETE cursors.** Dropping a retired source's rows costs one full re-read on the next nightly pass; carrying the old cursor value forward risks silently skipping any row in the new table older than the inherited cursor. _Source: spec analytics — Requirement: Incremental Recompute Via An Analytics-Owned Watermark._
- **A weeks-old revision is picked up because the cursor is `updated_at`-driven, not a trailing window.** A Supercharger session from three weeks ago whose `updated_at` refreshes today recomputes the day it affects. This is exactly the mechanism a human battery-% edit rides. _Source: spec analytics — Requirement: Incremental Recompute Via An Analytics-Owned Watermark._
- **Charge-to-day matching is source-specific and half-open for Supercharger sessions.** A session matches a day by its stop instant against `[predecessor capture, this day's capture)` — inclusive of the start, **exclusive** of the end; a manual entry matches by its logged calendar date, inclusive. Do not unify the two rules. _Source: spec analytics — Requirement: Charge-to-Day Matching Is Source-Specific._

## [index] ## Entities — ADD ROWS

| `watermark source` | `vehicle_metric_watermarks.source` (`vehicle_snapshots` / `charge_sessions` / `manual_charge_entries`) | entity | `entities/vehicle-metrics/guide.md` |
