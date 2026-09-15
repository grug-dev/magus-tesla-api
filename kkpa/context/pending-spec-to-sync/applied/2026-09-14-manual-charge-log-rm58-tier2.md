# Sync proposal — manual-charge-log

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `workflows/manual-charge-crud.md`
Source spec:  `openspec/specs/manual-charge-log/spec.md`
Generated:    2026-09-14
Status: APPLIED 2026-09-14

---

Scope of this delta: the four requirements RM58 tier 2 changed — "Edit an existing entry",
"Delete an entry", "Multi-tenant isolation" and "List entries by vehicle". The guide already
carries most of the vehicle-scoping story (tier 2's own doc task wrote it). Three rules the
spec states are still missing, and the `Delete:` flow line is now stale.

## [guide] ## How maintenance works — REPLACE

Order of operations for the common changes. Reference the files above by path.

- **Add a field to the manual charge entry:** `internal/charging/db/query.sql` (+ migration for `manual_charge_entries`) → `make sqlc` → `internal/charging/service.go` (pgtype mapping in `writerService` + `rowToEntry`) → `internal/charging/charging.go` (`Entry` DTO) → `internal/gateway/handlers/external_charges.go` (`parseExternalChargeForm` validation + `externalChargeEntryVMFromEntry` mapper) → `external_charges_vm.go` (`ExternalChargeEntryVM`) → the three `.templ` fragments (form/row/edit-row) → `make templ && make css` → i18n keys in `internal/gateway/i18n/catalog.go` (ES + EN, same line) for any new label.
- **Create:** `ExternalChargeCreate` — auth guard → `checkCSRF` → `acct.RegisteredVehicles` (tenant ownership of the submitted `tesla_id`+`vin`) → `parseExternalChargeForm` → `chargingWriter.Create` → `recalculateAfterExternalChargeWrite(new ChargedOn)` → `ExternalChargeCreateSuccessOOB` render (list refresh + reset form).
- **Read / list / search:** read handlers (`ExternalChargesPage`, `ExternalChargesListFragment`, `ExternalChargeRowStatic`, `ExternalChargeRowEditFragment`) + `buildExternalChargesPage` call ONLY `charging.Reader` (`ListEntriesByVehicle` …) scoped to the **selected TeslaID** (`resolveSelectedVehicle`); never the Writer. Reads are car-wide since `RM58-charging-demote-manual-charge-account-id`: a vehicle's entries come back whoever typed them, so a car shared by two accounts shows the same list to both.
- **Update:** `ExternalChargeRowUpdate` — guards as above → `fetchEntryTeslaIDAndChargedOn` FIRST (captures the OLD `ChargedOn`; unrecoverable after commit) → `chargingWriter.Update` (double-scoped `WHERE id AND tesla_id`, guarded by a `vehicleref.Ref` the caller must already hold; immutable `tesla_id`/`vin`/`created_at` untouched) → `recalculateAfterExternalChargeWrite(new ChargedOn)` AND, if the date changed, again for `oldChargedOn` → render static `ExternalChargeRow`.
- **Delete:** `ExternalChargeRowDelete` — guards → `fetchEntryTeslaIDAndChargedOn` first (Delete returns no row; a lookup miss now answers 404, it no longer falls through to the delete) → `chargingWriter.Delete` (double-scoped `WHERE id AND tesla_id`, guarded by a `vehicleref.Ref` the caller must already hold) → `recalculateAfterExternalChargeWrite(deleted row's ChargedOn)` → render `ChargeRowEmpty` (htmx outerHTML swap removes the row). **A delete that matches no row returns an error wrapping `pgx.ErrNoRows`, not success.** `DeleteEntry` is `:execrows` and the service turns zero rows into that error, so a rejected delete cannot look like an accepted one.
- **Status is parsed FIRST on both write paths.** `parseExternalChargeForm` resolves the submitted `status` to a `charging.Status` before any field-presence check, then drives the required set from `charging.RequiredFieldsFor(status)`. A missing or unrecognized status is itself a validation error, so it short-circuits before the per-field checks ever run — which is why every form fixture in a test must carry an explicit `status`.
- **Error re-render (422/500) preserves the whole submission.** Both `ExternalChargeCreate` and `ExternalChargeRowUpdate` rebuild the fragment from the raw submitted values rather than from fresh-load defaults: create overwrites `ExternalChargesPageData.FormValues` (plus the three date defaults) and calls `applyRawRequiredState`; the edit row goes through `externalChargeEntryVMFromRawValues`. Both recompute the `Required*` pair from the **submitted** status, so the served HTML's `required` attributes match the status being rendered. Keep both halves in step when touching either handler.
- **An entry completes in two ways, and one of them is silent.** Moving the edit row's status control `IN_PROGRESS` → `DONE` is the explicit way, and `DONE` → `IN_PROGRESS` is the only way to reopen one. There is no separate "complete" button or endpoint. Since RM51 there is also a silent way: `promoteIfComplete` promotes any submitted `IN_PROGRESS` entry to `DONE` when `ended_at` and `end_battery_pct` are both present, on **both** `Create` and `Update`. So filling those two fields while leaving the control on `IN_PROGRESS` still stores `DONE`. Reopening therefore only holds if the user also clears one of them.

## [guide] ## Conventions & gotchas — APPEND

- **The write proof is a value this module cannot build for itself.** `Writer.Update` and
  `Writer.Delete` take a `vehicleref.Ref`, and only the gateway's `authorizeVehicle` can
  construct one — `make vehicleref-guard` enforces that. So a call site that forgot to prove
  ownership is a compile error, not a security hole. Never add an overload that accepts a raw
  `tesla_id`, and never build a `Ref` inside `internal/charging`.
  _Source: spec manual-charge-log — Requirement: Multi-tenant isolation._
- **A write is never reachable by entry id alone.** `WHERE id = $1 AND tesla_id = $2` is the
  second line of defence behind the `Ref`. An update or delete that proves the wrong car
  changes nothing. Do not "simplify" either query back to `WHERE id = $1`.
  _Source: spec manual-charge-log — Requirement: Multi-tenant isolation._
- **A rejected delete is an error, never a silent success.** `DeleteEntry` is `:execrows`;
  `Delete` wraps `pgx.ErrNoRows` when zero rows matched. Before this, a delete naming the wrong
  car returned no error and the user saw the row disappear from the page. If you change
  `DeleteEntry` back to `:exec`, that bug returns.
  _Source: spec manual-charge-log — Requirement: Delete an entry._
- **`created_by_account_id` is never re-derived from the proof of ownership.** An update proves
  a vehicle, not an author. Whoever created the entry stays recorded, whichever co-owner edits
  it later. Do not set this column on an update path.
  _Source: spec manual-charge-log — Requirement: Edit an existing entry._
- **A co-owner of a shared car may now edit and delete the other account's entry.** This is
  intended, not a leak: reads were already car-wide, and the write path now matches. A test that
  asserts a co-owner is refused is asserting the old behaviour.
  _Source: spec manual-charge-log — Requirement: Edit an existing entry; Requirement: Delete an entry._
