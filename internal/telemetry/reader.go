package telemetry

import (
	"context"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	telemetrydb "github.com/cristianpena/magus-tesla-api/internal/telemetry/db"
)

// reader is the concrete implementation of the Reader port. It delegates the
// database call through the unexported store seam so it is fully offline-testable
// via a fake store without a real Postgres connection (design D3 / D7 of
// telemetry-add-snapshot-read-port). pgtype never appears in this file — all
// pgtype→domain conversions happen inside the dbStore implementation in service.go.
type reader struct {
	store store
}

// Compile-time assertion: *reader must satisfy the public Reader interface.
var _ Reader = (*reader)(nil)

// NewReader constructs a Reader backed by a real Postgres pool. It wires a
// telemetrydb-backed dbStore and returns the Reader interface — callers (the gateway,
// cmd/) depend on the interface, never on the concrete *reader. Mirrors how NewService
// builds its dbStore (service.go).
func NewReader(pool *pgxpool.Pool) Reader {
	return &reader{
		store: &dbStore{q: telemetrydb.New(pool)},
	}
}

// LatestSnapshotsByAccount implements Reader. It returns the most-recently captured
// Snapshot for each vehicle belonging to the given account. It returns a non-nil empty
// slice (never nil) when the account has no snapshots, so callers can range over the
// result safely (design D5).
func (r *reader) LatestSnapshotsByAccount(ctx context.Context, accountID uuid.UUID) ([]Snapshot, error) {
	return r.store.latestSnapshotsByAccount(ctx, accountID)
}

// --- Source B: SuperchargerReader ---

// superchargerReader is the concrete implementation of the SuperchargerReader port.
// It queries the supercharger_sessions table via the generated telemetrydb package.
// pgtype never appears in this type or its method return values — all pgtype→domain
// conversions happen in rowToSuperchargerSession (mapping.go).
type superchargerReader struct {
	q *telemetrydb.Queries
}

// Compile-time assertion: *superchargerReader must satisfy SuperchargerReader.
var _ SuperchargerReader = (*superchargerReader)(nil)

// newSuperchargerReaderImpl is the internal constructor called by the public
// NewSuperchargerReader in telemetry.go so the forward-declaration compiles
// before this file is parsed (design B6.3).
func newSuperchargerReaderImpl(pool *pgxpool.Pool) *superchargerReader {
	return &superchargerReader{q: telemetrydb.New(pool)}
}

// resolveLimit converts a caller-supplied limit (0 = no limit) to a concrete int32
// for the sqlc query. When limit is 0 we pass math.MaxInt32 as a practical "no limit"
// — large enough for any account's session history and avoids a separate SQL branch.
func resolveLimit(limit int) int32 {
	if limit <= 0 {
		return math.MaxInt32
	}
	return int32(limit)
}

// SuperchargerSessionsByAccount implements SuperchargerReader. It returns all
// Supercharger sessions for the given account, ordered by charge_start_date_time DESC,
// limited to limit rows (0 = math.MaxInt32). Returns a non-nil empty slice when no
// sessions exist (design DBS6 — non-nil so callers can safely range over the result).
func (r *superchargerReader) SuperchargerSessionsByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]SuperchargerSession, error) {
	rows, err := r.q.SuperchargerSessionsByAccount(ctx, telemetrydb.SuperchargerSessionsByAccountParams{
		AccountID:  accountID,
		LimitCount: resolveLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	sessions := make([]SuperchargerSession, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, rowToSuperchargerSession(row))
	}
	return sessions, nil
}

// SuperchargerSessionsByVehicle implements SuperchargerReader. It returns Supercharger
// sessions for the given vehicle within the given account, ordered by
// charge_start_date_time DESC, limited to limit rows (0 = math.MaxInt32). Returns a
// non-nil empty slice when no sessions exist (design DBS6).
func (r *superchargerReader) SuperchargerSessionsByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]SuperchargerSession, error) {
	rows, err := r.q.SuperchargerSessionsByVehicle(ctx, telemetrydb.SuperchargerSessionsByVehicleParams{
		AccountID:  accountID,
		TeslaID:    teslaIDToPgInt8(teslaID),
		LimitCount: resolveLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	sessions := make([]SuperchargerSession, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, rowToSuperchargerSession(row))
	}
	return sessions, nil
}
