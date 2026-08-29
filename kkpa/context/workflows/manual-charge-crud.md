# Manual charge CRUD (charging.Writer + analytics recalc hook) — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.

## Glossary

- **Known as:** `manual charge`, `charges form`, `charge row`, `editing a manual charge`, `Manual Records page`, `/charges`, `/ui/charges`
- **Internal name:** `ChargeCreate` / `ChargeRowUpdate` / `ChargeRowDelete` (gateway handlers) → `charging.Writer` port → `analytics.Recalculator.Recalculate` hook — table `manual_charge_entries`, downstream table `vehicle_metrics`

## Component map

Files involved, grouped by layer. Each row: the file's role in this concept.

### Gateway — routes & handlers

| File | Role |
|---|---|
| `internal/gateway/gateway.go` | Registers the routes: `POST /ui/charges/create`, `GET/PUT/DELETE /ui/charges/row/:id` (plus read-side GETs below). |
| `internal/gateway/handlers/charges.go` | The whole slice: `ChargeCreate`, `ChargeRowUpdate`, `ChargeRowDelete` (writes), `ChargePage`/`ChargesListFragment`/`ChargeRowStatic`/`ChargeRowEditFragment` (reads), and the helpers `parseChargeForm` (validation), `fetchEntryTeslaIDAndChargedOn` (pre-write old-date lookup), `recalculateAfterChargeWrite` (post-write analytics hook), `buildChargesPage` (gin-free page VM builder). |
| `internal/gateway/handlers/handlers.go` | `Deps` wiring: `ChargingWriter charging.Writer`, `ChargingReader charging.Reader`, `AnalyticsRecalculator analytics.Recalculator` ports on `Handler`. |

### Gateway — templates (view)

| File | Role |
|---|---|
| `internal/gateway/templates/pages/charges.templ` | The `/charges` page shell (`Manual Records`). |
| `internal/gateway/templates/fragments/charges_vm.go` | `ChargesPageData` / `ChargeEntryVM` presentation models. |
| `internal/gateway/templates/fragments/charge_create_form.templ` | Create form (`hx-post` → `/ui/charges/create`). |
| `internal/gateway/templates/fragments/charge_row.templ` | Static row with `hx-get …/edit` + `hx-delete` actions. |
| `internal/gateway/templates/fragments/charge_row_edit.templ` | Edit row form (`hx-put` → `/ui/charges/row/:id`); also `ChargeRowError` / `ChargeRowEmpty` response variants. |
| `internal/gateway/templates/fragments/charges_content.templ` | The `#charges-content` swap region (re-fetches on `vehicle-changed`). |

### Charging module — port & domain

| File | Role |
|---|---|
| `internal/charging/charging.go` | Public ports: `Writer` (`Create`/`Update`/`Delete`) and `Reader` (list queries); constructors `NewWriter(pool)` / `NewReader(pool)`. |
| `internal/charging/service.go` | `writerService` / `readerService` — the ONLY place pgtype conversions happen; `location_kind` required-ness enforced here before any DB call. |
| `internal/charging/db/queries.sql` | sqlc source of truth: `CreateEntry`, `UpdateEntry` (`:one`, `WHERE id AND account_id`), `DeleteEntry`, list queries. |
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

- **Add a field to the manual charge entry:** `internal/charging/db/queries.sql` (+ migration for `manual_charge_entries`) → `make sqlc` → `internal/charging/service.go` (pgtype mapping in `writerService` + `rowToEntry`) → `internal/charging/charging.go` (`Entry` DTO) → `internal/gateway/handlers/charges.go` (`parseChargeForm` validation + `chargeEntryVMFromEntry` mapper) → `charges_vm.go` (`ChargeEntryVM`) → the three `.templ` fragments (form/row/edit-row) → `make templ && make css` → i18n keys in `internal/gateway/i18n/catalog.go` (ES + EN, same line) for any new label.
- **Create:** `ChargeCreate` — auth guard → `checkCSRF` → `acct.RegisteredVehicles` (tenant ownership of the submitted `tesla_id`+`vin`) → `parseChargeForm` → `chargingWriter.Create` → `recalculateAfterChargeWrite(new ChargedOn)` → `ChargeCreateSuccessOOB` render (list refresh + reset form).
- **Read / list / search:** read handlers (`ChargePage`, `ChargesListFragment`, `ChargeRowStatic`, `ChargeRowEditFragment`) + `buildChargesPage` call ONLY `charging.Reader` (`ListEntriesByVehicle` …) scoped to the **selected TeslaID** (`resolveSelectedVehicle`); never the Writer.
- **Update:** `ChargeRowUpdate` — guards as above → `fetchEntryTeslaIDAndChargedOn` FIRST (captures the OLD `ChargedOn`; unrecoverable after commit) → `chargingWriter.Update` (double-scoped `WHERE id AND account_id`; immutable `tesla_id`/`vin`/`created_at` untouched) → `recalculateAfterChargeWrite(new ChargedOn)` AND, if the date changed, again for `oldChargedOn` → render static `ChargeRow`.
- **Delete:** `ChargeRowDelete` — guards → `fetchEntryTeslaIDAndChargedOn` first (Delete returns no row) → `chargingWriter.Delete` → `recalculateAfterChargeWrite(deleted row's ChargedOn)` → render `ChargeRowEmpty` (htmx outerHTML swap removes the row).

## Conventions & gotchas

- **The write handlers are the ONLY sanctioned gateway writes** — the documented D4 exception in `internal/gateway/AGENTS.md`: auth guard → CSRF → `RegisteredVehicles` ownership check → `charging.Writer` only. Every other handler stays Reader-only. _Source: `internal/gateway/AGENTS.md` → "Exception: user-initiated writes"._
- **Resolve the old `ChargedOn` BEFORE calling Update/Delete** — once the write commits the old date is gone (Delete doesn't return the row either); a lookup miss just means no extra day to recalculate, the write still proceeds. _Source: `internal/gateway/handlers/charges.go` comments, design.md D5 "Manual Charge Write Path Triggers Analytics Recalculation"._
- **Date-changing edits recalculate TWO days** — new `ChargedOn` and old `ChargedOn` both get `Recalculate`; keep both calls when touching `ChargeRowUpdate`. _Source: `ChargeRowUpdate`._
- **Recalc errors are log-only** — `recalculateAfterChargeWrite` logs and returns; the user's write already succeeded and must never fail because analytics hiccupped. _Source: `recalculateAfterChargeWrite`._
- **CSRF on DELETE travels as a header/query, not the body** — Go's `net/http` doesn't parse DELETE bodies, so the row's token must reach `checkCSRF` via the form-encoded URL or `X-CSRF-Token` (root cause of the MAG-5 403-alert bug). _Source: `ChargeRowDelete` doc comment, T1.1._
- **Non-2xx fragment bodies go through `renderError`** — htmx never swaps a plain 4xx/5xx body; without `HX-Error-Fragment` the response is invisible (silent 422). _Source: `internal/gateway/AGENTS.md` → "Non-2xx error fragments"._
- **No pgtype outside `internal/charging`** — `writerService`/`readerService` confine all pgtype conversion; the gateway sees only the pure `Entry` DTO. NEVER import `internal/charging/db` from the gateway. _Source: `internal/charging/service.go`, `internal/gateway/AGENTS.md`._
- **Reads are vehicle-scoped by the selected TeslaID** — per-vehicle reads must go through `resolveSelectedVehicle`; defaulting to `registered[0]` is a tenancy-correctness bug. _Source: `internal/gateway/AGENTS.md` → "Vehicle-scoped reads"._
- **Any new user-facing label needs ES + EN catalogue keys** — enforced by `TestCatalog_AllKeysHaveBothLanguages` and `make i18n-guard`. _Source: `internal/gateway/AGENTS.md` → i18n._
- **Regeneration:** `.templ` → `make templ`; new CSS classes → `make css` (commit `app.css` in the same change); `queries.sql` → `make sqlc`. _Source: `internal/gateway/AGENTS.md` → regeneration cheatsheet._

## Related KB

- Features: (none yet)
- Workflows: `workflows/supercharger-stats-read.md` (a sibling user-write path — a single narrow correction of two battery-percentage fields over an existing `charge_sessions` row via `charging.SessionVerifier.VerifySession`, with no Create and no Delete, vs. this concept's full Create/Update/Delete over `manual_charge_entries`)
- Architecture: (none yet)
