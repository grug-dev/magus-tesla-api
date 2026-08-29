# Sync proposal — gateway

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `workflows/supercharger-stats-read.md`
Source spec:  `openspec/specs/gateway/spec.md`
Generated:    2026-08-29
Status: PENDING REVIEW

**Supersedes the 2026-08-28 draft of this same file** (which was never applied). Regenerated after
`RM31-gateway-add-session-battery-edit` (tier 5) archived and synced five more requirements into
the `gateway` capability spec.

Scope of this proposal, after diffing the spec against the guide **as wave 6 of tier 5 left it**:

1. The **two RM31 tier-4 requirements** — "Supercharger Stats monthly chart has readable month and
   kWh axes" and "Supercharger Stats session table displays battery percentages" — carried forward
   verbatim from the superseded draft. These are still genuinely absent from the live guide.
2. **One** residual bullet from the five RM31 tier-5 requirements. Tier 5's own wave-6 KB task
   already wrote the write path into the guide directly (strict `PATCH` body, `±1 day`
   recalculation window and its rationale, nil-`TeslaID` skip-but-save, swallowed `Recalculate`
   error, `csrf_supercharger`, the tenant boundary, and the window-carrying row action URLs), so
   those requirements are **already reflected** and are deliberately not restated.

> **Why the Glossary block below is an edit, not the superseded draft's version.** That draft was
> generated before tier 5's wave 6 rewrote the live glossary. Applying its `REPLACE` verbatim today
> would **delete** the RM31 write-path aliases and internal names the guide now carries. The block
> below merges tier 4's chart/battery-column wording into the *current* glossary instead.

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

- **Known as:** `supercharger stats`, `Supercharger session`, `charge session log`, `Supercharger Stats page`, `/supercharger-stats`, `fast charging stats`, `session battery edit`, `verify session battery`, `battery percentage correction`, `session battery percentages`, `supercharger chart axes`
- **Internal name:** `SuperchargerStatsPage` / `SuperchargerStatsFragment` / `SuperchargerRowStatic` / `SuperchargerRowEditFragment` / `SuperchargerRowUpdate` (gateway handlers) → `charging.SessionReader` (`ListSessionsByVehicleBetween`) / `charging.SessionVerifier` (`VerifySession`) / `charging.SessionWriter` — table `charge_sessions`, owned by the `charging` module. **Changed by RM30** (was `telemetry.SuperchargerReader` over `supercharger_sessions`, the raw ingestion buffer). **Extended by RM31 tier 4**: the chart carries `YYYY-MM` bar labels + reused kWh y-axis ticks, and the session table carries the four `charging.Session` battery-percentage columns. **Write path added by RM31 tier 5** (`RM31-gateway-add-session-battery-edit`): a signed-in user can now correct a session's `start_battery_pct`/`end_battery_pct` inline through `charging.SessionVerifier.VerifySession` — still NO Create and NO Delete (see "Write path" below).

## [guide] ## How maintenance works — APPEND

- **Change the chart's month labels or kWh axis:** everything lives in `buildSuperchargerChart` in `internal/gateway/handlers/supercharger.go`, which populates `HistoryBar.Label` (`YYYY-MM`) and calls the shared `buildYAxisTicks` with a kWh formatter. There is no Supercharger-specific tick algorithm and no chart library — the shared `HistoryChart` / `HistoryBar` / `historyBarChart` contract renders both. A zero tallest bucket keeps the bars and the vertical-label selection but yields **no** ticks; do not special-case it in the template.
- **Add or change a battery column:** `buildSuperchargerRows` maps each nullable `charging.Session` percentage through the handler's single display formatter (present ⇒ `NN%`, nil ⇒ `—`) onto `SuperchargerRowVM`, then `internal/gateway/templates/fragments/supercharger_stats.templ` renders the pre-formatted string, with the header key in `internal/gateway/i18n/catalog.go` (ES **and** EN) → `make templ`. The values come from the read the page already performs — adding a column must not add a read.

## [guide] ## Conventions & gotchas — APPEND

- **The chart reuses the shared history rendering contract — never a second tick algorithm.** `HistoryChart` / `HistoryBar` / `historyBarChart` and the existing `buildYAxisTicks` render the Supercharger chart; a second tick algorithm, chart renderer, chart library, client-side chart code, or template arithmetic is forbidden. _Source: spec gateway — Requirement: Supercharger Stats monthly chart has readable month and kWh axes._
- **Every calendar-month bucket in the window gets a `YYYY-MM` label, zero-energy months included.** Months with no sessions are zero-filled bars, never skipped — a skipped month silently misaligns the axis. _Source: spec gateway — Requirement: Supercharger Stats monthly chart has readable month and kWh axes._
- **Labels render vertically.** `YYYY-MM` is wider than the dashboard's `MM-DD`, and the labels must stay legible across the 3-, 6-, and 12-month selector windows. _Source: spec gateway — Requirement: Supercharger Stats monthly chart has readable month and kWh axes._
- **A zero tallest bucket renders NO y-axis ticks — the chart must not fabricate a positive kWh maximum.** Bars and the vertical-label selection survive; only the ticks vanish. This is the shared chart's own behavior (`buildYAxisTicks(0, …)` returns nil), not a Supercharger special case, so do not "fix" it by inventing a floor. _Source: spec gateway — Requirement: Supercharger Stats monthly chart has readable month and kWh axes._
- **The four battery columns are start %, end %, start estimate, end estimate — all from `charging.Session`, all pre-formatted in the handler.** A present value renders as its integer percentage plus `%`; a nil value renders **exactly** `"—"`. The template presents strings only. _Source: spec gateway — Requirement: Supercharger Stats session table displays battery percentages._
- **The battery columns add NO read.** They are served from the existing `charging.SessionReader` result — no additional read, write, Tesla API call, or database query may be introduced to display them. _Source: spec gateway — Requirement: Supercharger Stats session table displays battery percentages._
- **The two `_est` columns render `"—"` today and the gateway must never compute one.** Both estimate fields are NULL for every row (no SOC estimator exists — RM27 D11 descoped it), so they must degrade visibly rather than look broken. The gateway calculates and persists nothing. _Source: spec gateway — Requirement: Supercharger Stats session table displays battery percentages._
- **A missing percentage is `"—"`, never `0%` and never an empty cell.** `0%` is a real, meaningful reading; conflating it with "unknown" would misreport a fully-drained arrival. _Source: spec gateway — Requirement: Supercharger Stats session table displays battery percentages._
- **Country stays gone — do not restore the column, the cell, or a Country i18n key.** The table retains Date, Site, Energy and Cost only; `charging.Session` carries no `CountryCode` (RM29 D1) and RM30 removed the column deliberately. _Source: spec gateway — Requirement: Supercharger Stats session table displays battery percentages._
- **Every new header resolves through the i18n catalogue with non-empty ES *and* EN.** A hardcoded or Spanish-only header is incomplete work here exactly as everywhere else in the gateway. _Source: spec gateway — Requirement: Supercharger Stats session table displays battery percentages._
- **A saved battery-percentage edit must NOT refresh the KPI tiles or the chart.** The swap replaces the row and nothing else: session count, energy, cost and average kWh/session, and every chart bar, are derived from energy and cost — never from a battery percentage — so re-rendering the region on save would be pure churn presented to the user as a change. _Source: spec gateway — Requirement: Supercharger session battery percentages are correctable inline._

## [index] ## Workflows — ADD ROWS

| `supercharger stats chart axes` (`YYYY-MM` bar labels + reused kWh y-axis ticks) | `workflows/supercharger-stats-read.md` |
| `supercharger session battery percentages` (the four `charging.Session` % columns; nil ⇒ `—`) | `workflows/supercharger-stats-read.md` |
