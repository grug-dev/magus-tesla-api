package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/analytics"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
	"github.com/cristianpena/magus-tesla-api/internal/tesla"
)

// --- fakes for the account and tesla interfaces (no DB, no real Tesla) ---

// fakeAccount simulates the account module's per-user data. It carries either a
// set of already-registered vehicles (so the dashboard reads from the DB and
// never calls Tesla), or a token + the scenario for a one-time Tesla seed path.
type fakeAccount struct {
	registered []account.Vehicle
	regErr     error
	token      string
	tokenErr   error
	seedErr    error
	// seedCalls captures the SeedVehicles invocation so a test can assert the
	// one-time Tesla call was persisted.
	seedCalls int
	// lastSeedVehicles captures the SeedVehicle list from the most recent SeedVehicles call.
	lastSeedVehicles []account.SeedVehicle

	// language is the value LanguageFor returns. The zero value ("") falls back
	// to account.LanguageES, so every pre-existing test (which never sets this
	// field) keeps its original LanguageFor behavior unchanged.
	language string
	// languageErr, when set, makes LanguageFor return this error instead.
	languageErr error
	// setLanguageErr, when set, makes SetLanguage return this error instead of
	// recording success.
	setLanguageErr error
	// setLanguageCalls captures every SetLanguage invocation (id + lang) so a
	// test can assert LangSwitch/GoogleCallback called it with the right values
	// — or, for the anonymous/no-cookie paths, that it was NOT called at all.
	setLanguageCalls []struct {
		ID   uuid.UUID
		Lang string
	}
}

func (f fakeAccount) UpsertFromOAuth(context.Context, account.OAuthIdentity) (account.Account, error) {
	return account.Account{}, nil
}
func (f fakeAccount) SaveTeslaTokens(context.Context, uuid.UUID, account.TeslaTokens) error {
	return nil
}
func (f fakeAccount) AccessTokenFor(context.Context, uuid.UUID) (string, error) {
	return f.token, f.tokenErr
}
func (f fakeAccount) RegisteredVehicles(context.Context, uuid.UUID) ([]account.Vehicle, error) {
	return f.registered, f.regErr
}

// AllRegisteredVehicles satisfies the widened account.Service (used by the telemetry
// collector, not by the gateway); the gateway never calls it, so a stub suffices.
func (f fakeAccount) AllRegisteredVehicles(context.Context) ([]account.OwnedVehicle, error) {
	return nil, nil
}

// SetVehicleConfigIfEmpty satisfies the widened account.Service (written by the telemetry
// collector, not by the gateway); the gateway never calls it, so a stub suffices.
func (f fakeAccount) SetVehicleConfigIfEmpty(context.Context, uuid.UUID, int64, string, string) error {
	return nil
}

// LanguageFor / SetLanguage satisfy account.Service. LanguageFor returns f.language
// (defaulting to account.LanguageES for every pre-existing test that never sets
// it, so a handler reading it always sees a valid code, never ""), or f.languageErr
// when set. SetLanguage records every call in setLanguageCalls and returns
// f.setLanguageErr (nil by default).
func (f *fakeAccount) LanguageFor(context.Context, uuid.UUID) (string, error) {
	if f.languageErr != nil {
		return "", f.languageErr
	}
	if f.language == "" {
		return account.LanguageES, nil
	}
	return f.language, nil
}
func (f *fakeAccount) SetLanguage(_ context.Context, id uuid.UUID, lang string) error {
	f.setLanguageCalls = append(f.setLanguageCalls, struct {
		ID   uuid.UUID
		Lang string
	}{ID: id, Lang: lang})
	return f.setLanguageErr
}

func (f *fakeAccount) SeedVehicles(_ context.Context, _ uuid.UUID, vs []account.SeedVehicle) ([]account.Vehicle, error) {
	f.seedCalls++
	f.lastSeedVehicles = vs
	if f.seedErr != nil {
		return nil, f.seedErr
	}
	out := make([]account.Vehicle, 0, len(vs))
	for _, v := range vs {
		out = append(out, account.Vehicle{TeslaID: v.TeslaID, VIN: v.VIN, DisplayName: v.DisplayName})
	}
	return out, nil
}

type fakeTesla struct {
	vehicles []tesla.VehicleTesla
	listErr  error
}

func (f fakeTesla) ListVehicles(context.Context, tesla.Credentials) ([]tesla.VehicleTesla, error) {
	return f.vehicles, f.listErr
}
func (f fakeTesla) VehicleData(context.Context, tesla.Credentials, int64) (*tesla.VehicleDataTesla, json.RawMessage, error) {
	return nil, nil, nil
}
func (f fakeTesla) WakeUp(context.Context, tesla.Credentials, int64) (*tesla.VehicleTesla, error) {
	return nil, nil
}
func (f fakeTesla) ChargingHistory(context.Context, tesla.Credentials, tesla.ChargingHistoryParams) (*tesla.ChargingHistoryTesla, error) {
	return nil, nil
}

// fakeReader is a test double for telemetry.Reader. Returns the configured
// snapshots or error — no DB or network.
type fakeReader struct {
	snapshots []telemetry.Snapshot
	err       error
}

func (f *fakeReader) LatestSnapshotsByAccount(_ context.Context, _ uuid.UUID) ([]telemetry.Snapshot, error) {
	return f.snapshots, f.err
}

// SnapshotsByVehicleSince is a stub satisfying the telemetry.Reader interface
// (added by telemetry-add-snapshot-history-read-port). Tests that need history
// data may embed or extend fakeReader; the default returns nil, nil.
func (f *fakeReader) SnapshotsByVehicleSince(_ context.Context, _ uuid.UUID, _ int64, _ time.Time) ([]telemetry.Snapshot, error) {
	return nil, nil
}

// SnapshotsByVehicleBetween is a stub satisfying the telemetry.Reader interface
// (added by RM8-telemetry-between-range-port). The history handler is rewired to
// Between in tier 2 (RM8-gateway-history-date-range); until then the default
// returns nil, nil.
func (f *fakeReader) SnapshotsByVehicleBetween(_ context.Context, _ uuid.UUID, _ int64, _ time.Time, _ time.Time) ([]telemetry.Snapshot, error) {
	return nil, nil
}

// SnapshotsByVehicleUpdatedSince satisfies the telemetry.Reader method added by
// RM29-analytics-add-vehicle-metrics task 1.2. No gateway handler calls it --
// it exists for analytics' recompute watermark -- so this returns the same
// no-op as SnapshotsByVehicleBetween immediately above.
func (f *fakeReader) SnapshotsByVehicleUpdatedSince(_ context.Context, _ uuid.UUID, _ int64, _ time.Time) ([]telemetry.Snapshot, error) {
	return nil, nil
}

// SnapshotPrecedingDay satisfies the telemetry.Reader method added by
// RM29-telemetry-drop-derived-columns task 1.2. Like SnapshotsByVehicleUpdatedSince
// above, no gateway handler calls it -- it exists so analytics can fetch the exact
// predecessor of a day it is recomputing, however far back that row sits -- so this
// returns the same no-op. A nil *Snapshot with a nil error is the port's documented
// "no predecessor exists" answer, not an error case.
func (f *fakeReader) SnapshotPrecedingDay(_ context.Context, _ uuid.UUID, _ int64, _ time.Time) (*telemetry.Snapshot, error) {
	return nil, nil
}

// newHandler builds a Handler for tests that don't involve telemetry (seeds, connect
// flows, etc.). The TelemetryReader is left nil — it won't be reached in those paths.
func newHandler(acct account.Service, tsvc tesla.VehicleService) *Handler {
	return New(Deps{Account: acct, Tesla: tsvc})
}

// newHandlerWithReader builds a Handler with a fake telemetry.Reader. Kept for
// the paths that still call telemetryReader (history.go's
// SnapshotsByVehicleBetween — see history_test.go's newHandlerForHistory) and
// for pre-existing tests that pass an unused reader through routes which never
// reach it (e.g. VehicleSelect).
func newHandlerWithReader(acct account.Service, tsvc tesla.VehicleService, reader telemetry.Reader) *Handler {
	return New(Deps{Account: acct, Tesla: tsvc, TelemetryReader: reader})
}

// newHandlerWithAnalytics builds a Handler with a fake analytics.Reader for
// tests that exercise vehiclesFor/dashboardFor/navHeaderFor's enriched-vehicle
// path (RM38-gateway-read-dashboard-from-metrics: these three call sites now
// read h.analyticsReader.LatestMetricsByAccount instead of
// h.telemetryReader.LatestSnapshotsByAccount). Mirrors newHandlerWithReader's
// shape for the new port.
func newHandlerWithAnalytics(acct account.Service, tsvc tesla.VehicleService, reader analytics.Reader) *Handler {
	return New(Deps{Account: acct, Tesla: tsvc, AnalyticsReader: reader})
}

// --- pre-existing tests (unchanged behavior) ---

func TestVehiclesFor_RegisteredRendersWithoutTeslaCall(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1, VIN: "VIN1", DisplayName: "Magus"},
		{TeslaID: 2, VIN: "VIN2", DisplayName: "Second"},
	}}
	tsvc := &fakeTesla{vehicles: []tesla.VehicleTesla{
		{DisplayName: "SHOULD NOT BE CALLED", VIN: "NEVER"},
	}}
	// RM38: vehiclesFor now reads h.analyticsReader.LatestMetricsByAccount
	// instead of h.telemetryReader.LatestSnapshotsByAccount.
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{}}
	h := newHandlerWithAnalytics(acct, tsvc, reader)
	d := h.vehiclesFor(context.Background(), uuid.New())
	if len(d.Vehicles) != 2 {
		t.Fatalf("want 2 vehicles from the registry, got %d (%+v)", len(d.Vehicles), d)
	}
	if d.Vehicles[0].DisplayName != "Magus" || d.Vehicles[0].VIN != "VIN1" {
		t.Errorf("unexpected mapping: %+v", d.Vehicles[0])
	}
	if acct.seedCalls != 0 {
		t.Errorf("expected no Tesla seed when vehicles are already registered, got %d seed calls", acct.seedCalls)
	}
}

func TestVehiclesFor_EmptyRegistryTriggersTeslaSeed(t *testing.T) {
	acct := &fakeAccount{token: "tok"}
	tsvc := &fakeTesla{vehicles: []tesla.VehicleTesla{
		{ID: 10, DisplayName: "Magus", VIN: "VIN10", State: "online"},
		{ID: 20, DisplayName: "Second", VIN: "VIN20", State: "asleep"},
	}}
	h := newHandler(acct, tsvc)
	d := h.vehiclesFor(context.Background(), uuid.New())
	if len(d.Vehicles) != 2 {
		t.Fatalf("want 2 seeded vehicles, got %d (%+v)", len(d.Vehicles), d)
	}
	if d.Vehicles[0].DisplayName != "Magus" || d.Vehicles[0].VIN != "VIN10" {
		t.Errorf("unexpected seeded mapping: %+v", d.Vehicles[0])
	}
	if acct.seedCalls != 1 {
		t.Errorf("expected exactly one SeedVehicles call, got %d", acct.seedCalls)
	}
}

func TestVehiclesFor_NoStateRendered(t *testing.T) {
	// Registered vehicles carry no State; the presentation model has no State field.
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1, VIN: "VIN1", DisplayName: "Magus"},
	}}
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{}}
	h := newHandlerWithAnalytics(acct, fakeTesla{}, reader)
	d := h.vehiclesFor(context.Background(), uuid.New())
	if len(d.Vehicles) != 1 {
		t.Fatalf("want 1 vehicle, got %d", len(d.Vehicles))
	}
	// fragments.Vehicle has no State field — this assertion is enforced by the
	// compiler; here we just confirm display name + VIN are the surfaced fields.
	if d.Vehicles[0].DisplayName != "Magus" || d.Vehicles[0].VIN != "VIN1" {
		t.Errorf("unexpected vehicle fields: %+v", d.Vehicles[0])
	}
}

func TestVehiclesFor_NoConnectionPromptsConnect(t *testing.T) {
	h := newHandler(&fakeAccount{tokenErr: account.ErrNoTeslaConnection}, fakeTesla{})
	d := h.vehiclesFor(context.Background(), uuid.New())
	if !d.NeedsConnect || len(d.Vehicles) != 0 {
		t.Fatalf("want NeedsConnect and no vehicles, got %+v", d)
	}
}

func TestVehiclesFor_UnauthorizedPromptsReconnect(t *testing.T) {
	h := newHandler(&fakeAccount{token: "tok"}, fakeTesla{listErr: tesla.ErrUnauthorized})
	d := h.vehiclesFor(context.Background(), uuid.New())
	if !d.NeedsConnect || d.Notice == "" {
		t.Fatalf("want a reconnect notice with NeedsConnect, got %+v", d)
	}
}

func TestVehiclesFor_EmptyTeslaListShowsNotice(t *testing.T) {
	h := newHandler(&fakeAccount{token: "tok"}, fakeTesla{vehicles: nil})
	d := h.vehiclesFor(context.Background(), uuid.New())
	if len(d.Vehicles) != 0 || d.NeedsConnect || d.Notice == "" {
		t.Fatalf("want a plain notice (no vehicles, no connect prompt), got %+v", d)
	}
}

// --- new tests for telemetry enrichment (task 3.2) ---

// boolPtr is a helper to take the address of a bool literal in tests.
func boolPtr(b bool) *bool { return &b }

func TestVehiclesFor_EnrichedCard(t *testing.T) {
	capturedAt := time.Now().Add(-1 * time.Hour) // fresh, within 36 h
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 42, VIN: "VIN42", DisplayName: "Magus"},
	}}
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{
		{
			TeslaID:         42,
			CapturedAt:      &capturedAt,
			BatteryLevelPct: 80,
			BatteryRangeKm:  321.8688, // already km — 200 mi * 1.609344, converted at capture time (tier 2)
			ChargingState:   ptrString("Disconnected"),
			OdometerKm:      19312.128, // already km — 12000 mi * 1.609344, converted at capture time (tier 2)
			InsideTempC:     ptrF64(22.5),
			OutsideTempC:    ptrF64(15.0),
			Locked:          boolPtr(true),
			SentryMode:      boolPtr(true),
		},
	}}
	h := newHandlerWithAnalytics(acct, fakeTesla{}, reader)
	d := h.vehiclesFor(context.Background(), uuid.New())

	if len(d.Vehicles) != 1 {
		t.Fatalf("want 1 vehicle, got %d", len(d.Vehicles))
	}
	v := d.Vehicles[0]
	if !v.HasSnapshot {
		t.Errorf("want HasSnapshot true, got false")
	}
	if v.Battery != "80%" {
		t.Errorf("want Battery %q, got %q", "80%", v.Battery)
	}
	// Literal expected strings (design D1/3.3): mapVehicles formats with "%.1f km"
	// directly off the already-km fields — no method to re-derive from.
	if v.BatteryRange != "321.9 km" {
		t.Errorf("want BatteryRange %q, got %q", "321.9 km", v.BatteryRange)
	}
	if v.ChargingState != "Disconnected" {
		t.Errorf("want ChargingState Disconnected, got %q", v.ChargingState)
	}
	if v.Odometer != "19312.1 km" {
		t.Errorf("want Odometer %q, got %q", "19312.1 km", v.Odometer)
	}
	if v.InsideTemp != "22.5 °C" {
		t.Errorf("want InsideTemp %q, got %q", "22.5 °C", v.InsideTemp)
	}
	if v.OutsideTemp != "15.0 °C" {
		t.Errorf("want OutsideTemp %q, got %q", "15.0 °C", v.OutsideTemp)
	}
	if v.Locked == nil || !*v.Locked {
		t.Errorf("want Locked *true, got %v", v.Locked)
	}
	if v.SentryMode == nil || !*v.SentryMode {
		t.Errorf("want SentryMode *true, got %v", v.SentryMode)
	}
	if v.LastUpdated == "" {
		t.Errorf("want LastUpdated set, got empty string")
	}
	if v.IsStale {
		t.Errorf("want IsStale false for a 1h-old snapshot, got true")
	}
}

func TestVehiclesFor_PlaceholderCard(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 7, VIN: "VIN7", DisplayName: "Ghost"},
	}}
	// Reader returns an empty slice — no status for this vehicle.
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{}}
	h := newHandlerWithAnalytics(acct, fakeTesla{}, reader)
	d := h.vehiclesFor(context.Background(), uuid.New())

	if len(d.Vehicles) != 1 {
		t.Fatalf("want 1 vehicle, got %d", len(d.Vehicles))
	}
	v := d.Vehicles[0]
	if v.HasSnapshot {
		t.Errorf("want HasSnapshot false, got true")
	}
	if v.Battery != "" || v.BatteryRange != "" || v.Odometer != "" || v.InsideTemp != "" || v.OutsideTemp != "" {
		t.Errorf("want all display string fields empty for placeholder, got Battery=%q BatteryRange=%q Odometer=%q InsideTemp=%q OutsideTemp=%q",
			v.Battery, v.BatteryRange, v.Odometer, v.InsideTemp, v.OutsideTemp)
	}
	if v.LastUpdated != "" {
		t.Errorf("want LastUpdated empty for placeholder, got %q", v.LastUpdated)
	}
	if v.IsStale {
		t.Errorf("placeholder card must not have IsStale set")
	}
}

func TestVehiclesFor_GracefulDegradation(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 9, VIN: "VIN9", DisplayName: "Bricked"},
	}}
	reader := &fakeAnalyticsReader{statusesErr: errors.New("db unavailable")}
	h := newHandlerWithAnalytics(acct, fakeTesla{}, reader)
	d := h.vehiclesFor(context.Background(), uuid.New())

	if d.Notice == "" {
		t.Errorf("want a degradation notice when Reader errors, got empty notice")
	}
	if len(d.Vehicles) != 1 {
		t.Fatalf("want 1 vehicle in placeholder state, got %d", len(d.Vehicles))
	}
	if d.Vehicles[0].HasSnapshot {
		t.Errorf("want HasSnapshot false on all vehicles after Reader error")
	}
}

func TestVehiclesFor_StaleBoundary(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1, VIN: "VIN1", DisplayName: "Magus"},
	}}

	// Clearly within the threshold (10 s of headroom) — should NOT be stale.
	withinThreshold := time.Now().Add(-stalenessThreshold + 10*time.Second)
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{
		{TeslaID: 1, CapturedAt: &withinThreshold},
	}}
	h := newHandlerWithAnalytics(acct, fakeTesla{}, reader)
	d := h.vehiclesFor(context.Background(), uuid.New())
	if len(d.Vehicles) != 1 {
		t.Fatalf("want 1 vehicle, got %d", len(d.Vehicles))
	}
	if d.Vehicles[0].IsStale {
		t.Errorf("want IsStale false when 10 s within threshold, got true")
	}

	// One second past the threshold — should be stale.
	pastThreshold := time.Now().Add(-stalenessThreshold - time.Second)
	reader2 := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{
		{TeslaID: 1, CapturedAt: &pastThreshold},
	}}
	h2 := newHandlerWithAnalytics(acct, fakeTesla{}, reader2)
	d2 := h2.vehiclesFor(context.Background(), uuid.New())
	if len(d2.Vehicles) != 1 {
		t.Fatalf("want 1 vehicle, got %d", len(d2.Vehicles))
	}
	if !d2.Vehicles[0].IsStale {
		t.Errorf("want IsStale true when 1 s past threshold, got false")
	}
}

// TestIsStale verifies the exact-at-threshold and boundary semantics of the isStale
// helper using a fixed clock — no wall-clock dependency, fully deterministic.
func TestIsStale(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	// Exactly at threshold: duration == stalenessThreshold, NOT stale (strict >).
	if isStale(now.Add(-stalenessThreshold), now) {
		t.Errorf("want isStale false at exactly threshold (strict >), got true")
	}

	// One nanosecond past threshold: duration > stalenessThreshold, IS stale.
	if !isStale(now.Add(-stalenessThreshold-time.Nanosecond), now) {
		t.Errorf("want isStale true one nanosecond past threshold, got false")
	}

	// One nanosecond within threshold: duration < stalenessThreshold, NOT stale.
	if isStale(now.Add(-stalenessThreshold+time.Nanosecond), now) {
		t.Errorf("want isStale false one nanosecond within threshold, got true")
	}
}

// TestConnectedAt verifies the exact-at-threshold and boundary semantics of the
// connectedAt nav-header helper using a fixed clock — fully deterministic. This
// mirrors TestIsStale, but for the 48 h connectedFreshnessWindow boundary (DD3).
func TestConnectedAt(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	// Exactly at window: duration == connectedFreshnessWindow, STILL connected
	// (<= window — the strict inverse of isStale's strict >).
	if !connectedAt(now.Add(-connectedFreshnessWindow), now) {
		t.Errorf("want connectedAt true at exactly window (<= boundary), got false")
	}

	// One nanosecond past the window: duration > window, NOT connected (asleep).
	if connectedAt(now.Add(-connectedFreshnessWindow-time.Nanosecond), now) {
		t.Errorf("want connectedAt false one nanosecond past window, got true")
	}

	// One nanosecond within the window: connected.
	if !connectedAt(now.Add(-connectedFreshnessWindow+time.Nanosecond), now) {
		t.Errorf("want connectedAt true one nanosecond within window, got false")
	}
}

func TestVehiclesFor_SentryModeThreeStates(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1, VIN: "VIN1", DisplayName: "Nil"},
		{TeslaID: 2, VIN: "VIN2", DisplayName: "Off"},
		{TeslaID: 3, VIN: "VIN3", DisplayName: "On"},
	}}
	now := time.Now()
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{
		{TeslaID: 1, CapturedAt: &now, SentryMode: nil},
		{TeslaID: 2, CapturedAt: &now, SentryMode: boolPtr(false)},
		{TeslaID: 3, CapturedAt: &now, SentryMode: boolPtr(true)},
	}}
	h := newHandlerWithAnalytics(acct, fakeTesla{}, reader)
	d := h.vehiclesFor(context.Background(), uuid.New())

	if len(d.Vehicles) != 3 {
		t.Fatalf("want 3 vehicles, got %d", len(d.Vehicles))
	}
	// Build a lookup by DisplayName for readability.
	byName := make(map[string]*bool)
	for _, v := range d.Vehicles {
		byName[v.DisplayName] = v.SentryMode
	}

	if byName["Nil"] != nil {
		t.Errorf("want SentryMode nil for 'Nil' vehicle, got %v", byName["Nil"])
	}
	if byName["Off"] == nil || *byName["Off"] != false {
		t.Errorf("want SentryMode *false for 'Off' vehicle, got %v", byName["Off"])
	}
	if byName["On"] == nil || *byName["On"] != true {
		t.Errorf("want SentryMode *true for 'On' vehicle, got %v", byName["On"])
	}
}

// TestVehiclesFor_LockedThreeStates mirrors
// TestVehiclesFor_SentryModeThreeStates immediately above, for the Locked
// field's three-way nil/false/true state (tasks.md 5.6,
// RM38-gateway-read-dashboard-from-metrics design.md D3): fragments.
// Vehicle.Locked is now *bool, sourced verbatim from analytics.VehicleStatus.
// Locked with no fabricated value on nil.
func TestVehiclesFor_LockedThreeStates(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1, VIN: "VIN1", DisplayName: "Nil"},
		{TeslaID: 2, VIN: "VIN2", DisplayName: "Unlocked"},
		{TeslaID: 3, VIN: "VIN3", DisplayName: "Locked"},
	}}
	now := time.Now()
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{
		{TeslaID: 1, CapturedAt: &now, Locked: nil},
		{TeslaID: 2, CapturedAt: &now, Locked: boolPtr(false)},
		{TeslaID: 3, CapturedAt: &now, Locked: boolPtr(true)},
	}}
	h := newHandlerWithAnalytics(acct, fakeTesla{}, reader)
	d := h.vehiclesFor(context.Background(), uuid.New())

	if len(d.Vehicles) != 3 {
		t.Fatalf("want 3 vehicles, got %d", len(d.Vehicles))
	}
	byName := make(map[string]*bool)
	for _, v := range d.Vehicles {
		byName[v.DisplayName] = v.Locked
	}

	if byName["Nil"] != nil {
		t.Errorf("want Locked nil for 'Nil' vehicle, got %v", byName["Nil"])
	}
	if byName["Unlocked"] == nil || *byName["Unlocked"] != false {
		t.Errorf("want Locked *false for 'Unlocked' vehicle, got %v", byName["Unlocked"])
	}
	if byName["Locked"] == nil || *byName["Locked"] != true {
		t.Errorf("want Locked *true for 'Locked' vehicle, got %v", byName["Locked"])
	}
}

// --- dashboardFor tests (single-vehicle bento VM) ---
//
// dashboardFor is the single-vehicle dashboard's core logic. These mirror the
// vehiclesFor coverage for the degradation paths (NeedsConnect, telemetry error,
// placeholder) and add the enriched-snapshot + status/limit/version formatting
// cases unique to the dashboard VM. All drive the handler directly (no HTTP, no
// session), like the vehiclesFor tests above.

func TestDashboardFor_NoConnectionPromptsConnect(t *testing.T) {
	// No registered vehicles and no Tesla token → NeedsConnect (the dashboard
	// does not seed here; seeding is a vehiclesFor side effect called by the
	// Dashboard handler before dashboardFor).
	h := newHandler(&fakeAccount{tokenErr: account.ErrNoTeslaConnection}, fakeTesla{})
	d := h.dashboardFor(context.Background(), uuid.New(), 0, startOfDay(time.Now()))
	if !d.NeedsConnect {
		t.Fatalf("want NeedsConnect, got %+v", d)
	}
}

func TestDashboardFor_RegisteredEmptyPromptsConnect(t *testing.T) {
	// Registered list is empty (already-seeded account with zero vehicles) →
	// NeedsConnect, not an error.
	acct := &fakeAccount{registered: nil}
	reader := &fakeReader{snapshots: nil}
	h := newHandlerWithReader(acct, fakeTesla{}, reader)
	d := h.dashboardFor(context.Background(), uuid.New(), 0, startOfDay(time.Now()))
	if !d.NeedsConnect {
		t.Fatalf("want NeedsConnect for empty registry, got %+v", d)
	}
}

func TestDashboardFor_AccountReadErrorShowsNotice(t *testing.T) {
	acct := &fakeAccount{regErr: errors.New("db down")}
	h := newHandlerWithReader(acct, fakeTesla{}, &fakeReader{})
	d := h.dashboardFor(context.Background(), uuid.New(), 0, startOfDay(time.Now()))
	if d.NeedsConnect {
		t.Errorf("want NeedsConnect false on account error, got true")
	}
	if d.Notice == "" {
		t.Errorf("want a degradation Notice on account error, got empty")
	}
	if d.HasSnapshot {
		t.Errorf("want HasSnapshot false on account error")
	}
}

func TestDashboardFor_TelemetryErrorIsUnavailable(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1, VIN: "VIN1", DisplayName: "Magus"},
	}}
	reader := &fakeAnalyticsReader{statusesErr: errors.New("db unavailable")}
	h := newHandlerWithAnalytics(acct, fakeTesla{}, reader)
	d := h.dashboardFor(context.Background(), uuid.New(), 0, startOfDay(time.Now()))

	if !d.TelemetryUnavailable {
		t.Fatalf("want TelemetryUnavailable true, got false")
	}
	if d.Notice == "" {
		t.Errorf("want a Notice on telemetry error, got empty")
	}
	if d.HasSnapshot {
		t.Errorf("want HasSnapshot false on telemetry error")
	}
	if d.VehicleName != "Magus" {
		t.Errorf("want VehicleName preserved on telemetry error, got %q", d.VehicleName)
	}
}

func TestDashboardFor_PlaceholderWhenNoSnapshot(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 7, VIN: "VIN7", DisplayName: "Ghost"},
	}}
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{}}
	h := newHandlerWithAnalytics(acct, fakeTesla{}, reader)
	d := h.dashboardFor(context.Background(), uuid.New(), 0, startOfDay(time.Now()))

	if d.HasSnapshot {
		t.Fatalf("want HasSnapshot false, got true")
	}
	if d.TelemetryUnavailable {
		t.Errorf("want TelemetryUnavailable false for a normal no-snapshot state")
	}
	if d.Notice != "" {
		t.Errorf("want no Notice for placeholder state, got %q", d.Notice)
	}
	if d.VehicleName != "Ghost" {
		t.Errorf("want VehicleName Ghost, got %q", d.VehicleName)
	}
	// All metric fields must be empty (the template renders "—" placeholders).
	for name, v := range map[string]string{
		"StatusLabel": d.StatusLabel, "SoftwareVer": d.SoftwareVer,
		"Odometer": d.Odometer, "InsideTemp": d.InsideTemp,
		"Battery": d.Battery, "BatteryPct": d.BatteryPct,
		"RangeNow": d.RangeNow, "ChargeLimit": d.ChargeLimit,
		"LastUpdated": d.LastUpdated,
	} {
		if v != "" {
			t.Errorf("want %s empty for placeholder, got %q", name, v)
		}
	}
	if d.IsStale {
		t.Errorf("want IsStale false for placeholder")
	}
}

func TestDashboardFor_EnrichedBento(t *testing.T) {
	capturedAt := time.Now().Add(-1 * time.Hour) // fresh, within 36 h
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 42, VIN: "VIN42", DisplayName: "Magus"},
		// A second vehicle confirms selectedTeslaID selection, not just [0].
		{TeslaID: 99, VIN: "VIN99", DisplayName: "Other"},
	}}
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{
		{
			TeslaID:           42,
			CapturedAt:        &capturedAt,
			BatteryLevelPct:   80,
			BatteryRangeKm:    321.8688,                  // already km — %.0f rounds → "322 km"
			ChargingState:     ptrString("Disconnected"), // → "Parked"
			ChargeLimitSocPct: ptrInt(80),
			OdometerKm:        19312.128, // already km — formatKm rounds → "19,312 km"
			InsideTempC:       ptrF64(22.0),
			OutsideTempC:      ptrF64(15.0),
			CarVersion:        ptrString("2024.32.5"),
			Locked:            boolPtr(true),
			SentryMode:        boolPtr(false),
		},
	}}
	h := newHandlerWithAnalytics(acct, fakeTesla{}, reader)
	// English ctx so the StatusLabel assertion below (a real per-language
	// string, unlike SoftwareVer/ChargeLimit which are identical or not yet
	// translated) keeps comparing against the pre-existing literal — mirrors
	// TestDashStatus / tier 2's T6.4 precedent rather than asserting Spanish.
	ctx := i18n.WithLang(context.Background(), account.LanguageEN)
	d := h.dashboardFor(ctx, uuid.New(), 42, startOfDay(time.Now()))

	if !d.HasSnapshot {
		t.Fatalf("want HasSnapshot true, got false")
	}
	if d.NeedsConnect || d.TelemetryUnavailable {
		t.Fatalf("want a clean enriched state, got NeedsConnect=%v Unavailable=%v", d.NeedsConnect, d.TelemetryUnavailable)
	}
	if d.VehicleName != "Magus" {
		t.Errorf("want VehicleName Magus (selectedTeslaID=42), got %q", d.VehicleName)
	}
	if d.StatusLabel != "Parked" {
		t.Errorf("want StatusLabel Parked for Disconnected, got %q", d.StatusLabel)
	}
	if d.SoftwareVer != "Software v2024.32.5" {
		t.Errorf("want SoftwareVer %q, got %q", "Software v2024.32.5", d.SoftwareVer)
	}
	if d.Battery != "80%" {
		t.Errorf("want Battery %q, got %q", "80%", d.Battery)
	}
	if d.BatteryPct != "80" {
		t.Errorf("want BatteryPct %q (bare), got %q", "80", d.BatteryPct)
	}
	// Literal expected string (design D1/3.3): mapDashboardSnapshot formats with
	// "%.0f km" directly off the already-km BatteryRangeKm field.
	if d.RangeNow != "322 km" {
		t.Errorf("want RangeNow %q, got %q", "322 km", d.RangeNow)
	}
	if d.ChargeLimit != "Limit 80%" {
		t.Errorf("want ChargeLimit %q, got %q", "Limit 80%", d.ChargeLimit)
	}
	// formatKm rounds 19312.069... → 19312, then comma-groups → "19,312 km".
	wantOdo := "19,312 km"
	if d.Odometer != wantOdo {
		t.Errorf("want Odometer %q, got %q", wantOdo, d.Odometer)
	}
	if d.InsideTemp != "22 °C" {
		t.Errorf("want InsideTemp %q, got %q", "22 °C", d.InsideTemp)
	}
	if d.OutsideTemp != "15 °C" {
		t.Errorf("want OutsideTemp %q, got %q", "15 °C", d.OutsideTemp)
	}
	if d.LastUpdated == "" {
		t.Errorf("want LastUpdated set, got empty")
	}
	if d.IsStale {
		t.Errorf("want IsStale false for a 1h-old snapshot, got true")
	}
	if d.Locked == nil || !*d.Locked {
		t.Errorf("want Locked *true, got %v", d.Locked)
	}
	if d.SentryMode == nil || *d.SentryMode {
		t.Errorf("want SentryMode *false, got %v", d.SentryMode)
	}
}

func TestDashboardFor_ChargingStatus(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{{TeslaID: 1, VIN: "V1", DisplayName: "ChargingCar"}}}
	now := time.Now()
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{
		{TeslaID: 1, CapturedAt: &now, ChargingState: ptrString("Charging")},
	}}
	h := newHandlerWithAnalytics(acct, fakeTesla{}, reader)
	ctx := i18n.WithLang(context.Background(), account.LanguageEN)
	d := h.dashboardFor(ctx, uuid.New(), 0, startOfDay(time.Now()))
	if d.StatusLabel != "Charging" {
		t.Fatalf("want StatusLabel Charging, got %q", d.StatusLabel)
	}
}

func TestDashboardFor_StaleSnapshot(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{{TeslaID: 1, VIN: "V1", DisplayName: "StaleCar"}}}
	past := time.Now().Add(-stalenessThreshold - time.Second)
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{
		{TeslaID: 1, CapturedAt: &past, BatteryLevelPct: 50, OdometerKm: 1000.0, InsideTempC: ptrF64(20)},
	}}
	h := newHandlerWithAnalytics(acct, fakeTesla{}, reader)
	d := h.dashboardFor(context.Background(), uuid.New(), 0, startOfDay(time.Now()))
	if !d.HasSnapshot || !d.IsStale {
		t.Fatalf("want HasSnapshot + IsStale, got HasSnapshot=%v IsStale=%v", d.HasSnapshot, d.IsStale)
	}
}

func TestDashboardFor_SelectsDefaultsToFirst(t *testing.T) {
	// selectedTeslaID == 0 (no session selection yet) → dashboardFor uses
	// registered[0], mirroring resolveSelectedVehicle's auto-select.
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 5, VIN: "V5", DisplayName: "First"},
		{TeslaID: 6, VIN: "V6", DisplayName: "Second"},
	}}
	now := time.Now()
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{
		{TeslaID: 5, CapturedAt: &now, BatteryLevelPct: 30, OdometerKm: 1, InsideTempC: ptrF64(21)},
	}}
	h := newHandlerWithAnalytics(acct, fakeTesla{}, reader)
	d := h.dashboardFor(context.Background(), uuid.New(), 0, startOfDay(time.Now()))
	if d.VehicleName != "First" {
		t.Fatalf("want default to first vehicle, got %q", d.VehicleName)
	}
}

// --- mapDashboardSnapshot Test Contract fixtures (tasks.md 5.2, design.md
// "Test Contract") ---
//
// These two tests drive mapDashboardSnapshot DIRECTLY with the exact fixture
// values design.md authored before this tier's implementation existed
// (Fixture RM38-G-Full / RM38-G-Nil), asserting the exact expected-value
// tables rather than whatever the code happens to produce.

// fixtureRM38Now is the fixed "now" the Full/Nil fixtures' CapturedAt values
// are computed relative to, so LastUpdated/IsStale are deterministic.
var fixtureRM38Now = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

func TestMapDashboardSnapshot_FixtureFull(t *testing.T) {
	capturedAt := fixtureRM38Now.Add(-2 * time.Hour) // well within both windows
	fixture := analytics.VehicleStatus{
		TeslaID:           1001,
		BatteryLevelPct:   72,
		BatteryRangeKm:    310.4,
		OdometerKm:        18452.0,
		InsideTempC:       ptrF64(21.5),
		OutsideTempC:      ptrF64(9.0),
		Locked:            boolPtr(true),
		SentryMode:        boolPtr(true),
		CarVersion:        ptrString("2026.20.4"),
		ChargingState:     ptrString("Charging"),
		ChargeLimitSocPct: ptrInt(80),
		CapturedAt:        &capturedAt,
	}
	ctx := i18n.WithLang(context.Background(), account.LanguageEN)
	var vm fragments.DashboardData
	mapDashboardSnapshot(ctx, &vm, fixture, fixtureRM38Now)

	if !vm.HasSnapshot {
		t.Errorf("want HasSnapshot true, got false")
	}
	if want := i18n.T(ctx, i18n.KeyDashboardStatusCharging); vm.StatusLabel != want {
		t.Errorf("want StatusLabel %q, got %q", want, vm.StatusLabel)
	}
	if want := fmt.Sprintf(i18n.T(ctx, i18n.KeyDashboardStatusSoftwareVersion), "2026.20.4"); vm.SoftwareVer != want {
		t.Errorf("want SoftwareVer %q, got %q", want, vm.SoftwareVer)
	}
	if want := capturedAt.UTC().Format("2006-01-02"); vm.LastUpdated != want {
		t.Errorf("want LastUpdated %q, got %q", want, vm.LastUpdated)
	}
	if vm.IsStale {
		t.Errorf("want IsStale false, got true")
	}
	if vm.Odometer != "18,452 km" {
		t.Errorf("want Odometer %q, got %q", "18,452 km", vm.Odometer)
	}
	if vm.InsideTemp != "22 °C" {
		t.Errorf("want InsideTemp %q, got %q", "22 °C", vm.InsideTemp)
	}
	if vm.OutsideTemp != "9 °C" {
		t.Errorf("want OutsideTemp %q, got %q", "9 °C", vm.OutsideTemp)
	}
	if vm.Battery != "72%" {
		t.Errorf("want Battery %q, got %q", "72%", vm.Battery)
	}
	if vm.BatteryPct != "72" {
		t.Errorf("want BatteryPct %q, got %q", "72", vm.BatteryPct)
	}
	if vm.RangeNow != "310 km" {
		t.Errorf("want RangeNow %q, got %q", "310 km", vm.RangeNow)
	}
	if want := fmt.Sprintf(i18n.T(ctx, i18n.KeyDashboardChargeLimit), 80); vm.ChargeLimit != want {
		t.Errorf("want ChargeLimit %q, got %q", want, vm.ChargeLimit)
	}
	if vm.Locked == nil || !*vm.Locked {
		t.Errorf("want Locked *true, got %v", vm.Locked)
	}
	if vm.SentryMode == nil || !*vm.SentryMode {
		t.Errorf("want SentryMode *true, got %v", vm.SentryMode)
	}
}

func TestMapDashboardSnapshot_FixtureNil(t *testing.T) {
	fixture := analytics.VehicleStatus{
		TeslaID:         1001,
		BatteryLevelPct: 72,
		BatteryRangeKm:  310.4,
		OdometerKm:      18452.0,
		// every pointer field left nil — a pre-migration vehicle_metrics row.
	}
	ctx := i18n.WithLang(context.Background(), account.LanguageEN)
	var vm fragments.DashboardData
	mapDashboardSnapshot(ctx, &vm, fixture, fixtureRM38Now)

	if !vm.HasSnapshot {
		t.Errorf("want HasSnapshot true (the row exists), got false")
	}
	if want := i18n.T(ctx, i18n.KeyDashboardStatusParked); vm.StatusLabel != want {
		t.Errorf("want StatusLabel %q (nil ChargingState collapses to Parked), got %q", want, vm.StatusLabel)
	}
	if vm.SoftwareVer != "" {
		t.Errorf("want SoftwareVer empty (nil CarVersion), got %q", vm.SoftwareVer)
	}
	if vm.LastUpdated != "" {
		t.Errorf("want LastUpdated empty (nil CapturedAt), got %q", vm.LastUpdated)
	}
	if vm.IsStale {
		t.Errorf("want IsStale false (zero value, nil CapturedAt), got true")
	}
	if vm.InsideTemp != "—" {
		t.Errorf("want InsideTemp %q, got %q", "—", vm.InsideTemp)
	}
	if vm.OutsideTemp != "—" {
		t.Errorf("want OutsideTemp %q, got %q", "—", vm.OutsideTemp)
	}
	// Odometer/Battery/RangeNow are always non-pointer — unaffected by the nil fixture.
	if vm.Odometer != "18,452 km" {
		t.Errorf("want Odometer %q, got %q", "18,452 km", vm.Odometer)
	}
	if vm.Battery != "72%" {
		t.Errorf("want Battery %q, got %q", "72%", vm.Battery)
	}
	if vm.RangeNow != "310 km" {
		t.Errorf("want RangeNow %q, got %q", "310 km", vm.RangeNow)
	}
	if vm.ChargeLimit != "" {
		t.Errorf("want ChargeLimit empty (nil ChargeLimitSocPct), got %q", vm.ChargeLimit)
	}
	if vm.Locked != nil {
		t.Errorf("want Locked nil, got %v", vm.Locked)
	}
	if vm.SentryMode != nil {
		t.Errorf("want SentryMode nil, got %v", vm.SentryMode)
	}
}

// --- dashboardFor formatters (pure helpers) ---

func TestCommaGroup(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0", "0"}, {"12", "12"}, {"123", "123"},
		{"1234", "1,234"}, {"12345", "12,345"}, {"123456", "123,456"},
		{"1234567", "1,234,567"},
	}
	for _, c := range cases {
		if got := commaGroup(c.in); got != c.want {
			t.Errorf("commaGroup(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatKm(t *testing.T) {
	// 19312.07 → whole 19312 → "19,312 km"; 321.5 → "322 km".
	if got := formatKm(19312.069321); got != "19,312 km" {
		t.Errorf("formatKm(19312.07) = %q, want %q", got, "19,312 km")
	}
	if got := formatKm(321.5); got != "322 km" {
		t.Errorf("formatKm(321.5) = %q, want %q", got, "322 km")
	}
	if got := formatKm(999.4); got != "999 km" {
		t.Errorf("formatKm(999.4) = %q, want %q", got, "999 km")
	}
}

// ptrString returns a pointer to s -- a small test-local helper mirroring the
// existing ptrInt/ptrTime helpers in this package (supercharger_test.go,
// charges_tiles_test.go), added by RM38-gateway-read-dashboard-from-metrics for
// the new *string fields on analytics.VehicleStatus.
func ptrString(s string) *string { return &s }

func TestDashStatus(t *testing.T) {
	// dashStatus now takes a chargingState *string (design.md D2,
	// RM38-gateway-read-dashboard-from-metrics), replacing the telemetry.Snapshot
	// parameter -- nil collapses to Parked, identical to the pre-migration
	// empty-string case (tasks.md 2.2). Still resolves through the i18n catalogue
	// (design.md D5, RM24-gateway-translate-all-pages): assert against
	// i18n.T(ctx, key), not a literal string.
	ctx := i18n.WithLang(context.Background(), account.LanguageEN)
	for _, tc := range []struct {
		name   string
		charge *string
		want   i18n.Key
	}{
		{"nil", nil, i18n.KeyDashboardStatusParked},
		{"Charging", ptrString("Charging"), i18n.KeyDashboardStatusCharging},
		{"Stopped", ptrString("Stopped"), i18n.KeyDashboardStatusParked},
		{"Disconnected", ptrString("Disconnected"), i18n.KeyDashboardStatusParked},
		{"Complete", ptrString("Complete"), i18n.KeyDashboardStatusParked},
		{"empty string", ptrString(""), i18n.KeyDashboardStatusParked},
	} {
		want := i18n.T(ctx, tc.want)
		if got := dashStatus(ctx, tc.charge); got != want {
			t.Errorf("dashStatus(%s) = %q, want %q", tc.name, got, want)
		}
	}
}

// TestMergeVehicleStatuses covers mergeVehicleStatuses, the RM38 rename+retype of
// mergeSnapshots (design.md D6, tasks.md 2.3). No prior unit test exercised
// mergeSnapshots directly (confirmed by search before writing this test), so this
// is a new test rather than a literal rename -- it asserts the same two
// properties the Test Contract calls for: a nil/empty slice produces an empty
// map, and a populated slice is indexed for O(1) lookup by TeslaID.
func TestMergeVehicleStatuses(t *testing.T) {
	if got := mergeVehicleStatuses(nil); len(got) != 0 {
		t.Fatalf("mergeVehicleStatuses(nil) = %v, want empty map", got)
	}
	if got := mergeVehicleStatuses([]analytics.VehicleStatus{}); len(got) != 0 {
		t.Fatalf("mergeVehicleStatuses(empty slice) = %v, want empty map", got)
	}

	statuses := []analytics.VehicleStatus{
		{TeslaID: 1001, BatteryLevelPct: 72},
		{TeslaID: 2002, BatteryLevelPct: 40},
	}
	got := mergeVehicleStatuses(statuses)
	if len(got) != 2 {
		t.Fatalf("mergeVehicleStatuses(len=2 slice) = %d entries, want 2", len(got))
	}
	if got[1001].BatteryLevelPct != 72 {
		t.Errorf("mergeVehicleStatuses[1001].BatteryLevelPct = %d, want 72", got[1001].BatteryLevelPct)
	}
	if got[2002].BatteryLevelPct != 40 {
		t.Errorf("mergeVehicleStatuses[2002].BatteryLevelPct = %d, want 40", got[2002].BatteryLevelPct)
	}
	if _, ok := got[9999]; ok {
		t.Errorf("mergeVehicleStatuses lookup for an absent TeslaID unexpectedly found an entry")
	}
}

// --- Sub-task D: seed AccessType mapping tests (sub-task C) ---

// TestSeedAccessTypeMapping_NonEmpty verifies that a non-empty VehicleTesla.AccessType
// is mapped to a non-nil *string on the SeedVehicle passed to account.SeedVehicles.
func TestSeedAccessTypeMapping_NonEmpty(t *testing.T) {
	acct := &fakeAccount{token: "tok"}
	tsvc := &fakeTesla{vehicles: []tesla.VehicleTesla{
		{ID: 10, DisplayName: "Magus", VIN: "VIN10", State: "online", AccessType: "OWNER"},
	}}
	// newHandler: telemetryReader is nil; the seed path never reaches it.
	h := newHandler(acct, tsvc)
	_ = h.vehiclesFor(context.Background(), uuid.New())

	if acct.seedCalls != 1 {
		t.Fatalf("want exactly 1 SeedVehicles call, got %d", acct.seedCalls)
	}
	if len(acct.lastSeedVehicles) != 1 {
		t.Fatalf("want 1 SeedVehicle, got %d", len(acct.lastSeedVehicles))
	}
	sv := acct.lastSeedVehicles[0]
	if sv.AccessType == nil {
		t.Errorf("want non-nil AccessType for OWNER vehicle, got nil")
	} else if *sv.AccessType != "OWNER" {
		t.Errorf("want AccessType=OWNER, got %q", *sv.AccessType)
	}
}

// TestSeedAccessTypeMapping_Empty verifies that an empty VehicleTesla.AccessType
// maps to nil on SeedVehicle.AccessType (boundary-nil convention).
func TestSeedAccessTypeMapping_Empty(t *testing.T) {
	acct := &fakeAccount{token: "tok"}
	tsvc := &fakeTesla{vehicles: []tesla.VehicleTesla{
		{ID: 20, DisplayName: "Unknown", VIN: "VIN20", State: "asleep", AccessType: ""},
	}}
	h := newHandler(acct, tsvc)
	_ = h.vehiclesFor(context.Background(), uuid.New())

	if acct.seedCalls != 1 {
		t.Fatalf("want exactly 1 SeedVehicles call, got %d", acct.seedCalls)
	}
	if len(acct.lastSeedVehicles) != 1 {
		t.Fatalf("want 1 SeedVehicle, got %d", len(acct.lastSeedVehicles))
	}
	sv := acct.lastSeedVehicles[0]
	if sv.AccessType != nil {
		t.Errorf("want nil AccessType for empty-string vehicle, got %q", *sv.AccessType)
	}
}

// --- nav-header fragment (T4) ---
//
// navHeaderFor is the gin-free helper; these unit tests cover the six enumerated
// cases purely (no HTTP, no session), mirroring the vehiclesFor tests above. The
// fragment route's anonymous -> /login redirect is covered in gateway_test.go.

// newNavHeaderHandler builds a Handler wired with account + analytics fakes for
// the nav-header helper tests (Tesla is never reached by navHeaderFor).
// Retyped from telemetry.Reader to analytics.Reader
// (RM38-gateway-read-dashboard-from-metrics, design.md D8): navHeaderFor now
// reads h.analyticsReader.LatestMetricsByAccount.
func newNavHeaderHandler(acct account.Service, reader analytics.Reader) *Handler {
	return newHandlerWithAnalytics(acct, fakeTesla{}, reader)
}

func TestNavHeaderFor_Connected(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 42, VIN: "VIN42", DisplayName: "Magus"},
	}}
	// 1 h old — well within the 48 h window.
	capturedAt := time.Now().Add(-1 * time.Hour)
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{
		{TeslaID: 42, CapturedAt: &capturedAt, BatteryLevelPct: 94},
	}}
	h := newNavHeaderHandler(acct, reader)
	vm := h.navHeaderFor(context.Background(), uuid.New(), 0)

	if vm.NeedsConnect {
		t.Fatalf("want NeedsConnect false, got true")
	}
	if vm.VehicleName != "Magus" {
		t.Errorf("want VehicleName Magus, got %q", vm.VehicleName)
	}
	if vm.Status != "connected" {
		t.Errorf("want Status connected, got %q", vm.Status)
	}
	if vm.BatteryPct != "94%" {
		t.Errorf("want BatteryPct 94%%, got %q", vm.BatteryPct)
	}
	if vm.LastSeenLabel != "" {
		t.Errorf("want no LastSeenLabel when connected, got %q", vm.LastSeenLabel)
	}
}

func TestNavHeaderFor_Asleep(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 42, VIN: "VIN42", DisplayName: "Magus"},
	}}
	// 3 days old — past the 48 h window -> asleep.
	capturedAt := time.Now().Add(-72 * time.Hour)
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{
		{TeslaID: 42, CapturedAt: &capturedAt, BatteryLevelPct: 50},
	}}
	h := newNavHeaderHandler(acct, reader)
	// English ctx so the "days ago" substring assertion below keeps comparing
	// against the resolved i18n string (KeyNavHeaderLastSeenDaysPlural's EN
	// value), mirroring TestDashStatus / tier 2's T6.4 precedent rather than
	// asserting the Spanish "hace %d días" phrasing.
	ctx := i18n.WithLang(context.Background(), account.LanguageEN)
	vm := h.navHeaderFor(ctx, uuid.New(), 0)

	if vm.VehicleName != "Magus" {
		t.Errorf("want VehicleName Magus, got %q", vm.VehicleName)
	}
	if vm.Status != "asleep" {
		t.Errorf("want Status asleep, got %q", vm.Status)
	}
	if vm.BatteryPct != "" {
		t.Errorf("want no BatteryPct when asleep, got %q", vm.BatteryPct)
	}
	// Relative label pre-computed by the handler, present, and references "days".
	if vm.LastSeenLabel == "" {
		t.Errorf("want non-empty LastSeenLabel when asleep, got empty")
	}
	if !strings.Contains(vm.LastSeenLabel, "days ago") {
		t.Errorf("want LastSeenLabel to be a days-ago relative label (72 h old), got %q", vm.LastSeenLabel)
	}
}

func TestNavHeaderFor_Awaiting(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 42, VIN: "VIN42", DisplayName: "Magus"},
	}}
	// Reader returns an empty slice — no status for the primary vehicle.
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{}}
	h := newNavHeaderHandler(acct, reader)
	vm := h.navHeaderFor(context.Background(), uuid.New(), 0)

	if vm.NeedsConnect {
		t.Fatalf("want NeedsConnect false (vehicle registered), got true")
	}
	if vm.VehicleName != "Magus" {
		t.Errorf("want VehicleName Magus, got %q", vm.VehicleName)
	}
	if vm.Status != "awaiting" {
		t.Errorf("want Status awaiting, got %q", vm.Status)
	}
	if vm.BatteryPct != "" || vm.LastSeenLabel != "" {
		t.Errorf("want no battery/last-seen for awaiting, got battery=%q lastSeen=%q", vm.BatteryPct, vm.LastSeenLabel)
	}
}

func TestNavHeaderFor_NeedsConnect(t *testing.T) {
	// No registered vehicles -> NeedsConnect state (connect link, no dot/name).
	acct := &fakeAccount{registered: nil}
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{}}
	h := newNavHeaderHandler(acct, reader)
	vm := h.navHeaderFor(context.Background(), uuid.New(), 0)

	if !vm.NeedsConnect {
		t.Fatalf("want NeedsConnect true when no vehicles registered, got false")
	}
	if vm.VehicleName != "" {
		t.Errorf("want no VehicleName in NeedsConnect state, got %q", vm.VehicleName)
	}
	if vm.Status != "" {
		t.Errorf("want empty Status in NeedsConnect state (no dot rendered), got %q", vm.Status)
	}
	if vm.BatteryPct != "" {
		t.Errorf("want no BatteryPct in NeedsConnect state, got %q", vm.BatteryPct)
	}
}

func TestNavHeaderFor_AccountError(t *testing.T) {
	// RegisteredVehicles errors -> degraded Unavailable, no name.
	acct := &fakeAccount{regErr: errFake}
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{}}
	h := newNavHeaderHandler(acct, reader)
	vm := h.navHeaderFor(context.Background(), uuid.New(), 0)

	if vm.NeedsConnect {
		t.Errorf("want NeedsConnect false on account error (don't pretend to know), got true")
	}
	if vm.VehicleName != "" {
		t.Errorf("want no VehicleName on account error, got %q", vm.VehicleName)
	}
	if vm.Status != "unavailable" {
		t.Errorf("want Status unavailable, got %q", vm.Status)
	}
}

// TestNavHeaderFor_AnalyticsReaderError retypes the pre-existing telemetry-error
// test (formerly TestNavHeaderFor_TelemetryReaderError) to
// analytics.Reader/fakeAnalyticsReader (RM38-gateway-read-dashboard-from-metrics
// design.md D8) — same scenario, new source.
func TestNavHeaderFor_AnalyticsReaderError(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 42, VIN: "VIN42", DisplayName: "Magus"},
	}}
	reader := &fakeAnalyticsReader{statusesErr: errFake}
	h := newNavHeaderHandler(acct, reader)
	vm := h.navHeaderFor(context.Background(), uuid.New(), 0)

	// Name preserved (account read ok), status degraded, no battery.
	if vm.VehicleName != "Magus" {
		t.Errorf("want VehicleName preserved on analytics reader error, got %q", vm.VehicleName)
	}
	if vm.Status != "unavailable" {
		t.Errorf("want Status unavailable on analytics reader error, got %q", vm.Status)
	}
	if vm.BatteryPct != "" {
		t.Errorf("want no BatteryPct on analytics reader error, got %q", vm.BatteryPct)
	}
}

// TestNavHeaderFor_NilCapturedAtForcesAsleep is the regression test for
// roadmap D9 / design.md D8 ("never Connected on a nil CapturedAt"): a
// vehicle_metrics row that predates the RM38 migration has no CapturedAt to
// prove connectivity with, so navHeaderFor must report Asleep with no
// last-seen label — never Connected, and never a battery percentage (tasks.md
// 5.4, design.md Fixture RM38-G-Nil).
func TestNavHeaderFor_NilCapturedAtForcesAsleep(t *testing.T) {
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1001, VIN: "VIN1001", DisplayName: "Magus"},
	}}
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{
		{TeslaID: 1001, BatteryLevelPct: 72, CapturedAt: nil},
	}}
	h := newNavHeaderHandler(acct, reader)
	vm := h.navHeaderFor(context.Background(), uuid.New(), 0)

	if vm.Status != fragments.NavStatusAsleep {
		t.Errorf("want Status %q for nil CapturedAt, got %q", fragments.NavStatusAsleep, vm.Status)
	}
	if vm.LastSeenLabel != "" {
		t.Errorf("want empty LastSeenLabel for nil CapturedAt, got %q", vm.LastSeenLabel)
	}
	if vm.BatteryPct != "" {
		t.Errorf("want empty BatteryPct for the Asleep branch, got %q", vm.BatteryPct)
	}
	if vm.VehicleName != "Magus" {
		t.Errorf("want VehicleName preserved, got %q", vm.VehicleName)
	}
}

// navHeaderEngine builds a minimal Gin engine with session middleware, a /_session
// route that seeds the uid (matching the sessionCookie contract from
// charges_test.go), and the nav-header fragment route. Reuses sessionCookie.
func navHeaderEngine(h *Handler, uid uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("test-secret"))
	r.Use(sessions.Sessions("test", store))
	r.GET("/_session", func(c *gin.Context) {
		sess := sessions.Default(c)
		sess.Set("uid", uid.String())
		_ = sess.Save()
		c.String(http.StatusOK, "ok")
	})
	r.GET("/ui/nav-header", h.NavHeaderFragment)
	return r
}

// TestNavHeaderFragment_ConnectedHTTP exercises the full fragment route with a
// seeded session: an authenticated GET /ui/nav-header returns 200 and the rendered
// fragment contains the vehicle name, the success-colored status dot
// (badge-success), and the pre-computed battery percentage.
func TestNavHeaderFragment_ConnectedHTTP(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 42, VIN: "VIN42", DisplayName: "Magus"},
	}}
	navCapturedAt := time.Now().Add(-1 * time.Hour)
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{
		{TeslaID: 42, CapturedAt: &navCapturedAt, BatteryLevelPct: 94},
	}}
	h := newNavHeaderHandler(acct, reader)
	eng := navHeaderEngine(h, uid)
	cookie := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/nav-header", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for authenticated nav-header fragment, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		`id="nav-header"`,
		"Magus",
		"badge-success",
		"94%",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("nav-header body missing %q\n%s", want, body)
		}
	}
}

// --- vehicle-select fragment (navbar switcher) ---
//
// GET /ui/vehicle-select is the navbar-mounted context switcher split out from
// nav-header. These HTTP tests cover the multi-vehicle render (select + options
// + CSRF hidden input) and the single-vehicle empty-placeholder render. The
// helper mirrors navHeaderEngine; the route differs.

func vehicleSelectEngine(h *Handler, uid uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("test-secret"))
	r.Use(sessions.Sessions("test", store))
	r.GET("/_session", func(c *gin.Context) {
		sess := sessions.Default(c)
		sess.Set("uid", uid.String())
		_ = sess.Save()
		c.String(http.StatusOK, "ok")
	})
	r.GET("/ui/vehicle-select", h.VehicleSelectFragment)
	return r
}

// TestVehicleSelectFragment_MultiVehicleRendersSelect verifies the fragment
// renders the <select> with both options, the selected marker on the session's
// selected vehicle, and the CSRF hidden input — for an account with >1 vehicle.
func TestVehicleSelectFragment_MultiVehicleRendersSelect(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1, VIN: "VIN1", DisplayName: "First"},
		{TeslaID: 2, VIN: "VIN2", DisplayName: "Second"},
	}}
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{}}
	h := newNavHeaderHandler(acct, reader)
	eng := vehicleSelectEngine(h, uid)
	cookie := sessionCookie(eng, uid, "")
	// No selected-vehicle context is seeded in the session; the handler's
	// auto-select picks the first OWNER (TeslaID=1, "First"), so the First
	// option is the selected one — asserted below.

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/vehicle-select", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for authenticated vehicle-select fragment, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		`id="vehicle-select"`,
		`name="vehicle"`,
		"First",
		"Second",
		`name="csrf_token"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("vehicle-select body missing %q\n%s", want, body)
		}
	}
	// Auto-select picks the first OWNER (TeslaID=1): exactly one option carries
	// the selected marker.
	if got := strings.Count(body, "selected"); got != 1 {
		t.Errorf("want exactly 1 selected option (auto-select first OWNER), got %d\n%s", got, body)
	}
}

// TestVehicleSelectFragment_SingleVehicleRendersEmptyPlaceholder verifies the
// fragment emits the stable #vehicle-select root div but NO <select> when the
// account has a single vehicle (the switcher only makes sense for >1).
func TestVehicleSelectFragment_SingleVehicleRendersEmptyPlaceholder(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 42, VIN: "VIN42", DisplayName: "Magus"},
	}}
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{}}
	h := newNavHeaderHandler(acct, reader)
	eng := vehicleSelectEngine(h, uid)
	cookie := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/vehicle-select", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for single-vehicle vehicle-select fragment, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="vehicle-select"`) {
		t.Errorf("want stable #vehicle-select root div even when empty, got:\n%s", body)
	}
	if strings.Contains(body, "<select") {
		t.Errorf("want NO <select> for a single-vehicle account, got:\n%s", body)
	}
}

// --- dashboard fragment + vehicle-switch refresh (GET /ui/dashboard, HX-Trigger) ---
//
// These cover the switcher → dashboard refresh wiring: the switch persists the new
// selection and fires "vehicle-changed" (VehicleSelect); the dashboard region then
// re-fetches GET /ui/dashboard for the newly-selected vehicle. The navHeaderEngine
// above seeds only uid; dashboardEngine additionally seeds the selected-vehicle
// context and the vehicle-select CSRF token so both routes can be exercised.

// dashboardEngine builds a minimal Gin engine with session middleware, a /_session
// route that seeds uid (plus an optional selected-vehicle context and vehicle-select
// CSRF token), and the dashboard fragment + vehicle-select routes. Mirrors
// navHeaderEngine. selTeslaID == 0 leaves the session with no selection (so the
// handler auto-selects); non-zero seeds the switcher's persisted choice.
func dashboardEngine(h *Handler, uid uuid.UUID, selTeslaID int64, selVIN, csrf string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("test-secret"))
	r.Use(sessions.Sessions("test", store))
	r.GET("/_session", func(c *gin.Context) {
		sess := sessions.Default(c)
		sess.Set("uid", uid.String())
		if selTeslaID != 0 {
			sess.Set(sessionTeslaIDKey, selTeslaID)
			sess.Set(sessionVINKey, selVIN)
		}
		if csrf != "" {
			sess.Set(csrfVehicleSelectKey, csrf)
		}
		_ = sess.Save()
		c.String(http.StatusOK, "ok")
	})
	r.GET("/ui/dashboard", h.DashboardFragment)
	r.POST("/ui/vehicle/select", h.VehicleSelect)
	return r
}

// TestDashboardFragment_RendersSelectedVehicle verifies GET /ui/dashboard renders the
// SELECTED vehicle's bento (not registered[0]) and emits ONLY the swappable region —
// no page shell and no #dashboard-content wrapper (the innerHTML-swap contract keeps
// the listening wrapper in the DOM; the fragment is just its inner content).
func TestDashboardFragment_RendersSelectedVehicle(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1, VIN: "VIN1", DisplayName: "First"},
		{TeslaID: 2, VIN: "VIN2", DisplayName: "Second"},
	}}
	dashCapturedAt := time.Now().Add(-time.Hour)
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{
		{TeslaID: 2, CapturedAt: &dashCapturedAt, BatteryLevelPct: 77, OdometerKm: 1000, InsideTempC: ptrF64(20)},
	}}
	h := newHandlerWithAnalytics(acct, fakeTesla{}, reader)
	eng := dashboardEngine(h, uid, 2, "VIN2", "")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/dashboard", nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for authenticated dashboard fragment, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Second") {
		t.Errorf("want SELECTED vehicle name 'Second' in fragment, got:\n%s", body)
	}
	if !strings.Contains(body, "77%") {
		t.Errorf("want SELECTED vehicle battery '77%%' in fragment, got:\n%s", body)
	}
	if strings.Contains(body, "First") {
		t.Errorf("fragment must render the selected vehicle only, not registered[0] 'First'")
	}
	if strings.Contains(body, "<html") {
		t.Errorf("dashboard fragment must not contain the full-page shell (<html>)")
	}
	if strings.Contains(body, `id="dashboard-content"`) {
		t.Errorf("dashboard fragment is the innerHTML of #dashboard-content; it must not re-emit the wrapper")
	}
}

// --- dashboard header badges (Locked/Sentry/Stale) httptest (tasks.md 5.3) ---
//
// These confirm dashLockedBadge/dashSentryBadge (1.1, unit-tested in isolation
// by templates/pages/dashboard_test.go's badge-matrix test) are actually wired
// into dashboard.templ's header row. No LanguageMiddleware runs in
// dashboardEngine, so i18n.T resolves to its Spanish default
// (i18n.FromContext) — assertions use the catalogue's ES strings.
func dashboardBadgeEngine(t *testing.T, statuses []analytics.VehicleStatus) string {
	t.Helper()
	uid := uuid.New()
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1001, VIN: "VIN1001", DisplayName: "Magus"},
	}}
	reader := &fakeAnalyticsReader{statuses: statuses}
	h := newHandlerWithAnalytics(acct, fakeTesla{}, reader)
	eng := dashboardEngine(h, uid, 1001, "VIN1001", "")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/dashboard", nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for dashboard fragment, got %d", w.Code)
	}
	return w.Body.String()
}

// TestDashboardFragment_BadgesFixtureFull asserts Fixture RM38-G-Full's Locked
// and Sentry badges both render with the correct text and DaisyUI kind class,
// and that the unrelated [Stale] badge does not (the fixture's CapturedAt is
// fresh).
func TestDashboardFragment_BadgesFixtureFull(t *testing.T) {
	capturedAt := time.Now().Add(-2 * time.Hour)
	body := dashboardBadgeEngine(t, []analytics.VehicleStatus{
		{
			TeslaID:         1001,
			BatteryLevelPct: 72,
			BatteryRangeKm:  310.4,
			OdometerKm:      18452.0,
			CapturedAt:      &capturedAt,
			Locked:          boolPtr(true),
			SentryMode:      boolPtr(true),
		},
	})
	if !strings.Contains(body, "badge-success") || !strings.Contains(body, "Bloqueado") {
		t.Errorf("want the Locked badge (badge-success / \"Bloqueado\") in body:\n%s", body)
	}
	if !strings.Contains(body, "badge-warning") || !strings.Contains(body, "Centinela: Encendido") {
		t.Errorf("want the Sentry badge (badge-warning / \"Centinela: Encendido\") in body:\n%s", body)
	}
	if strings.Contains(body, "Desactualizado") {
		t.Errorf("want no [Stale] badge for a fresh CapturedAt, got:\n%s", body)
	}
}

// TestDashboardFragment_BadgesFixtureNil asserts Fixture RM38-G-Nil's header
// shows NEITHER badge and no [Stale] badge (IsStale is false, not merely
// unset — a nil CapturedAt must never render [Stale]).
func TestDashboardFragment_BadgesFixtureNil(t *testing.T) {
	body := dashboardBadgeEngine(t, []analytics.VehicleStatus{
		{TeslaID: 1001, BatteryLevelPct: 72, BatteryRangeKm: 310.4, OdometerKm: 18452.0},
	})
	for _, absent := range []string{"Bloqueado", "Desbloqueado", "Centinela:", "Desactualizado", "Última actualización"} {
		if strings.Contains(body, absent) {
			t.Errorf("want no %q in the header for Fixture Nil, got:\n%s", absent, body)
		}
	}
}

// TestDashboardFragment_BadgesMixedLockedOnlySentryNil confirms one Locked
// badge renders alone when SentryMode is nil (badge matrix row Locked=*true/
// SentryMode=nil) — proving the two helpers are independently wired, not
// coupled.
func TestDashboardFragment_BadgesMixedLockedOnlySentryNil(t *testing.T) {
	capturedAt := time.Now().Add(-2 * time.Hour)
	body := dashboardBadgeEngine(t, []analytics.VehicleStatus{
		{
			TeslaID:         1001,
			BatteryLevelPct: 72,
			BatteryRangeKm:  310.4,
			OdometerKm:      18452.0,
			CapturedAt:      &capturedAt,
			Locked:          boolPtr(true),
			SentryMode:      nil,
		},
	})
	if !strings.Contains(body, "badge-success") || !strings.Contains(body, "Bloqueado") {
		t.Errorf("want the Locked badge in body:\n%s", body)
	}
	if strings.Contains(body, "Centinela:") {
		t.Errorf("want no Sentry badge when SentryMode is nil, got:\n%s", body)
	}
}

// TestVehicleSelect_FiresVehicleChangedTrigger verifies a successful switch responds
// with the HX-Trigger: vehicle-changed header so the dashboard region (and any other
// subscriber) refreshes for the newly-selected vehicle.
func TestVehicleSelect_FiresVehicleChangedTrigger(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1, VIN: "VIN1", DisplayName: "First"},
		{TeslaID: 2, VIN: "VIN2", DisplayName: "Second"},
	}}
	reader := &fakeReader{snapshots: []telemetry.Snapshot{}}
	h := newHandlerWithReader(acct, fakeTesla{}, reader)
	eng := dashboardEngine(h, uid, 0, "", "tok")
	c := sessionCookie(eng, uid, "")

	form := url.Values{"csrf_token": {"tok"}, "vehicle": {"2:VIN2"}}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/vehicle/select", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for a valid vehicle switch, got %d (%s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("HX-Trigger"); got != "vehicle-changed" {
		t.Errorf("want HX-Trigger: vehicle-changed so subscribed regions refresh, got %q", got)
	}
}

// homeEngine builds a minimal Gin engine with session middleware, a /_session route
// that seeds uid + email (so sessionCookie can forge a signed-in cookie), and the
// home route wired to h.Home. Mirrors the navHeaderEngine pattern.
func homeEngine(h *Handler, uid uuid.UUID, email string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("test-secret"))
	r.Use(sessions.Sessions("test", store))
	r.GET("/_session", func(c *gin.Context) {
		sess := sessions.Default(c)
		sess.Set("uid", uid.String())
		sess.Set("email", email)
		_ = sess.Save()
		c.String(http.StatusOK, "ok")
	})
	r.GET("/", h.Home)
	return r
}

// TestHome_SignedInRedirectsToDashboard verifies the signed-in home state: a
// logged-in user hitting "/" is redirected straight to /dashboard (the default
// authenticated experience). The old pages.Home content view is retired.
func TestHome_SignedInRedirectsToDashboard(t *testing.T) {
	uid := uuid.New()
	const email = "driver@example.com"

	h := New(Deps{Account: &fakeAccount{}, Tesla: &fakeTesla{}, TelemetryReader: &fakeReader{}})
	eng := homeEngine(h, uid, email)

	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302 redirect to /dashboard for signed-in home, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/dashboard" {
		t.Fatalf("Location = %q, want /dashboard", loc)
	}
}
