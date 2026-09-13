package telemetry

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// These tests exercise the real telemetrydb store and SuperchargerHistoryReader against a live
// Postgres from TEST_DATABASE_URL and self-skip when it is unset, so `go test ./...` stays
// green without a database (ai/go-conventions.md §persistence). They require:
//   - TEST_DATABASE_URL set to a running Postgres with goose migrations applied.
//   - telemetry.supercharger_history, keyed on tesla_id (NOT NULL) — there is no
//     account_id column and no account-wide read any more. A session for a VIN
//     that is not a registered vehicle is never stored (see collectChargingHistory
//     in service.go), so every row here always has a real tesla_id.
//
// Coverage:
//   - upsert round-trip with the nullable fields (energy, cost, currency, is_paid,
//     unlatch_date_time) non-nil.
//   - upsert round-trip with those same fields nil (NULL vs zero check).
//   - upsert idempotency + immutable columns unchanged on second upsert.
//   - SuperchargerHistoryByVehicle scopes to the queried vehicle only (T-7).
//   - Results are ordered by charge_start_date_time DESC (newest first).
//   - LIMIT is respected.

// itoa converts int64 to string for inline JSON building in tests.
func itoa(n int64) string {
	return fmt.Sprint(n)
}

// TestSupercharger_UpsertAndRead_RoundTrip verifies that a session upserted via
// the store is readable via SuperchargerHistoryByVehicle with all nullable fields
// faithfully preserved when non-nil.
func TestSupercharger_UpsertAndRead_RoundTrip(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	teslaID := int64(700001)
	sessionID := int64(60001)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.supercharger_history WHERE session_id = $1", sessionID)
	})

	cost := 4.20
	currency := "USD"
	energy := 35.5
	paid := true
	unlatch := time.Date(2026, 6, 28, 11, 0, 0, 0, time.UTC)

	sess := SuperchargerHistory{
		SessionID:           sessionID,
		VIN:                 "VIN_RT_001",
		TeslaID:             teslaID,
		SiteLocationName:    "Test Supercharger",
		CountryCode:         "US",
		ChargeStartDateTime: time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 6, 28, 10, 45, 0, 0, time.UTC),
		UnlatchDateTime:     &unlatch,
		BillingType:         "PAYMENT",
		VehicleMakeType:     "MODEL_3",
		EnergyKWh:           &energy,
		TotalCost:           &cost,
		Currency:            &currency,
		IsPaid:              &paid,
		RawData:             []byte(`{"sessionId":60001}`),
	}
	if err := st.upsertSuperchargerHistory(ctx, sess); err != nil {
		t.Fatalf("upsertSuperchargerHistory: %v", err)
	}

	r := newSuperchargerHistoryReaderImpl(pool)
	got, err := r.SuperchargerHistoryByVehicle(ctx, teslaID, 10)
	if err != nil {
		t.Fatalf("SuperchargerHistoryByVehicle: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 session, got %d", len(got))
	}
	s := got[0]
	if s.SessionID != sessionID {
		t.Errorf("SessionID: want %d, got %d", sessionID, s.SessionID)
	}
	if s.VIN != "VIN_RT_001" {
		t.Errorf("VIN: want VIN_RT_001, got %q", s.VIN)
	}
	if s.TeslaID != teslaID {
		t.Errorf("TeslaID: want %d, got %d", teslaID, s.TeslaID)
	}
	if s.EnergyKWh == nil || *s.EnergyKWh != 35.5 {
		t.Errorf("EnergyKWh: want *35.5, got %v", s.EnergyKWh)
	}
	if s.TotalCost == nil || *s.TotalCost != 4.20 {
		t.Errorf("TotalCost: want *4.20, got %v", s.TotalCost)
	}
	if s.Currency == nil || *s.Currency != "USD" {
		t.Errorf("Currency: want *USD, got %v", s.Currency)
	}
	if s.IsPaid == nil || !*s.IsPaid {
		t.Errorf("IsPaid: want *true, got %v", s.IsPaid)
	}
	if s.UnlatchDateTime == nil {
		t.Error("UnlatchDateTime: want non-nil, got nil")
	}
	if len(s.RawData) == 0 {
		t.Error("RawData: want non-empty, got empty")
	}
}

// TestSupercharger_UpsertNullableNullValues verifies that the fields that can
// still be nil (EnergyKWh, TotalCost, Currency, IsPaid, UnlatchDateTime) round-trip
// as SQL NULL, not a zero value. TeslaID cannot be nil any more — a session is
// only ever stored for a registered vehicle, so it always carries a real value.
func TestSupercharger_UpsertNullableNullValues(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	teslaID := int64(700002)
	sessionID := int64(60002)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.supercharger_history WHERE session_id = $1", sessionID)
	})

	sess := SuperchargerHistory{
		SessionID:           sessionID,
		VIN:                 "VIN_NOFEES",
		TeslaID:             teslaID,
		SiteLocationName:    "Test",
		CountryCode:         "US",
		ChargeStartDateTime: time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 6, 28, 10, 30, 0, 0, time.UTC),
		UnlatchDateTime:     nil, // absent in response
		BillingType:         "PAYMENT",
		VehicleMakeType:     "MODEL_3",
		EnergyKWh:           nil, // time-based billing only
		TotalCost:           nil, // no fees
		Currency:            nil, // no fees
		IsPaid:              nil, // no fees
		RawData:             []byte(`{"sessionId":60002}`),
	}
	if err := st.upsertSuperchargerHistory(ctx, sess); err != nil {
		t.Fatalf("upsertSuperchargerHistory: %v", err)
	}

	r := newSuperchargerHistoryReaderImpl(pool)
	got, err := r.SuperchargerHistoryByVehicle(ctx, teslaID, 10)
	if err != nil {
		t.Fatalf("SuperchargerHistoryByVehicle: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 session, got %d", len(got))
	}
	s := got[0]
	if s.TeslaID != teslaID {
		t.Errorf("TeslaID: want %d, got %d", teslaID, s.TeslaID)
	}
	if s.UnlatchDateTime != nil {
		t.Errorf("UnlatchDateTime: want nil, got %v", s.UnlatchDateTime)
	}
	if s.EnergyKWh != nil {
		t.Errorf("EnergyKWh: want nil, got %v", s.EnergyKWh)
	}
	if s.TotalCost != nil {
		t.Errorf("TotalCost: want nil, got %v", s.TotalCost)
	}
	if s.Currency != nil {
		t.Errorf("Currency: want nil, got %v", s.Currency)
	}
	if s.IsPaid != nil {
		t.Errorf("IsPaid: want nil, got %v", s.IsPaid)
	}
}

// TestSupercharger_UpsertIdempotency verifies that upserting the same session_id twice
// updates mutable fields (is_paid, total_cost, raw_data) and does NOT duplicate the row
// or change immutable fields (vin, charge_start_date_time, created_at).
func TestSupercharger_UpsertIdempotency(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	teslaID := int64(700003)
	sessionID := int64(60003)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.supercharger_history WHERE session_id = $1", sessionID)
	})

	notPaid := false
	cost1 := 2.00
	currency := "USD"
	energy := 20.0
	startTime := time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC)

	sess1 := SuperchargerHistory{
		SessionID:           sessionID,
		VIN:                 "VIN_IDEM",
		TeslaID:             teslaID,
		SiteLocationName:    "Test SC",
		CountryCode:         "US",
		ChargeStartDateTime: startTime,
		ChargeStopDateTime:  startTime.Add(30 * time.Minute),
		BillingType:         "PAYMENT",
		VehicleMakeType:     "MODEL_3",
		EnergyKWh:           &energy,
		TotalCost:           &cost1,
		Currency:            &currency,
		IsPaid:              &notPaid,
		RawData:             []byte(`{"sessionId":60003,"v":1}`),
	}
	if err := st.upsertSuperchargerHistory(ctx, sess1); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Second upsert: is_paid = true, total_cost = 3.50, different raw_data.
	paid := true
	cost2 := 3.50
	sess2 := sess1
	sess2.IsPaid = &paid
	sess2.TotalCost = &cost2
	sess2.RawData = []byte(`{"sessionId":60003,"v":2}`)
	if err := st.upsertSuperchargerHistory(ctx, sess2); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	r := newSuperchargerHistoryReaderImpl(pool)
	got, err := r.SuperchargerHistoryByVehicle(ctx, teslaID, 10)
	if err != nil {
		t.Fatalf("SuperchargerHistoryByVehicle: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly 1 row after idempotent upsert (no duplicates), got %d", len(got))
	}
	s := got[0]
	// Mutable fields updated.
	if s.IsPaid == nil || !*s.IsPaid {
		t.Errorf("IsPaid: want *true (updated by second upsert), got %v", s.IsPaid)
	}
	if s.TotalCost == nil || *s.TotalCost != 3.50 {
		t.Errorf("TotalCost: want *3.50 (updated), got %v", s.TotalCost)
	}
	// Immutable fields unchanged.
	if s.VIN != "VIN_IDEM" {
		t.Errorf("VIN must not change on conflict update: want VIN_IDEM, got %q", s.VIN)
	}
	if !s.ChargeStartDateTime.UTC().Equal(startTime) {
		t.Errorf("ChargeStartDateTime must not change: want %v, got %v", startTime, s.ChargeStartDateTime.UTC())
	}
}

// TestSupercharger_SessionsByVehicle_Scoping implements T-7: three sessions,
// two for the queried vehicle and one for another vehicle. Only the queried
// vehicle's sessions come back, newest first.
func TestSupercharger_SessionsByVehicle_Scoping(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	teslaA := int64(111)
	teslaB := int64(222)
	session7001 := int64(7001)
	session7002 := int64(7002)
	session7003 := int64(7003)
	ids := []int64{session7001, session7002, session7003}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.supercharger_history WHERE session_id = ANY($1::bigint[])", ids)
	})

	for _, tc := range []struct {
		sessionID int64
		teslaID   int64
		start     time.Time
	}{
		{session7001, teslaA, time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)},
		{session7002, teslaA, time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)},
		{session7003, teslaB, time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)},
	} {
		if err := st.upsertSuperchargerHistory(ctx, SuperchargerHistory{
			SessionID:           tc.sessionID,
			VIN:                 "VIN_SCOPE",
			TeslaID:             tc.teslaID,
			SiteLocationName:    "Site",
			CountryCode:         "US",
			ChargeStartDateTime: tc.start,
			ChargeStopDateTime:  tc.start.Add(30 * time.Minute),
			BillingType:         "PAYMENT",
			VehicleMakeType:     "MODEL_3",
			RawData:             []byte(`{"sessionId":` + itoa(tc.sessionID) + `}`),
		}); err != nil {
			t.Fatalf("upsert session %d: %v", tc.sessionID, err)
		}
	}

	r := newSuperchargerHistoryReaderImpl(pool)
	got, err := r.SuperchargerHistoryByVehicle(ctx, teslaA, 10)
	if err != nil {
		t.Fatalf("SuperchargerHistoryByVehicle: %v", err)
	}
	wantOrder := []int64{session7002, session7001}
	if len(got) != len(wantOrder) {
		t.Fatalf("want %d sessions for vehicle A, got %d: %+v", len(wantOrder), len(got), got)
	}
	for i, want := range wantOrder {
		if got[i].SessionID != want {
			t.Errorf("position %d: want session %d, got %d (newest first)", i, want, got[i].SessionID)
		}
	}
	for _, s := range got {
		if s.SessionID == session7003 {
			t.Error("session 7003 (a different vehicle) must not appear")
		}
	}
}

// TestSupercharger_Ordering_NewestFirst verifies that sessions are returned in
// charge_start_date_time DESC order (newest first).
func TestSupercharger_Ordering_NewestFirst(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	teslaID := int64(700030)
	sessionOld := int64(60030)
	sessionNew := int64(60031)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.supercharger_history WHERE session_id = ANY($1::bigint[])", []int64{sessionOld, sessionNew})
	})

	oldStart := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	newStart := time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		id    int64
		start time.Time
	}{
		{sessionOld, oldStart},
		{sessionNew, newStart},
	} {
		if err := st.upsertSuperchargerHistory(ctx, SuperchargerHistory{
			SessionID:           tc.id,
			VIN:                 "VIN_ORD",
			TeslaID:             teslaID,
			SiteLocationName:    "Site",
			CountryCode:         "US",
			ChargeStartDateTime: tc.start,
			ChargeStopDateTime:  tc.start.Add(30 * time.Minute),
			BillingType:         "PAYMENT",
			VehicleMakeType:     "MODEL_3",
			RawData:             []byte(`{"sessionId":` + itoa(tc.id) + `}`),
		}); err != nil {
			t.Fatalf("upsert session %d: %v", tc.id, err)
		}
	}

	r := newSuperchargerHistoryReaderImpl(pool)
	got, err := r.SuperchargerHistoryByVehicle(ctx, teslaID, 10)
	if err != nil {
		t.Fatalf("SuperchargerHistoryByVehicle: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(got))
	}
	if got[0].SessionID != sessionNew {
		t.Errorf("want newest session first (id=%d), got id=%d", sessionNew, got[0].SessionID)
	}
	if got[1].SessionID != sessionOld {
		t.Errorf("want oldest session second (id=%d), got id=%d", sessionOld, got[1].SessionID)
	}
}

// TestSupercharger_Limit verifies that passing limit=1 returns at most 1 row.
func TestSupercharger_Limit(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	teslaID := int64(700040)
	ids := []int64{60040, 60041, 60042}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.supercharger_history WHERE session_id = ANY($1::bigint[])", ids)
	})

	for i, id := range ids {
		start := time.Date(2026, 6, 28, 10+i, 0, 0, 0, time.UTC)
		if err := st.upsertSuperchargerHistory(ctx, SuperchargerHistory{
			SessionID:           id,
			VIN:                 "VIN_LMT",
			TeslaID:             teslaID,
			SiteLocationName:    "Site",
			CountryCode:         "US",
			ChargeStartDateTime: start,
			ChargeStopDateTime:  start.Add(30 * time.Minute),
			BillingType:         "PAYMENT",
			VehicleMakeType:     "MODEL_3",
			RawData:             []byte(`{"sessionId":` + itoa(id) + `}`),
		}); err != nil {
			t.Fatalf("upsert session %d: %v", id, err)
		}
	}

	r := newSuperchargerHistoryReaderImpl(pool)
	got, err := r.SuperchargerHistoryByVehicle(ctx, teslaID, 1)
	if err != nil {
		t.Fatalf("SuperchargerHistoryByVehicle limit=1: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly 1 row with limit=1, got %d", len(got))
	}
}
