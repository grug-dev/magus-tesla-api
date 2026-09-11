package telemetry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/clock"
	telemetrydb "github.com/cristianpena/magus-tesla-api/internal/telemetry/db"
	"github.com/cristianpena/magus-tesla-api/internal/tesla"
)

// defaultRetryBackoff is the short pause before the single bounded retry of a
// transient (api-error) collection failure (design D5). It is deliberately small: a
// nightly cycle can afford one brief pause per vehicle, but must not stall for minutes.
// It is held on the service (not a hard const) so the offline tests can drop it to zero
// and stay fast without a live call; production always uses this default.
const defaultRetryBackoff = 2 * time.Second

// store is the narrow persistence seam the collection service writes through. It
// exists so CollectAll can be unit-tested offline with a fake store (no TEST_DATABASE_URL,
// no Postgres) while the production path uses the telemetrydb-backed implementation
// built in NewService. Keeping it unexported and domain-typed (Snapshot / Attempt in,
// error out) is what keeps pgtype from leaking past the module boundary: the mapping
// to pgtype happens ONLY inside the telemetrydb-backed implementation below.
//
// The read methods (latestSnapshotsByVehicles and friends) are added here so the
// Reader implementation can also be unit-tested offline via the same fake-store
// pattern. The mapping from pgtype→domain happens in mapping.go (rowToSnapshot),
// confining pgtype to the concrete dbStore.
//
// upsertSuperchargerHistory is the Source B write seam: it maps a domain
// SuperchargerHistory into telemetrydb params at the DB boundary. pgtype never
// appears in the SuperchargerHistory type or any public interface (design DBS4/B4).
type store interface {
	insertSnapshot(ctx context.Context, s Snapshot) error
	insertPollAttempt(ctx context.Context, a Attempt) error
	latestSnapshotsByVehicles(ctx context.Context, teslaIDs []int64) ([]Snapshot, error)
	snapshotsByVehicleSince(ctx context.Context, teslaID int64, since time.Time) ([]Snapshot, error)
	snapshotsByVehicleBetween(ctx context.Context, teslaID int64, start, end time.Time) ([]Snapshot, error)
	// snapshotsByVehicleUpdatedSince returns every snapshot for teslaID whose
	// updated_at is at or after since, ordered oldest-first by updated_at
	// (Reader.SnapshotsByVehicleUpdatedSince). Reuses rowToSnapshot — no new
	// mapper.
	snapshotsByVehicleUpdatedSince(ctx context.Context, teslaID int64, since time.Time) ([]Snapshot, error)
	upsertSuperchargerHistory(ctx context.Context, s SuperchargerHistory) error
	// snapshotPrecedingDay backs the public Reader.SnapshotPrecedingDay: the
	// most recent stored snapshot for teslaID whose captured_date is strictly
	// before `day`, or (nil, nil) when none exists. Its bound is a calendar
	// day, not an instant — see the Reader.SnapshotPrecedingDay doc comment
	// (telemetry.go) for the full zone-safety rationale. This is the module's
	// former private previousSnapshot seam, now the sole predecessor lookup
	// and the only one telemetry itself never calls — its one consumer is
	// internal/analytics via the public Reader port. Implemented by dbStore in
	// reader.go and by every test fake in this file and reader_test.go.
	snapshotPrecedingDay(ctx context.Context, teslaID int64, day time.Time) (*Snapshot, error)
}

// service is the concrete Collector. It consumes the account and tesla PORTS only
// (never their DB packages) and writes snapshots/attempts through the store seam.
type service struct {
	acct  account.Service
	tsla  tesla.VehicleService
	store store
	cfg   Config
	// retryBackoff is the pause before the one bounded api-error retry (D5). Set from
	// defaultRetryBackoff in NewService; overridden to zero in offline tests to keep
	// them fast. Never exposed publicly.
	retryBackoff time.Duration
}

// Compile-time proof that *service satisfies the public Collector port (task 4.2).
var _ Collector = (*service)(nil)

// NewService builds the telemetry collection service. It wires the account + tesla
// ports and a telemetrydb-backed store around the given pool. The returned value is
// the Collector port — callers (cmd/poller, the scheduler) depend on the interface,
// never on *service. cfg carries the wake timeout and an optional test clock.
func NewService(pool *pgxpool.Pool, acct account.Service, tsla tesla.VehicleService, cfg Config) Collector {
	return &service{
		acct:         acct,
		tsla:         tsla,
		store:        newLoggingStore(&dbStore{q: telemetrydb.New(pool)}),
		cfg:          cfg,
		retryBackoff: defaultRetryBackoff,
	}
}

// now returns the current time, honoring the injected test clock when present so
// captured_at / attempted_at and any timeout math are deterministic in tests.
// Production falls back to clock.Now() — the platform's default-zone "now"
// (RM35-telemetry-adopt-clock, roadmap D4) — never a raw time.Now().
func (s *service) now() time.Time {
	if s.cfg.Clock != nil {
		return s.cfg.Clock()
	}
	return clock.Now()
}

// location returns the configured timezone for calendar-day derivation, honoring
// the injected Config.Location when present so CapturedDate is deterministic in
// tests; production falls back to clock.Zone() — the platform's default zone,
// America/Bogota (RM35-telemetry-adopt-clock, roadmap D4) — never time.Local.
func (s *service) location() *time.Location {
	if s.cfg.Location != nil {
		return s.cfg.Location
	}
	return clock.Zone()
}

// CollectAll runs one collection cycle over every registered vehicle across all
// accounts (the Collector port). It enumerates vehicles via the account port, elects
// one polling account per distinct vehicle (electPollingVehicles — a car registered to
// more than one account is fetched once a night, not once per registering account),
// groups the elected vehicles by account for per-account token batching (D3), and
// collects each vehicle with full per-vehicle isolation: a single vehicle's failure is
// recorded as a poll_attempt and never aborts the account or the cycle. It returns an
// error ONLY when the whole-cycle enumeration itself fails (D9) — never for an
// individual vehicle.
//
// run identifies this invocation (RunID/TriggeredBy, RM29-app-add-process-vehicle-data
// design D5) and is threaded straight through to collectAccount/record as a plain
// parameter — it is NEVER stored as a field on *service. *service is a long-lived
// object built once in cmd/poller and reused across every scheduled cycle; storing the
// current run's identity as mutable state on it would let a concurrent or future
// overlapping call silently attribute one run's attempts to another's RunID the moment
// two calls interleaved. A parameter cannot do that by construction.
func (s *service) CollectAll(ctx context.Context, run RunContext) (CycleReport, error) {
	report := CycleReport{FailuresByReason: map[Reason]int{}}

	// counted wraps s.tsla to count every Tesla Fleet API request this cycle makes
	// (design D9/D10). Constructed fresh on this call's stack, never stored on
	// *service, and threaded down as an explicit parameter — mirrors the same
	// per-call-not-per-field pattern RunContext already established.
	counted := newCallCounter(s.tsla)

	vehicles, err := s.acct.AllRegisteredVehicles(ctx)
	if err != nil {
		// Whole-cycle failure: we could not even list what to collect. This is the
		// ONLY error CollectAll returns (D9); everything else is contained per vehicle.
		return report, fmt.Errorf("telemetry: enumerating registered vehicles: %w", err)
	}

	// Elect one account to poll each distinct vehicle, so a car registered to more
	// than one account is fetched once a night, not once per registering account.
	elected := electPollingVehicles(vehicles)

	// Group by owning account so we resolve each account's access token exactly once
	// and make a single ListVehicles call per account (per-account batching, D3).
	byAccount := groupByAccount(elected)
	report.AccountsAttempted = len(byAccount)

	for accountID, owned := range byAccount {
		s.collectAccount(ctx, run, counted, accountID, owned, &report)
	}

	report.AccountsSucceeded = report.AccountsAttempted - report.AccountsFailed
	report.TeslaAPICalls = counted.calls

	return report, nil
}

// groupByAccount buckets the already-elected vehicle list by owning account id so
// the collection loop can batch token resolution and ListVehicles per account (D3).
func groupByAccount(vehicles []account.OwnedVehicle) map[uuid.UUID][]account.OwnedVehicle {
	byAccount := map[uuid.UUID][]account.OwnedVehicle{}
	for _, v := range vehicles {
		byAccount[v.AccountID] = append(byAccount[v.AccountID], v)
	}
	return byAccount
}

// electPollingVehicles picks exactly one owning account to poll each distinct
// tesla_id, so a car registered to more than one account is fetched once per
// night, not once per registering account. Prefers OWNER; falls back to any
// candidate; a vehicle is NEVER dropped regardless of AccessType (a missed
// night is a permanent gap — the Fleet API has no date filter). Ties (two
// OWNERs, or no OWNER with several candidates) break on the lowest
// AccountID, compared as raw bytes — arbitrary but deterministic, so the
// elected account does not flap between runs with no real change.
//
// Pure Go, no DB call: it reads only what AllRegisteredVehicles already
// returned. It does NOT check whether the elected account's token is usable
// — that stays collectAccount's job via the existing AccessTokenFor call, so
// election never pays AccessTokenFor's cost (a FOR UPDATE row lock plus a
// single-use refresh-token rotation) for a candidate it might not even use.
//
// Sorts its own input explicitly rather than trusting the caller's order:
// ListAllVehicles happens to return rows ordered (account_id, tesla_id), but
// that is an account-module implementation detail telemetry must not
// silently depend on.
func electPollingVehicles(vehicles []account.OwnedVehicle) []account.OwnedVehicle {
	sorted := make([]account.OwnedVehicle, len(vehicles))
	copy(sorted, vehicles)
	sort.Slice(sorted, func(i, j int) bool {
		return bytes.Compare(sorted[i].AccountID[:], sorted[j].AccountID[:]) < 0
	})

	elected := make(map[int64]account.OwnedVehicle, len(sorted))
	for _, v := range sorted {
		current, ok := elected[v.TeslaID]
		if !ok {
			elected[v.TeslaID] = v
			continue
		}
		if !isOwner(current) && isOwner(v) {
			elected[v.TeslaID] = v
		}
		// Otherwise keep current: it already has the lower AccountID (sorted
		// ascending above) at the same or better preference level.
	}

	result := make([]account.OwnedVehicle, 0, len(elected))
	for _, v := range elected {
		result = append(result, v)
	}
	return result
}

// isOwner reports whether v's AccessType is Tesla's "OWNER" value. A nil
// AccessType (not captured at seed time) is treated as not-owner, the same
// as any other non-OWNER value — it never wins an election tie against a
// confirmed OWNER candidate.
func isOwner(v account.OwnedVehicle) bool {
	return v.AccessType != nil && *v.AccessType == "OWNER"
}

// collectAccount collects every vehicle for one account (per-account batching, D3).
// It resolves the account's access token once, then makes a SINGLE ListVehicles call to
// read the current State of all the account's vehicles up front. Per vehicle it then
// avoids a redundant WakeUp: an already-online vehicle goes straight to VehicleData,
// while an asleep/offline/absent one runs the bounded wake flow (D4). This is what keeps
// the nightly cycle to one ListVehicles + one AccessTokenFor per account (instead of per
// vehicle) and spends a paid WakeUp only on vehicles that actually need waking.
//
// Two whole-account short-circuits, both recording every vehicle without further Tesla
// calls: (1) AccessTokenFor failure (no connection / refresh failure) → unauthorized;
// (2) the up-front ListVehicles returning ErrUnauthorized (the account's token is
// rejected fleet-wide, so per-vehicle calls would fail identically) → unauthorized.
//
// After the per-vehicle snapshot loop, collectAccount also fetches the account's
// Supercharger history (Source B, design DBS7). A ChargingHistory call failure
// only increments ChargingFetchFailures — it never aborts or affects the snapshot
// collection. No poll_attempts row is written for charging (poll_attempts is
// per-vehicle; charging is per-account — outcomes live in CycleReport).
//
// tsla is the tesla.VehicleService to use for this cycle's calls — CollectAll
// passes its freshly-constructed callCounter (design D10), never s.tsla directly,
// so every Tesla request this account's collection makes is counted.
func (s *service) collectAccount(ctx context.Context, run RunContext, tsla tesla.VehicleService, accountID uuid.UUID, owned []account.OwnedVehicle, report *CycleReport) {
	token, err := s.acct.AccessTokenFor(ctx, accountID)
	if err != nil {
		// No connection (or a refresh failure) applies to the WHOLE account: record
		// every one of its vehicles as unauthorized and skip all Tesla calls. A
		// refresh error other than ErrNoTeslaConnection is still an auth problem for
		// this account, so it maps to unauthorized too (no per-vehicle retry helps).
		// No captureVehicleConfig call here (design.md D2): no VehicleData was fetched
		// for any vehicle in this branch, so a config write-back would always be a
		// guaranteed no-op — this omission is deliberate, not a gap.
		report.AccountsFailed++
		for _, v := range owned {
			s.record(ctx, run, accountID, v.TeslaID, ReasonUnauthorized, report)
		}
		return
	}

	creds := tesla.Credentials{AccessToken: token}

	// One ListVehicles per account (D3): read every vehicle's current State once.
	states, err := s.listStates(ctx, tsla, creds)
	if err != nil {
		if errors.Is(err, tesla.ErrUnauthorized) {
			// The account's token is rejected fleet-wide: record every vehicle as
			// unauthorized and make NO further Tesla calls for this account.
			// No captureVehicleConfig call here (design.md D2): no VehicleData was
			// fetched for any vehicle in this branch, so a config write-back would
			// always be a guaranteed no-op — this omission is deliberate, not a gap.
			report.AccountsFailed++
			for _, v := range owned {
				s.record(ctx, run, accountID, v.TeslaID, ReasonUnauthorized, report)
			}
			return
		}
		// A transient ListVehicles failure (non-401) means we could not read any
		// vehicle's state. Rather than blindly waking every vehicle (which would
		// re-introduce the very WakeUp waste this change removes), treat the whole
		// account's fetch as an api-error for this cycle and record each vehicle
		// accordingly — per-vehicle isolation is preserved (the cycle continues).
		// No captureVehicleConfig call here (design.md D2): same reasoning as above —
		// no VehicleData was fetched for any vehicle in this branch. This branch does
		// NOT increment AccountsFailed (roadmap D4: only the two auth-shaped
		// short-circuits count as a whole-account failure).
		for _, v := range owned {
			s.record(ctx, run, accountID, v.TeslaID, ReasonAPIError, report)
		}
		return
	}

	for _, v := range owned {
		reason, cfg := s.collectVehicle(ctx, tsla, creds, v, states[v.TeslaID])
		s.record(ctx, run, v.AccountID, v.TeslaID, reason, report)
		s.captureVehicleConfig(ctx, v, cfg, report)
	}

	// Source B: fetch Supercharger session history for this account (design DBS7).
	// Zero params = full fetch, no vehicle wake. This call is per-account (not per
	// vehicle): one HTTP call returns all sessions for all vehicles in the account.
	s.collectChargingHistory(ctx, tsla, accountID, owned, creds, report)
}

// collectChargingHistory fetches the account's Supercharger session history and
// upserts each session. It is called after the per-vehicle snapshot loop so a
// ChargingHistory failure cannot abort or affect snapshot collection. On failure
// it only increments ChargingFetchFailures (design DBS7). Per-session isolation:
// a single upsert failure is logged but does not abort the remaining sessions.
// No poll_attempts rows are written — outcomes live in CycleReport (design DBS7).
//
// tsla is the tesla.VehicleService to use — passed down from CollectAll's
// callCounter (design D10) so this call is counted too.
func (s *service) collectChargingHistory(ctx context.Context, tsla tesla.VehicleService, accountID uuid.UUID, owned []account.OwnedVehicle, creds tesla.Credentials, report *CycleReport) {
	history, err := tsla.ChargingHistory(ctx, creds, tesla.ChargingHistoryParams{})
	if err != nil {
		// Charging-history fetch failure is isolated: never aborts the cycle and
		// never affects the snapshot loop. Count it for observability.
		report.ChargingFetchFailures++
		return
	}

	// Build VIN → TeslaID map from the account's registered vehicles so we can
	// resolve tesla_id for each session. Sessions for VINs not in this map get
	// tesla_id = NULL (sold / deregistered vehicle — row is kept, VIN is preserved).
	vinToTeslaID := make(map[string]int64, len(owned))
	for _, v := range owned {
		vinToTeslaID[v.VIN] = v.TeslaID
	}

	for _, session := range history.Data {
		// Resolve tesla_id: nil when the session's VIN is no longer a registered vehicle.
		var teslaID *int64
		if id, ok := vinToTeslaID[session.VIN]; ok {
			idCopy := id
			teslaID = &idCopy
		}

		// Derive the four computed fields from fees[] in Go (not in SQL) so they
		// are testable offline without a DB (design DBS2).
		domainSession := SuperchargerHistory{
			SessionID:           session.SessionID,
			AccountID:           accountID,
			VIN:                 session.VIN,
			TeslaID:             teslaID,
			SiteLocationName:    session.SiteLocationName,
			CountryCode:         session.CountryCode,
			ChargeStartDateTime: session.ChargeStartDateTime,
			ChargeStopDateTime:  session.ChargeStopDateTime,
			BillingType:         session.BillingType,
			VehicleMakeType:     session.VehicleMakeType,
			EnergyKWh:           deriveEnergyKWh(session.Fees),
			TotalCost:           deriveTotalCost(session.Fees),
			Currency:            deriveCurrency(session.Fees),
			IsPaid:              deriveIsPaid(session.Fees),
			// RawData is the verbatim session bytes captured in UnmarshalJSON (D13,
			// L2). NOT a re-marshal of the typed struct — lossless whole-session blob,
			// true parity with vehicle_snapshots.raw_data.
			RawData: session.Raw,
		}

		// Resolve unlatch_date_time: Tesla sends a zero time when absent; convert to nil.
		if !session.UnlatchDateTime.IsZero() {
			t := session.UnlatchDateTime
			domainSession.UnlatchDateTime = &t
		}

		if err := s.store.upsertSuperchargerHistory(ctx, domainSession); err != nil {
			// Per-session isolation: a single upsert failure does not abort the
			// charging ingestion pass. Continue to the next session.
			continue
		}
		report.ChargingSessionsUpserted++
	}
}

// listStates makes the single per-account ListVehicles call (D3) and returns a
// teslaID → State map. A vehicle registered in the account registry but absent from the
// live result (removed on Tesla's side) simply has no entry — collectVehicle then treats
// it as not-online and runs the bounded wake flow, whose own ListVehicles poll surfaces
// it as errVehicleNotListed → api-error (D3's "absent from ListVehicles" handling).
//
// tsla is the tesla.VehicleService to use — passed down from CollectAll's
// callCounter (design D10) so this call is counted too.
func (s *service) listStates(ctx context.Context, tsla tesla.VehicleService, creds tesla.Credentials) (map[int64]string, error) {
	vehicles, err := tsla.ListVehicles(ctx, creds)
	if err != nil {
		return nil, err
	}
	states := make(map[int64]string, len(vehicles))
	for _, v := range vehicles {
		states[v.ID] = v.State
	}
	return states, nil
}

// vehicleConfig carries the two static vehicle_config values observed during one
// attemptVehicle pass, so collectAccount (never attemptVehicle, RD6) can decide whether to
// write them back through the account port. The zero value (both fields "") means "not
// observed this pass" — either the attempt never reached VehicleData (wake timeout,
// unauthorized, a persistent api-error) or Tesla itself reported an empty string for one or
// both fields. Either way the caller's empty-string guard (RD5) treats it identically: skip.
type vehicleConfig struct {
	exteriorColor string
	carType       string
}

// collectVehicle collects one vehicle with a single bounded retry for transient
// (api-error) failures (D5). Unauthorized and asleep-timeout are terminal — never
// retried. state is the vehicle's State from the per-account ListVehicles (D3), used to
// decide whether a WakeUp is even needed. It returns the Reason to record plus the
// observed vehicleConfig (RM6 tier 2); it does NOT write the poll_attempt (the caller
// does, exactly once per vehicle per cycle). The retry's result — not the first
// attempt's — is what reaches the caller for both return values: this is what gives
// RD6's "at most one write-back per vehicle per cycle" for free.
//
// tsla is the tesla.VehicleService to use — passed down from CollectAll's
// callCounter (design D10) through to attemptVehicle, unchanged, on both the
// first attempt and the retry.
func (s *service) collectVehicle(ctx context.Context, tsla tesla.VehicleService, creds tesla.Credentials, v account.OwnedVehicle, state string) (Reason, vehicleConfig) {
	reason, cfg := s.attemptVehicle(ctx, tsla, creds, v, state)
	if reason == ReasonAPIError {
		// Exactly one retry after a short backoff, and only for transient errors.
		select {
		case <-ctx.Done():
			return reason, cfg
		case <-time.After(s.retryBackoff):
		}
		reason, cfg = s.attemptVehicle(ctx, tsla, creds, v, state)
	}
	return reason, cfg
}

// attemptVehicle performs one [wake→]fetch→map→store pass for a single vehicle and
// returns the Reason for the outcome plus the observed vehicleConfig (RM6 tier 2). It is
// fully error-contained: any failure maps to a Reason and is returned, never propagated.
// The wake step is SKIPPED when the per-account ListVehicles already reported the vehicle
// online (D3/D4): an online vehicle goes straight to VehicleData with no paid WakeUp; only
// an asleep/offline/absent vehicle runs the bounded wake flow. Reason mapping (D5):
//   - wake deadline elapsed (waitUntilOnline returned false,nil) → asleep-timeout
//   - tesla.ErrUnauthorized anywhere → unauthorized (terminal)
//   - any other tesla/decode/store error → api-error (the caller retries once)
//   - snapshot stored → ok
//   - on ReasonOK, the returned vehicleConfig carries the observed exterior_color/car_type
//     from this pass's VehicleData response; every other return path yields the zero
//     vehicleConfig{}.
//
// tsla is the tesla.VehicleService to use — passed down from CollectAll's
// callCounter (design D10). waitUntilOnline/isOnline need no change: both
// already take svc tesla.VehicleService explicitly, so tsla passes through
// unchanged.
func (s *service) attemptVehicle(ctx context.Context, tsla tesla.VehicleService, creds tesla.Credentials, v account.OwnedVehicle, state string) (Reason, vehicleConfig) {
	if state != "online" {
		// Asleep/offline/absent-from-the-list → run the bounded wake flow (D4). An
		// already-online vehicle bypasses this entirely, saving a paid WakeUp.
		online, err := waitUntilOnline(ctx, tsla, creds, v.TeslaID, s.cfg.WakeTimeout)
		if err != nil {
			return s.logAPIError(v.TeslaID, "wake", err, reasonFor(err)), vehicleConfig{}
		}
		if !online {
			// The bounded wake window elapsed before the vehicle reported online.
			return ReasonAsleepTimeout, vehicleConfig{}
		}
	}

	data, raw, err := tsla.VehicleData(ctx, creds, v.TeslaID)
	if err != nil {
		return s.logAPIError(v.TeslaID, "VehicleData", err, reasonFor(err)), vehicleConfig{}
	}

	// One clock reading serves the snapshot's captured_at (design D7 — surviving
	// residue of the removed dayStart-bounded predecessor lookup: a single s.now()
	// call still keeps every timestamp on this row internally consistent).
	capturedAt := s.now()
	snap := snapshotFrom(v.TeslaID, capturedAt, s.location(), data, raw)

	// attemptVehicle is a pure fetch → map → store pass (RM29-telemetry-drop-
	// derived-columns, design D8): the five derived-consumption columns and the
	// predecessor lookup that fed them were removed from this module. The
	// derivation now lives in internal/analytics, which computes the same figures
	// from this row's surviving raw columns (odometer_km, battery_level_pct,
	// captured_date) via telemetry.Reader.SnapshotPrecedingDay — telemetry itself
	// never reads its own history back on the write path.
	if err := s.store.insertSnapshot(ctx, snap); err != nil {
		// A store failure is transient from the cycle's point of view (retry once).
		return s.logAPIError(v.TeslaID, "insertSnapshot", err, ReasonAPIError), vehicleConfig{}
	}
	return ReasonOK, vehicleConfig{exteriorColor: data.VehicleConfig.ExteriorColor, carType: data.VehicleConfig.CarType}
}

// captureVehicleConfig persists the two static vehicle_config values observed during this
// cycle's collectVehicle pass for v, through the account port, at most once per vehicle per
// cycle. It never touches s.tsla and is called only from collectAccount (RD6) —
// attemptVehicle stays a pure fetch/map/store pass with no account-port access.
//
// Two independent skip conditions, checked in order:
//  1. RD2 (Go-side guard): v's already-known registry record has BOTH ExteriorColor and
//     CarType non-nil — already captured in a prior cycle, nothing to do. (The account
//     module's own SQL WHERE (exterior_color IS NULL OR car_type IS NULL) is the second,
//     independent guard — tier 1 design D4 — so this Go-side check is belt-and-braces, not
//     the only thing preventing a clobber.)
//  2. RD5: either observed value in cfg is "" — either Tesla reported an empty string, or
//     the vehicle's capture attempt never reached VehicleData this cycle (cfg is the zero
//     value). Writing "" would satisfy the account port's own IS-NULL guard forever,
//     permanently freezing the vehicle in a "captured" state with no real value and no way
//     to self-heal.
//
// On a genuine write-back attempt, a returned error increments report.ConfigCaptureFailures
// (RD4) — it does NOT alter the vehicle's already-recorded Reason (record() already ran one
// line above the call site) and does NOT write a poll_attempts row of its own; the failure
// is retried for free on the next cycle since the registry row is still missing a value.
func (s *service) captureVehicleConfig(ctx context.Context, v account.OwnedVehicle, cfg vehicleConfig, report *CycleReport) {
	if v.ExteriorColor != nil && v.CarType != nil {
		return
	}
	if cfg.exteriorColor == "" || cfg.carType == "" {
		return
	}
	if err := s.acct.SetVehicleConfigIfEmpty(ctx, v.AccountID, v.TeslaID, cfg.exteriorColor, cfg.carType); err != nil {
		report.ConfigCaptureFailures++
	}
}

// logAPIError is a TEMPORARY diagnostic seam. The per-cycle summary (LogCycle)
// intentionally collapses failures to a reason bucket and drops the underlying error
// text, which makes an `api-error` count opaque. When a step maps to ReasonAPIError this
// logs the real Tesla/decode/store error keyed by tesla_id, so an operator can see *why*
// a vehicle failed; non-api-error reasons (unauthorized/asleep-timeout) stay quiet since
// they are already self-explanatory in the summary. Remove once the failure is diagnosed.
func (s *service) logAPIError(teslaID int64, step string, err error, reason Reason) Reason {
	if reason == ReasonAPIError {
		log.Printf("telemetry: vehicle %d %s failed: %v", teslaID, step, err)
	}
	return reason
}

// reasonFor maps a tesla/account error to its poll_attempts Reason. An unauthorized
// signal (either sentinel) is terminal; everything else is a retriable api-error.
func reasonFor(err error) Reason {
	if errors.Is(err, tesla.ErrUnauthorized) || errors.Is(err, account.ErrNoTeslaConnection) {
		return ReasonUnauthorized
	}
	return ReasonAPIError
}

// record writes exactly one poll_attempt for a vehicle and folds the outcome into the
// cycle report. A store failure on the attempt row is swallowed on purpose: it must
// never abort the cycle, and the report already reflects the true collection outcome.
// run stamps RunID/TriggeredBy onto the Attempt it writes (design D5) — it is passed
// down unchanged from CollectAll, never generated or cached here.
func (s *service) record(ctx context.Context, run RunContext, accountID uuid.UUID, teslaID int64, reason Reason, report *CycleReport) {
	outcome := OutcomeFailure
	if reason == ReasonOK {
		outcome = OutcomeSuccess
	}

	_ = s.store.insertPollAttempt(ctx, Attempt{
		PolledByAccountID: accountID,
		TeslaID:           teslaID,
		AttemptedAt:       s.now(),
		Outcome:           outcome,
		Reason:            reason,
		RunID:             run.RunID,
		TriggeredBy:       run.TriggeredBy,
	})

	report.Attempted++
	if reason == ReasonOK {
		report.Succeeded++
	} else {
		report.FailuresByReason[reason]++
	}
}

// snapshotFrom maps the tesla adapter's ...Tesla DTO (plus the lossless raw payload)
// into our clean domain Snapshot (ai/architecture.md §6 — the adapter's DTO never
// flows into domain logic un-mapped). Distance/range/pressure fields are converted to
// their DISPLAY unit exactly once here, by calling the tesla adapter's Km()/PSI()
// companions — never by multiplying inline (telemetry-store-display-units design D3).
// Temperature is assigned straight from the DTO with no conversion: the Fleet API
// already reports Celsius. SentryMode stays *bool so an absent field remains distinct
// from a reported-off sentry (nil ≠ *false, D1).
//
// Source A (RM2-telemetry-add-charging-stats): the 5 charge-enrichment fields are
// stored as the ACTUAL DTO value pointer-wrapped — NO zero-is-absent heuristic (D12,
// design DSA3). The plain DTO fields always carry a concrete value (0 when idle),
// so every new row is non-NULL. A truthful 0 must be preserved; interpreting a 0 in
// context (e.g. against ChargingState) is the dashboard's responsibility. NULL is
// reserved exclusively for pre-migration rows that were never backfilled (DSA1).
//
// MaxRangeChargeCounter follows the same D12/DSA3 pointer-wrap convention: the DTO
// field is a plain int (0 when never charged to max-range), so ptr() stores *0 as
// non-NULL — a truthful zero is preserved, not collapsed into NULL. NULL means the
// row predates migration 20260801000001 (backfilled from raw_data in the migration Up).
//
// latitude/longitude and fast_charger_type are NOT extracted here — those typed columns
// were dropped in migration 20260801000001. Values remain lossless in raw_data JSONB.
func snapshotFrom(teslaID int64, capturedAt time.Time, loc *time.Location, data *tesla.VehicleDataTesla, raw []byte) Snapshot {
	return Snapshot{
		TeslaID:           teslaID,
		CapturedAt:        capturedAt,
		CapturedDate:      clock.CalendarDay(capturedAt, loc),
		BatteryLevelPct:   data.ChargeState.BatteryLevel,
		BatteryRangeKm:    data.ChargeState.BatteryRangeKm(),
		ChargingState:     data.ChargeState.ChargingState,
		ChargeLimitSocPct: data.ChargeState.ChargeLimitSoc,
		OdometerKm:        data.VehicleState.OdometerKm(),
		InsideTempC:       data.ClimateState.InsideTemp,
		OutsideTempC:      data.ClimateState.OutsideTemp,
		Locked:            data.VehicleState.Locked,
		SentryMode:        data.VehicleState.SentryMode,
		CarVersion:        data.VehicleState.CarVersion,
		RawData:           raw,
		// Source A enrichment — actual DTO values, pointer-wrapped (D12/DSA3).
		// ptr(v) returns &v; a 0 is a truthful reading and is stored non-NULL.
		ChargeEnergyAddedKWh:  ptr(data.ChargeState.ChargeEnergyAdded),
		ChargerPowerKW:        ptr(data.ChargeState.ChargerPower),
		ChargerVoltageV:       ptr(data.ChargeState.ChargerVoltage),
		ChargerActualCurrentA: ptr(data.ChargeState.ChargerActualCurrent),
		UsableBatteryLevelPct: ptr(data.ChargeState.UsableBatteryLevel),
		// MaxRangeChargeCounter: same D12/DSA3 pointer-wrap convention. A reported 0
		// (new vehicle, never charged to max-range) is stored non-NULL as *0.
		MaxRangeChargeCounter: ptr(data.ChargeState.MaxRangeChargeCounter),
		// TPMS pressure enrichment — converted to PSI via the tesla adapter's
		// TpmsPressure*PSI() companions BEFORE ptr() wraps (design D3), then
		// pointer-wrapped exactly as before (D12/DSA3): a truthful 0.0 PSI is a
		// non-NULL reading; NULL is reserved for pre-migration rows.
		TpmsPressureFLPSI: ptr(data.VehicleState.TpmsPressureFLPSI()),
		TpmsPressureFRPSI: ptr(data.VehicleState.TpmsPressureFRPSI()),
		TpmsPressureRLPSI: ptr(data.VehicleState.TpmsPressureRLPSI()),
		TpmsPressureRRPSI: ptr(data.VehicleState.TpmsPressureRRPSI()),
	}
}

// ptr wraps a plain value in a pointer, returning *T. Used by snapshotFrom to
// store the actual DTO value (including a truthful 0/"") into a *T field without
// a zero-is-absent heuristic (design DSA3/D12 of RM2-telemetry-add-charging-stats).
func ptr[T any](v T) *T { return &v }

// --- telemetrydb-backed store (the ONLY place pgtype is touched) ---

// dbStore is the production store: it maps our domain Snapshot / Attempt into the
// generated telemetrydb params at the DB boundary and back. This is the single place
// pgtype appears in the module — *bool↔pgtype.Bool and time.Time↔pgtype.Timestamptz
// conversions live here so pgtype never leaks into the service logic or public types
// (ai/go-conventions.md §persistence).
type dbStore struct {
	q *telemetrydb.Queries
}

func (d *dbStore) insertSnapshot(ctx context.Context, s Snapshot) error {
	return d.q.InsertVehicleSnapshot(ctx, telemetrydb.InsertVehicleSnapshotParams{
		TeslaID:           s.TeslaID,
		CapturedAt:        timestamptzFrom(s.CapturedAt),
		CapturedDate:      dateFrom(s.CapturedDate),
		RawData:           s.RawData,
		BatteryLevelPct:   int32(s.BatteryLevelPct),
		BatteryRangeKm:    s.BatteryRangeKm,
		ChargingState:     s.ChargingState,
		ChargeLimitSocPct: int32(s.ChargeLimitSocPct),
		OdometerKm:        s.OdometerKm,
		InsideTempC:       s.InsideTempC,
		OutsideTempC:      s.OutsideTempC,
		Locked:            s.Locked,
		SentryMode:        boolPtrToPgBool(s.SentryMode),
		CarVersion:        s.CarVersion,
		// Source A (RM2-telemetry-add-charging-stats): nullable charge-enrichment columns.
		// nil → invalid pgtype (SQL NULL); non-nil → valid with the concrete value.
		// Same Valid-field pattern as boolPtrToPgBool. pgtype never leaks past this boundary.
		// latitude/longitude/fast_charger_type dropped in 20260801000001; not sent here.
		ChargeEnergyAddedKwh:  float64PtrToPgFloat8(s.ChargeEnergyAddedKWh),
		ChargerPowerKw:        intPtrToPgInt4(s.ChargerPowerKW),
		ChargerVoltageV:       intPtrToPgInt4(s.ChargerVoltageV),
		ChargerActualCurrentA: intPtrToPgInt4(s.ChargerActualCurrentA),
		UsableBatteryLevelPct: intPtrToPgInt4(s.UsableBatteryLevelPct),
		// MaxRangeChargeCounter: same nil→NULL / non-nil→valid pattern (D12/DSA3).
		MaxRangeChargeCounter: intPtrToPgInt4(s.MaxRangeChargeCounter),
		// TPMS pressure fields: nullable REAL (pgtype.Float4), now storing PSI (converted
		// at capture time, telemetry-store-display-units design D1/D3). nil → invalid
		// (NULL); non-nil → valid Float32. Uses float64PtrToPgFloat4 (not Float8) because
		// the schema columns are REAL (float4). Precision is adequate for PSI readings.
		TpmsPressureFlPsi: float64PtrToPgFloat4(s.TpmsPressureFLPSI),
		TpmsPressureFrPsi: float64PtrToPgFloat4(s.TpmsPressureFRPSI),
		TpmsPressureRlPsi: float64PtrToPgFloat4(s.TpmsPressureRLPSI),
		TpmsPressureRrPsi: float64PtrToPgFloat4(s.TpmsPressureRRPSI),
	})
}

// float64PtrToPgFloat8 maps a *float64 to a nullable pgtype.Float8. nil → invalid
// (SQL NULL); non-nil → valid with the concrete value. Mirrors boolPtrToPgBool.
func float64PtrToPgFloat8(v *float64) pgtype.Float8 {
	if v == nil {
		return pgtype.Float8{Valid: false}
	}
	return pgtype.Float8{Float64: *v, Valid: true}
}

// float64PtrToPgFloat4 maps a *float64 to a nullable pgtype.Float4 (REAL / float32).
// nil → invalid (SQL NULL); non-nil → valid with the value narrowed to float32.
// Used for TPMS pressure columns, which are PostgreSQL REAL (single-precision).
// Precision loss is acceptable: tire pressure at float32 is ~7 significant digits,
// more than enough for bar/PSI readings.
func float64PtrToPgFloat4(v *float64) pgtype.Float4 {
	if v == nil {
		return pgtype.Float4{Valid: false}
	}
	return pgtype.Float4{Float32: float32(*v), Valid: true}
}

// intPtrToPgInt4 maps a *int to a nullable pgtype.Int4. nil → invalid (SQL NULL);
// non-nil → valid Int32. Mirrors boolPtrToPgBool.
func intPtrToPgInt4(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{Valid: false}
	}
	return pgtype.Int4{Int32: int32(*v), Valid: true}
}

// stringPtrToPgText maps a *string to a nullable pgtype.Text. nil → invalid
// (SQL NULL); non-nil → valid with the concrete string. Mirrors boolPtrToPgBool.
func stringPtrToPgText(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: *v, Valid: true}
}

// teslaIDToPgInt8 wraps a non-nullable int64 tesla id as a valid pgtype.Int8 query
// parameter. It lives here so pgtype stays confined to the module's DB-boundary files
// (service.go/mapping.go) and never appears in reader.go (ai/go-conventions.md §persistence).
func teslaIDToPgInt8(v int64) pgtype.Int8 {
	return pgtype.Int8{Int64: v, Valid: true}
}

// runIDToPgUUID wraps a non-nullable uuid.UUID as a valid pgtype.UUID query
// parameter. The run_id COLUMN is nullable (legacy pre-migration rows only,
// design D7), but every Attempt written by this module carries a real RunID
// (design D5), so there is no NULL-writing branch here — mirroring
// teslaIDToPgInt8's always-valid wrap above. uuid.UUID and pgtype.UUID.Bytes are
// both [16]byte under the hood, so the conversion is a plain reinterpretation.
func runIDToPgUUID(v uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(v), Valid: true}
}

func (d *dbStore) insertPollAttempt(ctx context.Context, a Attempt) error {
	return d.q.InsertPollAttempt(ctx, telemetrydb.InsertPollAttemptParams{
		PolledByAccountID: a.PolledByAccountID,
		TeslaID:           a.TeslaID,
		AttemptedAt:       timestamptzFrom(a.AttemptedAt),
		Outcome:           string(a.Outcome),
		Reason:            string(a.Reason),
		RunID:             runIDToPgUUID(a.RunID),
		TriggeredBy:       string(a.TriggeredBy),
	})
}

// latestSnapshotsByVehicles implements the read seam: it calls the DISTINCT ON
// query generated by sqlc and maps each row to the domain Snapshot via rowToSnapshot
// (mapping.go). pgtype never appears in the return type — it stays inside the module
// (ai/go-conventions.md §persistence). Returns a non-nil empty slice when none of the
// given vehicles has a snapshot (avoids nil-slice footguns for the caller). sqlc
// generates a plain positional []int64 argument for the bigint[] binding — no Params
// struct for this query.
func (d *dbStore) latestSnapshotsByVehicles(ctx context.Context, teslaIDs []int64) ([]Snapshot, error) {
	rows, err := d.q.LatestSnapshotsByVehicles(ctx, teslaIDs)
	if err != nil {
		return nil, err
	}
	snaps := make([]Snapshot, 0, len(rows))
	for _, r := range rows {
		snaps = append(snaps, rowToSnapshot(r))
	}
	return snaps, nil
}

// snapshotsByVehicleSince implements the history read seam: it calls the
// SnapshotsByVehicleSince range-scan query generated by sqlc and maps each row
// to the domain Snapshot via rowToSnapshot (mapping.go). The since parameter is
// converted at the DB boundary so pgtype never leaks past service.go.
// Returns a non-nil empty slice when no rows exist (parity with
// latestSnapshotsByVehicles).
func (d *dbStore) snapshotsByVehicleSince(ctx context.Context, teslaID int64, since time.Time) ([]Snapshot, error) {
	rows, err := d.q.SnapshotsByVehicleSince(ctx, telemetrydb.SnapshotsByVehicleSinceParams{
		TeslaID: teslaID,
		Since:   timestamptzFrom(since),
	})
	if err != nil {
		return nil, err
	}
	snaps := make([]Snapshot, 0, len(rows))
	for _, r := range rows {
		snaps = append(snaps, rowToSnapshot(r))
	}
	return snaps, nil
}

// snapshotsByVehicleBetween implements the bounded-window history read seam: it calls
// the SnapshotsByVehicleBetween range-scan query generated by sqlc and maps each row
// to the domain Snapshot via rowToSnapshot (mapping.go) — the SAME single DB→domain
// mapper snapshotsByVehicleSince uses, so this method inherits EffectiveDate and every
// extracted typed field with no per-method duplication (design D4 of
// telemetry-add-effective-date).
//
// Bounds translation (design D1/D5): the public Reader.SnapshotsByVehicleBetween takes
// a clean `(start, end)` window (UTC-midnight-bounded, `end` inclusive). This method
// translates that into the `captured_at` TIMESTAMPTZ bounds the SQL needs:
//
//	startBound := start.AddDate(0, 0, 1)  // start + 1 calendar day
//	endBound   := end  .AddDate(0, 0, 2)  // end + 2 calendar days (half-open upper bound)
//
// This honors `EffectiveDate ∈ [start, end]` inclusive, because
// `EffectiveDate = CapturedAt.AddDate(0,0,-1)` (rowToSnapshot), so
// `EffectiveDate ∈ [start, end]` ⟺ `CapturedAt ∈ [start+1day, end+1day]` (calendar
// days, inclusive both ends), which as TIMESTAMPTZ predicates is the half-open range
// `[start+1day, end+2day)`. The `AddDate` is calendar-day arithmetic (DST-safe),
// mirroring rowToSnapshot's own EffectiveDate computation; since `start`/`end` are
// UTC-midnight instants there is no DST ambiguity in the inputs. The translation lives
// HERE (the dbStore impl), not in the reader/store seam and not in the public method
// signature — the reader passes the caller's raw `(start, end)` through unchanged
// (design D5), so the public port stays a clean window and the offset math is testable
// in one place.
//
// pgtype never appears in the return type — `time.Time` is bound to `pgtype.Timestamptz`
// here at the DB boundary via timestamptzFrom (the same helper snapshotsByVehicleSince
// uses), so pgtype never leaks past service.go (ai/go-conventions.md §persistence).
//
// LIMIT 400 is enforced in the SQL itself (query.sql), so no pagination logic is
// needed here. Returns a non-nil empty slice when no rows exist (parity with
// latestSnapshotsByVehicles / snapshotsByVehicleSince — no nil-slice footgun
// for the gateway caller).
func (d *dbStore) snapshotsByVehicleBetween(ctx context.Context, teslaID int64, start, end time.Time) ([]Snapshot, error) {
	startBound := start.AddDate(0, 0, 1) // UTC midnight beginning first eligible capture day
	endBound := end.AddDate(0, 0, 2)     // UTC midnight ending last eligible capture day (exclusive)
	rows, err := d.q.SnapshotsByVehicleBetween(ctx, telemetrydb.SnapshotsByVehicleBetweenParams{
		TeslaID:    teslaID,
		StartBound: timestamptzFrom(startBound),
		EndBound:   timestamptzFrom(endBound),
	})
	if err != nil {
		return nil, err
	}
	snaps := make([]Snapshot, 0, len(rows))
	for _, r := range rows {
		snaps = append(snaps, rowToSnapshot(r))
	}
	return snaps, nil
}

// snapshotsByVehicleUpdatedSince implements the updated-since read seam: it calls
// the SnapshotsByVehicleUpdatedSince query generated by sqlc and maps each row to
// the domain Snapshot via rowToSnapshot (mapping.go) — the SAME mapper every other
// read method on this store uses, so this method inherits UpdatedAt and every
// other extracted typed field with no per-method duplication
// (RM29-analytics-add-vehicle-metrics task 1.2). The since parameter is converted
// at the DB boundary so pgtype never leaks past service.go. Returns a non-nil
// empty slice when no rows exist (parity with snapshotsByVehicleSince /
// latestSnapshotsByVehicles).
func (d *dbStore) snapshotsByVehicleUpdatedSince(ctx context.Context, teslaID int64, since time.Time) ([]Snapshot, error) {
	rows, err := d.q.SnapshotsByVehicleUpdatedSince(ctx, telemetrydb.SnapshotsByVehicleUpdatedSinceParams{
		TeslaID: teslaID,
		Since:   timestamptzFrom(since),
	})
	if err != nil {
		return nil, err
	}
	snaps := make([]Snapshot, 0, len(rows))
	for _, r := range rows {
		snaps = append(snaps, rowToSnapshot(r))
	}
	return snaps, nil
}

// timestamptzFrom converts a plain time.Time into a valid pgtype.Timestamptz at the
// DB boundary, so the domain never carries a pgtype value.
func timestamptzFrom(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// dateFrom converts a plain time.Time (already normalized to a calendar date
// by clock.CalendarDay) into a valid pgtype.Date at the DB boundary, mirroring
// timestamptzFrom. Lives here so pgtype stays confined to service.go/mapping.go.
func dateFrom(t time.Time) pgtype.Date {
	return pgtype.Date{Time: t, Valid: true}
}

// boolPtrToPgBool maps a *bool to a nullable pgtype.Bool preserving nil↔SQL NULL: a
// nil pointer (vehicle did not report sentry) becomes an invalid (NULL) pgtype.Bool,
// while *false / *true become valid false / true. Collapsing nil into false here would
// lose the absent≠off distinction the schema and Snapshot deliberately keep (D1).
func boolPtrToPgBool(b *bool) pgtype.Bool {
	if b == nil {
		return pgtype.Bool{Valid: false}
	}
	return pgtype.Bool{Bool: *b, Valid: true}
}

// upsertSuperchargerHistory maps a domain SuperchargerHistory to
// telemetrydb.UpsertSuperchargerHistoryParams at the DB boundary. This is the ONLY
// place pgtype is touched for supercharger session writes — pgtype never appears in
// SuperchargerHistory or any method signature outside service.go/mapping.go
// (design DBS4/B4.2, ai/go-conventions.md §persistence).
//
// Deliberately does NOT read s.StartBatteryPct / s.EndBatteryPct / s.BatteryPctSource:
// telemetrydb.UpsertSuperchargerHistoryParams has no fields for them (query.sql omits
// all three from the query entirely — R3/D3, RM27-telemetry-add-supercharger-battery-pct).
// This keeps the nightly poller from ever silently overwriting a human-verified value.
// (The two frozen estimate fields formerly also named here as fields this mapping
// never reads were dropped from SuperchargerHistory entirely by
// RM41-telemetry-drop-estimate-columns — there is no longer a field to not read.)
func (d *dbStore) upsertSuperchargerHistory(ctx context.Context, s SuperchargerHistory) error {
	// nullable tesla_id: nil → invalid (NULL), non-nil → valid BIGINT.
	var teslaID pgtype.Int8
	if s.TeslaID != nil {
		teslaID = pgtype.Int8{Int64: *s.TeslaID, Valid: true}
	}

	// nullable unlatch_date_time: zero time (empty parse) → invalid (NULL).
	var unlatchDT pgtype.Timestamptz
	if s.UnlatchDateTime != nil {
		unlatchDT = pgtype.Timestamptz{Time: *s.UnlatchDateTime, Valid: true}
	}

	// nullable derived fields.
	var energyKwh pgtype.Float8
	if s.EnergyKWh != nil {
		energyKwh = pgtype.Float8{Float64: *s.EnergyKWh, Valid: true}
	}
	var totalCost pgtype.Float8
	if s.TotalCost != nil {
		totalCost = pgtype.Float8{Float64: *s.TotalCost, Valid: true}
	}
	var currency pgtype.Text
	if s.Currency != nil {
		currency = pgtype.Text{String: *s.Currency, Valid: true}
	}
	var isPaid pgtype.Bool
	if s.IsPaid != nil {
		isPaid = pgtype.Bool{Bool: *s.IsPaid, Valid: true}
	}

	return d.q.UpsertSuperchargerHistory(ctx, telemetrydb.UpsertSuperchargerHistoryParams{
		SessionID:           s.SessionID,
		AccountID:           s.AccountID,
		Vin:                 s.VIN,
		TeslaID:             teslaID,
		SiteLocationName:    s.SiteLocationName,
		CountryCode:         s.CountryCode,
		ChargeStartDateTime: timestamptzFrom(s.ChargeStartDateTime),
		ChargeStopDateTime:  timestamptzFrom(s.ChargeStopDateTime),
		UnlatchDateTime:     unlatchDT,
		BillingType:         s.BillingType,
		VehicleMakeType:     s.VehicleMakeType,
		EnergyKwh:           energyKwh,
		TotalCost:           totalCost,
		Currency:            currency,
		IsPaid:              isPaid,
		RawData:             s.RawData,
	})
}
