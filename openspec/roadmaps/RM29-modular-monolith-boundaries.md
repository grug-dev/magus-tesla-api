# RM29 — Modular Monolith Boundaries

Source ticket: MAG-26 — https://linear.app/magus-monitor/issue/MAG-26/modular-monolith-refactoring

## Problem

The platform's module boundaries no longer match its data. Three specific violations:

1. **`vehicle_snapshots` carries five application-calculated columns.**
   `distance_traveled_km_calc`, `battery_used_pct_calc`, `km_per_pct_calc`,
   `estimated_range_km_calc` and `days_spanned_calc` are computed by
   `internal/telemetry`'s collector at capture time and stored on a table whose whole
   purpose is "what Tesla reported". Their only consumer is `internal/analytics`
   (`consumed.go`, renamed from `internal/battery` by tier 1) — the gateway never reads them and recomputes the same deltas by hand
   in `buildOdometerChart`.

2. **`supercharger_sessions` carries five columns Tesla does not report.**
   `start_battery_pct`, `end_battery_pct`, `battery_pct_source`, `start_battery_pct_est`
   and `end_battery_pct_est` are a human verification channel plus a frozen write-once
   estimate snapshot (RM27). The Fleet API has no battery-percentage field at all.

3. **There is no application layer.** `cmd/poller`'s `reconcilingCollector` is business
   logic living in a `cmd/` because there is nowhere else to put it — it hand-rolls
   exactly the `ProcessVehicleData` = sync + recalculate split MAG-26 asks for.

Alongside those, `internal/battery` and `internal/manualcharge` have outgrown their names:
the former will own odometer, temperature and TPMS read-model columns, the latter will own
non-manual charging data.

The result is that the gateway calculates at request time, source data is mutated with
derived values, and no module boundary tells you which of the three kinds of fact —
observation, domain, or derived — you are looking at.

## Decisions (binding — settled with the owner via grill-me, 2026-08-20)

Recorded verbatim on the ticket at
https://linear.app/magus-monitor/issue/MAG-26/modular-monolith-refactoring (comment,
2026-08-20).

| # | Decision |
|---|---|
| **D1** | **Analytics is a precomputed read model.** Duplicating observation columns (`battery_level`, `odometer`, `battery_range`) from `telemetry` into `analytics.vehicle_metrics` is **intentional and expected**, not a smell. UI/APIs read Analytics. |
| **D2** | **New `internal/app` module** owns `ProcessVehicleData` / `SyncVehicleData` / `RecalculateVehicleData` **and** the run log. `poll_attempts` moves there as `process_runs`, gaining `triggered_by` (`scheduler` \| `api`). Answers the ticket's open question: not `telemetry`, because `triggered_by` is not something Tesla reported. |
| **D3** | **Keep the module name `telemetry`** (ticket updated to match). Rename `manualcharge` → `charging` and `battery` → `analytics`. Rule: *rename only where the name becomes wrong.* Renaming `telemetry` would cost 1,060 references across 118 files for no enforcement gain, and would collide with the existing `internal/tesla` Fleet API adapter. |
| **D4** | **`charging`** owns `manual_charge_entries` (unchanged) plus a new `charge_sessions` that normalizes `telemetry.supercharger_sessions` and hosts the five battery-pct verification/estimate columns. Manual ↔ Supercharger convergence is **deferred** (see Future work). |
| **D5** | **Gateway rule: the gateway computes nothing about the vehicle, only about the chart.** Test — does the value change when the user picks a different date range? Bar heights, axis ticks, labels, i18n and formatting stay; deltas, totals, efficiency, clamps and anomaly rules move to analytics. Plain single-table CRUD (`/ui/charges`) reads and writes its owning module directly. |
| **D6** | **Analytics pulls** its inputs through ports it defines (the `vehicleLookup` style already in `battery/reader.go`); `app` says only "recalculate vehicle X". Follows by consequence from D7. |
| **D7** | **Incremental recompute via an `updated_at` watermark stored in analytics** — NOT a `processed` flag on telemetry's tables. Principle: **telemetry may describe its own rows (`updated_at`); it may not track another module's progress (`processed`).** Both telemetry tables already maintain `updated_at` on insert AND on conflict-update, so no new column is needed anywhere. |
| **D8** | **Tier order** — see Tiers below. Renames first (mechanical), then the vertical slice as the gold-standard other tiers mirror. |
| **D9** | **The HTTP API is parked to the last tier.** It cannot be designed until `ProcessVehicleData` exists. Consumer is curl; module, binary and auth deliberately undecided. |
| **D10** | **Unit tests: characterization only.** Pin current output before each move, assert identical output after — the only way to substantiate the ticket's "behaviorally equivalent" bar. No new unit tests for new code. Existing ~17k lines of tests move and update as the renames force. |

### Constraint that forced D7

`internal/telemetry/service.go:229` calls
`ChargingHistory(ctx, creds, tesla.ChargingHistoryParams{})` — **empty params, the full
charging history is fetched every night**. Sessions weeks old can appear or be revised at
any time (the table UPSERTs on `session_id` precisely because *"billing state is mutable
post-session"*). A trailing-window-from-now is therefore insufficient: the watermark must
expand its recompute window to the affected date range, not to "the last N days".

### Rejected alternatives

- **Renaming `telemetry` → `fleet`** (the ticket's original wording). Rejected under D3: a
  package name enforces no boundary, and `fleet` would collide with `internal/tesla`, which
  is already the Tesla Fleet API adapter. The ticket was updated to say `telemetry`.
- **A `processed` boolean on `telemetry.vehicle_snapshots` / `supercharger_sessions`**,
  flipped by analytics through a callback port. Rejected under D7 on five grounds: it
  requires a cross-module write into telemetry's tables; it makes rerunning impossible,
  which is the ticket's own stated requirement ("so calculations can later be rerun without
  calling the Tesla API again"); the metrics are not row-local (every delta needs its
  predecessor row, which is already flagged); revised Supercharger billing on an
  already-processed day is never revisited; and the flip is a second transaction that can be
  lost. `updated_at` already exists on both tables and carries the same information without
  any of these properties.
- **`app` fetching data and pushing it into `analytics`** (the ticket's literal sibling
  triangle). Rejected under D6: `app` would have to own the watermark and know that
  analytics needs snapshots *and* charging sessions over a particular window — analytics'
  own business rule leaking into the orchestrator.
- **Full manual ↔ Supercharger convergence in this roadmap.** Rejected under D4: the two
  sources differ in granularity, trust level, provenance and available fields. That is
  domain modelling, not refactoring.
- **A full recompute of every vehicle's history per run** (no watermark). Viable at current
  volume — one snapshot per vehicle per day — but the owner chose the watermark.

### Future work (see [`backlog.md`](backlog.md))

- **Manual ↔ Supercharger convergence** into one charging representation. Trigger: when a
  consumer needs to treat both sources uniformly (e.g. a combined cost-per-km metric).
- **The HTTP API's auth mechanism.** No API-key or bearer-token mechanism exists in the
  repo today; gateway auth is Google/cookie sessions only. Needed before the last tier.
- **Overlap protection** between a nightly run and a manual re-run. Same tier as the API.

## Tiers

| # | Status | Change | Module | Scope | depends_on |
|---|---|---|---|---|---|
| **T1** | `[x]` | `RM29-analytics-rename-from-battery` | `battery`→`analytics` | Pure package rename. No DB, no behaviour change. | — |
| **T2** | `[x]` | `RM29-charging-rename-from-manualcharge` | `manualcharge`→`charging` | Pure package rename + migrations dir + sqlc entry move. | — |
| **T3** | `[x]` | `RM29-analytics-add-vehicle-metrics` | `analytics` | **Gold standard.** `vehicle_metrics` + `updated_at` watermark + `Recalculate`; gateway re-points and its domain calculation moves out (D5). | T1 |
| **T4** | `[x]` | `RM29-telemetry-drop-derived-columns` | `telemetry` | Drop the five `_calc` columns from `vehicle_snapshots`. Only safe once T3 lands. | T3 |
| **T5** | `[x]` | `RM29-analytics-own-charge-gaps` | `analytics` | `charge_gaps` moves from telemetry to analytics with its `GapWriter` port. | T1 |
| **T6** | `[~]` | `RM29-charging-add-charge-sessions` | `charging` | `charge_sessions` + the five battery-pct columns move off `supercharger_sessions`. | T2 |
| **T7** | `[ ]` | `RM29-app-add-process-vehicle-data` | `app` (new) | The three use cases + `process_runs`; `poll_attempts` moves; `cmd/poller` re-points and `reconcilingCollector` is deleted. | T1, T2 |
| **T8** | `[ ]` | *(parked — D9)* | TBD | The manual-rerun HTTP API adapter. | T7 |

**Status legend:** `[ ]` pending — the tier's OpenSpec change has not been created yet ·
`[~]` in progress — the change exists but is not archived · `[x]` done — the change is
archived. The leader flips `[ ]`→`[~]` when a tier's artifacts are created and `[~]`→`[x]`
at change archive, mirroring each tier's `status` in
`RM29-modular-monolith-boundaries.progress.json`.

Dependency order is strict for T3 → T4 (the columns cannot be dropped until analytics
computes and persists them and every reader is re-pointed). T5, T6 and T7 are independent of
each other and of T4 once T1/T2 land.

## Status

**T1, T2 and T3 are archived.** The two renames landed first — `battery`→`analytics`
(`2026-08-20-RM29-analytics-rename-from-battery`) and `manualcharge`→`charging`
(`2026-08-21-RM29-charging-rename-from-manualcharge`) — followed by the gold-standard
tier, `2026-08-21-RM29-analytics-add-vehicle-metrics`. Each was reviewer-approved with the
owner's own reported test run. T2 also moved the migrations directory and the sqlc entry,
and the owner confirmed `make migrate-status` clean afterwards — the check that catches a
bad directory move, which otherwise fails silently while the build stays green.

**T3 established the pattern the remaining tiers mirror:** `analytics.vehicle_metrics` as a
precomputed daily read model (D1), the `vehicle_metric_watermarks` cursors implementing the
D7 `updated_at` watermark, `Recalculate`/`Reconcile` behind analytics' own ports (D6), and
the gateway's snapshot-derived arithmetic moved out per D5. `cmd/poller` is the only
production caller of `Reconcile`; the gateway calls `Recalculate` after a manual charge
write.

One lesson from T3 worth carrying into T4–T7: its review caught a task marked done whose
substance was never written — a missing `Reconcile` call site in `cmd/poller`. Build, vet
and the full suite all stayed green over it, because `cmd/poller` has no tests and a call
that is never made still compiles. Composition-root wiring in `cmd/` is invisible to every
automated signal this project has, so it needs reading, not trusting.

**T5 is in flight; T6 and T7 are unblocked and waiting.** T4 archived on 2026-08-22, so
T3's read model is now the single source of the five derived figures — `vehicle_snapshots`
carries observations only. The three remaining tiers are mutually independent, so the order
is the owner's call: T5 (`analytics` takes `charge_gaps`) and T6 (`charging` takes
`charge_sessions`) are each a single-module move; T7 (the new `app` module) is the largest
and the only one that touches `cmd/poller`'s composition root. T8 stays parked (D9).
Artifacts exist for T5 only; none yet for T6–T8.

**T6 is in flight.** `internal/charging` gains `charge_sessions` — a dense mirror of each
Supercharger session (window, site, energy, cost, paid) plus the five human-verified
battery-percentage columns. **Expand only**: T6 drops nothing from `telemetry`, because
`MIGRATIONS_DIRS` runs `telemetry` before `charging` and goose walks directories to
completion, so a same-change DROP would execute before the backfill and destroy data that
cannot be recomputed. The contract half — dropping telemetry's five columns — is a separate
later change, and it is **not** a one-line `ALTER`: it must re-point `internal/analytics`'s
consumed correction and gap detection, which read those columns today. Database design gate
confirmed by the owner, who amended the design at the gate from a verification sidecar to a
full mirror.

**T5 archived 2026-08-22.** `charge_gaps` is `internal/analytics`'s end to end — the
migration, the three queries, `ChargeGap`/`MissingChargingType`/`GapWriter`,
`gap_writer.go` and the 7-test DB-integration suite, with `cmd/poller` and the gateway
re-pointed. `analytics-reviewer` approved at round 2 after a round-1 `major` on stale doc
claims. The move was proved against a live database: `make migrate-status` shows the
relocated migration still **Applied**, confirming goose does not re-run a migration that
changed directory. **Owner-verified** — `make test` green and `cmd/poller --once` green,
reported 2026-08-22; the `--once` run is what covers `cmd/poller`, which has no tests of
its own and which this tier rewired. **T6 and T7 remain**, mutually independent.

**Carried into T5–T7 from T4:** two migrations (`20260822000001`, `20260822000002`) were
committed Pending; the owner has since run `make migrate-up` — as of 2026-08-22 both show
**Applied** (`Sat Aug 22 03:22:06 2026`) and no directory has anything pending. The D3
backfill lands on the next nightly `Reconcile`.

All tiers share the branch `ft/RM29-MAG-26-modular-monolith-boundaries`. Live state is in
`RM29-modular-monolith-boundaries.progress.json`; each tier is proposed, reviewed and
implemented one at a time per `openspec/config.yaml` (`rules.proposal`) and
`ai/agentic-workflow.md` §pipeline.
