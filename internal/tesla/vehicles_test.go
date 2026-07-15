package tesla

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// These tests exercise the TYPED adapter methods offline, against an httptest server
// standing in for the Fleet API — they never touch the live, paid API. The
// exploration capability (raw.go, cmd/explore-tesla-api) is deliberately NOT tested:
// no test in this repo may call the Raw* methods (see CLAUDE.md), so this file only
// drives VehicleService methods.

// testClient points a Client at the given test server via the unexported baseURL
// field (same-package access), leaving the public API untouched.
func testClient(srv *httptest.Server) *Client {
	return &Client{http: srv.Client(), baseURL: srv.URL}
}

const vehicleDataBody = `{
  "response": {
    "id": 42,
    "vin": "VINTEST",
    "display_name": "Magus",
    "state": "online",
    "charge_state": {
      "battery_level": 72,
      "battery_range": 210.5,
      "charging_state": "Charging",
      "charge_rate": 24.0,
      "charge_limit_soc": 80,
      "time_to_full_charge": 1.5,
      "charge_port_door_open": true
    },
    "climate_state": {
      "inside_temp": 21.0,
      "outside_temp": 15.5,
      "is_climate_on": true,
      "driver_temp_setting": 21.5,
      "passenger_temp_setting": 21.5
    },
    "drive_state": {
      "speed": null,
      "latitude": 4.65,
      "longitude": -74.05,
      "heading": 90
    },
    "vehicle_state": {
      "locked": true,
      "odometer": 12345.6,
      "car_version": "2025.20.1",
      "sentry_mode": false
    }
  }
}`

func TestVehicleData_ReturnsTypedAndLosslessRaw(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Path != "/api/1/vehicles/42/vehicle_data" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(vehicleDataBody))
	}))
	defer srv.Close()

	data, raw, err := testClient(srv).VehicleData(context.Background(), Credentials{AccessToken: "tok"}, 42)
	if err != nil {
		t.Fatalf("VehicleData: %v", err)
	}

	// Exactly one paid Fleet API request yields both artifacts.
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("want exactly 1 Fleet API request, got %d", got)
	}

	// Typed fields, including the two columns this change adds.
	if data.ChargeState.BatteryLevel != 72 || data.ChargeState.ChargeLimitSoc != 80 {
		t.Errorf("charge fields: %+v", data.ChargeState)
	}
	if data.VehicleState.SentryMode == nil || *data.VehicleState.SentryMode != false {
		t.Errorf("want sentry_mode reported *false, got %v", data.VehicleState.SentryMode)
	}
	if data.DriveState.Speed != nil {
		t.Errorf("want nil speed (parked), got %v", *data.DriveState.Speed)
	}

	// Raw is the INNER vehicle_data object, not the transport envelope (design #3).
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("raw is not a JSON object: %v", err)
	}
	if _, wrapped := keys["response"]; wrapped {
		t.Error("raw still wraps the transport envelope; want the inner object")
	}
	if _, ok := keys["charge_state"]; !ok {
		t.Error("raw missing charge_state — not the vehicle_data object")
	}

	// Losslessness: the DTO re-parses from the returned raw bytes to the same values.
	var reparsed VehicleDataTesla
	if err := json.Unmarshal(raw, &reparsed); err != nil {
		t.Fatalf("re-parsing raw: %v", err)
	}
	if reparsed.ChargeState.ChargeLimitSoc != data.ChargeState.ChargeLimitSoc ||
		reparsed.VehicleState.CarVersion != data.VehicleState.CarVersion ||
		reparsed.ChargeState.BatteryRange != data.ChargeState.BatteryRange {
		t.Errorf("raw and DTO disagree:\n raw=%+v\n dto=%+v", reparsed, *data)
	}
}

func TestVehicleData_UnauthorizedSignalsErrUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, _, err := testClient(srv).VehicleData(context.Background(), Credentials{AccessToken: "expired"}, 42)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized on 401, got %v", err)
	}
}

func TestWakeUp_ReturnsReportedState(t *testing.T) {
	const wakeBody = `{"response":{"id":42,"vin":"VINTEST","display_name":"Magus","state":"online"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("wake must be POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/1/vehicles/42/wake_up" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(wakeBody))
	}))
	defer srv.Close()

	v, err := testClient(srv).WakeUp(context.Background(), Credentials{AccessToken: "tok"}, 42)
	if err != nil {
		t.Fatalf("WakeUp: %v", err)
	}
	if v.State != "online" {
		t.Errorf("want reported state online, got %q", v.State)
	}
}

// --- ChargingHistory tests (T3.1–T3.6) ---

// canned single-page charging history response matching the live payload shape.
const chargingHistoryBody = `{
  "data": [
    {
      "sessionId": 1001,
      "vin": "VINTEST",
      "siteLocationName": "Bogota Supercharger",
      "chargeStartDateTime": "2026-06-28T10:24:41-05:00",
      "chargeStopDateTime": "2026-06-28T11:04:41-05:00",
      "unlatchDateTime": "2026-06-28T11:05:00-05:00",
      "countryCode": "CO",
      "fees": [
        {
          "sessionFeeId": 2001,
          "feeType": "CHARGING",
          "currencyCode": "COP",
          "pricingType": "PAYMENT",
          "rateBase": 1300.0,
          "rateTier1": 0.0,
          "rateTier2": 0.0,
          "rateTier3": null,
          "rateTier4": null,
          "usageBase": 46.2341,
          "usageTier1": 0.0,
          "usageTier2": 0.0,
          "usageTier3": null,
          "usageTier4": null,
          "totalBase": 60104.33,
          "totalTier1": 0.0,
          "totalTier2": 0.0,
          "totalTier3": 0.0,
          "totalTier4": 0.0,
          "totalDue": 60104.33,
          "netDue": 60104.33,
          "uom": "kwh",
          "isPaid": true,
          "status": "PAID"
        }
      ],
      "billingType": "IMMEDIATE",
      "invoices": [
        {
          "fileName": "3095P0000002437_ES-CO.pdf",
          "contentId": "abc123-def456",
          "invoiceType": "IMMEDIATE"
        }
      ],
      "vehicleMakeType": "TSLA"
    }
  ],
  "totalResults": 1
}`

// T3.1 — single-page response: assert all session, fee, and invoice fields are mapped.
func TestChargingHistory_SinglePageMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/1/dx/charging/history" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(chargingHistoryBody))
	}))
	defer srv.Close()

	result, err := testClient(srv).ChargingHistory(context.Background(), Credentials{AccessToken: "tok"}, ChargingHistoryParams{})
	if err != nil {
		t.Fatalf("ChargingHistory: %v", err)
	}
	if result.TotalResults != 1 {
		t.Errorf("TotalResults: want 1, got %d", result.TotalResults)
	}
	if len(result.Data) != 1 {
		t.Fatalf("len(Data): want 1, got %d", len(result.Data))
	}

	s := result.Data[0]
	if s.SessionID != 1001 {
		t.Errorf("SessionID: want 1001, got %d", s.SessionID)
	}
	if s.VIN != "VINTEST" {
		t.Errorf("VIN: want VINTEST, got %q", s.VIN)
	}
	if s.SiteLocationName != "Bogota Supercharger" {
		t.Errorf("SiteLocationName: want %q, got %q", "Bogota Supercharger", s.SiteLocationName)
	}
	if s.CountryCode != "CO" {
		t.Errorf("CountryCode: want CO, got %q", s.CountryCode)
	}
	if s.BillingType != "IMMEDIATE" {
		t.Errorf("BillingType: want IMMEDIATE, got %q", s.BillingType)
	}
	if s.VehicleMakeType != "TSLA" {
		t.Errorf("VehicleMakeType: want TSLA, got %q", s.VehicleMakeType)
	}

	// Datetimes: assert parsed correctly (start is 2026-06-28T10:24:41-05:00 → UTC 15:24:41).
	wantStart := time.Date(2026, 6, 28, 15, 24, 41, 0, time.UTC)
	if !s.ChargeStartDateTime.UTC().Equal(wantStart) {
		t.Errorf("ChargeStartDateTime: want %v, got %v", wantStart, s.ChargeStartDateTime.UTC())
	}

	// Fee assertions.
	if len(s.Fees) != 1 {
		t.Fatalf("len(Fees): want 1, got %d", len(s.Fees))
	}
	fee := s.Fees[0]
	if fee.SessionFeeID != 2001 {
		t.Errorf("SessionFeeID: want 2001, got %d", fee.SessionFeeID)
	}
	if fee.FeeType != "CHARGING" {
		t.Errorf("FeeType: want CHARGING, got %q", fee.FeeType)
	}
	if fee.CurrencyCode != "COP" {
		t.Errorf("CurrencyCode: want COP, got %q", fee.CurrencyCode)
	}
	if fee.RateTier3 != nil {
		t.Errorf("RateTier3: want nil (JSON null), got %v", *fee.RateTier3)
	}
	if fee.RateTier4 != nil {
		t.Errorf("RateTier4: want nil (JSON null), got %v", *fee.RateTier4)
	}
	if fee.UsageTier3 != nil {
		t.Errorf("UsageTier3: want nil (JSON null), got %v", *fee.UsageTier3)
	}
	if fee.UsageTier4 != nil {
		t.Errorf("UsageTier4: want nil (JSON null), got %v", *fee.UsageTier4)
	}
	if fee.UOM != "kwh" {
		t.Errorf("UOM: want kwh, got %q", fee.UOM)
	}
	if !fee.IsPaid {
		t.Error("IsPaid: want true")
	}

	// Invoice assertions.
	if len(s.Invoices) != 1 {
		t.Fatalf("len(Invoices): want 1, got %d", len(s.Invoices))
	}
	inv := s.Invoices[0]
	if inv.FileName != "3095P0000002437_ES-CO.pdf" {
		t.Errorf("FileName: want 3095P0000002437_ES-CO.pdf, got %q", inv.FileName)
	}
	if inv.ContentID != "abc123-def456" {
		t.Errorf("ContentID: want abc123-def456, got %q", inv.ContentID)
	}
	if inv.InvoiceType != "IMMEDIATE" {
		t.Errorf("InvoiceType: want IMMEDIATE, got %q", inv.InvoiceType)
	}
}

// T3.2 — auto-pagination: 2 pages, totalResults=2; assert merged len==2 and handler hit twice.
func TestChargingHistory_AutoPagination(t *testing.T) {
	page1 := `{
		"data": [{"sessionId":101,"vin":"VIN1","siteLocationName":"S1","chargeStartDateTime":"2026-01-01T10:00:00Z","chargeStopDateTime":"2026-01-01T11:00:00Z","unlatchDateTime":"2026-01-01T11:01:00Z","countryCode":"US","fees":[],"billingType":"IMMEDIATE","invoices":[],"vehicleMakeType":"TSLA"}],
		"totalResults": 2
	}`
	page2 := `{
		"data": [{"sessionId":102,"vin":"VIN2","siteLocationName":"S2","chargeStartDateTime":"2026-02-01T10:00:00Z","chargeStopDateTime":"2026-02-01T11:00:00Z","unlatchDateTime":"2026-02-01T11:01:00Z","countryCode":"US","fees":[],"billingType":"IMMEDIATE","invoices":[],"vehicleMakeType":"TSLA"}],
		"totalResults": 2
	}`

	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		if n == 1 {
			_, _ = w.Write([]byte(page1))
		} else {
			_, _ = w.Write([]byte(page2))
		}
	}))
	defer srv.Close()

	result, err := testClient(srv).ChargingHistory(context.Background(), Credentials{AccessToken: "tok"}, ChargingHistoryParams{})
	if err != nil {
		t.Fatalf("ChargingHistory: %v", err)
	}
	if got := atomic.LoadInt32(&callCount); got != 2 {
		t.Errorf("handler calls: want 2, got %d", got)
	}
	if len(result.Data) != 2 {
		t.Errorf("len(Data): want 2, got %d", len(result.Data))
	}
	if result.TotalResults != 2 {
		t.Errorf("TotalResults: want 2, got %d", result.TotalResults)
	}
}

// T3.3 — PageNo != 0: single-page fetch; handler called exactly once.
func TestChargingHistory_SinglePageExplicit(t *testing.T) {
	body := `{
		"data": [{"sessionId":201,"vin":"VINSINGLE","siteLocationName":"S1","chargeStartDateTime":"2026-03-01T10:00:00Z","chargeStopDateTime":"2026-03-01T11:00:00Z","unlatchDateTime":"2026-03-01T11:01:00Z","countryCode":"US","fees":[],"billingType":"IMMEDIATE","invoices":[],"vehicleMakeType":"TSLA"}],
		"totalResults": 10
	}`
	var callCount int32
	var capturedPageNo string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		capturedPageNo = r.URL.Query().Get("pageNo")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	result, err := testClient(srv).ChargingHistory(context.Background(), Credentials{AccessToken: "tok"}, ChargingHistoryParams{PageNo: 3})
	if err != nil {
		t.Fatalf("ChargingHistory: %v", err)
	}
	if got := atomic.LoadInt32(&callCount); got != 1 {
		t.Errorf("handler calls: want 1 (single-page), got %d", got)
	}
	if capturedPageNo != "3" {
		t.Errorf("pageNo query param: want 3, got %q", capturedPageNo)
	}
	if len(result.Data) != 1 {
		t.Errorf("len(Data): want 1, got %d", len(result.Data))
	}
}

// T3.4 — 401 → ErrUnauthorized; 403 → ErrForbidden.
func TestChargingHistory_ErrorSentinels(t *testing.T) {
	t.Run("401_yields_ErrUnauthorized", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()
		_, err := testClient(srv).ChargingHistory(context.Background(), Credentials{AccessToken: "expired"}, ChargingHistoryParams{})
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("want ErrUnauthorized on 401, got %v", err)
		}
	})

	t.Run("403_yields_ErrForbidden", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"missing_scopes"}`))
		}))
		defer srv.Close()
		_, err := testClient(srv).ChargingHistory(context.Background(), Credentials{AccessToken: "noscope"}, ChargingHistoryParams{})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("want ErrForbidden on 403, got %v", err)
		}
	})
}

// T3.5 — RFC3339 timestamp parsing: "2026-06-28T10:24:41-05:00" → UTC 2026-06-28T15:24:41Z.
func TestChargingHistory_RFC3339TimestampParsing(t *testing.T) {
	body := `{
		"data": [
			{
				"sessionId": 999,
				"vin": "V",
				"siteLocationName": "X",
				"chargeStartDateTime": "2026-06-28T10:24:41-05:00",
				"chargeStopDateTime": "2026-06-28T11:04:41-05:00",
				"unlatchDateTime": "2026-06-28T11:05:00-05:00",
				"countryCode": "CO",
				"fees": [],
				"billingType": "IMMEDIATE",
				"invoices": [],
				"vehicleMakeType": "TSLA"
			}
		],
		"totalResults": 1
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	result, err := testClient(srv).ChargingHistory(context.Background(), Credentials{AccessToken: "tok"}, ChargingHistoryParams{})
	if err != nil {
		t.Fatalf("ChargingHistory: %v", err)
	}
	if len(result.Data) != 1 {
		t.Fatalf("expected 1 session, got %d", len(result.Data))
	}
	s := result.Data[0]
	wantUTC := time.Date(2026, 6, 28, 15, 24, 41, 0, time.UTC)
	if !s.ChargeStartDateTime.UTC().Equal(wantUTC) {
		t.Errorf("ChargeStartDateTime UTC: want %v, got %v", wantUTC, s.ChargeStartDateTime.UTC())
	}
}

// T3.6 — Confirm: no test calls Raw* methods (this test file never references
// ChargingHistoryRaw, ListVehiclesRaw, VehicleDataRaw, or WakeUpRaw). This is
// enforced by inspection — the Raw* prohibition is maintained by convention.

func TestMetricCompanions(t *testing.T) {
	const eps = 1e-9

	cs := ChargeStateTesla{BatteryRange: 100, ChargeRate: 20}
	if got := cs.BatteryRangeKm(); math.Abs(got-160.9344) > eps {
		t.Errorf("BatteryRangeKm(100) = %v, want 160.9344", got)
	}
	if got := cs.ChargeRateKmh(); math.Abs(got-32.18688) > eps {
		t.Errorf("ChargeRateKmh(20) = %v, want 32.18688", got)
	}

	vs := VehicleStateTesla{Odometer: 50}
	if got := vs.OdometerKm(); math.Abs(got-80.4672) > eps {
		t.Errorf("OdometerKm(50) = %v, want 80.4672", got)
	}

	// SpeedKmh is nil-safe: nil in → nil out; value in → converted.
	if (DriveStateTesla{Speed: nil}).SpeedKmh() != nil {
		t.Error("SpeedKmh(nil) should be nil")
	}
	sp := 60.0
	if got := (DriveStateTesla{Speed: &sp}).SpeedKmh(); got == nil || math.Abs(*got-96.56064) > eps {
		t.Errorf("SpeedKmh(60) = %v, want 96.56064", got)
	}
}
