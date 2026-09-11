package charging

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	chargingdb "github.com/cristianpena/magus-tesla-api/internal/charging/db"
)

// sessionReader is the concrete implementation of the SessionReader port
// (RM30-charging-add-session-read-port design.md D7). It mirrors sessionWriter's exact
// shape: NOT part of the store interface service.go defines for Writer/Reader —
// SessionReader is tested only via real TEST_DATABASE_URL-gated integration tests (no fake),
// so it is a small unexported struct talking directly to chargingdb.Queries. pgtype is
// confined to this file (and service.go, session_writer.go) — see
// internal/charging/AGENTS.md §Allowed Imports.
type sessionReader struct {
	pool *pgxpool.Pool
	q    *chargingdb.Queries
}

// newSessionReader is the internal constructor called by the public NewSessionReader in
// charging.go so the forward-declaration compiles before this file is parsed (mirroring
// newSessionWriter's identical pattern). pool is retained for shape-parity with
// sessionWriter and future methods that might need a transaction; this tier's single
// method uses only q.
func newSessionReader(pool *pgxpool.Pool) *sessionReader {
	return &sessionReader{pool: pool, q: chargingdb.New(pool)}
}

// Compile-time assertion: *sessionReader must satisfy the public SessionReader
// interface.
var _ SessionReader = (*sessionReader)(nil)

// ListSessionsByVehicleBetween implements SessionReader. See the interface doc comment
// (charging.go) for the full contract. from/to are whole UTC calendar days, to
// inclusive of its entire day; endBound is computed HERE, in Go — never in SQL
// (design.md D5) — exactly mirroring
// telemetry.SuperchargerSessionsByVehicleBetween's own end-bound translation.
func (r *sessionReader) ListSessionsByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, from, to time.Time) ([]Session, error) {
	endBound := to.AddDate(0, 0, 1)

	rows, err := r.q.ListSessionsByVehicleBetween(ctx, chargingdb.ListSessionsByVehicleBetweenParams{
		AccountID: accountID,
		TeslaID:   teslaIDToPgInt8(teslaID),
		FromTime:  pgtype.Timestamptz{Time: from, Valid: true},
		EndBound:  pgtype.Timestamptz{Time: endBound, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("charging: list sessions by vehicle between: %w", err)
	}

	sessions := make([]Session, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, rowToSession(row))
	}
	return sessions, nil
}

// ListSessionsByVehicleUpdatedSince implements SessionReader. See the interface doc
// comment (charging.go) for the full contract. since is used as-is — no endBound
// translation, unlike ListSessionsByVehicleBetween (design.md D1).
func (r *sessionReader) ListSessionsByVehicleUpdatedSince(ctx context.Context, accountID uuid.UUID, teslaID int64, since time.Time) ([]Session, error) {
	rows, err := r.q.ListSessionsByVehicleUpdatedSince(ctx, chargingdb.ListSessionsByVehicleUpdatedSinceParams{
		AccountID: accountID,
		TeslaID:   teslaIDToPgInt8(teslaID),
		Since:     pgtype.Timestamptz{Time: since, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("charging: list sessions by vehicle updated since: %w", err)
	}

	sessions := make([]Session, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, rowToSession(row))
	}
	return sessions, nil
}

// ListSessionsByVehicle implements SessionReader. See the interface doc comment
// (charging.go) for the full contract. The limit <= 0 clamp runs in Go, before the query
// is issued (design.md D3), reusing the module's existing defaultLimit constant
// (service.go) — no new constant.
func (r *sessionReader) ListSessionsByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]Session, error) {
	if limit <= 0 {
		limit = defaultLimit
	}

	rows, err := r.q.ListSessionsByVehicle(ctx, chargingdb.ListSessionsByVehicleParams{
		AccountID:  accountID,
		TeslaID:    teslaIDToPgInt8(teslaID),
		LimitCount: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("charging: list sessions by vehicle: %w", err)
	}

	sessions := make([]Session, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, rowToSession(row))
	}
	return sessions, nil
}

// teslaIDToPgInt8 maps a plain int64 vehicle id to a valid pgtype.Int8 filter param.
// Named and shaped after telemetry.teslaIDToPgInt8 (internal/telemetry/reader.go) — the
// identical situation: a NOT NULL Go parameter filtering a nullable BIGINT column
// (design.md D6).
func teslaIDToPgInt8(teslaID int64) pgtype.Int8 {
	return pgtype.Int8{Int64: teslaID, Valid: true}
}

// rowToSession converts a generated chargingdb.SuperchargerSession row into the domain
// Session type. This is the DB→domain mapping boundary for supercharger_sessions reads: all
// pgtype conversions are confined here so pgtype never appears in public types, method
// signatures, or tests (ai/go-conventions.md §persistence).
//
// Mapping rules:
//   - ID, AccountID, Vin, SessionID, SiteLocationName: direct (non-nullable,
//     value-compatible).
//   - TeslaID: pgtype.Int8 → *int64 via pgInt8ToInt64Ptr (session_writer.go).
//   - ChargeStartDateTime, ChargeStopDateTime, CreatedAt, UpdatedAt: pgtype.Timestamptz
//     → time.Time via .Time (required, non-null).
//   - EnergyKwh, TotalCost: pgtype.Float8 → *float64 via pgFloat8ToFloat64Ptr
//     (session_writer.go).
//   - Currency: pgtype.Text → *string via pgTextToPtr (service.go).
//   - IsPaid: pgtype.Bool → *bool via pgBoolToBoolPtr (session_writer.go).
//   - StartBatteryPct, EndBatteryPct: pgtype.Int2 → *int via pgInt2ToIntPtr (service.go).
//   - BatteryPctSource: pgtype.Text → *string via pgTextToPtr (service.go).
//   - InferredCapacityKwhCalc: pgtype.Numeric → *float64 via pgNumericToFloat64Ptr
//     (service.go) — nullable GENERATED ALWAYS AS ... STORED column (MAG-25
//     design D8).
//   - Status: string (NOT NULL TEXT) → SessionStatus via a direct type conversion —
//     no pgtype involved, the same shape Vin/SiteLocationName already use for their
//     own NOT NULL columns.
func rowToSession(r chargingdb.SuperchargerSession) Session {
	return Session{
		ID:        r.ID,
		AccountID: r.AccountID,
		VIN:       r.Vin,
		TeslaID:   pgInt8ToInt64Ptr(r.TeslaID),
		SessionID: r.SessionID,

		ChargeStartDateTime: r.ChargeStartDateTime.Time,
		ChargeStopDateTime:  r.ChargeStopDateTime.Time,

		SiteLocationName: r.SiteLocationName,
		EnergyKWh:        pgFloat8ToFloat64Ptr(r.EnergyKwh),
		TotalCost:        pgFloat8ToFloat64Ptr(r.TotalCost),
		Currency:         pgTextToPtr(r.Currency),
		IsPaid:           pgBoolToBoolPtr(r.IsPaid),

		StartBatteryPct:  pgInt2ToIntPtr(r.StartBatteryPct),
		EndBatteryPct:    pgInt2ToIntPtr(r.EndBatteryPct),
		BatteryPctSource: pgTextToPtr(r.BatteryPctSource),

		Status: SessionStatus(r.Status),

		CreatedAt: r.CreatedAt.Time,
		UpdatedAt: r.UpdatedAt.Time,

		InferredCapacityKWhCalc: pgNumericToFloat64Ptr(r.InferredCapacityKwhCalc),
	}
}
