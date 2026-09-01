package telemetry

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	telemetrydb "github.com/cristianpena/magus-tesla-api/internal/telemetry/db"
)

// runWriter is the concrete implementation of the RunWriter port
// (RM36-telemetry-add-poll-runs design D12). It is NOT part of the store
// interface reader.go/service.go define for Reader/Collector — mirroring
// SuperchargerReader's and internal/analytics' gapWriter's identical precedent
// for a port proven by a DATABASE_URL-gated integration test rather than an
// offline fake. pgtype never appears in this file: run_id binds as a plain
// uuid.UUID (poll_runs.run_id is NOT NULL, so sqlc's uuid override maps it
// straight to uuid.UUID rather than pgtype.UUID — no runIDToPgUUID wrap
// needed, unlike the nullable poll_attempts.run_id column); started_at/
// finished_at convert via the existing timestamptzFrom helper (service.go);
// every other field binds as a plain non-nullable value since every PollRun
// field is always populated by contract (design D3).
type runWriter struct {
	pool *pgxpool.Pool
	q    *telemetrydb.Queries
}

// newRunWriter is the internal constructor called by the public NewRunWriter in
// telemetry.go so the forward-declaration compiles before this file is parsed
// (mirroring newSuperchargerReaderImpl's and newGapWriter's identical pattern).
func newRunWriter(pool *pgxpool.Pool) *runWriter {
	return &runWriter{pool: pool, q: telemetrydb.New(pool)}
}

// Compile-time assertion: *runWriter must satisfy the public RunWriter interface.
var _ RunWriter = (*runWriter)(nil)

// RecordRun implements RunWriter. See the interface doc comment (telemetry.go)
// for the full contract: a plain single INSERT, never an upsert (design D11) —
// a duplicate run.RunID surfaces as a PRIMARY KEY uniqueness-violation error from
// Postgres, which this method returns as-is rather than absorbing.
func (w *runWriter) RecordRun(ctx context.Context, run PollRun) error {
	return w.q.InsertPollRun(ctx, telemetrydb.InsertPollRunParams{
		RunID:                    run.RunID,
		TriggeredBy:              string(run.TriggeredBy),
		StartedAt:                timestamptzFrom(run.StartedAt),
		FinishedAt:               timestamptzFrom(run.FinishedAt),
		DurationSeconds:          run.DurationSeconds,
		AccountsAttempted:        int32(run.AccountsAttempted),
		AccountsSucceeded:        int32(run.AccountsSucceeded),
		AccountsFailed:           int32(run.AccountsFailed),
		VehiclesAttempted:        int32(run.VehiclesAttempted),
		VehiclesSucceeded:        int32(run.VehiclesSucceeded),
		FailuresAsleepTimeout:    int32(run.FailuresAsleepTimeout),
		FailuresUnauthorized:     int32(run.FailuresUnauthorized),
		FailuresApiError:         int32(run.FailuresAPIError),
		TeslaApiCalls:            int32(run.TeslaAPICalls),
		ChargingSessionsUpserted: int32(run.ChargingSessionsUpserted),
		ChargingFetchFailures:    int32(run.ChargingFetchFailures),
		ConfigCaptureFailures:    int32(run.ConfigCaptureFailures),
	})
}
