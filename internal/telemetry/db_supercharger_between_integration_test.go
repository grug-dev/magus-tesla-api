package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// These tests exercise SuperchargerReader.SuperchargerSessionsByVehicleBetween
// (RM28-telemetry-add-charge-gap-storage, roadmap D9/D12) against a real Postgres from
// DATABASE_URL and self-skip when it is unset, so `go test ./...` stays green without a
// database (ai/go-conventions.md §persistence, AGENTS.md §Testing notes). They reuse
// upsertSuperchargerSession (service.go) and newSuperchargerReaderImpl (reader.go) —
// the same helpers db_supercharger_integration_test.go already exercises — plus the
// itoa helper defined there (same package, no re-declaration needed).
//
// They implement the test contract authored in design.md BEFORE the implementation
// existed (§"Test Contract"):
//
//   - (c) TestSuperchargerSessionsByVehicleBetween_BoundaryInclusiveStartAndEndDay_ExcludesDayAfter
//   - (d) TestSuperchargerSessionsByVehicleBetween_IncludesMidnightSpanningSession_FiltersOnStopTime
//   - (e)-iii TestSuperchargerSessionsByVehicleBetween_TenantIsolation_ReturnsOnlyRequestedAccountVehicle
//
// DISCREPANCY IN design.md's SCENARIO (d) — see the worker report for this task: the
// prose says S5 is "ordered before S1 (its stop time, 00:15:00Z, is after S1's
// 00:00:00Z)" — those two clauses cannot both be true under the ascending-by-
// ChargeStopDateTime ordering scenario (c) establishes ("the returned slice is ordered
// oldest-first (S1 before S2 before S3)") and the SuperchargerReader interface doc
// comment restates ("ordered oldest-first (ascending by ChargeStopDateTime)"). The test
// below asserts the internally-consistent half of the contract — ascending stop-time
// order, so S1 (earlier stop) precedes S5 (later stop) — which is also exactly what
// tasks.md's own T7.4 acceptance criterion asks for ("ordered correctly among the
// scenario (c) fixtures by its stop time"), without repeating design.md's
// self-contradictory "before S1" wording.

// TestSuperchargerSessionsByVehicleBetween_BoundaryInclusiveStartAndEndDay_ExcludesDayAfter
// implements design.md test-contract scenario (c): a session stopping exactly on
// `start`, one exactly on `end` (the first instant of the end calendar day), and one
// late in the end calendar day are all included and ordered oldest-first; a session
// stopping exactly one day past `end` is excluded (T7.3).
func TestSuperchargerSessionsByVehicleBetween_BoundaryInclusiveStartAndEndDay_ExcludesDayAfter(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	teslaID := int64(920001)
	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)

	s1ID := int64(970001) // exactly on start
	s2ID := int64(970002) // exactly on end (first instant of the end day)
	s3ID := int64(970003) // late in the end day
	s4ID := int64(970004) // exactly one day past end — must be excluded
	ids := []int64{s1ID, s2ID, s3ID, s4ID}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM supercharger_sessions WHERE session_id = ANY($1::bigint[])", ids)
	})

	fixtures := []struct {
		id   int64
		stop time.Time
	}{
		{s1ID, time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)},
		{s2ID, time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)},
		{s3ID, time.Date(2026, 8, 12, 23, 59, 59, 0, time.UTC)},
		{s4ID, time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)},
	}

	tid := teslaID
	for _, f := range fixtures {
		if err := st.upsertSuperchargerSession(ctx, SuperchargerSession{
			SessionID:           f.id,
			AccountID:           accountID,
			VIN:                 "VIN_BETWEEN",
			TeslaID:             &tid,
			SiteLocationName:    "Site",
			CountryCode:         "US",
			ChargeStartDateTime: f.stop.Add(-30 * time.Minute),
			ChargeStopDateTime:  f.stop,
			BillingType:         "PAYMENT",
			VehicleMakeType:     "MODEL_3",
			RawData:             []byte(`{"sessionId":` + itoa(f.id) + `}`),
		}); err != nil {
			t.Fatalf("upsert session %d: %v", f.id, err)
		}
	}

	r := newSuperchargerReaderImpl(pool)
	got, err := r.SuperchargerSessionsByVehicleBetween(ctx, accountID, teslaID, start, end)
	if err != nil {
		t.Fatalf("SuperchargerSessionsByVehicleBetween: %v", err)
	}

	wantIDs := []int64{s1ID, s2ID, s3ID}
	if len(got) != len(wantIDs) {
		t.Fatalf("want %d sessions (S1,S2,S3), got %d: %+v", len(wantIDs), len(got), got)
	}
	for i, want := range wantIDs {
		if got[i].SessionID != want {
			t.Errorf("position %d: want session %d, got %d (result must be ordered oldest-first)", i, want, got[i].SessionID)
		}
	}
	for _, s := range got {
		if s.SessionID == s4ID {
			t.Error("S4 (charge_stop_date_time exactly end+1 day) must be excluded, was returned")
		}
	}
}

// TestSuperchargerSessionsByVehicleBetween_IncludesMidnightSpanningSession_FiltersOnStopTime
// implements design.md test-contract scenario (d): a session that STARTS the day before
// `start` but STOPS inside the window is included, because the port filters purely on
// ChargeStopDateTime and ignores where the session started (T7.4).
func TestSuperchargerSessionsByVehicleBetween_IncludesMidnightSpanningSession_FiltersOnStopTime(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	teslaID := int64(920002)
	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)

	s1ID := int64(971001) // stops exactly on start (scenario (c)'s S1 shape)
	s5ID := int64(971005) // starts the day BEFORE start, stops inside the window
	ids := []int64{s1ID, s5ID}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM supercharger_sessions WHERE session_id = ANY($1::bigint[])", ids)
	})

	tid := teslaID
	s1Stop := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	if err := st.upsertSuperchargerSession(ctx, SuperchargerSession{
		SessionID:           s1ID,
		AccountID:           accountID,
		VIN:                 "VIN_MIDNIGHT",
		TeslaID:             &tid,
		SiteLocationName:    "Site",
		CountryCode:         "US",
		ChargeStartDateTime: s1Stop.Add(-30 * time.Minute),
		ChargeStopDateTime:  s1Stop,
		BillingType:         "PAYMENT",
		VehicleMakeType:     "MODEL_3",
		RawData:             []byte(`{"sessionId":` + itoa(s1ID) + `}`),
	}); err != nil {
		t.Fatalf("upsert S1: %v", err)
	}

	s5Start := time.Date(2026, 8, 9, 23, 30, 0, 0, time.UTC) // the day BEFORE start
	s5Stop := time.Date(2026, 8, 10, 0, 15, 0, 0, time.UTC)  // inside the window
	if err := st.upsertSuperchargerSession(ctx, SuperchargerSession{
		SessionID:           s5ID,
		AccountID:           accountID,
		VIN:                 "VIN_MIDNIGHT",
		TeslaID:             &tid,
		SiteLocationName:    "Site",
		CountryCode:         "US",
		ChargeStartDateTime: s5Start,
		ChargeStopDateTime:  s5Stop,
		BillingType:         "PAYMENT",
		VehicleMakeType:     "MODEL_3",
		RawData:             []byte(`{"sessionId":` + itoa(s5ID) + `}`),
	}); err != nil {
		t.Fatalf("upsert S5: %v", err)
	}

	r := newSuperchargerReaderImpl(pool)
	got, err := r.SuperchargerSessionsByVehicleBetween(ctx, accountID, teslaID, start, end)
	if err != nil {
		t.Fatalf("SuperchargerSessionsByVehicleBetween: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("want 2 sessions (S1, S5) — S5 included despite starting before the window, got %d: %+v", len(got), got)
	}

	var foundS5 bool
	for _, s := range got {
		if s.SessionID == s5ID {
			foundS5 = true
		}
	}
	if !foundS5 {
		t.Error("S5 (starts before the window, stops inside it) must be included — the port filters on stop time only")
	}

	// Ascending-by-ChargeStopDateTime order (see the file-level DISCREPANCY note above):
	// S1's stop (00:00:00Z) precedes S5's stop (00:15:00Z).
	if got[0].SessionID != s1ID || got[1].SessionID != s5ID {
		t.Errorf("want oldest-first order [S1, S5] by ChargeStopDateTime, got [%d, %d]", got[0].SessionID, got[1].SessionID)
	}
}

// TestSuperchargerSessionsByVehicleBetween_TenantIsolation_ReturnsOnlyRequestedAccountVehicle
// implements design.md test-contract scenario (e)-iii: given two accounts each with a
// session whose ChargeStopDateTime falls in the same window, calling the method for one
// account/vehicle returns only that account's session, never the other's (T7.5).
func TestSuperchargerSessionsByVehicleBetween_TenantIsolation_ReturnsOnlyRequestedAccountVehicle(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountA := uuid.New()
	teslaA := int64(920003)
	accountB := uuid.New()
	teslaB := int64(920004)
	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)

	sessionA := int64(972001)
	sessionB := int64(972002)
	ids := []int64{sessionA, sessionB}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM supercharger_sessions WHERE session_id = ANY($1::bigint[])", ids)
	})

	stop := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC) // same window for both accounts

	tidA := teslaA
	if err := st.upsertSuperchargerSession(ctx, SuperchargerSession{
		SessionID:           sessionA,
		AccountID:           accountA,
		VIN:                 "VIN_TENANT_A",
		TeslaID:             &tidA,
		SiteLocationName:    "Site",
		CountryCode:         "US",
		ChargeStartDateTime: stop.Add(-30 * time.Minute),
		ChargeStopDateTime:  stop,
		BillingType:         "PAYMENT",
		VehicleMakeType:     "MODEL_3",
		RawData:             []byte(`{"sessionId":` + itoa(sessionA) + `}`),
	}); err != nil {
		t.Fatalf("upsert account A session: %v", err)
	}

	tidB := teslaB
	if err := st.upsertSuperchargerSession(ctx, SuperchargerSession{
		SessionID:           sessionB,
		AccountID:           accountB,
		VIN:                 "VIN_TENANT_B",
		TeslaID:             &tidB,
		SiteLocationName:    "Site",
		CountryCode:         "US",
		ChargeStartDateTime: stop.Add(-30 * time.Minute),
		ChargeStopDateTime:  stop,
		BillingType:         "PAYMENT",
		VehicleMakeType:     "MODEL_3",
		RawData:             []byte(`{"sessionId":` + itoa(sessionB) + `}`),
	}); err != nil {
		t.Fatalf("upsert account B session: %v", err)
	}

	r := newSuperchargerReaderImpl(pool)
	got, err := r.SuperchargerSessionsByVehicleBetween(ctx, accountA, teslaA, start, end)
	if err != nil {
		t.Fatalf("SuperchargerSessionsByVehicleBetween: %v", err)
	}

	if len(got) != 1 || got[0].SessionID != sessionA {
		t.Fatalf("want only account A's session (%d), got %+v", sessionA, got)
	}
	for _, s := range got {
		if s.AccountID != accountA {
			t.Errorf("returned a row for a different account: %v", s.AccountID)
		}
	}
}
