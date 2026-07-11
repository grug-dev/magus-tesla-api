package telemetry

import (
	"context"

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
