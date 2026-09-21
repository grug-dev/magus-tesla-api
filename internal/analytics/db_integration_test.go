// File db_integration_test.go holds this module's TEST_DATABASE_URL-gated
// DB-integration tests (RM29-analytics-add-vehicle-metrics, Wave 6) — this
// module's FIRST-EVER DB-backed test file, run against a live Postgres
// provisioned by TestMain (testdb_test.go), auto-provisioned via
// testcontainers-go when TEST_DATABASE_URL is unset/unreachable
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
// charging.NewWriter(pool).Create — TestRecalculate_FixtureD2_ChargeInsideTheGap
// below (RM29-telemetry-drop-derived-columns, tier 4) is the first test in
// this file to use it, exactly as this file uses telemetry.NewReader/
// charging.NewSuperchargerSessionAnalyticsReader/charging.NewReader (never
// telemetrydb/chargingdb) for every READ against seeded data. (The Supercharger
// read is charging's since RM31 tier 3; telemetry's own port, today
// telemetry.NewSuperchargerHistoryReader, is no longer called from this module.)
//
// # RM29-telemetry-drop-derived-columns (tier 4) — tasks 6b.1/6b.2/6b.3
//
// Tier 4 moved the five per-day consumption figures from telemetry
// (deriveConsumption, once computed at write time and stored as
// vehicle_snapshots' own _calc columns) into this module
// (consumption.go's deriveConsumption, computed by Recalculate itself).
// vehicle_snapshots no longer HAS those five columns (telemetry migration
// 20260822000001), so every fixture below that seeds vehicle_snapshots (via
// seedSnapshot) now supplies ONLY the raw observations
// (odometer_km, battery_level_pct, battery_range_km, captured_at,
// captured_date) — the expected vehicle_metrics values are PRODUCED by
// Recalculate's real derivation, never copied off the seed (design.md
// D1/D10, roadmap D10). Fixtures A/B/C's expected values are UNCHANGED from
// tier 3 (the characterization bar), and two more fixtures were added: D
// (a multi-day capture gap — the case telemetry.Reader.SnapshotPrecedingDay
// exists for, design.md D2/D7) and E (the battery_used_pct_calc == 0
// divisor guard, distinct from Fixture B's negative case).
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
	"github.com/cristianpena/magus-tesla-api/internal/clock"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
	"github.com/cristianpena/magus-tesla-api/internal/vehicleref"
)

// newTestPool builds a pgxpool.Pool against the test Postgres provisioned by
// TestMain (testdb_test.go). Mirrors internal/telemetry/db_integration_test.go's
// newTestStore skip-when-no-DB convention.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDSN == "" {
		t.Skip("no test Postgres: set TEST_DATABASE_URL or start Docker to run the DB-backed tests")
	}
	pool, err := pgxpool.New(context.Background(), testDSN)
	if err != nil {
		t.Fatalf("connecting to Postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// cleanupVehicleMetrics removes any vehicle_metrics/vehicle_metric_watermarks
// rows this test created, scoped to one teslaID, so a shared DB stays tidy
// across test runs (mirrors telemetry/db_integration_test.go's cleanupVehicle
// one level up).
//
// It also deletes the telemetry.vehicle_snapshots rows this file inserts
// directly. Telemetry's own helpers never see those rows, so only this
// function can remove them.
func cleanupVehicleMetrics(t *testing.T, pool *pgxpool.Pool, teslaID int64) {
	t.Helper()
	purge := func() {
		ctx := context.Background()
		for _, stmt := range []string{
			"DELETE FROM analytics.vehicle_metrics WHERE tesla_id = $1",
			"DELETE FROM analytics.vehicle_metric_watermarks WHERE tesla_id = $1",
			"DELETE FROM telemetry.vehicle_snapshots WHERE tesla_id = $1",
			// The three charge sources. A seeded charge that outlives its test
			// is counted again by the next run, which silently doubles
			// ConsumedPct instead of failing on the row it came from.
			"DELETE FROM charging.manual_charge_entries WHERE tesla_id = $1",
			"DELETE FROM charging.supercharger_sessions WHERE tesla_id = $1",
			"DELETE FROM telemetry.supercharger_history WHERE tesla_id = $1",
		} {
			_, _ = pool.Exec(ctx, stmt, teslaID)
		}
	}
	// Purge on the way in as well as on the way out. Cleanup alone cannot undo
	// what an earlier interrupted run already left behind, and every caller
	// runs this before it seeds anything.
	purge()
	t.Cleanup(purge)
}

// fixtureAPair returns a (predecessor, current) telemetry.Snapshot pair
// shaped like design.md's Test Contract Fixture A, scoped to the given
// vehicle — enough for deriveVehicleMetrics to emit exactly one row (D9/D10),
// so Recalculate's UPSERT+DELETE transaction has real work to do. The exact
// derived values are NOT asserted by the tests in this file (that is
// task 6.1's job, blocked — see the file-level comment above); only that the
// write succeeds and the three source ports were called with the right
// window/scoping, or that an error from one of them propagates.
func fixtureAPair(teslaID int64) (prev, cur telemetry.Snapshot) {
	prev = telemetry.Snapshot{
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 10, 3, 30, 0, 0, time.UTC),
		CapturedDate:    time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		OdometerKm:      1000.0,
		BatteryLevelPct: 80,
		BatteryRangeKm:  300.0,
	}
	cur = telemetry.Snapshot{
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 11, 3, 30, 0, 0, time.UTC),
		CapturedDate:    time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC),
		OdometerKm:      1050.0,
		BatteryLevelPct: 65,
		BatteryRangeKm:  280.0,
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
	const teslaID = int64(910001)
	cleanupVehicleMetrics(t, pool, teslaID)

	prev, cur := fixtureAPair(teslaID)
	fakeTelemetry := &fakeTelemetryReader{snapshots: []telemetry.Snapshot{prev, cur}}
	fakeSupercharger := &fakeSuperchargerReader{}
	fakeManual := &fakeManualReader{}

	rec := NewRecalculator(pool, fakeTelemetry, fakeSupercharger, fakeManual)

	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	if err := rec.Recalculate(context.Background(), teslaID, start, end); err != nil {
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

// TestRecalculate_TeslaIDScoping_PassedToEveryPort asserts that the teslaID
// reaches every one of the three source ports. An older offline test asserted
// the same thing against the live ConsumedByDay. That read no longer computes
// anything, so the assertion moved here, onto Recalculate, its new fetcher.
func TestRecalculate_TeslaIDScoping_PassedToEveryPort(t *testing.T) {
	pool := newTestPool(t)
	const teslaID = int64(910002)
	cleanupVehicleMetrics(t, pool, teslaID)

	prev, cur := fixtureAPair(teslaID)
	fakeTelemetry := &fakeTelemetryReader{snapshots: []telemetry.Snapshot{prev, cur}}
	fakeSupercharger := &fakeSuperchargerReader{}
	fakeManual := &fakeManualReader{}

	rec := NewRecalculator(pool, fakeTelemetry, fakeSupercharger, fakeManual)

	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	end := start
	if err := rec.Recalculate(context.Background(), teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	// All three source reads are keyed on tesla_id alone now, so the vehicle is
	// the only identity there is left to check. Nothing scopes the written
	// rows by account any more -- tesla_id alone identifies the row.
	if fakeTelemetry.gotTeslaID != teslaID {
		t.Errorf("telemetry scoping: want teslaID %d, got %d", teslaID, fakeTelemetry.gotTeslaID)
	}
	if fakeSupercharger.gotTeslaID != teslaID {
		t.Errorf("supercharger scoping: want teslaID %d, got %d", teslaID, fakeSupercharger.gotTeslaID)
	}
	if fakeManual.gotTeslaID != teslaID {
		t.Errorf("manual scoping: want teslaID %d, got %d", teslaID, fakeManual.gotTeslaID)
	}
}

// TestRecalculate_TelemetryError_Propagates asserts task 6.4(c) for the
// telemetry source: an error from SnapshotsByVehicleBetween propagates from
// Recalculate rather than being swallowed, and short-circuits before the
// supercharger/manual ports are ever called.
func TestRecalculate_TelemetryError_Propagates(t *testing.T) {
	pool := newTestPool(t)
	const teslaID = int64(910003)
	cleanupVehicleMetrics(t, pool, teslaID)

	sentinel := errors.New("boom: telemetry unavailable")
	fakeTelemetry := &fakeTelemetryReader{err: sentinel}
	fakeSupercharger := &fakeSuperchargerReader{}
	fakeManual := &fakeManualReader{}

	rec := NewRecalculator(pool, fakeTelemetry, fakeSupercharger, fakeManual)

	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	err := rec.Recalculate(context.Background(), teslaID, start, start)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Recalculate error: want wrapping %v, got %v", sentinel, err)
	}
	// A zero gotTeslaID is the "never called" sentinel: every test vehicle id
	// is non-zero.
	if fakeSupercharger.gotTeslaID != 0 {
		t.Errorf("supercharger port must not be called once telemetry fails, got teslaID=%d", fakeSupercharger.gotTeslaID)
	}
	if fakeManual.gotTeslaID != 0 {
		t.Errorf("manual port must not be called once telemetry fails, got teslaID=%d", fakeManual.gotTeslaID)
	}
}

// TestRecalculate_SuperchargerError_Propagates asserts task 6.4(c) for the
// supercharger source: an error from SuperchargerSessionsByVehicleBetween
// propagates, short-circuiting before the manual port is called.
func TestRecalculate_SuperchargerError_Propagates(t *testing.T) {
	pool := newTestPool(t)
	const teslaID = int64(910004)
	cleanupVehicleMetrics(t, pool, teslaID)

	sentinel := errors.New("boom: supercharger unavailable")
	fakeTelemetry := &fakeTelemetryReader{}
	fakeSupercharger := &fakeSuperchargerReader{err: sentinel}
	fakeManual := &fakeManualReader{}

	rec := NewRecalculator(pool, fakeTelemetry, fakeSupercharger, fakeManual)

	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	err := rec.Recalculate(context.Background(), teslaID, start, start)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Recalculate error: want wrapping %v, got %v", sentinel, err)
	}
	if fakeManual.gotTeslaID != 0 {
		t.Errorf("manual port must not be called once supercharger fails, got teslaID=%d", fakeManual.gotTeslaID)
	}
}

// TestRecalculate_ManualError_Propagates asserts task 6.4(c) for the manual
// charge-entries source: an error from ListEntriesByVehicleBetween
// propagates from Recalculate rather than being swallowed.
func TestRecalculate_ManualError_Propagates(t *testing.T) {
	pool := newTestPool(t)
	const teslaID = int64(910005)
	cleanupVehicleMetrics(t, pool, teslaID)

	sentinel := errors.New("boom: manual entries unavailable")
	fakeTelemetry := &fakeTelemetryReader{}
	fakeSupercharger := &fakeSuperchargerReader{}
	fakeManual := &fakeManualReader{err: sentinel}

	rec := NewRecalculator(pool, fakeTelemetry, fakeSupercharger, fakeManual)

	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	err := rec.Recalculate(context.Background(), teslaID, start, start)
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
// (D19: telemetry exposes no public writer for a single row). As of
// RM29-telemetry-drop-derived-columns (tier 4), vehicle_snapshots no longer
// carries the five _calc columns at all (telemetry's migration
// 20260822000001 dropped them, and telemetry.Snapshot lost the matching
// fields) — this helper seeds the surviving raw columns
// (odometer_km, battery_level_pct, battery_range_km, captured_at,
// captured_date). The five derived figures are no longer something a caller
// supplies: Recalculate's real read path derives them itself, from these raw
// columns, via consumption.go's deriveConsumption — which is the entire
// point of this tier (design.md D1/D10). updatedAt defaults to CapturedAt
// when the caller leaves Snapshot.UpdatedAt at its zero value — callers that
// need precise watermark control (the Reconcile tests) set it explicitly.
//
// RM38-analytics-add-vehicle-status-columns: this helper now also binds
// s.ChargingState/s.ChargeLimitSocPct/s.InsideTempC/s.OutsideTempC/
// s.Locked/s.SentryMode/s.CarVersion as REAL parameters instead of the
// hardcoded placeholder literals ('Complete', 0, false, ”) this function
// used before this tier — those literals silently discarded whatever a
// caller set on those fields, which made it impossible to seed the Fixture
// RM38-A/RM38-B status values this tier's tests need. Existing callers that
// never set these fields get the Go zero values (false, nil, "", 0.0, 0),
// which is a behavior-preserving change for every pre-existing fixture:
// no test in this file asserts vehicle_snapshots.charging_state/locked/etc.
// directly, only the vehicle_metrics values Recalculate derives from them.
//
// RM50-analytics-add-tire-pressure-columns: also binds the four
// s.TpmsPressure{FL,FR,RL,RR}PSI fields. The column type is REAL (float4),
// not DOUBLE PRECISION, so a bare *float64 cannot bind directly — pgFloat4FromFloat64Ptr
// (below) narrows it, mirroring telemetry's own production
// float64PtrToPgFloat4 helper (service.go), which this test file cannot
// import (unexported, and cross-module test-only imports still respect the
// boundary). nil stays nil (SQL NULL); existing callers that never set these
// fields keep getting NULL, a behavior-preserving change for every
// pre-existing fixture.
// seedSnapshot writes a snapshot with no account. vehicle_snapshots is keyed on
// tesla_id, so the account is not part of a row's identity any more.
func seedSnapshot(t *testing.T, pool *pgxpool.Pool, s telemetry.Snapshot) {
	t.Helper()
	updatedAt := s.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = s.CapturedAt
	}
	_, err := pool.Exec(context.Background(), `
		INSERT INTO telemetry.vehicle_snapshots (
			tesla_id, captured_at, captured_date, raw_data,
			battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
			odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode, car_version,
			tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
			updated_at
		) VALUES (
			$1, $2, $3, '{}'::jsonb,
			$4, $5, $6, $7,
			$8, $9, $10, $11, $12, $13,
			$14, $15, $16, $17,
			$18
		)`,
		s.TeslaID,
		pgtype.Timestamptz{Time: s.CapturedAt, Valid: true},
		dateFrom(s.CapturedDate),
		int32(s.BatteryLevelPct), s.BatteryRangeKm, s.ChargingState, int32(s.ChargeLimitSocPct),
		s.OdometerKm, s.InsideTempC, s.OutsideTempC, s.Locked, pgBoolFromPtr(s.SentryMode), s.CarVersion,
		pgFloat4FromFloat64Ptr(s.TpmsPressureFLPSI), pgFloat4FromFloat64Ptr(s.TpmsPressureFRPSI),
		pgFloat4FromFloat64Ptr(s.TpmsPressureRLPSI), pgFloat4FromFloat64Ptr(s.TpmsPressureRRPSI),
		pgtype.Timestamptz{Time: updatedAt, Valid: true},
	)
	if err != nil {
		t.Fatalf("seeding vehicle_snapshots: %v", err)
	}
}

// pgFloat4FromFloat64Ptr maps a *float64 to a nullable pgtype.Float4 (REAL) --
// nil becomes SQL NULL. telemetry.vehicle_snapshots' four tpms_pressure_*_psi
// columns are REAL, not DOUBLE PRECISION, so seedSnapshot needs this narrowing
// step; mapping.go's own pgFloat8FromPtr targets DOUBLE PRECISION columns
// (analytics.vehicle_metrics' own tpms_pressure_*_psi columns) and is the
// wrong type here.
func pgFloat4FromFloat64Ptr(v *float64) pgtype.Float4 {
	if v == nil {
		return pgtype.Float4{Valid: false}
	}
	return pgtype.Float4{Float32: float32(*v), Valid: true}
}

// sessionIDSeq hands out globally-unique session_id values across this
// file's tests (supercharger_sessions_session_id_unique). Sequential, not
// atomic: this file's tests never run with t.Parallel().
var sessionIDSeq = time.Now().UnixNano()

func nextSessionID() int64 {
	sessionIDSeq++
	return sessionIDSeq
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

// seedSuperchargerSession inserts one telemetry.SuperchargerHistory directly
// into supercharger_sessions (D19: telemetry exposes no public writer for
// this table at all — upsertSuperchargerHistory is unexported, reachable
// only from inside Collector.CollectAll). Returns the session_id actually
// used: s.SessionID when the caller set one, otherwise a fresh value from
// nextSessionID (the column is UNIQUE NOT NULL). updatedAt defaults to
// ChargeStopDateTime when the caller leaves Session.UpdatedAt at its zero
// value.
func seedSuperchargerSession(t *testing.T, pool *pgxpool.Pool, s telemetry.SuperchargerHistory) int64 {
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
		INSERT INTO telemetry.supercharger_history (
			session_id, vin, tesla_id, site_location_name, country_code,
			charge_start_date_time, charge_stop_date_time, billing_type, vehicle_make_type,
			start_battery_pct, end_battery_pct, raw_data, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'{}'::jsonb,$12)`,
		sessionID, s.VIN, s.TeslaID, s.SiteLocationName, s.CountryCode,
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

// ---------------------------------------------------------------------------
// RM31-analytics-read-sessions-from-charging (tier 3) seeding helpers —
// charge_sessions is now this module's real Supercharger-session source
// (design.md §3); seedSuperchargerSession above is
// KEPT, not removed — Test Contract T3 (below) still legitimately seeds
// telemetry.supercharger_sessions as the "stale telemetry copy" side of its
// comparison (tasks.md 2.2's own acceptance note). Only this module's OWN
// Supercharger-session fixtures move to charge_sessions.
// ---------------------------------------------------------------------------

// pgTextFromStringPtr maps a *string to a nullable pgtype.Text — the
// charge_sessions.currency shape (nullable TEXT). Test-local like
// pgInt8FromPtr/pgInt2FromIntPtr above: this column belongs to charging, not
// analytics, so the conversion does not belong in mapping.go.
func pgTextFromStringPtr(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: *v, Valid: true}
}

// pgBoolFromPtr (charge_sessions.is_paid's shape, nullable BOOLEAN) is no
// longer test-local: RM38-analytics-add-vehicle-status-columns added a
// production helper of the identical name and behavior to mapping.go for
// vehicle_metrics' own nullable BOOLEAN columns (design D3/task 3.3). Reused
// here rather than redeclared to avoid a duplicate symbol in this package.

// seedChargeSession inserts one charging.Session directly into
// charge_sessions (D19: no public writer can construct an arbitrary row with
// specific percentages in one call — SessionWriter.MirrorSessions has no
// percentage fields at all, design D6 of RM29-charging-add-session-storage,
// and SessionVerifier.VerifySession requires an existing row's id — this
// module's own AGENTS.md §Testing, design.md §3 "Test files"). Returns the
// session_id actually used: s.SessionID when the caller set one (nonzero),
// otherwise a fresh value from nextSessionID — session_id is globally unique
// on this table, and reusing the same counter keeps this file's one sequence
// simple. updatedAt defaults to
// ChargeStopDateTime and createdAt to updatedAt when the caller leaves
// Session.UpdatedAt/CreatedAt at their zero value, mirroring
// seedSuperchargerSession's identical convention. vin/siteLocationName
// default to placeholders when empty (both NOT NULL columns; neither is read
// by any of this module's three retyped functions, design.md §1a). When
// either StartBatteryPct or EndBatteryPct is set, battery_pct_source
// defaults to "user_verified" to satisfy charge_sessions_pct_source_required
// — callers needing a different provenance may still set
// s.BatteryPctSource explicitly.
func seedChargeSession(t *testing.T, pool *pgxpool.Pool, s charging.Session) int64 {
	t.Helper()
	sessionID := s.SessionID
	if sessionID == 0 {
		sessionID = nextSessionID()
	}
	updatedAt := s.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = s.ChargeStopDateTime
	}
	createdAt := s.CreatedAt
	if createdAt.IsZero() {
		createdAt = updatedAt
	}
	vin := s.VIN
	if vin == "" {
		vin = "5YJ3E1EA0NF000001"
	}
	siteLocationName := s.SiteLocationName
	if siteLocationName == "" {
		siteLocationName = "Test Supercharger Site"
	}
	batteryPctSource := s.BatteryPctSource
	if batteryPctSource == nil && (s.StartBatteryPct != nil || s.EndBatteryPct != nil) {
		src := "user_verified"
		batteryPctSource = &src
	}
	_, err := pool.Exec(context.Background(), `
		INSERT INTO charging.supercharger_sessions (
			vin, tesla_id, session_id,
			charge_start_date_time, charge_stop_date_time,
			site_location_name, energy_kwh, total_cost, currency, is_paid,
			start_battery_pct, end_battery_pct, battery_pct_source,
			created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		vin, s.TeslaID, sessionID,
		pgtype.Timestamptz{Time: s.ChargeStartDateTime, Valid: true},
		pgtype.Timestamptz{Time: s.ChargeStopDateTime, Valid: true},
		siteLocationName, pgFloat8FromPtr(s.EnergyKWh), pgFloat8FromPtr(s.TotalCost),
		pgTextFromStringPtr(s.Currency), pgBoolFromPtr(s.IsPaid),
		pgInt2FromIntPtr(s.StartBatteryPct), pgInt2FromIntPtr(s.EndBatteryPct),
		pgTextFromStringPtr(batteryPctSource),
		pgtype.Timestamptz{Time: createdAt, Valid: true},
		pgtype.Timestamptz{Time: updatedAt, Valid: true},
	)
	if err != nil {
		t.Fatalf("seeding charge_sessions: %v", err)
	}
	return sessionID
}

// reviseChargeSession simulates a Tesla billing-state revision, or a human
// SessionVerifier.VerifySession edit, on an already-seeded
// supercharger_sessions row. session_id is the table's unique key, so it
// identifies the row on its own. charge_start_date_time/
// charge_stop_date_time are left untouched (the session's own calendar day
// never moves), only end_battery_pct and updated_at change.
func reviseChargeSession(t *testing.T, pool *pgxpool.Pool, sessionID int64, endBatteryPct int, updatedAt time.Time) {
	t.Helper()
	tag, err := pool.Exec(context.Background(),
		`UPDATE charging.supercharger_sessions SET end_battery_pct = $1, updated_at = $2 WHERE session_id = $3`,
		int16(endBatteryPct), pgtype.Timestamptz{Time: updatedAt, Valid: true}, sessionID,
	)
	if err != nil {
		t.Fatalf("revising supercharger_sessions: %v", err)
	}
	// A fixture UPDATE that matches no row does not error. Without this check
	// the assertions below would pass or fail for an unrelated reason.
	if n := tag.RowsAffected(); n != 1 {
		t.Fatalf("revising supercharger_sessions: %d row(s) affected, want 1", n)
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
// by its (tesla_id, metric_date) — the table's own UNIQUE index. It selects
// every column so a test can assert on any of them without a second helper.
// Returns ok=false when no row exists (never a zero-value row masquerading
// as "found"). Widened to also select/scan
// distance_traveled_km_delta_calc/consumed_pct_delta_calc/
// km_per_pct_delta_calc — analyticsdb.VehicleMetric (models.go) already
// carries these fields once sqlc regenerates from the widened schema, so
// only this helper's SQL and Scan list needed the addition.
func fetchVehicleMetric(t *testing.T, pool *pgxpool.Pool, teslaID int64, metricDate time.Time) (analyticsdb.VehicleMetric, bool) {
	t.Helper()
	var m analyticsdb.VehicleMetric
	err := pool.QueryRow(context.Background(), `
		SELECT id, tesla_id, metric_date, battery_level_pct, odometer_km,
		       battery_range_km, distance_traveled_km_calc, battery_used_pct_calc,
		       km_per_pct_calc, efficiency_range_100_pct_km_calc, days_spanned_calc,
		       distance_traveled_km_delta_calc, consumed_pct_delta_calc, km_per_pct_delta_calc,
		       consumed_pct, flagged, missing_charging_type, created_at, updated_at,
		       locked, sentry_mode, car_version, inside_temp_c, outside_temp_c,
		       charging_state, charge_limit_soc_pct, captured_at,
		       tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi
		FROM analytics.vehicle_metrics
		WHERE tesla_id = $1 AND metric_date = $2`,
		teslaID, dateFrom(metricDate),
	).Scan(&m.ID, &m.TeslaID, &m.MetricDate, &m.BatteryLevelPct,
		&m.OdometerKm, &m.BatteryRangeKm, &m.DistanceTraveledKmCalc, &m.BatteryUsedPctCalc,
		&m.KmPerPctCalc, &m.EfficiencyRange100PctKmCalc, &m.DaysSpannedCalc,
		&m.DistanceTraveledKmDeltaCalc, &m.ConsumedPctDeltaCalc, &m.KmPerPctDeltaCalc,
		&m.ConsumedPct,
		&m.Flagged, &m.MissingChargingType, &m.CreatedAt, &m.UpdatedAt,
		&m.Locked, &m.SentryMode, &m.CarVersion, &m.InsideTempC, &m.OutsideTempC,
		&m.ChargingState, &m.ChargeLimitSocPct, &m.CapturedAt,
		&m.TpmsPressureFlPsi, &m.TpmsPressureFrPsi, &m.TpmsPressureRlPsi, &m.TpmsPressureRrPsi)
	if errors.Is(err, pgx.ErrNoRows) {
		return analyticsdb.VehicleMetric{}, false
	}
	if err != nil {
		t.Fatalf("querying vehicle_metrics: %v", err)
	}
	return m, true
}

// fetchWatermark SELECTs one (tesla_id, source) cursor's source_updated_at
// directly from vehicle_metric_watermarks (the table's own UNIQUE index).
// Returns ok=false when no watermark row
// exists yet for this source (design.md D7's "no row = epoch" case) — never
// a zero time.Time masquerading as a real cursor value.
func fetchWatermark(t *testing.T, pool *pgxpool.Pool, teslaID int64, source string) (time.Time, bool) {
	t.Helper()
	var ts pgtype.Timestamptz
	err := pool.QueryRow(context.Background(),
		`SELECT source_updated_at FROM analytics.vehicle_metric_watermarks WHERE tesla_id = $1 AND source = $2`,
		teslaID, source,
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
// don't export across files in the sense of needing an import, but the two
// test families assert different things: pure-function output there,
// full-column persisted-row content plus Reader read-back here) —
// design.md's contract is the single source both are checked against.
//
// As of RM29-telemetry-drop-derived-columns (tier 4), telemetry.Snapshot no
// longer carries the five _calc fields at all, so every fixture below
// supplies ONLY raw observations (OdometerKm, BatteryLevelPct, CapturedAt,
// CapturedDate, BatteryRangeKm) — the expected vehicle_metrics values below
// must now be PRODUCED by Recalculate's real derivation
// (consumption.go's deriveConsumption), never copied off the seed, which is
// the whole point of this tier (design.md D1/D10, roadmap D10).
// ---------------------------------------------------------------------------

// metricsFixtureA returns design.md's Test Contract Fixture A: a plain day,
// no charge events. Raw observations only — analytics computes the five
// derived figures itself (D1/D10).
func metricsFixtureA(teslaID int64) (prev, cur telemetry.Snapshot) {
	prev = telemetry.Snapshot{
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 10, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 8, 10),
		OdometerKm:      1000.0,
		BatteryLevelPct: 80,
		BatteryRangeKm:  300.0,
	}
	cur = telemetry.Snapshot{
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 11, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 8, 11),
		OdometerKm:      1050.0,
		BatteryLevelPct: 65,
		BatteryRangeKm:  280.0,
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
func metricsFixtureB(teslaID int64) (prev, cur telemetry.Snapshot) {
	prev = telemetry.Snapshot{
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 12, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 8, 12),
		OdometerKm:      2000.0,
		BatteryLevelPct: 40,
		BatteryRangeKm:  250.0,
	}
	cur = telemetry.Snapshot{
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 13, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 8, 13),
		OdometerKm:      1998.0,
		BatteryLevelPct: 85,
		BatteryRangeKm:  260.0,
	}
	return prev, cur
}

// metricsFixtureC returns design.md's Test Contract Fixture C: a vehicle's
// true first-ever snapshot, no predecessor row seeded at all.
func metricsFixtureC(teslaID int64) telemetry.Snapshot {
	return telemetry.Snapshot{
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 5, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 8, 5),
		OdometerKm:      500.0,
		BatteryLevelPct: 90,
		BatteryRangeKm:  320.0,
	}
}

// metricsFixtureRM38A returns design.md's Test Contract Fixture RM38-A: the
// same predecessor/current day pair as metricsFixtureA above, with the eight
// new status columns added on cur ONLY (design D3 -- the eight new columns
// copy from cur, never from prev, so prev is left exactly as
// metricsFixtureA already builds it).
func metricsFixtureRM38A(teslaID int64) (prev, cur telemetry.Snapshot) {
	prev, cur = metricsFixtureA(teslaID)
	cur.Locked = true
	cur.SentryMode = boolPtr(false)
	cur.CarVersion = "2026.28.4"
	cur.InsideTempC = 21.5
	cur.OutsideTempC = 18.0
	cur.ChargingState = "Disconnected"
	cur.ChargeLimitSocPct = 80
	cur.CapturedAt = time.Date(2026, 8, 11, 3, 31, 0, 0, time.UTC)
	return prev, cur
}

// metricsFixtureRM38B returns design.md's Test Contract Fixture RM38-B: the
// same predecessor-less snapshot as metricsFixtureC above, with the eight
// new status columns added -- proving design D3 populates them even without
// a predecessor. SentryMode stays nil (the vehicle genuinely did not report
// sentry this capture -- design D2/D8's "not reported" reading, distinct
// from Fixture RM38-C's "predates the migration" reading below).
func metricsFixtureRM38B(teslaID int64) telemetry.Snapshot {
	cur := metricsFixtureC(teslaID)
	cur.Locked = false
	cur.SentryMode = nil
	cur.CarVersion = "2026.28.4"
	cur.InsideTempC = 19.0
	cur.OutsideTempC = 14.0
	cur.ChargingState = "Charging"
	cur.ChargeLimitSocPct = 90
	cur.CapturedAt = time.Date(2026, 8, 5, 3, 30, 15, 0, time.UTC)
	return cur
}

// seedPreMigrationVehicleMetric inserts a vehicle_metrics row directly via
// SQL carrying only the columns that existed BEFORE this migration
// (battery_level_pct, odometer_km, battery_range_km, flagged) -- design.md's
// Test Contract Fixture RM38-C: "a vehicle_metrics row written before this
// migration exists". No writer in this codebase can produce this shape any
// more (Recalculate always populates the eight new columns from cur,
// design D3), so a direct INSERT is the only way to construct it -- mirrors
// this file's existing D19 precedent for seeding a shape no public writer
// can build. Every column this INSERT omits (the five _calc columns,
// consumed_pct, missing_charging_type, and all eight RM38 columns) stays
// SQL NULL, exactly matching a genuine pre-migration row.
func seedPreMigrationVehicleMetric(t *testing.T, pool *pgxpool.Pool, teslaID int64, metricDate time.Time, batteryLevelPct int, odometerKm, batteryRangeKm float64) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO analytics.vehicle_metrics (
			tesla_id, metric_date, battery_level_pct, odometer_km, battery_range_km, flagged
		) VALUES ($1, $2, $3, $4, $5, false)`,
		teslaID, dateFrom(metricDate), int32(batteryLevelPct), odometerKm, batteryRangeKm,
	)
	if err != nil {
		t.Fatalf("seeding pre-migration vehicle_metrics row: %v", err)
	}
}

// newRealRecalculator builds a Recalculator over the REAL telemetry/charging
// Readers (never fakes) against the shared test pool — task 6.1/6.2's whole
// point is to exercise the real cross-module read path Recalculate depends
// on, unlike task 6.4's fakeTelemetryReader/fakeSuperchargerReader/
// fakeManualReader (reader_test.go), which stay in use only for 6.4's own
// fetch-shape assertions above.
//
// RM31-analytics-read-sessions-from-charging (tier 3): the Supercharger port
// retypes from telemetry.SuperchargerReader (renamed
// telemetry.SuperchargerHistoryReader by RM39 tier 5) to
// charging.SuperchargerSessionAnalyticsReader (design.md §3) — this call site
// is exactly what the leader's Wave 2 dispatch flagged as failing to build
// (telemetry's own constructor no longer satisfies NewRecalculator's
// retyped parameter).
func newRealRecalculator(pool *pgxpool.Pool) Recalculator {
	return NewRecalculator(pool, telemetry.NewReader(pool), charging.NewSuperchargerSessionAnalyticsReader(pool), charging.NewReader(pool))
}

// newRealReader builds a Reader over a fresh *pgxpool-backed
// analyticsdb.Queries via NewReader's own pool parameter. Every Reader
// method reads only vehicle_metrics, so no other dependency is needed.
func newRealReader(pool *pgxpool.Pool) Reader {
	return NewReader(pool)
}

// ===========================================================================
// Task 6.1 — TestRecalculate_FixtureA/B/C: seed via direct SQL (D19), call
// Recalculate, assert the persisted vehicle_metrics row matches design.md's
// Test Contract exactly, all columns.
// ===========================================================================

func TestRecalculate_FixtureA(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	const teslaID = int64(920001)
	cleanupVehicleMetrics(t, pool, teslaID)

	prev, cur := metricsFixtureA(teslaID)
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)

	rec := newRealRecalculator(pool)
	start := day(2026, 8, 10)
	end := start
	if err := rec.Recalculate(ctx, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	row, ok := fetchVehicleMetric(t, pool, teslaID, start)
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
	wantEfficiencyRange := wantKmPerPct * 100
	if !row.EfficiencyRange100PctKmCalc.Valid || !approxEqual(row.EfficiencyRange100PctKmCalc.Float64, wantEfficiencyRange) {
		t.Errorf("EfficiencyRange100PctKmCalc: want %v, got %+v", wantEfficiencyRange, row.EfficiencyRange100PctKmCalc)
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
	const teslaID = int64(920002)
	cleanupVehicleMetrics(t, pool, teslaID)

	prev, cur := metricsFixtureB(teslaID)
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)
	// No Supercharger session, no manual entry seeded near this vehicle at
	// all -- design.md: "no matching charge event logged in either source".

	rec := newRealRecalculator(pool)
	start := day(2026, 8, 12)
	end := start
	if err := rec.Recalculate(ctx, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	row, ok := fetchVehicleMetric(t, pool, teslaID, start)
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
	if row.EfficiencyRange100PctKmCalc.Valid {
		t.Errorf("EfficiencyRange100PctKmCalc: want NULL (same guard), got %v", row.EfficiencyRange100PctKmCalc.Float64)
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
	if !row.MissingChargingType.Valid || row.MissingChargingType.String != string(MissingChargingTypeManual) {
		t.Errorf("MissingChargingType: want MANUAL, got %+v", row.MissingChargingType)
	}
}

func TestRecalculate_FixtureC(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	const teslaID = int64(920003)
	cleanupVehicleMetrics(t, pool, teslaID)

	cur := metricsFixtureC(teslaID)
	seedSnapshot(t, pool, cur)

	rec := newRealRecalculator(pool)
	start := day(2026, 8, 4)
	end := start
	if err := rec.Recalculate(ctx, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	row, ok := fetchVehicleMetric(t, pool, teslaID, start)
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
	if row.EfficiencyRange100PctKmCalc.Valid {
		t.Errorf("EfficiencyRange100PctKmCalc: want NULL, got %v", row.EfficiencyRange100PctKmCalc.Float64)
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
	const teslaID = int64(930001)
	cleanupVehicleMetrics(t, pool, teslaID)

	day0 := clock.CalendarDay(time.Now(), time.UTC).AddDate(0, 0, -20)
	day1 := day0.AddDate(0, 0, 1)

	prev := telemetry.Snapshot{
		TeslaID:    teslaID,
		CapturedAt: day0.Add(3*time.Hour + 30*time.Minute), CapturedDate: day0,
		OdometerKm: 500.0, BatteryLevelPct: 70, BatteryRangeKm: 260.0,
	}
	cur := telemetry.Snapshot{
		TeslaID:    teslaID,
		CapturedAt: day1.Add(3*time.Hour + 30*time.Minute), CapturedDate: day1,
		OdometerKm: 520.0, BatteryLevelPct: 62, BatteryRangeKm: 240.0,
	}
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)

	rec := newRealRecalculator(pool)
	if err := rec.Reconcile(ctx, teslaID); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// D7: no prior watermark is treated as the epoch -- the vehicle's entire
	// stored telemetry history backfills into vehicle_metrics in one pass.
	wantDay := effectiveDay(cur) // == day0
	row, ok := fetchVehicleMetric(t, pool, teslaID, wantDay)
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
	if _, ok := fetchWatermark(t, pool, teslaID, sourceVehicleSnapshots); !ok {
		t.Error("want a vehicle_snapshots watermark row after the first Reconcile, found none")
	}
	// A source with zero returned rows leaves NO watermark row at all, even
	// on the very first run (design.md D2/D3's idempotence contract) -- no
	// Supercharger session and no manual entry was ever seeded for this
	// vehicle, so both of those sources' cursors stay at "no row = epoch".
	// This source's label is supercharger_sessions again (sourceSuperchargerSessions),
	// as of RM39 tier 3b -- see recalculate.go. The underlying table this
	// cursor tracks is internal/charging's Supercharger session table, moved
	// there from internal/telemetry by RM31 tier 3 and renamed by RM39 tier 3
	// (charging.supercharger_sessions); the watermark label was reset to match
	// by RM39-analytics-fix-watermark-vocabulary (design.md §2/§7).
	if _, ok := fetchWatermark(t, pool, teslaID, sourceSuperchargerSessions); ok {
		t.Error("want no supercharger_sessions watermark row (no session data ever seeded for this vehicle)")
	}
	if _, ok := fetchWatermark(t, pool, teslaID, sourceManualChargeEntries); ok {
		t.Error("want no manual_charge_entries watermark row (no entry ever seeded for this vehicle)")
	}
}

func TestReconcile_Idempotent(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	const teslaID = int64(930002)
	cleanupVehicleMetrics(t, pool, teslaID)

	day0 := clock.CalendarDay(time.Now(), time.UTC).AddDate(0, 0, -22)
	day1 := day0.AddDate(0, 0, 1)

	prev := telemetry.Snapshot{
		TeslaID:    teslaID,
		CapturedAt: day0.Add(3*time.Hour + 30*time.Minute), CapturedDate: day0,
		OdometerKm: 1000.0, BatteryLevelPct: 80, BatteryRangeKm: 300.0,
	}
	cur := telemetry.Snapshot{
		TeslaID:    teslaID,
		CapturedAt: day1.Add(3*time.Hour + 30*time.Minute), CapturedDate: day1,
		OdometerKm: 1050.0, BatteryLevelPct: 65, BatteryRangeKm: 280.0,
	}
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)

	rec := newRealRecalculator(pool)
	if err := rec.Reconcile(ctx, teslaID); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}

	wantDay := effectiveDay(cur)
	before, ok := fetchVehicleMetric(t, pool, teslaID, wantDay)
	if !ok {
		t.Fatal("expected a vehicle_metrics row after the first Reconcile")
	}
	watermarkBefore, ok := fetchWatermark(t, pool, teslaID, sourceVehicleSnapshots)
	if !ok {
		t.Fatal("expected a vehicle_snapshots watermark row after the first Reconcile")
	}
	var rowCountBefore int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM analytics.vehicle_metrics WHERE tesla_id=$1`, teslaID).Scan(&rowCountBefore); err != nil {
		t.Fatalf("counting vehicle_metrics rows: %v", err)
	}

	// Second Reconcile: no source data changed at all since the first run.
	if err := rec.Reconcile(ctx, teslaID); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}

	after, ok := fetchVehicleMetric(t, pool, teslaID, wantDay)
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
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM analytics.vehicle_metrics WHERE tesla_id=$1`, teslaID).Scan(&rowCountAfter); err != nil {
		t.Fatalf("counting vehicle_metrics rows: %v", err)
	}
	if rowCountAfter != rowCountBefore {
		t.Errorf("net row count changed: want %d, got %d (design.md's idempotence contract)", rowCountBefore, rowCountAfter)
	}

	watermarkAfter, ok := fetchWatermark(t, pool, teslaID, sourceVehicleSnapshots)
	if !ok {
		t.Fatal("expected the vehicle_snapshots watermark row to still exist")
	}
	if watermarkAfter.Before(watermarkBefore) {
		t.Errorf("watermark regressed: before %s, after %s (design.md: never regressed)", watermarkBefore, watermarkAfter)
	}
}

// TestReconcile_RevisedOldSuperchargerSession retargeted by
// RM31-analytics-read-sessions-from-charging (tier 3): the Supercharger
// session it seeds/revises now lives in charging.charge_sessions, not
// telemetry.supercharger_sessions -- Reconcile reads through
// charging.SuperchargerSessionAnalyticsReader after this tier (design.md §3),
// so a fixture seeded into the OLD table would never be observed. The test's
// own name and business intent (spec.md "A revised Supercharger session
// weeks old is picked up") are unchanged; only the seeding/revision table and
// the watermark source label move.
func TestReconcile_RevisedOldSuperchargerSession(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	const teslaIDConst = int64(930003)
	teslaID := teslaIDConst // addressable copy for charging.Session.TeslaID (*int64)
	cleanupVehicleMetrics(t, pool, teslaID)

	refNow := time.Now().UTC()
	oldDay := clock.CalendarDay(refNow, time.UTC).AddDate(0, 0, -25) // "three weeks ago" and then some -- safely before yesterday

	prev := telemetry.Snapshot{
		TeslaID:    teslaID,
		CapturedAt: oldDay.Add(3*time.Hour + 30*time.Minute), CapturedDate: oldDay,
		OdometerKm: 1000.0, BatteryLevelPct: 50, BatteryRangeKm: 220.0,
	}
	curDay := oldDay.AddDate(0, 0, 1)
	cur := telemetry.Snapshot{
		TeslaID:    teslaID,
		CapturedAt: curDay.Add(3*time.Hour + 30*time.Minute), CapturedDate: curDay,
		OdometerKm: 1010.0, BatteryLevelPct: 45, BatteryRangeKm: 210.0,
	}
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)

	t0 := oldDay.Add(11 * time.Hour) // the session's original sync time -- itself weeks old
	session := charging.Session{
		TeslaID:             teslaID,
		ChargeStartDateTime: oldDay.Add(10 * time.Hour),
		ChargeStopDateTime:  oldDay.Add(11 * time.Hour),
		StartBatteryPct:     intPtr(30),
		EndBatteryPct:       intPtr(40),
		UpdatedAt:           t0,
	}
	sessionID := seedChargeSession(t, pool, session)

	rec := newRealRecalculator(pool)
	if err := rec.Reconcile(ctx, teslaID); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}

	wantDay := effectiveDay(cur) // == oldDay
	before, ok := fetchVehicleMetric(t, pool, teslaID, wantDay)
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
	// old is picked up". reviseChargeSession is the charge_sessions analogue
	// of the retired reviseSuperchargerSession helper -- a direct-SQL stand-in
	// for a real SessionVerifier.VerifySession edit (out of this module's
	// sandbox), keyed on the globally unique session_id.
	reviseChargeSession(t, pool, sessionID, 50, refNow)

	if err := rec.Reconcile(ctx, teslaID); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}

	after, ok := fetchVehicleMetric(t, pool, teslaID, wantDay)
	if !ok {
		t.Fatal("expected the vehicle_metrics row to still exist after the revision")
	}
	wantConsumedAfter := 5.0 + 20.0 // BatteryUsedPctCalc(5) + revised session delta (50-30)
	if !after.ConsumedPct.Valid || !approxEqual(after.ConsumedPct.Float64, wantConsumedAfter) {
		t.Errorf("ConsumedPct after revision: want %v (the day, well outside any trailing window measured from today, must still be recomputed), got %+v", wantConsumedAfter, after.ConsumedPct)
	}

	watermark, ok := fetchWatermark(t, pool, teslaID, sourceSuperchargerSessions)
	if !ok {
		t.Fatal("expected a supercharger_sessions watermark row")
	}
	if watermark.Before(t0) {
		t.Errorf("supercharger_sessions watermark did not advance past the original sync time: got %s", watermark)
	}
}

// ===========================================================================
// RM31-analytics-read-sessions-from-charging (tier 3), tasks 2.2/2.3 —
// design.md §5 Test Contract T1-T4. T1 (the migration itself) lives in its
// own file, db_watermark_migration_integration_test.go, because it drives
// goose directly rather than Recalculator/Reader. T2, T3, T4 below.
// ===========================================================================

// recordingSuperchargerReader is a fake charging.SuperchargerSessionAnalyticsReader
// local to this file. It exists to (a) prove WHICH port method Reconcile actually
// calls and (b) when teslaScoped is true, enforce the SAME "a session whose
// TeslaID is nil is never returned for any teslaID" filter the real SQL
// implementation guarantees (charging.go's SuperchargerSessionAnalyticsReader
// doc comment) -- rather than trusting consumed.go to filter it, which it
// structurally cannot (neither sumSuperchargerPctBetween nor
// inferMissingChargingType reads TeslaID at all).
type recordingSuperchargerReader struct {
	sessions    []charging.Session
	teslaScoped bool

	updatedSinceCalled bool
	betweenCalled      bool
	listCalled         bool
}

// filtered returns f.sessions as-is when teslaScoped is false, or -- when
// true -- only the sessions whose TeslaID equals teslaID. A stored session
// always names a vehicle now, so there is no null case left to model.
func (f *recordingSuperchargerReader) filtered(teslaID int64) []charging.Session {
	if !f.teslaScoped {
		return f.sessions
	}
	out := make([]charging.Session, 0, len(f.sessions))
	for _, s := range f.sessions {
		if s.TeslaID == teslaID {
			out = append(out, s)
		}
	}
	return out
}

func (f *recordingSuperchargerReader) ListSessionsByVehicleBetween(_ context.Context, teslaID int64, _, _ time.Time) ([]charging.Session, error) {
	f.betweenCalled = true
	return f.filtered(teslaID), nil
}

func (f *recordingSuperchargerReader) ListSessionsByVehicleUpdatedSince(_ context.Context, teslaID int64, _ time.Time) ([]charging.Session, error) {
	f.updatedSinceCalled = true
	return f.filtered(teslaID), nil
}

func (f *recordingSuperchargerReader) ListSessionsByVehicle(_ context.Context, teslaID int64, _ int) ([]charging.Session, error) {
	f.listCalled = true
	return f.filtered(teslaID), nil
}

// Compile-time assertion: *recordingSuperchargerReader must satisfy
// charging.SuperchargerSessionAnalyticsReader.
var _ charging.SuperchargerSessionAnalyticsReader = (*recordingSuperchargerReader)(nil)

// TestReconcile_T2_ReadsSessionsThroughChargingPort implements design.md §5
// Test Contract T2. It combines a REAL pool + REAL telemetry.Reader (so the
// written vehicle_metrics row is genuinely derived from real seeded snapshots
// and read back from the real table) with a FAKE supercharger port (so
// exactly which of its three methods Reconcile calls is directly observable
// -- design.md's own "call-log" framing). There is no "old" sibling method to
// prove uncalled any more: charging.SuperchargerSessionAnalyticsReader is the
// only type this dependency can even be, post-retype, so a fake satisfying it
// structurally cannot expose SuperchargerSessionsByVehicleUpdatedSince at
// all -- the direct, still-meaningful proof left is that
// ListSessionsByVehicleUpdatedSince (Reconcile's own cursor read) is in fact
// invoked.
func TestReconcile_T2_ReadsSessionsThroughChargingPort(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	const teslaID = int64(970001)
	cleanupVehicleMetrics(t, pool, teslaID)

	// prev/cur CapturedDate are one day past each fixture's own EFFECTIVE day
	// (day(2026,8,13)/day(2026,8,14) respectively) -- effectiveDay(s) =
	// clock.CalendarDay(s.CapturedDate, time.UTC) - 1 day (consumed.go), and design.md's own
	// "Then" bullet pins the resulting row's metric_date at day(2026,8,14).
	prev := telemetry.Snapshot{
		TeslaID:    teslaID,
		CapturedAt: day(2026, 8, 14).Add(3*time.Hour + 30*time.Minute), CapturedDate: day(2026, 8, 14),
		OdometerKm: 100.0, BatteryLevelPct: 80,
	}
	cur := telemetry.Snapshot{
		TeslaID:    teslaID,
		CapturedAt: day(2026, 8, 15).Add(3*time.Hour + 30*time.Minute), CapturedDate: day(2026, 8, 15),
		OdometerKm: 140.0, BatteryLevelPct: 75,
	}
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)

	energyKWh := 30.0
	session := charging.Session{
		SessionID:           900, // fake-only fixture, never written to a real table -- no collision risk
		TeslaID:             teslaID,
		ChargeStartDateTime: time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC),
		StartBatteryPct:     intPtr(20),
		EndBatteryPct:       intPtr(80),
		EnergyKWh:           &energyKWh,
		UpdatedAt:           time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC),
	}
	superchargerFake := &recordingSuperchargerReader{sessions: []charging.Session{session}}

	rec := NewRecalculator(pool, telemetry.NewReader(pool), superchargerFake, charging.NewReader(pool))
	if err := rec.Reconcile(ctx, teslaID); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if !superchargerFake.updatedSinceCalled {
		t.Error("want ListSessionsByVehicleUpdatedSince to be called by Reconcile's own cursor read, was not -- the retype did not actually change which port method is consulted")
	}

	row, ok := fetchVehicleMetric(t, pool, teslaID, day(2026, 8, 14))
	if !ok {
		t.Fatal("expected a vehicle_metrics row for 2026-08-14, found none")
	}
	if !row.BatteryUsedPctCalc.Valid || row.BatteryUsedPctCalc.Int32 != 5 {
		t.Errorf("BatteryUsedPctCalc: want 5 (80-75), got %+v", row.BatteryUsedPctCalc)
	}
	wantConsumed := 65.0 // 5 (BatteryUsedPctCalc) + 60 (session delta 80-20)
	if !row.ConsumedPct.Valid || !approxEqual(row.ConsumedPct.Float64, wantConsumed) {
		t.Errorf("ConsumedPct: want %v, got %+v", wantConsumed, row.ConsumedPct)
	}
	if row.Flagged != false {
		t.Errorf("Flagged: want false (65 is neither negative nor zero), got %v", row.Flagged)
	}

	watermark, ok := fetchWatermark(t, pool, teslaID, sourceSuperchargerSessions)
	if !ok {
		t.Fatal("expected a supercharger_sessions watermark row to be created (no prior row -- epoch)")
	}
	if !watermark.Equal(session.UpdatedAt) {
		t.Errorf("supercharger_sessions watermark: want advanced to the session's own updated_at %s, got %s", session.UpdatedAt, watermark)
	}
}

// TestReconcile_T4_OtherVehicleSessionExcludedByPort is a non-regression, not
// new filtering: the fake enforces the same tesla_id scoping the real SQL
// does, so another vehicle's session is never returned for this one, and
// neither sumSuperchargerPctBetween nor inferMissingChargingType (which never
// read TeslaID at all) ever sees it. A session can no longer be orphaned --
// tesla_id is NOT NULL -- so the other vehicle is the case that remains.
func TestReconcile_T4_OtherVehicleSessionExcludedByPort(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	const teslaID = int64(970002)
	cleanupVehicleMetrics(t, pool, teslaID)

	prev := telemetry.Snapshot{
		TeslaID:    teslaID,
		CapturedAt: day(2026, 8, 14).Add(3*time.Hour + 30*time.Minute), CapturedDate: day(2026, 8, 14),
		OdometerKm: 100.0, BatteryLevelPct: 80,
	}
	cur := telemetry.Snapshot{
		TeslaID:    teslaID,
		CapturedAt: day(2026, 8, 15).Add(3*time.Hour + 30*time.Minute), CapturedDate: day(2026, 8, 15),
		OdometerKm: 140.0, BatteryLevelPct: 75,
	}
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)

	// Another vehicle's session. Its ChargeStopDateTime otherwise falls inside
	// [prev.CapturedAt, cur.CapturedAt) for teslaID, so only the fake's
	// teslaScoped filtering (not consumed.go) keeps it out.
	otherVehicle := charging.Session{
		SessionID:           902,
		TeslaID:             teslaID + 1,
		ChargeStartDateTime: time.Date(2026, 8, 14, 8, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC),
		StartBatteryPct:     intPtr(20),
		EndBatteryPct:       intPtr(80),
		UpdatedAt:           time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC),
	}
	superchargerFake := &recordingSuperchargerReader{sessions: []charging.Session{otherVehicle}, teslaScoped: true}

	rec := NewRecalculator(pool, telemetry.NewReader(pool), superchargerFake, charging.NewReader(pool))
	if err := rec.Reconcile(ctx, teslaID); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	row, ok := fetchVehicleMetric(t, pool, teslaID, day(2026, 8, 14))
	if !ok {
		t.Fatal("expected a vehicle_metrics row for 2026-08-14, found none")
	}
	if !row.BatteryUsedPctCalc.Valid || row.BatteryUsedPctCalc.Int32 != 5 {
		t.Errorf("BatteryUsedPctCalc: want 5 (80-75), got %+v", row.BatteryUsedPctCalc)
	}
	wantConsumed := 5.0 // NO session delta -- another vehicle's session must never surface here
	if !row.ConsumedPct.Valid || !approxEqual(row.ConsumedPct.Float64, wantConsumed) {
		t.Errorf("ConsumedPct: want %v (the other vehicle's delta must be absent), got %+v", wantConsumed, row.ConsumedPct)
	}

	// Even a future port that failed to filter would not crash these two
	// functions, because neither reads TeslaID at all -- a documented
	// property, exercised directly for completeness.
	_ = sumSuperchargerPctBetween([]charging.Session{otherVehicle}, prev.CapturedAt, cur.CapturedAt)
	_ = inferMissingChargingType([]charging.Session{otherVehicle}, prev.CapturedAt, cur.CapturedAt)
}

// TestReconcile_T3_ChargingSourcedValueWinsOverStaleTelemetryCopy implements
// design.md §5 Test Contract T3 -- the direct proof that the retyped read
// PATH, not merely a type change, has the intended effect. The SAME
// Supercharger session exists simultaneously in BOTH
// telemetry.supercharger_sessions (a stale/unverified copy) and
// charging.charge_sessions (the human-verified copy) -- exactly as they
// would mid-migration, or in any test proving the read moved. Recalculate
// must derive from charge_sessions, never from telemetry's stale copy.
//
// telemetry.supercharger_sessions is seeded here deliberately (not a
// leftover): this module's own AGENTS.md/tasks.md 2.2 note that
// seedSuperchargerSession is KEPT for exactly this purpose.
func TestReconcile_T3_ChargingSourcedValueWinsOverStaleTelemetryCopy(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	const teslaID = int64(970003)
	cleanupVehicleMetrics(t, pool, teslaID)

	// prev/cur CapturedDate one day past each fixture's own effective day
	// (day(2026,8,20)/day(2026,8,21)) -- design.md's "Then" pins the row's
	// metric_date at day(2026,8,21).
	prev := telemetry.Snapshot{
		TeslaID:    teslaID,
		CapturedAt: day(2026, 8, 21).Add(3*time.Hour + 30*time.Minute), CapturedDate: day(2026, 8, 21),
		OdometerKm: 1000.0, BatteryLevelPct: 90,
	}
	cur := telemetry.Snapshot{
		TeslaID:    teslaID,
		CapturedAt: day(2026, 8, 22).Add(3*time.Hour + 30*time.Minute), CapturedDate: day(2026, 8, 22),
		OdometerKm: 1000.0, BatteryLevelPct: 85,
	}
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)

	// A single shared session id correlates "the same session, two
	// representations" -- generated via nextSessionID rather than design.md's
	// illustrative literal 901, because supercharger_sessions.session_id is
	// GLOBALLY unique (unlike charge_sessions' account-scoped key) and this
	// value is never itself asserted by T3's "Then" clauses, only the
	// resulting consumed_pct is.
	sessionID := nextSessionID()
	startAt := time.Date(2026, 8, 21, 8, 0, 0, 0, time.UTC)
	stopAt := time.Date(2026, 8, 21, 8, 30, 0, 0, time.UTC)

	// The STALE telemetry copy -- never read by Recalculate any more after
	// this tier's retype; seeded only to prove it is NOT what consumed_pct
	// comes from.
	seedSuperchargerSession(t, pool, telemetry.SuperchargerHistory{
		SessionID:           sessionID,
		TeslaID:             teslaID,
		ChargeStartDateTime: startAt,
		ChargeStopDateTime:  stopAt,
		StartBatteryPct:     intPtr(30),
		EndBatteryPct:       intPtr(70),
	})

	// The human-verified charging copy -- what Recalculate must read from.
	seedChargeSession(t, pool, charging.Session{
		SessionID:           sessionID,
		TeslaID:             teslaID,
		ChargeStartDateTime: startAt,
		ChargeStopDateTime:  stopAt,
		StartBatteryPct:     intPtr(20),
		EndBatteryPct:       intPtr(90),
	})

	rec := newRealRecalculator(pool)
	if err := rec.Reconcile(ctx, teslaID); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	row, ok := fetchVehicleMetric(t, pool, teslaID, day(2026, 8, 21))
	if !ok {
		t.Fatal("expected a vehicle_metrics row for 2026-08-21, found none")
	}
	if !row.ConsumedPct.Valid {
		t.Fatal("ConsumedPct: want a value, got NULL")
	}
	wantConsumed := 75.0 // 5 (BatteryUsedPctCalc: 90-85) + 70 (charging-sourced delta: 90-20)
	if !approxEqual(row.ConsumedPct.Float64, wantConsumed) {
		t.Errorf("ConsumedPct: want %v (the charging-sourced delta), got %v", wantConsumed, row.ConsumedPct.Float64)
	}
	// The negative assertion IS the point of this test (design.md T3): a
	// retype that compiled but left a stray call site pointed at the old
	// telemetry-backed port would still produce this value, undetected by the
	// positive assertion alone.
	wantStaleConsumed := 45.0 // 5 + 40 (telemetry's stale delta: 70-30)
	if approxEqual(row.ConsumedPct.Float64, wantStaleConsumed) {
		t.Errorf("ConsumedPct: got the STALE telemetry-sourced value %v -- Recalculate is still reading telemetry.supercharger_sessions instead of charging.charge_sessions", row.ConsumedPct.Float64)
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
	const teslaID = int64(940001)
	cleanupVehicleMetrics(t, pool, teslaID)

	prev, cur := metricsFixtureA(teslaID)
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)

	rec := newRealRecalculator(pool)
	start := day(2026, 8, 10)
	end := start
	if err := rec.Recalculate(ctx, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	rdr := newRealReader(pool)
	got, err := rdr.ConsumedByDay(ctx, teslaID, start, end)
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
	const teslaID = int64(940002)
	cleanupVehicleMetrics(t, pool, teslaID)

	prev, cur := metricsFixtureB(teslaID)
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)

	rec := newRealRecalculator(pool)
	start := day(2026, 8, 12)
	end := start
	if err := rec.Recalculate(ctx, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	rdr := newRealReader(pool)

	gotOdometer, err := rdr.OdometerDeltaByDay(ctx, teslaID, start, end)
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
	gotConsumed, err := rdr.ConsumedByDay(ctx, teslaID, start, end)
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
	if gotConsumed[0].MissingChargingType != MissingChargingTypeManual {
		t.Errorf("MissingChargingType: want MANUAL, got %v", gotConsumed[0].MissingChargingType)
	}
}

func TestReader_BothMethods_ExcludeFixtureCRow(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	const teslaID = int64(940003)
	cleanupVehicleMetrics(t, pool, teslaID)

	cur := metricsFixtureC(teslaID)
	seedSnapshot(t, pool, cur)

	rec := newRealRecalculator(pool)
	start := day(2026, 8, 4)
	end := start
	if err := rec.Recalculate(ctx, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	// Direct-read confirmation: the row genuinely exists in the real database
	// (design.md: "the exact property a sparse table would not have had").
	if _, ok := fetchVehicleMetric(t, pool, teslaID, start); !ok {
		t.Fatal("expected the dense vehicle_metrics row for Fixture C's predecessor-less day to exist, found none")
	}

	rdr := newRealReader(pool)

	gotConsumed, err := rdr.ConsumedByDay(ctx, teslaID, start, end)
	if err != nil {
		t.Fatalf("ConsumedByDay: %v", err)
	}
	if len(gotConsumed) != 0 {
		t.Errorf("ConsumedByDay: want empty (D13's IS NOT NULL filter excludes the predecessor-less row), got %+v", gotConsumed)
	}

	gotOdometer, err := rdr.OdometerDeltaByDay(ctx, teslaID, start, end)
	if err != nil {
		t.Fatalf("OdometerDeltaByDay: %v", err)
	}
	if len(gotOdometer) != 0 {
		t.Errorf("OdometerDeltaByDay: want empty (D13's IS NOT NULL filter excludes the predecessor-less row), got %+v", gotOdometer)
	}
}

// ===========================================================================
// Task 6b.2 (RM29-telemetry-drop-derived-columns, tier 4) --
// TestRecalculate_FixtureD_MultiDayGap / _FixtureD2_ChargeInsideTheGap: the
// DB-backed proof of design.md D2/D7 (telemetry.Reader.SnapshotPrecedingDay)
// and D8b (the widened charge-source fetch), exercised through the REAL
// telemetry/charging Readers, not fakes -- consumed_test.go's
// TestDeriveVehicleMetrics_FixtureD*/consumption_test.go pin the same
// figures through the pure functions; these tests pin that Recalculate's own
// I/O (the real SnapshotsByVehicleBetween/SnapshotPrecedingDay/
// SuperchargerSessionsByVehicleBetween/ListEntriesByVehicleBetween calls)
// wires them together correctly end to end.
// ===========================================================================

// metricsFixtureD returns design.md's Test Contract Fixture D: a seven-day
// capture gap. Raw observations only (D1/D10) -- the true predecessor
// (2026-08-01) sits far outside Recalculate's normal one-day lookback and is
// reachable only via telemetry.Reader.SnapshotPrecedingDay (design.md D2/D7).
func metricsFixtureD(teslaID int64) (prev, cur telemetry.Snapshot) {
	prev = telemetry.Snapshot{
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 1, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 8, 1),
		OdometerKm:      1000.0,
		BatteryLevelPct: 90,
		BatteryRangeKm:  350.0,
	}
	cur = telemetry.Snapshot{
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 8, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 8, 8),
		OdometerKm:      1210.0,
		BatteryLevelPct: 55,
		BatteryRangeKm:  220.0,
	}
	return prev, cur
}

// seedManualEntry creates one charging.Entry via the module's own public
// writer, charging.NewWriter(pool).Create -- manual_charge_entries DOES have
// a clean writer (unlike vehicle_snapshots/supercharger_sessions, D19), so
// this is the right tool rather than direct SQL. startPct/endPct set the
// entry's battery delta (BatteryDelta() = end - start), the only field
// Fixture D2 cares about; every other required field is a plausible
// placeholder.
func seedManualEntry(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64, chargedOn time.Time, startPct, endPct int) {
	t.Helper()
	lk := "HOME"
	_, err := charging.NewWriter(pool).Create(context.Background(), charging.Entry{
		CreatedByAccountID: accountID,
		TeslaID:            teslaID,
		VIN:                "5YJ3E1EA0NF000001",
		ChargedOn:          chargedOn,
		EnergyAddedKWh:     fp(10.0),
		Price:              1000.0,
		Currency:           "COP",
		LocationKind:       &lk,
		StartBatteryPct:    intPtr(startPct),
		EndBatteryPct:      intPtr(endPct),
	})
	if err != nil {
		t.Fatalf("seeding manual_charge_entries via charging.Writer: %v", err)
	}
}

// TestRecalculate_FixtureD_MultiDayGap covers design.md's Test Contract
// Fixture D end to end against the real telemetry Reader: seeding ONLY the
// 2026-08-01 and 2026-08-08 snapshots (nothing in between), Recalculate's
// own SnapshotsByVehicleBetween fetch returns just the current row -- the
// predecessor arrives solely through the real SnapshotPrecedingDay call.
func TestRecalculate_FixtureD_MultiDayGap(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	const teslaID = int64(950001)
	cleanupVehicleMetrics(t, pool, teslaID)

	prev, cur := metricsFixtureD(teslaID)
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)
	// No Supercharger sessions, no manual entries anywhere in the span.

	rec := newRealRecalculator(pool)
	start := day(2026, 8, 7)
	end := start
	if err := rec.Recalculate(ctx, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	row, ok := fetchVehicleMetric(t, pool, teslaID, start)
	if !ok {
		t.Fatal("expected a vehicle_metrics row for Fixture D's gap day, found none -- the day SnapshotPrecedingDay exists to recover")
	}
	if row.BatteryLevelPct != 55 {
		t.Errorf("BatteryLevelPct: want 55, got %d", row.BatteryLevelPct)
	}
	if row.OdometerKm != 1210.0 {
		t.Errorf("OdometerKm: want 1210.0, got %v", row.OdometerKm)
	}
	if row.BatteryRangeKm != 220.0 {
		t.Errorf("BatteryRangeKm: want 220.0, got %v", row.BatteryRangeKm)
	}
	if !row.DistanceTraveledKmCalc.Valid || !approxEqual(row.DistanceTraveledKmCalc.Float64, 210.0) {
		t.Errorf("DistanceTraveledKmCalc: want 210.0 (the true total across the gap, never averaged), got %+v", row.DistanceTraveledKmCalc)
	}
	if !row.BatteryUsedPctCalc.Valid || row.BatteryUsedPctCalc.Int32 != 35 {
		t.Errorf("BatteryUsedPctCalc: want 35, got %+v", row.BatteryUsedPctCalc)
	}
	if !row.DaysSpannedCalc.Valid || row.DaysSpannedCalc.Int32 != 7 {
		t.Errorf("DaysSpannedCalc: want 7 -- NOT 1 -- got %+v", row.DaysSpannedCalc)
	}
	if !row.KmPerPctCalc.Valid || !approxEqual(row.KmPerPctCalc.Float64, 6.0) {
		t.Errorf("KmPerPctCalc: want 6.0, got %+v", row.KmPerPctCalc)
	}
	if !row.EfficiencyRange100PctKmCalc.Valid || !approxEqual(row.EfficiencyRange100PctKmCalc.Float64, 600.0) {
		t.Errorf("EfficiencyRange100PctKmCalc: want 600.0, got %+v", row.EfficiencyRange100PctKmCalc)
	}
	if !row.ConsumedPct.Valid || !approxEqual(row.ConsumedPct.Float64, 35.0) {
		t.Errorf("ConsumedPct: want 35.0 (no charge events in the gap), got %+v", row.ConsumedPct)
	}
	if row.Flagged != false {
		t.Errorf("Flagged: want false, got %v", row.Flagged)
	}
	if row.MissingChargingType.Valid {
		t.Errorf("MissingChargingType: want NULL, got %v", row.MissingChargingType.String)
	}

	// The non-empty assertion below IS the point (design.md): an
	// implementation that omits the SnapshotPrecedingDay call falls into the
	// prev == nil branch and both Reader methods return an EMPTY slice -- a
	// silently dropped day that compiles and does not error.
	rdr := newRealReader(pool)

	gotConsumed, err := rdr.ConsumedByDay(ctx, teslaID, start, end)
	if err != nil {
		t.Fatalf("ConsumedByDay: %v", err)
	}
	if len(gotConsumed) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(gotConsumed), gotConsumed)
	}
	c := gotConsumed[0]
	if !c.Date.Equal(start) {
		t.Errorf("ConsumedByDay Date: want %v, got %v", start, c.Date)
	}
	if !approxEqual(c.ConsumedPct, 35.0) {
		t.Errorf("ConsumedByDay ConsumedPct: want 35.0, got %v", c.ConsumedPct)
	}
	if c.DistanceKm != 210.0 {
		t.Errorf("ConsumedByDay DistanceKm: want 210.0, got %v", c.DistanceKm)
	}
	if c.Flagged {
		t.Error("ConsumedByDay: want Flagged=false")
	}
	if c.MissingChargingType != "" {
		t.Errorf("ConsumedByDay MissingChargingType: want \"\", got %v", c.MissingChargingType)
	}
	if c.DaysSpanned != 7 {
		t.Errorf("ConsumedByDay DaysSpanned: want 7, got %d", c.DaysSpanned)
	}

	gotOdometer, err := rdr.OdometerDeltaByDay(ctx, teslaID, start, end)
	if err != nil {
		t.Fatalf("OdometerDeltaByDay: %v", err)
	}
	if len(gotOdometer) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(gotOdometer), gotOdometer)
	}
	if gotOdometer[0].KmDriven != 210.0 {
		t.Errorf("OdometerDeltaByDay KmDriven: want 210.0, got %v", gotOdometer[0].KmDriven)
	}
	if gotOdometer[0].OdometerKm != 1210.0 {
		t.Errorf("OdometerDeltaByDay OdometerKm: want 1210.0, got %v", gotOdometer[0].OdometerKm)
	}
}

// TestRecalculate_FixtureD2_ChargeInsideTheGap covers design.md's Test
// Contract Fixture D2 -- D8b's proof, end to end against the real
// telemetry/charging Readers. Same gap as Fixture D, plus one manual charge
// entry dated 2026-08-04, four days before the un-widened fetch's own start
// (2026-08-06) -- it is fetched only because Recalculate widens both
// charge-source fetches back to effectiveDay(preceding) when preceding
// exists and precedes the normal lookback (design.md D8b).
func TestRecalculate_FixtureD2_ChargeInsideTheGap(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	const teslaID = int64(950002)
	cleanupVehicleMetrics(t, pool, teslaID)

	prev, cur := metricsFixtureD(teslaID)
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)
	seedManualEntry(t, pool, accountID, teslaID, day(2026, 8, 4), 30, 50) // +20

	rec := newRealRecalculator(pool)
	start := day(2026, 8, 7)
	end := start
	if err := rec.Recalculate(ctx, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	row, ok := fetchVehicleMetric(t, pool, teslaID, start)
	if !ok {
		t.Fatal("expected a vehicle_metrics row for Fixture D2, found none")
	}
	if !row.BatteryUsedPctCalc.Valid || row.BatteryUsedPctCalc.Int32 != 35 {
		t.Errorf("BatteryUsedPctCalc: want 35 (the raw delta is unaffected by charging), got %+v", row.BatteryUsedPctCalc)
	}
	if !row.DistanceTraveledKmCalc.Valid || !approxEqual(row.DistanceTraveledKmCalc.Float64, 210.0) {
		t.Errorf("DistanceTraveledKmCalc: want 210.0, got %+v", row.DistanceTraveledKmCalc)
	}
	if !row.DaysSpannedCalc.Valid || row.DaysSpannedCalc.Int32 != 7 {
		t.Errorf("DaysSpannedCalc: want 7, got %+v", row.DaysSpannedCalc)
	}
	wantConsumed := 55.0 // 35 (raw delta) + 20 (the matched manual charge)
	if !row.ConsumedPct.Valid || !approxEqual(row.ConsumedPct.Float64, wantConsumed) {
		t.Errorf("ConsumedPct: want %v -- an implementation that widened the predecessor lookup (D2/D7) but not the charge-source fetches (D8b) produces 35.0 here, got %+v", wantConsumed, row.ConsumedPct)
	}
	// MAG-81, end to end: the efficiency divides by the CORRECTED 55, not by
	// the raw 35. This fixture is Fixture D plus one manual charge, so the
	// contrast is exact -- TestRecalculate_FixtureD_MultiDayGap, the same
	// snapshots with no charge, stores 210/35 = 6.0 here. A regression that
	// restored the raw divisor would store 6.0 in this test too, which is why
	// the two fixtures must keep asserting different numbers.
	wantKmPerPct := 210.0 / wantConsumed // ~3.818
	if !row.KmPerPctCalc.Valid || !approxEqual(row.KmPerPctCalc.Float64, wantKmPerPct) {
		t.Errorf("KmPerPctCalc: want %v (210.0/55, the charge-corrected divisor) -- 6.0 means the raw battery delta was used, got %+v", wantKmPerPct, row.KmPerPctCalc)
	}
	if !row.EfficiencyRange100PctKmCalc.Valid || !approxEqual(row.EfficiencyRange100PctKmCalc.Float64, wantKmPerPct*100) {
		t.Errorf("EfficiencyRange100PctKmCalc: want %v (KmPerPctCalc*100), got %+v", wantKmPerPct*100, row.EfficiencyRange100PctKmCalc)
	}
	if row.Flagged != false {
		t.Errorf("Flagged: want false, got %v", row.Flagged)
	}
}

// ===========================================================================
// Task 6b.3 (RM29-telemetry-drop-derived-columns, tier 4) --
// TestRecalculate_ZeroDivisorGuard / _AfterSameDayRecapture_RefreshesSuccessorRow.
// ===========================================================================

// metricsFixtureE returns design.md's Test Contract Fixture E: the
// consumed_pct == 0 divisor guard (a parked day) -- distinct from Fixture B's
// negative-divisor case: the guard is `consumed > 0` (MAG-81; `batteryUsed > 0`
// before it), so zero is excluded exactly like a negative. No charge is seeded,
// so the corrected figure equals the raw one here. Raw observations only
// (D1/D10).
func metricsFixtureE(teslaID int64) (prev, cur telemetry.Snapshot) {
	prev = telemetry.Snapshot{
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 15, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 8, 15),
		OdometerKm:      3000.0,
		BatteryLevelPct: 70,
		BatteryRangeKm:  280.0,
	}
	cur = telemetry.Snapshot{
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 16, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 8, 16),
		OdometerKm:      3000.0,
		BatteryLevelPct: 70,
		BatteryRangeKm:  280.0,
	}
	return prev, cur
}

// TestRecalculate_ZeroDivisorGuard covers design.md's Test Contract Fixture
// E end to end: both efficiency columns must be NULL while
// distance_traveled_km_calc (0.0) and battery_used_pct_calc (0) are stored
// NON-NULL -- a stored zero is a truthful reading (a parked day), never
// treated as an absence. ConsumedByDay must still return the day: a 0 is not
// an absence, distinguishing a correct implementation from one that
// conflated "zero" with "absent" (the IS NOT NULL filter checks NULL-ness,
// not falsy-ness).
func TestRecalculate_ZeroDivisorGuard(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	const teslaID = int64(960001)
	cleanupVehicleMetrics(t, pool, teslaID)

	prev, cur := metricsFixtureE(teslaID)
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)

	rec := newRealRecalculator(pool)
	start := day(2026, 8, 15)
	end := start
	if err := rec.Recalculate(ctx, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	row, ok := fetchVehicleMetric(t, pool, teslaID, start)
	if !ok {
		t.Fatal("expected a vehicle_metrics row for Fixture E, found none")
	}
	if !row.DistanceTraveledKmCalc.Valid || !approxEqual(row.DistanceTraveledKmCalc.Float64, 0.0) {
		t.Errorf("DistanceTraveledKmCalc: want 0.0 (stored, non-NULL -- a truthful zero), got %+v", row.DistanceTraveledKmCalc)
	}
	if !row.BatteryUsedPctCalc.Valid || row.BatteryUsedPctCalc.Int32 != 0 {
		t.Errorf("BatteryUsedPctCalc: want 0 (stored, non-NULL), got %+v", row.BatteryUsedPctCalc)
	}
	if row.KmPerPctCalc.Valid {
		t.Errorf("KmPerPctCalc: want NULL -- the guard is consumedPct > 0 (MAG-81; batteryUsed > 0 before it), and with no charge to correct it the zero is excluded exactly like a negative, got %v", row.KmPerPctCalc.Float64)
	}
	if row.EfficiencyRange100PctKmCalc.Valid {
		t.Errorf("EfficiencyRange100PctKmCalc: want NULL (same guard), got %v", row.EfficiencyRange100PctKmCalc.Float64)
	}
	if !row.DaysSpannedCalc.Valid || row.DaysSpannedCalc.Int32 != 1 {
		t.Errorf("DaysSpannedCalc: want 1, got %+v", row.DaysSpannedCalc)
	}
	if !row.ConsumedPct.Valid || !approxEqual(row.ConsumedPct.Float64, 0.0) {
		t.Errorf("ConsumedPct: want 0.0, got %+v", row.ConsumedPct)
	}
	if row.Flagged != false {
		t.Errorf("Flagged: want false (consumed==0 but distanceKm 0.0 is not > minFlagDistanceKm 10.0), got %v", row.Flagged)
	}
	if row.MissingChargingType.Valid {
		t.Errorf("MissingChargingType: want NULL, got %v", row.MissingChargingType.String)
	}

	rdr := newRealReader(pool)
	gotConsumed, err := rdr.ConsumedByDay(ctx, teslaID, start, end)
	if err != nil {
		t.Fatalf("ConsumedByDay: %v", err)
	}
	if len(gotConsumed) != 1 {
		t.Fatalf("want exactly 1 entry (a 0 is not an absence -- battery_used_pct_calc is 0, not NULL), got %d: %+v", len(gotConsumed), gotConsumed)
	}
	c := gotConsumed[0]
	if !approxEqual(c.ConsumedPct, 0.0) {
		t.Errorf("ConsumedPct: want 0.0, got %v", c.ConsumedPct)
	}
	if c.DistanceKm != 0.0 {
		t.Errorf("DistanceKm: want 0.0, got %v", c.DistanceKm)
	}
	if c.Flagged {
		t.Error("want Flagged=false")
	}
	if c.DaysSpanned != 1 {
		t.Errorf("DaysSpanned: want 1, got %d", c.DaysSpanned)
	}
}

// TestRecalculate_AfterSameDayRecapture_RefreshesSuccessorRow is the
// derived-columns half of telemetry's old
// TestStore_SnapshotUpsert_RecapturesRecomputeDerivedColumns (design.md's
// "Characterization parity contract" table), re-homed here. Before this
// tier, a successor row's five _calc figures were computed ONCE at
// telemetry's write time against whatever its predecessor said then, and
// were never recomputed when that predecessor was later REPLACED by a
// same-day re-capture (vehicle_snapshots_account_tesla_date_unique's dedupe
// UPSERT) -- the stale-successor bug design.md D10 names, unobservable
// before this change because there was no derivation left to re-run.
// Recalculate now derives fresh from whatever the two rows say at recompute
// time, so re-running it must change day N+1's stored figures to match the
// REPLACEMENT, not the value first computed.
func TestRecalculate_AfterSameDayRecapture_RefreshesSuccessorRow(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	const teslaID = int64(960002)
	cleanupVehicleMetrics(t, pool, teslaID)

	snapA := telemetry.Snapshot{ // day N-1
		TeslaID:    teslaID,
		CapturedAt: time.Date(2026, 8, 20, 3, 30, 0, 0, time.UTC), CapturedDate: day(2026, 8, 20),
		OdometerKm: 1000.0, BatteryLevelPct: 80, BatteryRangeKm: 300.0,
	}
	snapB := telemetry.Snapshot{ // day N -- the row that gets recaptured
		TeslaID:    teslaID,
		CapturedAt: time.Date(2026, 8, 21, 3, 30, 0, 0, time.UTC), CapturedDate: day(2026, 8, 21),
		OdometerKm: 1050.0, BatteryLevelPct: 70, BatteryRangeKm: 280.0,
	}
	snapC := telemetry.Snapshot{ // day N+1 -- the successor whose row must refresh
		TeslaID:    teslaID,
		CapturedAt: time.Date(2026, 8, 22, 3, 30, 0, 0, time.UTC), CapturedDate: day(2026, 8, 22),
		OdometerKm: 1150.0, BatteryLevelPct: 55, BatteryRangeKm: 250.0,
	}
	seedSnapshot(t, pool, snapA)
	seedSnapshot(t, pool, snapB)
	seedSnapshot(t, pool, snapC)

	rec := newRealRecalculator(pool)
	start := day(2026, 8, 20) // day N
	end := day(2026, 8, 21)   // day N+1
	if err := rec.Recalculate(ctx, teslaID, start, end); err != nil {
		t.Fatalf("first Recalculate: %v", err)
	}

	dayNPlus1 := day(2026, 8, 21)
	before, ok := fetchVehicleMetric(t, pool, teslaID, dayNPlus1)
	if !ok {
		t.Fatal("expected a vehicle_metrics row for day N+1 after the first Recalculate")
	}
	if !before.DistanceTraveledKmCalc.Valid || !approxEqual(before.DistanceTraveledKmCalc.Float64, 100.0) {
		t.Fatalf("before: DistanceTraveledKmCalc want 100.0 (1150-1050, against day N's ORIGINAL reading), got %+v", before.DistanceTraveledKmCalc)
	}
	if !before.BatteryUsedPctCalc.Valid || before.BatteryUsedPctCalc.Int32 != 15 {
		t.Fatalf("before: BatteryUsedPctCalc want 15 (70-55), got %+v", before.BatteryUsedPctCalc)
	}

	// Simulate a same-day re-capture REPLACING day N's row with different
	// readings -- exactly what the dedupe UPSERT does to vehicle_snapshots
	// (design D1 of telemetry-dedupe-daily-snapshots: latest capture wins).
	// A direct UPDATE is the right substitute here: telemetry exposes no
	// public writer for a single row (D19), and the point under test is
	// Recalculate's read-time behavior, not the UPSERT mechanics themselves.
	tag, err := pool.Exec(ctx,
		`UPDATE telemetry.vehicle_snapshots SET odometer_km = $1, battery_level_pct = $2, battery_range_km = $3
		 WHERE tesla_id = $4 AND captured_date = $5`,
		1080.0, int32(60), 260.0, teslaID, dateFrom(day(2026, 8, 21)),
	)
	if err != nil {
		t.Fatalf("simulating same-day recapture: %v", err)
	}
	// Check the row count. An UPDATE that matches nothing does not error, so a
	// wrong WHERE here would silently skip the recapture and make the
	// assertions below pass for the wrong reason.
	if tag.RowsAffected() != 1 {
		t.Fatalf("simulating same-day recapture: want 1 row updated, got %d", tag.RowsAffected())
	}

	if err := rec.Recalculate(ctx, teslaID, start, end); err != nil {
		t.Fatalf("second Recalculate: %v", err)
	}

	after, ok := fetchVehicleMetric(t, pool, teslaID, dayNPlus1)
	if !ok {
		t.Fatal("expected the vehicle_metrics row for day N+1 to still exist after the recapture")
	}
	if !after.DistanceTraveledKmCalc.Valid || !approxEqual(after.DistanceTraveledKmCalc.Float64, 70.0) {
		t.Errorf("after: DistanceTraveledKmCalc want 70.0 (1150-1080, against the REPLACEMENT), got %+v", after.DistanceTraveledKmCalc)
	}
	if !after.BatteryUsedPctCalc.Valid || after.BatteryUsedPctCalc.Int32 != 5 {
		t.Errorf("after: BatteryUsedPctCalc want 5 (60-55, against the REPLACEMENT), got %+v", after.BatteryUsedPctCalc)
	}
	if !after.ConsumedPct.Valid || !approxEqual(after.ConsumedPct.Float64, 5.0) {
		t.Errorf("after: ConsumedPct want 5.0, got %+v", after.ConsumedPct)
	}
	if approxEqual(after.DistanceTraveledKmCalc.Float64, before.DistanceTraveledKmCalc.Float64) {
		t.Error("day N+1's row did not change after the recapture -- the stale-successor bug is still present")
	}
}

// ===========================================================================
// RM38-analytics-add-vehicle-status-columns -- Wave 5 DB-integration tests
// (tasks 5.1-5.3). Fixtures RM38-A/RM38-B extend Fixture A/C's shape
// (metricsFixtureRM38A/metricsFixtureRM38B above) and Fixture RM38-C is a
// pre-migration row (seedPreMigrationVehicleMetric above), all per
// design.md's Test Contract. Expected values are copied verbatim from that
// contract, never derived by reading recalculate.go/reader.go.
// ===========================================================================

// TestRecalculate_FixtureRM38A_StatusColumnsPersisted covers task 5.1:
// Recalculate persists all eight new columns for Fixture RM38-A
// (predecessor exists), read back via fetchVehicleMetric's direct SELECT.
func TestRecalculate_FixtureRM38A_StatusColumnsPersisted(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	const teslaID = int64(990001)
	cleanupVehicleMetrics(t, pool, teslaID)

	prev, cur := metricsFixtureRM38A(teslaID)
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)

	rec := newRealRecalculator(pool)
	start := day(2026, 8, 10)
	end := start
	if err := rec.Recalculate(ctx, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	row, ok := fetchVehicleMetric(t, pool, teslaID, start)
	if !ok {
		t.Fatal("expected a vehicle_metrics row for Fixture RM38-A, found none")
	}

	if !row.Locked.Valid || row.Locked.Bool != true {
		t.Errorf("locked: want true, got %+v", row.Locked)
	}
	if !row.SentryMode.Valid || row.SentryMode.Bool != false {
		t.Errorf("sentry_mode: want false (not NULL -- a real reported value), got %+v", row.SentryMode)
	}
	if !row.CarVersion.Valid || row.CarVersion.String != "2026.28.4" {
		t.Errorf("car_version: want 2026.28.4, got %+v", row.CarVersion)
	}
	if !row.InsideTempC.Valid || !approxEqual(row.InsideTempC.Float64, 21.5) {
		t.Errorf("inside_temp_c: want 21.5, got %+v", row.InsideTempC)
	}
	if !row.OutsideTempC.Valid || !approxEqual(row.OutsideTempC.Float64, 18.0) {
		t.Errorf("outside_temp_c: want 18.0, got %+v", row.OutsideTempC)
	}
	if !row.ChargingState.Valid || row.ChargingState.String != "Disconnected" {
		t.Errorf("charging_state: want Disconnected, got %+v", row.ChargingState)
	}
	if !row.ChargeLimitSocPct.Valid || row.ChargeLimitSocPct.Int32 != 80 {
		t.Errorf("charge_limit_soc_pct: want 80, got %+v", row.ChargeLimitSocPct)
	}
	wantCapturedAt := time.Date(2026, 8, 11, 3, 31, 0, 0, time.UTC)
	if !row.CapturedAt.Valid || !row.CapturedAt.Time.Equal(wantCapturedAt) {
		t.Errorf("captured_at: want %v, got %+v", wantCapturedAt, row.CapturedAt)
	}

	// Every RM29 column value is unchanged from the archived Fixture A
	// (design.md: "every RM29 column value is unchanged from the archived
	// fixture") -- re-asserted here so this test also guards the eight new
	// columns' write path against disturbing the pre-existing ones.
	if row.BatteryLevelPct != 65 {
		t.Errorf("BatteryLevelPct: want 65, got %d", row.BatteryLevelPct)
	}
	if !row.ConsumedPct.Valid || !approxEqual(row.ConsumedPct.Float64, 15.0) {
		t.Errorf("ConsumedPct: want 15.0, got %+v", row.ConsumedPct)
	}
	if row.Flagged != false {
		t.Errorf("Flagged: want false, got %v", row.Flagged)
	}
}

// TestRecalculate_FixtureRM38B_StatusColumnsPersistedWithoutPredecessor
// covers task 5.2: Recalculate for Fixture RM38-B (no predecessor) persists
// the eight new columns (not NULL) while the five _calc columns/consumed_pct
// stay NULL and flagged is false -- the DB-level proof of design D3,
// complementing consumed_test.go's offline proof (task 4.1).
func TestRecalculate_FixtureRM38B_StatusColumnsPersistedWithoutPredecessor(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	const teslaID = int64(990002)
	cleanupVehicleMetrics(t, pool, teslaID)

	cur := metricsFixtureRM38B(teslaID)
	seedSnapshot(t, pool, cur)

	rec := newRealRecalculator(pool)
	start := day(2026, 8, 4)
	end := start
	if err := rec.Recalculate(ctx, teslaID, start, end); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	row, ok := fetchVehicleMetric(t, pool, teslaID, start)
	if !ok {
		t.Fatal("expected a DENSE vehicle_metrics row for Fixture RM38-B's predecessor-less day, found none (design.md D9)")
	}

	// The eight new columns: populated (NOT NULL) even without a
	// predecessor -- design D3, the core proof this test exists for.
	if !row.Locked.Valid || row.Locked.Bool != false {
		t.Errorf("locked: want false (NOT NULL, design D3), got %+v", row.Locked)
	}
	if row.SentryMode.Valid {
		t.Errorf("sentry_mode: want NULL (the vehicle genuinely did not report sentry -- design D2/D8's 'not reported' reading), got %v", row.SentryMode.Bool)
	}
	if !row.CarVersion.Valid || row.CarVersion.String != "2026.28.4" {
		t.Errorf("car_version: want 2026.28.4, got %+v", row.CarVersion)
	}
	if !row.InsideTempC.Valid || !approxEqual(row.InsideTempC.Float64, 19.0) {
		t.Errorf("inside_temp_c: want 19.0, got %+v", row.InsideTempC)
	}
	if !row.OutsideTempC.Valid || !approxEqual(row.OutsideTempC.Float64, 14.0) {
		t.Errorf("outside_temp_c: want 14.0, got %+v", row.OutsideTempC)
	}
	if !row.ChargingState.Valid || row.ChargingState.String != "Charging" {
		t.Errorf("charging_state: want Charging, got %+v", row.ChargingState)
	}
	if !row.ChargeLimitSocPct.Valid || row.ChargeLimitSocPct.Int32 != 90 {
		t.Errorf("charge_limit_soc_pct: want 90, got %+v", row.ChargeLimitSocPct)
	}
	wantCapturedAt := time.Date(2026, 8, 5, 3, 30, 15, 0, time.UTC)
	if !row.CapturedAt.Valid || !row.CapturedAt.Time.Equal(wantCapturedAt) {
		t.Errorf("captured_at: want %v, got %+v", wantCapturedAt, row.CapturedAt)
	}

	// The five _calc columns / consumed_pct / flagged: UNCHANGED D9
	// behavior -- the DB-level half of design D3's regression guard
	// (consumed_test.go's task 4.1 tests pin the offline half).
	if row.DistanceTraveledKmCalc.Valid {
		t.Errorf("DistanceTraveledKmCalc: want NULL, got %v", row.DistanceTraveledKmCalc.Float64)
	}
	if row.BatteryUsedPctCalc.Valid {
		t.Errorf("BatteryUsedPctCalc: want NULL, got %v", row.BatteryUsedPctCalc.Int32)
	}
	if row.KmPerPctCalc.Valid {
		t.Errorf("KmPerPctCalc: want NULL, got %v", row.KmPerPctCalc.Float64)
	}
	if row.EfficiencyRange100PctKmCalc.Valid {
		t.Errorf("EfficiencyRange100PctKmCalc: want NULL, got %v", row.EfficiencyRange100PctKmCalc.Float64)
	}
	if row.DaysSpannedCalc.Valid {
		t.Errorf("DaysSpannedCalc: want NULL, got %v", row.DaysSpannedCalc.Int32)
	}
	if row.ConsumedPct.Valid {
		t.Errorf("ConsumedPct: want NULL, got %v", row.ConsumedPct.Float64)
	}
	if row.Flagged != false {
		t.Errorf("Flagged: want false (NOT NULL, design.md D9), got %v", row.Flagged)
	}
	if row.MissingChargingType.Valid {
		t.Errorf("MissingChargingType: want NULL, got %v", row.MissingChargingType.String)
	}
}

// ===========================================================================
// TestReader_LatestMetricsForVehicles_*: four cases. One vehicle, several
// vehicles (DISTINCT ON must pick each car's own latest day), a pre-migration
// row, and an empty vehicle set. The empty-set case mirrors
// LatestSnapshotsByVehicles: an empty non-nil slice, never an error.
// ===========================================================================

// TestReader_LatestMetricsForVehicles_SingleVehicleFullyPopulated covers
// design.md's Test Contract "A single vehicle's latest status is returned":
// one vehicle, one recalculated day, LatestMetricsForVehicles returns exactly
// one VehicleStatus with every pointer field non-nil.
func TestReader_LatestMetricsForVehicles_SingleVehicleFullyPopulated(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	const teslaID = int64(990101)
	cleanupVehicleMetrics(t, pool, teslaID)

	prev, cur := metricsFixtureRM38A(teslaID)
	seedSnapshot(t, pool, prev)
	seedSnapshot(t, pool, cur)

	rec := newRealRecalculator(pool)
	start := day(2026, 8, 10)
	if err := rec.Recalculate(ctx, teslaID, start, start); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	rdr := newRealReader(pool)
	got, err := rdr.LatestMetricsForVehicles(ctx, vehicleref.All([]int64{teslaID}))
	if err != nil {
		t.Fatalf("LatestMetricsForVehicles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}
	vs := got[0]

	if vs.TeslaID != teslaID {
		t.Errorf("TeslaID: want %d, got %d", teslaID, vs.TeslaID)
	}
	if vs.BatteryLevelPct != 65 {
		t.Errorf("BatteryLevelPct: want 65, got %d", vs.BatteryLevelPct)
	}
	if vs.BatteryRangeKm != 280.0 {
		t.Errorf("BatteryRangeKm: want 280.0, got %v", vs.BatteryRangeKm)
	}
	if vs.OdometerKm != 1050.0 {
		t.Errorf("OdometerKm: want 1050.0, got %v", vs.OdometerKm)
	}
	if vs.InsideTempC == nil || !approxEqual(*vs.InsideTempC, 21.5) {
		t.Errorf("InsideTempC: want 21.5, got %v", vs.InsideTempC)
	}
	if vs.OutsideTempC == nil || !approxEqual(*vs.OutsideTempC, 18.0) {
		t.Errorf("OutsideTempC: want 18.0, got %v", vs.OutsideTempC)
	}
	if vs.Locked == nil || *vs.Locked != true {
		t.Errorf("Locked: want true, got %v", vs.Locked)
	}
	if vs.SentryMode == nil || *vs.SentryMode != false {
		t.Errorf("SentryMode: want false (non-nil), got %v", vs.SentryMode)
	}
	if vs.CarVersion == nil || *vs.CarVersion != "2026.28.4" {
		t.Errorf("CarVersion: want 2026.28.4, got %v", vs.CarVersion)
	}
	if vs.ChargingState == nil || *vs.ChargingState != "Disconnected" {
		t.Errorf("ChargingState: want Disconnected, got %v", vs.ChargingState)
	}
	if vs.ChargeLimitSocPct == nil || *vs.ChargeLimitSocPct != 80 {
		t.Errorf("ChargeLimitSocPct: want 80, got %v", vs.ChargeLimitSocPct)
	}
	wantCapturedAt := time.Date(2026, 8, 11, 3, 31, 0, 0, time.UTC)
	if vs.CapturedAt == nil || !vs.CapturedAt.Equal(wantCapturedAt) {
		t.Errorf("CapturedAt: want %v, got %v", wantCapturedAt, vs.CapturedAt)
	}
}

// TestReader_LatestMetricsForVehicles_TwoVehiclesEachOwnLatestDay covers
// design.md's Test Contract "Multi-vehicle DISTINCT ON case": two vehicles
// in one requested set, each with its own most-recent metric_date, must each
// return their OWN latest row -- never one vehicle's entry describing the
// other's day.
func TestReader_LatestMetricsForVehicles_TwoVehiclesEachOwnLatestDay(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	const teslaID1 = int64(990102)
	const teslaID2 = int64(990103)
	cleanupVehicleMetrics(t, pool, teslaID1)
	cleanupVehicleMetrics(t, pool, teslaID2)

	// Vehicle 1: Fixture RM38-A's predecessor/current pair, latest day 2026-08-11.
	prev1, cur1 := metricsFixtureRM38A(teslaID1)
	seedSnapshot(t, pool, prev1)
	seedSnapshot(t, pool, cur1)

	// Vehicle 2: Fixture RM38-B's single predecessor-less snapshot, latest
	// (and only) day 2026-08-04 -- a different calendar date than vehicle
	// 1's, and deliberately its own row so a cross-vehicle mixup is
	// observable in either direction.
	cur2 := metricsFixtureRM38B(teslaID2)
	seedSnapshot(t, pool, cur2)

	rec := newRealRecalculator(pool)
	if err := rec.Recalculate(ctx, teslaID1, day(2026, 8, 10), day(2026, 8, 10)); err != nil {
		t.Fatalf("Recalculate vehicle 1: %v", err)
	}
	if err := rec.Recalculate(ctx, teslaID2, day(2026, 8, 4), day(2026, 8, 4)); err != nil {
		t.Fatalf("Recalculate vehicle 2: %v", err)
	}

	rdr := newRealReader(pool)
	got, err := rdr.LatestMetricsForVehicles(ctx, vehicleref.All([]int64{teslaID1, teslaID2}))
	if err != nil {
		t.Fatalf("LatestMetricsForVehicles: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want exactly 2 entries (one per vehicle), got %d: %+v", len(got), got)
	}

	byVehicle := map[int64]VehicleStatus{}
	for _, vs := range got {
		byVehicle[vs.TeslaID] = vs
	}
	v1, ok := byVehicle[teslaID1]
	if !ok {
		t.Fatalf("missing entry for teslaID %d", teslaID1)
	}
	if v1.BatteryLevelPct != 65 {
		t.Errorf("vehicle 1 BatteryLevelPct: want 65 (its own latest day), got %d", v1.BatteryLevelPct)
	}
	if v1.Locked == nil || *v1.Locked != true {
		t.Errorf("vehicle 1 Locked: want true, got %v", v1.Locked)
	}

	v2, ok := byVehicle[teslaID2]
	if !ok {
		t.Fatalf("missing entry for teslaID %d", teslaID2)
	}
	if v2.BatteryLevelPct != 90 {
		t.Errorf("vehicle 2 BatteryLevelPct: want 90 (its own latest -- and only -- day, never vehicle 1's), got %d", v2.BatteryLevelPct)
	}
	if v2.Locked == nil || *v2.Locked != false {
		t.Errorf("vehicle 2 Locked: want false (its own value, never vehicle 1's true), got %v", v2.Locked)
	}
	if v2.SentryMode != nil {
		t.Errorf("vehicle 2 SentryMode: want nil, got %v", *v2.SentryMode)
	}
}

// TestReader_LatestMetricsForVehicles_PreMigrationRowReportsAbsentStatus
// covers design.md's Test Contract Fixture RM38-C: a vehicle_metrics row
// written before this migration exists. Its existing battery/range/odometer
// values come back unchanged; all eight new fields come back nil -- never a
// fabricated default such as "unlocked" or "sentry off".
func TestReader_LatestMetricsForVehicles_PreMigrationRowReportsAbsentStatus(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	const teslaID = int64(990104)
	cleanupVehicleMetrics(t, pool, teslaID)

	metricDate := day(2026, 8, 1)
	seedPreMigrationVehicleMetric(t, pool, teslaID, metricDate, 55, 900.0, 310.0)

	rdr := newRealReader(pool)
	got, err := rdr.LatestMetricsForVehicles(ctx, vehicleref.All([]int64{teslaID}))
	if err != nil {
		t.Fatalf("LatestMetricsForVehicles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}
	vs := got[0]

	if vs.TeslaID != teslaID {
		t.Errorf("TeslaID: want %d, got %d", teslaID, vs.TeslaID)
	}
	if vs.BatteryLevelPct != 55 {
		t.Errorf("BatteryLevelPct: want 55 (the existing pre-migration value), got %d", vs.BatteryLevelPct)
	}
	if vs.OdometerKm != 900.0 {
		t.Errorf("OdometerKm: want 900.0, got %v", vs.OdometerKm)
	}
	if vs.BatteryRangeKm != 310.0 {
		t.Errorf("BatteryRangeKm: want 310.0, got %v", vs.BatteryRangeKm)
	}
	if vs.InsideTempC != nil {
		t.Errorf("InsideTempC: want nil, got %v", *vs.InsideTempC)
	}
	if vs.OutsideTempC != nil {
		t.Errorf("OutsideTempC: want nil, got %v", *vs.OutsideTempC)
	}
	if vs.Locked != nil {
		t.Errorf("Locked: want nil, not a fabricated default, got %v", *vs.Locked)
	}
	if vs.SentryMode != nil {
		t.Errorf("SentryMode: want nil, got %v", *vs.SentryMode)
	}
	if vs.CarVersion != nil {
		t.Errorf("CarVersion: want nil, got %v", *vs.CarVersion)
	}
	if vs.ChargingState != nil {
		t.Errorf("ChargingState: want nil, got %v", *vs.ChargingState)
	}
	if vs.ChargeLimitSocPct != nil {
		t.Errorf("ChargeLimitSocPct: want nil, got %v", *vs.ChargeLimitSocPct)
	}
	if vs.CapturedAt != nil {
		t.Errorf("CapturedAt: want nil, got %v", *vs.CapturedAt)
	}

	// RM50-analytics-add-tire-pressure-columns (task 2.5): the six new-to-
	// the-port columns (four TPMS + the two newly-exposed _calc columns) are
	// exactly like the eight RM38 fields above on this pre-migration row --
	// seedPreMigrationVehicleMetric's INSERT never named them, so Postgres
	// leaves them NULL, never a fabricated default (design.md's Test
	// Contract "a pre-migration row").
	if vs.TpmsPressureFLPSI != nil {
		t.Errorf("TpmsPressureFLPSI: want nil, got %v", *vs.TpmsPressureFLPSI)
	}
	if vs.TpmsPressureFRPSI != nil {
		t.Errorf("TpmsPressureFRPSI: want nil, got %v", *vs.TpmsPressureFRPSI)
	}
	if vs.TpmsPressureRLPSI != nil {
		t.Errorf("TpmsPressureRLPSI: want nil, got %v", *vs.TpmsPressureRLPSI)
	}
	if vs.TpmsPressureRRPSI != nil {
		t.Errorf("TpmsPressureRRPSI: want nil, got %v", *vs.TpmsPressureRRPSI)
	}
	if vs.DistanceTraveledKmCalc != nil {
		t.Errorf("DistanceTraveledKmCalc: want nil, got %v", *vs.DistanceTraveledKmCalc)
	}
	if vs.ConsumedPct != nil {
		t.Errorf("ConsumedPct: want nil, got %v", *vs.ConsumedPct)
	}
}

// TestReader_LatestMetricsForVehicles_EmptyVehicleSetReturnsEmptyNonNilSlice
// asserts that an empty vehicle set returns no results, not an error --
// mirroring telemetry.Reader.LatestSnapshotsByVehicles's identical
// empty-input contract. This is the empty-input case, not an account lookup:
// no vehicle is ever seeded here.
func TestReader_LatestMetricsForVehicles_EmptyVehicleSetReturnsEmptyNonNilSlice(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	rdr := newRealReader(pool)
	got, err := rdr.LatestMetricsForVehicles(ctx, vehicleref.All(nil))
	if err != nil {
		t.Fatalf("LatestMetricsForVehicles: want nil error, got %v", err)
	}
	if got == nil {
		t.Fatal("want a non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("want 0 entries, got %d: %+v", len(got), got)
	}
}

// ===========================================================================
// TPMS coverage: one Recalculate round-trip and one read through Reader. The
// pre-migration case is not repeated here; it extends
// TestReader_LatestMetricsForVehicles_PreMigrationRowReportsAbsentStatus
// above instead of seeding a second fixture. Every expected value below was
// written before the implementation, never read back out of it.
// ===========================================================================

// TestRecalculate_TPMS_RoundTrip seeds one
// telemetry.vehicle_snapshots row with all four TPMS fields non-NULL,
// Recalculate for the day containing it, then the same four values read
// back unconverted from analytics.vehicle_metrics.
//
// The four values are chosen as exact binary fractions (42.5, 42.25, 41.75,
// 42.125) on purpose: telemetry.vehicle_snapshots' own tpms_pressure_*_psi
// columns are REAL (float4, ai/go-conventions.md's telemetry precedent), so
// seedSnapshot narrows through float32 on the way in. A value with a
// non-power-of-two fraction (e.g. 41.8) would pick up a float32 rounding
// error under 1e-6 -- irrelevant to production (telemetry's own adapter
// converts Fleet API bar readings the same way) but larger than this
// package's approxEqual epsilon (1e-9), which would fail the assertion for
// a reason unrelated to the code under test. The row is predecessor-less (a
// single seeded snapshot): design.md D2 says TPMS populates in BOTH
// branches, so this also exercises the predecessor-less branch at the DB
// level, mirroring Fixture RM38-B's own precedent.
func TestRecalculate_TPMS_RoundTrip(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	const teslaID = int64(990201)
	cleanupVehicleMetrics(t, pool, teslaID)

	metricDate := day(2026, 9, 1)
	cur := telemetry.Snapshot{
		TeslaID:           teslaID,
		CapturedAt:        time.Date(2026, 9, 2, 3, 30, 0, 0, time.UTC),
		CapturedDate:      metricDate.AddDate(0, 0, 1),
		BatteryLevelPct:   72,
		OdometerKm:        1200.0,
		BatteryRangeKm:    290.0,
		ChargingState:     "Disconnected",
		ChargeLimitSocPct: 90,
		InsideTempC:       20.0,
		OutsideTempC:      15.0,
		Locked:            true,
		CarVersion:        "2026.30.1",
		TpmsPressureFLPSI: floatPtr(42.5),
		TpmsPressureFRPSI: floatPtr(42.25),
		TpmsPressureRLPSI: floatPtr(41.75),
		TpmsPressureRRPSI: floatPtr(42.125),
	}
	seedSnapshot(t, pool, cur)

	rec := newRealRecalculator(pool)
	if err := rec.Recalculate(ctx, teslaID, metricDate, metricDate); err != nil {
		t.Fatalf("Recalculate: %v", err)
	}

	row, ok := fetchVehicleMetric(t, pool, teslaID, metricDate)
	if !ok {
		t.Fatal("expected a vehicle_metrics row, found none")
	}
	if !row.TpmsPressureFlPsi.Valid || row.TpmsPressureFlPsi.Float64 != 42.5 {
		t.Errorf("TpmsPressureFlPsi: want 42.5, got %+v", row.TpmsPressureFlPsi)
	}
	if !row.TpmsPressureFrPsi.Valid || row.TpmsPressureFrPsi.Float64 != 42.25 {
		t.Errorf("TpmsPressureFrPsi: want 42.25, got %+v", row.TpmsPressureFrPsi)
	}
	if !row.TpmsPressureRlPsi.Valid || row.TpmsPressureRlPsi.Float64 != 41.75 {
		t.Errorf("TpmsPressureRlPsi: want 41.75, got %+v", row.TpmsPressureRlPsi)
	}
	if !row.TpmsPressureRrPsi.Valid || row.TpmsPressureRrPsi.Float64 != 42.125 {
		t.Errorf("TpmsPressureRrPsi: want 42.125, got %+v", row.TpmsPressureRrPsi)
	}
}

// TestReader_LatestMetricsForVehicles_TPMS_And_ExposedCalcColumns seeds one
// vehicle_metrics row with non-NULL tpms_pressure_fl_psi (42.5),
// distance_traveled_km_calc (12.3), consumed_pct (5.0), and the three
// day-over-day travel-progress deltas (distance_traveled_km_delta_calc 7.5,
// consumed_pct_delta_calc 1.2, km_per_pct_delta_calc -0.4) -- all DOUBLE
// PRECISION columns, not REAL, so no float32 narrowing applies and exact
// equality is the right assertion. The row is seeded directly, not through
// Recalculate: this test is about the read projection, which
// TestRecalculate_TPMS_RoundTrip above does not exercise (it only reads back
// via fetchVehicleMetric's raw SQL, never through Reader). The three deltas
// are covered here so a rename or a projection regression on this column
// family is caught without needing a full Recalculate-driven round trip --
// that coverage lives separately, against real computed values.
func TestReader_LatestMetricsForVehicles_TPMS_And_ExposedCalcColumns(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	const teslaID = int64(990202)
	cleanupVehicleMetrics(t, pool, teslaID)

	metricDate := day(2026, 9, 3)
	_, err := pool.Exec(ctx, `
		INSERT INTO analytics.vehicle_metrics (
			tesla_id, metric_date, battery_level_pct, odometer_km, battery_range_km,
			flagged, tpms_pressure_fl_psi, distance_traveled_km_calc, consumed_pct,
			distance_traveled_km_delta_calc, consumed_pct_delta_calc, km_per_pct_delta_calc
		) VALUES ($1, $2, $3, $4, $5, false, $6, $7, $8, $9, $10, $11)`,
		teslaID, dateFrom(metricDate), int32(60), 800.0, 300.0,
		42.5, 12.3, 5.0,
		7.5, 1.2, -0.4,
	)
	if err != nil {
		t.Fatalf("seeding vehicle_metrics: %v", err)
	}

	rdr := newRealReader(pool)
	got, err := rdr.LatestMetricsForVehicles(ctx, vehicleref.All([]int64{teslaID}))
	if err != nil {
		t.Fatalf("LatestMetricsForVehicles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}
	vs := got[0]

	if vs.TpmsPressureFLPSI == nil || *vs.TpmsPressureFLPSI != 42.5 {
		t.Errorf("TpmsPressureFLPSI: want 42.5, got %v", vs.TpmsPressureFLPSI)
	}
	if vs.DistanceTraveledKmCalc == nil || *vs.DistanceTraveledKmCalc != 12.3 {
		t.Errorf("DistanceTraveledKmCalc: want 12.3, got %v", vs.DistanceTraveledKmCalc)
	}
	if vs.ConsumedPct == nil || *vs.ConsumedPct != 5.0 {
		t.Errorf("ConsumedPct: want 5.0, got %v", vs.ConsumedPct)
	}
	if vs.DistanceTraveledKmDeltaCalc == nil || *vs.DistanceTraveledKmDeltaCalc != 7.5 {
		t.Errorf("DistanceTraveledKmDeltaCalc: want 7.5, got %v", vs.DistanceTraveledKmDeltaCalc)
	}
	if vs.ConsumedPctDeltaCalc == nil || *vs.ConsumedPctDeltaCalc != 1.2 {
		t.Errorf("ConsumedPctDeltaCalc: want 1.2, got %v", vs.ConsumedPctDeltaCalc)
	}
	if vs.KmPerPctDeltaCalc == nil || *vs.KmPerPctDeltaCalc != -0.4 {
		t.Errorf("KmPerPctDeltaCalc: want -0.4, got %v", vs.KmPerPctDeltaCalc)
	}
}

// TestReader_LatestMetricsForVehicles_TravelProgressDeltas_RoundTripThroughRecalculate
// proves the three new travel-progress deltas reach VehicleStatus through a
// REAL Recalculate pass, not a hand-seeded row -- unlike the fixed-value test
// above, which only checks the projection. Three consecutive snapshots are
// seeded: a predecessor day plus two tracked days.
//
//	day 0 (predecessor only): odometer 1000.0, battery 90%
//	day 1: odometer 1050.0, battery 75%  -> distance 50.0, consumed 15.0, km/pct 50/15
//	day 2: odometer 1110.0, battery 55%  -> distance 60.0, consumed 20.0, km/pct 60/20
//
// Recalculating [day1, day2] in one pass gives day2 a same-pass predecessor
// (day1's own freshly-derived row), so its deltas come back non-nil: 10.0,
// 5.0, and 60/20-50/15. Recalculating day2 again
// alone afterward is fixture 2's case: day1 is now outside the window, so day2
// is the first row this second pass processes and its deltas must come back
// nil even though its own figures are unchanged.
func TestReader_LatestMetricsForVehicles_TravelProgressDeltas_RoundTripThroughRecalculate(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	const teslaID = int64(990203)
	cleanupVehicleMetrics(t, pool, teslaID)

	day0 := telemetry.Snapshot{
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 9, 9, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 9, 9),
		OdometerKm:      1000.0,
		BatteryLevelPct: 90,
		BatteryRangeKm:  350.0,
	}
	day1 := telemetry.Snapshot{
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 9, 10, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 9, 10),
		OdometerKm:      1050.0,
		BatteryLevelPct: 75,
		BatteryRangeKm:  310.0,
	}
	day2 := telemetry.Snapshot{
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 9, 11, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 9, 11),
		OdometerKm:      1110.0,
		BatteryLevelPct: 55,
		BatteryRangeKm:  270.0,
	}
	seedSnapshot(t, pool, day0)
	seedSnapshot(t, pool, day1)
	seedSnapshot(t, pool, day2)
	// No Supercharger sessions, no manual entries -- chargePct stays 0 for
	// both days, matching fixture 1's plain-driving numbers exactly.

	rec := newRealRecalculator(pool)
	rdr := newRealReader(pool)
	// A snapshot's metric date is its capture date minus one day, so the
	// window is stated in metric dates, not capture dates. Passing the
	// capture dates here would put day1 outside the window and silently
	// leave day2 without a same-pass predecessor.
	day1Eff := day(2026, 9, 9)
	day2Eff := day(2026, 9, 10)

	// Pass 1: recalculate day1 and day2 together, so day2 gets a same-pass
	// predecessor and its deltas come back non-nil.
	if err := rec.Recalculate(ctx, teslaID, day1Eff, day2Eff); err != nil {
		t.Fatalf("Recalculate([day1, day2]): %v", err)
	}

	got, err := rdr.LatestMetricsForVehicles(ctx, vehicleref.All([]int64{teslaID}))
	if err != nil {
		t.Fatalf("LatestMetricsForVehicles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}
	vs := got[0]

	wantKmPerPctDelta := 60.0/20.0 - 50.0/15.0
	if vs.DistanceTraveledKmDeltaCalc == nil || !approxEqual(*vs.DistanceTraveledKmDeltaCalc, 10.0) {
		t.Errorf("pass 1 DistanceTraveledKmDeltaCalc: want 10.0, got %v", vs.DistanceTraveledKmDeltaCalc)
	}
	if vs.ConsumedPctDeltaCalc == nil || !approxEqual(*vs.ConsumedPctDeltaCalc, 5.0) {
		t.Errorf("pass 1 ConsumedPctDeltaCalc: want 5.0, got %v", vs.ConsumedPctDeltaCalc)
	}
	if vs.KmPerPctDeltaCalc == nil || !approxEqual(*vs.KmPerPctDeltaCalc, wantKmPerPctDelta) {
		t.Errorf("pass 1 KmPerPctDeltaCalc: want %v, got %v", wantKmPerPctDelta, vs.KmPerPctDeltaCalc)
	}

	// Pass 2: recalculate day2 alone. day1 falls outside this window, so day2
	// is the first row THIS pass processes -- its own figures stay the same,
	// but the three deltas must now come back nil (fixture 2's case).
	if err := rec.Recalculate(ctx, teslaID, day2Eff, day2Eff); err != nil {
		t.Fatalf("Recalculate([day2, day2]): %v", err)
	}

	got, err = rdr.LatestMetricsForVehicles(ctx, vehicleref.All([]int64{teslaID}))
	if err != nil {
		t.Fatalf("LatestMetricsForVehicles (pass 2): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}
	vs = got[0]

	if vs.DistanceTraveledKmCalc == nil || !approxEqual(*vs.DistanceTraveledKmCalc, 60.0) {
		t.Errorf("pass 2 DistanceTraveledKmCalc: want 60.0 (own figure unchanged), got %v", vs.DistanceTraveledKmCalc)
	}
	if vs.DistanceTraveledKmDeltaCalc != nil {
		t.Errorf("pass 2 DistanceTraveledKmDeltaCalc: want nil (single-day window, no same-pass predecessor), got %v", *vs.DistanceTraveledKmDeltaCalc)
	}
	if vs.ConsumedPctDeltaCalc != nil {
		t.Errorf("pass 2 ConsumedPctDeltaCalc: want nil, got %v", *vs.ConsumedPctDeltaCalc)
	}
	if vs.KmPerPctDeltaCalc != nil {
		t.Errorf("pass 2 KmPerPctDeltaCalc: want nil, got %v", *vs.KmPerPctDeltaCalc)
	}
}
