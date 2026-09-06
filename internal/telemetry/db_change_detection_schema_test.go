// db_change_detection_schema_test.go is the D7.7 self-checking schema test
// for UpsertSuperchargerHistory's change-detecting ON CONFLICT DO UPDATE SET
// clause (RM44-telemetry-add-change-detecting-upsert, design.md D7.7,
// tasks.md task 2.2). It mirrors internal/charging's own analogous self-check
// for the sibling MirrorSuperchargerSession query
// (RM44-charging-add-change-detecting-mirror,
// db_mirror_schema_selfcheck_integration_test.go).
//
// This file does not test one upsert call — it tests that the deny-list
// inside the query's updated_at comparison stays correct as the schema
// evolves. The governing rule (design.md D2): the comparison covers EXACTLY
// the columns the SET clause writes (6) and nothing else; every other live
// column is deny-listed (16 today). Under that rule the live schema is a
// total partition of the two sets, with no leftover bucket. Check 1 below
// asserts that partition against information_schema at runtime, so a future
// migration that adds a column makes this test fail on its own, by name,
// with no code review needed to catch it. Check 2 is the behavioral proof:
// poke every settable deny-listed column with a sentinel, re-upsert with
// identical values, and assert updated_at did not move.
package telemetry

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// changeDetectSetColumns is the SET clause's own column list — the six
// columns UpsertSuperchargerHistory refreshes on every conflict. Hardcoded
// here, matching production (db/query.sql), mirroring internal/charging's own
// writtenColumns for the same design pattern.
var changeDetectSetColumns = []string{
	"raw_data", "energy_kwh", "total_cost", "currency", "is_paid", "tesla_id",
}

// changeDetectDenyListColumns is the 16-column deny-list from db/query.sql's
// UpsertSuperchargerHistory, grouped by design.md D2's three buckets:
//
//	(a) bookkeeping, not session data: id, created_at, updated_at
//	(b) human-owned, never touched by the poller: start_battery_pct,
//	    end_battery_pct, battery_pct_source
//	(c) write-once columns the SET clause never refreshes: session_id,
//	    account_id, vin, site_location_name, country_code,
//	    charge_start_date_time, charge_stop_date_time, unlatch_date_time,
//	    billing_type, vehicle_make_type
var changeDetectDenyListColumns = []string{
	"id", "created_at", "updated_at",
	"start_battery_pct", "end_battery_pct", "battery_pct_source",
	"session_id", "account_id", "vin", "site_location_name", "country_code",
	"charge_start_date_time", "charge_stop_date_time", "unlatch_date_time",
	"billing_type", "vehicle_make_type",
}

// fetchLiveSuperchargerHistoryColumns queries information_schema.columns at
// runtime for telemetry.supercharger_history — never a hand-typed Go slice
// standing in for the real schema. This is what makes check 1 self-checking:
// it fails the moment a migration adds or removes a column, without anyone
// updating this test by hand.
func fetchLiveSuperchargerHistoryColumns(t *testing.T, pool *pgxpool.Pool) map[string]bool {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_schema = 'telemetry' AND table_name = 'supercharger_history'`)
	if err != nil {
		t.Fatalf("querying information_schema.columns: %v", err)
	}
	defer rows.Close()

	live := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scanning column_name: %v", err)
		}
		live[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading information_schema.columns rows: %v", err)
	}
	if len(live) == 0 {
		t.Fatalf("information_schema.columns returned no rows for telemetry.supercharger_history — is the migration applied?")
	}
	return live
}

// TestUpsertSuperchargerHistory_ColumnsMatchSetPlusDenylist is check 1 of the
// D7.7 self-check: the live column set of telemetry.supercharger_history must
// equal EXACTLY changeDetectSetColumns UNION changeDetectDenyListColumns, with
// no leftover bucket. A column found on neither side fails by name, telling
// the next person exactly which list to add it to (design.md's own governing
// rule: add a column to SET, remove it from the deny-list; add a column SET
// does not write, add it to the deny-list).
func TestUpsertSuperchargerHistory_ColumnsMatchSetPlusDenylist(t *testing.T) {
	_, pool := newTestStore(t)
	live := fetchLiveSuperchargerHistoryColumns(t, pool)

	set := make(map[string]bool, len(changeDetectSetColumns))
	for _, c := range changeDetectSetColumns {
		set[c] = true
	}
	deny := make(map[string]bool, len(changeDetectDenyListColumns))
	for _, c := range changeDetectDenyListColumns {
		deny[c] = true
	}

	// No column may appear in both lists — that would mean the SET clause
	// writes a column this test also claims is never refreshed.
	var inBoth []string
	for c := range set {
		if deny[c] {
			inBoth = append(inBoth, c)
		}
	}
	if len(inBoth) > 0 {
		sort.Strings(inBoth)
		t.Errorf("column(s) claimed by BOTH the SET list and the deny-list (fix this test file): %v", inBoth)
	}

	// Every live column must be in SET or deny.
	var uncovered []string
	for c := range live {
		if !set[c] && !deny[c] {
			uncovered = append(uncovered, c)
		}
	}
	if len(uncovered) > 0 {
		sort.Strings(uncovered)
		t.Errorf(
			"live column(s) in telemetry.supercharger_history belong to NEITHER the SET list nor the deny-list: %v — "+
				"if UpsertSuperchargerHistory's ON CONFLICT DO UPDATE SET now refreshes this column, add it to "+
				"changeDetectSetColumns in this file (and to the query's comparison); otherwise add it to "+
				"changeDetectDenyListColumns in this file (and to the query's deny-list array). See design.md D2.",
			uncovered)
	}

	// Every SET/deny entry must actually exist on the live table — catches a
	// stale entry left behind by a column rename or drop.
	var stale []string
	for c := range set {
		if !live[c] {
			stale = append(stale, "set:"+c)
		}
	}
	for c := range deny {
		if !live[c] {
			stale = append(stale, "deny:"+c)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("this test's SET/deny lists name column(s) that no longer exist on telemetry.supercharger_history: %v — update this file to match the current schema", stale)
	}
}

// TestUpsertSuperchargerHistory_UndenylistedColumnBreaksChangeDetection is
// check 2 of the D7.7 self-check: the behavioral proof. Seed one row, poke
// every settable deny-listed column with a sentinel, re-upsert with the SAME
// values used to seed, and assert updated_at did not move.
//
// Exemptions, both documented at the point they are skipped below:
//   - id, session_id: session_id is the ON CONFLICT match key for this table
//     (unlike id, which is never a query parameter at all). Poking it would
//     make the re-upsert insert a NEW row instead of updating this one, which
//     would prove nothing about the deny-list — the same reason id itself is
//     skipped. See this task's worker report for the note on this point.
//   - start_battery_pct, end_battery_pct, battery_pct_source (bucket b): the
//     human-owned trio. Poking it is exactly what a human verification does
//     and must not look like a detected "change" — already covered by
//     TestUpsertSuperchargerHistory_HumanBatteryPctSet_UnchangedResyncStillLeavesUpdatedAtUntouched
//     (D7.5) in db_change_detection_integration_test.go.
func TestUpsertSuperchargerHistory_UndenylistedColumnBreaksChangeDetection(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const sessionID = int64(61100)
	teslaID := int64(810100)
	start := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.supercharger_history WHERE session_id = $1", sessionID)
	})

	sess := SuperchargerHistory{
		SessionID:           sessionID,
		AccountID:           accountID,
		VIN:                 "VIN_POKE_ORIGINAL",
		TeslaID:             &teslaID,
		SiteLocationName:    "Original Site",
		CountryCode:         "US",
		ChargeStartDateTime: start,
		ChargeStopDateTime:  start.Add(30 * time.Minute),
		UnlatchDateTime:     nil,
		BillingType:         "PAYMENT",
		VehicleMakeType:     "MODEL_3",
		RawData:             []byte(`{"sessionId":61100}`),
	}
	if err := st.upsertSuperchargerHistory(ctx, sess); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	before, err := queryChangeDetectUpdatedAt(ctx, pool, sessionID)
	if err != nil {
		t.Fatalf("reading updated_at: %v", err)
	}

	// Poke every bucket-(a)/(c) column this design's SET clause never
	// refreshes with a sentinel value distinguishable from what the identical
	// re-upsert below supplies.
	sentinelAccountID := uuid.New()
	sentinelTime := time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx,
		`UPDATE telemetry.supercharger_history SET
		    created_at             = $1,
		    account_id             = $2,
		    vin                    = $3,
		    site_location_name     = $4,
		    country_code           = $5,
		    charge_start_date_time = $6,
		    charge_stop_date_time  = $7,
		    unlatch_date_time      = $8,
		    billing_type           = $9,
		    vehicle_make_type      = $10
		  WHERE session_id = $11`,
		sentinelTime, sentinelAccountID, "SENTINEL_VIN", "SENTINEL_SITE", "ZZ",
		sentinelTime, sentinelTime, sentinelTime, "SENTINEL_BILLING", "SENTINEL_MAKE",
		sessionID,
	); err != nil {
		t.Fatalf("poking deny-listed columns: %v", err)
	}

	time.Sleep(changeDetectSleep)
	if err := st.upsertSuperchargerHistory(ctx, sess); err != nil {
		t.Fatalf("identical re-upsert after poking deny-listed columns: %v", err)
	}

	after, err := queryChangeDetectUpdatedAt(ctx, pool, sessionID)
	if err != nil {
		t.Fatalf("reading updated_at after re-upsert: %v", err)
	}
	if !before.Equal(after) {
		t.Errorf("updated_at moved when only deny-listed columns mismatched: before=%v after=%v", before, after)
	}
}
