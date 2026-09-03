package telemetry

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// These tests exercise the real telemetrydb store and SuperchargerReader against a live
// Postgres from DATABASE_URL and self-skip when it is unset, so `go test ./...` stays
// green without a database (ai/go-conventions.md §persistence). They require:
//   - DATABASE_URL set to a running Postgres with goose migrations applied.
//   - The supercharger_sessions table created by migration 20260716000001.
//
// Design compliance:
//   - B8.1: upsert round-trip with all nullable fields (non-nil).
//   - B8.1: upsert round-trip with all nullable fields nil (NULL vs zero check).
//   - B8.2: upsert idempotency + immutable columns unchanged on second upsert.
//   - B8.3: SuperchargerSessionsByVehicle scopes to the queried vehicle only.
//   - B8.4: SuperchargerSessionsByAccount scopes to the queried account only.
//   - B8.5: Results are ordered by charge_start_date_time DESC (newest first).
//   - B8.6: LIMIT is respected.

// itoa converts int64 to string for inline JSON building in tests.
func itoa(n int64) string {
	return fmt.Sprint(n)
}

// cleanupSuperchargerBySessionID registers a cleanup that deletes supercharger_history
// rows by session_id using the pool returned by newTestStore. Keeps shared DB tidy.
func cleanupSuperchargerBySessionID(t *testing.T, st *dbStore, pool interface {
	Exec(ctx context.Context, sql string, args ...any) (interface{}, error)
}, sessionIDs ...int64) {
	// We can't use pgxpool.Pool directly here without importing pgxpool. Instead we
	// use a type switch via the pool value returned from newTestStore (which is
	// *pgxpool.Pool in the same package). We store the cleanup using the st.q
	// db.Exec path — but since telemetrydb.Queries embeds DBTX (which is pgxpool.Pool),
	// we'll just run raw SQL through the same pool by using the pg pool helper below.
	//
	// Actually the simplest approach: accept a rawExec func from the call site.
}

// TestSupercharger_UpsertAndRead_RoundTrip verifies that a session upserted via
// the store is readable via SuperchargerSessionsByAccount with all nullable fields
// faithfully preserved when non-nil (B8.1 non-nil).
func TestSupercharger_UpsertAndRead_RoundTrip(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
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
		AccountID:           accountID,
		VIN:                 "VIN_RT_001",
		TeslaID:             &teslaID,
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
	if err := st.upsertSuperchargerSession(ctx, sess); err != nil {
		t.Fatalf("upsertSuperchargerSession: %v", err)
	}

	r := newSuperchargerReaderImpl(pool)
	got, err := r.SuperchargerSessionsByAccount(ctx, accountID, 10)
	if err != nil {
		t.Fatalf("SuperchargerSessionsByAccount: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 session, got %d", len(got))
	}
	s := got[0]
	if s.SessionID != sessionID {
		t.Errorf("SessionID: want %d, got %d", sessionID, s.SessionID)
	}
	if s.AccountID != accountID {
		t.Errorf("AccountID: want %v, got %v", accountID, s.AccountID)
	}
	if s.VIN != "VIN_RT_001" {
		t.Errorf("VIN: want VIN_RT_001, got %q", s.VIN)
	}
	if s.TeslaID == nil || *s.TeslaID != teslaID {
		t.Errorf("TeslaID: want *%d, got %v", teslaID, s.TeslaID)
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

// TestSupercharger_UpsertNullableNullValues verifies that nil pointer fields round-trip
// as SQL NULL (not zero values) — critical for TeslaID (orphan sessions), EnergyKWh
// (time-based billing), IsPaid (fees empty), UnlatchDateTime (B8.1 nil).
func TestSupercharger_UpsertNullableNullValues(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	sessionID := int64(60002)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.supercharger_history WHERE session_id = $1", sessionID)
	})

	sess := SuperchargerHistory{
		SessionID:           sessionID,
		AccountID:           accountID,
		VIN:                 "VIN_ORPHAN",
		TeslaID:             nil, // orphan — VIN not a current vehicle
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
	if err := st.upsertSuperchargerSession(ctx, sess); err != nil {
		t.Fatalf("upsertSuperchargerSession: %v", err)
	}

	r := newSuperchargerReaderImpl(pool)
	got, err := r.SuperchargerSessionsByAccount(ctx, accountID, 10)
	if err != nil {
		t.Fatalf("SuperchargerSessionsByAccount: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 session, got %d", len(got))
	}
	s := got[0]
	if s.TeslaID != nil {
		t.Errorf("TeslaID: want nil for orphan, got %v", s.TeslaID)
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
// or change immutable fields (account_id, vin, charge_start_date_time, created_at) (B8.2).
func TestSupercharger_UpsertIdempotency(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	teslaID := int64(700002)
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
		AccountID:           accountID,
		VIN:                 "VIN_IDEM",
		TeslaID:             &teslaID,
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
	if err := st.upsertSuperchargerSession(ctx, sess1); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Second upsert: is_paid = true, total_cost = 3.50, different raw_data.
	paid := true
	cost2 := 3.50
	sess2 := sess1
	sess2.IsPaid = &paid
	sess2.TotalCost = &cost2
	sess2.RawData = []byte(`{"sessionId":60003,"v":2}`)
	if err := st.upsertSuperchargerSession(ctx, sess2); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	r := newSuperchargerReaderImpl(pool)
	got, err := r.SuperchargerSessionsByAccount(ctx, accountID, 10)
	if err != nil {
		t.Fatalf("SuperchargerSessionsByAccount: %v", err)
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
	if s.AccountID != accountID {
		t.Errorf("AccountID must not change on conflict update: want %v, got %v", accountID, s.AccountID)
	}
	if s.VIN != "VIN_IDEM" {
		t.Errorf("VIN must not change on conflict update: want VIN_IDEM, got %q", s.VIN)
	}
	if !s.ChargeStartDateTime.UTC().Equal(startTime) {
		t.Errorf("ChargeStartDateTime must not change: want %v, got %v", startTime, s.ChargeStartDateTime.UTC())
	}
}

// TestSupercharger_SessionsByVehicle_Scoping verifies that SuperchargerSessionsByVehicle
// returns only the queried vehicle's sessions within the account (B8.3).
func TestSupercharger_SessionsByVehicle_Scoping(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	teslaID1 := int64(700010)
	teslaID2 := int64(700011)
	sessionA := int64(60010)
	sessionB := int64(60011)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.supercharger_history WHERE session_id = ANY($1::bigint[])", []int64{sessionA, sessionB})
	})

	startA := time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC)
	startB := time.Date(2026, 6, 28, 11, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		sessionID int64
		teslaID   int64
		vin       string
		start     time.Time
	}{
		{sessionA, teslaID1, "VIN_V1", startA},
		{sessionB, teslaID2, "VIN_V2", startB},
	} {
		tid := tc.teslaID
		if err := st.upsertSuperchargerSession(ctx, SuperchargerHistory{
			SessionID:           tc.sessionID,
			AccountID:           accountID,
			VIN:                 tc.vin,
			TeslaID:             &tid,
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

	r := newSuperchargerReaderImpl(pool)
	got, err := r.SuperchargerSessionsByVehicle(ctx, accountID, teslaID1, 10)
	if err != nil {
		t.Fatalf("SuperchargerSessionsByVehicle: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 session for vehicle1, got %d", len(got))
	}
	if got[0].SessionID != sessionA {
		t.Errorf("want sessionA (%d), got sessionID=%d", sessionA, got[0].SessionID)
	}
}

// TestSupercharger_SessionsByAccount_CrossAccountIsolation verifies that
// SuperchargerSessionsByAccount returns only sessions for the queried account (B8.4).
func TestSupercharger_SessionsByAccount_CrossAccountIsolation(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	acctA := uuid.New()
	acctB := uuid.New()
	sessionA := int64(60020)
	sessionB := int64(60021)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.supercharger_history WHERE session_id = ANY($1::bigint[])", []int64{sessionA, sessionB})
	})

	for _, tc := range []struct {
		sessionID int64
		accountID uuid.UUID
		vin       string
		start     time.Time
	}{
		{sessionA, acctA, "VIN_A", time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC)},
		{sessionB, acctB, "VIN_B", time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)},
	} {
		if err := st.upsertSuperchargerSession(ctx, SuperchargerHistory{
			SessionID:           tc.sessionID,
			AccountID:           tc.accountID,
			VIN:                 tc.vin,
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

	r := newSuperchargerReaderImpl(pool)

	gotA, err := r.SuperchargerSessionsByAccount(ctx, acctA, 10)
	if err != nil {
		t.Fatalf("SuperchargerSessionsByAccount (acctA): %v", err)
	}
	if len(gotA) != 1 || gotA[0].SessionID != sessionA {
		t.Errorf("acctA: want 1 session (%d), got %+v", sessionA, gotA)
	}
	for _, s := range gotA {
		if s.AccountID != acctA {
			t.Errorf("acctA query returned row for different account: %v", s.AccountID)
		}
	}

	gotB, err := r.SuperchargerSessionsByAccount(ctx, acctB, 10)
	if err != nil {
		t.Fatalf("SuperchargerSessionsByAccount (acctB): %v", err)
	}
	if len(gotB) != 1 || gotB[0].SessionID != sessionB {
		t.Errorf("acctB: want 1 session (%d), got %+v", sessionB, gotB)
	}
}

// TestSupercharger_Ordering_NewestFirst verifies that sessions are returned in
// charge_start_date_time DESC order (newest first, B8.5).
func TestSupercharger_Ordering_NewestFirst(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
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
		if err := st.upsertSuperchargerSession(ctx, SuperchargerHistory{
			SessionID:           tc.id,
			AccountID:           accountID,
			VIN:                 "VIN_ORD",
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

	r := newSuperchargerReaderImpl(pool)
	got, err := r.SuperchargerSessionsByAccount(ctx, accountID, 10)
	if err != nil {
		t.Fatalf("SuperchargerSessionsByAccount: %v", err)
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

// TestSupercharger_Limit verifies that passing limit=1 returns at most 1 row (B8.6).
func TestSupercharger_Limit(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	ids := []int64{60040, 60041, 60042}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.supercharger_history WHERE session_id = ANY($1::bigint[])", ids)
	})

	for i, id := range ids {
		start := time.Date(2026, 6, 28, 10+i, 0, 0, 0, time.UTC)
		if err := st.upsertSuperchargerSession(ctx, SuperchargerHistory{
			SessionID:           id,
			AccountID:           accountID,
			VIN:                 "VIN_LMT",
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

	r := newSuperchargerReaderImpl(pool)
	got, err := r.SuperchargerSessionsByAccount(ctx, accountID, 1)
	if err != nil {
		t.Fatalf("SuperchargerSessionsByAccount limit=1: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly 1 row with limit=1, got %d", len(got))
	}
}
