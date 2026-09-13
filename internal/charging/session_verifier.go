package charging

import (
	"context"
	"errors"
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
// TEST_DATABASE_URL-gated integration tests (no fake), so it is a small unexported struct
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

// sessionStatusFor computes the lifecycle SessionStatus for a supercharger_sessions
// row from the FINAL values VerifySession is about to store, plus whether THIS call
// derived startPct rather than storing a caller-supplied value
// (RM41-charging-add-session-status, MAG-45, design.md "Go-side call shape"). It is
// the single place this rule is written, mirroring needsDerivedStartBatteryPct's own
// "one small named function" shape.
//
// startPct/endPct here are the FINAL values about to be written -- startPct is
// VerifySession's own startToStore (the caller's value, the derived one, or nil),
// never the caller's raw startBatteryPct parameter. derived is true only when THIS
// call both attempted and succeeded at deriving startPct
// (needsDerivedStartBatteryPct(...) was true AND the derivation produced a non-nil
// result) -- a failed derivation (energy_kwh SQL NULL, or an out-of-range result)
// leaves startPct nil and therefore falls through to SessionStatusInProgress via the
// first rule below, never SessionStatusDoneCalculated.
//
// Rule, in priority order (design.md "State truth table"):
//  1. Either percentage absent (nil) -> SessionStatusInProgress. Covers "nothing
//     recorded", "only a start percentage recorded" (deliberately IN_PROGRESS, not a
//     fourth state), and "only an end percentage recorded and no derivation was
//     possible or successful".
//  2. Both present AND derived -> SessionStatusDoneCalculated.
//  3. Both present, not derived -> SessionStatusDone.
func sessionStatusFor(startPct, endPct *int, derived bool) SessionStatus {
	if startPct == nil || endPct == nil {
		return SessionStatusInProgress
	}
	if derived {
		return SessionStatusDoneCalculated
	}
	return SessionStatusDone
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
//     3.5. Compute status via sessionStatusFor from startToStore, the caller's
//     endBatteryPct, and whether step 2 actually derived a value
//     (RM41-charging-add-session-status design.md "Go-side call shape").
//  4. Call VerifySuperchargerSession — against the open transaction when one exists,
//     against v.q otherwise — scoped by id + teslaID, and map the returned row via
//     the existing rowToSession (session_reader.go) — no new mapping code. Commit the
//     transaction, when one was opened, only after VerifySuperchargerSession succeeds.
func (v *sessionVerifier) VerifySession(ctx context.Context, teslaID int64, id uuid.UUID, startBatteryPct, endBatteryPct *int) (Session, error) {
	if startBatteryPct != nil && (*startBatteryPct < 0 || *startBatteryPct > 100) {
		return Session{}, fmt.Errorf("charging: start_battery_pct %d out of range [0,100]", *startBatteryPct)
	}
	if endBatteryPct != nil && (*endBatteryPct < 0 || *endBatteryPct > 100) {
		return Session{}, fmt.Errorf("charging: end_battery_pct %d out of range [0,100]", *endBatteryPct)
	}

	startToStore := startBatteryPct
	calculated := false
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
			ID:      id,
			TeslaID: teslaID,
		})
		if err != nil {
			// design.md D10: identical wrap to VerifySuperchargerSession's own
			// not-found case — a caller of VerifySession cannot observe, and
			// must not need to care, which of the two internal queries
			// produced a given not-found error.
			return Session{}, fmt.Errorf("charging: verify session: %w", err)
		}

		// row.TeslaID is the locked row's own vehicle id, always the same value as
		// the teslaID parameter above -- it is NOT NULL on this table now, so there
		// is no unregistered-vehicle case left to fall back from.
		capacityKWh, err := packCapacityKWh(ctx, v, row.TeslaID)
		if err != nil {
			return Session{}, fmt.Errorf("charging: resolving pack capacity: %w", err)
		}

		startToStore = derivedStartBatteryPct(capacityKWh, pgFloat8ToFloat64Ptr(row.EnergyKwh), endBatteryPct)
		calculated = startToStore != nil
	}

	status := sessionStatusFor(startToStore, endBatteryPct, calculated)

	var source *string
	if startToStore != nil || endBatteryPct != nil {
		s := batteryPctSourceUserVerified
		source = &s
	}

	row, err := q.VerifySuperchargerSession(ctx, chargingdb.VerifySuperchargerSessionParams{
		ID:               id,
		TeslaID:          teslaID,
		StartBatteryPct:  intPtrToPgInt2(startToStore),
		EndBatteryPct:    intPtrToPgInt2(endBatteryPct),
		BatteryPctSource: stringPtrToPgText(source),
		Status:           string(status),
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

// latestMeasuredCapacity satisfies packCapacityLookup (design.md D3) using this
// port's own *chargingdb.Queries -- mirrors dbStore's identical method in
// service.go; not shared, because sessionVerifier is deliberately outside the
// store interface (this file's own existing doc comment).
func (v *sessionVerifier) latestMeasuredCapacity(ctx context.Context, teslaID int64) (*float64, error) {
	val, err := v.q.LatestMeasuredCapacity(ctx, teslaID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("charging: reading latest measured capacity for tesla_id %d: %w", teslaID, err)
	}
	return pgFloat8ToFloat64Ptr(val), nil
}
