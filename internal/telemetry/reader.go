package telemetry

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// SnapshotsByVehicleUpdatedSince implements Reader. It returns every snapshot for
// the given vehicle (within the given account) whose UpdatedAt is at or after
// since, ordered oldest-first by updated_at. Returns a non-nil empty slice (never
// nil) when no snapshot has been updated in the window (parity with every other
// Reader method's empty-result contract). Reuses the store.snapshotsByVehicleUpdatedSince
// seam so it is fully offline-testable via a fake store, mirroring
// SnapshotsByVehicleSince's pattern (RM29-analytics-add-vehicle-metrics task 1.2).
func (r *reader) SnapshotsByVehicleUpdatedSince(ctx context.Context, accountID uuid.UUID, teslaID int64, since time.Time) ([]Snapshot, error) {
	return r.store.snapshotsByVehicleUpdatedSince(ctx, accountID, teslaID, since)
}

// SnapshotPrecedingDay implements Reader. It delegates to the store seam so it
// stays offline-testable via a fake store, mirroring every other method on this
// type. See the interface doc comment (telemetry.go) for the full contract —
// absent-predecessor vs. error semantics, the zone-safety rationale for bounding
// on captured_date rather than captured_at, and the unbounded-lookback guarantee.
// (RM29-telemetry-drop-derived-columns design D2, wave 1.)
func (r *reader) SnapshotPrecedingDay(ctx context.Context, accountID uuid.UUID, teslaID int64, day time.Time) (*Snapshot, error) {
	return r.store.snapshotPrecedingDay(ctx, accountID, teslaID, day)
}

// snapshotPrecedingDay implements the store seam design D2 of
// RM29-telemetry-drop-derived-columns relies on: it calls the SnapshotPrecedingDay
// query generated by sqlc and maps pgx.ErrNoRows to (nil, nil) — "no predecessor
// exists" is NOT an error. Any other query error is returned as-is; the caller
// (internal/analytics' Recalculate, via Reader.SnapshotPrecedingDay) treats it as
// a transient store failure — NEVER as "no predecessor" — so a real DB error
// cannot silently zero out a real vehicle's derived figures. A found row is
// mapped via the existing shared rowToSnapshot mapper (mapping.go) — no new
// mapper. `day` is bound via the existing dateFrom helper (service.go):
// captured_date is a DATE column, so the parameter is pgtype.Date, not
// pgtype.Timestamptz. This is the module's sole predecessor lookup: the former
// dbStore.previousSnapshot (instant-bounded) was deleted in tier 4 (design D8)
// once this calendar-day-bounded method took over as its only caller's need.
func (d *dbStore) snapshotPrecedingDay(ctx context.Context, accountID uuid.UUID, teslaID int64, day time.Time) (*Snapshot, error) {
	row, err := d.q.SnapshotPrecedingDay(ctx, telemetrydb.SnapshotPrecedingDayParams{
		AccountID: accountID,
		TeslaID:   teslaID,
		Day:       dateFrom(day),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	snap := rowToSnapshot(row)
	return &snap, nil
}

// --- Source B: SuperchargerReader ---

// superchargerReader is the concrete implementation of the SuperchargerReader port.
// It queries the supercharger_history table via the generated telemetrydb package.
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
func (r *superchargerReader) SuperchargerSessionsByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]SuperchargerHistory, error) {
	rows, err := r.q.SuperchargerHistoryByAccount(ctx, telemetrydb.SuperchargerHistoryByAccountParams{
		AccountID:  accountID,
		LimitCount: resolveLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	sessions := make([]SuperchargerHistory, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, rowToSuperchargerSession(row))
	}
	return sessions, nil
}

// SuperchargerSessionsByVehicle implements SuperchargerReader. It returns Supercharger
// sessions for the given vehicle within the given account, ordered by
// charge_start_date_time DESC, limited to limit rows (0 = math.MaxInt32). Returns a
// non-nil empty slice when no sessions exist (design DBS6).
func (r *superchargerReader) SuperchargerSessionsByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]SuperchargerHistory, error) {
	rows, err := r.q.SuperchargerHistoryByVehicle(ctx, telemetrydb.SuperchargerHistoryByVehicleParams{
		AccountID:  accountID,
		TeslaID:    teslaIDToPgInt8(teslaID),
		LimitCount: resolveLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	sessions := make([]SuperchargerHistory, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, rowToSuperchargerSession(row))
	}
	return sessions, nil
}

// SuperchargerSessionsByVehicleUpdatedSince implements SuperchargerReader. It
// returns every Supercharger session for the given vehicle (within the given
// account) whose updated_at is at or after since, ordered oldest-first by
// updated_at (RM29-analytics-add-vehicle-metrics task 1.3). Reuses the existing
// rowToSuperchargerSession mapper (mapping.go) — no new field, no new mapper.
// Returns a non-nil empty slice when no sessions have been updated in the
// window (design DBS6 parity with every other method on this interface).
func (r *superchargerReader) SuperchargerSessionsByVehicleUpdatedSince(ctx context.Context, accountID uuid.UUID, teslaID int64, since time.Time) ([]SuperchargerHistory, error) {
	rows, err := r.q.SuperchargerHistoryByVehicleUpdatedSince(ctx, telemetrydb.SuperchargerHistoryByVehicleUpdatedSinceParams{
		AccountID: accountID,
		TeslaID:   teslaIDToPgInt8(teslaID),
		Since:     timestamptzFrom(since),
	})
	if err != nil {
		return nil, err
	}
	sessions := make([]SuperchargerHistory, 0, len(rows))
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
// mapper (mapping.go) — SuperchargerHistory gains no new field for this query, so
// no new mapping helper is needed. Returns a non-nil empty slice when no
// sessions exist in the window (design DBS6 parity).
func (r *superchargerReader) SuperchargerSessionsByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]SuperchargerHistory, error) {
	endBound := end.AddDate(0, 0, 1)
	rows, err := r.q.SuperchargerHistoryByVehicleBetween(ctx, telemetrydb.SuperchargerHistoryByVehicleBetweenParams{
		AccountID: accountID,
		TeslaID:   teslaIDToPgInt8(teslaID),
		Start:     timestamptzFrom(start),
		EndBound:  timestamptzFrom(endBound),
	})
	if err != nil {
		return nil, err
	}
	sessions := make([]SuperchargerHistory, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, rowToSuperchargerSession(row))
	}
	return sessions, nil
}
