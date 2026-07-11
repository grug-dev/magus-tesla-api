package telemetry

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	telemetrydb "github.com/cristianpena/magus-tesla-api/internal/telemetry/db"
)

// These tests exercise the real telemetrydb store against a live Postgres from
// DATABASE_URL and self-skip when it is unset, so `go test ./...` stays green without a
// database (ai/go-conventions.md §persistence). They mirror the account module's
// DATABASE_URL-gated pattern. Requires the goose migration applied (`make migrate-up`).

// newTestStore builds a dbStore against a real Postgres from DATABASE_URL, skipping the
// test when it is unset. It returns the store plus the pool for direct-SQL assertions.
func newTestStore(t *testing.T) (*dbStore, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping telemetry store integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
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
		AccountID:      accountID,
		TeslaID:        teslaID,
		CapturedAt:     captured,
		BatteryLevel:   64,
		BatteryRange:   210.5,
		ChargingState:  "Disconnected",
		ChargeLimitSoc: 80,
		Odometer:       54321.75,
		InsideTemp:     21.5,
		OutsideTemp:    17.0,
		Locked:         true,
		SentryMode:     nil, // not reported → must round-trip as SQL NULL
		CarVersion:     "2026.20.1",
		Latitude:       40.7128,
		Longitude:      -74.0060,
		RawData:        []byte(`{"response":{"id":900001,"charge_state":{"battery_level":64}}}`),
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
	if row.BatteryLevel != 64 || row.ChargeLimitSoc != 80 {
		t.Errorf("integer columns wrong: battery=%d limit=%d", row.BatteryLevel, row.ChargeLimitSoc)
	}
	if row.BatteryRange != 210.5 || row.Odometer != 54321.75 {
		t.Errorf("float columns wrong: range=%v odo=%v", row.BatteryRange, row.Odometer)
	}
	if row.ChargingState != "Disconnected" || row.CarVersion != "2026.20.1" || !row.Locked {
		t.Errorf("string/bool columns wrong: %+v", row)
	}
	if row.Latitude != 40.7128 || row.Longitude != -74.0060 {
		t.Errorf("lat/lng wrong: %v/%v", row.Latitude, row.Longitude)
	}
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

			snap := Snapshot{
				AccountID:     accountID,
				TeslaID:       tc.teslaID,
				CapturedAt:    time.Now().UTC(),
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

func TestStore_SnapshotAppendOnly(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()
	q := telemetrydb.New(pool)

	accountID := uuid.New()
	const teslaID = int64(900020)
	cleanupVehicle(t, pool, accountID, teslaID)

	base := Snapshot{
		AccountID:     accountID,
		TeslaID:       teslaID,
		ChargingState: "Disconnected",
		CarVersion:    "v1",
		RawData:       []byte(`{}`),
	}
	first := base
	first.CapturedAt = time.Now().UTC().Add(-time.Hour)
	first.BatteryLevel = 50
	if err := st.insertSnapshot(ctx, first); err != nil {
		t.Fatalf("first insertSnapshot: %v", err)
	}

	second := base
	second.CapturedAt = time.Now().UTC()
	second.BatteryLevel = 55
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
	// Append-only: a second insert adds a row and leaves the first intact.
	if len(got) != 2 {
		t.Fatalf("want 2 snapshots after a second insert, got %d", len(got))
	}
	// Newest first (ORDER BY captured_at DESC): battery levels 55 then 50.
	if got[0].BatteryLevel != 55 || got[1].BatteryLevel != 50 {
		t.Errorf("append-only order wrong: got %d then %d (want 55 then 50)", got[0].BatteryLevel, got[1].BatteryLevel)
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
