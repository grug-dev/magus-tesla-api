// Package charging_test — database-backed integration tests for the one-time
// charge_sessions backfill shipped inside the
// 20260823000001_add_charge_sessions.sql migration's guarded `DO $$ … $$` block
// (design.md D8c, RM29-charging-add-charge-sessions, tasks.md task 3.3). Implements
// design.md's Test Contract **Group A** (A1, A2).
//
// A migration's backfill runs once, at `goose up`, before any test can seed a
// source row — so it always executes against an empty telemetry.supercharger_history and can
// never be observed through normal test provisioning. These tests work around that
// by extracting the SHIPPED backfill statement at runtime from the embedded
// migration file, between the `-- BACKFILL-BEGIN` / `-- BACKFILL-END` sentinels
// task 1.1 wrote, and re-executing that exact text against a database this package's
// TestMain has already seeded — never a hand-copied duplicate of the SQL, so the
// test can never drift from what actually ships to production (design.md D8c
// explicitly rejects copying the SQL into the test as a const).
//
// Test → Test Contract case mapping:
//
//	A1 TestBackfill_RealFourRowDataset_OnePercentageBearing
//	A2 TestBackfill_IdempotentOnRerun_OverwritesNothing
//
// A3 ("no-op, not an error, when the source table is absent") is documented rather
// than exercised in-suite, per design.md: this package's own TestMain always applies
// telemetry's migration directory first (testdb_test.go), so the to_regclass guard
// never actually trips inside this suite. It is verified by the charging-only path
// instead: applying only internal/charging/db/migrations/ to an empty database
// succeeds and yields an empty supercharger_sessions.
package charging_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationFileName is the shipped migration this package's backfill tests extract
// their statement from — kept as one named constant so a future rename of the file
// only needs to change one line.
const migrationFileName = "db/migrations/20260823000001_add_charge_sessions.sql"

// extractBackfillStatement reads the shipped migration file through the package's
// existing //go:embed (migrationsFS, testdb_test.go) and returns the exact SQL text
// between the BACKFILL-BEGIN and BACKFILL-END sentinel comments, trimmed. Fails the
// test loudly if either sentinel is missing — a silent empty statement would make
// every assertion below vacuously pass.
func extractBackfillStatement(t *testing.T) string {
	t.Helper()
	data, err := migrationsFS.ReadFile(migrationFileName)
	if err != nil {
		t.Fatalf("reading migration file %s: %v", migrationFileName, err)
	}
	content := string(data)

	const beginMarker = "-- BACKFILL-BEGIN"
	const endMarker = "-- BACKFILL-END"

	beginIdx := strings.Index(content, beginMarker)
	if beginIdx == -1 {
		t.Fatalf("BACKFILL-BEGIN sentinel not found in %s", migrationFileName)
	}
	endIdx := strings.Index(content, endMarker)
	if endIdx == -1 {
		t.Fatalf("BACKFILL-END sentinel not found in %s", migrationFileName)
	}
	if endIdx <= beginIdx {
		t.Fatalf("BACKFILL-END appears before BACKFILL-BEGIN in %s", migrationFileName)
	}

	stmt := strings.TrimSpace(content[beginIdx+len(beginMarker) : endIdx])
	if stmt == "" {
		t.Fatalf("extracted backfill statement is empty — sentinels moved?")
	}
	return stmt
}

// runBackfill executes the extracted backfill statement against the shared test
// pool.
// insertTargetOld / insertTargetNew map the backfill's INSERT target forward to the
// name it has today. RM39 tier 3 moved this module's table into the `charging` schema
// and renamed it `charge_sessions` -> `supercharger_sessions`; the shipped migration
// necessarily still names the table as it existed when it ran, and historic migrations
// are never edited (RM39 D1). Replaying it against a fully-migrated database therefore
// has to map that one name forward.
//
// THREE names are rewritten, not one. RM39 tier 4 moved telemetry's tables into the
// `telemetry` schema and renamed `supercharger_sessions` to `supercharger_history`, so
// both the statement's source reads now point at a relation that no longer exists under
// the old name — alongside the INSERT target, which tier 3 had already moved.
//
// The to_regclass guard is the dangerous one. Miss it and the mapping fails SILENTLY:
// `to_regclass('public.supercharger_sessions')` evaluates to NULL forever, the DO block
// RETURNs early, raises no error (only a NOTICE), inserts nothing, and A1/A2 fail on
// EMPTY assertions — a failure that reads like a data bug rather than a name bug. Each
// mapping therefore carries an "exactly one occurrence, else t.Fatalf" guard, so a
// change to the migration's shape fails loudly instead of quietly asserting nothing.
//
// The NOTICE string inside the guard also names the table. It is prose inside a RAISE
// and constrains nothing, so it is deliberately NOT rewritten — a fourth fragile string
// match would buy nothing and could break the replay on a harmless wording change.
//
// This is deliberately NOT the `search_path` approach RM39 D12 used for analytics'
// replay test, and tier 4 makes that argument stronger rather than weaker. A search_path
// of `charging, public` would resolve a bare `supercharger_sessions` to charging's OWN
// table — which since tier 3 is a real, populated table of a different shape — so the
// backfill would silently read the wrong source and assert nothing. D12's fix is safe
// only where a move happened without a name collision; here the name collides, so every
// mapping must be explicit.
const (
	insertTargetOld = "INSERT INTO charge_sessions ("
	insertTargetNew = "INSERT INTO charging.supercharger_sessions ("

	// The guard's argument: telemetry's table is now telemetry.supercharger_history.
	guardTargetOld = "to_regclass('public.supercharger_sessions')"
	guardTargetNew = "to_regclass('telemetry.supercharger_history')"

	// The source read, aliased `s` in the shipped statement.
	sourceTableOld = "FROM supercharger_sessions s"
	sourceTableNew = "FROM telemetry.supercharger_history s"
)

func runBackfill(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	stmt := extractBackfillStatement(t)

	// Fail loudly if the shipped statement no longer contains the target we expect.
	// A silent no-op rewrite would re-run the original text and fail with a confusing
	// "relation does not exist", hiding the real cause: the migration changed shape.
	for _, m := range []struct{ old, new, what string }{
		{insertTargetOld, insertTargetNew, "INSERT target"},
		{guardTargetOld, guardTargetNew, "to_regclass guard"},
		{sourceTableOld, sourceTableNew, "source table read"},
	} {
		if n := strings.Count(stmt, m.old); n != 1 {
			t.Fatalf("expected exactly 1 occurrence of %q (%s) in the shipped backfill statement, got %d — "+
				"the migration changed shape and this rewrite needs updating", m.old, m.what, n)
		}
		stmt = strings.Replace(stmt, m.old, m.new, 1)
	}

	if _, err := pool.Exec(context.Background(), stmt); err != nil {
		t.Fatalf("executing extracted backfill statement: %v", err)
	}
}

// superchargerFixtureRow is one row of the live 4-row telemetry.supercharger_history dataset
// design.md Test Contract A1 reproduces. Percentage fields are pointers so a NULL
// case (three of the four rows) is expressible.
type superchargerFixtureRow struct {
	SessionID           int64
	VIN                 string
	TeslaID             int64
	SiteLocationName    string
	CountryCode         string
	ChargeStartDateTime time.Time
	ChargeStopDateTime  time.Time
	BillingType         string
	VehicleMakeType     string
	EnergyKWh           float64
	TotalCost           float64
	Currency            string
	IsPaid              bool
	CreatedAt           time.Time

	StartBatteryPct    *int
	EndBatteryPct      *int
	BatteryPctSource   *string
	StartBatteryPctEst *int
	EndBatteryPctEst   *int
}

// insertSuperchargerSessionFixture seeds one telemetry.supercharger_history row via direct
// SQL — the sanctioned form for seeding another module's table from a _test.go file
// when that module exposes no writer for the shape needed (ai/go-conventions.md
// §Testing, RM29 decision D19); telemetry has no writer at all for a single
// Supercharger session with a caller-chosen created_at. raw_data is a fixed empty
// JSONB object — this backfill never reads it (design.md D1's closed exclusion
// list), so its content is irrelevant to every assertion here.
func insertSuperchargerSessionFixture(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, r superchargerFixtureRow) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO telemetry.supercharger_history (
			session_id, account_id, vin, tesla_id, site_location_name, country_code,
			charge_start_date_time, charge_stop_date_time, billing_type, vehicle_make_type,
			energy_kwh, total_cost, currency, is_paid, raw_data, created_at,
			start_battery_pct, end_battery_pct, battery_pct_source,
			start_battery_pct_est, end_battery_pct_est
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10,
			$11, $12, $13, $14, '{}'::jsonb, $15,
			$16, $17, $18,
			$19, $20
		)`,
		r.SessionID, accountID, r.VIN, r.TeslaID, r.SiteLocationName, r.CountryCode,
		r.ChargeStartDateTime, r.ChargeStopDateTime, r.BillingType, r.VehicleMakeType,
		r.EnergyKWh, r.TotalCost, r.Currency, r.IsPaid, r.CreatedAt,
		r.StartBatteryPct, r.EndBatteryPct, r.BatteryPctSource,
		r.StartBatteryPctEst, r.EndBatteryPctEst,
	)
	if err != nil {
		t.Fatalf("seeding telemetry.supercharger_history fixture (session %d): %v", r.SessionID, err)
	}
}

// cleanupSuperchargerSessions registers a cleanup that deletes telemetry.supercharger_history
// rows created by these tests so a shared DB stays tidy.
func cleanupSuperchargerSessions(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM telemetry.supercharger_history WHERE account_id = $1", accountID)
	})
}

// seedA1Fixture seeds design.md Test Contract A1's exact live 4-row dataset under
// one fresh account and returns the four session ids seeded, in insertion order
// (index 0 is the percentage-bearing session, session_id 734860294).
func seedA1Fixture(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID) []int64 {
	t.Helper()

	const vin = "LRWYGCFJ8TC495757"
	const teslaID = int64(3744327027802250)
	const site = "Medellín, Colombia"

	rows := []superchargerFixtureRow{
		{
			SessionID:           734860294,
			VIN:                 vin,
			TeslaID:             teslaID,
			SiteLocationName:    site,
			CountryCode:         "CO",
			ChargeStartDateTime: time.Date(2026, 8, 7, 18, 28, 9, 0, time.FixedZone("-05", -5*3600)),
			ChargeStopDateTime:  time.Date(2026, 8, 7, 19, 10, 0, 0, time.FixedZone("-05", -5*3600)),
			BillingType:         "PAYMENT",
			VehicleMakeType:     "MODEL_3",
			EnergyKWh:           41.2,
			TotalCost:           58900.0,
			Currency:            "COP",
			IsPaid:              true,
			CreatedAt:           time.Date(2026, 8, 7, 19, 15, 0, 0, time.UTC),
			StartBatteryPct:     ptrInt(29),
			EndBatteryPct:       ptrInt(100),
			// BatteryPctSource intentionally nil — the one legacy provenance
			// violation design.md D5 resolves via COALESCE in the backfill.
		},
		{
			SessionID:           734860301,
			VIN:                 vin,
			TeslaID:             teslaID,
			SiteLocationName:    site,
			CountryCode:         "CO",
			ChargeStartDateTime: time.Date(2026, 8, 9, 8, 0, 0, 0, time.FixedZone("-05", -5*3600)),
			ChargeStopDateTime:  time.Date(2026, 8, 9, 8, 35, 0, 0, time.FixedZone("-05", -5*3600)),
			BillingType:         "PAYMENT",
			VehicleMakeType:     "MODEL_3",
			EnergyKWh:           30.0,
			TotalCost:           41500.0,
			Currency:            "COP",
			IsPaid:              true,
			CreatedAt:           time.Date(2026, 8, 9, 8, 40, 0, 0, time.UTC),
		},
		{
			SessionID:           734860308,
			VIN:                 vin,
			TeslaID:             teslaID,
			SiteLocationName:    site,
			CountryCode:         "CO",
			ChargeStartDateTime: time.Date(2026, 8, 11, 14, 5, 0, 0, time.FixedZone("-05", -5*3600)),
			ChargeStopDateTime:  time.Date(2026, 8, 11, 14, 42, 0, 0, time.FixedZone("-05", -5*3600)),
			BillingType:         "PAYMENT",
			VehicleMakeType:     "MODEL_3",
			EnergyKWh:           25.7,
			TotalCost:           35200.0,
			Currency:            "COP",
			IsPaid:              true,
			CreatedAt:           time.Date(2026, 8, 11, 14, 45, 0, 0, time.UTC),
		},
		{
			SessionID:           734860315,
			VIN:                 vin,
			TeslaID:             teslaID,
			SiteLocationName:    site,
			CountryCode:         "CO",
			ChargeStartDateTime: time.Date(2026, 8, 14, 6, 20, 0, 0, time.FixedZone("-05", -5*3600)),
			ChargeStopDateTime:  time.Date(2026, 8, 14, 7, 0, 0, 0, time.FixedZone("-05", -5*3600)),
			BillingType:         "PAYMENT",
			VehicleMakeType:     "MODEL_3",
			EnergyKWh:           38.9,
			TotalCost:           54100.0,
			Currency:            "COP",
			IsPaid:              true,
			CreatedAt:           time.Date(2026, 8, 14, 7, 5, 0, 0, time.UTC),
		},
	}

	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		insertSuperchargerSessionFixture(t, pool, accountID, r)
		ids = append(ids, r.SessionID)
	}
	return ids
}

// A1: the real 4-row dataset backfills to exactly 4 rows, one of them
// percentage-bearing.
func TestBackfill_RealFourRowDataset_OnePercentageBearing(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupSuperchargerSessions(t, pool, accountID)
	cleanupChargingSuperchargerSessions(t, pool, accountID)

	sessionIDs := seedA1Fixture(t, pool, accountID)
	const percentageBearingSessionID = int64(734860294)

	runBackfill(t, pool)

	if n := countSuperchargerSessions(t, pool, accountID); n != 4 {
		t.Fatalf("expected exactly 4 rows after backfill, got %d", n)
	}

	for _, sessionID := range sessionIDs {
		var srcVIN, srcSite string
		var srcTeslaID int64
		var srcStart, srcStop, srcCreatedAt time.Time
		var srcEnergyKWh, srcTotalCost float64
		var srcCurrency string
		var srcIsPaid bool
		if err := pool.QueryRow(context.Background(), `
			SELECT vin, tesla_id, charge_start_date_time, charge_stop_date_time,
			       site_location_name, energy_kwh, total_cost, currency, is_paid, created_at
			FROM telemetry.supercharger_history WHERE account_id = $1 AND session_id = $2`,
			accountID, sessionID,
		).Scan(&srcVIN, &srcTeslaID, &srcStart, &srcStop, &srcSite, &srcEnergyKWh, &srcTotalCost, &srcCurrency, &srcIsPaid, &srcCreatedAt); err != nil {
			t.Fatalf("reading source telemetry.supercharger_history row %d: %v", sessionID, err)
		}

		row, ok := fetchSuperchargerSession(t, pool, accountID, sessionID)
		if !ok {
			t.Fatalf("expected mirrored row for session %d", sessionID)
		}

		if row.VIN != srcVIN {
			t.Errorf("session %d: VIN got %q, want %q", sessionID, row.VIN, srcVIN)
		}
		if row.TeslaID == nil || *row.TeslaID != srcTeslaID {
			t.Errorf("session %d: TeslaID got %v, want %v", sessionID, row.TeslaID, srcTeslaID)
		}
		if !row.ChargeStartDateTime.Equal(srcStart) {
			t.Errorf("session %d: ChargeStartDateTime got %v, want %v", sessionID, row.ChargeStartDateTime, srcStart)
		}
		if !row.ChargeStopDateTime.Equal(srcStop) {
			t.Errorf("session %d: ChargeStopDateTime got %v, want %v", sessionID, row.ChargeStopDateTime, srcStop)
		}
		if row.SiteLocationName != srcSite {
			t.Errorf("session %d: SiteLocationName got %q, want %q (non-ASCII must round-trip)", sessionID, row.SiteLocationName, srcSite)
		}
		if row.EnergyKWh == nil || *row.EnergyKWh != srcEnergyKWh {
			t.Errorf("session %d: EnergyKWh got %v, want %v", sessionID, row.EnergyKWh, srcEnergyKWh)
		}
		if row.TotalCost == nil || *row.TotalCost != srcTotalCost {
			t.Errorf("session %d: TotalCost got %v, want %v", sessionID, row.TotalCost, srcTotalCost)
		}
		if row.Currency == nil || *row.Currency != srcCurrency {
			t.Errorf("session %d: Currency got %v, want %q", sessionID, row.Currency, srcCurrency)
		}
		if row.IsPaid == nil || *row.IsPaid != srcIsPaid {
			t.Errorf("session %d: IsPaid got %v, want %v", sessionID, row.IsPaid, srcIsPaid)
		}
		if !row.CreatedAt.Equal(srcCreatedAt) {
			t.Errorf("session %d: CreatedAt got %v, want source's %v", sessionID, row.CreatedAt, srcCreatedAt)
		}

		if sessionID == percentageBearingSessionID {
			if row.StartBatteryPct == nil || *row.StartBatteryPct != 29 {
				t.Errorf("session %d: StartBatteryPct got %v, want 29", sessionID, row.StartBatteryPct)
			}
			if row.EndBatteryPct == nil || *row.EndBatteryPct != 100 {
				t.Errorf("session %d: EndBatteryPct got %v, want 100", sessionID, row.EndBatteryPct)
			}
			if row.BatteryPctSource == nil || *row.BatteryPctSource != "user_verified" {
				t.Errorf("session %d: BatteryPctSource got %v, want 'user_verified' (resolved from NULL by the backfill's COALESCE)", sessionID, row.BatteryPctSource)
			}
		} else {
			if row.StartBatteryPct != nil {
				t.Errorf("session %d: StartBatteryPct got %v, want nil", sessionID, *row.StartBatteryPct)
			}
			if row.EndBatteryPct != nil {
				t.Errorf("session %d: EndBatteryPct got %v, want nil", sessionID, *row.EndBatteryPct)
			}
			if row.BatteryPctSource != nil {
				t.Errorf("session %d: BatteryPctSource got %v, want nil (must NOT be fabricated for a row with no percentages)", sessionID, *row.BatteryPctSource)
			}
		}
	}
}

// A2: the backfill is idempotent and overwrites nothing on a re-run — ON CONFLICT DO
// NOTHING means it is a one-time import, never a re-sync (that is MirrorSuperchargerSession's
// job).
func TestBackfill_IdempotentOnRerun_OverwritesNothing(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupSuperchargerSessions(t, pool, accountID)
	cleanupChargingSuperchargerSessions(t, pool, accountID)

	sessionIDs := seedA1Fixture(t, pool, accountID)
	const percentageBearingSessionID = int64(734860294)
	nullRowSessionID := sessionIDs[1] // any of the three all-NULL-percentage rows

	runBackfill(t, pool)
	if n := countSuperchargerSessions(t, pool, accountID); n != 4 {
		t.Fatalf("expected 4 rows after first backfill, got %d", n)
	}

	before := make(map[int64]time.Time, len(sessionIDs))
	for _, id := range sessionIDs {
		row, ok := fetchSuperchargerSession(t, pool, accountID, id)
		if !ok {
			t.Fatalf("expected row for session %d after first backfill", id)
		}
		before[id] = row.CreatedAt
	}

	// (a) simulate a later human verification of a previously-unverified row.
	if _, err := pool.Exec(context.Background(), `
		UPDATE charging.supercharger_sessions SET start_battery_pct = 55, end_battery_pct = 80, battery_pct_source = 'user_verified'
		WHERE account_id = $1 AND session_id = $2`,
		accountID, nullRowSessionID); err != nil {
		t.Fatalf("simulating human verification: %v", err)
	}
	// (b) simulate a fee figure a later mirror pass already refreshed.
	if _, err := pool.Exec(context.Background(), `
		UPDATE charging.supercharger_sessions SET total_cost = 99999, is_paid = false
		WHERE account_id = $1 AND session_id = $2`,
		accountID, percentageBearingSessionID); err != nil {
		t.Fatalf("simulating a refreshed fee figure: %v", err)
	}

	runBackfill(t, pool)

	if n := countSuperchargerSessions(t, pool, accountID); n != 4 {
		t.Fatalf("expected still 4 rows after second backfill, got %d", n)
	}

	verifiedRow, ok := fetchSuperchargerSession(t, pool, accountID, nullRowSessionID)
	if !ok {
		t.Fatalf("expected row for session %d after second backfill", nullRowSessionID)
	}
	if verifiedRow.StartBatteryPct == nil || *verifiedRow.StartBatteryPct != 55 {
		t.Errorf("session %d: StartBatteryPct got %v, want still 55 (backfill must not overwrite)", nullRowSessionID, verifiedRow.StartBatteryPct)
	}
	if verifiedRow.EndBatteryPct == nil || *verifiedRow.EndBatteryPct != 80 {
		t.Errorf("session %d: EndBatteryPct got %v, want still 80", nullRowSessionID, verifiedRow.EndBatteryPct)
	}
	if verifiedRow.BatteryPctSource == nil || *verifiedRow.BatteryPctSource != "user_verified" {
		t.Errorf("session %d: BatteryPctSource got %v, want still 'user_verified'", nullRowSessionID, verifiedRow.BatteryPctSource)
	}

	refreshedRow, ok := fetchSuperchargerSession(t, pool, accountID, percentageBearingSessionID)
	if !ok {
		t.Fatalf("expected row for session %d after second backfill", percentageBearingSessionID)
	}
	if refreshedRow.TotalCost == nil || *refreshedRow.TotalCost != 99999 {
		t.Errorf("session %d: TotalCost got %v, want still 99999 (backfill must not overwrite)", percentageBearingSessionID, refreshedRow.TotalCost)
	}
	if refreshedRow.IsPaid == nil || *refreshedRow.IsPaid {
		t.Errorf("session %d: IsPaid got %v, want still false", percentageBearingSessionID, refreshedRow.IsPaid)
	}

	for _, id := range sessionIDs {
		row, ok := fetchSuperchargerSession(t, pool, accountID, id)
		if !ok {
			t.Fatalf("expected row for session %d after second backfill", id)
		}
		if !row.CreatedAt.Equal(before[id]) {
			t.Errorf("session %d: CreatedAt changed across the second backfill: was %v, now %v", id, before[id], row.CreatedAt)
		}
	}
}
