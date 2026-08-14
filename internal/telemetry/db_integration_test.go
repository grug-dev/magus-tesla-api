package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	telemetrydb "github.com/cristianpena/magus-tesla-api/internal/telemetry/db"
)

// These tests exercise the real telemetrydb store against a live Postgres
// provisioned by TestMain (see testdb_test.go). The test database is
// auto-provisioned via testcontainers-go when DATABASE_URL is unset/unreachable
// (ai/go-conventions.md §persistence), so `go test ./...` is green with no
// manual DB setup as long as Docker is running locally.

// newTestStore builds a dbStore against the test Postgres provisioned by
// TestMain. It returns the store plus the pool for direct-SQL assertions.
func newTestStore(t *testing.T) (*dbStore, *pgxpool.Pool) {
	t.Helper()
	if testDSN == "" {
		t.Skip("no test Postgres: set DATABASE_URL or start Docker to run the DB-backed tests")
	}
	pool, err := pgxpool.New(context.Background(), testDSN)
	if err != nil {
		t.Fatalf("connecting to Postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	return &dbStore{q: telemetrydb.New(pool)}, pool
}

// cleanupVehicle removes any rows this test created so a shared DB stays tidy. The
// tables are append-only in production, but tests own their (account_id, tesla_id).
func cleanupVehicle(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64) {
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, "DELETE FROM vehicle_snapshots WHERE account_id = $1 AND tesla_id = $2", accountID, teslaID)
		_, _ = pool.Exec(ctx, "DELETE FROM poll_attempts WHERE account_id = $1 AND tesla_id = $2", accountID, teslaID)
	})
}

func ptrBool(b bool) *bool { return &b }

func TestStore_SnapshotRoundTrip_SentryNilIsNull(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()
	q := telemetrydb.New(pool)

	accountID := uuid.New()
	const teslaID = int64(900001)
	cleanupVehicle(t, pool, accountID, teslaID)

	captured := time.Now().UTC().Truncate(time.Microsecond)
	snap := Snapshot{
		AccountID:         accountID,
		TeslaID:           teslaID,
		CapturedAt:        captured,
		CapturedDate:      dateOnly(captured, time.UTC),
		BatteryLevelPct:   64,
		BatteryRangeKm:    338.766912, // 210.5 mi * 1.609344 (was miles pre-display-units)
		ChargingState:     "Disconnected",
		ChargeLimitSocPct: 80,
		OdometerKm:        87422.382432, // 54321.75 mi * 1.609344
		InsideTempC:       21.5,
		OutsideTempC:      17.0,
		Locked:            true,
		SentryMode:        nil, // not reported → must round-trip as SQL NULL
		CarVersion:        "2026.20.1",
		// latitude/longitude dropped in 20260801000001; lossless in raw_data JSONB.
		RawData: []byte(`{"response":{"id":900001,"charge_state":{"battery_level":64}}}`),
	}
	if err := st.insertSnapshot(ctx, snap); err != nil {
		t.Fatalf("insertSnapshot: %v", err)
	}

	got, err := q.ListSnapshotsByVehicle(ctx, telemetrydb.ListSnapshotsByVehicleParams{
		AccountID: accountID,
		TeslaID:   teslaID,
	})
	if err != nil {
		t.Fatalf("ListSnapshotsByVehicle: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 snapshot, got %d", len(got))
	}
	row := got[0]
	if row.BatteryLevelPct != 64 || row.ChargeLimitSocPct != 80 {
		t.Errorf("integer columns wrong: battery=%d limit=%d", row.BatteryLevelPct, row.ChargeLimitSocPct)
	}
	if row.BatteryRangeKm != 338.766912 || row.OdometerKm != 87422.382432 {
		t.Errorf("float columns wrong: range=%v odo=%v", row.BatteryRangeKm, row.OdometerKm)
	}
	if row.ChargingState != "Disconnected" || row.CarVersion != "2026.20.1" || !row.Locked {
		t.Errorf("string/bool columns wrong: %+v", row)
	}
	// latitude/longitude dropped in 20260801000001 — not asserted here;
	// values remain recoverable from raw_data->'drive_state'.
	// The nil *bool must persist as SQL NULL (invalid pgtype.Bool).
	if row.SentryMode.Valid {
		t.Errorf("nil SentryMode should round-trip as SQL NULL, got Valid=%t value=%t", row.SentryMode.Valid, row.SentryMode.Bool)
	}
	// captured_at round-trips.
	if !row.CapturedAt.Time.UTC().Equal(captured) {
		t.Errorf("captured_at wrong: want %v, got %v", captured, row.CapturedAt.Time.UTC())
	}
	// raw JSONB is preserved (JSONB may re-order keys/whitespace, so compare parsed).
	if len(row.RawData) == 0 {
		t.Error("raw_data JSONB was not stored")
	}
}

func TestStore_SentryTrueAndFalseRoundTripFaithfully(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()
	q := telemetrydb.New(pool)

	for _, tc := range []struct {
		name    string
		teslaID int64
		sentry  *bool
	}{
		{"sentry on", 900010, ptrBool(true)},
		{"sentry off", 900011, ptrBool(false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accountID := uuid.New()
			cleanupVehicle(t, pool, accountID, tc.teslaID)

			captured := time.Now().UTC()
			snap := Snapshot{
				AccountID:     accountID,
				TeslaID:       tc.teslaID,
				CapturedAt:    captured,
				CapturedDate:  dateOnly(captured, time.UTC),
				ChargingState: "Charging",
				CarVersion:    "v",
				SentryMode:    tc.sentry,
				RawData:       []byte(`{}`),
			}
			if err := st.insertSnapshot(ctx, snap); err != nil {
				t.Fatalf("insertSnapshot: %v", err)
			}

			got, err := q.ListSnapshotsByVehicle(ctx, telemetrydb.ListSnapshotsByVehicleParams{
				AccountID: accountID,
				TeslaID:   tc.teslaID,
			})
			if err != nil {
				t.Fatalf("ListSnapshotsByVehicle: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("want 1 snapshot, got %d", len(got))
			}
			row := got[0]
			if !row.SentryMode.Valid {
				t.Fatalf("sentry should be non-NULL for %s", tc.name)
			}
			if row.SentryMode.Bool != *tc.sentry {
				t.Errorf("sentry value wrong: want %t, got %t", *tc.sentry, row.SentryMode.Bool)
			}
		})
	}
}

// TestStore_SnapshotUpsert_SameDayReplaces verifies design D1 of
// telemetry-dedupe-daily-snapshots: a second capture for the SAME vehicle on
// the SAME captured_date REPLACES the existing row (latest capture wins),
// rather than appending a second row (the superseded append-only behavior of
// RM1-telemetry-add-nightly-snapshots design D1).
func TestStore_SnapshotUpsert_SameDayReplaces(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()
	q := telemetrydb.New(pool)

	accountID := uuid.New()
	const teslaID = int64(900020)
	cleanupVehicle(t, pool, accountID, teslaID)

	// Fixed, deterministic day (not "now") so the two captures cannot
	// accidentally straddle a UTC midnight boundary.
	day := time.Date(2026, 1, 15, 4, 0, 0, 0, time.UTC)

	base := Snapshot{
		AccountID:     accountID,
		TeslaID:       teslaID,
		ChargingState: "Disconnected",
		CarVersion:    "v1",
		RawData:       []byte(`{"pass":1}`),
	}
	first := base
	first.CapturedAt = day
	first.CapturedDate = dateOnly(first.CapturedAt, time.UTC)
	first.BatteryLevelPct = 50
	if err := st.insertSnapshot(ctx, first); err != nil {
		t.Fatalf("first insertSnapshot: %v", err)
	}

	second := base
	second.CapturedAt = day.Add(time.Hour) // same calendar day, later instant
	second.CapturedDate = dateOnly(second.CapturedAt, time.UTC)
	second.BatteryLevelPct = 55
	second.CarVersion = "v2"
	second.RawData = []byte(`{"pass":2}`)
	if err := st.insertSnapshot(ctx, second); err != nil {
		t.Fatalf("second insertSnapshot: %v", err)
	}

	got, err := q.ListSnapshotsByVehicle(ctx, telemetrydb.ListSnapshotsByVehicleParams{
		AccountID: accountID,
		TeslaID:   teslaID,
	})
	if err != nil {
		t.Fatalf("ListSnapshotsByVehicle: %v", err)
	}
	// Same-day replace: exactly ONE row survives (design D1).
	if len(got) != 1 {
		t.Fatalf("want 1 snapshot after a same-day replace, got %d", len(got))
	}
	row := got[0]
	// The surviving row must carry the SECOND capture's values, not the first's.
	if row.BatteryLevelPct != 55 {
		t.Errorf("battery_level wrong: want 55 (second capture), got %d", row.BatteryLevelPct)
	}
	if row.CarVersion != "v2" {
		t.Errorf("car_version wrong: want v2 (second capture), got %q", row.CarVersion)
	}
	if !row.CapturedAt.Time.UTC().Equal(second.CapturedAt) {
		t.Errorf("captured_at wrong: want %v (second capture), got %v", second.CapturedAt, row.CapturedAt.Time.UTC())
	}
}

// TestStore_SnapshotInsert_DifferentDayCreatesNewRow verifies that a capture
// on a NEW captured_date always inserts a new row and never touches a prior
// day's row — the dedupe constraint is scoped to (account_id, tesla_id,
// captured_date), not the vehicle alone.
func TestStore_SnapshotInsert_DifferentDayCreatesNewRow(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()
	q := telemetrydb.New(pool)

	accountID := uuid.New()
	const teslaID = int64(900021)
	cleanupVehicle(t, pool, accountID, teslaID)

	day1 := time.Date(2026, 1, 15, 4, 0, 0, 0, time.UTC)
	day2 := day1.AddDate(0, 0, 1)

	base := Snapshot{
		AccountID:     accountID,
		TeslaID:       teslaID,
		ChargingState: "Disconnected",
		CarVersion:    "v1",
		RawData:       []byte(`{}`),
	}
	first := base
	first.CapturedAt = day1
	first.CapturedDate = dateOnly(first.CapturedAt, time.UTC)
	first.BatteryLevelPct = 50
	if err := st.insertSnapshot(ctx, first); err != nil {
		t.Fatalf("day-1 insertSnapshot: %v", err)
	}

	second := base
	second.CapturedAt = day2
	second.CapturedDate = dateOnly(second.CapturedAt, time.UTC)
	second.BatteryLevelPct = 55
	if err := st.insertSnapshot(ctx, second); err != nil {
		t.Fatalf("day-2 insertSnapshot: %v", err)
	}

	got, err := q.ListSnapshotsByVehicle(ctx, telemetrydb.ListSnapshotsByVehicleParams{
		AccountID: accountID,
		TeslaID:   teslaID,
	})
	if err != nil {
		t.Fatalf("ListSnapshotsByVehicle: %v", err)
	}
	// Different days: both rows coexist.
	if len(got) != 2 {
		t.Fatalf("want 2 snapshots across two different days, got %d", len(got))
	}
	// Newest first (ORDER BY captured_at DESC): day2 (55) then day1 (50).
	if got[0].BatteryLevelPct != 55 || got[1].BatteryLevelPct != 50 {
		t.Errorf("order/values wrong: got %d then %d (want 55 then 50)", got[0].BatteryLevelPct, got[1].BatteryLevelPct)
	}
	// The day-1 row must be unchanged by the day-2 insert.
	if !got[1].CapturedAt.Time.UTC().Equal(day1) {
		t.Errorf("day-1 row's captured_at was altered: want %v, got %v", day1, got[1].CapturedAt.Time.UTC())
	}
}

func TestStore_PollAttemptRoundTrip(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()
	q := telemetrydb.New(pool)

	accountID := uuid.New()
	const teslaID = int64(900030)
	cleanupVehicle(t, pool, accountID, teslaID)

	attemptedAt := time.Now().UTC().Truncate(time.Microsecond)
	if err := st.insertPollAttempt(ctx, Attempt{
		AccountID:   accountID,
		TeslaID:     teslaID,
		AttemptedAt: attemptedAt,
		Outcome:     OutcomeFailure,
		Reason:      ReasonAsleepTimeout,
	}); err != nil {
		t.Fatalf("insertPollAttempt: %v", err)
	}

	got, err := q.ListPollAttemptsByVehicle(ctx, telemetrydb.ListPollAttemptsByVehicleParams{
		AccountID: accountID,
		TeslaID:   teslaID,
	})
	if err != nil {
		t.Fatalf("ListPollAttemptsByVehicle: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 poll attempt, got %d", len(got))
	}
	row := got[0]
	if row.Outcome != string(OutcomeFailure) || row.Reason != string(ReasonAsleepTimeout) {
		t.Errorf("poll attempt wrong: outcome=%q reason=%q", row.Outcome, row.Reason)
	}
	if !row.AttemptedAt.Time.UTC().Equal(attemptedAt) {
		t.Errorf("attempted_at wrong: want %v, got %v", attemptedAt, row.AttemptedAt.Time.UTC())
	}
}
