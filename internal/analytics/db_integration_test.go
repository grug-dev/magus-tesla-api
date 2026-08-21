// File db_integration_test.go holds this module's DATABASE_URL-gated
// DB-integration tests (RM29-analytics-add-vehicle-metrics, Wave 6) — this
// module's FIRST-EVER DB-backed test file, run against a live Postgres
// provisioned by TestMain (testdb_test.go), auto-provisioned via
// testcontainers-go when DATABASE_URL is unset/unreachable
// (ai/go-conventions.md §persistence).
//
// # Status of this file relative to tasks.md Wave 6
//
// All of 6.1, 6.2, 6.3 and 6.4 are implemented here. The original blocker
// (see Wave 6b in tasks.md) was that this package's own auto-provisioned test
// database could contain only its own migrations (//go:embed cannot cross a
// ".." boundary) and that telemetry exposes no public writer for a single
// vehicle_snapshots row or for a supercharger_sessions row at all. The owner
// resolved this as decision D19 (tasks.md "Wave 6b"): testdb_test.go now
// provisions via testdb.ProvisionDirs across three modules' migration
// directories (this package's own, ../telemetry/db/migrations,
// ../charging/db/migrations — see testdb_test.go), so vehicle_snapshots,
// supercharger_sessions and manual_charge_entries all exist in the test
// database; and fixture seeding for the two sources with no public writer
// (vehicle_snapshots, supercharger_sessions) uses direct SQL INSERT
// (seedSnapshot / seedSuperchargerSession below) rather than a module writer.
// This supersedes 6.1's original "not direct SQL" clause per D19 — tasks are
// append-only, so that clause stays in tasks.md unedited, but every
// assertion in this file matches design.md's Test Contract exactly.
//
// manual_charge_entries DOES have a clean public writer,
// charging.NewWriter(pool).Create, and it is genuinely usable in bounds — but
// none of design.md's Test Contract fixtures (A/B/C) or the D7/D8/D-Test-
// Contract Reconcile scenarios this file covers include a manual entry, so
// no test below calls it. A future fixture that needs one should use
// charging.NewWriter(pool).Create, not direct SQL, exactly as this file uses
// telemetry.NewReader/NewSuperchargerReader/charging.NewReader (never
// telemetrydb/chargingdb) for every READ against seeded data.
package analytics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	analyticsdb "github.com/cristianpena/magus-tesla-api/internal/analytics/db"
	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// newTestPool builds a pgxpool.Pool against the test Postgres provisioned by
// TestMain (testdb_test.go). Mirrors internal/telemetry/db_integration_test.go's
// newTestStore skip-when-no-DB convention.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDSN == "" {
		t.Skip("no test Postgres: set DATABASE_URL or start Docker to run the DB-backed tests")
	}
	pool, err := pgxpool.New(context.Background(), testDSN)
	if err != nil {
		t.Fatalf("connecting to Postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// cleanupVehicleMetrics removes any vehicle_metrics/vehicle_metric_watermarks
// rows this test created, scoped to one (accountID, teslaID), so a shared DB
// stays tidy across test runs (mirrors telemetry/db_integration_test.go's
// cleanupVehicle one level up).
func cleanupVehicleMetrics(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, "DELETE FROM vehicle_metrics WHERE account_id = $1 AND tesla_id = $2", accountID, teslaID)
		_, _ = pool.Exec(ctx, "DELETE FROM vehicle_metric_watermarks WHERE account_id = $1 AND tesla_id = $2", accountID, teslaID)
	})
}

// fixtureAPair returns a (predecessor, current) telemetry.Snapshot pair
// shaped like design.md's Test Contract Fixture A, scoped to the given
// vehicle — enough for deriveVehicleMetrics to emit exactly one row (D9/D10),
// so Recalculate's UPSERT+DELETE transaction has real work to do. The exact
// derived values are NOT asserted by the tests in this file (that is
// task 6.1's job, blocked — see the file-level comment above); only that the
// write succeeds and the three source ports were called with the right
// window/scoping, or that an error from one of them propagates.
func fixtureAPair(accountID uuid.UUID, teslaID int64) (prev, cur telemetry.Snapshot) {
	prev = telemetry.Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 10, 3, 30, 0, 0, time.UTC),
		CapturedDate:    time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		OdometerKm:      1000.0,
		BatteryLevelPct: 80,
		BatteryRangeKm:  300.0,
	}
	usedPct := 15
	distanceKm := 50.0
	daysSpanned := 1
	cur = telemetry.Snapshot{
		AccountID:              accountID,
		TeslaID:                teslaID,
		CapturedAt:             time.Date(2026, 8, 11, 3, 30, 0, 0, time.UTC),
		CapturedDate:           time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC),
		OdometerKm:             1050.0,
		BatteryLevelPct:        65,
		BatteryRangeKm:         280.0,
		DistanceTraveledKmCalc: &distanceKm,
		BatteryUsedPctCalc:     &usedPct,
		DaysSpannedCalc:        &daysSpanned,
	}
	return prev, cur
}

// TestRecalculate_FetchWindows_MatchLookbackShape asserts task 6.4(a): the
// exact [start, end] lookback window Recalculate passes to each of the three
// source ports (design.md D11 — telemetry: start-1/end+1; supercharger:
// start-1/end+2; manual: start-1/end, no tail). Uses fakes, not the live DB,
// for the source ports (see file-level comment); only vehicle_metrics itself
// is real, backing the UPSERT/DELETE half of Recalculate.
func TestRecalculate_FetchWindows_MatchLookbackShape(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	const teslaID = int64(910001)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	prev, cur := fixtureAPair(accountID, teslaID)
	fakeTelemetry := &fakeTelemetryReader{snapshots: []telemetry.Snapshot{prev, cur}}
	fakeSupercharger := &fakeSuperchargerReader{}
	fakeManual := &fakeManualReader{}

	rec := NewRecalculator(pool, fakeTelemetry, fakeSupercharger, fakeManual)

	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	if err := rec.Recalculate(context.Background(), accountID, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	wantTelemetryStart := start.AddDate(0, 0, -1)
	wantTelemetryEnd := end.AddDate(0, 0, 1)
	if !fakeTelemetry.gotBetweenStart.Equal(wantTelemetryStart) || !fakeTelemetry.gotBetweenEnd.Equal(wantTelemetryEnd) {
		t.Errorf("telemetry window: want [%s, %s], got [%s, %s]",
			wantTelemetryStart, wantTelemetryEnd, fakeTelemetry.gotBetweenStart, fakeTelemetry.gotBetweenEnd)
	}

	wantSuperchargerStart := start.AddDate(0, 0, -1)
	wantSuperchargerEnd := end.AddDate(0, 0, 2)
	if !fakeSupercharger.gotBetweenStart.Equal(wantSuperchargerStart) || !fakeSupercharger.gotBetweenEnd.Equal(wantSuperchargerEnd) {
		t.Errorf("supercharger window: want [%s, %s], got [%s, %s]",
			wantSuperchargerStart, wantSuperchargerEnd, fakeSupercharger.gotBetweenStart, fakeSupercharger.gotBetweenEnd)
	}

	wantManualStart := start.AddDate(0, 0, -1)
	wantManualEnd := end
	if !fakeManual.gotBetweenStart.Equal(wantManualStart) || !fakeManual.gotBetweenEnd.Equal(wantManualEnd) {
		t.Errorf("manual window: want [%s, %s], got [%s, %s]",
			wantManualStart, wantManualEnd, fakeManual.gotBetweenStart, fakeManual.gotBetweenEnd)
	}
}

// TestRecalculate_AccountIDScoping_PassedToEveryPort asserts task 6.4(b):
// accountID/teslaID scoping reaches every one of the three source ports —
// the coverage rescue for the offline test tasks.md records as removed
// (TestConsumedByDay_AccountIDScoping_PassedToEveryPort), now asserted
// against Recalculate (its new fetcher) instead of the old live ConsumedByDay.
func TestRecalculate_AccountIDScoping_PassedToEveryPort(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	const teslaID = int64(910002)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	prev, cur := fixtureAPair(accountID, teslaID)
	fakeTelemetry := &fakeTelemetryReader{snapshots: []telemetry.Snapshot{prev, cur}}
	fakeSupercharger := &fakeSuperchargerReader{}
	fakeManual := &fakeManualReader{}

	rec := NewRecalculator(pool, fakeTelemetry, fakeSupercharger, fakeManual)

	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	end := start
	if err := rec.Recalculate(context.Background(), accountID, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	if fakeTelemetry.gotAccountID != accountID || fakeTelemetry.gotTeslaID != teslaID {
		t.Errorf("telemetry scoping: want (%s, %d), got (%s, %d)",
			accountID, teslaID, fakeTelemetry.gotAccountID, fakeTelemetry.gotTeslaID)
	}
	if fakeSupercharger.gotAccountID != accountID || fakeSupercharger.gotTeslaID != teslaID {
		t.Errorf("supercharger scoping: want (%s, %d), got (%s, %d)",
			accountID, teslaID, fakeSupercharger.gotAccountID, fakeSupercharger.gotTeslaID)
	}
	if fakeManual.gotAccountID != accountID || fakeManual.gotTeslaID != teslaID {
		t.Errorf("manual scoping: want (%s, %d), got (%s, %d)",
			accountID, teslaID, fakeManual.gotAccountID, fakeManual.gotTeslaID)
	}
}

// TestRecalculate_TelemetryError_Propagates asserts task 6.4(c) for the
// telemetry source: an error from SnapshotsByVehicleBetween propagates from
// Recalculate rather than being swallowed, and short-circuits before the
// supercharger/manual ports are ever called.
func TestRecalculate_TelemetryError_Propagates(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	const teslaID = int64(910003)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	sentinel := errors.New("boom: telemetry unavailable")
	fakeTelemetry := &fakeTelemetryReader{err: sentinel}
	fakeSupercharger := &fakeSuperchargerReader{}
	fakeManual := &fakeManualReader{}

	rec := NewRecalculator(pool, fakeTelemetry, fakeSupercharger, fakeManual)

	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	err := rec.Recalculate(context.Background(), accountID, teslaID, start, start)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Recalculate error: want wrapping %v, got %v", sentinel, err)
	}
	if fakeSupercharger.gotAccountID != uuid.Nil {
		t.Errorf("supercharger port must not be called once telemetry fails, got accountID=%s", fakeSupercharger.gotAccountID)
	}
	if fakeManual.gotAccountID != uuid.Nil {
		t.Errorf("manual port must not be called once telemetry fails, got accountID=%s", fakeManual.gotAccountID)
	}
}

// TestRecalculate_SuperchargerError_Propagates asserts task 6.4(c) for the
// supercharger source: an error from SuperchargerSessionsByVehicleBetween
// propagates, short-circuiting before the manual port is called.
func TestRecalculate_SuperchargerError_Propagates(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	const teslaID = int64(910004)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	sentinel := errors.New("boom: supercharger unavailable")
	fakeTelemetry := &fakeTelemetryReader{}
	fakeSupercharger := &fakeSuperchargerReader{err: sentinel}
	fakeManual := &fakeManualReader{}

	rec := NewRecalculator(pool, fakeTelemetry, fakeSupercharger, fakeManual)

	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	err := rec.Recalculate(context.Background(), accountID, teslaID, start, start)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Recalculate error: want wrapping %v, got %v", sentinel, err)
	}
	if fakeManual.gotAccountID != uuid.Nil {
		t.Errorf("manual port must not be called once supercharger fails, got accountID=%s", fakeManual.gotAccountID)
	}
}

// TestRecalculate_ManualError_Propagates asserts task 6.4(c) for the manual
// charge-entries source: an error from ListEntriesByVehicleBetween
// propagates from Recalculate rather than being swallowed.
func TestRecalculate_ManualError_Propagates(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	const teslaID = int64(910005)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	sentinel := errors.New("boom: manual entries unavailable")
	fakeTelemetry := &fakeTelemetryReader{}
	fakeSupercharger := &fakeSuperchargerReader{}
	fakeManual := &fakeManualReader{err: sentinel}

	rec := NewRecalculator(pool, fakeTelemetry, fakeSupercharger, fakeManual)

	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	err := rec.Recalculate(context.Background(), accountID, teslaID, start, start)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Recalculate error: want wrapping %v, got %v", sentinel, err)
	}
}

// Note: charging.Entry / charging.NewWriter(pool).Create IS a clean,
// in-bounds public writer for manual charge entries — the one source of the
// three that has one (see the file-level comment). It is not exercised
// anywhere in this file because none of design.md's Test Contract fixtures
// (A/B/C) or the Reconcile scenarios in tasks 6.1-6.3 below include a manual
// entry; a future fixture that needs one should call it directly rather than
// seeding manual_charge_entries via SQL.

// ---------------------------------------------------------------------------
// Seeding helpers (D19) — direct SQL for the two sources with no public
// single-row writer (vehicle_snapshots, supercharger_sessions). Every helper
// reuses mapping.go's pg* conversion helpers, the same domain<->pgtype
// boundary Recalculate's own write path uses, so a seeded row is
// byte-identical in shape to one telemetry's real write path would have
// produced.
// ---------------------------------------------------------------------------

// seedSnapshot inserts one telemetry.Snapshot directly into vehicle_snapshots
// (D19: telemetry exposes no public writer for a single row). Every _calc
// column comes verbatim from the given Snapshot's own pointer fields —
// telemetry computes these in Go at write time (deriveConsumption,
// internal/telemetry/service.go); this helper reproduces that
// already-computed shape rather than re-deriving it, so Recalculate's fetch
// (telemetry.Reader.SnapshotsByVehicleBetween/UpdatedSince, the REAL
// implementation, not a fake) reads back exactly what design.md's Test
// Contract fixtures specify. updatedAt defaults to CapturedAt when the
// caller leaves Snapshot.UpdatedAt at its zero value — callers that need
// precise watermark control (task 6.2's Reconcile tests) set it explicitly.
func seedSnapshot(t *testing.T, pool *pgxpool.Pool, s telemetry.Snapshot) {
	t.Helper()
	updatedAt := s.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = s.CapturedAt
	}
	_, err := pool.Exec(context.Background(), `
		INSERT INTO vehicle_snapshots (
			account_id, tesla_id, captured_at, captured_date, raw_data,
			battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
			odometer_km, inside_temp_c, outside_temp_c, locked, car_version,
			distance_traveled_km_calc, battery_used_pct_calc, km_per_pct_calc,
			estimated_range_km_calc, days_spanned_calc, updated_at
		) VALUES (
			$1, $2, $3, $4, '{}'::jsonb,
			$5, $6, 'Complete', 0,
			$7, 0, 0, false, '',
			$8, $9, $10, $11, $12, $13
		)`,
		s.AccountID, s.TeslaID,
		pgtype.Timestamptz{Time: s.CapturedAt, Valid: true},
		dateFrom(s.CapturedDate),
		int32(s.BatteryLevelPct), s.BatteryRangeKm,
		s.OdometerKm,
		pgFloat8FromPtr(s.DistanceTraveledKmCalc),
		pgInt4FromPtr(s.BatteryUsedPctCalc),
		pgFloat8FromPtr(s.KmPerPctCalc),
		pgFloat8FromPtr(s.EstimatedRangeKmCalc),
		pgInt4FromPtr(s.DaysSpannedCalc),
		pgtype.Timestamptz{Time: updatedAt, Valid: true},
	)
	if err != nil {
		t.Fatalf("seeding vehicle_snapshots: %v", err)
	}
}

// sessionIDSeq hands out globally-unique session_id values across this
// file's tests (supercharger_sessions_session_id_unique). Sequential, not
// atomic: this file's tests never run with t.Parallel().
var sessionIDSeq = time.Now().UnixNano()

func nextSessionID() int64 {
	sessionIDSeq++
	return sessionIDSeq
}

// pgInt8FromPtr maps a *int64 to a nullable pgtype.Int8 — the
// supercharger_sessions.tesla_id shape (nullable BIGINT). Test-local: this
// column belongs to telemetry, not analytics, so the conversion does not
// belong in mapping.go (analytics' own DB-facing mapping boundary).
func pgInt8FromPtr(v *int64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{Valid: false}
	}
	return pgtype.Int8{Int64: *v, Valid: true}
}

// pgInt2FromIntPtr maps a *int to a nullable pgtype.Int2 — the
// supercharger_sessions.start_battery_pct/end_battery_pct shape (nullable
// SMALLINT). Test-local for the same reason as pgInt8FromPtr above.
func pgInt2FromIntPtr(v *int) pgtype.Int2 {
	if v == nil {
		return pgtype.Int2{Valid: false}
	}
	return pgtype.Int2{Int16: int16(*v), Valid: true}
}

// seedSuperchargerSession inserts one telemetry.SuperchargerSession directly
// into supercharger_sessions (D19: telemetry exposes no public writer for
// this table at all — upsertSuperchargerSession is unexported, reachable
// only from inside Collector.CollectAll). Returns the session_id actually
// used: s.SessionID when the caller set one, otherwise a fresh value from
// nextSessionID (the column is UNIQUE NOT NULL). updatedAt defaults to
// ChargeStopDateTime when the caller leaves Session.UpdatedAt at its zero
// value.
func seedSuperchargerSession(t *testing.T, pool *pgxpool.Pool, s telemetry.SuperchargerSession) int64 {
	t.Helper()
	sessionID := s.SessionID
	if sessionID == 0 {
		sessionID = nextSessionID()
	}
	updatedAt := s.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = s.ChargeStopDateTime
	}
	_, err := pool.Exec(context.Background(), `
		INSERT INTO supercharger_sessions (
			session_id, account_id, vin, tesla_id, site_location_name, country_code,
			charge_start_date_time, charge_stop_date_time, billing_type, vehicle_make_type,
			start_battery_pct, end_battery_pct, raw_data, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'{}'::jsonb,$13)`,
		sessionID, s.AccountID, s.VIN, pgInt8FromPtr(s.TeslaID), s.SiteLocationName, s.CountryCode,
		pgtype.Timestamptz{Time: s.ChargeStartDateTime, Valid: true},
		pgtype.Timestamptz{Time: s.ChargeStopDateTime, Valid: true},
		s.BillingType, s.VehicleMakeType,
		pgInt2FromIntPtr(s.StartBatteryPct), pgInt2FromIntPtr(s.EndBatteryPct),
		pgtype.Timestamptz{Time: updatedAt, Valid: true},
	)
	if err != nil {
		t.Fatalf("seeding supercharger_sessions: %v", err)
	}
	return sessionID
}

// reviseSuperchargerSession simulates a Tesla billing-state revision on an
// already-seeded session (spec.md "A revised Supercharger session weeks old
// is picked up"): charge_start_date_time/charge_stop_date_time are left
// untouched (the session's own calendar day never moves), only
// end_battery_pct and updated_at change — exactly what a real Tesla
// re-fetch-and-UPSERT would do to a session whose fee/battery data was
// corrected post-session (design DBS3 in telemetry's own migration comment).
func reviseSuperchargerSession(t *testing.T, pool *pgxpool.Pool, sessionID int64, endBatteryPct int, updatedAt time.Time) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`UPDATE supercharger_sessions SET end_battery_pct = $1, updated_at = $2 WHERE session_id = $3`,
		int16(endBatteryPct), pgtype.Timestamptz{Time: updatedAt, Valid: true}, sessionID,
	)
	if err != nil {
		t.Fatalf("revising supercharger_sessions: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Read-back helpers — direct SQL against this module's OWN tables (never
// telemetrydb/chargingdb). Reuse analyticsdb.VehicleMetric (models.go) as the
// scan target rather than a hand-rolled struct: its field types already
// match vehicle_metrics' columns exactly (the same generated type
// UpsertVehicleMetric's caller and this file's assertions both reason about).
// ---------------------------------------------------------------------------

// fetchVehicleMetric SELECTs every non-key column of one vehicle_metrics row
// by its (account_id, tesla_id, metric_date) — the table's own UNIQUE index
// (Index Plan #1) — for the "assert every column" requirement in task 6.1.
// Returns ok=false when no row exists (never a zero-value row masquerading
// as "found").
func fetchVehicleMetric(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64, metricDate time.Time) (analyticsdb.VehicleMetric, bool) {
	t.Helper()
	var m analyticsdb.VehicleMetric
	err := pool.QueryRow(context.Background(), `
		SELECT id, account_id, tesla_id, metric_date, battery_level_pct, odometer_km,
		       battery_range_km, distance_traveled_km_calc, battery_used_pct_calc,
		       km_per_pct_calc, estimated_range_km_calc, days_spanned_calc,
		       consumed_pct, flagged, missing_charging_type, created_at, updated_at
		FROM vehicle_metrics
		WHERE account_id = $1 AND tesla_id = $2 AND metric_date = $3`,
		accountID, teslaID, dateFrom(metricDate),
	).Scan(&m.ID, &m.AccountID, &m.TeslaID, &m.MetricDate, &m.BatteryLevelPct,
		&m.OdometerKm, &m.BatteryRangeKm, &m.DistanceTraveledKmCalc, &m.BatteryUsedPctCalc,
		&m.KmPerPctCalc, &m.EstimatedRangeKmCalc, &m.DaysSpannedCalc, &m.ConsumedPct,
		&m.Flagged, &m.MissingChargingType, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return analyticsdb.VehicleMetric{}, false
	}
	if err != nil {
		t.Fatalf("querying vehicle_metrics: %v", err)
	}
	return m, true
}

// fetchWatermark SELECTs one (account_id, tesla_id, source) cursor's
// source_updated_at directly from vehicle_metric_watermarks (the table's own
// UNIQUE index, Index Plan #4). Returns ok=false when no watermark row
// exists yet for this source (design.md D7's "no row = epoch" case) — never
// a zero time.Time masquerading as a real cursor value.
func fetchWatermark(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64, source string) (time.Time, bool) {
	t.Helper()
	var ts pgtype.Timestamptz
	err := pool.QueryRow(context.Background(),
		`SELECT source_updated_at FROM vehicle_metric_watermarks WHERE account_id = $1 AND tesla_id = $2 AND source = $3`,
		accountID, teslaID, source,
	).Scan(&ts)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false
	}
	if err != nil {
		t.Fatalf("querying vehicle_metric_watermarks: %v", err)
	}
	return ts.Time, true
}

// ---------------------------------------------------------------------------
// Fixture builders — design.md's Test Contract Fixtures A/B/C, reproduced
// here (not imported from consumed_test.go — package-internal _test.go files
// don't export across files in the sense of needing an import, but each
// fixture here additionally carries KmPerPctCalc/EstimatedRangeKmCalc, which
// TestDeriveVehicleMetrics_FixtureA/B/C's own local fixtures need too — kept
// side by side rather than factored into a single shared helper because the
// two test families assert different things (pure-function output there,
// full-column persisted-row content plus Reader read-back here) and
// design.md's contract is the single source both are checked against).
// ---------------------------------------------------------------------------

// metricsFixtureA returns design.md's Test Contract Fixture A: a plain day,
// no charge events. cur carries every _calc column telemetry would have
// computed at write time — deriveVehicleMetrics only copies these verbatim
// (D9/D10); direct-SQL seeding (D19) must supply them explicitly.
func metricsFixtureA(accountID uuid.UUID, teslaID int64) (prev, cur telemetry.Snapshot) {
	prev = telemetry.Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 10, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 8, 10),
		OdometerKm:      1000.0,
		BatteryLevelPct: 80,
		BatteryRangeKm:  300.0,
	}
	cur = telemetry.Snapshot{
		AccountID:              accountID,
		TeslaID:                teslaID,
		CapturedAt:             time.Date(2026, 8, 11, 3, 30, 0, 0, time.UTC),
		CapturedDate:           day(2026, 8, 11),
		OdometerKm:             1050.0,
		BatteryLevelPct:        65,
		BatteryRangeKm:         280.0,
		DistanceTraveledKmCalc: fp(50.0),
		BatteryUsedPctCalc:     intPtr(15),
		KmPerPctCalc:           fp(50.0 / 15.0),
		EstimatedRangeKmCalc:   fp(50.0 / 15.0 * 100),
		DaysSpannedCalc:        intPtr(1),
	}
	return prev, cur
}

// metricsFixtureB returns design.md's Test Contract Fixture B: negative
// odometer clamp + flagged/missing-charge day. No Supercharger session, no
// manual entry seeded anywhere near this vehicle — inferMissingChargingType
// must fall through to its "MANUAL otherwise" branch (no session found in
// the interval at all). design.md's contract does not specify a
// battery_range_km value for this fixture, so BatteryRangeKm is set but not
// asserted against by name in the tests below (only battery_level_pct and
// odometer_km are named for this fixture in design.md's "raw observations"
// row).
func metricsFixtureB(accountID uuid.UUID, teslaID int64) (prev, cur telemetry.Snapshot) {
	prev = telemetry.Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 12, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 8, 12),
		OdometerKm:      2000.0,
		BatteryLevelPct: 40,
		BatteryRangeKm:  250.0,
	}
	cur = telemetry.Snapshot{
		AccountID:              accountID,
		TeslaID:                teslaID,
		CapturedAt:             time.Date(2026, 8, 13, 3, 30, 0, 0, time.UTC),
		CapturedDate:           day(2026, 8, 13),
		OdometerKm:             1998.0,
		BatteryLevelPct:        85,
		BatteryRangeKm:         260.0,
		DistanceTraveledKmCalc: fp(-2.0),
		BatteryUsedPctCalc:     intPtr(-45),
		DaysSpannedCalc:        intPtr(1),
	}
	return prev, cur
}

// metricsFixtureC returns design.md's Test Contract Fixture C: a vehicle's
// true first-ever snapshot, no predecessor row seeded at all.
func metricsFixtureC(accountID uuid.UUID, teslaID int64) telemetry.Snapshot {
	return telemetry.Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 5, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 8, 5),
		OdometerKm:      500.0,
		BatteryLevelPct: 90,
		BatteryRangeKm:  320.0,
	}
}

// newRealRecalculator builds a Recalculator over the REAL telemetry/charging
// Readers (never fakes) against the shared test pool — task 6.1/6.2's whole
// point is to exercise the real cross-module read path Recalculate depends
// on, unlike task 6.4's fakeTelemetryReader/fakeSuperchargerReader/
// fakeManualReader (reader_test.go), which stay in use only for 6.4's own
// fetch-shape assertions above.
func newRealRecalculator(pool *pgxpool.Pool) Recalculator {
	return NewRecalculator(pool, telemetry.NewReader(pool), telemetry.NewSuperchargerReader(pool), charging.NewReader(pool))
}

// newRealReader builds a Reader the same way — real telemetry/charging
// Readers, a fresh *pgxpool-backed analyticsdb.Queries via NewReader's own
// pool parameter. account is a no-op fakeVehicleLookup (reader_test.go):
// ConsumedByDay/OdometerDeltaByDay never call it.
func newRealReader(pool *pgxpool.Pool) Reader {
	return NewReader(pool, telemetry.NewReader(pool), telemetry.NewSuperchargerReader(pool), charging.NewReader(pool), &fakeVehicleLookup{}, DefaultWindow)
}

// ===========================================================================
// Task 6.1 — TestRecalculate_FixtureA/B/C: seed via direct SQL (D19), call
// Recalculate, assert the persisted vehicle_metrics row matches design.md's
// Test Contract exactly, all columns.
// ===========================================================================

func TestRecalculate_FixtureA(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	const teslaID = int64(920001)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	prev, cur := metricsFixtureA(accountID, teslaID)
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)

	rec := newRealRecalculator(pool)
	start := day(2026, 8, 10)
	end := start
	if err := rec.Recalculate(ctx, accountID, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	row, ok := fetchVehicleMetric(t, pool, accountID, teslaID, start)
	if !ok {
		t.Fatal("expected a vehicle_metrics row for Fixture A, found none")
	}

	if row.BatteryLevelPct != 65 {
		t.Errorf("BatteryLevelPct: want 65, got %d", row.BatteryLevelPct)
	}
	if row.OdometerKm != 1050.0 {
		t.Errorf("OdometerKm: want 1050.0, got %v", row.OdometerKm)
	}
	if row.BatteryRangeKm != 280.0 {
		t.Errorf("BatteryRangeKm: want 280.0, got %v", row.BatteryRangeKm)
	}
	if !row.DistanceTraveledKmCalc.Valid || !approxEqual(row.DistanceTraveledKmCalc.Float64, 50.0) {
		t.Errorf("DistanceTraveledKmCalc: want 50.0, got %+v", row.DistanceTraveledKmCalc)
	}
	if !row.BatteryUsedPctCalc.Valid || row.BatteryUsedPctCalc.Int32 != 15 {
		t.Errorf("BatteryUsedPctCalc: want 15, got %+v", row.BatteryUsedPctCalc)
	}
	wantKmPerPct := 50.0 / 15.0
	if !row.KmPerPctCalc.Valid || !approxEqual(row.KmPerPctCalc.Float64, wantKmPerPct) {
		t.Errorf("KmPerPctCalc: want %v, got %+v", wantKmPerPct, row.KmPerPctCalc)
	}
	wantEstRange := wantKmPerPct * 100
	if !row.EstimatedRangeKmCalc.Valid || !approxEqual(row.EstimatedRangeKmCalc.Float64, wantEstRange) {
		t.Errorf("EstimatedRangeKmCalc: want %v, got %+v", wantEstRange, row.EstimatedRangeKmCalc)
	}
	if !row.DaysSpannedCalc.Valid || row.DaysSpannedCalc.Int32 != 1 {
		t.Errorf("DaysSpannedCalc: want 1, got %+v", row.DaysSpannedCalc)
	}
	if !row.ConsumedPct.Valid || !approxEqual(row.ConsumedPct.Float64, 15.0) {
		t.Errorf("ConsumedPct: want 15.0, got %+v", row.ConsumedPct)
	}
	if row.Flagged != false {
		t.Errorf("Flagged: want false (the actual boolean value), got %v", row.Flagged)
	}
	if row.MissingChargingType.Valid {
		t.Errorf("MissingChargingType: want NULL, got %v", row.MissingChargingType.String)
	}
}

func TestRecalculate_FixtureB(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	const teslaID = int64(920002)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	prev, cur := metricsFixtureB(accountID, teslaID)
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)
	// No Supercharger session, no manual entry seeded near this vehicle at
	// all -- design.md: "no matching charge event logged in either source".

	rec := newRealRecalculator(pool)
	start := day(2026, 8, 12)
	end := start
	if err := rec.Recalculate(ctx, accountID, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	row, ok := fetchVehicleMetric(t, pool, accountID, teslaID, start)
	if !ok {
		t.Fatal("expected a vehicle_metrics row for Fixture B, found none")
	}

	if row.BatteryLevelPct != 85 {
		t.Errorf("BatteryLevelPct: want 85, got %d", row.BatteryLevelPct)
	}
	if row.OdometerKm != 1998.0 {
		t.Errorf("OdometerKm: want 1998.0, got %v", row.OdometerKm)
	}
	if !row.DistanceTraveledKmCalc.Valid || !approxEqual(row.DistanceTraveledKmCalc.Float64, -2.0) {
		t.Errorf("DistanceTraveledKmCalc: want -2.0 (raw, unclamped), got %+v", row.DistanceTraveledKmCalc)
	}
	if !row.BatteryUsedPctCalc.Valid || row.BatteryUsedPctCalc.Int32 != -45 {
		t.Errorf("BatteryUsedPctCalc: want -45, got %+v", row.BatteryUsedPctCalc)
	}
	if row.KmPerPctCalc.Valid {
		t.Errorf("KmPerPctCalc: want NULL (divisor -45 <= 0), got %v", row.KmPerPctCalc.Float64)
	}
	if row.EstimatedRangeKmCalc.Valid {
		t.Errorf("EstimatedRangeKmCalc: want NULL (same guard), got %v", row.EstimatedRangeKmCalc.Float64)
	}
	if !row.DaysSpannedCalc.Valid || row.DaysSpannedCalc.Int32 != 1 {
		t.Errorf("DaysSpannedCalc: want 1, got %+v", row.DaysSpannedCalc)
	}
	if !row.ConsumedPct.Valid || !approxEqual(row.ConsumedPct.Float64, -45.0) {
		t.Errorf("ConsumedPct: want -45.0 (no matched charge event), got %+v", row.ConsumedPct)
	}
	if row.Flagged != true {
		t.Errorf("Flagged: want true (the actual boolean value, ConsumedPct < 0), got %v", row.Flagged)
	}
	if !row.MissingChargingType.Valid || row.MissingChargingType.String != string(telemetry.MissingChargingTypeManual) {
		t.Errorf("MissingChargingType: want MANUAL, got %+v", row.MissingChargingType)
	}
}

func TestRecalculate_FixtureC(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	const teslaID = int64(920003)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	cur := metricsFixtureC(accountID, teslaID)
	seedSnapshot(t, pool, cur)

	rec := newRealRecalculator(pool)
	start := day(2026, 8, 4)
	end := start
	if err := rec.Recalculate(ctx, accountID, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	row, ok := fetchVehicleMetric(t, pool, accountID, teslaID, start)
	if !ok {
		t.Fatal("Fixture C: expected a DENSE vehicle_metrics row for the predecessor-less day, found none (design.md D9)")
	}

	if row.BatteryLevelPct != 90 {
		t.Errorf("BatteryLevelPct: want 90, got %d", row.BatteryLevelPct)
	}
	if row.OdometerKm != 500.0 {
		t.Errorf("OdometerKm: want 500.0, got %v", row.OdometerKm)
	}
	if row.BatteryRangeKm != 320.0 {
		t.Errorf("BatteryRangeKm: want 320.0, got %v", row.BatteryRangeKm)
	}
	if row.DistanceTraveledKmCalc.Valid {
		t.Errorf("DistanceTraveledKmCalc: want NULL, got %v", row.DistanceTraveledKmCalc.Float64)
	}
	if row.BatteryUsedPctCalc.Valid {
		t.Errorf("BatteryUsedPctCalc: want NULL, got %v", row.BatteryUsedPctCalc.Int32)
	}
	if row.KmPerPctCalc.Valid {
		t.Errorf("KmPerPctCalc: want NULL, got %v", row.KmPerPctCalc.Float64)
	}
	if row.EstimatedRangeKmCalc.Valid {
		t.Errorf("EstimatedRangeKmCalc: want NULL, got %v", row.EstimatedRangeKmCalc.Float64)
	}
	if row.DaysSpannedCalc.Valid {
		t.Errorf("DaysSpannedCalc: want NULL, got %v", row.DaysSpannedCalc.Int32)
	}
	if row.ConsumedPct.Valid {
		t.Errorf("ConsumedPct: want NULL, got %v", row.ConsumedPct.Float64)
	}
	// Assert the ACTUAL boolean value, not merely falsy/zero (task 6.1's
	// explicit instruction) -- row.Flagged is a real bool scanned from a
	// NOT NULL BOOLEAN column, so there is no zero-vs-NULL ambiguity to
	// begin with, but the comparison is written against `false` explicitly
	// rather than `!row.Flagged` to make that intent visible at the call site.
	if row.Flagged != false {
		t.Errorf("Flagged: want false (NOT NULL, never left to a stray zero-value comparison, design.md D9), got %v", row.Flagged)
	}
	if row.MissingChargingType.Valid {
		t.Errorf("MissingChargingType: want NULL, got %v", row.MissingChargingType.String)
	}
}

// ===========================================================================
// Task 6.2 — TestReconcile_BackfillsOnFirstRun / _Idempotent /
// _RevisedOldSuperchargerSession.
//
// The clock problem: Reconcile clamps its end date to "yesterday" in UTC
// (design.md D8) and has no injectable clock (D13 -- deliberately not
// widened). Every fixture below is seeded comfortably in the past, anchored
// to time.Now().UTC() read once per test rather than a hardcoded calendar
// date, so the clamp is never the boundary under test and these tests stay
// valid no matter when they run.
// ===========================================================================

func TestReconcile_BackfillsOnFirstRun(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	const teslaID = int64(930001)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	day0 := calendarDay(time.Now()).AddDate(0, 0, -20)
	day1 := day0.AddDate(0, 0, 1)

	prev := telemetry.Snapshot{
		AccountID: accountID, TeslaID: teslaID,
		CapturedAt: day0.Add(3*time.Hour + 30*time.Minute), CapturedDate: day0,
		OdometerKm: 500.0, BatteryLevelPct: 70, BatteryRangeKm: 260.0,
	}
	cur := telemetry.Snapshot{
		AccountID: accountID, TeslaID: teslaID,
		CapturedAt: day1.Add(3*time.Hour + 30*time.Minute), CapturedDate: day1,
		OdometerKm: 520.0, BatteryLevelPct: 62, BatteryRangeKm: 240.0,
		DistanceTraveledKmCalc: fp(20.0), BatteryUsedPctCalc: intPtr(8),
		KmPerPctCalc: fp(20.0 / 8.0), EstimatedRangeKmCalc: fp(20.0 / 8.0 * 100),
		DaysSpannedCalc: intPtr(1),
	}
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)

	rec := newRealRecalculator(pool)
	if err := rec.Reconcile(ctx, accountID, teslaID); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// D7: no prior watermark is treated as the epoch -- the vehicle's entire
	// stored telemetry history backfills into vehicle_metrics in one pass.
	wantDay := effectiveDay(cur) // == day0
	row, ok := fetchVehicleMetric(t, pool, accountID, teslaID, wantDay)
	if !ok {
		t.Fatalf("expected a vehicle_metrics row for %s after the first-ever Reconcile, found none", wantDay)
	}
	if row.BatteryLevelPct != 62 {
		t.Errorf("BatteryLevelPct: want 62, got %d", row.BatteryLevelPct)
	}
	if !row.BatteryUsedPctCalc.Valid || row.BatteryUsedPctCalc.Int32 != 8 {
		t.Errorf("BatteryUsedPctCalc: want 8, got %+v", row.BatteryUsedPctCalc)
	}

	// The vehicle_snapshots source watermark now exists (design.md D2/D7).
	if _, ok := fetchWatermark(t, pool, accountID, teslaID, sourceVehicleSnapshots); !ok {
		t.Error("want a vehicle_snapshots watermark row after the first Reconcile, found none")
	}
	// A source with zero returned rows leaves NO watermark row at all, even
	// on the very first run (design.md D2/D3's idempotence contract) -- no
	// Supercharger session and no manual entry was ever seeded for this
	// vehicle, so both of those sources' cursors stay at "no row = epoch".
	if _, ok := fetchWatermark(t, pool, accountID, teslaID, sourceSuperchargerSessions); ok {
		t.Error("want no supercharger_sessions watermark row (no session data ever seeded for this vehicle)")
	}
	if _, ok := fetchWatermark(t, pool, accountID, teslaID, sourceManualChargeEntries); ok {
		t.Error("want no manual_charge_entries watermark row (no entry ever seeded for this vehicle)")
	}
}

func TestReconcile_Idempotent(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	const teslaID = int64(930002)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	day0 := calendarDay(time.Now()).AddDate(0, 0, -22)
	day1 := day0.AddDate(0, 0, 1)

	prev := telemetry.Snapshot{
		AccountID: accountID, TeslaID: teslaID,
		CapturedAt: day0.Add(3*time.Hour + 30*time.Minute), CapturedDate: day0,
		OdometerKm: 1000.0, BatteryLevelPct: 80, BatteryRangeKm: 300.0,
	}
	cur := telemetry.Snapshot{
		AccountID: accountID, TeslaID: teslaID,
		CapturedAt: day1.Add(3*time.Hour + 30*time.Minute), CapturedDate: day1,
		OdometerKm: 1050.0, BatteryLevelPct: 65, BatteryRangeKm: 280.0,
		DistanceTraveledKmCalc: fp(50.0), BatteryUsedPctCalc: intPtr(15),
		KmPerPctCalc: fp(50.0 / 15.0), EstimatedRangeKmCalc: fp(50.0 / 15.0 * 100),
		DaysSpannedCalc: intPtr(1),
	}
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)

	rec := newRealRecalculator(pool)
	if err := rec.Reconcile(ctx, accountID, teslaID); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}

	wantDay := effectiveDay(cur)
	before, ok := fetchVehicleMetric(t, pool, accountID, teslaID, wantDay)
	if !ok {
		t.Fatal("expected a vehicle_metrics row after the first Reconcile")
	}
	watermarkBefore, ok := fetchWatermark(t, pool, accountID, teslaID, sourceVehicleSnapshots)
	if !ok {
		t.Fatal("expected a vehicle_snapshots watermark row after the first Reconcile")
	}
	var rowCountBefore int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM vehicle_metrics WHERE account_id=$1 AND tesla_id=$2`, accountID, teslaID).Scan(&rowCountBefore); err != nil {
		t.Fatalf("counting vehicle_metrics rows: %v", err)
	}

	// Second Reconcile: no source data changed at all since the first run.
	if err := rec.Reconcile(ctx, accountID, teslaID); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}

	after, ok := fetchVehicleMetric(t, pool, accountID, teslaID, wantDay)
	if !ok {
		t.Fatal("expected the vehicle_metrics row to still exist after the idempotent second Reconcile")
	}
	// UpdatedAt legitimately advances on every UPSERT (query.sql's
	// UpsertVehicleMetric always sets updated_at = now(), even when every
	// other column is byte-identical) -- zero it on both sides before
	// comparing so the assertion is about CONTENT, matching design.md's
	// "byte-identical UPSERT" framing (the SET VALUES were identical, not
	// that the row's own bookkeeping column never moves).
	before.UpdatedAt = pgtype.Timestamptz{}
	after.UpdatedAt = pgtype.Timestamptz{}
	if after != before {
		t.Errorf("Reconcile is not idempotent: row changed from %+v to %+v", before, after)
	}
	var rowCountAfter int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM vehicle_metrics WHERE account_id=$1 AND tesla_id=$2`, accountID, teslaID).Scan(&rowCountAfter); err != nil {
		t.Fatalf("counting vehicle_metrics rows: %v", err)
	}
	if rowCountAfter != rowCountBefore {
		t.Errorf("net row count changed: want %d, got %d (design.md's idempotence contract)", rowCountBefore, rowCountAfter)
	}

	watermarkAfter, ok := fetchWatermark(t, pool, accountID, teslaID, sourceVehicleSnapshots)
	if !ok {
		t.Fatal("expected the vehicle_snapshots watermark row to still exist")
	}
	if watermarkAfter.Before(watermarkBefore) {
		t.Errorf("watermark regressed: before %s, after %s (design.md: never regressed)", watermarkBefore, watermarkAfter)
	}
}

func TestReconcile_RevisedOldSuperchargerSession(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	const teslaIDConst = int64(930003)
	teslaID := teslaIDConst // addressable copy for SuperchargerSession.TeslaID (*int64)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	refNow := time.Now().UTC()
	oldDay := calendarDay(refNow).AddDate(0, 0, -25) // "three weeks ago" and then some -- safely before yesterday

	prev := telemetry.Snapshot{
		AccountID: accountID, TeslaID: teslaID,
		CapturedAt: oldDay.Add(3*time.Hour + 30*time.Minute), CapturedDate: oldDay,
		OdometerKm: 1000.0, BatteryLevelPct: 50, BatteryRangeKm: 220.0,
	}
	curDay := oldDay.AddDate(0, 0, 1)
	cur := telemetry.Snapshot{
		AccountID: accountID, TeslaID: teslaID,
		CapturedAt: curDay.Add(3*time.Hour + 30*time.Minute), CapturedDate: curDay,
		OdometerKm: 1010.0, BatteryLevelPct: 45, BatteryRangeKm: 210.0,
		DistanceTraveledKmCalc: fp(10.0), BatteryUsedPctCalc: intPtr(5),
		KmPerPctCalc: fp(10.0 / 5.0), EstimatedRangeKmCalc: fp(10.0 / 5.0 * 100),
		DaysSpannedCalc: intPtr(1),
	}
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)

	t0 := oldDay.Add(11 * time.Hour) // the session's original sync time -- itself weeks old
	session := telemetry.SuperchargerSession{
		AccountID:           accountID,
		TeslaID:             &teslaID,
		ChargeStartDateTime: oldDay.Add(10 * time.Hour),
		ChargeStopDateTime:  oldDay.Add(11 * time.Hour),
		StartBatteryPct:     intPtr(30),
		EndBatteryPct:       intPtr(40),
		UpdatedAt:           t0,
	}
	sessionID := seedSuperchargerSession(t, pool, session)

	rec := newRealRecalculator(pool)
	if err := rec.Reconcile(ctx, accountID, teslaID); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}

	wantDay := effectiveDay(cur) // == oldDay
	before, ok := fetchVehicleMetric(t, pool, accountID, teslaID, wantDay)
	if !ok {
		t.Fatal("expected a vehicle_metrics row after the first Reconcile")
	}
	wantConsumedBefore := 5.0 + 10.0 // BatteryUsedPctCalc(5) + matched session delta (40-30)
	if !before.ConsumedPct.Valid || !approxEqual(before.ConsumedPct.Float64, wantConsumedBefore) {
		t.Fatalf("ConsumedPct before revision: want %v, got %+v", wantConsumedBefore, before.ConsumedPct)
	}

	// Simulate a billing revision "today": end_battery_pct changes and
	// updated_at refreshes, while the session's own calendar day (three-plus
	// weeks old) never moves -- spec.md "A revised Supercharger session weeks
	// old is picked up".
	reviseSuperchargerSession(t, pool, sessionID, 50, refNow)

	if err := rec.Reconcile(ctx, accountID, teslaID); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}

	after, ok := fetchVehicleMetric(t, pool, accountID, teslaID, wantDay)
	if !ok {
		t.Fatal("expected the vehicle_metrics row to still exist after the revision")
	}
	wantConsumedAfter := 5.0 + 20.0 // BatteryUsedPctCalc(5) + revised session delta (50-30)
	if !after.ConsumedPct.Valid || !approxEqual(after.ConsumedPct.Float64, wantConsumedAfter) {
		t.Errorf("ConsumedPct after revision: want %v (the day, well outside any trailing window measured from today, must still be recomputed), got %+v", wantConsumedAfter, after.ConsumedPct)
	}

	watermark, ok := fetchWatermark(t, pool, accountID, teslaID, sourceSuperchargerSessions)
	if !ok {
		t.Fatal("expected a supercharger_sessions watermark row")
	}
	if watermark.Before(t0) {
		t.Errorf("supercharger_sessions watermark did not advance past the original sync time: got %s", watermark)
	}
}

// ===========================================================================
// Task 6.3 — TestReader_ConsumedByDay_ReadsBackWhatRecalculateWrote,
// TestReader_OdometerDeltaByDay_ReadsBackWhatRecalculateWrote,
// TestReader_BothMethods_ExcludeFixtureCRow. These close the loop Wave 4's
// offline reader_test.go tests fake: real Recalculate write, then a real
// Reader read, against the real vehicle_metrics table and the real SQL
// WHERE ... IS NOT NULL filter (D13).
// ===========================================================================

func TestReader_ConsumedByDay_ReadsBackWhatRecalculateWrote(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	const teslaID = int64(940001)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	prev, cur := metricsFixtureA(accountID, teslaID)
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)

	rec := newRealRecalculator(pool)
	start := day(2026, 8, 10)
	end := start
	if err := rec.Recalculate(ctx, accountID, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	rdr := newRealReader(pool)
	got, err := rdr.ConsumedByDay(ctx, accountID, teslaID, start, end)
	if err != nil {
		t.Fatalf("ConsumedByDay: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}
	entry := got[0]
	if !entry.Date.Equal(start) {
		t.Errorf("Date: want %v, got %v", start, entry.Date)
	}
	if !approxEqual(entry.ConsumedPct, 15.0) {
		t.Errorf("ConsumedPct: want 15.0, got %v", entry.ConsumedPct)
	}
	if entry.DistanceKm != 50.0 {
		t.Errorf("DistanceKm: want 50.0, got %v", entry.DistanceKm)
	}
	if entry.Flagged {
		t.Error("want Flagged=false")
	}
	if entry.MissingChargingType != "" {
		t.Errorf("MissingChargingType: want \"\", got %v", entry.MissingChargingType)
	}
	if entry.DaysSpanned != 1 {
		t.Errorf("DaysSpanned: want 1, got %d", entry.DaysSpanned)
	}
}

func TestReader_OdometerDeltaByDay_ReadsBackWhatRecalculateWrote(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	const teslaID = int64(940002)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	prev, cur := metricsFixtureB(accountID, teslaID)
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)

	rec := newRealRecalculator(pool)
	start := day(2026, 8, 12)
	end := start
	if err := rec.Recalculate(ctx, accountID, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	rdr := newRealReader(pool)

	gotOdometer, err := rdr.OdometerDeltaByDay(ctx, accountID, teslaID, start, end)
	if err != nil {
		t.Fatalf("OdometerDeltaByDay: %v", err)
	}
	if len(gotOdometer) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(gotOdometer), gotOdometer)
	}
	if gotOdometer[0].KmDriven != 0.0 {
		t.Errorf("KmDriven: want 0.0 (clamped on read from the stored -2.0), got %v", gotOdometer[0].KmDriven)
	}
	if gotOdometer[0].OdometerKm != 1998.0 {
		t.Errorf("OdometerKm: want 1998.0, got %v", gotOdometer[0].OdometerKm)
	}

	// ConsumedByDay's DistanceKm for the SAME underlying row stays -2.0,
	// unclamped -- the exact divergence design.md's Test Contract calls out
	// (only the odometer chart's displayed delta is ever clamped).
	gotConsumed, err := rdr.ConsumedByDay(ctx, accountID, teslaID, start, end)
	if err != nil {
		t.Fatalf("ConsumedByDay: %v", err)
	}
	if len(gotConsumed) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(gotConsumed), gotConsumed)
	}
	if gotConsumed[0].DistanceKm != -2.0 {
		t.Errorf("DistanceKm: want -2.0 (unclamped), got %v", gotConsumed[0].DistanceKm)
	}
	if !gotConsumed[0].Flagged {
		t.Error("want Flagged=true")
	}
	if gotConsumed[0].MissingChargingType != telemetry.MissingChargingTypeManual {
		t.Errorf("MissingChargingType: want MANUAL, got %v", gotConsumed[0].MissingChargingType)
	}
}

func TestReader_BothMethods_ExcludeFixtureCRow(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	const teslaID = int64(940003)
	cleanupVehicleMetrics(t, pool, accountID, teslaID)

	cur := metricsFixtureC(accountID, teslaID)
	seedSnapshot(t, pool, cur)

	rec := newRealRecalculator(pool)
	start := day(2026, 8, 4)
	end := start
	if err := rec.Recalculate(ctx, accountID, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	// Direct-read confirmation: the row genuinely exists in the real database
	// (design.md: "the exact property a sparse table would not have had").
	if _, ok := fetchVehicleMetric(t, pool, accountID, teslaID, start); !ok {
		t.Fatal("expected the dense vehicle_metrics row for Fixture C's predecessor-less day to exist, found none")
	}

	rdr := newRealReader(pool)

	gotConsumed, err := rdr.ConsumedByDay(ctx, accountID, teslaID, start, end)
	if err != nil {
		t.Fatalf("ConsumedByDay: %v", err)
	}
	if len(gotConsumed) != 0 {
		t.Errorf("ConsumedByDay: want empty (D13's IS NOT NULL filter excludes the predecessor-less row), got %+v", gotConsumed)
	}

	gotOdometer, err := rdr.OdometerDeltaByDay(ctx, accountID, teslaID, start, end)
	if err != nil {
		t.Fatalf("OdometerDeltaByDay: %v", err)
	}
	if len(gotOdometer) != 0 {
		t.Errorf("OdometerDeltaByDay: want empty (D13's IS NOT NULL filter excludes the predecessor-less row), got %+v", gotOdometer)
	}
}
