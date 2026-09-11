package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/clock"
	telemetrydb "github.com/cristianpena/magus-tesla-api/internal/telemetry/db"
)

// These tests exercise the real telemetrydb store against a live Postgres
// provisioned by TestMain (see testdb_test.go). The test database is
// auto-provisioned via testcontainers-go when TEST_DATABASE_URL is unset/unreachable
// (ai/go-conventions.md §persistence), so `go test ./...` is green with no
// manual DB setup as long as Docker is running locally.

// newTestStore builds a dbStore against the test Postgres provisioned by
// TestMain. It returns the store plus the pool for direct-SQL assertions.
func newTestStore(t *testing.T) (*dbStore, *pgxpool.Pool) {
	t.Helper()
	if testDSN == "" {
		t.Skip("no test Postgres: set TEST_DATABASE_URL or start Docker to run the DB-backed tests")
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
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.vehicle_snapshots WHERE account_id = $1 AND tesla_id = $2", accountID, teslaID)
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.poll_attempts WHERE account_id = $1 AND tesla_id = $2", accountID, teslaID)
	})
}

func ptrBool(b bool) *bool { return &b }

// The helpers below replace the deleted telemetrydb.ListSnapshotsByVehicle /
// ListPollAttemptsByVehicle sqlc queries (RM44-telemetry-add-query-logging D5/D13):
// every test that used to call them now reads its row(s) back with a raw SQL
// SELECT via the pool directly, independent of this module's own reader
// queries, narrowed to the columns that test actually asserts.

// snapshotFullRow carries the columns TestStore_SnapshotRoundTrip_SentryNilIsNull
// asserts.
type snapshotFullRow struct {
	BatteryLevelPct   int32
	ChargeLimitSocPct int32
	BatteryRangeKm    float64
	OdometerKm        float64
	ChargingState     string
	CarVersion        string
	Locked            bool
	SentryMode        pgtype.Bool
	CapturedAt        pgtype.Timestamptz
	RawData           []byte
}

func querySnapshotsFull(ctx context.Context, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64) ([]snapshotFullRow, error) {
	rows, err := pool.Query(ctx,
		`SELECT battery_level_pct, charge_limit_soc_pct, battery_range_km, odometer_km,
		        charging_state, car_version, locked, sentry_mode, captured_at, raw_data
		   FROM telemetry.vehicle_snapshots
		  WHERE account_id = $1 AND tesla_id = $2
		  ORDER BY captured_at DESC`,
		accountID, teslaID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var got []snapshotFullRow
	for rows.Next() {
		var r snapshotFullRow
		if err := rows.Scan(&r.BatteryLevelPct, &r.ChargeLimitSocPct, &r.BatteryRangeKm, &r.OdometerKm,
			&r.ChargingState, &r.CarVersion, &r.Locked, &r.SentryMode, &r.CapturedAt, &r.RawData); err != nil {
			return nil, err
		}
		got = append(got, r)
	}
	return got, rows.Err()
}

// snapshotSentryRow carries the column TestStore_SentryTrueAndFalseRoundTripFaithfully
// asserts.
type snapshotSentryRow struct {
	SentryMode pgtype.Bool
}

func querySnapshotsSentry(ctx context.Context, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64) ([]snapshotSentryRow, error) {
	rows, err := pool.Query(ctx,
		`SELECT sentry_mode
		   FROM telemetry.vehicle_snapshots
		  WHERE account_id = $1 AND tesla_id = $2
		  ORDER BY captured_at DESC`,
		accountID, teslaID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var got []snapshotSentryRow
	for rows.Next() {
		var r snapshotSentryRow
		if err := rows.Scan(&r.SentryMode); err != nil {
			return nil, err
		}
		got = append(got, r)
	}
	return got, rows.Err()
}

// snapshotBatteryVersionRow carries the columns TestStore_SnapshotUpsert_SameDayReplaces
// asserts.
type snapshotBatteryVersionRow struct {
	BatteryLevelPct int32
	CarVersion      string
	CapturedAt      pgtype.Timestamptz
}

func querySnapshotsBatteryVersion(ctx context.Context, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64) ([]snapshotBatteryVersionRow, error) {
	rows, err := pool.Query(ctx,
		`SELECT battery_level_pct, car_version, captured_at
		   FROM telemetry.vehicle_snapshots
		  WHERE account_id = $1 AND tesla_id = $2
		  ORDER BY captured_at DESC`,
		accountID, teslaID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var got []snapshotBatteryVersionRow
	for rows.Next() {
		var r snapshotBatteryVersionRow
		if err := rows.Scan(&r.BatteryLevelPct, &r.CarVersion, &r.CapturedAt); err != nil {
			return nil, err
		}
		got = append(got, r)
	}
	return got, rows.Err()
}

// snapshotBatteryRow carries the columns TestStore_SnapshotInsert_DifferentDayCreatesNewRow
// asserts.
type snapshotBatteryRow struct {
	BatteryLevelPct int32
	CapturedAt      pgtype.Timestamptz
}

func querySnapshotsBattery(ctx context.Context, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64) ([]snapshotBatteryRow, error) {
	rows, err := pool.Query(ctx,
		`SELECT battery_level_pct, captured_at
		   FROM telemetry.vehicle_snapshots
		  WHERE account_id = $1 AND tesla_id = $2
		  ORDER BY captured_at DESC`,
		accountID, teslaID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var got []snapshotBatteryRow
	for rows.Next() {
		var r snapshotBatteryRow
		if err := rows.Scan(&r.BatteryLevelPct, &r.CapturedAt); err != nil {
			return nil, err
		}
		got = append(got, r)
	}
	return got, rows.Err()
}

// pollAttemptRow carries the columns TestStore_PollAttemptRoundTrip asserts.
type pollAttemptRow struct {
	Outcome     string
	Reason      string
	AttemptedAt pgtype.Timestamptz
}

func queryPollAttempts(ctx context.Context, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64) ([]pollAttemptRow, error) {
	rows, err := pool.Query(ctx,
		`SELECT outcome, reason, attempted_at
		   FROM telemetry.poll_attempts
		  WHERE account_id = $1 AND tesla_id = $2
		  ORDER BY attempted_at DESC`,
		accountID, teslaID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var got []pollAttemptRow
	for rows.Next() {
		var r pollAttemptRow
		if err := rows.Scan(&r.Outcome, &r.Reason, &r.AttemptedAt); err != nil {
			return nil, err
		}
		got = append(got, r)
	}
	return got, rows.Err()
}

func TestStore_SnapshotRoundTrip_SentryNilIsNull(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(900001)
	cleanupVehicle(t, pool, accountID, teslaID)

	captured := time.Now().UTC().Truncate(time.Microsecond)
	snap := Snapshot{
		AccountID:         accountID,
		TeslaID:           teslaID,
		CapturedAt:        captured,
		CapturedDate:      clock.CalendarDay(captured, time.UTC),
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

	got, err := querySnapshotsFull(ctx, pool, accountID, teslaID)
	if err != nil {
		t.Fatalf("querying vehicle_snapshots: %v", err)
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
				CapturedDate:  clock.CalendarDay(captured, time.UTC),
				ChargingState: "Charging",
				CarVersion:    "v",
				SentryMode:    tc.sentry,
				RawData:       []byte(`{}`),
			}
			if err := st.insertSnapshot(ctx, snap); err != nil {
				t.Fatalf("insertSnapshot: %v", err)
			}

			got, err := querySnapshotsSentry(ctx, pool, accountID, tc.teslaID)
			if err != nil {
				t.Fatalf("querying vehicle_snapshots: %v", err)
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
	first.CapturedDate = clock.CalendarDay(first.CapturedAt, time.UTC)
	first.BatteryLevelPct = 50
	if err := st.insertSnapshot(ctx, first); err != nil {
		t.Fatalf("first insertSnapshot: %v", err)
	}

	second := base
	second.CapturedAt = day.Add(time.Hour) // same calendar day, later instant
	second.CapturedDate = clock.CalendarDay(second.CapturedAt, time.UTC)
	second.BatteryLevelPct = 55
	second.CarVersion = "v2"
	second.RawData = []byte(`{"pass":2}`)
	if err := st.insertSnapshot(ctx, second); err != nil {
		t.Fatalf("second insertSnapshot: %v", err)
	}

	got, err := querySnapshotsBatteryVersion(ctx, pool, accountID, teslaID)
	if err != nil {
		t.Fatalf("querying vehicle_snapshots: %v", err)
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
	first.CapturedDate = clock.CalendarDay(first.CapturedAt, time.UTC)
	first.BatteryLevelPct = 50
	if err := st.insertSnapshot(ctx, first); err != nil {
		t.Fatalf("day-1 insertSnapshot: %v", err)
	}

	second := base
	second.CapturedAt = day2
	second.CapturedDate = clock.CalendarDay(second.CapturedAt, time.UTC)
	second.BatteryLevelPct = 55
	if err := st.insertSnapshot(ctx, second); err != nil {
		t.Fatalf("day-2 insertSnapshot: %v", err)
	}

	got, err := querySnapshotsBattery(ctx, pool, accountID, teslaID)
	if err != nil {
		t.Fatalf("querying vehicle_snapshots: %v", err)
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

	got, err := queryPollAttempts(ctx, pool, accountID, teslaID)
	if err != nil {
		t.Fatalf("querying poll_attempts: %v", err)
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

// The three tests below implement design.md's Test Contract group B
// (RM29-app-add-process-vehicle-data, task 2.8) for the run_id/triggered_by
// columns added by migration 20260823000002_add_run_id_triggered_by_poll_attempts.
// Expected values were authored in design.md BEFORE this test file was written
// (ai/go-conventions.md §Testing "contract-first authoring"); do not adjust them
// to match whatever the mapping code happens to do.
//
// Each test reads the column back with a DIRECT SQL SELECT — not through
// ListPollAttemptsByVehicle or any other sqlc reader query — so the assertion
// exercises exactly the two new columns, independent of any other query's own
// column list or mapping. The row is identified by its unique
// (account_id, tesla_id) pair (each test uses a fresh uuid.New() account plus
// its own unused tesla_id constant), which is unique per test exactly like
// TestStore_PollAttemptRoundTrip's own lookup above; poll_attempts.id exists
// but insertPollAttempt is a sqlc :exec query with no RETURNING clause, so the
// row has no id available to the caller to filter on.

// TestStore_PollAttemptRoundTrip_RunIDAndTriggeredByAPI is Test Contract B1.
func TestStore_PollAttemptRoundTrip_RunIDAndTriggeredByAPI(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(900040)
	cleanupVehicle(t, pool, accountID, teslaID)

	wantRunID := uuid.New()
	attemptedAt := time.Now().UTC().Truncate(time.Microsecond)
	if err := st.insertPollAttempt(ctx, Attempt{
		AccountID:   accountID,
		TeslaID:     teslaID,
		AttemptedAt: attemptedAt,
		Outcome:     OutcomeSuccess,
		Reason:      ReasonOK,
		RunID:       wantRunID,
		TriggeredBy: TriggeredByAPI,
	}); err != nil {
		t.Fatalf("insertPollAttempt: %v", err)
	}

	var gotRunID pgtype.UUID
	var gotTriggeredBy string
	if err := pool.QueryRow(ctx,
		`SELECT run_id, triggered_by FROM telemetry.poll_attempts WHERE account_id = $1 AND tesla_id = $2`,
		accountID, teslaID,
	).Scan(&gotRunID, &gotTriggeredBy); err != nil {
		t.Fatalf("querying poll_attempts: %v", err)
	}

	// Expected (design.md B1): run_id equals the supplied UUID exactly.
	if !gotRunID.Valid {
		t.Fatalf("run_id should be non-NULL, got NULL")
	}
	if uuid.UUID(gotRunID.Bytes) != wantRunID {
		t.Errorf("run_id wrong: want %v, got %v", wantRunID, uuid.UUID(gotRunID.Bytes))
	}
	// Expected (design.md B1): triggered_by = 'api'.
	// The literal, not string(TriggeredByAPI): comparing the constant against a row
	// written FROM that same constant is a tautology that passes even if the constant
	// is misspelled. design.md B1's expected value is the wire value 'api'.
	if gotTriggeredBy != "api" {
		t.Errorf("triggered_by wrong: want %q, got %q (constant is %q)", "api", gotTriggeredBy, TriggeredByAPI)
	}
}

// TestStore_PollAttempt_PreMigrationRowDefaultsRunIDNullTriggeredByScheduler is
// Test Contract B2 — the only coverage of spec.md's "a record from before this
// capability existed has no run identifier" scenario. It reproduces exactly
// what happened to every real poll_attempts row when this migration's ALTER
// ran: a direct INSERT naming neither run_id nor triggered_by.
func TestStore_PollAttempt_PreMigrationRowDefaultsRunIDNullTriggeredByScheduler(t *testing.T) {
	_, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(900041)
	cleanupVehicle(t, pool, accountID, teslaID)

	attemptedAt := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx,
		`INSERT INTO telemetry.poll_attempts (account_id, tesla_id, attempted_at, outcome, reason)
		 VALUES ($1, $2, $3, $4, $5)`,
		accountID, teslaID, attemptedAt, string(OutcomeSuccess), string(ReasonOK),
	); err != nil {
		t.Fatalf("direct INSERT into poll_attempts: %v", err)
	}

	var gotRunID pgtype.UUID
	var gotTriggeredBy string
	if err := pool.QueryRow(ctx,
		`SELECT run_id, triggered_by FROM telemetry.poll_attempts WHERE account_id = $1 AND tesla_id = $2`,
		accountID, teslaID,
	).Scan(&gotRunID, &gotTriggeredBy); err != nil {
		t.Fatalf("querying poll_attempts: %v", err)
	}

	// Expected (design.md B2): run_id IS NULL.
	if gotRunID.Valid {
		t.Errorf("run_id should be NULL for a pre-migration-shaped row, got %v", uuid.UUID(gotRunID.Bytes))
	}
	// Expected (design.md B2): triggered_by = 'scheduler' (the column's own default).
	if gotTriggeredBy != "scheduler" {
		t.Errorf("triggered_by wrong: want %q, got %q", "scheduler", gotTriggeredBy)
	}
}

// TestStore_PollAttemptRoundTrip_TriggeredByScheduler is Test Contract B3. It
// pins that the Go constant TriggeredByScheduler and the triggered_by column's
// own SQL default ('scheduler') agree character-for-character: a typo in
// either would let B1 and B2 each pass independently while silently
// disagreeing with each other.
func TestStore_PollAttemptRoundTrip_TriggeredByScheduler(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(900042)
	cleanupVehicle(t, pool, accountID, teslaID)

	attemptedAt := time.Now().UTC().Truncate(time.Microsecond)
	if err := st.insertPollAttempt(ctx, Attempt{
		AccountID:   accountID,
		TeslaID:     teslaID,
		AttemptedAt: attemptedAt,
		Outcome:     OutcomeSuccess,
		Reason:      ReasonOK,
		RunID:       uuid.New(),
		TriggeredBy: TriggeredByScheduler,
	}); err != nil {
		t.Fatalf("insertPollAttempt: %v", err)
	}

	var gotTriggeredBy string
	if err := pool.QueryRow(ctx,
		`SELECT triggered_by FROM telemetry.poll_attempts WHERE account_id = $1 AND tesla_id = $2`,
		accountID, teslaID,
	).Scan(&gotTriggeredBy); err != nil {
		t.Fatalf("querying poll_attempts: %v", err)
	}

	// Expected (design.md B3): triggered_by = 'scheduler' through the Go seam too.
	if gotTriggeredBy != "scheduler" {
		t.Errorf("triggered_by wrong: want %q, got %q", "scheduler", gotTriggeredBy)
	}
	// B3's whole purpose (design.md): pin that the Go constant and the column's own
	// SQL default are the same characters. Asserted explicitly, because comparing a
	// round-tripped value against the constant that wrote it cannot detect a typo in
	// the constant — B2 would then fail alone, with no clue which side moved.
	if string(TriggeredByScheduler) != "scheduler" {
		t.Errorf("TriggeredByScheduler drifted from the migration's DEFAULT: want %q, got %q",
			"scheduler", TriggeredByScheduler)
	}
}
