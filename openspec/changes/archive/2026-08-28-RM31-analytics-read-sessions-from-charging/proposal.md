Source: MAG-19 — https://linear.app/magus-monitor/issue/MAG-19/allow-editing-supercharger-sessions
Roadmap: openspec/roadmaps/RM31-supercharger-session-verification.md
Tier: 3 of 5 (`analytics` switches its Supercharger source from `telemetry` to `charging`,
depending on tier 2's two new `charging.SuperchargerSessionAnalyticsReader` read methods; tier
1 was `charging`'s `SessionVerifier` write port, tier 4 is `gateway`'s display-only columns,
tier 5 is the gateway edit itself, which depends on tiers 1, 2, 3 and 4).
Design gate: **tripped.** This change alters an existing database object —
`vehicle_metric_watermarks.source`'s CHECK constraint and every existing row whose value it
constrains — so `openspec/config.yaml` §`rules.design` applies in full: design.md carries the
complete migration SQL, the rationale (including a rejected alternative), and an index plan
justified against the read pattern. **This change requires the owner's explicit confirmation
of design.md before Apply.**
Unit tests: offline unit tests for the retyped pure functions
(`sumSuperchargerPctBetween`, `inferMissingChargingType`, `sumSuperchargerKWh`,
`deriveVehicleMetrics`) are written early, since they compile against code that already
exists once the retype lands. `DATABASE_URL`-gated integration tests (the migration's
per-source DELETE, `Reconcile` reading through the new port, the nil-`TeslaID` case) are written
last, per `ai/go-conventions.md` §Testing's "cannot compile before the migration and the
sqlc-generated types exist" rule — but their expected values are pinned in design.md's Test
Contract **before** any implementation exists (T1–T4), not derived after reading the code.

## Why

RM31's core requirement (MAG-19) is that a human's correction to a Supercharger session's
start/end battery percentage — written to `charging.charge_sessions` by tier 1's
`SessionVerifier.VerifySession` — reaches `vehicle_metrics`. Today it cannot: `analytics`
computes `vehicle_metrics` from `telemetry.SuperchargerSession`
(`sumSuperchargerPctBetween`/`inferMissingChargingType` in `consumed.go`, and
`sumSuperchargerKWh` in `reader.go`), a table `SessionVerifier` never writes and the nightly
mirror deliberately never writes back to (RM29 design D6). Editing `charge_sessions` and
calling `Recalculate` today is a no-op — the calc fields do not move (roadmap "Findings").

Roadmap Decision 1 resolves this by **moving** the reader rather than adding a fourth source:
exactly one table carries a session's percentages, so no conflict rule between two copies is
ever needed. Roadmap Decision 8 extends the move to **both** of `analytics`'s Supercharger
reads — the per-day `vehicle_metrics` derivation (`consumed.go`, reads the percentages) and
the Wh/km `RecentEfficiency` read (`reader.go`, reads only `EnergyKWh` and is untouched by a
human edit) — because `telemetry.supercharger_sessions` is read by nothing but the nightly
mirror after this tier (roadmap Decision 8's finding), and a half-migration would leave
`analytics` reading one logical entity from two physical tables.

**Field parity was verified in code and passes**: the three consuming calculations read only
`ChargeStartDateTime`, `ChargeStopDateTime`, `StartBatteryPct`, `EndBatteryPct`, and
`EnergyKWh` — all present on `charging.Session` with identical Go types. The five fields
`charge_sessions` deliberately omits (`CountryCode`, `BillingType`, `UnlatchDateTime`,
`VehicleMakeType`, `RawData` — RM29 design D1) feed nothing in `analytics`. There is no
workaround to design here.

**Port parity was the actual blocker**, and tier 2 closed it: `analytics` consumes three
methods of `telemetry.SuperchargerReader` (`…ByVehicleBetween`, `…ByVehicleUpdatedSince`,
`…ByVehicle(limit)`); `charging.SuperchargerSessionAnalyticsReader` (new in tier 2) now
exposes the identical three-method shape, embedding the pre-existing `SessionReader` for the
`Between` method. This tier depends on tier 2 for exactly that reason.

`charging.Session.TeslaID` is `*int64` (nil when the VIN is not a currently-registered
vehicle), unlike `telemetry.SuperchargerSession.TeslaID`, which is not read by any of the
three retyped functions in the first place — none of `sumSuperchargerPctBetween`,
`inferMissingChargingType`, or `sumSuperchargerKWh` ever reads a `TeslaID` field. A
nil-`TeslaID` row is additionally never returned by any vehicle-scoped read on
`SuperchargerSessionAnalyticsReader` (tier 2 design D4/D6 — `SQL NULL = value` is never true),
so the retyped functions are structurally never handed one. Test Contract T4 confirms this by
non-regression rather than by new application-level filtering.

## What Changes

- **CHANGED (database)** — `vehicle_metric_watermarks.source`'s CHECK constraint becomes
  `source IN ('vehicle_snapshots', 'charge_sessions', 'manual_charge_entries')`. Every existing
  row with `source = 'supercharger_sessions'` is **DELETED** — resetting that source's cursor to
  the epoch, which `Reconcile`'s own `watermark` method already treats as "backfill this
  source's full history" (design D7) — per the roadmap document's own Decision 10, reconfirmed
  by the owner at the design gate on 2026-08-28 after an earlier draft of this design mistakenly
  followed a stale, self-contradicting table-cell in the same roadmap document instead. See
  design.md for the full migration SQL, the ordering that makes it constraint-safe, and why the
  DELETE is nearly free rather than a costly full rebuild.
- **CHANGED** — `consumed.go`: `sumSuperchargerPctBetween` and `inferMissingChargingType`
  retype their `sessions` parameter from `[]telemetry.SuperchargerSession` to
  `[]charging.Session`; `deriveVehicleMetrics` retypes its `sessions` parameter identically.
  No formula changes — same fields, same comparisons, new package.
- **CHANGED** — `reader.go`: `reader.supercharger` retypes from `telemetry.SuperchargerReader`
  to `charging.SuperchargerSessionAnalyticsReader`; `sumSuperchargerKWh` retypes its `sessions`
  parameter; `RecentEfficiency`'s call site moves from
  `SuperchargerSessionsByVehicle` to `ListSessionsByVehicle`. `NewReader`'s `supercharger`
  parameter retypes identically.
- **CHANGED** — `recalculate.go`: `recalculator.supercharger` retypes from
  `telemetry.SuperchargerReader` to `charging.SuperchargerSessionAnalyticsReader`;
  `Recalculate`'s call site moves from `SuperchargerSessionsByVehicleBetween` to
  `ListSessionsByVehicleBetween`; `Reconcile`'s call site moves from
  `SuperchargerSessionsByVehicleUpdatedSince` to `ListSessionsByVehicleUpdatedSince`.
  `NewRecalculator`'s `supercharger` parameter retypes identically. The
  `sourceSuperchargerSessions` constant's **value** changes from `"supercharger_sessions"` to
  `"charge_sessions"`; the **identifier** is renamed to `sourceChargeSessions` — see design.md
  for why (this module's own established convention names each source constant after the
  physical table it now reads).
- **CHANGED** — every test file touching the Supercharger source (`consumed_test.go`,
  `reader_test.go`, `recalculate_test.go`, `db_integration_test.go`) retypes its fixtures and
  fakes from `telemetry.SuperchargerSession`/`telemetry.SuperchargerReader` to
  `charging.Session`/`charging.SuperchargerSessionAnalyticsReader`.
- **CHANGED** — `internal/analytics/AGENTS.md`'s "Allowed / forbidden imports" section:
  `internal/telemetry` no longer lists `SuperchargerReader`/`SuperchargerSession`; the module
  now imports `internal/charging`'s `SuperchargerSessionAnalyticsReader` alongside its existing
  `Reader`/`Entry`.
- **UNCHANGED** — `internal/analytics` still imports `internal/telemetry`'s `Reader` for
  snapshots (`SnapshotsByVehicleBetween`, `SnapshotsByVehicleSince`, `SnapshotPrecedingDay`,
  `SnapshotsByVehicleUpdatedSince`) — only the Supercharger path moves. `vehicle_metrics`,
  `charge_gaps`, and every other analytics table/port are untouched. No public `Reader`/
  `Recalculator` method signature changes — this is an internal dependency swap.
- **NOT in this tier** — `cmd/web`'s wiring of `telemetry.NewSuperchargerReader(pool)` into
  `NewRecalculator`/`NewReader` (and `cmd/poller`'s equivalent, if any) is **leader-owned
  integration**: `cmd/` sits outside `internal/`, outside this worker's sandbox. Design.md
  documents the required wiring change for the leader to apply. `telemetry.SuperchargerReader`
  itself, `telemetry.supercharger_sessions`, and the nightly mirror are untouched — the port
  and table still exist and are still written; `analytics` simply stops reading them.

**Breaking:** no, for any external caller — `Reader`/`Recalculator`'s public method
signatures are unchanged. It IS a breaking change to `NewReader`'s and `NewRecalculator`'s
constructor signatures (parameter type change on `supercharger`), which is why the `cmd/web`
wiring update is called out above as required, leader-owned follow-up.

**Modules affected:** `analytics` (all code changes in this tier) and `charging` (read-only —
this tier consumes tier 2's `SuperchargerSessionAnalyticsReader`, already merged, no `charging`
code changes here). `internal/telemetry` is unchanged. `cmd/web` needs a wiring update, tracked
above as leader-owned, not part of this tier's task list.

## Read paths affected

Per `openspec/config.yaml` §proposal:

- **`Recalculator.Recalculate`/`Reconcile`** (the nightly per-vehicle write path, and the
  manual-charge-write-triggered recalculation) — the Supercharger session read moves from
  `telemetry.supercharger_sessions` (indexed on `(account_id, tesla_id,
  charge_start_date_time)`) to `charging.charge_sessions` (indexed on `(account_id, tesla_id,
  charge_stop_date_time)` via `idx_charge_sessions_vehicle_stop`, built by RM29 tier 6 and
  already proven to serve exactly these three access patterns by tier 2's design.md). No new
  index; see design.md.
- **`Reader.RecentEfficiency`** (a dashboard read, `ai/architecture.md` §7 read-heavy path) —
  same table swap for its `EnergyKWh` sum. Unaffected by the human edit itself (percentages
  are not read here), but moved per roadmap Decision 8 to avoid a half-migrated source.
- **`vehicle_metric_watermarks`**'s single-row cursor lookup (`Reconcile`'s
  `WHERE account_id = $1 AND tesla_id = $2 AND source = $3`) — unaffected by the CHECK-
  constraint migration; still served entirely by the existing
  `vehicle_metric_watermarks_account_tesla_source_unique` index. No new index. See design.md
  "Index Plan".
- **No read path becomes slower.** `charge_sessions`'s `idx_charge_sessions_vehicle_stop` was
  purpose-built by RM29 tier 6 for exactly this vehicle-scoped windowed/cursor/limit read
  shape, and tier 2's design.md already proved (via `EXPLAIN`) that all three access patterns
  this tier needs are served by it with no sequential scan.

## Impact

- **Affected specs:** `analytics` (existing capability). **MODIFIED** — the "No Cross-Module
  Database Access" requirement's scenario, which names the specific public interfaces this
  capability imports (a structural detail already present in the existing spec, updated to
  match the new dependency set). No other requirement changes: every other requirement
  describes derivation *behavior* (WHAT), which is source-table-agnostic and therefore
  unaffected by this HOW-level swap.
- **Affected code:** `internal/analytics/` (`consumed.go`, `reader.go`, `recalculate.go`,
  their four test files, `AGENTS.md`) and `internal/analytics/db/migrations/` (one new
  migration). No file outside `internal/analytics/` is touched by this tier's task list.
- **Design gate:** tripped — see header. design.md carries the full migration, rationale,
  index plan, retype plan, and Test Contract T1–T4.
- **Deferred, explicitly NOT in scope:** `cmd/web`'s constructor-wiring update (leader-owned,
  see "What Changes" above), the gateway display/edit tiers (4–5), removing
  `telemetry.supercharger_sessions`'s five now-vestigial verification columns (roadmap
  Decision 2's noted future work, not actionable until this tier and tier 5 both land).
