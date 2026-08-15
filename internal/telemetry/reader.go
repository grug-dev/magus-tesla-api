package telemetry

import (
	"context"
	"math"
	"time"

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

// SnapshotsByVehicleSince implements Reader. It returns all snapshots captured for the
// given vehicle (within the given account) at or after since, ordered oldest-first.
// Returns a non-nil empty slice (never nil) when no snapshots exist in the window
// (parity with LatestSnapshotsByAccount's empty contract — callers range safely).
// Reuses the store.snapshotsByVehicleSince seam so it is fully offline-testable via a
// fake store, mirroring the LatestSnapshotsByAccount pattern (design D3/D7 of
// telemetry-add-snapshot-read-port).
func (r *reader) SnapshotsByVehicleSince(ctx context.Context, accountID uuid.UUID, teslaID int64, since time.Time) ([]Snapshot, error) {
	return r.store.snapshotsByVehicleSince(ctx, accountID, teslaID, since)
}

// SnapshotsByVehicleBetween implements Reader. It returns the snapshots for the given
// vehicle (within the given account) whose EffectiveDate calendar day falls in the
// caller-supplied `[start, end]` window inclusive, ordered oldest-first (ascending by
// EffectiveDate == ascending by captured_at). Returns a non-nil empty slice (never nil)
// when no snapshots exist in the window (parity with SnapshotsByVehicleSince /
// LatestSnapshotsByAccount — no nil-slice footgun for callers).
//
// This pass-through does NOT translate `(start, end)` into captured_at bounds — that
// `+1day`/`+2day` translation is the dbStore implementation's job (design D5), so the
// public port stays a clean bounded window and the offset math stays in one testable
// place (service.go's dbStore.snapshotsByVehicleBetween). The reader forwards the
// caller's raw `(accountID, teslaID, start, end)` to the store seam unchanged, mirroring
// SnapshotsByVehicleSince's pattern (design D3/D7 of telemetry-add-snapshot-read-port).
// The compile-time `var _ Reader = (*reader)(nil)` assertion above pins this new
// interface method.
func (r *reader) SnapshotsByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]Snapshot, error) {
	return r.store.snapshotsByVehicleBetween(ctx, accountID, teslaID, start, end)
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

// SuperchargerSessionsByVehicleBetween implements SuperchargerReader. It returns
// Supercharger sessions for the given vehicle (within the given account) whose
// ChargeStopDateTime falls in the caller-supplied [start, end] window, inclusive
// of the whole end calendar day, ordered oldest-first (RM28-telemetry-add-
// charge-gap-storage, roadmap D9/D12). endBound = end + 1 calendar day
// (Go-side day arithmetic, mirroring dbStore.snapshotsByVehicleBetween's own
// precedent of doing bounds translation in Go, not SQL) makes the underlying
// SQL's half-open `>= start AND < endBound` include every instant of the end
// calendar day with no off-by-one. Reuses the existing rowToSuperchargerSession
// mapper (mapping.go) — SuperchargerSession gains no new field for this query, so
// no new mapping helper is needed. Returns a non-nil empty slice when no
// sessions exist in the window (design DBS6 parity).
func (r *superchargerReader) SuperchargerSessionsByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]SuperchargerSession, error) {
	endBound := end.AddDate(0, 0, 1)
	rows, err := r.q.SuperchargerSessionsByVehicleBetween(ctx, telemetrydb.SuperchargerSessionsByVehicleBetweenParams{
		AccountID: accountID,
		TeslaID:   teslaIDToPgInt8(teslaID),
		Start:     timestamptzFrom(start),
		EndBound:  timestamptzFrom(endBound),
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
