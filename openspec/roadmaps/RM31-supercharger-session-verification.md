# RM31 — Supercharger session battery-% verification

Source ticket: MAG-19 — https://linear.app/magus-monitor/issue/MAG-19/allow-editing-supercharger-sessions

**Intention:** a user can correct the start/end battery percentage of a Supercharger
session inline on `/supercharger-stats`, and that correction immediately flows into
`vehicle_metrics` — because `analytics` reads the same table the edit writes.

This closes **backlog item 11** ("Supercharger start/end SOC verification UI"), which has
been open since RM27 shipped storage-only on 2026-08-15.

## Why this is a roadmap and not one change

It spans three modules — `charging` must grow a write port for its human-owned
verification columns and two read ports `analytics` needs (Decision 9), `analytics` must
switch its Supercharger source from `telemetry` to `charging`, and `gateway` must render
and drive the edit.
`openspec/config.yaml` §`rules.proposal` forbids one big cross-module change.

## Findings that shaped it (verified in code, 2026-08-27)

The ticket describes six items. Investigation before writing this roadmap found that two
are already satisfied and one rests on a premise the code contradicts:

- **Item 2 (remove the `Country` column) is already done.** `charging.Session` carries no
  `CountryCode` at all — RM29 design D1 excluded `country_code` from `charge_sessions`, and
  RM30 tier 2 removed the column and its i18n keys from the page. The table renders exactly
  Date / Site / Energy / Cost. **No tier covers item 2.**
- **Item 1 is small and is reuse, not new work.** `SuperchargerStatsView.Chart` already
  reuses the `HistoryChart` / `HistoryBar` types verbatim, which already carry per-bar
  `Label` and `YAxisTicks`. `buildYAxisTicks(max, format)` already exists in
  `internal/gateway/handlers/history.go`. `buildSuperchargerChart` simply never populates
  either. The work is to populate them, reusing the history gold standard.
- **Item 4d could not have worked as the ticket assumed.** The page reads
  `charging.charge_sessions`, but `analytics` computes `vehicle_metrics` from
  `telemetry.SuperchargerSession` — `sumSuperchargerPctBetween` and
  `inferMissingChargingType` both take `[]telemetry.SuperchargerSession`, and
  `vehicle_metric_watermarks.source` is constrained to
  `('vehicle_snapshots', 'supercharger_sessions', 'manual_charge_entries')`.
  `charge_sessions` is **not** an analytics source. Editing the table the page displays and
  then calling `Recalculate` would have been a no-op — the calc fields would not move.
  Decision 1 resolves this.

The five verification columns already exist on `charge_sessions`
(`20260823000001_add_charge_sessions.sql`) and are deliberately absent from the nightly
mirror's INSERT and `ON CONFLICT DO UPDATE` — `charging.SessionMirror` has no field for
them, so the sync cannot overwrite a verified value even by mistake (RM29 design D6).
**No new column is needed for the edit itself.**

## Tiers

Legend — `[ ]` pending (change not created) · `[~]` in progress (change created, not
archived) · `[x]` done (archived).

| Status | Change | Module | Scope | depends_on | Proposal prompt |
|---|---|---|---|---|---|
| `[x]` | `RM31-charging-add-session-verification-port` | `charging` | Add the write port for the human-owned verification columns: a `SessionVerifier` port with a single method that updates **only** `start_battery_pct`, `end_battery_pct` and `battery_pct_source` on one account-scoped session, plus its sqlc query. No schema change — all five columns already exist. | — | Add a verification write port to `internal/charging` over `charge_sessions`. One method, account-scoped, updating **exactly three** columns — `start_battery_pct`, `end_battery_pct`, `battery_pct_source` — and `updated_at`. Every other column, including the two `_est` snapshot columns, must be absent from the UPDATE's SET clause, mirroring how `MirrorChargeSession` protects these five from the sync (design D6): protection by the query's shape, not by comment. `battery_pct_source` is always written `'user_verified'` — the port takes no source parameter, so no caller can write `'polled'`. Honour `charge_sessions_pct_source_required`: setting both percentages to NULL must also NULL the source, or the CHECK rejects the row. Validate 0–100 in Go before the query (the DB CHECK is the backstop, not the error message). Return the updated `Session` so the caller can re-render without a second read. Mirror `Writer.Update`'s account-scoping and not-found semantics. No new DB object — no migration. |
| `[x]` | `RM31-charging-add-session-read-ports` | `charging` | Grow `SessionReader` with the two read shapes `analytics` needs and `charge_sessions` does not yet expose: `ListSessionsByVehicleUpdatedSince` (the cursor-driven read that carries a `VerifySession` edit into `Reconcile`) and `ListSessionsByVehicle` (limit-bounded, for the Wh/km read). No schema change — both are queries over existing columns and indexes. | — | Add two read methods to `internal/charging`'s `SessionReader` port over `charge_sessions`, both account- and vehicle-scoped exactly like the existing `ListSessionsByVehicleBetween`. **(1)** `ListSessionsByVehicleUpdatedSince(ctx, accountID, teslaID, since) ([]Session, error)` — every session whose `updated_at >= since`. Mirror `ListEntriesByVehicleUpdatedSince` (`query.sql:118`), which already solves this exact problem for `manual_charge_entries`, including its documented index reasoning (vehicle-scoped range scan with `updated_at` as a residual predicate, NOT a new index). This method is the mechanism that makes RM31 work at all: `VerifyChargeSession` sets `updated_at = now()`, so a human edit becomes visible to `analytics.Recalculator.Reconcile` through this method and no other. **(2)** `ListSessionsByVehicle(ctx, accountID, teslaID, limit) ([]Session, error)` — the most recent `limit` sessions, mirroring `ListEntriesByVehicle` (`query.sql:81`). State and justify the sort direction against the table's own index rather than copying a sibling's convention — `ListSessionsByVehicleBetween`'s doc comment already warns that these two `Session` reads deliberately do NOT share a sort-direction rule. Both methods return a non-nil empty slice on no match, and neither ever returns a row whose `TeslaID` is NULL (RM29 design D6 — SQL `NULL = value` is never true, so an orphaned session is definitionally outside a vehicle-scoped read); say so in the doc comments. **No new DB object — no migration, no new index.** If the index plan says otherwise, stop and report rather than adding one. |
| `[x]` | `RM31-analytics-read-sessions-from-charging` | `analytics` | Switch the Supercharger source from `telemetry.SuperchargerSession` to `charging.Session`: repoint the source port, retype `sumSuperchargerPctBetween` / `inferMissingChargingType`, and migrate the watermark source value `supercharger_sessions` → `charge_sessions` (CHECK constraint + existing rows). **DB design gate applies.** | 2 | Switch `internal/analytics`'s Supercharger source from `telemetry` to `charging` so a user-verified percentage reaches `vehicle_metrics`. Repoint the source port onto `charging.SessionReader`; retype `sumSuperchargerPctBetween` and `inferMissingChargingType` from `[]telemetry.SuperchargerSession` to `[]charging.Session`. **Verify field parity first and report it** — `charge_sessions` deliberately has no `country_code`, `billing_type`, `unlatch_date_time`, `vehicle_make_type` or `raw_data` (RM29 design D1); if any of those feeds a calc, say so and stop rather than working around it. Note `charging.Session.TeslaID` is `*int64` (nil when the VIN is not a registered vehicle) where telemetry's is not — a nil TeslaID row is never returned by a vehicle-scoped read (RM30 design D6), but state how the retyped functions handle it. Migration: `vehicle_metric_watermarks.source` CHECK becomes `('vehicle_snapshots', 'charge_sessions', 'manual_charge_entries')` and existing `'supercharger_sessions'` rows are DELETEd in the same migration, NOT renamed — see **Decision 10**, which is authoritative here. (This cell previously said UPDATEd, contradicting Decision 10; corrected 2026-08-28 at the tier-3 design gate, where the owner reconfirmed DELETE. An absent watermark row is defined as the epoch, so the next nightly `Reconcile` rebuilds each vehicle's history once from the new table.) design.md MUST carry the full schema change, the rationale, and the index plan per `openspec/config.yaml` §`rules.design`; this change is **design-gated** and needs the owner's confirmation before Apply. `internal/analytics` must no longer import `internal/telemetry` for the Supercharger path (it still may for snapshots). |
| `[x]` | `RM31-gateway-show-session-battery-pct` | `gateway` | Display-only: populate the Supercharger chart's per-bar `YYYY-MM` labels and Y-axis ticks (ticket item 1), and surface the four battery columns on the session table (ticket item 3). No write path. | — | Two display changes to `internal/gateway`'s supercharger-stats slice, both pure reuse of the history gold standard. **(1)** `buildSuperchargerChart` currently returns `HistoryChart{Bars: bars, Empty: false}` with neither per-bar `Label` nor `YAxisTicks`. Populate `HistoryBar.Label` with the bar's month as `YYYY-MM`, and set `YAxisTicks` by calling the existing `buildYAxisTicks(max, format)` from `history.go` with a kWh formatter. Do not write a second tick algorithm and do not add a chart library — `historyBarChart` already renders both, and the template does no arithmetic. Consider `LabelVertical` given `YYYY-MM` is wider than `MM-DD`. **(2)** Add the four battery fields to `SuperchargerRowVM` and the table: the human-verified `start_battery_pct` / `end_battery_pct` and the frozen `start_battery_pct_est` / `end_battery_pct_est`, all already present on `charging.Session`. Render nil as `"—"` — per Decision 3 the `_est` pair is **always** NULL today, so it must degrade gracefully rather than look broken. Both languages in `internal/gateway/i18n/catalog.go` for every new header. Do NOT add the `Country` column back — it was removed deliberately (RM30 tier 2). |
| `[ ]` | `RM31-gateway-add-session-battery-edit` | `gateway` | The edit itself: an htmx inline row swap that PATCHes the two percentages through `charging`'s verification port, then triggers `analytics.Recalculate` for that session's day, mirroring `recalculateAfterChargeWrite`. Plus the KB update (ticket item 6). | 1, 2, 3, 4 | Add inline battery-% verification to `/supercharger-stats`. An Edit action per row swaps that `<tr>` for an editable row (two number inputs + save/cancel) via `hx-get`; save issues a PATCH that calls tier 1's `charging` verification port and swaps the updated row back. **No delete action** (ticket item 5). Only the two percentages are editable — `battery_pct_source` is set to `user_verified` by the port, never by the form, and the two `_est` columns are never written. Wire the recalculation exactly as the manual-charge path does: `internal/gateway/handlers/charges.go`'s `recalculateAfterChargeWrite` calls `h.analyticsRecalculator.Recalculate(ctx, uid, teslaID, chargedOn, chargedOn)` and swallows the error into a log line — reuse that helper rather than writing a parallel one, deriving the day from the session's `ChargeStopDateTime` in the vehicle's timezone. `analyticsRecalculator` is **already** on the gateway `Handler` and already wired in `cmd/web/main.go`, so no new dependency. Both ES and EN for every new string. **KB (ticket item 6):** update `kkpa/context/workflows/supercharger-stats-read.md`, which currently documents this page as having *no user write path* — add the write path and the recalculation hop, and add the INDEX rows for it. Note the `charging.Session.TeslaID` nil case: a session whose VIN is not a registered vehicle has no vehicle to recalculate for. |

`cmd/web` wiring (injecting the new `charging` verification port into `gateway.Deps`) is
leader-owned integration — it sits outside `internal/`, so it belongs to no tier.

## Decisions

1. **`analytics` switches its Supercharger source from `telemetry.supercharger_sessions`
   to `charging.charge_sessions`** (owner, 2026-08-27). The user's edit has to land where
   `analytics` reads, or item 4d is a no-op. The owner chose to move the reader rather than
   add a fourth source, so exactly one table carries a session's percentages and no
   conflict rule is needed. Consequences, all intended: `telemetry` returns to being purely
   the ingest layer for this data; `vehicle_metric_watermarks.source` needs a CHECK-constraint
   migration plus an in-place value update; and `analytics` stops importing `internal/telemetry`
   for the Supercharger path. This is what makes tier 2 database-gated.
2. **The edit writes `charge_sessions` only — it never propagates back to
   `telemetry.supercharger_sessions`.** `charging` owns the human-owned channel by design
   (RM29 D1/D5/D6), and after Decision 1 nothing downstream reads telemetry's copy of these
   columns. Telemetry's own five columns (added by RM27/MAG-14) become vestigial for this
   flow; removing them is **not** in scope — recorded as future work below.
3. **`start_battery_pct_est` / `end_battery_pct_est` stay NULL** (owner, 2026-08-27). They
   are documented as a frozen snapshot of "the estimate on screen at verification time",
   but no code anywhere computes such an estimate — RM27 D11 descoped the SOC estimator.
   Freezing a value that does not exist would be a fabricated drift log. They are displayed
   (ticket item 3) and always render `"—"` until an estimator exists. Building one is
   explicitly out of scope; it remains backlog item 11's third follow-up.
4. **Inline htmx row swap for the edit UX** (owner, 2026-08-27), over a modal or
   always-editable cells. It matches the ticket's "inline on the record", reuses the
   fragment-swap pattern the page already uses, and avoids firing a PATCH on an accidental
   blur.
5. **Ticket item 2 requires no work** — see Findings. Called out here so a later reader does
   not file its absence as a missed requirement.
6. **The KB update rides in tier 4, not a separate pass** (owner, 2026-08-27), so the
   guide and the write path it documents land together.
7. **Tier order: `charging` first** (owner, 2026-08-27). It is the dependency for the
   gateway edit and the smallest well-bounded piece. Tiers 1, 2 and 4 have no dependency on
   one another and could be done in any order or in parallel; tier 3 needs tier 2 (Decision 9)
   and tier 5 needs all four.
8. **`telemetry.supercharger_sessions` is read by the nightly mirror and by nothing else**
   (owner, 2026-08-27), which settles how far Decision 1 reaches. `internal/app`'s
   `processChargingData` reads it and writes `charge_sessions` through
   `charging.MirrorSessions`; that one hop is the only legitimate reader. Therefore **both**
   of `analytics`' Supercharger reads move to `charge_sessions`, not just the one MAG-19
   forces: the per-day `vehicle_metrics` derivation (`recalculate.go`, reads the battery
   percentages) *and* the Wh/km `RecentEfficiency` read (`reader.go`, reads only
   `EnergyKWh` and is untouched by a human edit). The alternative — moving only the
   percentage path — was rejected as a half-migration: it leaves `analytics` reading one
   logical entity from two physical tables with a rule a future reader must be taught, and
   it keeps telemetry's vestigial percentage columns alive (Decision 2's future-work item).
9. **The `charging` read-port gap gets its own tier (tier 2), before `analytics`** (owner,
   2026-08-27). Verified in code while planning tier 3: `analytics` consumes three methods
   of `telemetry.SuperchargerReader` — `…ByVehicleBetween`, `…ByVehicleUpdatedSince` and
   `…ByVehicle(limit)` — but `charging.SessionReader` exposes only the `Between` shape, so
   Decision 1 cannot be executed inside the `analytics` boundary at all. The two missing
   methods are `charging` code; a single tier spanning both modules was rejected because it
   breaks the one-module-per-worker rule that tier 1 and `ai/agentic-workflow.md` follow.
   **Field parity itself passes** — the three calcs read only `ChargeStartDateTime`,
   `ChargeStopDateTime`, `StartBatteryPct`, `EndBatteryPct` and `EnergyKWh`, all present on
   `charging.Session`; none of `country_code`, `billing_type`, `unlatch_date_time`,
   `vehicle_make_type` or `raw_data` feeds a calculation. The blocker is the port's shape,
   not the row's.
10. **The watermark migration resets the cursors to epoch rather than renaming their values
    in place** (owner, 2026-08-27). Existing `vehicle_metric_watermarks` rows with
    `source = 'supercharger_sessions'` are DELETEd, not UPDATEd to `'charge_sessions'`. An
    absent watermark row is *defined* as the epoch (RM29 tier 3 design D7), so the next
    nightly `Reconcile` rebuilds each vehicle's history once from the new table — the
    backfill IS the nightly path, with no one-off binary. Exact precedent:
    `20260822000002_reset_vehicle_metric_watermarks.sql`. Carrying the value over was
    rejected because it silently assumes no `charge_sessions.updated_at` predates the
    inherited cursor; any session that did would be skipped forever, with nothing to
    detect it. A single redundant backfill pass is the cheaper failure mode.

## Future work

- Removing the now-vestigial five verification columns from
  `telemetry.supercharger_sessions` (see Decision 2), once tier 2 has landed and nothing
  reads them. Not filed as a backlog item yet — it is only actionable after RM31 completes.
- The SOC estimator that would give the `_est` snapshot columns a real value (Decision 3)
  remains open as the third follow-up under **backlog item 11**.
