Source: MAG-19 — https://linear.app/magus-monitor/issue/MAG-19/allow-editing-supercharger-sessions
Roadmap: openspec/roadmaps/RM31-supercharger-session-verification.md
Tier: 5 of 5 (the final tier — the write UI). Depends on tiers 1-4, all archived: tier 1
(`charging.SessionVerifier`), tier 2 (`charging` analytics read ports), tier 3 (`analytics`
reading `charge_sessions`), tier 4 (gateway display-only battery columns + chart labels/ticks).

Design gate: **not tripped.** This change creates no database object and alters none: no
migration, table, column, index, constraint, view, query, or db-package file is touched. The
write it performs goes entirely through tier 1's existing `charging.SessionVerifier.VerifySession`
port, which already validates range and computes `battery_pct_source`; the gateway adds no SQL of
its own. See design.md "Database Changes" for the explicit confirmation.

Grill-me: the owner interviewed and settled every material product/design decision on this tier
in this session (recorded in design.md as D1-D8, keyed to the dispatch's D-I1-D-I8 labels) before
this proposal was written — verb (PATCH, not PUT), strict-body semantics (absent key = 400,
empty value = clear), the recalculation window (±1 day, new helper, not `recalculateAfterChargeWrite`),
the day source (`ChargeStopDateTime`, UTC), the nil-`TeslaID` skip, blank-clears/no-ordering-
validation, the new CSRF key, and the KB-update scope. No further interview is needed for this
artifact pass.

## Why

Tiers 1-4 gave the platform a write port for the two human-owned battery percentages on a
Supercharger session (`charging.SessionVerifier`), the two extra `charging` read shapes and the
`analytics` source switch that make a correction visible in `vehicle_metrics`, and a
display-only rendering of all four battery columns on `/supercharger-stats`. Nothing yet lets a
user actually **make** a correction — the page is still read-only for these two fields. This
tier is the missing write UI: an inline, per-row edit affordance that calls the existing
verification port and immediately triggers the same style of analytics recalculation the
manual-charge write path already performs, so the corrected percentage is reflected without
waiting for the nightly reconcile pass.

## Breaking change

**No.** This is additive: two new htmx routes (`GET`/`PATCH` on a new
`/ui/supercharger-stats/row/:id` family) and one new field on an existing gateway view model
(`SuperchargerRowVM.ID`, `SuperchargerStatsView.CSRFToken`/`WindowStartStr`/`WindowEndStr`).
No existing route, port signature, or column changes behavior. The two page-level `GET` routes
retain their exact current contract.

**Modules affected:** `gateway` only. `charging` is consumed solely through its existing public
`SessionVerifier` (new dependency, tier 1's already-shipped port) and `SessionReader` (existing
dependency) — no changes to either interface. `analytics` is consumed solely through the
already-wired `Recalculator.Recalculate` — no interface change. `cmd/web` needs one new
composition-root wiring line (`charging.NewSessionVerifier(pool)` → `gateway.Deps.SuperchargerVerifier`);
that edit lives outside `internal/` and is called out as a leader-owned task in tasks.md, not
performed by this module's worker.

## Read paths affected (Performance-Profile: read-heavy)

The two existing page-level reads (`GET /supercharger-stats`, `GET /ui/supercharger-stats`) are
unchanged — still one bounded `ListSessionsByVehicleBetween` call per render. This tier adds
three new low-traffic, user-triggered request paths, none of which are on the read-heavy hot
path (dashboard/history/session-list loads):

- `GET /ui/supercharger-stats/row/:id/edit` and `GET /ui/supercharger-stats/row/:id` (cancel) —
  each performs the SAME single bounded `ListSessionsByVehicleBetween` read the page already
  performs (scoped to the row's own rendered `[start, end]` window, carried in the request), then
  matches the row `id` in memory. No new query, no new index, no unbounded scan (design.md D1).
- `PATCH /ui/supercharger-stats/row/:id` — one `VerifySession` write (tier 1's existing,
  account-scoped, three-column `UPDATE ... WHERE id AND account_id`) plus one `Recalculate` call
  over a bounded 3-day window. Both already exist as ports; this tier adds no SQL.

None of these three routes is called on a page load — only on explicit user interaction with one
row's Edit/Save/Cancel controls, matching the manual-charge write path's own read-heavy
exemption reasoning (`internal/gateway/AGENTS.md` §"Exception: user-initiated writes").

Performance profile (binding): `read-heavy — read performance is mandatory over write performance; writes are mostly done by pollers at midnight, so denormalizing, indexing aggressively, and precomputing for reads is acceptable — never at the cost of the modular-monolith boundaries or module data ownership.`

## What Changes

- **CHANGED** — `internal/gateway/handlers/supercharger.go`: add `csrfSuperchargerKey`; issue
  the token from `SuperchargerStatsPage`, read it (never re-issue) from
  `SuperchargerStatsFragment`; add `fetchSuperchargerRowVM` (the list-and-match single-session
  resolve, D-I1); add `SuperchargerRowStatic`, `SuperchargerRowEditFragment`,
  `SuperchargerRowUpdate` handlers; add `recalculateAfterSessionVerify` (the ±1-day recalculation
  helper, D-I3/D-I4); extract `superchargerRowVMFromSession` out of `buildSuperchargerRows` so
  both the list build and the single-row handlers share one mapping; add
  `WindowStartStr`/`WindowEndStr` to the view.
- **CHANGED** — `internal/gateway/templates/fragments/supercharger_vm.go`:
  `SuperchargerRowVM` gains `ID`, `RawStartBatteryPct`, `RawEndBatteryPct`; `SuperchargerStatsView`
  gains `CSRFToken`, `WindowStartStr`, `WindowEndStr`.
- **NEW** — `internal/gateway/templates/fragments/supercharger_row.templ`: `SuperchargerRow`
  (the addressable, Edit-button-carrying static row, extracted out of the inline loop
  `superchargerTable` currently has) and `SuperchargerRowError`.
- **NEW** — `internal/gateway/templates/fragments/supercharger_row_edit.templ`:
  `SuperchargerRowEdit` — the two-input (start/end battery %) inline edit form, PATCH on the
  `<form>`, Save/Cancel buttons, mirroring `ChargeRowEdit`'s shape.
- **CHANGED** — `internal/gateway/templates/fragments/supercharger_stats.templ`:
  `superchargerTable` calls `SuperchargerRow` per row instead of inlining `<tr>`; add the
  "Actions" header.
- **CHANGED** — `internal/gateway/i18n/catalog.go`: 10 new bilingual ES/EN keys (Actions header,
  Edit/Save/Cancel row-action labels, five error strings — invalid id, session not found,
  malformed body, start/end out-of-range).
- **CHANGED** — `internal/gateway/handlers/handlers.go` and `internal/gateway/gateway.go`: add
  `Deps.SuperchargerVerifier charging.SessionVerifier`, thread it into `Handler`, register
  `GET /ui/supercharger-stats/row/:id`, `GET /ui/supercharger-stats/row/:id/edit`,
  `PATCH /ui/supercharger-stats/row/:id`.
- **CHANGED** — `internal/gateway/AGENTS.md`: document the new `Deps.SuperchargerVerifier` bullet
  (mirroring the existing `Deps.SuperchargerReader`/`Deps.ChargingWriter` bullets) and a new
  "Exception: Supercharger session battery verification" amendment alongside the existing D4
  (`charging.Writer`) and D-lang (`SetLanguage`) amendments — this tier is a THIRD, narrower
  aperture on the gateway's read-only default, calling `charging.SessionVerifier` instead of
  `charging.Writer` (design.md D8).
- **NEW** — `internal/gateway/handlers/supercharger_test.go` additions: pure/offline tests per
  design.md's Test Contract, covering the strict-body 400/clear distinction, the ±1-day
  recalculation window (including the just-after-midnight case), the nil-`TeslaID` skip, blanks
  clearing both percentages and the source, out-of-range 422s, and the no-issued-token CSRF
  403. `DATABASE_URL`-gated fake-port `httptest` coverage for the three new routes' end-to-end
  wiring is written LAST, after the pure tests (project test-authoring order).
- **CHANGED** — `kkpa/context/workflows/supercharger-stats-read.md`: add the write path and the
  recalculation hop (ticket item 6, ridden in this tier per roadmap Decision 6); update its
  cross-reference in `kkpa/context/workflows/manual-charge-crud.md` and the `INDEX.md` rows for
  both.
- **LEADER TASK (outside `internal/`)** — `cmd/web/main.go`: wire
  `charging.NewSessionVerifier(pool)` into `gateway.Deps.SuperchargerVerifier`. Called out in
  tasks.md as a leader-owned task; this module worker does not touch `cmd/`.

## Non-Goals

- **No delete action** (ticket item 5) — no `DELETE` route, no delete button, no confirm dialog
  for this row type.
- **No `charging` read-port addition.** No by-ID `GetSession` method is added to `SessionReader`
  or any other `charging` interface — the single-session resolve is list-and-match over the
  already-shipped `ListSessionsByVehicleBetween`, entirely inside `internal/gateway` (D-I1).
- **No ordering validation** (`end < start` is never rejected) and **no write to
  `battery_pct_source`, `start_battery_pct_est`, or `end_battery_pct_est`** from the gateway —
  `VerifySession` itself computes the source and cannot reach the two `_est` columns.
- **No `hx-swap-oob` / out-of-band tile or chart refresh.** Verified in code: `buildSuperchargerTiles`
  reads only `EnergyKWh`/`TotalCost`/`Currency`/count, and `buildSuperchargerChart` buckets only
  `EnergyKWh` — neither reads any battery percentage, so a battery-% edit cannot change either.
  The row-only swap is provably sufficient, not an oversight.
- **No new database object** — no migration, table, column, index, constraint, or view.
- **No change to `recalculateAfterChargeWrite`** or its single-day window — that helper stays
  exactly as the manual-charge path needs it; this tier adds a SEPARATE helper (D3/D4).

## AI-efficiency rationale

This tier is a close mirror of `charges.go`'s existing edit/CSRF/error-fragment vocabulary
(`ChargeRowStatic`/`ChargeRowEditFragment`/`ChargeRowUpdate`, `checkCSRFKey`, `renderError`) —
reusing that closed vocabulary rather than inventing a second CRUD idiom keeps the change local
(one new handler group, two new small templates, ten catalogue lines) and lets a future agent
implement or review it by diffing against a known-good reference slice instead of re-deriving
conventions. The single new abstraction this tier adds — `fetchSuperchargerRowVM`, mirroring
`fetchEntryVM` — is justified because the underlying `charging.SessionReader` gap (no by-ID read)
is itself a considered, out-of-scope decision (D-I1), not an oversight this change should design
around by growing the `charging` port.
