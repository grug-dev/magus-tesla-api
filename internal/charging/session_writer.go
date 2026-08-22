package charging

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	chargingdb "github.com/cristianpena/magus-tesla-api/internal/charging/db"
)

// sessionWriter is the concrete implementation of the SessionWriter port
// (design.md D6/D7, RM29-charging-add-charge-sessions). It is NOT part of the
// store interface service.go defines for Writer/Reader — mirroring
// analytics.gapWriter's precedent, a port tested via real DB-integration tests
// rather than the offline-fake-testable seam gets its own small concrete type
// talking directly to chargingdb.Queries. pgtype is confined to this file (and
// service.go) — see internal/charging/AGENTS.md §Allowed Imports.
type sessionWriter struct {
	pool *pgxpool.Pool
	q    *chargingdb.Queries
}

// newSessionWriter is the internal constructor called by the public
// NewSessionWriter in charging.go so the forward-declaration compiles before
// this file is parsed (mirroring newWriter/newReader's identical pattern in
// service.go, and gapWriter's newGapWriter).
func newSessionWriter(pool *pgxpool.Pool) *sessionWriter {
	return &sessionWriter{pool: pool, q: chargingdb.New(pool)}
}

// Compile-time assertion: *sessionWriter must satisfy the public SessionWriter
// interface.
var _ SessionWriter = (*sessionWriter)(nil)

// MirrorSessions implements SessionWriter. See the interface doc comment
// (charging.go) for the full contract. Implementation shape (design.md D6/D7,
// task 2.2), validate-then-transact in this order:
//
//  1. Validate every entry's AccountID against accountID BEFORE tx.Begin — a
//     single mis-scoped entry rejects the WHOLE call with no partial write,
//     never a tx.Begin followed by a rollback (Test Contract B6, mirroring
//     GapWriter.ReconcileWindow's all-or-nothing contract).
//  2. Return nil immediately for an empty/nil slice, before opening a
//     transaction (Test Contract B7).
//  3. One transaction, one MirrorChargeSession call per entry, commit-or-rollback.
func (w *sessionWriter) MirrorSessions(ctx context.Context, accountID uuid.UUID, sessions []SessionMirror) error {
	for _, s := range sessions {
		if s.AccountID != accountID {
			return fmt.Errorf("charging: session %d has account %s, does not match call scope (account %s)", s.SessionID, s.AccountID, accountID)
		}
	}

	if len(sessions) == 0 {
		return nil
	}

	// Mirrors internal/account's own Begin/WithTx/Commit pattern
	// (account/service.go's AccessTokenFor) and analytics.gapWriter's identical
	// shape — the other transactional writes in this codebase.
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("charging: beginning tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once committed

	qtx := w.q.WithTx(tx)

	for _, s := range sessions {
		if err := qtx.MirrorChargeSession(ctx, chargingdb.MirrorChargeSessionParams{
			AccountID:           s.AccountID,
			Vin:                 s.VIN,
			TeslaID:             int64PtrToPgInt8(s.TeslaID),
			SessionID:           s.SessionID,
			ChargeStartDateTime: pgtype.Timestamptz{Time: s.ChargeStartDateTime, Valid: true},
			ChargeStopDateTime:  pgtype.Timestamptz{Time: s.ChargeStopDateTime, Valid: true},
			SiteLocationName:    s.SiteLocationName,
			EnergyKwh:           float64PtrToPgFloat8(s.EnergyKWh),
			TotalCost:           float64PtrToPgFloat8(s.TotalCost),
			Currency:            stringPtrToPgText(s.Currency),
			IsPaid:              boolPtrToPgBool(s.IsPaid),
		}); err != nil {
			return fmt.Errorf("charging: mirroring session %d: %w", s.SessionID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("charging: committing tx: %w", err)
	}
	return nil
}

// int64PtrToPgInt8 maps a *int64 to a nullable pgtype.Int8 (BIGINT). Follows the
// existing intPtrToPgInt2 naming/shape (service.go); no reverse
// (pgInt8ToInt64Ptr) pair exists because SessionMirror is write-only — this
// module exposes no reader for charge_sessions (design.md D9).
func int64PtrToPgInt8(v *int64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{Valid: false}
	}
	return pgtype.Int8{Int64: *v, Valid: true}
}

// float64PtrToPgFloat8 maps a *float64 to a nullable pgtype.Float8 (DOUBLE
// PRECISION). Follows the existing intPtrToPgInt2 naming/shape (service.go); no
// reverse pair exists — see int64PtrToPgInt8.
func float64PtrToPgFloat8(v *float64) pgtype.Float8 {
	if v == nil {
		return pgtype.Float8{Valid: false}
	}
	return pgtype.Float8{Float64: *v, Valid: true}
}

// boolPtrToPgBool maps a *bool to a nullable pgtype.Bool. Follows the existing
// intPtrToPgInt2 naming/shape (service.go); no reverse pair exists — see
// int64PtrToPgInt8.
func boolPtrToPgBool(v *bool) pgtype.Bool {
	if v == nil {
		return pgtype.Bool{Valid: false}
	}
	return pgtype.Bool{Bool: *v, Valid: true}
}
