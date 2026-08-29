# Proposal — RM33-gateway-add-entries-dashboard

Source: MAG-18 — https://linear.app/magus-monitor/issue/MAG-18/adding-status-to-manual-records

Roadmap: `openspec/roadmaps/RM33-manual-record-status.md` — **tier 3 of 3**, module `gateway`,
implementing roadmap decisions **D9, D10, D11, D13, D14** verbatim, plus decisions confirmed at
the 2026-08-29 tier-3 interview: **D-RM33-9** (no-vehicle fallback), **D-RM33-10** (filter
wiring mirrors `superchargerMonthsSelector`), **D-RM33-11** (post-write window preservation),
**D-RM33-12** (two-state completeness dot). Depends on: `RM33-charging-add-entry-status`
(tier 1, **archived**) — `internal/charging` already exports `Status`, `Entry.EnergyAddedKWh
*float64`, `Entry.OdometerKm *int`, and `Reader.ListEntriesByVehicleBetween(ctx, accountID,
teslaID, from, to) ([]Entry, error)` (inclusive `[from,to]`, `charged_on DESC`, no limit
parameter). **Independent of** `RM33-gateway-update-charge-form` (tier 2, **archived**) — both
tiers depend only on tier 1 and touch disjoint files (tier 2:
`charge_create_form.templ`/`charge_row_edit.templ` field-level form changes; tier 3:
`charges_list.templ`/`charge_row.templ` table/filter/tiles) — but this change's diff sits
**on top of** tier 2's already-merged code (`ChargeEntryVM.Status`/`RawStatus`,
`ChargesPageData.FormValues`, the `status` `<select>`, the COP-suffixed price input, etc. are
all already in `main`).

Design gate: **NOT tripped.** This change touches no database object — no migration, no
column, no index. `internal/charging`'s tier-1 schema and its existing
`ListEntriesByVehicleBetween` port are consumed as-is. `CLAUDE.md` §Pipeline config →
`Design-Gates: database` therefore does not apply; design.md is still written up front
(mandatory for this change per the dispatch's artifact rules) because it fixes the Test
Contract, the date-filter semantics, and the post-write refresh mechanics before
implementation — not because a schema changed.

Unit tests: **included** — the roadmap's standing decision (`D-RM33-3`, MAG-18/RM33, confirmed
at the Step 2 interview), unchanged for this tier. Offline `httptest`-based handler tests
(fake `charging.Reader`/`Writer`), mirroring `internal/gateway/handlers/charges_test.go`'s
existing style. No `DATABASE_URL`-gated test is added — the gateway owns no database.

---

## Why

Tiers 1 and 2 gave manual charge entries a lifecycle `Status`, optional energy/price, and an
odometer field, and taught both charge forms to render/validate that new contract. But the
**entries dashboard** — the table and its surrounding chrome — still reflects the pre-RM33
shape: no way to see at a glance which entries are `DONE` but missing data, no battery-range
column (only the delta), a Vehicle column that is redundant now that the whole page is
vehicle-scoped by the sidebar switcher, a Refresh button that duplicates what any filter action
already does, no date filter, and no aggregation summary. MAG-18's dashboard half — "review your
entries through a date filter with aggregation tiles and spot incomplete records at a glance" —
is unbuilt until this tier lands.

Two structural facts drive this tier's shape, both established by the leader before dispatch and
reconfirmed here from the repository:

- **`GET /ui/charges/list` (`ChargesListFragment`) is the Refresh button's only consumer
  today.** D9 removes that button; per the roadmap tier row and D-RM33-10, this SAME route
  becomes the date-filter endpoint (`?start=YYYY-MM-DD&end=YYYY-MM-DD`, mirroring
  `GET /ui/supercharger-stats`) — no new route.
- **The read stays on the existing port.** D13 forbids a new `charging.Reader` method; the
  vehicle-scoped read becomes `ListEntriesByVehicleBetween(ctx, uid, teslaID, start, end)`,
  replacing `ListEntriesByVehicle(ctx, uid, teslaID, defaultChargeLimit)` on the filtered path.
  The `defaultChargeLimit` (100-row) cap disappears from that call — the window itself bounds
  the result, per the port's own documented contract.

## What Changes

- **ADDED — a two-preset date filter** ("Last 7 days", default; "This month", the FULL
  calendar month — 1st through last day, not month-to-date) rendered as `ui.Join`'d
  `ui.Button`s with server-built absolute `?start=&end=` hrefs, `hx-get="/ui/charges/list"`,
  `hx-target="#charges-list"`, `hx-swap="outerHTML"` — mirroring
  `superchargerMonthsSelector`/`buildSuperchargerPresets` exactly (D-RM33-10). Active preset
  renders `Variant: "primary"`; inactive, `"ghost"`. A new `parseChargesRange` helper (mirrors
  `parseHistoryRange`/`parseSuperchargerRange`) validates `?start=&end=` against
  **`browserToday(c)`**, never UTC.
- **ADDED — four aggregation tiles** (Sessions, Energy, Cost, Avg kWh/session) above the table,
  composing `ui.StatTile`, summed by the handler over the SAME `ListEntriesByVehicleBetween`
  result the table renders — provably matching by construction (D13). Cost is a **single COP
  total** (manual entries are COP by construction; no per-currency split like
  `SuperchargerTiles.CostLines`). An empty window renders `0` (Sessions/Energy/Cost) and `—`
  (Avg kWh, division-guarded).
- **ADDED — a Status column** (D9) carrying BOTH a coloured completeness dot (green = every
  required-for-DONE field present AND energy present AND price present [`price > 0`, since the
  column itself is `NOT NULL DEFAULT 0` and cannot distinguish "unset" from "typed zero" any
  other way]; yellow otherwise — **two-state, no red**, D-RM33-12) and a text badge
  (`IN_PROGRESS`/`DONE`). Both are handler-computed `ChargeEntryVM` fields (D10) — the template
  performs no arithmetic and no `charging.RequiredFieldsFor` call itself.
- **ADDED — a battery-range column** (`"22% → 70%"`) alongside the existing `BatteryDelta`
  column (D11) — both stay, the user asked for both the absolute range and the delta.
- **REMOVED — the Vehicle column and the Refresh button.** The page is already vehicle-scoped
  (sidebar switcher); a per-row vehicle label is redundant, and Refresh is subsumed by any
  filter-preset click.
- **CHANGED — every currently-blank table cell now renders the em-dash `—`** instead of an
  empty string (D14; tier 2 already applied this to `EnergyKWh` as an incidental necessity —
  this tier extends it to `CostPerKWhLabel`, `BatteryDelta`, `DurationLabel`, and the new
  `BatteryRange`).
- **CHANGED — the table column count** from 8 to 9 (`Date, Status, Energy, Price, Cost/kWh,
  Battery Range, Battery Δ, Duration, Actions`), so `charge_row_edit.templ`'s
  `<td colspan="8">` (deliberately left untouched by tier 2's design.md §D-Colspan,
  flagged for this tier) becomes `colspan="9"`.
- **CHANGED — no-vehicle fallback drops the account-wide read.** When `resolveSelectedVehicle`
  yields no vehicle, the page renders the SAME empty state as "no entries yet" (`—` chrome,
  filter, and tiles ALL hidden) instead of falling back to `ListEntriesByAccount` (D-RM33-9).
  This is a genuine, deliberate behavior change for the (extremely rare — zero registered
  vehicles) edge case; that account cannot create an entry either, so nothing is lost.
- **CHANGED — post-write refresh preserves the active window** (D-RM33-11). The create form's
  POST and every inline edit row's PUT both carry the currently-active `start`/`end` (via
  `hx-include` for the create form — see design.md §D-Include — and plain hidden inputs for the
  edit row, mirroring `supercharger_row.templ`'s `windowStartStr`/`windowEndStr` threading into
  its Edit URL), so their OOB refresh of `#charges-list` re-renders the SAME window the user was
  filtered to, not a silent reset to "last 7 days".
- **CHANGED (gap-filling, see design.md §D-Refresh) — `ChargeRowUpdate`'s success path now ALSO
  OOB-refreshes `#charges-list`**, and **`ChargeRowDelete`'s target changes from
  `#charge-row-{id}` to `#charges-list`** (whole-region re-render on both success and failure).
  Neither previously touched the list region at all; now that the list carries aggregation
  tiles computed over the same rows, an edit or delete that does not refresh them would
  silently desynchronize the tiles from the table the very first time either action is used —
  breaking D13's "provably match the rows shown" guarantee one write after this tier ships. This
  extends D-RM33-11's literal "create/update" wording to delete, for the same reason.
  `fragments.ChargeRowEmpty`/`ChargeRowError` become dead code and are removed.
- **REMOVED (dead)** — `KeyChargesListRefresh`, `KeyChargesListHeaderVehicle` i18n keys (verified
  unreferenced elsewhere before deletion — see tasks.md).
- **ADDED** — i18n catalogue keys for: the two filter-preset labels, the four tile labels, the
  new Status/Battery-range column headers, the two status-badge labels, and the two
  completeness-dot tooltips. Every new key carries both `ES` and `EN`
  (`internal/gateway/AGENTS.md` §i18n). Per the catalogue's own documented precedent
  (`internal/gateway/i18n/catalog.go`, the comment above `KeyChargesListHeaderDate` et al.:
  "a table column header is a different semantic role... than a form label or card title"),
  these are NEW keys, not reuses of `KeySupercharger*` or tier-2's `KeyChargesForm*` keys, even
  where the English word coincides.

**Out of scope, deliberately:** any change to `internal/charging`'s schema, domain rules, or
ports (tier 1's territory, archived); any further change to the two charge FORMS beyond
threading the active window through them (tier 2's territory, archived, and NOT reopened here
except for that one addition); a chart of any kind (the roadmap names only tiles + a table for
this tier, unlike Supercharger Stats' kWh-per-month chart).

## Breaking?

**NO.** This is additive/corrective at the gateway's own surface. `internal/gateway` exposes no
Go interface consumed by sibling modules (it is the leaf of the dependency graph), so no
external caller is affected. `ChargesListFragment`'s response shape and the delete button's
`hx-target` both change, but both are internal, private wiring between this module's own
templates and handlers — no other module or external client depends on them.

## Modules affected

- **`gateway`** — owner of every file this tier touches: `handlers/charges.go`,
  `templates/fragments/charges_vm.go`, `templates/fragments/charges_list.templ`,
  `templates/fragments/charge_row.templ`, `templates/fragments/charge_row_edit.templ`
  (colspan fix + two hidden window inputs), `templates/fragments/charge_create_form.templ`
  (`hx-include` attribute only), `i18n/catalog.go`, `AGENTS.md` (no new client-side JS — `—`
  no new RD entry needed; see design.md §D-Include for why this is NOT a zero-JS exception).
- **`charging`** — consumed read-only through its existing public surface
  (`charging.Reader.ListEntriesByVehicleBetween`, `charging.RequiredFieldsFor`,
  `charging.Status`/`StatusDone`). No change to `internal/charging` in this tier.
- No other module. No database object of any kind is touched.

## Read paths affected

**Yes — this tier changes the manual-records list's read path.** The vehicle-scoped list read
switches from `ListEntriesByVehicle(ctx, uid, teslaID, defaultChargeLimit=100)` to
`ListEntriesByVehicleBetween(ctx, uid, teslaID, start, end)` — window-bounded instead of
row-count-bounded, per the `charging` module's own documented contract for that port (no limit
parameter; the `[from,to]` window itself bounds the result). This read now happens on every
`GET /charges`, `GET /ui/charges`, `GET /ui/charges/list`, plus (new, D-Refresh) on the success
AND failure paths of `ChargeRowUpdate`/`ChargeRowDelete` — up from those same routes' existing
one-read-per-render behavior, so no new read multiplicity is introduced, only a different bound.
The `Performance-Profile: read-heavy` window caps (`chargesRangeMaxDays`, see design.md
§D-Range) protect this path from an unbounded scan exactly as `historyRangeMaxDays`/
`superchargerRangeMaxDays` already do for their own endpoints.

## Impact

- **Affected spec:** `gateway` — three **MODIFIED** requirements ("Charge List Fragment",
  "Delete Charge Entry", "Charge Log Page" — the vehicle-scoped read source and the
  no-vehicle-fallback behavior change) and two **ADDED** requirements (the entries-dashboard
  date filter + aggregation tiles; the Status/completeness/battery-range table presentation).
  `manual-charge-log` (the `charging` capability spec) is **untouched** — no domain rule changes
  in this tier.
- **Affected code:** every file listed under "Modules affected" above.
- **Deferred, explicitly NOT in scope:** any further charge-form change (tier 2's territory,
  archived); any `internal/charging` change (tier 1's territory, archived); a chart component
  for the entries dashboard (not asked for by the roadmap tier row).
