package telemetry

import (
	"context"
	"encoding/json"

	"github.com/cristianpena/magus-tesla-api/internal/tesla"
)

// callCounter wraps a tesla.VehicleService and counts every request made
// through it during one collection cycle (RM36-telemetry-add-poll-runs design
// D9/D10). It implements every tesla.VehicleService method EXPLICITLY —
// deliberately not by embedding the interface. Embedding
// (struct{ tesla.VehicleService }, overriding only today's methods) would let a
// future Fleet API method added to tesla.VehicleService satisfy this type
// SILENTLY via promotion, so that call would never be counted — exactly the
// "known accepted risk" roadmap D2 names. Explicit, no-embedding
// implementation turns a missed override into a COMPILE ERROR the moment
// tesla.VehicleService gains a method this file does not also gain.
//
// calls is incremented BEFORE delegating to inner, unconditionally — a call
// that returns an error still counts, because a rejected or failed request
// still spends a request against Tesla's API and its rate limit (design D9).
//
// calls is a plain int with no synchronization: CollectAll's account loop is a
// sequential for loop with no goroutines, so this field is never accessed
// concurrently within one run (design D10). If CollectAll is ever
// parallelized across accounts, this field must gain synchronization at that
// time — not added speculatively here.
type callCounter struct {
	inner tesla.VehicleService
	calls int
}

// newCallCounter constructs a callCounter wrapping inner. A fresh instance
// must be constructed once per CollectAll call (never stored on *service,
// never reused across runs) — see design D10's rationale in
// telemetry.CollectAll.
func newCallCounter(inner tesla.VehicleService) *callCounter {
	return &callCounter{inner: inner}
}

// Compile-time assertion: *callCounter must satisfy the full tesla.VehicleService
// interface. A future method added to that interface without a matching
// explicit override here fails to compile (design D9).
var _ tesla.VehicleService = (*callCounter)(nil)

// ListVehicles implements tesla.VehicleService, counting the call before delegating.
func (c *callCounter) ListVehicles(ctx context.Context, creds tesla.Credentials) ([]tesla.VehicleTesla, error) {
	c.calls++
	return c.inner.ListVehicles(ctx, creds)
}

// VehicleData implements tesla.VehicleService, counting the call before delegating.
func (c *callCounter) VehicleData(ctx context.Context, creds tesla.Credentials, vehicleID int64) (*tesla.VehicleDataTesla, json.RawMessage, error) {
	c.calls++
	return c.inner.VehicleData(ctx, creds, vehicleID)
}

// WakeUp implements tesla.VehicleService, counting the call before delegating.
func (c *callCounter) WakeUp(ctx context.Context, creds tesla.Credentials, vehicleID int64) (*tesla.VehicleTesla, error) {
	c.calls++
	return c.inner.WakeUp(ctx, creds, vehicleID)
}

// ChargingHistory implements tesla.VehicleService, counting the call before delegating.
func (c *callCounter) ChargingHistory(ctx context.Context, creds tesla.Credentials, params tesla.ChargingHistoryParams) (*tesla.ChargingHistoryTesla, error) {
	c.calls++
	return c.inner.ChargingHistory(ctx, creds, params)
}
