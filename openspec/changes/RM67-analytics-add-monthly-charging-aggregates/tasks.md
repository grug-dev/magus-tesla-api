# Tasks — RM67-analytics-add-monthly-charging-aggregates

Each task names its file(s), its `depends_on`, and whether it is `parallel_ok`. A task
marked `[leader-owned]` touches a file outside `internal/analytics/` and needs an
explicit grant or the leader's own hand.

**Wave A — offline, no database, run first.** TASK-1 and TASK-7 touch disjoint files
and need no database at all — `go build`/`go vet` is the only signal either needs.

**Wave B — offline, depends on Wave A's shapes existing.** TASK-2 widens the
constructor and wires the new fetches; still no database call happens at compile time.

**Wave C — needs a real database, run last.** TASK-4 is the only task that must run
against a live Postgres. TASK-5 and TASK-6 are documentation-only and need no database,
but they describe the tier's *final* shape, so they wait for TASK-2.

## Tasks

- [ ] **TASK-1** — New file `internal/analytics/monthly_charging.go`:
  `chargeTally` (with `add`), `EndingBatteryDist.addToBucket`, `aggregateChargingMonth`,
  and `monthBounds` — exactly as specified in `design.md` D1–D4. `addToBucket` is a new
  method on the existing `EndingBatteryDist` type (`analytics.go`); it does not move or
  redefine that type. No database import — this file compiles with no live Postgres.
  Plus the offline unit test `internal/analytics/monthly_charging_test.go` covering
  T-CHG-1, T-CHG-2, and T-CHG-3 from `design.md`'s Test Contract.
  Files: `internal/analytics/monthly_charging.go`,
  `internal/analytics/monthly_charging_test.go`.
  depends_on: none. parallel_ok: yes (with TASK-7).

- [ ] **TASK-2** — Widen `MonthlySyncer`'s construction and `SyncMonth`'s body:
  - `internal/analytics/analytics.go`: widen `NewMonthlySyncer`'s signature to
    `(pool *pgxpool.Pool, capacity charging.MonthlyCapacityReader, charges
    charging.Reader, supercharger charging.SuperchargerSessionAnalyticsReader)
    MonthlySyncer`, passing the two new arguments through to `newMonthlySyncer`
    unchanged (`design.md` D6). Also apply the three doc-comment edits from `design.md`
    D8, on `EndingBatteryDist`, `VehicleMonthlyMetrics`, and
    `MonthlySyncer.SyncMonth`'s own comments — remove the "a later change computes
    them" language now that this tier is that later change.
  - `internal/analytics/monthly_sync.go`: add the `charges`/`supercharger` fields to
    `monthlySyncer` and to `newMonthlySyncer` (D6); in `SyncMonth`, call `monthBounds`
    (TASK-1) to get the month's start/end, fetch external charges with
    `s.charges.ListEntriesByVehicleBetween(ctx, teslaID, monthStart, monthEnd)` and
    Supercharger sessions with `s.supercharger.ListSessionsByVehicleBetween(ctx,
    teslaID, monthStart.AddDate(0,0,-1), monthEnd.AddDate(0,0,1))` (D5), call
    `aggregateChargingMonth` (TASK-1) over both results, and replace the current
    zero-literal `ExtAc*`/`ExtDc*`/`Sc*` params on the `UpsertVehicleMonthlyMetric` call
    with the three tallies' real fields — converting each `*Cost` through the existing
    `pgNumericFromFloat64` helper (`mapping.go`, unchanged) exactly as `CapacityKWh`'s
    sibling fields already do. `Currency` stays the literal `"COP"` (RD15) — this line
    does not change.
  Files: `internal/analytics/analytics.go`, `internal/analytics/monthly_sync.go`.
  depends_on: TASK-1 (needs `monthBounds`/`aggregateChargingMonth`/`chargeTally`).
  parallel_ok: no.

- [ ] **TASK-3** — Update `newTestMonthlySyncer` in
  `internal/analytics/db_monthly_sync_integration_test.go` to pass the two new
  constructor arguments — `charging.NewReader(pool)` and
  `charging.NewSuperchargerSessionAnalyticsReader(pool)` — so the file keeps compiling
  once TASK-2 lands. This is required regardless of whether TASK-4's new tests are
  written yet; every existing test in this file calls this helper.
  Files: `internal/analytics/db_monthly_sync_integration_test.go`.
  depends_on: TASK-2 (needs the final constructor signature). parallel_ok: no.

- [ ] **TASK-4** — Extend `internal/analytics/db_monthly_sync_integration_test.go`
  (package `analytics`, `TEST_DATABASE_URL`-gated) with T-CHG-INT-1, T-CHG-INT-2, and
  T-CHG-INT-3 from `design.md`'s Test Contract:
  - T-CHG-INT-1 seeds external entries through the real `charging.NewWriter(pool).Create`
    writer.
  - T-CHG-INT-2 seeds two Supercharger sessions with a direct SQL `INSERT` into
    `charging.supercharger_sessions`, mirroring `db_integration_test.go`'s
    `seedSuperchargerSession` convention for the identical "no public writer for a bare
    session" case, then asserts both March's and February's sync pick up the right one.
  - T-CHG-INT-3 mirrors tier 2's own idempotence test (T6): re-running `SyncMonth` for
    the same `(teslaID, period)` must not double-count.
  This task needs `TEST_DATABASE_URL` or Docker to actually run; it still compiles
  without either (self-skipping), matching this module's existing convention.
  Files: `internal/analytics/db_monthly_sync_integration_test.go`.
  depends_on: TASK-2, TASK-3. parallel_ok: no.

- [ ] **TASK-5** — Update `internal/analytics/AGENTS.md`:
  - "Public interface (the port)" — the `MonthlySyncer`/`SyncMonth` table row: state
    that it now derives the `ext_*`/`sc_*` figures for real, not the tier-2 zero
    placeholder.
  - "Allowed / forbidden imports" — the existing `internal/charging` paragraph already
    lists `charging.Reader` and `charging.SuperchargerSessionAnalyticsReader`; add one
    sentence noting `MonthlySyncer.SyncMonth`, not only `Recalculate`, now calls
    `ListEntriesByVehicleBetween` and `ListSessionsByVehicleBetween` through them.
  Files: `internal/analytics/AGENTS.md`.
  depends_on: TASK-2 (needs the final shape). parallel_ok: yes (with TASK-4).

- [ ] **TASK-6** `[leader-owned]` — Check `kkpa/context/` for guides this change
  invalidates:
  - `kkpa/context/workflows/vehicle-monthly-metrics.md` (if it exists after tier 2's own
    TASK-11) — extend it to say the charging columns are now real, not zero-filled.
  - `kkpa/context/entities/vehicle-metrics/guide.md` — check whether its description of
    `vehicle_monthly_metrics`'s charging columns needs the same "now real" update.
  Files: under `kkpa/context/` (outside `internal/analytics/`).
  depends_on: TASK-2. parallel_ok: yes.

- [ ] **TASK-7** `[leader-owned]` — Check the root `README.md`'s "Project Structure"
  tree and "Architecture" table for whether this change makes either stale. Based on
  tier 2's identical check (its own TASK-12), this is expected to need no edit — but the
  check itself, and its outcome, must be recorded, not assumed (`CLAUDE.md`'s "docs
  track structural change" rule).
  Files: `README.md` (outside `internal/analytics/`).
  depends_on: none. parallel_ok: yes (with TASK-1).

## Not in this change — do not do these

- **Wiring the nightly trigger.** That is `RM67-app-add-monthly-metrics-step` (tier 4),
  in `internal/app`. Nothing there changes here, and `NewMonthlySyncer` is still called
  from nowhere in production.
- **Changing the migration, the table shape, or `UpsertVehicleMonthlyMetric`'s
  statement.** None of the three needs a change (`design.md` D7) — this tier only
  changes what Go computes before that statement runs.
- **Adding a new `charging` port.** Both reads this tier needs already exist
  (`design.md` fact 3, roadmap RD9). Do not widen `charging.Reader` or
  `charging.SuperchargerSessionAnalyticsReader` "for symmetry."
- **Filtering external charges by `Status`, or resolving multi-currency cost totals
  into more than one currency.** Both were open questions tier 2's design.md flagged
  for this tier; RD15 and RD16 close them. Do not reopen either.
- **Adding a `{bucket}` count column split by whether `EndBatteryPct` was present.**
  Not asked for by this tier's scope or by RD17; the existing `*_entry_count`/
  `*_session_count` columns already count every record regardless.
