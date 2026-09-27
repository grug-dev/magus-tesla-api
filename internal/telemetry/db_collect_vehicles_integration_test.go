package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	telemetrydb "github.com/cristianpena/magus-tesla-api/internal/telemetry/db"
)

// These tests exercise the new "retry" TriggeredBy value and CollectVehicles
// against a real Postgres provisioned by TestMain (see testdb_test.go),
// mirroring the shape of this package's other db_*_integration_test.go
// files (testdb.Provision, real telemetrydb.Queries).

// TestTriggeredByRetry_RoundTripsWithNoCheckViolation checks: the
// plain-text triggered_by column on both poll_attempts and poll_runs accepts
// the new "retry" value with no migration and no CHECK violation.
func TestTriggeredByRetry_RoundTripsWithNoCheckViolation(t *testing.T) {
	_, pool := newTestStore(t)
	ctx := context.Background()
	q := telemetrydb.New(pool)

	teslaID := int64(800001)
	runID := uuid.New()
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.poll_attempts WHERE tesla_id = $1", teslaID)
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.poll_runs WHERE run_id = $1", runID)
	})

	err := q.InsertPollAttempt(ctx, telemetrydb.InsertPollAttemptParams{
		PolledByAccountID: uuid.New(),
		TeslaID:           teslaID,
		AttemptedAt:       timestamptzFrom(time.Now()),
		Outcome:           string(OutcomeSuccess),
		Reason:            string(ReasonOK),
		RunID:             runIDToPgUUID(runID),
		TriggeredBy:       string(TriggeredByRetry),
	})
	if err != nil {
		t.Fatalf("InsertPollAttempt with triggered_by=retry: want nil error, got %v", err)
	}

	var gotAttemptTrigger string
	if err := pool.QueryRow(ctx,
		"SELECT triggered_by FROM telemetry.poll_attempts WHERE tesla_id = $1", teslaID,
	).Scan(&gotAttemptTrigger); err != nil {
		t.Fatalf("SELECT poll_attempts.triggered_by: %v", err)
	}
	if gotAttemptTrigger != "retry" {
		t.Errorf("poll_attempts.triggered_by: want %q, got %q", "retry", gotAttemptTrigger)
	}

	runWriter := newRunWriter(pool)
	fixedT0 := time.Date(2026, 9, 1, 3, 30, 0, 0, time.UTC)
	if err := runWriter.RecordRun(ctx, PollRun{
		RunID:       runID,
		TriggeredBy: TriggeredByRetry,
		StartedAt:   fixedT0,
		FinishedAt:  fixedT0.Add(5 * time.Second),
	}); err != nil {
		t.Fatalf("RecordRun with TriggeredBy=retry: want nil error, got %v", err)
	}

	var gotRunTrigger string
	if err := pool.QueryRow(ctx,
		"SELECT triggered_by FROM telemetry.poll_runs WHERE run_id = $1", runID,
	).Scan(&gotRunTrigger); err != nil {
		t.Fatalf("SELECT poll_runs.triggered_by: %v", err)
	}
	if gotRunTrigger != "retry" {
		t.Errorf("poll_runs.triggered_by: want %q, got %q", "retry", gotRunTrigger)
	}
}

// TestCollectVehicles_EndToEnd_RealStore checks: CollectVehicles
// against the real dbStore (not the offline fake), with a fake account.Service
// and a fake tesla.VehicleService (never a live Fleet API call, per this
// module's standing "never wake a car in a test" rule). It proves the
// retried vehicle gets exactly one poll_attempts row stamped triggered_by =
// 'retry' and exactly one vehicle_snapshots row, while the account's other,
// not-retried vehicle gets neither.
func TestCollectVehicles_EndToEnd_RealStore(t *testing.T) {
	store, pool := newTestStore(t)
	ctx := context.Background()

	acctID := uuid.New()
	retriedID := int64(800101)
	otherID := int64(800102)
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.vehicle_snapshots WHERE tesla_id IN ($1, $2)", retriedID, otherID)
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.poll_attempts WHERE tesla_id IN ($1, $2)", retriedID, otherID)
	})

	ft := newFakeTesla()
	ft.set(retriedID, &vehicleScript{state: "online", data: onlineData(retriedID, nil)})
	ft.set(otherID, &vehicleScript{state: "online", data: onlineData(otherID, nil)})

	fa := &fakeAccount{
		vehicles: []account.OwnedVehicle{
			{AccountID: acctID, TeslaID: retriedID, VIN: "RETRY-VIN"},
			{AccountID: acctID, TeslaID: otherID, VIN: "OTHER-VIN"},
		},
		tokens: map[uuid.UUID]string{acctID: "tok"},
	}

	svc := &service{
		acct:  fa,
		tsla:  ft,
		store: store,
		cfg: Config{
			WakeTimeout: 50 * time.Millisecond,
			Clock:       func() time.Time { return time.Now() },
		},
		retryBackoff: 0,
	}

	run := RunContext{RunID: uuid.New(), TriggeredBy: TriggeredByRetry}
	_, err := svc.CollectVehicles(ctx, run, []int64{retriedID})
	if err != nil {
		t.Fatalf("CollectVehicles: want nil error, got %v", err)
	}

	// Exactly one poll_attempts row for this run, and it is stamped retry.
	var attemptTeslaID int64
	var attemptTrigger string
	err = pool.QueryRow(ctx,
		"SELECT tesla_id, triggered_by FROM telemetry.poll_attempts WHERE run_id = $1",
		run.RunID,
	).Scan(&attemptTeslaID, &attemptTrigger)
	if err != nil {
		t.Fatalf("SELECT poll_attempts for run %s: %v", run.RunID, err)
	}
	if attemptTeslaID != retriedID {
		t.Errorf("poll_attempts.tesla_id: want %d, got %d", retriedID, attemptTeslaID)
	}
	if attemptTrigger != "retry" {
		t.Errorf("poll_attempts.triggered_by: want %q, got %q", "retry", attemptTrigger)
	}
	var attemptCount int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM telemetry.poll_attempts WHERE run_id = $1", run.RunID,
	).Scan(&attemptCount); err != nil {
		t.Fatalf("counting poll_attempts for run %s: %v", run.RunID, err)
	}
	if attemptCount != 1 {
		t.Errorf("want exactly 1 poll_attempts row for this run, got %d", attemptCount)
	}

	// Exactly one vehicle_snapshots row for the retried vehicle, none for the other.
	var snapshotCount int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM telemetry.vehicle_snapshots WHERE tesla_id = $1", retriedID,
	).Scan(&snapshotCount); err != nil {
		t.Fatalf("counting vehicle_snapshots for %d: %v", retriedID, err)
	}
	if snapshotCount != 1 {
		t.Errorf("want exactly 1 vehicle_snapshots row for the retried vehicle, got %d", snapshotCount)
	}
	var otherSnapshotCount int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM telemetry.vehicle_snapshots WHERE tesla_id = $1", otherID,
	).Scan(&otherSnapshotCount); err != nil {
		t.Fatalf("counting vehicle_snapshots for %d: %v", otherID, err)
	}
	if otherSnapshotCount != 0 {
		t.Errorf("want zero vehicle_snapshots rows for the not-retried vehicle, got %d", otherSnapshotCount)
	}

	// Never called with a live tesla.VehicleService — the fake proves it.
	if ft.wakeCalls[otherID] != 0 || ft.dataCalls[otherID] != 0 {
		t.Errorf("the not-retried vehicle must receive zero Tesla calls, got wakes=%d data=%d", ft.wakeCalls[otherID], ft.dataCalls[otherID])
	}
}
