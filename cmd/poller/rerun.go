// rerun.go implements the manual-rerun HTTP listener for cmd/poller:
// guardedProcessor serializes the scheduler's nightly tick and any
// API-triggered rerun behind one mutex, and rerunHandler starts a rerun in
// the background. See design.md D3-D5 of platform-add-manual-rerun-api.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/cristianpena/magus-tesla-api/internal/app"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// errCycleBusy is returned by guardedProcessor.ProcessVehicleData when another
// cycle already holds the lock. Scheduler.Run already logs and swallows any
// cycle error without stopping the schedule, so this must read as a SKIP, not
// a crash: it becomes that night's only telemetry.LogCycle record.
var errCycleBusy = errors.New("a vehicle-data cycle is already running; this scheduled run was skipped")

// guardedProcessor serializes every call to the wrapped Processor behind one
// mutex, so the scheduler's nightly tick and an API-triggered rerun can never
// overlap. Built once in main() and shared by both call sites.
type guardedProcessor struct {
	inner app.Processor
	mu    sync.Mutex
}

// ProcessVehicleData implements app.Processor — this is what Scheduler.Run calls.
// Synchronous: acquire, run the whole cycle, release. If a cycle (from either
// trigger) is already running, it returns immediately with errCycleBusy instead of
// running. Scheduler.Run already logs and swallows any cycle error without
// stopping the schedule, so this needs no change to internal/app.
func (g *guardedProcessor) ProcessVehicleData(ctx context.Context, triggeredBy telemetry.TriggeredBy) (telemetry.CycleReport, error) {
	if !g.mu.TryLock() {
		return telemetry.CycleReport{}, errCycleBusy
	}
	defer g.mu.Unlock()
	return g.inner.ProcessVehicleData(ctx, triggeredBy)
}

// TryStartAPIRun attempts the SAME lock for a background, API-triggered run. On
// success it returns a start func the caller must run exactly once, in a new
// goroutine — it releases the lock when the cycle finishes. On failure (a cycle
// from either trigger already holds the lock) it returns ok=false and touches
// nothing: the HTTP handler must not spawn a goroutine in that case.
func (g *guardedProcessor) TryStartAPIRun(ctx context.Context) (start func(), ok bool) {
	if !g.mu.TryLock() {
		return nil, false
	}
	return func() {
		defer g.mu.Unlock()
		report, err := g.inner.ProcessVehicleData(ctx, telemetry.TriggeredByAPI)
		telemetry.LogCycle(report, err)
	}, true
}

// rerunHandler builds the HTTP handler for the manual-rerun endpoint. ctx MUST
// be the poller's ROOT context — the signal.NotifyContext value built once in
// main() — bound here at wiring time, NEVER the *http.Request's own context
// (design.md D4(b)). net/http cancels a request's context the moment the
// handler returns, and this handler returns immediately (that is the point of
// 202): a goroutine started from r.Context() would have its context cancelled
// within microseconds of the response being flushed, killing the cycle before
// it does anything.
func rerunHandler(ctx context.Context, guarded *guardedProcessor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start, ok := guarded.TryStartAPIRun(ctx)
		if !ok {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"started"}`))
		go start()
	}
}

// newRerunMux builds the mux that serves the manual-rerun route. The token is
// the last segment of the registered pattern, so a token holding characters
// ServeMux reads as wildcard syntax (a curly brace, for example) makes
// HandleFunc panic. That panic would kill main(), and compose restarts the
// poller, so the container would crash-loop and the nightly cycle would stop
// running. A bad token must never cost the owner a night of data. Recover the
// panic here and return an error instead: the caller logs it, the listener
// stays off, and the poller keeps its schedule.
func newRerunMux(ctx context.Context, token string, guarded *guardedProcessor) (mux *http.ServeMux, err error) {
	defer func() {
		if r := recover(); r != nil {
			mux = nil
			err = fmt.Errorf("POLLER_RERUN_TOKEN cannot be used in a URL path (%v); generate one with `openssl rand -hex 16`", r)
		}
	}()

	mux = http.NewServeMux()
	mux.HandleFunc("POST /internal/rerun/"+token, rerunHandler(ctx, guarded))
	return mux, nil
}
