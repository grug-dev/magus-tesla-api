package telemetry

import (
	"context"
	"errors"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/tesla"
)

// wakePollInterval is how often the wake helper re-reads the vehicle's state via
// ListVehicles while waiting for it to come online. ~5s balances responsiveness
// against paid Fleet API calls (design D4).
const wakePollInterval = 5 * time.Second

// errVehicleNotListed is returned internally by the state lookup when the target
// vehicle is absent from a ListVehicles result. It is not exported: the wake
// helper surfaces it as a plain error to the caller, which maps it to api-error.
var errVehicleNotListed = errors.New("telemetry: vehicle not present in ListVehicles result")

// waitUntilOnline wakes a sleeping vehicle and polls until it reports online,
// bounded by timeout (design D4). It calls WakeUp once, then polls ListVehicles
// on a fixed interval, filtering to teslaID's State, under a context.WithTimeout
// deadline derived from ctx. It returns:
//
//   - (true, nil)  — the vehicle reached "online" within the timeout.
//   - (false, nil) — the timeout (or the parent ctx) elapsed before "online";
//     the caller records an asleep-timeout failure and does NOT keep waking it.
//   - (false, err) — WakeUp/ListVehicles failed (including tesla.ErrUnauthorized,
//     which the caller detects with errors.Is); the caller maps the reason.
//
// It is pure of storage — it only orchestrates the tesla port, so it is unit-
// testable with a fake VehicleService and a short timeout.
func waitUntilOnline(ctx context.Context, svc tesla.VehicleService, creds tesla.Credentials, teslaID int64, timeout time.Duration) (bool, error) {
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if _, err := svc.WakeUp(deadlineCtx, creds, teslaID); err != nil {
		// A deadline hit during the wake request itself is a timeout, not an
		// API error — the vehicle simply didn't wake in time.
		if isDeadline(deadlineCtx, err) {
			return false, nil
		}
		return false, err
	}

	// Check immediately (a WakeUp can return already-online) before the first wait.
	for {
		online, err := isOnline(deadlineCtx, svc, creds, teslaID)
		if err != nil {
			if isDeadline(deadlineCtx, err) {
				return false, nil
			}
			return false, err
		}
		if online {
			return true, nil
		}

		select {
		case <-deadlineCtx.Done():
			// Timed out (or the parent ctx was cancelled) before online.
			return false, nil
		case <-time.After(wakePollInterval):
		}
	}
}

// isOnline reads the current fleet-wide vehicle list and reports whether teslaID
// is present and online. It returns errVehicleNotListed when the vehicle is not
// in the result (removed on Tesla's side), which the caller maps to api-error.
func isOnline(ctx context.Context, svc tesla.VehicleService, creds tesla.Credentials, teslaID int64) (bool, error) {
	vehicles, err := svc.ListVehicles(ctx, creds)
	if err != nil {
		return false, err
	}
	for _, v := range vehicles {
		if v.ID == teslaID {
			return v.State == "online", nil
		}
	}
	return false, errVehicleNotListed
}

// isDeadline reports whether err is our own timeout deadline (or ctx cancellation)
// rather than a genuine adapter error, so a bounded wait maps to asleep-timeout
// and not api-error.
func isDeadline(ctx context.Context, err error) bool {
	return ctx.Err() != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled))
}
