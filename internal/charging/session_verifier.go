package charging

import (
	"context"
	"fmt"

	"github.com/google/uuid"
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

// VerifySession implements SessionVerifier. See the interface doc comment (charging.go)
// for the full contract. Implementation shape is validate-then-query (design.md
// §"Go-side call shape"):
//
//  1. Range-validate each non-nil percentage against [0, 100] BEFORE any database call
//     (design.md D3) — the DB's own SMALLINT CHECK is the backstop, not the error
//     message.
//  2. Compute battery_pct_source: batteryPctSourceUserVerified when either percentage
//     is non-nil, nil (SQL NULL) when both are nil (design.md D2/D7).
//  3. Call VerifyChargeSession, scoped by id + accountID, and map the returned row via
//     the existing rowToSession (session_reader.go) — no new mapping code.
func (v *sessionVerifier) VerifySession(ctx context.Context, accountID uuid.UUID, id uuid.UUID, startBatteryPct, endBatteryPct *int) (Session, error) {
	if startBatteryPct != nil && (*startBatteryPct < 0 || *startBatteryPct > 100) {
		return Session{}, fmt.Errorf("charging: start_battery_pct %d out of range [0,100]", *startBatteryPct)
	}
	if endBatteryPct != nil && (*endBatteryPct < 0 || *endBatteryPct > 100) {
		return Session{}, fmt.Errorf("charging: end_battery_pct %d out of range [0,100]", *endBatteryPct)
	}

	var source *string
	if startBatteryPct != nil || endBatteryPct != nil {
		s := batteryPctSourceUserVerified
		source = &s
	}

	row, err := v.q.VerifyChargeSession(ctx, chargingdb.VerifyChargeSessionParams{
		ID:               id,
		AccountID:        accountID,
		StartBatteryPct:  intPtrToPgInt2(startBatteryPct),
		EndBatteryPct:    intPtrToPgInt2(endBatteryPct),
		BatteryPctSource: stringPtrToPgText(source),
	})
	if err != nil {
		return Session{}, fmt.Errorf("charging: verify session: %w", err)
	}
	return rowToSession(row), nil
}
