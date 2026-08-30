# Delete a manual charge record — DELETE /ui/charges/row/:id

> One external entry point, one output. **Backend only** — the adapter side lives in the
> input-port file that links here. Paths + symbols only; ask CodeGraph for signatures, never
> record line numbers. All KB links below are relative to `kkpa/context/`.

## Entry point

- **Symbol:** `Handler.ChargeRowDelete` — `internal/gateway/handlers/charges.go`
- **Trigger:** `DELETE /ui/charges/row/:id`
- **Module:** `charging` (the domain this serves; the handler itself lives in `internal/gateway`)

## Triggered by

- `input-port/charging/charges.md` — the Manual Records page (`/charges`), per-row delete button

## Input / output

- **Input:** the entry id as a path param; the CSRF token on the **`X-CSRF-Token` header** (not
  the body — Go's `net/http` only parses bodies for POST/PUT/PATCH); `?start=&end=` on the query
  string carrying the active filter window.
- **Output:** `200` re-rendering the whole `#charges-list` region (the row no longer exists to
  swap into). `500` renders the same region with an error banner. `403` on CSRF failure, `400` on
  an unparseable id.

## Flow

1. `Handler.ChargeRowDelete` — `internal/gateway/handlers/charges.go` — auth guard, `checkCSRF`,
   parse the id.
2. `windowFromQuery` → `bestEffortWindow` — the filter window to re-render, never a gate.
3. `Handler.resolveSelectedVehicle` — the vehicle context to scope the re-rendered list to.
4. `Handler.fetchEntryTeslaIDAndChargedOn` → `charging.Reader.ListEntriesByAccount` — **the only
   chance** to learn `tesla_id` + `charged_on`; `Writer.Delete` returns nothing.
   ⚠ capped at 100 rows — see the gotchas below.
5. `charging.Writer.Delete` — `internal/charging/service.go` — `DeleteEntry`, double-scoped
   `id AND account_id`. Hard delete: no soft delete, no tombstone, no audit row.
6. `Handler.buildChargesPage` — rebuilds the list region (runs before the error branch too, so
   both outcomes render the same way).
7. `Handler.recalculateAfterChargeWrite` → `analytics.Recalculator.Recalculate(uid, teslaID, D, D)`
   — **only when step 4 found the row**. Errors logged and swallowed.

## Database

| # | Op | Table / entity | Where |
|---|---|---|---|
| 1 | READ | `manual_charge_entries` | `charging.Reader.ListEntriesByAccount` (resolve the affected day) |
| 2 | DELETE | `manual_charge_entries` | `DeleteEntry` |
| 3 | READ | `manual_charge_entries`, `vehicles` | `buildChargesPage` |
| 4 | READ | `vehicle_snapshots`, `charge_sessions`, `manual_charge_entries` | `Recalculate`'s three source fetches |
| 5 | WRITE | `vehicle_metrics` | `UpsertVehicleMetric` × n **+** `DeleteVehicleMetricsInRangeExcept`, one transaction |

Steps 4–5 are skipped entirely when step 1 did not find the row. **Not touched:** `charge_gaps`,
`vehicle_metric_watermarks`.

## Entities involved

- `entities/vehicle-metrics/guide.md`

## Related use cases

- `use-case/charging/update-manual-charge.md` — same pre-write lookup, same recalc hook
- `use-case/charging/verify-session-battery.md` — the Supercharger path has no delete counterpart

## Conventions & gotchas

- **This is the only mutation in the system with no nightly self-heal.** `Reconcile` finds work
  via `ListEntriesByVehicleUpdatedSince` over live rows; a deleted row is invisible to it
  forever. The `Recalculate` call in step 7 is not a fast path, it is the *only* path — and its
  error is logged and swallowed. Treat any change to that call as safety-critical.
  _Source: `analytics/recalculate.go` `Reconcile`; `architecture/charge-record-mutation.md`._
- **The 100-row lookup cap is worst here.** `ListEntriesByAccount(ctx, uid, 0)` resolves to
  `defaultLimit = 100`, ordered `charged_on DESC` across all the account's vehicles. Deleting an
  entry outside that set means `hadEntry == false` and **no recalculation at all** — silently,
  with nothing logged. Fixing this needs a `GetEntry` on the `charging.Reader` port.
  _Source: `charging/service.go` `defaultLimit`, `fetchEntryTeslaIDAndChargedOn`._
- **CSRF travels on the header for this route, by necessity.** The delete button uses htmx
  `hx-headers` to set `X-CSRF-Token`; a hidden body input would never be parsed and would 403.
  Do not "normalise" it to the body form the other routes use.
  _Source: `ChargeRowDelete` doc comment (MAG-5 root cause)._
- **`buildChargesPage` is called before the error check** so the success and failure branches
  render the identical region. Keep that order if you touch the handler.
  _Source: `ChargeRowDelete`._
