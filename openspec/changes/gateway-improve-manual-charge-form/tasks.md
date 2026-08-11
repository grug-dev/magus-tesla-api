# Tasks: gateway-improve-manual-charge-form

> MAG-5 — New records improvements. Single module (`internal/gateway`), no DB, no
> new route, no `manualcharge` or `telemetry` module change. Sub-tasks T1–T6 touch
> largely disjoint files and can be implemented in parallel once their
> dependencies are noted; T7 (verification) depends on all of them; T8 (codegen
> regen) is the closing step. Decision IDs (D1..D7) from `design.md` are noted on
> every task so the leader's reconciliation can match design → task.

## T1. Root-cause and fix the delete-row alert bug — D3

- [ ] T1.1 **Reproduce first, do not assume.** Load `GET /charges` against the
  running server; click Delete on an existing row and confirm the alert via the
  browser devtools (Network tab: the HTTP status code and response body the
  `hx-delete` request received). Capture which of the three candidate root causes
  in `design.md` D3 is actually firing: (a) stale CSRF → 403, (b) row-id / target
  mismatch so `hx-swap="outerHTML"` lands nowhere, or (c) Writer error →
  `fragments.ChargeRowError` rendered as 500. File the observation as a code
  comment in `internal/gateway/handlers/charges.go` at the top of `ChargeRowDelete`
  before editing, so the fix is traceable to the actual failure.
- [ ] T1.2 Fix the matching root cause in the gateway only:
  - If **(a) stale CSRF**: align the delete button's embedded `csrf_token` (rendered
    in `fragments.ChargeRow` via `#csrf-delete-<id>`) with the live session token at
    delete time. The likely fix is to make the row's CSRF token re-issue on the
    `vehicle-changed`-driven `ChargesContentFragment` refresh (which already
    re-issues `csrf_manualcharge` and re-renders the rows) and/or ensure the static
    row rendered by the initial `ChargePage` carries the same token the session
    holds. Do NOT weaken the CSRF check (`checkCSRF` / `subtle.ConstantTimeCompare`
    stays fail-closed).
  - If **(b) target/swap mismatch**: confirm the `<tr id="charge-row-<id>">` in
    `fragments.ChargeRow` and `fragments.ChargeRowEmpty` both match the
    `hx-target="#charge-row-<id>"` on the Delete button; fix the id construction if
    they differ.
  - If **(c) Writer error path**: confirm the failing Writer call is a gateway bug
    (e.g. wrong account scoping) and not a `manualcharge` service issue — the latter
    is out of scope; if it is a `manualcharge` bug, STOP and flag it as a blocker
    per `design.md` (this change does not modify `manualcharge`).
- [ ] T1.3 Add/extend a test in `internal/gateway/handlers/charges_test.go` (or the
  existing `gateway_test.go`) covering the fixed path: an authenticated `DELETE
  /ui/charges/row/:id` with a valid CSRF token **returns 200, renders
  `fragments.ChargeRowEmpty`, and the row is no longer in the list on a subsequent
  `GET /ui/charges/list`**. Add (or keep) a negative test: a stale/missing CSRF
  token is rejected with 403 and the Writer is not called. Use the existing fake
  `manualcharge.Writer`/`Reader` pattern in the suite.

_depends_on: none_ — T1 is independent of T2–T6 (it can land first or in parallel;
it touches `charges.go` `ChargeRowDelete` + the row fragments + tests).

## T2. Remove the Vehicle field; source TeslaID/VIN from the session selection — D4

- [ ] T2.1 In `internal/gateway/templates/fragments/charge_create_form.templ`,
  delete the entire `@ui.Field(ui.FieldProps{Label: "Vehicle" …})` block (both the
  `SingleVehicle` disabled-`<select>`+hidden-input branch and the multi-vehicle
  `<select>` branch). The form no longer renders any vehicle picker.
- [ ] T2.2 In `internal/gateway/handlers/charges.go` `parseChargeForm`:
  - Remove the `vehicleVal := c.PostForm("vehicle")` / `parseVehicleValue` /
    `vehicleOwned` lines from the validation path.
  - Replace them with a resolution of `(teslaID, vin)` from the session-selected
    vehicle: call `h.resolveSelectedVehicle(ctx, c, uid)` (the same call
    `ChargePage`/`ChargesListFragment`/`ChargesContentFragment` already make), use
    its `TeslaID` and `VIN` if resolved.
  - Keep the tenant-ownership check: after `account.RegisteredVehicles(uid)`, verify
    the resolved `(teslaID, vin)` is in the user's vehicle list (reuse
    `vehicleOwned`). If the resolved vehicle is not owned by the account (unlikely
    but defense-in-depth), return HTTP 403 as today.
  - When `resolveSelectedVehicle` returns `(_, false)` (no vehicles / nothing
    selected and none auto-picked): the create POST fails with a validation error
    "Please select a vehicle." (or similar — exact wording is an implementer choice)
    rendered via `fragments.ChargeCreateForm`, HTTP 422 — parity with today's
    ownership-403 path but reached earlier. Do NOT call the Writer.
- [ ] T2.3 Confirm `fragments.ChargeCreateSuccessOOB` and the create-success flow
  still work: the success path re-renders the form (now without the vehicle field)
  and the OOB `#charges-list` refresh. No behavior change is required beyond the
  field removal; just verify it compiles and the success swap still fires.
- [ ] T2.4 Update affected tests in `internal/gateway/handlers/charges_test.go`:
  any test that submits a `vehicle` form field in the create POST must drop that
  field and instead seed the session with a selected vehicle (via the existing
  vehicle-selection seam the test suite uses) so the ownership resolution succeeds.
  Add a test that a create POST with no resolvable selected vehicle is rejected
  without calling the Writer.

_depends_on: none_ — touch `charge_create_form.templ` (the vehicle-field block only)
and `charges.go` `parseChargeForm` (the vehicle-resolution block only). Disjoint
from T1 (delete handler), T3 (currency field/value), T4 (battery fields +
suggestion), T5 (date fields), T6 (energy input step) at the file-line level — but
**note** T2, T3, T4, T5, T6 all edit `charge_create_form.templ`, so coordinate
template edits serially or merge them in one pass. `parseChargeForm` in
`charges.go` is also touched by T3 (currency hardcode), T4 (battery required), and
T6 (energy already floats) — coordinate that function's edit serially.

## T3. Disable the Currency field; hardcode COP in the parser — D5

- [ ] T3.1 In `charge_create_form.templ`, change the Currency `@ui.Field`/`@ui.Input`
  to a disabled input with `Value: "COP"`. Use the `ui.Input` `Disabled: true` flag
  (or whatever the `ui.InputProps` exposes for `disabled` — verify against
  `internal/gateway/templates/ui/`); do NOT add a new `ui/` wrapper for this —
  re-use the existing disabled affordance on `ui.Input`.
- [ ] T3.2 In `charges.go` `parseChargeForm`: delete the
  `currency := strings.TrimSpace(c.PostForm("currency"))` + `if currency == ""`
  validation branch. Hardcode `Currency: "COP"` on the built `manualcharge.Entry`.
  (A disabled input is not submitted, so reading `c.PostForm("currency")` would
  return `""` — hardcoding avoids that footgun entirely.)
- [ ] T3.3 Update tests: any create-POST test that previously submitted
  `currency=COP` (or any currency) drops that form field; the assertion on the
  persisted `Entry.Currency` stays `COP`.

_depends_on: none_ — disjoint from T1/T4/T5/T6 at the file-line level inside the
files; coordinate `parseChargeForm` serially with T2/T4 (see T2.4 note).

## T4. Make start/end battery % required + add the start-battery suggestion label — D6 + D2

- [ ] T4.1 In `charges_vm.go`, add the `StartBatteryPctSuggestion string` field to
  `ChargesPageData` exactly as specified in `design.md` §"View model changes"
  (empty string = no suggestion; non-empty = the helper-label text). Document the
  graceful-empty contract (D2) in the field's godoc comment.
- [ ] T4.2 In `charges.go` `buildChargesPage`, after the existing
  `manualcharge.Reader` list call, add ONE read of
  `h.telemetryReader.LatestSnapshotsByAccount(ctx, uid)` (the port is already on
  `Deps.TelemetryReader`). Pick the `Snapshot` whose `TeslaID == teslaIDFilter` from
  the returned slice; if found, set `StartBatteryPctSuggestion` to a formatted
  helper string (e.g. `fmt.Sprintf("Latest: %d%%", snap.BatteryLevelPct)`); if
  not found OR the telemetry read returns an error, leave `StartBatteryPctSuggestion`
  empty (graceful — log the telemetry error at most; do NOT degrade the page).
  Reuse the existing `telemetry.Reader` interface; do NOT add a new reader method.
  Do NOT import `internal/telemetry/db`.
- [ ] T4.3 In `charge_create_form.templ`, render the `start_battery_pct` field's
  suggestion label using `d.StartBatteryPctSuggestion` — pass it as a helper label
  / placeholder on the `ui.Field`/`ui.Input` for that field (whichever the `ui/`
  kit supports for a hint string — verify against `ui.FieldProps`/`ui.InputProps`).
  When `d.StartBatteryPctSuggestion == ""`, render no hint. Also mark both
  `start_battery_pct` and `end_battery_pct` inputs `Required: true` (keep
  `min=0`, `max=100`, `step=1`).
- [ ] T4.4 In `charges.go` `parseChargeForm`: make `start_battery_pct` and
  `end_battery_pct` required. Empty → `errs["start_battery_pct"]` /
  `errs["end_battery_pct"] = "Battery percentage is required."`. Non-integer or out
  of [0, 100] → "<field> must be an integer between 0 and 100." Build non-nil
  `*int` pointers on the `manualcharge.Entry` as today. The 0–100 bound behavior
  `manual-charge-log` spec still allows nullable independently at the service —
  the gateway-required policy is a UI-layer check only; do not change the service.
- [ ] T4.5 Tests in `charges_test.go`:
  - A `buildChargesPage` test with a fake `telemetryReader` returning a snapshot at
    `BatteryLevelPct = 73` for the selected TeslaID → asserts
    `StartBatteryPctSuggestion == "Latest: 73%"` (or the chosen format).
  - A `buildChargesPage` test where the telemetry port returns an empty slice (no
    snapshot for the selected vehicle) → asserts `StartBatteryPctSuggestion == ""`
    and the page still renders (no error shown).
  - A create-POST test that submitting without `start_battery_pct` or
    `end_battery_pct` is rejected with the matching validation error and the Writer
    is not called.
  - A create-POST test that submitting `start_battery_pct = 101` or `-1` (and same
    for end) is rejected with the out-of-range error.

_depends_on: T2 (the `buildChargesPage` signature/flow is touched by both — T4
adds the telemetry read to the helper T2 already calls; coordinate serially) and
T3 (coordinate `parseChargeForm` serially — see T2.4 note). The `charge_create_form.templ`
markup edits for T4 are disjoint from T2/T3/T5/T6 markup edits at the line level,
but coordinate the single template-file edit serially.

## T5. Move the optional date fields up + default today — D1

- [ ] T5.1 In `charge_create_form.templ`, move the `started_at` and `ended_at`
  `@ui.Field`/`@ui.Input` blocks out of the `<details>` "More details" disclosure
  and into the main `grid gap-3 sm:grid-cols-2` card grid (alongside Date / Energy /
  Price / Location). Keep them `type="datetime-local"` (defaulted to today's date).
  Decide (implementer's call, scoped by `design.md` D1's implementation note)
  whether to keep `datetime-local` and default only the date portion, or simplify
  to `type="date"`. Either is acceptable; document the choice in the PR description.
- [ ] T5.2 In `charges.go` where the create-form default values are computed
  (`buildChargesPage`, or wherever the form's initial `Value` strings are built —
  verify the current code path), set the `started_at` and `ended_at` defaults to
  today's date in `YYYY-MM-DD` form (e.g.
  `time.Now().UTC().Format("2006-01-02")`) so the inputs render pre-filled. Pass
  them through `ChargesPageData` if a new VM field is needed, OR reuse an existing
  mechanism if the template can compute the default — but per the
  no-business-logic-in-templates rule, the date string is pre-computed by the
  handler. Add the VM fields if needed (e.g. `DefaultStartedAt`,
  `DefaultEndedAt` on `ChargesPageData`) — this is an additive VM change
  consistent with D1; update `charges_vm.go` accordingly.
- [ ] T5.3 In `parseChargeForm`, the optional-handling for `started_at` /
  `ended_at` stays — clearing either field is still allowed (the fields stay
  OPTIONAL, per D1). No change to the manualcharge nullable contract.
- [ ] T5.4 Tests: assert the rendered create form's `started_at` / `ended_at`
  default values equal today's `YYYY-MM-DD` (UTC); and that submitting with both
  cleared still persists `nil` `StartedAt` / `EndedAt` (parity with today).

_depends_on: none_ for the markup move; coordinate `charge_create_form.templ`
serially with T2/T3/T4/T6. The `charges_vm.go` and `charges.go`
default-computation edits are disjoint at the line level from T4's
`StartBatteryPctSuggestion` additions but in the same files — coordinate.

## T6. Allow 3 decimals on energy + keep nearest-valid-range after 3 — D7

- [ ] T6.1 In `charge_create_form.templ`, change the `energy_added_kwh` input's
  `step` attribute from `"0.01"` to `"0.001"` (the `Attrs: templ.Attributes{"step":
  "0.01", "min": "0.01"}` line — also update `min` from `"0.01"` to `"0.001"` if
  you want the min to match the new step, but `min="0"` is also acceptable; keep
  the existing nearest-valid-range helper behavior by only widening the step, not
  removing it). Do NOT remove the `step` attribute.
- [ ] T6.2 In `charges.go` `parseChargeForm`, no server-side rounding is applied —
  `strconv.ParseFloat(energyStr, 64)` already accepts any precision; the 3-decimal
  cap is a UI-only affordance. The validation `energy <= 0` stays. (If a stricter
  server-side 3-decimal clamp is wanted, it is an implementer choice; the design
  does not gate it — keep parity with today's no-server-side-clamp behavior unless
  a reason to change it surfaces.)
- [ ] T6.3 Tests: a create-POST submitting `energy_added_kwh = 7.345` is accepted
  and persisted with `EnergyAddedKWh = 7.345`. The existing
  non-positive-rejection test (`energy = 0` and negative) still passes unchanged.

_depends_on: none_ — a single `step` attribute change plus tests; coordinate the
template-edit serially with T2/T3/T4/T5.

## T7. Verification gate — depends on T1–T6

- [ ] T7.1 `go vet ./internal/gateway/...` is clean.
- [ ] T7.2 `go test ./internal/gateway/...` passes (all new and pre-existing tests).
  DB-gated tests in this module self-skip when `DATABASE_URL` is unset; this change
  adds no new DB-gated test path (it uses fakes for the read ports).
- [ ] T7.3 `make check` (build + vet + ui-guard + tests) passes — run only if the
  project's build authorization (`CLAUDE.md` → "Builds & local checks") allows;
  otherwise list the commands for the leader/user to run.
- [ ] T7.4 Manual smoke (optional but recommended for T1): load `GET /charges`,
  create an entry, delete it, and confirm the row is removed with **no alert**;
  switch vehicles and confirm the create form, the entries list, the start-battery
  suggestion, and the date defaults all follow the switch.

_depends_on: T1, T2, T3, T4, T5, T6_

## T8. Codegen regen — depends on T2–T6 (any `.templ` edit)

- [ ] T8.1 Run `make templ` (pinned `go tool templ generate`) to regenerate the
  `*_templ.go` files for every edited `.templ` (at minimum
  `charge_create_form.templ`; `charge_create_form_templ.go` will be regenerated).
- [ ] T8.2 If any new DaisyUI/Tailwind **class** was added to a `.templ` (unlikely
  for this change — the edits reuse existing `ui.Field`/`ui.Input` wrappers and
  existing classes), run `make css` and commit `static/app.css` alongside the
  `.templ` edits per the gateway `AGENTS.md` "stale CSS" gotcha. Skip T8.2 if no
  new class was introduced (verify by inspecting the `.templ` diff).
- [ ] T8.3 Confirm `internal/manualcharge/` and `internal/telemetry/` have **zero**
  file changes under this change (`git diff --stat internal/manualcharge
  internal/telemetry` is empty) — the boundary held.

_depends_on: T2, T3, T4, T5, T6_ (and therefore T8 must run before T7's final
`make check`, but is grouped separately so the codegen step is auditable on its
own).

## Blocker note

If during implementation of **T1** (delete root-cause) you discover the delete
failure is a `manualcharge` Writer / service bug (not a gateway CSRF / htmx-wiring
issue), STOP — that is out of this change's scope (per the dispatch's boundary:
"this change does NOT modify the `internal/manualcharge/` module"). Mark T1 as
`blocked` with an honest note in this file and in the change's `progress.json`
(owned by the leader), and report the finding to the leader so it can be
re-dispatched as a separate `manualcharge-*` change.

If during **any** task you discover the change actually needs a database object (a
table, column, index, constraint, view, or migration) — STOP. Per the dispatch's
"Database rule (absolute)" and `design.md` §"Database Changes", that is out of
this design's scope. Leave the change unclosed in the artifacts, mark the gap as a
blocker note in this file and `design.md`, and report it — do NOT silently invent
a schema.