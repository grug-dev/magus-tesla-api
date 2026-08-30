# Verify a Supercharger session's battery percentages — PATCH /ui/supercharger-stats/row/:id

> One external entry point, one output. **Backend only** — the adapter side lives in the
> input-port file that links here. Paths + symbols only; ask CodeGraph for signatures, never
> record line numbers. All KB links below are relative to `kkpa/context/`.

## Entry point

- **Symbol:** `Handler.SuperchargerRowUpdate` — `internal/gateway/handlers/supercharger.go`
- **Trigger:** `PATCH /ui/supercharger-stats/row/:id`
- **Module:** `charging` (the domain this serves; the handler itself lives in `internal/gateway`)

This is the **only** write route against `charge_sessions` reachable from the UI. There is no
create and no delete: the rows are mirrored from Tesla by the nightly job, so the user's sole
write channel is correcting the two human-owned battery percentages.

## Triggered by

- `input-port/charging/supercharger-stats.md` — the Supercharger Stats page
  (`/supercharger-stats`), inline row edit form

## Input / output

- **Input:** form body with exactly `csrf_token`, `start_battery_pct`, `end_battery_pct`; plus
  `?start=&end=` on the URL carrying the active window. Both percentage keys must be **present**
  — an absent key is a `400`. A present-but-empty (or whitespace-only) value is an explicit
  *clear*, not a validation error, and is not range-checked.
- **Output:** `200` with `fragments.SuperchargerRow` — the single static row, no list refresh.
  `422` re-renders the edit form with per-field errors and the raw submitted values echoed back;
  `404` when the session cannot be re-resolved; `500` with a top-of-form error; `403` on CSRF
  failure; `400` on an unparseable id or a malformed body.

## Flow

1. `Handler.SuperchargerRowUpdate` — `internal/gateway/handlers/supercharger.go` — auth guard,
   `checkCSRFKey(csrfSuperchargerKey)`, parse the id. **No `RegisteredVehicles` ownership check**
   — a deliberate divergence from the manual path.
2. `c.GetPostForm` × 2 — strict presence check, then range-validate each non-empty value to
   `[0, 100]` before any port call.
3. `charging.SessionVerifier.VerifySession` — `internal/charging/session_verifier.go` —
   re-validates the range, computes `battery_pct_source`, calls `VerifyChargeSession`, maps the
   returned row via `rowToSession`.
4. `Handler.recalculateAfterSessionVerify` →
   `analytics.Recalculator.Recalculate(uid, teslaID, day−1, day+1)` where `day` is the **UTC**
   calendar day of `charge_stop_date_time`. **Skipped entirely when `updated.TeslaID` is nil**
   (the VIN is not a currently-registered vehicle) — logged, and the write still stands.
5. `superchargerRowVMFromSession` — maps `VerifySession`'s own returned `Session`, so the success
   path costs no extra read.

## Database

| # | Op | Table / entity | Where |
|---|---|---|---|
| 1 | WRITE | `charge_sessions` | `VerifyChargeSession` — SETs `start_battery_pct`, `end_battery_pct`, `battery_pct_source`, `updated_at` and nothing else; Postgres recomputes `inferred_capacity_kwh_calc` in the same statement |
| 2 | READ | `vehicle_snapshots` | `SnapshotsByVehicleBetween` + `SnapshotPrecedingDay` |
| 3 | READ | `charge_sessions` | `ListSessionsByVehicleBetween` |
| 4 | READ | `manual_charge_entries` | `ListEntriesByVehicleBetween` |
| 5 | WRITE | `vehicle_metrics` | `UpsertVehicleMetric` × n **+** `DeleteVehicleMetricsInRangeExcept`, one transaction |

On the error branches, `fetchSuperchargerRowVM` additionally READs `charge_sessions` (via
`ListSessionsByVehicleBetween`) and `vehicles` to re-resolve the row's display context.
**Not touched:** `charge_gaps`, `vehicle_metric_watermarks`, `manual_charge_entries` (write side).

## Entities involved

- `entities/vehicle-metrics/guide.md`

## Related use cases

- `use-case/charging/update-manual-charge.md` — the manual counterpart; converges on the same
  `Recalculate` with a narrower window
- `use-case/charging/delete-manual-charge.md` — no Supercharger equivalent exists

## Conventions & gotchas

- **`VerifyChargeSession` and `MirrorChargeSession` are deliberate mirror images.** This query
  can touch only the three human-owned columns plus `updated_at`; the nightly mirror can touch
  everything *except* them. The protection is the query's shape, not a comment. Never add a
  column to either SET clause to "complete the pattern".
  _Source: `charging/db/query.sql`._
- **`battery_pct_source` is computed in Go and never accepted from the caller** —
  `user_verified` when either percentage is non-nil, SQL NULL when both are nil. Setting it in
  the same statement as the percentages is what keeps the
  `charge_sessions_pct_source_required` CHECK satisfied with no intermediate state.
  _Source: `charging/session_verifier.go`._
- **The `±1 day` recalculation window is load-bearing, not padding.** `charge_stop_date_time` is
  a timestamp; its UTC calendar day can differ from the poller-zone day the metric is bucketed
  into, so a single-day window is provably wrong here. This is why the two flows' windows
  legitimately differ.
  _Source: `recalculateAfterSessionVerify` doc comment (design D4/D5)._
- **A nil `TeslaID` silently skips recalculation.** The write succeeds and the skip is logged.
  Any change here must preserve "the write still stands".
  _Source: `SuperchargerRowUpdate` (design D6)._
- **The error branches re-resolve via `fetchSuperchargerRowVM` rather than inspecting the
  error** — that is how a 404 is told apart from a 500 without importing pgx into the gateway.
  _Source: `SuperchargerRowUpdate` (design D9)._
- **`start_battery_pct_est` / `end_battery_pct_est` are unreachable from every write path in the
  repo** and therefore always render `—`. They await an estimator that does not exist yet.
  _Source: `charging/db/query.sql`, `superchargerRowVMFromSession`._
- **The success response refreshes only the row.** Unlike the manual path there is no OOB list
  or tile refresh. Fine while the tiles aggregate only energy and cost; revisit the moment the
  page surfaces anything derived from the percentages.
  _Source: `SuperchargerRowUpdate`; `architecture/charge-record-mutation.md`._
