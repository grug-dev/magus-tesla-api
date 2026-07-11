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
