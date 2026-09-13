// Package charging_test — the D8 self-checking schema test for
// MirrorSuperchargerSession's change-detecting ON CONFLICT DO UPDATE SET clause
// (RM44-charging-add-change-detecting-mirror, design.md "The self-checking schema
// test", tasks.md task 3.3).
//
// This file does not test one MirrorSessions call — it tests that the deny-list
// inside the query's updated_at comparison stays correct as the schema evolves.
// The governing rule (design.md): the comparison covers EXACTLY the columns the
// SET clause writes (5) and nothing else; every other live column is deny-listed
// (13 today, since RM57-charging-rekey-supercharger-sessions-on-tesla-id dropped
// account_id from the table). Under that rule the live schema is a total
// partition of the two sets, with no leftover bucket — 5 + 13 = 18 live columns.
// Part 1 below asserts that partition against information_schema at runtime, so
// a future migration that adds a column makes this test fail on its own, by
// name, with no code review needed to catch it. Part 2 is the behavioral proof:
// poke every settable deny-listed column with a sentinel, re-mirror with
// identical values, and assert nothing moved.
package charging_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// writtenColumns is the SET clause's own column list — the 5 columns
// MirrorSuperchargerSession refreshes on every mirror pass. Hardcoded here,
// matching production (design.md "The self-checking schema test", step 2).
var writtenColumns = []string{
	"energy_kwh", "total_cost", "currency", "is_paid", "tesla_id",
}

// denyListColumns is the 13-column deny-list — every column this query never
// refreshes, grouped by design.md's three buckets:
//
//	(a) bookkeeping, not row data: id, created_at, updated_at
//	(b) human-owned / derived from human-owned, never touched by the poller:
//	    start_battery_pct, end_battery_pct, battery_pct_source, status,
//	    inferred_capacity_kwh_calc
//	(c) write-once mirrored columns the SET clause never refreshes: vin,
//	    session_id, charge_start_date_time, charge_stop_date_time,
//	    site_location_name
//
// account_id left this list when the column itself was dropped from the table
// (RM57-charging-rekey-supercharger-sessions-on-tesla-id): there is no longer a
// column here to guard.
var denyListColumns = []string{
	"id", "created_at", "updated_at",
	"start_battery_pct", "end_battery_pct", "battery_pct_source", "status", "inferred_capacity_kwh_calc",
	"vin", "session_id", "charge_start_date_time", "charge_stop_date_time", "site_location_name",
}

// fetchLiveColumnNames queries information_schema.columns at runtime for
// charging.supercharger_sessions — never a hand-typed Go slice standing in for
// the real schema. This is what makes part 1 self-checking: it fails the moment a
// migration adds or removes a column, without anyone updating this test by hand.
func fetchLiveColumnNames(t *testing.T, pool *pgxpool.Pool) map[string]bool {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_schema = 'charging' AND table_name = 'supercharger_sessions'`)
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
		t.Fatalf("information_schema.columns returned no rows for charging.supercharger_sessions — is the migration applied?")
	}
	return live
}

// TestMirrorSchemaSelfCheck_LiveColumnsEqualWrittenUnionDeny is part 1 of the D8
// self-check: the live column set of charging.supercharger_sessions must equal
// EXACTLY written ∪ deny, with no leftover bucket. A column found on neither side
// fails by name, telling the next person exactly which list to add it to. A
// column claimed by BOTH lists is a bug in this test file itself, and also fails
// by name.
func TestMirrorSchemaSelfCheck_LiveColumnsEqualWrittenUnionDeny(t *testing.T) {
	pool := newTestPool(t)
	live := fetchLiveColumnNames(t, pool)

	written := make(map[string]bool, len(writtenColumns))
	for _, c := range writtenColumns {
		written[c] = true
	}
	deny := make(map[string]bool, len(denyListColumns))
	for _, c := range denyListColumns {
		deny[c] = true
	}

	// No column may appear in both lists — that would mean the SET clause writes
	// a column this test also claims is never refreshed.
	var inBoth []string
	for c := range written {
		if deny[c] {
			inBoth = append(inBoth, c)
		}
	}
	if len(inBoth) > 0 {
		sort.Strings(inBoth)
		t.Errorf("column(s) claimed by BOTH the written list and the deny-list (fix this test file): %v", inBoth)
	}

	// Every live column must be in written OR deny.
	var uncovered []string
	for c := range live {
		if !written[c] && !deny[c] {
			uncovered = append(uncovered, c)
		}
	}
	if len(uncovered) > 0 {
		sort.Strings(uncovered)
		t.Errorf(
			"live column(s) in charging.supercharger_sessions belong to NEITHER the written list nor the deny-list: %v — "+
				"if MirrorSuperchargerSession's ON CONFLICT DO UPDATE SET now refreshes this column, add it to writtenColumns "+
				"in this file (and to the query's comparison); otherwise add it to denyListColumns in this file "+
				"(and to the query's deny-list array). See design.md 'The governing rule'.",
			uncovered)
	}

	// Every written/deny entry must actually exist on the live table — catches a
	// stale entry left behind by a column rename or drop.
	var stale []string
	for c := range written {
		if !live[c] {
			stale = append(stale, "written:"+c)
		}
	}
	for c := range deny {
		if !live[c] {
			stale = append(stale, "deny:"+c)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("this test's written/deny lists name column(s) that no longer exist on charging.supercharger_sessions: %v — update this file to match the current schema", stale)
	}
}

// selfCheckSentinelTime is the fixed past instant every timestamp sentinel in
// part 2 is set to. Far enough in the past that no code path could produce it by
// accident, so seeing it survive a re-mirror is unambiguous proof of "unchanged."
var selfCheckSentinelTime = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// TestMirrorSchemaSelfCheck_PokedDenyListColumnsSurviveRemirror is part 2 of the
// D8 self-check: the behavioral proof. Seed one row, directly poke every settable
// deny-listed column to a sentinel, re-mirror with the SAME SessionMirror values
// used to seed, and assert every sentinel — including updated_at — is unchanged.
//
// Exemptions, both documented at the point they are skipped below:
//   - id, session_id: these identify the row and form the ON CONFLICT target.
//     Poking them would make the re-mirror insert a NEW row instead of updating
//     this one, which would pass this test for the wrong reason (it would prove
//     nothing about the deny-list).
//   - inferred_capacity_kwh_calc: GENERATED ALWAYS, so it cannot be set directly
//     (Postgres rejects the write). It is still exercised indirectly: poking
//     start_battery_pct/end_battery_pct below recomputes it to a real value, and
//     this test asserts that value is unchanged after the re-mirror too.
func TestMirrorSchemaSelfCheck_PokedDenyListColumnsSurviveRemirror(t *testing.T) {
	pool := newTestPool(t)
	const sessionID = int64(970100)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, sessionID)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	seed := charging.SessionMirror{
		VIN:                 "VSELFCHECK",
		TeslaID:             sessionID,
		SessionID:           sessionID,
		ChargeStartDateTime: time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 8, 12, 9, 30, 0, 0, time.UTC),
		SiteLocationName:    "Self-Check Original Site",
		EnergyKWh:           ptrFloat64(25.0),
		TotalCost:           ptrFloat64(10.0),
		Currency:            ptrString("USD"),
		IsPaid:              ptrBool(true),
	}
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{seed}); err != nil {
		t.Fatalf("MirrorSessions (seed): %v", err)
	}

	// Poke every settable deny-listed column to a sentinel, directly by SQL —
	// never through a chargingdb reader/writer.
	pokedStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pokedStop := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	pokedSite := "Self-Check POKED Site (must not survive as this)"
	tag, err := pool.Exec(ctx, `
		UPDATE charging.supercharger_sessions
		SET created_at = $2,
		    updated_at = $2,
		    start_battery_pct = 55,
		    end_battery_pct = 90,
		    battery_pct_source = 'user_verified',
		    status = 'DONE',
		    charge_start_date_time = $3,
		    charge_stop_date_time = $4,
		    site_location_name = $5
		WHERE session_id = $1`,
		sessionID, selfCheckSentinelTime, pokedStart, pokedStop, pokedSite,
	)
	if err != nil {
		t.Fatalf("direct-SQL poke of deny-listed columns: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("direct-SQL poke: expected 1 row affected, got %d", tag.RowsAffected())
	}

	before, ok := fetchSuperchargerSession(t, pool, sessionID)
	if !ok {
		t.Fatalf("expected row after poke")
	}
	statusBefore, capacityBefore := fetchStatusAndCapacity(t, pool, sessionID)
	if capacityBefore == nil {
		t.Fatalf("setup: expected inferred_capacity_kwh_calc to be non-NULL after poking both percentages (energy_kwh is present)")
	}

	// Re-mirror with the SAME SessionMirror values used to seed — nothing about
	// the source has changed.
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{seed}); err != nil {
		t.Fatalf("MirrorSessions (re-mirror over poked row): %v", err)
	}

	after, ok := fetchSuperchargerSession(t, pool, sessionID)
	if !ok {
		t.Fatalf("expected row after re-mirror")
	}
	statusAfter, capacityAfter := fetchStatusAndCapacity(t, pool, sessionID)

	if !after.CreatedAt.Equal(selfCheckSentinelTime) {
		t.Errorf("CreatedAt: want still the poked sentinel %v, got %v", selfCheckSentinelTime, after.CreatedAt)
	}
	if !after.UpdatedAt.Equal(selfCheckSentinelTime) {
		t.Errorf("UpdatedAt: want still the poked sentinel %v (not a fresh now()), got %v", selfCheckSentinelTime, after.UpdatedAt)
	}
	if after.StartBatteryPct == nil || *after.StartBatteryPct != 55 {
		t.Errorf("StartBatteryPct: want still poked 55, got %v", after.StartBatteryPct)
	}
	if after.EndBatteryPct == nil || *after.EndBatteryPct != 90 {
		t.Errorf("EndBatteryPct: want still poked 90, got %v", after.EndBatteryPct)
	}
	if after.BatteryPctSource == nil || *after.BatteryPctSource != "user_verified" {
		t.Errorf("BatteryPctSource: want still poked user_verified, got %v", after.BatteryPctSource)
	}
	if statusAfter != statusBefore {
		t.Errorf("Status: want unchanged %q, got %q", statusBefore, statusAfter)
	}
	if !after.ChargeStartDateTime.Equal(pokedStart) {
		t.Errorf("ChargeStartDateTime: want still poked %v, got %v", pokedStart, after.ChargeStartDateTime)
	}
	if !after.ChargeStopDateTime.Equal(pokedStop) {
		t.Errorf("ChargeStopDateTime: want still poked %v, got %v", pokedStop, after.ChargeStopDateTime)
	}
	if after.SiteLocationName != pokedSite {
		t.Errorf("SiteLocationName: want still poked %q, got %q", pokedSite, after.SiteLocationName)
	}
	if capacityAfter == nil {
		t.Fatalf("InferredCapacityKWhCalc: want still non-NULL, got nil")
	}
	if *capacityAfter != *capacityBefore {
		t.Errorf("InferredCapacityKWhCalc: want unchanged %v (exercised indirectly via the percentage pokes), got %v", *capacityBefore, *capacityAfter)
	}

	// The 5 refreshed columns and identity/keys must be untouched by the poke
	// (they were never poked) — a sanity check that this test's own setup did not
	// accidentally corrupt something outside the deny-list.
	if before.EnergyKWh == nil || *before.EnergyKWh != 25.0 {
		t.Errorf("setup sanity: EnergyKWh before re-mirror: got %v, want 25.0 (unpoked)", before.EnergyKWh)
	}
}
