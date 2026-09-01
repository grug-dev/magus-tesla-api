package charging

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	chargingdb "github.com/cristianpena/magus-tesla-api/internal/charging/db"
)

// batteryPctSourceUserVerified is the single literal this port ever writes into
// battery_pct_source. Declared once, used in the single branch VerifySession computes
// it in (design.md D10) — the same "closed, small vocabulary" reasoning D9 invokes,
// applied at the string-literal level: a single named constant is what makes "this port
// can never write anything but user_verified" a property with exactly one place to
// verify, rather than a claim that happens to be true today because nobody has typed the
// literal wrong yet.
const batteryPctSourceUserVerified = "user_verified"

// sessionVerifier is the concrete implementation of the SessionVerifier port
// (RM31-charging-add-session-verification-port design.md D9). It mirrors
// sessionWriter's/sessionReader's exact shape: NOT part of the store interface
// service.go defines for Writer/Reader — SessionVerifier is tested only via real
// DATABASE_URL-gated integration tests (no fake), so it is a small unexported struct
// talking directly to chargingdb.Queries. pgtype never appears in this file — the two
// existing helpers it reuses (intPtrToPgInt2, stringPtrToPgText, service.go) confine
// pgtype to the DB boundary, as this module's Allowed Imports rule requires.
type sessionVerifier struct {
	pool *pgxpool.Pool
	q    *chargingdb.Queries
}

// newSessionVerifier is the internal constructor called by the public
// NewSessionVerifier in charging.go so the forward-declaration compiles before this
// file is parsed (mirroring newSessionWriter's/newSessionReader's identical pattern).
func newSessionVerifier(pool *pgxpool.Pool) *sessionVerifier {
	return &sessionVerifier{pool: pool, q: chargingdb.New(pool)}
}

// Compile-time assertion: *sessionVerifier must satisfy the public SessionVerifier
// interface.
var _ SessionVerifier = (*sessionVerifier)(nil)

// needsDerivedStartBatteryPct reports whether VerifySession must derive
// start_battery_pct rather than write the caller's value as-is (MAG-36,
// charging-add-derived-start-battery-pct design.md D2, conditions 1+2 of the four —
// the row-energy and in-range conditions are decided separately, by
// derivedStartBatteryPct, once the row is read). It is true iff the caller's
// startPct is nil AND the caller's endPct is non-nil.
//
// This is deliberately a separate, named, offline-testable function rather than
// folded into derivedStartBatteryPct (design.md D8): derivedStartBatteryPct computes
// a start percentage, so it has no natural way to represent "the caller already
// supplied one, don't touch it" — folding this check in would force that function to
// also accept startPct, a value it does not otherwise need. Splitting the gate out
// here is what makes "a caller-supplied start is never recomputed" (design.md D2)
// assertable with a single, cheap, pure function call, before any database row is
// read — no context.Context, no query, no transaction.
func needsDerivedStartBatteryPct(startPct, endPct *int) bool {
	return startPct == nil && endPct != nil
}

// VerifySession implements SessionVerifier. See the interface doc comment (charging.go)
// for the full contract. Implementation shape is validate-then-derive-then-query
// (design.md §"Go-side call shape", D8/D9, MAG-36
// charging-add-derived-start-battery-pct):
//
//  1. Range-validate each non-nil percentage against [0, 100] BEFORE any database call
//     (design.md D3) — the DB's own SMALLINT CHECK is the backstop, not the error
//     message.
//  2. If needsDerivedStartBatteryPct(startBatteryPct, endBatteryPct) is true (the
//     caller left start nil and supplied end): open a transaction, lock the row via
//     the new LockSessionForVerification query (FOR UPDATE — design.md D9), resolve
//     the vehicle's pack capacity via packCapacityKWh, and compute the start percentage
//     to store via derivedStartBatteryPct. Otherwise the value to store is exactly the
//     caller's startBatteryPct, unchanged — today's single-statement, non-transactional
//     path, untouched in cost or shape (design.md D9).
//  3. Compute battery_pct_source: batteryPctSourceUserVerified when either the
//     (possibly derived) start or the end percentage is non-nil, nil (SQL NULL) when
//     both are nil (design.md D1/D2/D7 — unchanged: no new source value).
//  4. Call VerifyChargeSession — against the open transaction when one exists,
//     against v.q otherwise — scoped by id + accountID, and map the returned row via
//     the existing rowToSession (session_reader.go) — no new mapping code. Commit the
//     transaction, when one was opened, only after VerifyChargeSession succeeds.
func (v *sessionVerifier) VerifySession(ctx context.Context, accountID uuid.UUID, id uuid.UUID, startBatteryPct, endBatteryPct *int) (Session, error) {
	if startBatteryPct != nil && (*startBatteryPct < 0 || *startBatteryPct > 100) {
		return Session{}, fmt.Errorf("charging: start_battery_pct %d out of range [0,100]", *startBatteryPct)
	}
	if endBatteryPct != nil && (*endBatteryPct < 0 || *endBatteryPct > 100) {
		return Session{}, fmt.Errorf("charging: end_battery_pct %d out of range [0,100]", *endBatteryPct)
	}

	startToStore := startBatteryPct
	q := v.q
	var tx pgx.Tx

	if needsDerivedStartBatteryPct(startBatteryPct, endBatteryPct) {
		// design.md D9: the row is locked FOR UPDATE for the remainder of this
		// transaction, mirroring internal/account's AccessTokenFor and this
		// module's own SessionWriter.MirrorSessions, so a concurrent
		// SessionWriter.MirrorSessions refresh of energy_kwh cannot land between
		// this read and the write below and leave the derived percentage
		// computed from a value the row no longer holds.
		var err error
		tx, err = v.pool.Begin(ctx)
		if err != nil {
			return Session{}, fmt.Errorf("charging: beginning tx: %w", err)
		}
		defer tx.Rollback(ctx) // no-op once committed

		qtx := v.q.WithTx(tx)
		q = qtx

		row, err := qtx.LockSessionForVerification(ctx, chargingdb.LockSessionForVerificationParams{
			ID:        id,
			AccountID: accountID,
		})
		if err != nil {
			// design.md D10: identical wrap to VerifyChargeSession's own
			// not-found case — a caller of VerifySession cannot observe, and
			// must not need to care, which of the two internal queries
			// produced a given not-found error.
			return Session{}, fmt.Errorf("charging: verify session: %w", err)
		}

		capacityKWh, err := packCapacityKWh(ctx, row.Vin)
		if err != nil {
			return Session{}, fmt.Errorf("charging: resolving pack capacity: %w", err)
		}

		startToStore = derivedStartBatteryPct(capacityKWh, pgFloat8ToFloat64Ptr(row.EnergyKwh), endBatteryPct)
	}

	var source *string
	if startToStore != nil || endBatteryPct != nil {
		s := batteryPctSourceUserVerified
		source = &s
	}

	row, err := q.VerifyChargeSession(ctx, chargingdb.VerifyChargeSessionParams{
		ID:               id,
		AccountID:        accountID,
		StartBatteryPct:  intPtrToPgInt2(startToStore),
		EndBatteryPct:    intPtrToPgInt2(endBatteryPct),
		BatteryPctSource: stringPtrToPgText(source),
	})
	if err != nil {
		return Session{}, fmt.Errorf("charging: verify session: %w", err)
	}

	if tx != nil {
		if err := tx.Commit(ctx); err != nil {
			return Session{}, fmt.Errorf("charging: committing tx: %w", err)
		}
	}

	return rowToSession(row), nil
}
