// db_change_detection_integration_test.go proves the change-detecting
// updated_at CASE expression added to UpsertSuperchargerHistory by
// RM44-telemetry-add-change-detecting-upsert (design.md D1/D2). The six
// scenarios below are design.md's test contract D7.1-D7.6, authored before
// this file existed (ai/go-conventions.md "contract-first authoring") — the
// expected values here are binding; do not adjust them to match whatever the
// SQL happens to do.
//
// All tests are TEST_DATABASE_URL-gated integration tests, following the existing
// pattern in db_integration_test.go (internal/testdb provisioning, TestMain
// auto-skip when no Postgres is reachable). A small sleep separates the two
// upserts in every test: Postgres's now() has microsecond resolution, so a
// sleep makes a wrongly-moving (or wrongly-frozen) updated_at show up as a
// clearly different timestamp instead of a same-microsecond coincidence.
package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// changeDetectSleep separates a session's two upserts in every test below.
const changeDetectSleep = 5 * time.Millisecond

// queryChangeDetectUpdatedAt reads updated_at directly by session_id,
// independent of any reader query's own column list.
func queryChangeDetectUpdatedAt(ctx context.Context, pool *pgxpool.Pool, sessionID int64) (time.Time, error) {
	var updatedAt pgtype.Timestamptz
	err := pool.QueryRow(ctx,
		`SELECT updated_at FROM telemetry.supercharger_history WHERE session_id = $1`,
		sessionID,
	).Scan(&updatedAt)
	if err != nil {
		return time.Time{}, err
	}
	return updatedAt.Time, nil
}

// changeDetectHumanTrio carries the three human-owned verification columns
// D7.5 asserts.
type changeDetectHumanTrio struct {
	StartBatteryPct  pgtype.Int2
	EndBatteryPct    pgtype.Int2
	BatteryPctSource pgtype.Text
}

func queryChangeDetectHumanTrio(ctx context.Context, pool *pgxpool.Pool, sessionID int64) (changeDetectHumanTrio, error) {
	var row changeDetectHumanTrio
	err := pool.QueryRow(ctx,
		`SELECT start_battery_pct, end_battery_pct, battery_pct_source
		   FROM telemetry.supercharger_history WHERE session_id = $1`,
		sessionID,
	).Scan(&row.StartBatteryPct, &row.EndBatteryPct, &row.BatteryPctSource)
	return row, err
}

// queryChangeDetectUnlatch reads unlatch_date_time directly, for D7.6.
func queryChangeDetectUnlatch(ctx context.Context, pool *pgxpool.Pool, sessionID int64) (pgtype.Timestamptz, error) {
	var unlatch pgtype.Timestamptz
	err := pool.QueryRow(ctx,
		`SELECT unlatch_date_time FROM telemetry.supercharger_history WHERE session_id = $1`,
		sessionID,
	).Scan(&unlatch)
	return unlatch, err
}

// TestUpsertSuperchargerHistory_UnchangedResync_LeavesUpdatedAtUntouched is D7.1:
// an identical re-upsert must leave updated_at byte-identical, down to the
// microsecond.
func TestUpsertSuperchargerHistory_UnchangedResync_LeavesUpdatedAtUntouched(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const sessionID = int64(61001)
	teslaID := int64(810001)
	start := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.supercharger_history WHERE session_id = $1", sessionID)
	})

	energy := 10.0
	sess := SuperchargerHistory{
		SessionID:           sessionID,
		AccountID:           accountID,
		VIN:                 "VIN_CD_001",
		TeslaID:             &teslaID,
		SiteLocationName:    "Site",
		CountryCode:         "US",
		ChargeStartDateTime: start,
		ChargeStopDateTime:  start.Add(30 * time.Minute),
		BillingType:         "PAYMENT",
		VehicleMakeType:     "MODEL_3",
		EnergyKWh:           &energy,
		RawData:             []byte(`{"sessionId":61001}`),
	}
	if err := st.upsertSuperchargerHistory(ctx, sess); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	before, err := queryChangeDetectUpdatedAt(ctx, pool, sessionID)
	if err != nil {
		t.Fatalf("reading updated_at: %v", err)
	}

	time.Sleep(changeDetectSleep)
	if err := st.upsertSuperchargerHistory(ctx, sess); err != nil {
		t.Fatalf("identical re-upsert: %v", err)
	}

	after, err := queryChangeDetectUpdatedAt(ctx, pool, sessionID)
	if err != nil {
		t.Fatalf("reading updated_at after re-upsert: %v", err)
	}
	if !before.Equal(after) {
		t.Errorf("updated_at moved on an unchanged re-upsert: before=%v after=%v", before, after)
	}
}

// TestUpsertSuperchargerHistory_EnergyChange_AdvancesUpdatedAt is D7.2: a real
// energy_kwh change must advance updated_at.
func TestUpsertSuperchargerHistory_EnergyChange_AdvancesUpdatedAt(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const sessionID = int64(61002)
	teslaID := int64(810002)
	start := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.supercharger_history WHERE session_id = $1", sessionID)
	})

	energy1 := 10.0
	sess := SuperchargerHistory{
		SessionID:           sessionID,
		AccountID:           accountID,
		VIN:                 "VIN_CD_002",
		TeslaID:             &teslaID,
		SiteLocationName:    "Site",
		CountryCode:         "US",
		ChargeStartDateTime: start,
		ChargeStopDateTime:  start.Add(30 * time.Minute),
		BillingType:         "PAYMENT",
		VehicleMakeType:     "MODEL_3",
		EnergyKWh:           &energy1,
		RawData:             []byte(`{"sessionId":61002}`),
	}
	if err := st.upsertSuperchargerHistory(ctx, sess); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	before, err := queryChangeDetectUpdatedAt(ctx, pool, sessionID)
	if err != nil {
		t.Fatalf("reading updated_at: %v", err)
	}

	time.Sleep(changeDetectSleep)
	energy2 := 20.0
	sess.EnergyKWh = &energy2
	if err := st.upsertSuperchargerHistory(ctx, sess); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	after, err := queryChangeDetectUpdatedAt(ctx, pool, sessionID)
	if err != nil {
		t.Fatalf("reading updated_at after re-upsert: %v", err)
	}
	if !after.After(before) {
		t.Errorf("updated_at did not advance on an energy_kwh change: before=%v after=%v", before, after)
	}
}

// TestUpsertSuperchargerHistory_IsPaidChange_AdvancesUpdatedAt is D7.3: is_paid
// flipping false to true must advance updated_at.
func TestUpsertSuperchargerHistory_IsPaidChange_AdvancesUpdatedAt(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const sessionID = int64(61003)
	teslaID := int64(810003)
	start := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.supercharger_history WHERE session_id = $1", sessionID)
	})

	notPaid := false
	sess := SuperchargerHistory{
		SessionID:           sessionID,
		AccountID:           accountID,
		VIN:                 "VIN_CD_003",
		TeslaID:             &teslaID,
		SiteLocationName:    "Site",
		CountryCode:         "US",
		ChargeStartDateTime: start,
		ChargeStopDateTime:  start.Add(30 * time.Minute),
		BillingType:         "PAYMENT",
		VehicleMakeType:     "MODEL_3",
		IsPaid:              &notPaid,
		RawData:             []byte(`{"sessionId":61003}`),
	}
	if err := st.upsertSuperchargerHistory(ctx, sess); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	before, err := queryChangeDetectUpdatedAt(ctx, pool, sessionID)
	if err != nil {
		t.Fatalf("reading updated_at: %v", err)
	}

	time.Sleep(changeDetectSleep)
	paid := true
	sess.IsPaid = &paid
	if err := st.upsertSuperchargerHistory(ctx, sess); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	after, err := queryChangeDetectUpdatedAt(ctx, pool, sessionID)
	if err != nil {
		t.Fatalf("reading updated_at after re-upsert: %v", err)
	}
	if !after.After(before) {
		t.Errorf("updated_at did not advance on an is_paid change: before=%v after=%v", before, after)
	}
}

// TestUpsertSuperchargerHistory_TeslaIDRecovered_AdvancesUpdatedAt is D7.4: a
// tesla_id moving from NULL to a real value (orphan recovery) must advance
// updated_at — the path roadmap D3 calls out by name.
func TestUpsertSuperchargerHistory_TeslaIDRecovered_AdvancesUpdatedAt(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const sessionID = int64(61004)
	start := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.supercharger_history WHERE session_id = $1", sessionID)
	})

	sess := SuperchargerHistory{
		SessionID:           sessionID,
		AccountID:           accountID,
		VIN:                 "VIN_CD_004",
		TeslaID:             nil, // orphan: VIN not a currently registered vehicle
		SiteLocationName:    "Site",
		CountryCode:         "US",
		ChargeStartDateTime: start,
		ChargeStopDateTime:  start.Add(30 * time.Minute),
		BillingType:         "PAYMENT",
		VehicleMakeType:     "MODEL_3",
		RawData:             []byte(`{"sessionId":61004}`),
	}
	if err := st.upsertSuperchargerHistory(ctx, sess); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	before, err := queryChangeDetectUpdatedAt(ctx, pool, sessionID)
	if err != nil {
		t.Fatalf("reading updated_at: %v", err)
	}

	time.Sleep(changeDetectSleep)
	teslaID := int64(810004)
	sess.TeslaID = &teslaID
	if err := st.upsertSuperchargerHistory(ctx, sess); err != nil {
		t.Fatalf("second upsert (orphan recovery): %v", err)
	}

	after, err := queryChangeDetectUpdatedAt(ctx, pool, sessionID)
	if err != nil {
		t.Fatalf("reading updated_at after re-upsert: %v", err)
	}
	if !after.After(before) {
		t.Errorf("updated_at did not advance when tesla_id recovered from NULL: before=%v after=%v", before, after)
	}
}

// TestUpsertSuperchargerHistory_HumanBatteryPctSet_UnchangedResyncStillLeavesUpdatedAtUntouched
// is D7.5: a human-set battery-% trio must survive an otherwise-unchanged
// re-upsert both in value (never cleared) AND in updated_at (never advanced).
// This is D2 bucket (b)'s load-bearing case: on the live database, 3 of 8 rows
// already carry real values here.
func TestUpsertSuperchargerHistory_HumanBatteryPctSet_UnchangedResyncStillLeavesUpdatedAtUntouched(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const sessionID = int64(61005)
	teslaID := int64(810005)
	start := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.supercharger_history WHERE session_id = $1", sessionID)
	})

	energy := 15.0
	sess := SuperchargerHistory{
		SessionID:           sessionID,
		AccountID:           accountID,
		VIN:                 "VIN_CD_005",
		TeslaID:             &teslaID,
		SiteLocationName:    "Site",
		CountryCode:         "US",
		ChargeStartDateTime: start,
		ChargeStopDateTime:  start.Add(30 * time.Minute),
		BillingType:         "PAYMENT",
		VehicleMakeType:     "MODEL_3",
		EnergyKWh:           &energy,
		RawData:             []byte(`{"sessionId":61005}`),
	}
	if err := st.upsertSuperchargerHistory(ctx, sess); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Simulate a human verification: set the trio by raw SQL, never through
	// the upsert (there is no writer for it — R3/D3).
	if _, err := pool.Exec(ctx,
		`UPDATE telemetry.supercharger_history
		    SET start_battery_pct = $1, end_battery_pct = $2, battery_pct_source = $3
		  WHERE session_id = $4`,
		55, 80, "user_verified", sessionID,
	); err != nil {
		t.Fatalf("simulating human verification: %v", err)
	}

	before, err := queryChangeDetectUpdatedAt(ctx, pool, sessionID)
	if err != nil {
		t.Fatalf("reading updated_at: %v", err)
	}

	time.Sleep(changeDetectSleep)
	if err := st.upsertSuperchargerHistory(ctx, sess); err != nil {
		t.Fatalf("re-upsert after human verification: %v", err)
	}

	after, err := queryChangeDetectUpdatedAt(ctx, pool, sessionID)
	if err != nil {
		t.Fatalf("reading updated_at after re-upsert: %v", err)
	}
	if !before.Equal(after) {
		t.Errorf("updated_at moved after a human battery-percent verification: before=%v after=%v", before, after)
	}

	trio, err := queryChangeDetectHumanTrio(ctx, pool, sessionID)
	if err != nil {
		t.Fatalf("reading human battery-percent trio: %v", err)
	}
	if !trio.StartBatteryPct.Valid || trio.StartBatteryPct.Int16 != 55 {
		t.Errorf("start_battery_pct was cleared or changed by the re-upsert: got %+v", trio.StartBatteryPct)
	}
	if !trio.EndBatteryPct.Valid || trio.EndBatteryPct.Int16 != 80 {
		t.Errorf("end_battery_pct was cleared or changed by the re-upsert: got %+v", trio.EndBatteryPct)
	}
	if !trio.BatteryPctSource.Valid || trio.BatteryPctSource.String != "user_verified" {
		t.Errorf("battery_pct_source was cleared or changed by the re-upsert: got %+v", trio.BatteryPctSource)
	}
}

// TestUpsertSuperchargerHistory_WriteOnceColumnMismatch_NeverAdvancesUpdatedAt
// is D7.6: a write-once column (unlatch_date_time) mismatching between the
// stored row and the incoming values must never advance updated_at, and this
// must hold on a SECOND identical re-upsert too — proving the mismatch does
// not need to resolve, because bucket (c) keeps it out of the comparison
// every night, not just once.
func TestUpsertSuperchargerHistory_WriteOnceColumnMismatch_NeverAdvancesUpdatedAt(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const sessionID = int64(61006)
	teslaID := int64(810006)
	start := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.supercharger_history WHERE session_id = $1", sessionID)
	})

	energy := 12.0
	sess := SuperchargerHistory{
		SessionID:           sessionID,
		AccountID:           accountID,
		VIN:                 "VIN_CD_006",
		TeslaID:             &teslaID,
		SiteLocationName:    "Site",
		CountryCode:         "US",
		ChargeStartDateTime: start,
		ChargeStopDateTime:  start.Add(30 * time.Minute),
		UnlatchDateTime:     nil, // session not yet finalized
		BillingType:         "PAYMENT",
		VehicleMakeType:     "MODEL_3",
		EnergyKWh:           &energy,
		RawData:             []byte(`{"sessionId":61006}`),
	}
	if err := st.upsertSuperchargerHistory(ctx, sess); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	before, err := queryChangeDetectUpdatedAt(ctx, pool, sessionID)
	if err != nil {
		t.Fatalf("reading updated_at: %v", err)
	}

	// Tesla now reports a real unlatch time for the same session. The SET
	// clause never writes this column (bucket c), so this mismatch must never
	// be treated as a real change.
	unlatch := start.Add(45 * time.Minute)
	sess.UnlatchDateTime = &unlatch

	time.Sleep(changeDetectSleep)
	if err := st.upsertSuperchargerHistory(ctx, sess); err != nil {
		t.Fatalf("re-upsert with unlatch_date_time mismatch: %v", err)
	}

	after, err := queryChangeDetectUpdatedAt(ctx, pool, sessionID)
	if err != nil {
		t.Fatalf("reading updated_at after re-upsert: %v", err)
	}
	if !before.Equal(after) {
		t.Errorf("updated_at moved for a write-once-column mismatch: before=%v after=%v", before, after)
	}

	storedUnlatch, err := queryChangeDetectUnlatch(ctx, pool, sessionID)
	if err != nil {
		t.Fatalf("reading unlatch_date_time: %v", err)
	}
	if storedUnlatch.Valid {
		t.Errorf("unlatch_date_time should stay NULL (the SET clause never writes it), got %v", storedUnlatch.Time)
	}

	// Run the identical re-upsert a SECOND time: the mismatch persists
	// forever by design (the SET clause never resolves it), so updated_at
	// must not move this time either.
	time.Sleep(changeDetectSleep)
	if err := st.upsertSuperchargerHistory(ctx, sess); err != nil {
		t.Fatalf("second identical re-upsert: %v", err)
	}
	after2, err := queryChangeDetectUpdatedAt(ctx, pool, sessionID)
	if err != nil {
		t.Fatalf("reading updated_at after second re-upsert: %v", err)
	}
	if !before.Equal(after2) {
		t.Errorf("updated_at moved on the second re-upsert of the same write-once mismatch: before=%v after2=%v", before, after2)
	}
}
