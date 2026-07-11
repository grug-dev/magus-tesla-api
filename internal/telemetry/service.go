package telemetry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/account"
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
// exists so CollectAll can be unit-tested offline with a fake store (no DATABASE_URL,
// no Postgres) while the production path uses the telemetrydb-backed implementation
// built in NewService. Keeping it unexported and domain-typed (Snapshot / Attempt in,
// error out) is what keeps pgtype from leaking past the module boundary: the mapping
// to pgtype happens ONLY inside the telemetrydb-backed implementation below.
//
// The read method latestSnapshotsByAccount is added here so the Reader implementation
// can also be unit-tested offline via the same fake-store pattern (design D3 of
// telemetry-add-snapshot-read-port). The mapping from pgtype→domain happens in
// mapping.go (rowToSnapshot), confining pgtype to the concrete dbStore.
type store interface {
	insertSnapshot(ctx context.Context, s Snapshot) error
	insertPollAttempt(ctx context.Context, a Attempt) error
	latestSnapshotsByAccount(ctx context.Context, accountID uuid.UUID) ([]Snapshot, error)
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
		store:        &dbStore{q: telemetrydb.New(pool)},
		cfg:          cfg,
		retryBackoff: defaultRetryBackoff,
	}
}

// now returns the current time, honoring the injected test clock when present so
// captured_at / attempted_at and any timeout math are deterministic in tests.
func (s *service) now() time.Time {
	if s.cfg.Clock != nil {
		return s.cfg.Clock()
	}
	return time.Now()
}

// CollectAll runs one collection cycle over every registered vehicle across all
// accounts (the Collector port). It enumerates vehicles via the account port, groups
// them by account for per-account token batching (D3), and collects each vehicle with
// full per-vehicle isolation: a single vehicle's failure is recorded as a poll_attempt
// and never aborts the account or the cycle. It returns an error ONLY when the
// whole-cycle enumeration itself fails (D9) — never for an individual vehicle.
func (s *service) CollectAll(ctx context.Context) (CycleReport, error) {
	report := CycleReport{FailuresByReason: map[Reason]int{}}

	vehicles, err := s.acct.AllRegisteredVehicles(ctx)
	if err != nil {
		// Whole-cycle failure: we could not even list what to collect. This is the
		// ONLY error CollectAll returns (D9); everything else is contained per vehicle.
		return report, fmt.Errorf("telemetry: enumerating registered vehicles: %w", err)
	}

	// Group by owning account so we resolve each account's access token exactly once
	// and make a single ListVehicles call per account (per-account batching, D3).
	byAccount := groupByAccount(vehicles)

	for accountID, owned := range byAccount {
		s.collectAccount(ctx, accountID, owned, &report)
	}

	return report, nil
}

// groupByAccount buckets the flat cross-account vehicle list by owning account id so
// the collection loop can batch token resolution and ListVehicles per account (D3).
func groupByAccount(vehicles []account.OwnedVehicle) map[uuid.UUID][]account.OwnedVehicle {
	byAccount := map[uuid.UUID][]account.OwnedVehicle{}
	for _, v := range vehicles {
		byAccount[v.AccountID] = append(byAccount[v.AccountID], v)
	}
	return byAccount
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
func (s *service) collectAccount(ctx context.Context, accountID uuid.UUID, owned []account.OwnedVehicle, report *CycleReport) {
	token, err := s.acct.AccessTokenFor(ctx, accountID)
	if err != nil {
		// No connection (or a refresh failure) applies to the WHOLE account: record
		// every one of its vehicles as unauthorized and skip all Tesla calls. A
		// refresh error other than ErrNoTeslaConnection is still an auth problem for
		// this account, so it maps to unauthorized too (no per-vehicle retry helps).
		for _, v := range owned {
			s.record(ctx, accountID, v.TeslaID, ReasonUnauthorized, report)
		}
		return
	}

	creds := tesla.Credentials{AccessToken: token}

	// One ListVehicles per account (D3): read every vehicle's current State once.
	states, err := s.listStates(ctx, creds)
	if err != nil {
		if errors.Is(err, tesla.ErrUnauthorized) {
			// The account's token is rejected fleet-wide: record every vehicle as
			// unauthorized and make NO further Tesla calls for this account.
			for _, v := range owned {
				s.record(ctx, accountID, v.TeslaID, ReasonUnauthorized, report)
			}
			return
		}
		// A transient ListVehicles failure (non-401) means we could not read any
		// vehicle's state. Rather than blindly waking every vehicle (which would
		// re-introduce the very WakeUp waste this change removes), treat the whole
		// account's fetch as an api-error for this cycle and record each vehicle
		// accordingly — per-vehicle isolation is preserved (the cycle continues).
		for _, v := range owned {
			s.record(ctx, accountID, v.TeslaID, ReasonAPIError, report)
		}
		return
	}

	for _, v := range owned {
		reason := s.collectVehicle(ctx, creds, accountID, v.TeslaID, states[v.TeslaID])
		s.record(ctx, accountID, v.TeslaID, reason, report)
	}
}

// listStates makes the single per-account ListVehicles call (D3) and returns a
// teslaID → State map. A vehicle registered in the account registry but absent from the
// live result (removed on Tesla's side) simply has no entry — collectVehicle then treats
// it as not-online and runs the bounded wake flow, whose own ListVehicles poll surfaces
// it as errVehicleNotListed → api-error (D3's "absent from ListVehicles" handling).
func (s *service) listStates(ctx context.Context, creds tesla.Credentials) (map[int64]string, error) {
	vehicles, err := s.tsla.ListVehicles(ctx, creds)
	if err != nil {
		return nil, err
	}
	states := make(map[int64]string, len(vehicles))
	for _, v := range vehicles {
		states[v.ID] = v.State
	}
	return states, nil
}

// collectVehicle collects one vehicle with a single bounded retry for transient
// (api-error) failures (D5). Unauthorized and asleep-timeout are terminal — never
// retried. state is the vehicle's State from the per-account ListVehicles (D3), used to
// decide whether a WakeUp is even needed. It returns the Reason to record; it does NOT
// write the poll_attempt (the caller does, exactly once per vehicle per cycle).
func (s *service) collectVehicle(ctx context.Context, creds tesla.Credentials, accountID uuid.UUID, teslaID int64, state string) Reason {
	reason := s.attemptVehicle(ctx, creds, accountID, teslaID, state)
	if reason == ReasonAPIError {
		// Exactly one retry after a short backoff, and only for transient errors.
		select {
		case <-ctx.Done():
			return reason
		case <-time.After(s.retryBackoff):
		}
		reason = s.attemptVehicle(ctx, creds, accountID, teslaID, state)
	}
	return reason
}

// attemptVehicle performs one [wake→]fetch→map→store pass for a single vehicle and
// returns the Reason for the outcome. It is fully error-contained: any failure maps
// to a Reason and is returned, never propagated. The wake step is SKIPPED when the
// per-account ListVehicles already reported the vehicle online (D3/D4): an online
// vehicle goes straight to VehicleData with no paid WakeUp; only an asleep/offline/
// absent vehicle runs the bounded wake flow. Reason mapping (D5):
//   - wake deadline elapsed (waitUntilOnline returned false,nil) → asleep-timeout
//   - tesla.ErrUnauthorized anywhere → unauthorized (terminal)
//   - any other tesla/decode/store error → api-error (the caller retries once)
//   - snapshot stored → ok
func (s *service) attemptVehicle(ctx context.Context, creds tesla.Credentials, accountID uuid.UUID, teslaID int64, state string) Reason {
	if state != "online" {
		// Asleep/offline/absent-from-the-list → run the bounded wake flow (D4). An
		// already-online vehicle bypasses this entirely, saving a paid WakeUp.
		online, err := waitUntilOnline(ctx, s.tsla, creds, teslaID, s.cfg.WakeTimeout)
		if err != nil {
			return reasonFor(err)
		}
		if !online {
			// The bounded wake window elapsed before the vehicle reported online.
			return ReasonAsleepTimeout
		}
	}

	data, raw, err := s.tsla.VehicleData(ctx, creds, teslaID)
	if err != nil {
		return reasonFor(err)
	}

	snap := snapshotFrom(accountID, teslaID, s.now(), data, raw)
	if err := s.store.insertSnapshot(ctx, snap); err != nil {
		// A store failure is transient from the cycle's point of view (retry once).
		return ReasonAPIError
	}
	return ReasonOK
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
func (s *service) record(ctx context.Context, accountID uuid.UUID, teslaID int64, reason Reason, report *CycleReport) {
	outcome := OutcomeFailure
	if reason == ReasonOK {
		outcome = OutcomeSuccess
	}

	_ = s.store.insertPollAttempt(ctx, Attempt{
		AccountID:   accountID,
		TeslaID:     teslaID,
		AttemptedAt: s.now(),
		Outcome:     outcome,
		Reason:      reason,
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
// flows into domain logic un-mapped). Distances stay API-native (miles); km is derived
// on read via the Snapshot Km() companions, never stored. SentryMode stays *bool so an
// absent field remains distinct from a reported-off sentry (nil ≠ *false, D1).
func snapshotFrom(accountID uuid.UUID, teslaID int64, capturedAt time.Time, data *tesla.VehicleDataTesla, raw []byte) Snapshot {
	return Snapshot{
		AccountID:      accountID,
		TeslaID:        teslaID,
		CapturedAt:     capturedAt,
		BatteryLevel:   data.ChargeState.BatteryLevel,
		BatteryRange:   data.ChargeState.BatteryRange,
		ChargingState:  data.ChargeState.ChargingState,
		ChargeLimitSoc: data.ChargeState.ChargeLimitSoc,
		Odometer:       data.VehicleState.Odometer,
		InsideTemp:     data.ClimateState.InsideTemp,
		OutsideTemp:    data.ClimateState.OutsideTemp,
		Locked:         data.VehicleState.Locked,
		SentryMode:     data.VehicleState.SentryMode,
		CarVersion:     data.VehicleState.CarVersion,
		Latitude:       data.DriveState.Latitude,
		Longitude:      data.DriveState.Longitude,
		RawData:        raw,
	}
}

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
		AccountID:      s.AccountID,
		TeslaID:        s.TeslaID,
		CapturedAt:     timestamptzFrom(s.CapturedAt),
		RawData:        s.RawData,
		BatteryLevel:   int32(s.BatteryLevel),
		BatteryRange:   s.BatteryRange,
		ChargingState:  s.ChargingState,
		ChargeLimitSoc: int32(s.ChargeLimitSoc),
		Odometer:       s.Odometer,
		InsideTemp:     s.InsideTemp,
		OutsideTemp:    s.OutsideTemp,
		Locked:         s.Locked,
		SentryMode:     boolPtrToPgBool(s.SentryMode),
		CarVersion:     s.CarVersion,
		Latitude:       s.Latitude,
		Longitude:      s.Longitude,
	})
}

func (d *dbStore) insertPollAttempt(ctx context.Context, a Attempt) error {
	return d.q.InsertPollAttempt(ctx, telemetrydb.InsertPollAttemptParams{
		AccountID:   a.AccountID,
		TeslaID:     a.TeslaID,
		AttemptedAt: timestamptzFrom(a.AttemptedAt),
		Outcome:     string(a.Outcome),
		Reason:      string(a.Reason),
	})
}

// latestSnapshotsByAccount implements the read seam: it calls the DISTINCT ON
// query generated by sqlc and maps each row to the domain Snapshot via rowToSnapshot
// (mapping.go). pgtype never appears in the return type — it stays inside the module
// (ai/go-conventions.md §persistence). Returns a non-nil empty slice when the account
// has no snapshots (design D5: avoids nil-slice footguns for the gateway caller).
func (d *dbStore) latestSnapshotsByAccount(ctx context.Context, accountID uuid.UUID) ([]Snapshot, error) {
	rows, err := d.q.LatestSnapshotsByAccount(ctx, accountID)
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
