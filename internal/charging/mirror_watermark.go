package charging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	chargingdb "github.com/cristianpena/magus-tesla-api/internal/charging/db"
)

// mirrorWatermarkStore is the concrete implementation of the
// MirrorWatermarkStore port (design.md "Interfaces",
// RM44-platform-add-mirror-watermark, MAG-48). Mirrors session_writer.go's
// exact pattern: a small concrete type talking directly to chargingdb.Queries,
// not part of service.go's Writer/Reader store. pgtype stays confined to this
// file, per internal/charging/AGENTS.md's pgtype-boundary rule.
type mirrorWatermarkStore struct {
	pool *pgxpool.Pool
	q    *chargingdb.Queries
}

// newMirrorWatermarkStore is the internal constructor called by the public
// NewMirrorWatermarkStore in charging.go so the forward-declaration compiles
// before this file is parsed (mirroring newSessionWriter's identical pattern
// in session_writer.go).
func newMirrorWatermarkStore(pool *pgxpool.Pool) *mirrorWatermarkStore {
	return &mirrorWatermarkStore{pool: pool, q: chargingdb.New(pool)}
}

// Compile-time assertion: *mirrorWatermarkStore must satisfy the public
// MirrorWatermarkStore interface.
var _ MirrorWatermarkStore = (*mirrorWatermarkStore)(nil)

// MirrorWatermark implements MirrorWatermarkStore. See the interface doc
// comment (charging.go) for the full contract. Translates pgx.ErrNoRows to
// (time.Time{}, nil) -- design.md D3, copying
// analytics.recalculator.watermark's identical translation rather than
// re-deriving it.
func (s *mirrorWatermarkStore) MirrorWatermark(ctx context.Context, teslaID int64) (time.Time, error) {
	ts, err := s.q.GetMirrorWatermark(ctx, teslaID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("charging: reading mirror watermark for vehicle %d: %w", teslaID, err)
	}
	return ts.Time, nil
}

// AdvanceMirrorWatermark implements MirrorWatermarkStore. See the interface
// doc comment (charging.go) for the full contract. A plain upsert with no
// row-count validation -- design.md D5: the empty-read guard (never call this
// when the bounded telemetry read returned zero rows) lives in the caller
// (internal/app.processChargingData), not here, mirroring
// analytics.recalculator.advanceWatermark's identical division of labor.
func (s *mirrorWatermarkStore) AdvanceMirrorWatermark(ctx context.Context, teslaID int64, observed time.Time) error {
	if err := s.q.UpsertMirrorWatermark(ctx, chargingdb.UpsertMirrorWatermarkParams{
		TeslaID:         teslaID,
		SourceUpdatedAt: pgtype.Timestamptz{Time: observed, Valid: true},
	}); err != nil {
		return fmt.Errorf("charging: advancing mirror watermark for vehicle %d: %w", teslaID, err)
	}
	return nil
}
