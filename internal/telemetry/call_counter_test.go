package telemetry

import (
	"context"
	"encoding/json"
	"errors"
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
