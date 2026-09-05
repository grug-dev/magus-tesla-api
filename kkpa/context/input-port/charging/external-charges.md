# External charges page — /external-charges

> **Adapter side:** what the outside world calls, and where it forwards to. **No backend flow
> here** — that lives in the linked use-case files.
>
> "Input port" in this KB means the *driving adapter* — the page, route, or endpoint that
> triggers a use case. (Strict hexagonal reserves "port" for the interface the core exposes;
> this KB deliberately uses the looser sense. Do not go looking for a `Port` interface in the
> code because of this filename.)
>
> All KB links below are relative to `kkpa/context/`.

## Page

- **Route / URI:** `/external-charges` (labelled **External** / **Externas**)
- **Description:** The user's own charge log — every charge they typed in by hand, for the
  vehicle currently selected in the sidebar switcher. Create form on top, aggregation tiles, and
  a date-filtered list whose rows edit and delete inline via htmx. This is the only page in the
  app with a full create/update/delete surface.
- **Module:** `charging`

## Front-end component map

| File | Role |
|---|---|
| `internal/gateway/templates/pages/external_charges.templ` | Page shell — tiles, create form slot, `#external-charges-list` region |
| `internal/gateway/templates/fragments/external_charge_create_form.templ` | Create form (`hx-post` → `/ui/external-charges/create`) |
| `internal/gateway/templates/fragments/external_charges_list.templ` | The `#external-charges-list` region + the date-range preset selector |
| `internal/gateway/templates/fragments/external_charge_row.templ` | Static row; carries the Edit link and the Delete button (CSRF on the `X-CSRF-Token` header via `hx-headers`) |
| `internal/gateway/templates/fragments/external_charge_row_edit.templ` | Inline edit form, incl. the hidden `start`/`end` window inputs |
| `internal/gateway/templates/fragments/external_charges_vm.go` | `ExternalChargesPageData` / `ExternalChargeEntryVM` / `ExternalChargeFormValues` presentation models |
| `internal/gateway/handlers/external_charges.go` | All eight handlers for this page plus `buildExternalChargesPage` |
| `internal/gateway/handlers/external_charges_tiles.go` | `entryComplete` (the row status dot) + `buildExternalChargeTiles` |
| `internal/gateway/handlers/external_charges_range.go` | `parseExternalChargesRange` — the strict `?start=&end=` contract for the filter route |
| `internal/gateway/i18n/catalog.go` | Every user-facing string on this page, ES + EN |

## Endpoints

| Method + path | Purpose | Use case |
|---|---|---|
| `GET /external-charges` | Full page load (`ExternalChargesPage`); always uses the default window | — read path, see `workflows/manual-charge-crud.md` |
| `GET /ui/external-charges` | Re-render the create form + list after a vehicle switch (`ExternalChargesContentFragment`) | — read path |
| `GET /ui/external-charges/list` | The date-filter endpoint (`ExternalChargesListFragment`); `400` renders the empty-state with no filter chrome | — read path |
| `GET /ui/external-charges/row/:id` | Cancel-edit — swap back to the static row (`ExternalChargeRowStatic`) | — read path |
| `GET /ui/external-charges/row/:id/edit` | Swap the static row for the inline edit form (`ExternalChargeRowEditFragment`) | — read path |
| `POST /ui/external-charges/create` | Create a new manual entry (`ExternalChargeCreate`) | `workflows/manual-charge-crud.md` |
| `PUT /ui/external-charges/row/:id` | Save an edited row | `use-case/charging/update-manual-charge.md` |
| `DELETE /ui/external-charges/row/:id` | Delete a row | `use-case/charging/delete-manual-charge.md` |

## Adapter-side conventions

- **Tenant ownership is checked in the GATEWAY, on every write** — the handler calls
  `account.Service.RegisteredVehicles` and confirms the submitted `(tesla_id, vin)` pair belongs
  to the caller, returning `403` otherwise. It lives there, not in `charging`, because no
  cross-module FK exists in the database: application-layer scoping is the only referential
  boundary this write has. Never drop it "because the query is account-scoped" — unlike the
  Supercharger path, `charging.Writer` takes the vehicle from the form.
- **CSRF token key:** `csrf_externalcharge`, issued by `ExternalChargesPage` and `ExternalChargesContentFragment`.
  The delete button sends it on the **`X-CSRF-Token` header**, every other write in the body —
  Go does not parse bodies for `DELETE`.
- **Vehicle scope:** the list and the create form both follow `resolveSelectedVehicle`; the
  `#external-charges-content` region re-fetches on the sidebar's `vehicle-changed` event. There is no
  `vehicle` form field.
- **Window threading:** `?start=&end=` (or hidden form inputs) is echoed through every write so a
  save or delete never resets the user's filter. It is **best-effort only** — malformed values
  fall back to the default window and never fail a write. The one exception is
  `GET /ui/external-charges/list`, which rejects a malformed window with `400`.
- **i18n:** every string on this page resolves through `i18n.T(ctx, key)` with both ES and EN
  non-empty. A hardcoded string is incomplete work — `make i18n-guard` enforces it.

## Form layout & field rules

> Moved here from `internal/gateway/AGENTS.md` (MAG-39). It is `/external-charges`-specific detail,
> so it is fetched on demand rather than re-read on every gateway dispatch.

`ExternalChargeCreateForm` and `ExternalChargeRowEdit` render the SAME ten fields in the SAME order —
`status`, `charged_on`, `energy_added_kwh`, `price`, `started_at`, `ended_at`,
`start_battery_pct`, `end_battery_pct`, `location_kind`, `location_label` — in the main
grid, followed by an always-visible **"Optional details"** `<section>` (`charging_type`,
`odometer_km`, `notes`). The edit row appends one edit-only extra after the shared ten:
the read-only Vehicle display. (`location_label` moved from the optional section into the
main grid on 2026-09-01, closing the grid behind `location_kind`.)

Field order and section placement are **no longer pinned by a test** (MAG-39) — they are
verified by looking at the page. The list above is the description to keep current, not an
assertion.

- **No `<details>`/`<summary>` collapse on either form — do not reintroduce one.** It
  previously hid `location_kind` (always required) and `ended_at` (required when status is
  DONE) in the edit row, and a browser **cannot report an HTML5 validation message on a
  control inside a closed `<details>`** — Chrome logs *"An invalid form control with
  name='location_kind' is not focusable"* and the submit silently does nothing: no message,
  no request. If a future field must be tucked away, it has to be unconditionally optional,
  and the section stays open. **This one IS still tested** —
  `TestExternalChargeForms_NoDetailsCollapse` (`handlers/external_charges_test.go`), because the failure is
  invisible rather than ugly.
- **`location_kind` is required and belongs in the main grid** — never in the optional
  section; the optional `location_label` follows it as the grid's final field.
- **The section heading carries the optional signal for its own three fields**; the
  per-field `ui.FieldProps.Optional` hint marks the optional fields that live in the MAIN
  grid (`energy_added_kwh`, `price`, `started_at`, `location_label`), so the two signals
  never duplicate each other.
- **`location_label` is disabled unless `location_kind` is `OTHER`** (RD14) — the server
  renders the initial `disabled` state and the `static/app.js` listener keeps it live on
  select change; a disabled input is not submitted, so a label typed under HOME/WORK is
  dropped at save (deliberate).
- **No Currency input.** Currency is not user-supplied; the price carries a fixed COP
  suffix instead. Pinned by `TestExternalChargeForms_NoCurrencyField`.

## Manual charge edit: a successful save returns the whole list, retargeted

`ExternalChargeRowUpdate`'s SUCCESS path answers with the **whole `#external-charges-list` fragment** plus
`HX-Retarget: #external-charges-list` / `HX-Reswap: outerHTML`, rendered under
`defaultExternalChargesWindow(today)` so the user lands back on the **"last 7 days"** preset.
`defaultExternalChargesWindow` returns exactly that preset's `(today-6, today)` range, so
`buildExternalChargesPresets` marks it `Active` by its own exact-match rule — nothing hardcodes a
preset index or label.

- **Never pair a top-level `<tr>` with a non-table `hx-swap-oob` sibling in one response.**
  See the module-wide version of this trap in `internal/gateway/AGENTS.md` — it applies to
  every page, not just this one. This path is where it was discovered.
- **Why `HX-Retarget` rather than changing the form's `hx-target`.** The form's `hx-target`
  stays `#external-charge-row-{id}`, which is correct for the 4xx/5xx branches: they re-render the
  edit row in place and preserve the user's typed values. Only the success path retargets,
  so one response element covers it with no OOB and no mixed content.
- **Only success resets the window.** The error branches still echo the POSTED window
  (`windowFromForm` → the form's hidden `start`/`end` inputs); a failed save must not move
  the user's filter. Test Contract **D3** covers the retarget + reset, **D3b** the
  error-path echo — the pair is the contract.
- **This amends the original §D-Refresh/§D-Include rule** for the update path only;
  `ExternalChargeCreate` and `ExternalChargeRowDelete` still preserve the posted window.
- **Known consequence:** an entry dated outside the last 7 days will not appear in the
  refreshed list after being edited. That is inherent to resetting the filter — the record
  is saved, it is just outside the window now shown.

**Known latent issue, not yet fixed:** `ExternalChargeCreateSuccessOOB` wraps `ExternalChargesList` (whose
own root is `<div id="external-charges-list">`) in a second `<div id="external-charges-list" hx-swap-oob=...>`,
so after a create the live DOM holds two nested elements with that id. It works today — the
OOB replaces the outer, lookups resolve to it — but it is a duplicate-id trap for anything
that later targets `#external-charges-list`. The fix is to let `ExternalChargesList`'s own root carry the OOB
attribute instead of wrapping it; do that the next time this path is touched.

## Manual charge list: only ONE row is editable at a time

`GET /ui/external-charges/row/:id/edit` (`ExternalChargeRowEditFragment`) renders the **whole `#external-charges-list`
region** with that row — and only that row — in edit mode, driven by
`ExternalChargesPageData.EditingID`. The row's Edit button therefore carries
`hx-target="#external-charges-list"`, not `hx-target="#external-charge-row-{id}"`.

- **Why the list, not the row.** When the row was its own swap target, each Edit click was
  independent, so a user could open every row at once and end up with N competing forms.
  Making the LIST the swap unit means opening a second editor necessarily re-renders the
  first one closed — the invariant holds on every render instead of depending on client-side
  bookkeeping a stray swap could desynchronize. It also needs **no new JS**, so no RD entry:
  the Delete button in the same file already targets `#external-charges-list` this exact way.
- **`EditingID` is set by `ExternalChargeRowEditFragment` and by nothing else.** Every other render
  leaves it empty, which is what closes an open editor after a successful save
  (`ExternalChargeRowUpdate`'s OOB `#external-charges-list` refresh), a delete, a filter click or a vehicle
  switch. Do not set it from `buildExternalChargesPage`.
- **Cancel still swaps the single row** (`ExternalChargeRowStatic` → `#external-charge-row-{id}`) and stays
  correct precisely because only one row can be open.
- A 404 for an id absent from the rendered window is deliberate: Edit is only reachable from
  a row the user can see, and the presence check costs no extra read (it scans the page just
  built).

## Manual charge form helper copy

Both forms carry one short line under the title, from the catalogue:
`KeyChargesFormCreateHint` (create) and `KeyChargesFormEditHint` (edit row).
**These strings state rules that live in Go**, so a change to either rule is incomplete
until the copy follows:

- the create hint states `charging.resolveEnergy`'s derivation (`service.go`) — an omitted
  energy is estimated from the battery delta × pack capacity, and **only when both
  `StartBatteryPct` and `EndBatteryPct` are present with end > start**, so an IN_PROGRESS
  entry gets no estimate until it is completed — and the IN_PROGRESS required set;
- the edit hint states `charging.RequiredFieldsFor(StatusDone)`'s extra fields (`ended_at`,
  `end_battery_pct`).

## Manual charge rule: one IN_PROGRESS entry per (vehicle, charged_on)

A vehicle may have at most **one** manual charge entry with status `IN_PROGRESS` on any
given `charged_on` date. `DONE` entries are unconstrained — any number may share a date.
Enforced in `handlers.inProgressConflictOn` (`handlers/external_charges.go`) on **both** write paths:
`ExternalChargeCreate` (`POST /ui/external-charges/create`, excluding nothing) and `ExternalChargeRowUpdate`
(`PUT /ui/external-charges/row/:id`, excluding the edited row's own id so an already-in-progress
entry never conflicts with itself). A conflict is reported through the SAME 422 branch as
every other validation failure — `validationErrors["_top"]`, so the user's submitted values
survive the re-render — carrying `i18n.KeyChargesErrorInProgressExists` formatted with the
conflicting date as `YYYY-MM-DD`.

- **No new port.** The check reads
  `charging.Reader.ListEntriesByVehicleBetween(chargedOn, chargedOn)` — the same port every
  list render already uses, with both bounds on the single day in question. Do NOT add a
  status-filtered method to `charging.Reader` for this; the day's entry count is small and
  the read is already bounded. The helper nonetheless **re-asserts the calendar day on every
  returned row** instead of trusting the port's window — a write-blocking rule must not
  depend on a read port's filtering being exact, and the two sides carry different time
  components (form-parsed UTC midnight vs. the `DATE` column's round-trip).
- **There is NO database constraint behind this rule** — it is an application-level rule, so
  the check **fails open**: a reader error is logged and the write proceeds, matching this
  module's log-and-continue posture for non-essential follow-ups
  (`recalculateAfterExternalChargeWrite`, the telemetry suggestion lookup in `buildExternalChargesPage`).
  Turning a transient read failure into a refusal to save would trade a real data loss for a
  hypothetical duplicate. If this ever needs to be airtight, the fix is a partial unique
  index in the `charging` module, not a fail-closed gateway check.
- **Only `IN_PROGRESS` submissions are checked** — a `DONE` submission returns without
  reading anything.

## Manual charge success notice

`fragments.ExternalChargesPageData.Notice` is the success counterpart of `.Error`: a non-empty value
renders a `ui.Alert{Kind: "success"}` at the top of the create-form card (the same slot the
`_top` validation alert uses). It is set in exactly ONE place — `ExternalChargeCreate`'s success
path, to `i18n.KeyChargesNoticeEntryCreated` — so it rides in on the response to the write
that earned it via the primary `#external-charges-create-form` swap and is gone on the next render of
any kind. `buildExternalChargesPage` never sets it; do not set it from a read path, or the message
will persist across refreshes.

## Client-side JS on this page — RD12, RD13, RD14

Three of the gateway's six sanctioned exceptions to the **zero-JS** rule belong to this
page's forms. All three live in `static/app.js`, all three delegate on `document.body`, and
all three are narrow exceptions — **not** a precedent. Any further client-side JS needs its
own RD entry per RD8, with its own rationale and rejected alternative. The register of all
six lives in `internal/gateway/AGENTS.md` under RD9.

### RD12 — date→time-preserving sync

A `change` listener matching `input[name="charged_on"]`. On fire, it looks up
`started_at`/`ended_at` within `evt.target.closest("form")` and, for each that is
**non-empty**, rewrites only the date portion: `input.value = newDate + input.value.slice(10)`.
A `datetime-local` value is always `YYYY-MM-DDTHH:MM`, so `.slice(10)` is exactly `"THH:MM"` —
the time half is preserved verbatim. An empty field is left empty; the listener never
auto-fills one (roadmap D12's explicit rejection of "clobber to midnight"). Added by
`RM33-gateway-update-charge-form` (MAG-18, roadmap D12).

**Why:** editing the date otherwise leaves the time fields pointing at the *old* date while
displaying only a time — an easy way to silently record a charge on the wrong day. The
**rejected alternative** was a CSS-only DaisyUI pattern: this is a value *transformation*
(splicing one field's substring into another's), which no CSS mechanism can express.

**Degradation:** if the fields are empty, or the input is not inside a `<form>`, it no-ops.
Without JS the fields keep whatever the user last typed; the server derives neither field
from the other.

### RD13 — status-driven required toggle

A `change` + `htmx:load` listener pair matching `select[name="status"]`. Both call
`applyChargeStatusRequiredToggle(select)`, which resolves `select.closest("form")` and sets
`ended_at.required` / `end_battery_pct.required` to `select.value === "DONE"`. Running on
`htmx:load` as well as `change` means a freshly-rendered or freshly-swapped form is correct
immediately, not only after the user's first interaction — the literal ask in D-RM33-6.
Added by `RM33-gateway-update-charge-form` (MAG-18, roadmap D-RM33-6).

This deliberately duplicates the server-rendered initial `required` state computed from
`charging.RequiredFieldsFor`. If the two ever disagree the JS state wins in the live DOM,
and **the disagreement is inert**.

**Why:** D-RM33-6 explicitly asks for "no htmx round-trip" — the user must see the required
asterisk change the instant they pick `DONE`. The **rejected alternative** was letting a
status change take effect only after a full submit/re-render, which is the round-trip
D-RM33-6 asks to avoid.

**Why it does not erode the `ui/` boundary:** the listener only reads `select.value` and
writes a native DOM `.required` boolean — no DaisyUI class string, no markup, no styling
decision is made in JavaScript.

**Degradation:** if a matched `<select>` has no enclosing `<form>`, or the form lacks either
input, the helper no-ops on the missing piece (`if (endedAt) endedAt.required = isDone`).
Without JS the server-rendered initial state still governs at submit time.

### RD14 — location-kind-driven label toggle

A `change` + `htmx:load` listener pair matching `select[name="location_kind"]`, calling
`applyChargeLocationLabelToggle(select)`, which resolves `select.closest("form")` and sets
`location_label.disabled = select.value !== "OTHER"`. The server renders the same initial
state (`Disabled: LocationKind != "OTHER"` on `ui.InputProps` in both
`external_charge_create_form.templ` and `external_charge_row_edit.templ`); if the two disagree the JS state
wins in the live DOM and the disagreement is inert. Added 2026-09-01, alongside
`location_label`'s move into the main grid.

The `change` path additionally **focuses** the input the moment it is enabled (picking OTHER
is the only path that enables it, and the user's next action is typing into it); the
`htmx:load` path deliberately does **not** focus — a page load or row swap must never steal
focus from where the user already is.

**Why:** the free-text label is only meaningful when the kind is `OTHER`; a live text field
next to HOME/WORK invites noise data. The **rejected alternative** was the server-only
`disabled` attribute with no round-trip: on the create form the select change never
re-renders, so the input could never be enabled at all. The second rejected alternative was
an htmx round-trip on select change, rejected for the same reason RD13 rejected it.

**Consequence (deliberate):** a `disabled` input is **not submitted**. If a user types a
label and then switches the kind to HOME/WORK, the save silently drops it.

## Related KB

- `architecture/charge-record-mutation.md` — the shared write contract and its known divergences
- `workflows/manual-charge-crud.md` — the read side and the create path
- `input-port/charging/supercharger-stats.md` — the sibling page
