# Manual charge CRUD (charging.Writer + analytics recalc hook) — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.

## Glossary

- **Known as:** `manual charge`, `charges form`, `charge row`, `editing a manual charge`, `External charges page`, `/external-charges`, `/ui/external-charges`, `inferred capacity`, `inferred pack capacity`, `entry status`, `charge status`, `in progress charge`, `energy source`, `energy provenance`, `price source`, `price provenance`, `zero price confirmation`, `odometer reading`, `charge authorship`, `entry author`, `created_by_account_id`
- **Internal name:** `ExternalChargeCreate` / `ExternalChargeRowUpdate` / `ExternalChargeRowDelete` (gateway handlers) → `charging.Writer` port → `analytics.Recalculator.Recalculate` hook — table `manual_charge_entries`, downstream table `vehicle_metrics`. The inferred pack capacity is `charging.Entry.InferredCapacityKWhCalc` (`*float64`), backed by the database column `manual_charge_entries.inferred_capacity_kwh_calc`. The lifecycle status is `charging.Status` (`StatusInProgress` / `StatusDone`) on `charging.Entry.Status`, its required-field rule is `charging.RequiredFieldsFor`, the energy provenance is `charging.EnergySource` (`EnergySourceUser` / `EnergySourceEstimated`) on `charging.Entry.EnergySource`, and the price provenance is `charging.PriceSource` (`PriceSourceUser` / `PriceSourceUnconfirmed`) on `charging.Entry.PriceSource`, driven by the write-only `charging.Entry.PriceConfirmed`. Authorship is `charging.Entry.CreatedByAccountID`, backed by `manual_charge_entries.created_by_account_id`.

## Component map

Files involved, grouped by layer. Each row: the file's role in this concept.

### Gateway — routes & handlers

| File | Role |
|---|---|
| `internal/gateway/gateway.go` | Registers the routes: `POST /ui/external-charges/create`, `GET/PUT/DELETE /ui/external-charges/row/:id` (plus read-side GETs below). |
| `internal/gateway/handlers/external_charges.go` | The whole slice: `ExternalChargeCreate`, `ExternalChargeRowUpdate`, `ExternalChargeRowDelete` (writes), `ExternalChargesPage`/`ExternalChargesListFragment`/`ExternalChargeRowStatic`/`ExternalChargeRowEditFragment` (reads), and the helpers `parseExternalChargeForm` (validation), `fetchEntryTeslaIDAndChargedOn` (pre-write old-date lookup), `recalculateAfterExternalChargeWrite` (post-write analytics hook), `buildExternalChargesPage` (gin-free page VM builder). |
| `internal/gateway/handlers/handlers.go` | `Deps` wiring: `ChargingWriter charging.Writer`, `ChargingReader charging.Reader`, `AnalyticsRecalculator analytics.Recalculator` ports on `Handler`. |

### Gateway — templates (view)

| File | Role |
|---|---|
| `internal/gateway/templates/pages/external_charges.templ` | The `/external-charges` page shell (`External charges`). |
| `internal/gateway/templates/fragments/external_charges_vm.go` | `ExternalChargesPageData` / `ExternalChargeEntryVM` presentation models. |
| `internal/gateway/templates/fragments/external_charge_create_form.templ` | Create form (`hx-post` → `/ui/external-charges/create`). |
| `internal/gateway/templates/fragments/external_charge_row.templ` | Static row with `hx-get …/edit` + `hx-delete` actions. |
| `internal/gateway/templates/fragments/external_charge_row_edit.templ` | Edit row form (`hx-put` → `/ui/external-charges/row/:id`); also `ChargeRowError` / `ChargeRowEmpty` response variants. |
| `internal/gateway/templates/fragments/charges_content.templ` | The `#external-charges-content` swap region (re-fetches on `vehicle-changed`). |

### Charging module — port & domain

| File | Role |
|---|---|
| `internal/charging/charging.go` | Public ports: `Writer` (`Create`/`Update`/`Delete`) and `Reader` (list queries); constructors `NewWriter(pool)` / `NewReader(pool)`. |
| `internal/charging/service.go` | `writerService` / `readerService` — the ONLY place pgtype conversions happen; `location_kind` required-ness enforced here before any DB call. Also holds `resolveEnergy` and `resolvePriceSource`, the two module-computed provenance rules. |
| `internal/charging/db/query.sql` | sqlc source of truth: `CreateEntry`, `UpdateEntry` (`:one`, `WHERE id AND tesla_id`), `DeleteEntry` (`:execrows`, same `WHERE`), list queries. The `tesla_id` guard on `Update`/`Delete` is the second line of defence behind the `vehicleref.Ref` the caller must already hold (`RM58-charging-entry-writes-take-ref`) — no longer transitional. |
| `internal/charging/validation.go` | `RequiredFieldsFor(Status) []Field` — the SINGLE source of truth for which fields a status demands — plus `normalizeStatus` (`""` → `IN_PROGRESS`), `missingFields`, and `promoteIfComplete` (a complete `IN_PROGRESS` entry is promoted to `DONE` on write, RM51). Change a required-field rule HERE, not in the gateway and not as a DB `CHECK`. |
| `internal/charging/capacity.go` | `packCapacityKWh(ctx, lookup, teslaID)` — the pack-capacity seam. Since RM52 tier 1 (MAG-32) it reads the vehicle's newest measured capacity from `charging.monthly_effective_capacity` through the `packCapacityLookup` interface, and falls back to `defaultPackCapacityKWh` (`62.0`) only while that vehicle has no measured month. Also holds the pure `derivedEnergyKWh(capacity, startPct, endPct)`. |
| `internal/charging/db/models.go` | `ManualChargeEntry` row model (generated-style, pgtype fields). |
| `internal/charging/db/query.sql.go` | sqlc output — regenerate via `make sqlc`, never hand-edit. |

### Analytics module — post-write recalculation

| File | Role |
|---|---|
| `internal/analytics/analytics.go` | `Recalculator` interface (`Recalculate(ctx, accountID, teslaID, start, end)`, `Reconcile`). |
| `internal/analytics/recalculate.go` | `recalculator` impl + `NewRecalculator(pool, telemetryReader, supercharger, manual)`; UPSERTs `vehicle_metrics` rows for the affected day(s), deletes stale ones; idempotent. |

### Wiring / composition root

| File | Role |
|---|---|
| `cmd/web` | Injects `charging.NewWriter(pool)`, `charging.NewReader(pool)`, `analytics.NewRecalculator(pool, …)` into `gateway.Deps`. |

## How maintenance works

Order of operations for the common changes. Reference the files above by path.

- **Add a field to the manual charge entry:** `internal/charging/db/query.sql` (+ migration for `manual_charge_entries`) → `make sqlc` → `internal/charging/service.go` (pgtype mapping in `writerService` + `rowToEntry`) → `internal/charging/charging.go` (`Entry` DTO) → `internal/gateway/handlers/external_charges.go` (`parseExternalChargeForm` validation + `externalChargeEntryVMFromEntry` mapper) → `external_charges_vm.go` (`ExternalChargeEntryVM`) → the three `.templ` fragments (form/row/edit-row) → `make templ && make css` → i18n keys in `internal/gateway/i18n/catalog.go` (ES + EN, same line) for any new label.
- **Create:** `ExternalChargeCreate` — auth guard → `checkCSRF` → `acct.RegisteredVehicles` (tenant ownership of the submitted `tesla_id`+`vin`) → `parseExternalChargeForm` → `chargingWriter.Create` → `recalculateAfterExternalChargeWrite(new ChargedOn)` → `ExternalChargeCreateSuccessOOB` render (list refresh + reset form).
- **Read / list / search:** read handlers (`ExternalChargesPage`, `ExternalChargesListFragment`, `ExternalChargeRowStatic`, `ExternalChargeRowEditFragment`) + `buildExternalChargesPage` call ONLY `charging.Reader` (`ListEntriesByVehicle` …) scoped to the **selected TeslaID** (`resolveSelectedVehicle`); never the Writer. Reads are car-wide since `RM58-charging-demote-manual-charge-account-id`: a vehicle's entries come back whoever typed them, so a car shared by two accounts shows the same list to both.
- **Update:** `ExternalChargeRowUpdate` — guards as above → `fetchEntryTeslaIDAndChargedOn` FIRST (captures the OLD `ChargedOn`; unrecoverable after commit) → `chargingWriter.Update` (double-scoped `WHERE id AND tesla_id`, guarded by a `vehicleref.Ref` the caller must already hold; immutable `tesla_id`/`vin`/`created_at` untouched) → `recalculateAfterExternalChargeWrite(new ChargedOn)` AND, if the date changed, again for `oldChargedOn` → render static `ExternalChargeRow`.
- **Delete:** `ExternalChargeRowDelete` — guards → `fetchEntryTeslaIDAndChargedOn` first (Delete returns no row; a lookup miss now answers 404, it no longer falls through to the delete) → `chargingWriter.Delete` (double-scoped `WHERE id AND tesla_id`, guarded by a `vehicleref.Ref` the caller must already hold) → `recalculateAfterExternalChargeWrite(deleted row's ChargedOn)` → render `ChargeRowEmpty` (htmx outerHTML swap removes the row). **A delete that matches no row returns an error wrapping `pgx.ErrNoRows`, not success.** `DeleteEntry` is `:execrows` and the service turns zero rows into that error, so a rejected delete cannot look like an accepted one.
- **Status is parsed FIRST on both write paths.** `parseExternalChargeForm` resolves the submitted `status` to a `charging.Status` before any field-presence check, then drives the required set from `charging.RequiredFieldsFor(status)`. A missing or unrecognized status is itself a validation error, so it short-circuits before the per-field checks ever run — which is why every form fixture in a test must carry an explicit `status`.
- **Error re-render (422/500) preserves the whole submission.** Both `ExternalChargeCreate` and `ExternalChargeRowUpdate` rebuild the fragment from the raw submitted values rather than from fresh-load defaults: create overwrites `ExternalChargesPageData.FormValues` (plus the three date defaults) and calls `applyRawRequiredState`; the edit row goes through `externalChargeEntryVMFromRawValues`. Both recompute the `Required*` pair from the **submitted** status, so the served HTML's `required` attributes match the status being rendered. Keep both halves in step when touching either handler.
- **An entry completes in two ways, and one of them is silent.** Moving the edit row's status control `IN_PROGRESS` → `DONE` is the explicit way, and `DONE` → `IN_PROGRESS` is the only way to reopen one. There is no separate "complete" button or endpoint. Since RM51 there is also a silent way: `promoteIfComplete` promotes any submitted `IN_PROGRESS` entry to `DONE` when `ended_at` and `end_battery_pct` are both present, on **both** `Create` and `Update`. So filling those two fields while leaving the control on `IN_PROGRESS` still stores `DONE`. Reopening therefore only holds if the user also clears one of them.

## Conventions & gotchas

- **The write handlers are the ONLY sanctioned gateway writes** — the documented D4 exception in `internal/gateway/AGENTS.md`: auth guard → CSRF → `RegisteredVehicles` ownership check → `charging.Writer` only. Every other handler stays Reader-only. _Source: `internal/gateway/AGENTS.md` → "Exception: user-initiated writes"._
- **Resolve the old `ChargedOn` BEFORE calling Update/Delete** — once the write commits the old date is gone (Delete doesn't return the row either); a lookup miss just means no extra day to recalculate, the write still proceeds. _Source: `internal/gateway/handlers/external_charges.go` comments, design.md D5 "Manual Charge Write Path Triggers Analytics Recalculation"._
- **Date-changing edits recalculate TWO days** — new `ChargedOn` and old `ChargedOn` both get `Recalculate`; keep both calls when touching `ExternalChargeRowUpdate`. _Source: `ExternalChargeRowUpdate`._
- **Recalc errors are log-only** — `recalculateAfterExternalChargeWrite` logs and returns; the user's write already succeeded and must never fail because analytics hiccupped. _Source: `recalculateAfterExternalChargeWrite`._
- **CSRF on DELETE travels as a header/query, not the body** — Go's `net/http` doesn't parse DELETE bodies, so the row's token must reach `checkCSRF` via the form-encoded URL or `X-CSRF-Token` (root cause of the MAG-5 403-alert bug). _Source: `ExternalChargeRowDelete` doc comment, T1.1._
- **Non-2xx fragment bodies go through `renderError`** — htmx never swaps a plain 4xx/5xx body; without `HX-Error-Fragment` the response is invisible (silent 422). _Source: `internal/gateway/AGENTS.md` → "Non-2xx error fragments"._
- **Energy added is OPTIONAL and may be DERIVED on write** — `energy_added_kwh` is nullable since MAG-18/RM33. When the caller leaves `Entry.EnergyAddedKWh` nil, `writerService` derives it as `packCapacityKWh × (end_battery_pct − start_battery_pct)/100` and stamps `energy_source = 'ESTIMATED'`; otherwise it stores what the caller gave and stamps `'USER'`. **`Entry.EnergySource` supplied by a caller is ignored and overwritten** — provenance is the module's to assert, the same shape `supercharger_sessions.battery_pct_source` (renamed from `charge_sessions.battery_pct_source`, RM39 tier 3) already uses. No derivation happens when either percentage is missing or the delta is not positive; the value stays NULL. _Source: `internal/charging/service.go` → `resolveEnergy`, `internal/charging/capacity.go`._
- **Price provenance is DERIVED on write** — `price_source` (`USER` / `UNCONFIRMED`) records whether a zero price is a real free charge or a price nobody entered. Added in RM51/MAG-58, `TEXT NOT NULL DEFAULT 'UNCONFIRMED'` with a `CHECK`, not indexed. The rule: a price above zero is always `USER`; a zero price is `USER` only when the caller sets `Entry.PriceConfirmed`, and `UNCONFIRMED` otherwise. **`Entry.PriceSource` supplied by a caller is ignored and overwritten**, the same contract `energy_source` already uses. `PriceConfirmed` is write-only intent and is not stored: a read always returns it `false`. _Source: `internal/charging/service.go` → `resolvePriceSource`._
- **The pack capacity is MEASURED per vehicle, not hardcoded** — `packCapacityKWh(ctx, lookup, teslaID)` in `internal/charging/capacity.go` reads the vehicle's newest measured capacity from `charging.monthly_effective_capacity`. It returns `defaultPackCapacityKWh` (`62.0`) only while that vehicle has no measured month yet, so 62 kWh is now a fallback, not a placeholder. RM52 tier 1 (MAG-32) closed backlog **#18**, which asked for exactly this. **The monthly job that fills the table filters `WHERE energy_source = 'USER'`** when averaging `inferred_capacity_kwh_calc`: on a derived row the generated column returns exactly the capacity constant by algebra (`(C × d/100) / (d/100) = C`), so including derived rows would average the seed value back into itself and never converge on the pack's real capacity. This is the whole reason `energy_source` is stored. _Source: `internal/charging/capacity.go` `TODO(MAG-18)`, migration `20260829000002_add_entry_status.sql`._
- **Required fields depend on the entry's `status`** — `IN_PROGRESS` requires only `charged_on` + `location_kind`; `DONE` additionally requires `ended_at` + `end_battery_pct`. The rule lives in `charging.RequiredFieldsFor` and is deliberately NOT a database `CHECK`, so changing the skip set is a Go edit rather than a migration. The gateway drives which inputs render as required from the same function. _Source: `internal/charging/validation.go`._
- **A complete `IN_PROGRESS` entry is PROMOTED to `DONE` on write** — since RM51/MAG-58, `Writer.Create` and `Writer.Update` both promote an entry whose submitted status is `IN_PROGRESS` when every field `RequiredFieldsFor(StatusDone)` demands is present. Promotion only: a `DONE` entry is never demoted. The helper reuses `missingFields` against `RequiredFieldsFor(StatusDone)`, so adding a DONE-required field tightens promotion with no other edit. Consequence: a user cannot keep a complete entry open. _Source: `internal/charging/validation.go` → `promoteIfComplete`._
- **No pgtype outside `internal/charging`** — `writerService`/`readerService` confine all pgtype conversion; the gateway sees only the pure `Entry` DTO. NEVER import `internal/charging/db` from the gateway. _Source: `internal/charging/service.go`, `internal/gateway/AGENTS.md`._
- **Reads are vehicle-scoped by the selected TeslaID** — per-vehicle reads must go through `resolveSelectedVehicle`; defaulting to `registered[0]` is a tenancy-correctness bug. _Source: `internal/gateway/AGENTS.md` → "Vehicle-scoped reads"._
- **Any new user-facing label needs ES + EN catalogue keys** — enforced by `TestCatalog_AllKeysHaveBothLanguages` and `make i18n-guard`. _Source: `internal/gateway/AGENTS.md` → i18n._
- **Regeneration:** `.templ` → `make templ`; new CSS classes → `make css` (commit `app.css` in the same change); `queries.sql` → `make sqlc`. _Source: `internal/gateway/AGENTS.md` → regeneration cheatsheet._
- **The gateway renders `required` from `charging.RequiredFieldsFor`, never from its own rule** — `ExternalChargesPageData` (create) and `ExternalChargeEntryVM` (edit row) each carry a `RequiredEndedAt` / `RequiredEndBatteryPct` pair the HANDLER computes; templates hold no business logic. On an error re-render the pair must be recomputed from the SUBMITTED status, or the form comes back with `required` attributes describing a status the user is no longer on. _Source: spec gateway — Requirement: Create Charge Entry._
- **`start_battery_pct` is required at EVERY status; `ended_at` / `end_battery_pct` only at `DONE`** — the first has no `charging.Field` constant and is therefore outside `RequiredFieldsFor`'s domain, so it stays unconditionally required in the gateway. Do not "unify" it into the status-gated set. _Source: spec gateway — Requirement: Create Charge Entry._
- **`energy_added_kwh` and `price` are optional at the gateway, and their empty cases differ** — empty energy is stored as ABSENT (never a fabricated zero, so the charging module's derivation can fire); empty price is stored as `0`. A supplied `energy_added_kwh` of `0` and a supplied `price` of `-1` are both still validation errors. _Source: spec gateway — Requirement: Create Charge Entry._
- **There is no Currency field on either form — `COP` is a suffix on the price input** — rendered via `ui.InputProps.Suffix` (DaisyUI v5's compound-`label` idiom). `currency` remains fixed to `COP` server-side regardless of anything submitted. Do not reintroduce a Currency input, disabled or otherwise. _Source: spec gateway — Requirement: Create Charge Entry._
- **The odometer field is optional and lives inside "More details"** — grouped with charging type, location label and notes, as a whole-kilometre integer; negative or non-integer values are field-level validation errors. _Source: spec gateway — Requirement: Charge form odometer field._
- **AC/DC options carry descriptive text, but the submitted values stay `AC` / `DC`** — the labels explain slow home/destination vs fast Supercharger charging and exist in both ES and EN. Changing the label text must never change the option `value`. _Source: spec gateway — Requirement: Charging type option text explains AC and DC._
- **Changing the charge date rewrites only the DATE half of the session timestamps** — the time-of-day portion of a non-empty `started_at` / `ended_at` is preserved, and an EMPTY one stays empty rather than being populated. This is client-side JS (recorded as RD12 in `internal/gateway/AGENTS.md`), one of the module's few sanctioned exceptions to the no-client-side-JS rule. _Source: spec gateway — Requirement: Charge date change keeps the time of day on start and end timestamps._
- **A second sanctioned JS exception (RD13) toggles `required` live when the status control changes** — it fires with no network request, and the server-side `RequiredFieldsFor` gate remains authoritative. The JS is a UX affordance, never the validation. Both RD12 and RD13 live in the single shared `internal/gateway/static/app.js`. _Source: spec gateway — Requirement: Create Charge Entry; `internal/gateway/AGENTS.md` RD12/RD13._
- **Writing a negative test for these forms: assert the specific field message, not just the 422** — because a missing `status` is itself a validation error, a fixture that omits it produces a 422 for the WRONG reason, and a test asserting only the status code stays green even if the check it names is deleted. Supply a valid `status` and assert the field's own i18n message. _Source: spec gateway — Requirement: Create Charge Entry (status parsed first); RM33 tier-2 test-contract fixture convention._

- **The inferred pack capacity is derived by the database, never by Go.** It is a
  `GENERATED ALWAYS AS (…) STORED` column, so it is correct on every write path — including
  write paths added in future — with no caller action. Do not add a Go-side computation, and do
  not name the column in any `INSERT`/`UPDATE` column list.
  _Source: spec manual-charge-log — Requirement: Inferred pack capacity is recorded on every entry._
- **The column is unwritable, and that is enforced by the engine.** A caller cannot set, override,
  or corrupt it; a direct write fails with `column "inferred_capacity_kwh_calc" can only be
  updated to DEFAULT` (SQLSTATE `428C9`). Setting the field on the struct passed to
  `Writer.Create` / `Writer.Update` is silently ignored, exactly as `ID` / `CreatedAt` /
  `UpdatedAt` already are.
  _Source: spec manual-charge-log — Requirement: Inferred pack capacity is recorded on every entry._
- **A recorded absence is normal, not an error.** The value exists only when the entry has all
  three inputs (energy added, start %, end %) **and** the end percentage is strictly greater than
  the start. Otherwise it is `NULL` / `nil`, and the entry still creates and edits successfully.
  The strict `>` matters: an equal delta would be a division by zero that would otherwise *reject*
  an ordinary row, and a negative delta would store a negative "capacity", which is not a physical
  quantity.
  _Source: spec manual-charge-log — Requirement: Inferred pack capacity is recorded on every entry._
- **Editing a battery percentage silently changes the recorded capacity.** Changing the end
  percentage from 74 to 84 on a 7.04 kWh entry moves the recorded value from `70.400` to `35.200`
  with no caller involvement. Any read model or cache keyed on this value must be refreshed by the
  same hook that already handles the entry edit.
  _Source: spec manual-charge-log — Requirement: Inferred pack capacity is recorded on every entry._
- **This value is stored, unlike the capability's other derived values.** Cost per kWh, battery
  delta and session duration remain computed on read as value-receiver methods on `charging.Entry`
  and are not persisted. Do not follow their pattern when touching the inferred capacity, or the
  reverse.
  _Source: spec manual-charge-log — Requirement: Inferred pack capacity is recorded on every entry._
- **Small battery deltas produce mathematically valid but practically worthless figures.** A
  1-point delta divides by `0.01`, so a ±0.5% reading error becomes a ±50% capacity error. This is
  accepted deliberately: no minimum-delta floor exists in the column, because filtering is a
  presentation decision. Any consumer that aggregates this value should apply its own floor, or
  prefer a median over a mean.
  _Source: spec manual-charge-log — Requirement: Inferred pack capacity is recorded on every entry._
- **Naming rule for any future stored derived column: `<what>_<unit>_calc`.** That is the shape
  `internal/analytics`' `vehicle_metrics` established and this column follows; it satisfies the
  project's mandatory unit suffix while marking the column as engine-derived.
  _Source: spec manual-charge-log — Requirement: Inferred pack capacity is recorded on every entry._
- **Which fields an entry must carry is a function of its status, and that rule lives in exactly
  one place.** `RequiredFieldsFor` is the sole source of truth: the capability enforces exactly it
  on every create and every edit, for every caller, and any presentation layer deciding which
  inputs to mark required must READ it rather than restate it. Adding a field to the rule must
  stay a one-place change — if you find yourself writing a second check, you have just given the
  single source of truth a second source.
  _Source: spec manual-charge-log — Requirement: A charge entry has a recorded status that governs its required fields._
- **The rule is deliberately NOT a database CHECK.** Enforcing it in the schema would turn every
  future change to the required-field set into a migration, which is precisely what the rule is
  shaped to avoid. Do not "harden" it by adding a constraint.
  _Source: spec manual-charge-log — Requirement: A charge entry has a recorded status that governs its required fields._
- **An absent status means in progress; an unrecognized one is rejected before any write.** These
  are different outcomes for different inputs, and the distinction is what lets a caller that does
  not yet send a status keep working while a typo still fails loudly.
  _Source: spec manual-charge-log — Requirement: A charge entry has a recorded status that governs its required fields._
- **There is no transition rule — done may be reopened.** The capability does not restrict which
  status an entry moves to. Do not add a guard against done → in progress; it is explicitly
  permitted.
  _Source: spec manual-charge-log — Requirement: A charge entry has a recorded status that governs its required fields._
- **Entries that pre-date the status are recorded as in progress, on purpose.** Historical entries
  surface as unreviewed rather than being silently asserted complete. This was chosen over the
  more flattering default; it is not an oversight to "correct".
  _Source: spec manual-charge-log — Requirement: A charge entry has a recorded status that governs its required fields._
- **Energy added is optional, but zero and negative are still rejected.** Only the *absence* of a
  value became permissible. Never substitute a fabricated `0` for an unknown energy — it is both a
  lie about the charge and a constraint violation.
  _Source: spec manual-charge-log — Requirement: Energy added is optional, may be derived on write, and records its provenance._
- **Energy is derived ON WRITE and never recomputed on read.** When the caller supplies none and
  the entry has both percentages with a strictly positive difference, the capability derives the
  value from the pack capacity and stores it. In every other case it stores exactly what the
  caller gave, including nothing. Do not add a read-time fallback — that would make the same entry
  report different energy as the capacity constant changes.
  _Source: spec manual-charge-log — Requirement: Energy added is optional, may be derived on write, and records its provenance._
- **Provenance is the capability's to assert, never the caller's.** A caller-supplied energy
  source is ignored, and provenance is re-determined on every write — so replacing a derived value
  with a typed one flips it back to "from the person". Read it on the way out; never trust it on
  the way in.
  _Source: spec manual-charge-log — Requirement: Energy added is optional, may be derived on write, and records its provenance._
- **The pack capacity comes from ONE named place, and that is the point.** Replacing today's fixed
  figure with a real per-vehicle value must stay a one-place change. **Any future averaging of
  inferred capacities MUST exclude derived rows** — on a derived entry the recorded inferred
  capacity is arithmetically equal to the capacity the derivation used, so including those rows
  averages the seed value back into itself and never converges. That is the entire reason
  provenance is stored.
  _Source: spec manual-charge-log — Requirement: Energy added is optional, may be derived on write, and records its provenance._
- **An entry with no energy records no inferred capacity, and that is not an error.** The
  pre-existing inferred-capacity behaviour is unchanged by energy becoming optional; a missing
  energy simply lands in the same "no recorded capacity" case a missing percentage already did.
  _Source: spec manual-charge-log — Requirement: Energy added is optional, may be derived on write, and records its provenance._
- **The odometer reading belongs to the charge EVENT, not to the vehicle.** Two entries for the
  same vehicle carry two independent readings, which is why it lives on the entry and not on a
  vehicle record. It is optional, in whole kilometres, and negative is rejected.
  _Source: spec manual-charge-log — Requirement: An entry records the odometer reading taken at the charge event._
- **Authorship is stored and returned, but no read or write may use it.** `created_by_account_id` records which account typed the entry. Every read returns it. No read or write may filter, order, group or join by it. A read decides its result from the vehicle asked for, never from who created a row.
  _Source: spec manual-charge-log — Requirement: Charge entry authorship is recorded but never scopes a read._
- **The column is kept because authorship cannot be re-derived.** A vehicle may be registered to more than one account, and the account vehicle registry records registration, not who typed a charge. That is why this table keeps its account column while the Supercharger session store dropped its own.
  _Source: spec manual-charge-log — Requirement: Charge entry authorship is recorded but never scopes a read._
- **Read isolation is the caller's job, not this capability's.** The capability scopes every read by vehicle and trusts the vehicle ids it is given. It runs no tenant check of its own. Which vehicles a caller may see is decided before the capability is reached. A caller that passes an unfiltered vehicle set gets an unfiltered read, and nothing here will stop it.
  _Source: spec manual-charge-log — Requirement: Multi-tenant isolation._
- **An empty vehicle set means no rows, never "no filter".** `ListEntriesByVehicles` with an empty or nil slice returns a non-nil, zero-length result. Reading an empty set as "no filter" would return every row in the table.
  _Source: spec manual-charge-log — Requirement: List entries for a set of vehicles._
- **The write guard now matches the read: both are vehicle-scoped.** `Update` and `Delete` require a `vehicleref.Ref` and match on `tesla_id`, not `created_by_account_id`. A co-owner of a shared car can now edit and delete another account's entry for that car, same as it could already read it. `created_by_account_id` is authorship only — nothing predicates on it anywhere.
  _Source: spec manual-charge-log — Requirement: Charge entry authorship is recorded but never scopes a read._
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

## Related KB

- Features: (none yet)
- Workflows: `workflows/supercharger-stats-read.md` (a sibling user-write path — a single narrow correction of two battery-percentage fields over an existing `supercharger_sessions` row via `charging.SessionVerifier.VerifySession`, with no Create and no Delete, vs. this concept's full Create/Update/Delete over `manual_charge_entries`)
- Architecture: `architecture/charge-record-mutation.md` — the contract this concept's Update/Delete share with the Supercharger write path (affected period → centralized recalculation → persist → gaps), and the documented divergences between the two implementations
- Use cases: `use-case/charging/update-manual-charge.md`, `use-case/charging/delete-manual-charge.md`
- Input ports: `input-port/charging/external-charges.md`
