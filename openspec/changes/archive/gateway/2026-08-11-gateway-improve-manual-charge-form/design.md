# Design: gateway-improve-manual-charge-form

## Context

The Charge log page (`internal/gateway`, served by `GET /charges` + the
`/ui/charges*` htmx fragment routes) is the user's path for entering and managing
manual charge records. The handlers live in `internal/gateway/handlers/charges.go`;
the form markup lives in `internal/gateway/templates/fragments/charge_create_form.templ`
and the row markup (static, edit, empty, error) in `charge_row.templ`; the view
models live in `internal/gateway/templates/fragments/charges_vm.go`. The module
boundary (gateway calls interfaces, never a DB) and the user-initiated-writes
exception that lets `ChargeCreate`/`ChargeRowUpdate`/`ChargeRowDelete` call
`manualcharge.Writer` are documented in `internal/gateway/AGENTS.md` §"Exception:
user-initiated writes" and §"Vehicle-scoped reads."

This change is a **UI / form / validation change**. It does not touch the database
or a hot read path. The project's Performance-Profile
(`read-heavy — read performance is mandatory over write performance; writes are
mostly done by pollers at midnight, so denormalizing, indexing aggressively, and
precomputing for reads is acceptable — never at the cost of the modular-monolith
boundaries or module data ownership.`) is therefore **not engaged** by this change —
it is a relevant design fact because the only new read the change introduces is one
reuse of an existing batched `telemetry.Reader` call per page render to populate a
suggestion label, which is not a read-path optimization concern.

The `manualcharge` module is **not** modified. Its `Writer.Create` / `Writer.Update`
/ `Writer.Delete` interface already carries `Currency` (string), nullable
`StartBatteryPct` / `EndBatteryPct`, nullable `StartedAt` / `EndedAt`, and
`EnergyAddedKwh float64` — so making the gateway send these as required / COP /
3-decimal-cleaned values needs no `manualcharge` change. per the dispatch's scope
boundary.

## Goals / Non-Goals

**Goals**
- Fix the delete-row bug so deleting a manual charge entry removes the row from the
  page without surfacing an alert.
- Stop double-collecting the vehicle: source it from the menu selection, remove the
  form field.
- Fix currency to COP on the editable surface while keeping it transparent (a
  disabled, pre-filled input — not a hidden field, so the user can still see what
  they are saving).
- Make `start_battery_pct` / `end_battery_pct` required, and surface a
  telemetry-sourced suggestion label for the start field when data exists.
- Surface the optional date fields in the main "Log a charge" card with today's date
  pre-filled, keeping them optional.
- Permit 3-decimal precision on energy added, preserving the browser's
  nearest-valid-range helper behavior (now applied after 3 decimals).

**Non-Goals**
- Any `manualcharge` module change. The `Writer`/`Reader` interfaces and the
  `manual-charge-log` spec are untouched.
- Any `telemetry` module change. Specifically, no new `telemetry.Reader` method and
  no telemetry DB/query/migration. The gateway reuses
  `LatestSnapshotsByAccount(ctx, accountID)` — the existing port the dashboard already
  calls.
- Any database object (table, column, index, constraint, view, migration).
- Changing the `/api/v1` JSON surface. There is none for manual charges today; none
  is added.
- Changing the page's routing, fragment-region structure (`#charges-create-form`,
  `#charges-list`, `#charges-content`), or the vehicle-changed subscription wiring —
  the change is inside the existing fragments.

## Decisions

### D1 — Move the optional date fields up + default to today (item 5, settled)

**What.** The `started_at` and `ended_at` inputs are removed from the `<details>`
"More details" disclosure in `charge_create_form.templ` and rendered instead in the
main "Log a charge" card's grid (alongside Date / Energy / Price). Both inputs are
auto-filled with today's date in `YYYY-MM-DD` form as a **default value**, and both
stay **OPTIONAL** — the form still submits if the user clears them.

**Why.** The author wants the date fields visible and pre-populated so a typical
"charge happened today" entry does not require expanding "More details." Burying
them behind a disclosure made them effectively invisible; the default makes the
common case zero-click. Keeping them optional preserves the manualcharge contract
(nullable `StartedAt` / `EndedAt`).

**Trade-off / rejected alternative.** Making them required was considered and
rejected by the user — the author explicitly said "They are optional values" (item
5 of the ticket). Forcing a value would break logging a historical charge whose
start/end time isn't known precisely today.

**Implementation note (scoped, not gated here).** The current inputs are
`type="datetime-local"`. The today's-date default applies to the **date portion**
(the time portion stays unset, leaving the field's precision as the user's choice).
Whether to (a) keep `datetime-local` and default only the date, or (b) simplify to
`type="date"` for the common case, is an implementer choice made in tasks.md — not a
behavior change this design gates.

### D2 — Start-battery suggestion label from latest telemetry (item 4, settled)

**What.** The `start_battery_pct` field gains a **suggestion label** (a placeholder
/ helper-label string, e.g. `"Latest: 73%"`) built from the active vehicle's latest
telemetry snapshot `BatteryLevelPct`. **When no telemetry snapshot exists for the
active vehicle, no suggestion is rendered** (graceful empty — empty placeholder,
no fabricated number).

**Why.** The user shouldn't have to leave the form to look up the current battery
level. The latest nightly snapshot is the closest-at-hand proxy for "where the
battery is now," and the gateway already has the `telemetry.Reader` port injected
and already calls `LatestSnapshotsByAccount(ctx, accountID)` once per dashboard
render — so reusing that call on the charges page reads what the platform already
stored without waking the car or adding a new port method.

**It is a suggestion, not a forced value.** The placeholder is pre-filled text the
user can overwrite; the field itself remains a normal integer input (required, 0–100,
per D6). The user may type any value; the suggestion just reduces lookups.

**Source: the existing `telemetry.Reader.LatestSnapshotsByAccount` port.** The
handler calls it once per `buildChargesPage`, selects the `Snapshot` whose
`TeslaID == resolvedSelectedVehicle.TeslaID`, and reads `Snapshot.BatteryLevelPct`
(an `int`, already in display units — no conversion per the project's unit
convention). No new `telemetry.Reader` method is added. The telemetry module's
database, queries, and spec are untouched.

**Trade-off / rejected alternatives.**
- *A live Tesla Fleet API read for the current battery %* — rejected. The gateway's
  "read-only at request time / no live Tesla calls on user requests" rule
  (`internal/gateway/AGENTS.md`) forbids it, and a live read would wake the car and
  cost API quota to pre-fill a text box. The latest stored snapshot is the right
  proxy.
- *A new `telemetry.Reader.LatestSnapshotForVehicle(...)` per-vehicle helper* —
  rejected. It would violate the module's batch-read convention
  (`ai/go-conventions.md` §Read optimization: "Write batch reads at the module
  interface level … never per-entity helpers that the caller must loop over"). The
  existing `LatestSnapshotsByAccount` already returns one snapshot per vehicle in a
  single indexed query; the gateway picks the right one out of the slice it already
  receives.
- *Persisting the suggestion on the entry* — rejected. The suggestion is a UI hint,
  not data; persisting it would duplicate the telemetry signal and create a write
  path from a read port.

### D3 — Root-cause and fix the delete-row alert (item 1)

**What.** Today `ChargeRowDelete` (`internal/gateway/handlers/charges.go:294`) on
success returns `render(c, http.StatusOK, fragments.ChargeRowEmpty(id.String()))` —
an empty `<tr id="charge-row-<id>">` swapped in by `hx-swap="outerHTML"` targeting
`#charge-row-<id>` (delete button in `charge_row.templ`:
`hx-delete="/ui/charges/row/:id"`, `hx-target="#charge-row-<id>"`,
`hx-swap="outerHTML"`, `hx-confirm`, `hx-include="#csrf-delete-<id>"`). The user is
reporting that the row is **not** removed and an **alert** appears. The fix is
gateway-only.

**Likely root causes (the implementer must root-cause against the running page, not
assume):**
1. **Stale CSRF token.** `checkCSRF(c)` reads the session's `csrf_manualcharge` token
   and the form's `csrf_token` (sourced via `hx-include` from the hidden
   `#csrf-delete-<id>` input embedded in `ChargeRow`). If the page's CSRF token was
   rotated after a `ChargesContentFragment` refresh (it issues a fresh token and
   `sess.Set(csrfManualChargeKey, …)` every render) but a stale static row still
   holds an old token, the constant-time compare fails (`subtle.ConstantTimeCompare
   != 1`) and the handler writes HTTP 403 "invalid csrf token" — which htmx surfaces
   as an `alert`. This is the strongest candidate.
2. **Wrong htmx target / swap.** `hx-target="#charge-row-<id>"` +
   `hx-swap="outerHTML"` should replace the `<tr>` with `ChargeRowEmpty`'s
   `<tr id="charge-row-<id>"></tr>`. If the row id isn't matching (template id
   mismatch) the swap lands nowhere and the row stays.
3. **Non-2xx path.** If `manualcharge.Writer.Delete` returns an error, the handler
   renders `fragments.ChargeRowError(id, "Could not delete entry — please try
   again.")` with `http.StatusInternalServerError` — an error `<tr>` swapped in.
   Depending on how the page wires the alert, an OOB-only error row can look like
   "nothing happened + an alert."

**The fix is gateway-only.** It is a routing/htmx-wiring/CSRF-lifecycle problem in
the gateway, not a `manualcharge.Writer.Delete` correctness problem — the Writer's
delete semantics (account-scoped, no cross-tenant leak) are already specified and
unchanged by `manual-charge-log`'s `Requirement: Delete an entry`.

**The implementer MUST reproduce the alert against the running page first** (the
HTTP response body and status code the Delete button receives in the failing case),
then fix the matching root cause. Do not "fix" D3 by editing the
`ChargeRowEmpty`/`ChargeRowError` markup speculatively — confirm the failing path
first.

**Trade-off / rejected alternative.** Rewriting the delete flow as an OOB event
(`HX-Trigger: charge-deleted`) and a separate list re-fetch — rejected as
over-engineering for this fix. The current outerHTML-swap-to-empty-row design is
sound when the CSRF lifecycle and target id are correct; fixing the lifecycle is
cheaper than re-architecting the swap.

### D4 — Remove the Vehicle field; source from the active menu vehicle (item 2)

**What.** The `@ui.Field(ui.FieldProps{Label: "Vehicle"…})` block (with its
single-vehicle disabled `<select>` + hidden input branch, and its multi-vehicle
`<select>` branch) is removed from `charge_create_form.templ`. The form no longer
submits a `vehicle` field. The handler's `parseChargeForm` stops reading
`c.PostForm("vehicle")` and instead derives `(teslaID, vin)` from the
**already-resolved** active menu vehicle via `h.resolveSelectedVehicle(ctx, c, uid)`
(the same call `ChargePage`/`ChargesListFragment`/`ChargesContentFragment` already
make to scope the entry list). The tenant-ownership check
(`vehicleOwned(teslaID, vin, vehicles)` against `account.RegisteredVehicles`) is
preserved unchanged — the resolved vehicle is still confirmed to belong to the
calling account before any write proceeds.

**Why.** The sidebar vehicle selector is the single source of truth for "which
vehicle am I working on" across every vehicle-scoped page (gateway AGENTS.md
§"Vehicle-scoped reads — always send the selected TeslaID"). The form's Vehicle
picker duplicated that selector and let the user pick a *different* vehicle than
the one whose entries the list below was showing — an inconsistency the user
reportedly found confusing (item 2).

**Trade-off / rejected alternative.** Keeping the field but defaulting it to the
selected vehicle — rejected. It still permits the inconsistency and adds a second
control the user has to think about. Removing the field and sourcing from the
session selection is the cleaner closed vocabulary: one selector on the page.

**Edge case.** If the account has **no** registered vehicles, `resolveSelectedVehicle`
returns `(_, false)` and the page already degrades to an empty entries list. The
create form should not permit a write in that state (there is nothing to log a
charge against). The implementer surfaces this as a validation error on the create
POST ("no vehicle selected") rather than calling the Writer — parity with today's
`vehicle not owned by this account` 403 path, just reached earlier. This is noted
for the implementer, not gated as a separate decision.

### D5 — Disable the Currency field; always send "COP" (item 3)

**What.** The Currency input in `charge_create_form.templ` becomes a **disabled**
`ui.Input` with `Value: "COP"`. A disabled input is not submitted with the form, so
`parseChargeForm` does **not** read `c.PostForm("currency")` for the value — it
hardcodes `Currency: "COP"` on the built `manualcharge.Entry`. The existing
`currency == ""` → `errs["currency"] = "Currency is required."` branch is removed
(currency can no longer be missing — it is always COP).

**Why.** The platform only operates in COP today; the field was a place to type the
wrong thing. Keeping it visible-but-disabled (rather than hidden) lets the user see
the value being saved — transparency over a hidden pre-fill — without making it
editable noise.

**The `manualcharge.Entry.Currency` column and `Writer.Create`/`Writer.Update` are
unchanged.** The gateway simply always hands `"COP"` down the port; the column's
existing `'COP'` default is now redundant from the gateway's perspective but
remains in place (it stays as a defense-in-depth DB default for any future writer).

**Trade-off / rejected alternative.** A hidden `<input type="hidden" name="currency"
value="COP">` — rejected. It hides the value from the user; the user asked the field
be *disabled* (item 3 says "Disabled the Currency FIELD"), which is a visible, read-
only control, not an invisible one.

### D6 — Make start and end battery % required (item 4)

**What.** `start_battery_pct` and `end_battery_pct` go from optional to required.
`parseChargeForm` adds for each: empty → `errs["start_battery_pct"]` /
`errs["end_battery_pct"]` = "Battery percentage is required."; non-integer or out
of [0, 100] → "<field> must be an integer between 0 and 100." The form's
`ui.Input` for each gains `Required: true` (and keeps `min=0`, `max=100`,
`step=1`).

**Why.** A charge entry saved without battery data makes the derived
`BatteryDelta()` read-time method silently nil — losing the per-entry
start→end battery delta, which is one of the manual-charge-log's derived values
(`manual-charge-log` spec `Requirement: Derived read-time values`). Making the
fields required at the gateway's validation layer (the cheapest, earliest,
deterministic signal — per the project's "deterministic signals over human
round-trips" AI-efficiency principle) prevents that silent hole.

**Trade-off / rejected alternative.** Enforcing `start_battery_pct < end_battery_pct`
(charge went up) — rejected. That is a vehicle-state assumption that doesn't always
hold (a user might log a partial charge from 40% to 65% but actually drove first;
the user-asserted entry is the user's truth). The 0–100 range check is the right
floor; the relative order is the user's call.

**Interaction with `manual-charge-log`.** The `manual-charge-log` spec's
`Requirement: Validate optional field constraints` keeps
`start_battery_pct`/`end_battery_pct` as **nullable optional fields at the service**
(`BETWEEN 0 AND 100` when present, either nullable independently). The gateway
making them **required at the form** is a stricter UI-layer policy that does not
change the service contract — the service still accepts a nullable pair; the
gateway just always sends a non-nil pair. The `manual-charge-log` spec is
unchanged.

### D7 — Allow 3 decimals on energy; keep nearest-valid-range helper after 3 (item 6)

**What.** The `energy_added_kwh` input's `step` attribute changes from `"0.01"` to
`"0.001"` in `charge_create_form.templ`. The browser's native step-validation now
permits up to three decimals; the existing nearest-valid-range helper label
("please use the nearest valid value: X.XX" today → "X.XXX" after this change)
kicks in only when the user types **more** precision than the new 3-decimal step
allows (e.g. `7.3456`). `parseChargeForm`'s server-side `ParseFloat` already
accepts any precision; the three-decimal cap is a UI affordance only — no
rounding is applied server-side. (If a stricter 3-decimal server-side clamp is
wanted, it is an implementer choice in tasks.md, not a behavior this design gates;
keep parity with today's no-server-side-clamp behavior unless the implementer finds
a reason to change it.)

**Why.** Tesla and many EV energy displays report kWh to three decimals (e.g.
`7.345 kWh`); forcing two decimals was over-precise rounding that lost real signal.

**Trade-off / rejected alternative.** Removing the `step` attribute entirely (any
precision) — rejected. The nearest-valid-range helper is good behavior the user
explicitly wants to keep ("That validation is good, but we can apply it after 3
decimals" — item 6); just changing the threshold preserves it.

## Data flow (unchanged shape across the gateway boundary; one extra read for the suggestion label)

```
GET /charges (page) / GET /ui/charges (vehicle-changed refresh) / GET /ui/charges/list (list refresh)
   handler: currentUID → resolveSelectedVehicle(session) → (accountID, teslaID)
            account.RegisteredVehicles(uid)                                          [UNCHANGED]
            manualcharge.Reader.ListEntriesByVehicle(accountID, teslaID, limit)      [UNCHANGED — 1 read]
            telemetry.Reader.LatestSnapshotsByAccount(accountID)                     [NEW 1 read — D2 suggestion label]
              → pick snapshot.TeslaID == teslaID → StartBatteryPctSuggestion string
   build ChargesPageData{ … StartBatteryPctSuggestion: "Latest: 73%" or "" … }
   render pages.ChargePage → fragments.ChargeCreateForm (D1/D4/D5/D6/D7 markup) + fragments.ChargesList

POST /ui/charges/create
   handler: currentUID → checkCSRF → resolveSelectedVehicle → (teslaID, vin)        [D4: no form `vehicle` field]
            account.RegisteredVehicles → vehicleOwned(teslaID, vin)                 [UNCHANGED ownership check]
            parseChargeForm: Currency = "COP" hardcoded                              [D5]
                             start_battery_pct / end_battery_pct REQUIRED, 0–100     [D6]
                             energy_added_kwh: ParseFloat (UI step=0.001 caps input) [D7]
            manualcharge.Writer.Create(entry with Currency="COP", non-nil battery%) [UNCHANGED port call]

DELETE /ui/charges/row/:id
   handler: currentUID → checkCSRF → manualcharge.Writer.Delete(uid, id)            [D3: root-cause the failing path]
            on success → render ChargeRowEmpty(id) (outerHTML swap removes row)      [intended; D3 fixes why it doesn't today]
            on error → render ChargeRowError(id, msg)                               [UNCHANGED render]
```

## View model changes

`fragments.ChargesPageData` (`charges_vm.go`) gains one new field:

```go
type ChargesPageData struct {
    ...existing fields unchanged...
    // StartBatteryPctSuggestion is a placeholder/helper-label string for the
    // start_battery_pct field built from the active vehicle's latest telemetry
    // snapshot BatteryLevelPct (D2). Empty string when no telemetry snapshot exists
    // for the active vehicle (graceful empty — render no suggestion in that case).
    // Example non-empty value: "Latest: 73%".
    StartBatteryPctSuggestion string
}
```

No new view-model field is required for D1 (the today's-date default is computed in
the handler from `time.Now()` and passed straight into the input's `Value` — no
extra VM field needed), D4 (the vehicle section is removed, not parameterized), D5
(currency is a static disabled `"COP"` input), D6 (required is an input attribute),
or D7 (the `step` is a static `"0.001"` attribute). The handler pre-computes
`StartBatteryPctSuggestion` (with a `"Latest: N%"` formatter or whatever the
implementer chooses — the exact wording is a tasks.md detail, not a behavior gate)
so the template does not call `time` or `fmt` — per the gateway's standing "no
business logic in templates" rule.

## Risks / Trade-offs

- **[D2 reads one extra port per charges page render.]** A second read
  (`LatestSnapshotsByAccount`) is added to `buildChargesPage`, alongside the
  existing `manualcharge.Reader` list call. Both are bounded, batched reads (one
  indexed query each); neither is on the platform's hot read path. This is consistent
  with the dashboard, which already issues the telemetry read once per render. No
  N+1 (the telemetry call returns all the account's vehicles' latest snapshots in
  one query; the handler picks the right one by `TeslaID`).
- **[D4 removes an escape hatch.]** A user who wanted to log a charge for a vehicle
  that is not the currently-selected one now has to switch the sidebar selection
  first. This is the intended behavior (one vehicle selector), but if a data-entry
  workflow relied on the form's old picker it breaks that workflow. The user asked
  for this, so it is accepted.
- **[D5 hardcoding "COP" couples the gateway to a currency choice.]** If the
  platform ever supports multi-currency, D5 is undone and the currency field is
  restored as editable. This is a deliberate "YAGNI now, reversible later" decision;
  the `manualcharge.Entry.Currency` column already exists, so a future multi-currency
  change touches the gateway form only, not the service or DB.
- **[D3 root-cause is implementation work, not a spec'd algorithm.]** The design
  names the three candidate root causes but the implementer must confirm against the
  running page. tasks.md gates D3 on a reproduce-then-fix step so the fix matches the
  actual failure, not a guess.

## Migration Plan

None. No DB, no data, no config. Rollback = revert the gateway commits; the create
form returns to today's shape (vehicle picker, editable currency, optional battery
%, 2-decimal energy, dates in "More details", delete bug present).

## Database Changes

**None.** No table, column, index, constraint, view, or migration is added, changed,
or removed by this change. The `database` design gate (`openspec/config.yaml`) does
not trigger.

- `Currency` stays a column on `manualcharge`'s entry table; the gateway simply
  always sends `"COP"`.
- `TeslaID` / `VIN` travel on `manualcharge.Entry` unchanged; the gateway sources
  them from the active menu vehicle instead of a form input.
- `start_battery_pct` / `end_battery_pct` stay nullable at the service
  (`manual-charge-log` spec unchanged); the gateway-required policy is a UI-layer
  check, not a column constraint.
- `energy_added_kwh` column precision/typing is unchanged; the 3-decimal cap is a
  UI `step` attribute, not a column scale change.
- `started_at` / `ended_at` stay nullable; the today's-date default and the move
  to the main card are markup-only.

If implementation discovers a real need for a DB object while building this, that is
out of this design's scope — STOP, leave the change unclosed in the artifacts, mark
the gap in `tasks.md` as a blocker note, and raise it for a design-gated follow-up
rather than adding one silently.