package analytics

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	analyticsdb "github.com/cristianpena/magus-tesla-api/internal/analytics/db"
)

// gapWriter is the concrete implementation of the GapWriter port
// (RM28-telemetry-add-charge-gap-storage, design D3/D7/D7a/D7b; relocated
// here by RM29-analytics-own-charge-gaps). It is NOT part of the store
// interface reader.go/recalculate.go define for Reader/Recalculator --
// SuperchargerReader already established the precedent that a port not
// shaped by that offline-fake-testable seam (because it is tested via real
// DB-integration tests instead) gets its own small concrete type talking
// directly to analyticsdb.Queries. pgtype never appears in this file: every
// value it touches flows through the existing dateFrom and dateFromPg
// helpers (mapping.go), both already typed in terms of time.Time at the
// call site.
type gapWriter struct {
	pool *pgxpool.Pool
	q    *analyticsdb.Queries
}

// newGapWriter is the internal constructor called by the public NewGapWriter
// in analytics.go so the forward-declaration compiles before this file is
// parsed (mirroring newSuperchargerReaderImpl's identical pattern, design
// B6.3 of RM27-telemetry-add-supercharger-battery-pct).
func newGapWriter(pool *pgxpool.Pool) *gapWriter {
	return &gapWriter{pool: pool, q: analyticsdb.New(pool)}
}

// Compile-time assertion: *gapWriter must satisfy the public GapWriter interface.
var _ GapWriter = (*gapWriter)(nil)

// ReconcileWindow implements GapWriter. See the interface doc comment
// (analytics.go) for the full contract. Implementation shape (design.md's
// "Go-Level Seam Summary"):
//
//  1. Validate every element of flagged against (accountID, teslaID, start,
//     end) BEFORE opening a transaction — a caller bug rejects the whole call
//     with no partial write, never a tx.Begin followed by a rollback.
//  2. Load every existing charge_gaps date for (accountID, teslaID) in
//     [start, end] (ChargeGapDatesByVehicleBetween) and diff it in Go against
//     the caller's flagged set: any existing date NOT in flagged is deleted.
//  3. Upsert every element of flagged (UpsertChargeGap; the UNIQUE constraint
//     makes this idempotent — a still-flagged day is refreshed in place, not
//     duplicated).
//  4. Commit. A failure at any step rolls back the whole transaction, so a
//     ReconcileWindow call either fully applies or has no effect (design D7b).
//
// Deliberately a read-then-diff-then-write loop, not a single array-bound
// DELETE ... WHERE gap_date <> ALL(@keep::date[]): this codebase has no
// existing precedent for binding a Postgres array parameter through
// sqlc/pgx, and the window this loop ever runs over is small by construction
// (bounded, roadmap-wide, to roughly 90 calendar days) — design.md's Risks
// section accepts the extra round-trip as negligible under this project's
// read-heavy/write-light-and-off-hours Performance-Profile.
func (w *gapWriter) ReconcileWindow(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time, flagged []ChargeGap) error {
	for _, g := range flagged {
		if g.AccountID != accountID || g.TeslaID != teslaID {
			return fmt.Errorf("charge gap for account %s vehicle %d does not match call scope (account %s vehicle %d)", g.AccountID, g.TeslaID, accountID, teslaID)
		}
		if g.Date.Before(start) || g.Date.After(end) {
			return fmt.Errorf("charge gap date %s falls outside window [%s, %s]", g.Date, start, end)
		}
	}

	// Mirrors internal/account's own Begin/WithTx/Commit pattern
	// (account/service.go's AccessTokenFor) -- the only other transactional
	// write in this codebase.
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once committed

	qtx := w.q.WithTx(tx)

	existing, err := qtx.ChargeGapDatesByVehicleBetween(ctx, analyticsdb.ChargeGapDatesByVehicleBetweenParams{
		AccountID: accountID,
		TeslaID:   teslaID,
		Start:     dateFrom(start),
		EndDate:   dateFrom(end),
	})
	if err != nil {
		return fmt.Errorf("loading existing charge gaps: %w", err)
	}

	keep := make(map[time.Time]bool, len(flagged))
	for _, g := range flagged {
		keep[g.Date] = true
	}

	for _, d := range existing {
		if t := dateFromPg(d); !keep[t] {
			if err := qtx.DeleteChargeGap(ctx, analyticsdb.DeleteChargeGapParams{
				AccountID: accountID,
				TeslaID:   teslaID,
				GapDate:   d,
			}); err != nil {
				return fmt.Errorf("deleting resolved charge gap: %w", err)
			}
		}
	}

	for _, g := range flagged {
		if err := qtx.UpsertChargeGap(ctx, analyticsdb.UpsertChargeGapParams{
			AccountID:           accountID,
			TeslaID:             teslaID,
			Vin:                 g.VIN,
			GapDate:             dateFrom(g.Date),
			MissingChargingType: string(g.MissingChargingType),
		}); err != nil {
			return fmt.Errorf("upserting charge gap: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing tx: %w", err)
	}
	return nil
}
