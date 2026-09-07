package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cristianpena/magus-tesla-api/internal/tesla"
)

// minimalFakeTesla is a tiny, self-contained tesla.VehicleService fake used only
// by this file — deliberately not the package's shared fakeTesla (service_test.go),
// so this test compiles and passes independently of Wave 4's service.go wiring
// (design D9/D10 concern only the decorator itself, not CollectAll).
type minimalFakeTesla struct {
	listErr error
}

func (f *minimalFakeTesla) ListVehicles(_ context.Context, _ tesla.Credentials) ([]tesla.VehicleTesla, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return []tesla.VehicleTesla{{ID: 1}}, nil
}

func (f *minimalFakeTesla) VehicleData(_ context.Context, _ tesla.Credentials, _ int64) (*tesla.VehicleDataTesla, json.RawMessage, error) {
	return &tesla.VehicleDataTesla{}, json.RawMessage(`{}`), nil
}

func (f *minimalFakeTesla) WakeUp(_ context.Context, _ tesla.Credentials, _ int64) (*tesla.VehicleTesla, error) {
	return &tesla.VehicleTesla{ID: 1}, nil
}

func (f *minimalFakeTesla) ChargingHistory(_ context.Context, _ tesla.Credentials, _ tesla.ChargingHistoryParams) (*tesla.ChargingHistoryTesla, error) {
	return &tesla.ChargingHistoryTesla{}, nil
}

// TestCallCounter_CountsEveryCallRegardlessOfOutcome exercises all four
// tesla.VehicleService methods a distinct number of times, including at least
// one call that returns an error, and asserts calls equals the total
// regardless of success/failure (design D9: a failed call still spends a
// Tesla request, so it must still count).
func TestCallCounter_CountsEveryCallRegardlessOfOutcome(t *testing.T) {
	ctx := context.Background()
	creds := tesla.Credentials{}

	// listErr makes ListVehicles fail on every call — the counter must still
	// count these failed calls (design D9's "even a rejected request counts").
	inner := &minimalFakeTesla{listErr: errors.New("boom")}
	counter := newCallCounter(inner)

	const (
		listCalls   = 3
		wakeCalls   = 2
		dataCalls   = 4
		chargeCalls = 1
	)

	for i := 0; i < listCalls; i++ {
		if _, err := counter.ListVehicles(ctx, creds); err == nil {
			t.Fatalf("ListVehicles call %d: expected error from fake, got nil", i)
		}
	}
	for i := 0; i < wakeCalls; i++ {
		if _, err := counter.WakeUp(ctx, creds, 1); err != nil {
			t.Fatalf("WakeUp call %d: unexpected error: %v", i, err)
		}
	}
	for i := 0; i < dataCalls; i++ {
		if _, _, err := counter.VehicleData(ctx, creds, 1); err != nil {
			t.Fatalf("VehicleData call %d: unexpected error: %v", i, err)
		}
	}
	for i := 0; i < chargeCalls; i++ {
		if _, err := counter.ChargingHistory(ctx, creds, tesla.ChargingHistoryParams{}); err != nil {
			t.Fatalf("ChargingHistory call %d: unexpected error: %v", i, err)
		}
	}

	want := listCalls + wakeCalls + dataCalls + chargeCalls
	if counter.calls != want {
		t.Fatalf("calls = %d, want %d (list=%d wake=%d data=%d charge=%d)",
			counter.calls, want, listCalls, wakeCalls, dataCalls, chargeCalls)
	}
}

// TestCallCounter_LogsExpectedLines exercises all four tesla.VehicleService
// methods and asserts the emitted "fleet api:" log line matches
// RM44-telemetry-add-query-logging design.md D4's table exactly (Test
// Contract Group B). Each expected line below was transcribed from D4's
// table before re-reading call_counter.go's format strings — the assertions
// pin what the design specifies, not what the code happens to print.
func TestCallCounter_LogsExpectedLines(t *testing.T) {
	buf := captureLog(t)
	ctx := context.Background()
	creds := tesla.Credentials{}
	counter := newCallCounter(&minimalFakeTesla{})

	if _, err := counter.ListVehicles(ctx, creds); err != nil {
		t.Fatalf("ListVehicles: unexpected error: %v", err)
	}
	if _, _, err := counter.VehicleData(ctx, creds, 99); err != nil {
		t.Fatalf("VehicleData: unexpected error: %v", err)
	}
	if _, err := counter.WakeUp(ctx, creds, 99); err != nil {
		t.Fatalf("WakeUp: unexpected error: %v", err)
	}
	params := tesla.ChargingHistoryParams{
		StartTime: "2026-06-01",
		EndTime:   "2026-09-01",
		PageNo:    2,
		Count:     50,
	}
	if _, err := counter.ChargingHistory(ctx, creds, params); err != nil {
		t.Fatalf("ChargingHistory: unexpected error: %v", err)
	}

	got := buf.String()
	wantLines := []string{
		"fleet api: ListVehicles",
		fmt.Sprintf("fleet api: VehicleData vehicle_id=%d", 99),
		fmt.Sprintf("fleet api: WakeUp vehicle_id=%d", 99),
		fmt.Sprintf("fleet api: ChargingHistory start_time=%q end_time=%q page_no=%d count=%d",
			params.StartTime, params.EndTime, params.PageNo, params.Count),
	}
	for _, want := range wantLines {
		if !strings.Contains(got, want) {
			t.Fatalf("log output missing line %q; got:\n%s", want, got)
		}
	}
}

// TestCallCounter_NeverLogsCredentials is the concrete, structural proof
// (design D3/D9, Test Contract Group C) that a live tesla.Credentials value
// never reaches a "fleet api:" log line through any of the 4 callCounter
// methods.
func TestCallCounter_NeverLogsCredentials(t *testing.T) {
	buf := captureLog(t)
	ctx := context.Background()
	const secret = "SECRET-TOKEN-DO-NOT-LOG-9f3a"
	creds := tesla.Credentials{AccessToken: secret}
	counter := newCallCounter(&minimalFakeTesla{})

	if _, err := counter.ListVehicles(ctx, creds); err != nil {
		t.Fatalf("ListVehicles: unexpected error: %v", err)
	}
	if _, _, err := counter.VehicleData(ctx, creds, 1); err != nil {
		t.Fatalf("VehicleData: unexpected error: %v", err)
	}
	if _, err := counter.WakeUp(ctx, creds, 1); err != nil {
		t.Fatalf("WakeUp: unexpected error: %v", err)
	}
	if _, err := counter.ChargingHistory(ctx, creds, tesla.ChargingHistoryParams{}); err != nil {
		t.Fatalf("ChargingHistory: unexpected error: %v", err)
	}

	if got := buf.String(); strings.Contains(got, secret) {
		t.Fatalf("log output leaked credential; got:\n%s", got)
	}
}
