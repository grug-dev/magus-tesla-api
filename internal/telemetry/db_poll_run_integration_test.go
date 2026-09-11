package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests exercise RunWriter.RecordRun against a real Postgres poll_runs table
// (RM36-telemetry-add-poll-runs, design D1-D5/D11/D12). They implement the test
// contract authored in this change's design.md BEFORE the implementation existed
// (§"Test Contract", Fixture 5a/5b/5c). TEST_DATABASE_URL-gated via the package's existing
// testdb/TestMain pattern (see testdb_test.go): newTestStore skips every test in this
// file when no Postgres is reachable and no Docker daemon can provision one.
//
// There is no Reader-style method for poll_runs yet (design D5/backlog), so every
// assertion below reads the row back via a direct SQL SELECT, exactly as design.md's
// Fixture 5a specifies.

// pollRunRow mirrors poll_runs' 17 columns for the direct-SQL round-trip assertions
// below — a test-only scanning target, not a public type.
type pollRunRow struct {
	runID                    uuid.UUID
	triggeredBy              string
	startedAt                time.Time
	finishedAt               time.Time
	durationSeconds          float64
	accountsAttempted        int32
	accountsSucceeded        int32
	accountsFailed           int32
	vehiclesAttempted        int32
	vehiclesSucceeded        int32
	failuresAsleepTimeout    int32
	failuresUnauthorized     int32
	failuresAPIError         int32
	teslaAPICalls            int32
	chargingSessionsUpserted int32
	chargingFetchFailures    int32
	configCaptureFailures    int32
}

// fetchPollRun reads one poll_runs row by run_id via direct SQL — there is no
// Reader method for this table in this tier (design D5/backlog).
func fetchPollRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID uuid.UUID) (pollRunRow, error) {
	t.Helper()
	var r pollRunRow
	err := pool.QueryRow(ctx, `
		SELECT run_id, triggered_by, started_at, finished_at, duration_seconds,
		       accounts_attempted, accounts_succeeded, accounts_failed,
		       vehicles_attempted, vehicles_succeeded,
		       failures_asleep_timeout, failures_unauthorized, failures_api_error,
		       tesla_api_calls,
		       charging_sessions_upserted, charging_fetch_failures, config_capture_failures
		FROM telemetry.poll_runs WHERE run_id = $1`, runID).Scan(
		&r.runID, &r.triggeredBy, &r.startedAt, &r.finishedAt, &r.durationSeconds,
		&r.accountsAttempted, &r.accountsSucceeded, &r.accountsFailed,
		&r.vehiclesAttempted, &r.vehiclesSucceeded,
		&r.failuresAsleepTimeout, &r.failuresUnauthorized, &r.failuresAPIError,
		&r.teslaAPICalls,
		&r.chargingSessionsUpserted, &r.chargingFetchFailures, &r.configCaptureFailures,
	)
	return r, err
}

// countPollRuns returns how many rows exist for runID — used by Fixture 5c to prove
// a rejected duplicate INSERT left exactly one row behind.
func countPollRuns(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM telemetry.poll_runs WHERE run_id = $1`, runID).Scan(&n); err != nil {
		t.Fatalf("counting poll_runs rows for %s: %v", runID, err)
	}
	return n
}

// TestRunWriter_RecordRun_NormalSuccessfulRun implements design.md Fixture 5a: a
// normal successful run's full 17-field round-trip via direct SQL SELECT.
func TestRunWriter_RecordRun_NormalSuccessfulRun(t *testing.T) {
	_, pool := newTestStore(t)
	ctx := context.Background()
	w := newRunWriter(pool)

	runID := uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM telemetry.poll_runs WHERE run_id = $1", runID)
	})

	fixedT0 := time.Date(2026, 8, 30, 3, 30, 0, 0, time.UTC)
	run := PollRun{
		RunID:                    runID,
		TriggeredBy:              TriggeredByScheduler,
		StartedAt:                fixedT0,
		FinishedAt:               fixedT0.Add(42 * time.Second),
		DurationSeconds:          42.0,
		AccountsAttempted:        2,
		AccountsSucceeded:        1,
		AccountsFailed:           1,
		VehiclesAttempted:        3,
		VehiclesSucceeded:        1,
		FailuresAsleepTimeout:    0,
		FailuresUnauthorized:     2,
		FailuresAPIError:         0,
		TeslaAPICalls:            3,
		ChargingSessionsUpserted: 5,
		ChargingFetchFailures:    0,
		ConfigCaptureFailures:    0,
	}

	if err := w.RecordRun(ctx, run); err != nil {
		t.Fatalf("RecordRun: want nil error, got %v", err)
	}

	got, err := fetchPollRun(t, ctx, pool, runID)
	if err != nil {
		t.Fatalf("SELECT poll_runs: %v", err)
	}

	if got.runID != run.RunID {
		t.Errorf("run_id: want %v, got %v", run.RunID, got.runID)
	}
	if got.triggeredBy != string(run.TriggeredBy) {
		t.Errorf("triggered_by: want %q, got %q", run.TriggeredBy, got.triggeredBy)
	}
	if !got.startedAt.Equal(run.StartedAt) {
		t.Errorf("started_at: want %v, got %v", run.StartedAt, got.startedAt)
	}
	if !got.finishedAt.Equal(run.FinishedAt) {
		t.Errorf("finished_at: want %v, got %v", run.FinishedAt, got.finishedAt)
	}
	if got.durationSeconds != run.DurationSeconds {
		t.Errorf("duration_seconds: want %v, got %v", run.DurationSeconds, got.durationSeconds)
	}
	// finished_at - started_at consistent with duration_seconds, within tolerance.
	if wallSeconds := got.finishedAt.Sub(got.startedAt).Seconds(); wallSeconds != got.durationSeconds {
		t.Errorf("finished_at - started_at (%vs) must equal duration_seconds (%v)", wallSeconds, got.durationSeconds)
	}
	if int(got.accountsAttempted) != run.AccountsAttempted {
		t.Errorf("accounts_attempted: want %d, got %d", run.AccountsAttempted, got.accountsAttempted)
	}
	if int(got.accountsSucceeded) != run.AccountsSucceeded {
		t.Errorf("accounts_succeeded: want %d, got %d", run.AccountsSucceeded, got.accountsSucceeded)
	}
	if int(got.accountsFailed) != run.AccountsFailed {
		t.Errorf("accounts_failed: want %d, got %d", run.AccountsFailed, got.accountsFailed)
	}
	if int(got.vehiclesAttempted) != run.VehiclesAttempted {
		t.Errorf("vehicles_attempted: want %d, got %d", run.VehiclesAttempted, got.vehiclesAttempted)
	}
	if int(got.vehiclesSucceeded) != run.VehiclesSucceeded {
		t.Errorf("vehicles_succeeded: want %d, got %d", run.VehiclesSucceeded, got.vehiclesSucceeded)
	}
	if int(got.failuresAsleepTimeout) != run.FailuresAsleepTimeout {
		t.Errorf("failures_asleep_timeout: want %d, got %d", run.FailuresAsleepTimeout, got.failuresAsleepTimeout)
	}
	if int(got.failuresUnauthorized) != run.FailuresUnauthorized {
		t.Errorf("failures_unauthorized: want %d, got %d", run.FailuresUnauthorized, got.failuresUnauthorized)
	}
	if int(got.failuresAPIError) != run.FailuresAPIError {
		t.Errorf("failures_api_error: want %d, got %d", run.FailuresAPIError, got.failuresAPIError)
	}
	if int(got.teslaAPICalls) != run.TeslaAPICalls {
		t.Errorf("tesla_api_calls: want %d, got %d", run.TeslaAPICalls, got.teslaAPICalls)
	}
	if int(got.chargingSessionsUpserted) != run.ChargingSessionsUpserted {
		t.Errorf("charging_sessions_upserted: want %d, got %d", run.ChargingSessionsUpserted, got.chargingSessionsUpserted)
	}
	if int(got.chargingFetchFailures) != run.ChargingFetchFailures {
		t.Errorf("charging_fetch_failures: want %d, got %d", run.ChargingFetchFailures, got.chargingFetchFailures)
	}
	if int(got.configCaptureFailures) != run.ConfigCaptureFailures {
		t.Errorf("config_capture_failures: want %d, got %d", run.ConfigCaptureFailures, got.configCaptureFailures)
	}
}

// TestRunWriter_RecordRun_WholeCycleFailureTrace implements design.md Fixture 5b:
// the step-1 whole-cycle-failure trace (the case roadmap D1 exists for) — an
// all-zero-counts row that the NOT NULL schema (design D3) admits without
// complaint because finished_at/duration_seconds are still concretely known.
func TestRunWriter_RecordRun_WholeCycleFailureTrace(t *testing.T) {
	_, pool := newTestStore(t)
	ctx := context.Background()
	w := newRunWriter(pool)

	runID := uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM telemetry.poll_runs WHERE run_id = $1", runID)
	})

	fixedT0 := time.Date(2026, 8, 30, 3, 30, 0, 0, time.UTC)
	run := PollRun{
		RunID:             runID,
		TriggeredBy:       TriggeredByScheduler,
		StartedAt:         fixedT0,
		FinishedAt:        fixedT0.Add(150 * time.Millisecond),
		DurationSeconds:   0.15,
		AccountsAttempted: 0,
		AccountsSucceeded: 0,
		AccountsFailed:    0,
		VehiclesAttempted: 0,
		VehiclesSucceeded: 0,
		TeslaAPICalls:     0,
		// every other field left zero
	}

	if err := w.RecordRun(ctx, run); err != nil {
		t.Fatalf("RecordRun: want nil error for an all-zero-counts row, got %v", err)
	}

	got, err := fetchPollRun(t, ctx, pool, runID)
	if err != nil {
		t.Fatalf("SELECT poll_runs: %v", err)
	}
	if got.accountsAttempted != 0 || got.vehiclesAttempted != 0 || got.teslaAPICalls != 0 {
		t.Errorf("want an all-zero counts row, got %+v", got)
	}
	if !got.startedAt.Equal(run.StartedAt) || !got.finishedAt.Equal(run.FinishedAt) {
		t.Errorf("want started_at/finished_at to still be concretely recorded, got started=%v finished=%v", got.startedAt, got.finishedAt)
	}
	if got.durationSeconds != 0.15 {
		t.Errorf("duration_seconds: want 0.15, got %v", got.durationSeconds)
	}
}

// TestRunWriter_RecordRun_DuplicateRunIDFailsLoudly implements design.md Fixture
// 5c (design D11): a second RecordRun call for the same run_id is rejected as a
// PRIMARY KEY uniqueness violation, and the table still holds exactly the first
// call's row, unmodified.
func TestRunWriter_RecordRun_DuplicateRunIDFailsLoudly(t *testing.T) {
	_, pool := newTestStore(t)
	ctx := context.Background()
	w := newRunWriter(pool)

	runID := uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM telemetry.poll_runs WHERE run_id = $1", runID)
	})

	fixedT0 := time.Date(2026, 8, 30, 3, 30, 0, 0, time.UTC)
	first := PollRun{
		RunID:             runID,
		TriggeredBy:       TriggeredByScheduler,
		StartedAt:         fixedT0,
		FinishedAt:        fixedT0.Add(10 * time.Second),
		DurationSeconds:   10.0,
		AccountsAttempted: 1,
		VehiclesAttempted: 1,
		VehiclesSucceeded: 1,
		AccountsSucceeded: 1,
		TeslaAPICalls:     2,
	}
	if err := w.RecordRun(ctx, first); err != nil {
		t.Fatalf("first RecordRun: want nil error, got %v", err)
	}

	// Second call for the SAME run_id — fields deliberately differ to prove the
	// first call's values survive untouched.
	second := PollRun{
		RunID:             runID,
		TriggeredBy:       TriggeredByAPI,
		StartedAt:         fixedT0.Add(time.Hour),
		FinishedAt:        fixedT0.Add(time.Hour + 99*time.Second),
		DurationSeconds:   99.0,
		AccountsAttempted: 9,
		VehiclesAttempted: 9,
	}
	err := w.RecordRun(ctx, second)
	if err == nil {
		t.Fatal("second RecordRun for the same run_id: want a non-nil error, got nil")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("want a *pgconn.PgError (PRIMARY KEY uniqueness violation), got %T: %v", err, err)
	}
	if pgErr.Code != "23505" {
		t.Errorf("want SQLSTATE 23505 (unique_violation), got %q: %v", pgErr.Code, err)
	}

	if n := countPollRuns(t, ctx, pool, runID); n != 1 {
		t.Fatalf("want exactly 1 row for run_id %s after the rejected duplicate, got %d", runID, n)
	}

	got, err := fetchPollRun(t, ctx, pool, runID)
	if err != nil {
		t.Fatalf("SELECT poll_runs: %v", err)
	}
	if got.triggeredBy != string(first.TriggeredBy) {
		t.Errorf("triggered_by must still be the FIRST call's value %q, got %q", first.TriggeredBy, got.triggeredBy)
	}
	if !got.startedAt.Equal(first.StartedAt) {
		t.Errorf("started_at must still be the FIRST call's value %v, got %v", first.StartedAt, got.startedAt)
	}
	if got.durationSeconds != first.DurationSeconds {
		t.Errorf("duration_seconds must still be the FIRST call's value %v, got %v", first.DurationSeconds, got.durationSeconds)
	}
	if int(got.accountsAttempted) != first.AccountsAttempted || int(got.vehiclesAttempted) != first.VehiclesAttempted {
		t.Errorf("accounts_attempted/vehicles_attempted must still be the FIRST call's values, got %+v", got)
	}
}
