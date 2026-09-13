package telemetry

import (
	"context"
	"testing"
	"time"
)

// These tests exercise SuperchargerHistoryReader.SuperchargerHistoryByVehicleBetween
// against a real Postgres from TEST_DATABASE_URL and self-skip when it is unset, so
// `go test ./...` stays green without a database (ai/go-conventions.md §persistence,
// AGENTS.md §Testing notes). They reuse upsertSuperchargerHistory (service.go) and
// newSuperchargerHistoryReaderImpl (reader.go) — the same helpers
// db_supercharger_integration_test.go already exercises — plus the itoa helper
// defined there (same package, no re-declaration needed).
//
// T-10 (design.md's test contract): a session stopping on `start`, one stopping
// exactly on `end`, one late in the end calendar day, and one starting the day
// before `start` but stopping inside the window are all included, ordered
// oldest-first by stop time; a session stopping one day past `end` is excluded.

// TestSuperchargerHistoryByVehicleBetween_StopTimeSemantics implements T-10.
func TestSuperchargerHistoryByVehicleBetween_StopTimeSemantics(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	teslaID := int64(111)
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)

	s7201 := int64(7201) // stops exactly on start
	s7202 := int64(7202) // stops late in the end day
	s7203 := int64(7203) // stops one day past end — must be excluded
	s7204 := int64(7204) // starts the day before start, stops inside the window
	ids := []int64{s7201, s7202, s7203, s7204}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM telemetry.supercharger_history WHERE session_id = ANY($1::bigint[])", ids)
	})

	fixtures := []struct {
		id         int64
		chargeFrom time.Time
		chargeTo   time.Time
	}{
		{s7201, time.Date(2026, 7, 31, 23, 30, 0, 0, time.UTC), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
		{s7202, time.Date(2026, 8, 3, 23, 29, 0, 0, time.UTC), time.Date(2026, 8, 3, 23, 59, 0, 0, time.UTC)},
		{s7203, time.Date(2026, 8, 3, 23, 30, 0, 0, time.UTC), time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)},
		{s7204, time.Date(2026, 7, 31, 23, 0, 0, 0, time.UTC), time.Date(2026, 8, 1, 1, 0, 0, 0, time.UTC)},
	}
	for _, f := range fixtures {
		if err := st.upsertSuperchargerHistory(ctx, SuperchargerHistory{
			SessionID:           f.id,
			VIN:                 "VIN_BETWEEN",
			TeslaID:             teslaID,
			SiteLocationName:    "Site",
			CountryCode:         "US",
			ChargeStartDateTime: f.chargeFrom,
			ChargeStopDateTime:  f.chargeTo,
			BillingType:         "PAYMENT",
			VehicleMakeType:     "MODEL_3",
			RawData:             []byte(`{"sessionId":` + itoa(f.id) + `}`),
		}); err != nil {
			t.Fatalf("upsert session %d: %v", f.id, err)
		}
	}

	r := newSuperchargerHistoryReaderImpl(pool)
	got, err := r.SuperchargerHistoryByVehicleBetween(ctx, teslaID, start, end)
	if err != nil {
		t.Fatalf("SuperchargerHistoryByVehicleBetween: %v", err)
	}

	wantIDs := []int64{s7201, s7204, s7202}
	if len(got) != len(wantIDs) {
		t.Fatalf("want %d sessions %v, got %d: %+v", len(wantIDs), wantIDs, len(got), got)
	}
	for i, want := range wantIDs {
		if got[i].SessionID != want {
			t.Errorf("position %d: want session %d, got %d (ordered by stop time ascending)", i, want, got[i].SessionID)
		}
	}
	for _, s := range got {
		if s.SessionID == s7203 {
			t.Error("session 7203 (stops one day past end) must be excluded, was returned")
		}
	}
}

// TestSuperchargerHistoryByVehicleBetween_VehicleIsolation verifies that two
// vehicles each with a session in the same window only return the queried
// vehicle's session.
func TestSuperchargerHistoryByVehicleBetween_VehicleIsolation(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	teslaA := int64(920003)
	teslaB := int64(920004)
	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)

	sessionA := int64(972001)
	sessionB := int64(972002)
	ids := []int64{sessionA, sessionB}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM telemetry.supercharger_history WHERE session_id = ANY($1::bigint[])", ids)
	})

	stop := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC) // same window for both vehicles

	insert := func(sessionID, teslaID int64, vin string) {
		if err := st.upsertSuperchargerHistory(ctx, SuperchargerHistory{
			SessionID:           sessionID,
			VIN:                 vin,
			TeslaID:             teslaID,
			SiteLocationName:    "Site",
			CountryCode:         "US",
			ChargeStartDateTime: stop.Add(-30 * time.Minute),
			ChargeStopDateTime:  stop,
			BillingType:         "PAYMENT",
			VehicleMakeType:     "MODEL_3",
			RawData:             []byte(`{"sessionId":` + itoa(sessionID) + `}`),
		}); err != nil {
			t.Fatalf("upsert session %d: %v", sessionID, err)
		}
	}
	insert(sessionA, teslaA, "VIN_TENANT_A")
	insert(sessionB, teslaB, "VIN_TENANT_B")

	r := newSuperchargerHistoryReaderImpl(pool)
	got, err := r.SuperchargerHistoryByVehicleBetween(ctx, teslaA, start, end)
	if err != nil {
		t.Fatalf("SuperchargerHistoryByVehicleBetween: %v", err)
	}

	if len(got) != 1 || got[0].SessionID != sessionA {
		t.Fatalf("want only vehicle A's session (%d), got %+v", sessionA, got)
	}
	for _, s := range got {
		if s.TeslaID != teslaA {
			t.Errorf("returned a row for a different vehicle: %v", s.TeslaID)
		}
	}
}
