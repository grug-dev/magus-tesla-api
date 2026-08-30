# Manual Records page — /charges

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

- **Route / URI:** `/charges` (labelled **Manual Records** / **Registros manuales**)
- **Description:** The user's own charge log — every charge they typed in by hand, for the
  vehicle currently selected in the sidebar switcher. Create form on top, aggregation tiles, and
  a date-filtered list whose rows edit and delete inline via htmx. This is the only page in the
  app with a full create/update/delete surface.
- **Module:** `charging`

## Front-end component map

| File | Role |
|---|---|
| `internal/gateway/templates/pages/charges.templ` | Page shell — tiles, create form slot, `#charges-list` region |
| `internal/gateway/templates/fragments/charge_create_form.templ` | Create form (`hx-post` → `/ui/charges/create`) |
| `internal/gateway/templates/fragments/charges_list.templ` | The `#charges-list` region + the date-range preset selector |
| `internal/gateway/templates/fragments/charge_row.templ` | Static row; carries the Edit link and the Delete button (CSRF on the `X-CSRF-Token` header via `hx-headers`) |
| `internal/gateway/templates/fragments/charge_row_edit.templ` | Inline edit form, incl. the hidden `start`/`end` window inputs |
| `internal/gateway/templates/fragments/charges_vm.go` | `ChargesPageData` / `ChargeEntryVM` / `ChargeFormValues` presentation models |
| `internal/gateway/handlers/charges.go` | All eight handlers for this page plus `buildChargesPage` |
| `internal/gateway/handlers/charges_tiles.go` | `entryComplete` (the row status dot) + `buildChargeTiles` |
| `internal/gateway/handlers/charges_range.go` | `parseChargesRange` — the strict `?start=&end=` contract for the filter route |
| `internal/gateway/i18n/catalog.go` | Every user-facing string on this page, ES + EN |

## Endpoints

| Method + path | Purpose | Use case |
|---|---|---|
| `GET /charges` | Full page load (`ChargePage`); always uses the default window | — read path, see `workflows/manual-charge-crud.md` |
| `GET /ui/charges` | Re-render the create form + list after a vehicle switch (`ChargesContentFragment`) | — read path |
| `GET /ui/charges/list` | The date-filter endpoint (`ChargesListFragment`); `400` renders the empty-state with no filter chrome | — read path |
| `GET /ui/charges/row/:id` | Cancel-edit — swap back to the static row (`ChargeRowStatic`) | — read path |
| `GET /ui/charges/row/:id/edit` | Swap the static row for the inline edit form (`ChargeRowEditFragment`) | — read path |
| `POST /ui/charges/create` | Create a new manual entry (`ChargeCreate`) | `workflows/manual-charge-crud.md` |
| `PUT /ui/charges/row/:id` | Save an edited row | `use-case/charging/update-manual-charge.md` |
| `DELETE /ui/charges/row/:id` | Delete a row | `use-case/charging/delete-manual-charge.md` |

## Adapter-side conventions

- **CSRF token key:** `csrf_manualcharge`, issued by `ChargePage` and `ChargesContentFragment`.
  The delete button sends it on the **`X-CSRF-Token` header**, every other write in the body —
  Go does not parse bodies for `DELETE`.
- **Vehicle scope:** the list and the create form both follow `resolveSelectedVehicle`; the
  `#charges-content` region re-fetches on the sidebar's `vehicle-changed` event. There is no
  `vehicle` form field.
- **Window threading:** `?start=&end=` (or hidden form inputs) is echoed through every write so a
  save or delete never resets the user's filter. It is **best-effort only** — malformed values
  fall back to the default window and never fail a write. The one exception is
  `GET /ui/charges/list`, which rejects a malformed window with `400`.
- **i18n:** every string on this page resolves through `i18n.T(ctx, key)` with both ES and EN
  non-empty. A hardcoded string is incomplete work — `make i18n-guard` enforces it.

## Related KB

- `architecture/charge-record-mutation.md` — the shared write contract and its known divergences
- `workflows/manual-charge-crud.md` — the read side and the create path
- `input-port/charging/supercharger-stats.md` — the sibling page
