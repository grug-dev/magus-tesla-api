package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log"
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
//
// upsertSuperchargerSession is the Source B write seam: it maps a domain
// SuperchargerSession into telemetrydb params at the DB boundary. pgtype never
// appears in the SuperchargerSession type or any public interface (design DBS4/B4).
type store interface {
	insertSnapshot(ctx context.Context, s Snapshot) error
	insertPollAttempt(ctx context.Context, a Attempt) error
	latestSnapshotsByAccount(ctx context.Context, accountID uuid.UUID) ([]Snapshot, error)
	snapshotsByVehicleSince(ctx context.Context, accountID uuid.UUID, teslaID int64, since time.Time) ([]Snapshot, error)
	upsertSuperchargerSession(ctx context.Context, s SuperchargerSession) error
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
//
// After the per-vehicle snapshot loop, collectAccount also fetches the account's
// Supercharger history (Source B, design DBS7). A ChargingHistory call failure
// only increments ChargingFetchFailures — it never aborts or affects the snapshot
// collection. No poll_attempts row is written for charging (poll_attempts is
// per-vehicle; charging is per-account — outcomes live in CycleReport).
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

	// Source B: fetch Supercharger session history for this account (design DBS7).
	// Zero params = full fetch, no vehicle wake. This call is per-account (not per
	// vehicle): one HTTP call returns all sessions for all vehicles in the account.
	s.collectChargingHistory(ctx, accountID, owned, creds, report)
}

// collectChargingHistory fetches the account's Supercharger session history and
// upserts each session. It is called after the per-vehicle snapshot loop so a
// ChargingHistory failure cannot abort or affect snapshot collection. On failure
// it only increments ChargingFetchFailures (design DBS7). Per-session isolation:
// a single upsert failure is logged but does not abort the remaining sessions.
// No poll_attempts rows are written — outcomes live in CycleReport (design DBS7).
func (s *service) collectChargingHistory(ctx context.Context, accountID uuid.UUID, owned []account.OwnedVehicle, creds tesla.Credentials, report *CycleReport) {
	history, err := s.tsla.ChargingHistory(ctx, creds, tesla.ChargingHistoryParams{})
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
		domainSession := SuperchargerSession{
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

		if err := s.store.upsertSuperchargerSession(ctx, domainSession); err != nil {
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
			return s.logAPIError(teslaID, "wake", err, reasonFor(err))
		}
		if !online {
			// The bounded wake window elapsed before the vehicle reported online.
			return ReasonAsleepTimeout
		}
	}

	data, raw, err := s.tsla.VehicleData(ctx, creds, teslaID)
	if err != nil {
		return s.logAPIError(teslaID, "VehicleData", err, reasonFor(err))
	}

	snap := snapshotFrom(accountID, teslaID, s.now(), data, raw)
	if err := s.store.insertSnapshot(ctx, snap); err != nil {
		// A store failure is transient from the cycle's point of view (retry once).
		return s.logAPIError(teslaID, "insertSnapshot", err, ReasonAPIError)
	}
	return ReasonOK
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
		RawData:        raw,
		// Source A enrichment — actual DTO values, pointer-wrapped (D12/DSA3).
		// ptr(v) returns &v; a 0 is a truthful reading and is stored non-NULL.
		ChargeEnergyAdded:    ptr(data.ChargeState.ChargeEnergyAdded),
		ChargerPower:         ptr(data.ChargeState.ChargerPower),
		ChargerVoltage:       ptr(data.ChargeState.ChargerVoltage),
		ChargerActualCurrent: ptr(data.ChargeState.ChargerActualCurrent),
		UsableBatteryLevel:   ptr(data.ChargeState.UsableBatteryLevel),
		// MaxRangeChargeCounter: same D12/DSA3 pointer-wrap convention. A reported 0
		// (new vehicle, never charged to max-range) is stored non-NULL as *0.
		MaxRangeChargeCounter: ptr(data.ChargeState.MaxRangeChargeCounter),
		// TPMS pressure enrichment — actual DTO values, pointer-wrapped (D12/DSA3).
		// ptr(v) returns &v; a 0.0 bar is a truthful reading and is stored non-NULL.
		// NULL is reserved for pre-migration rows (values remain in raw_data).
		TpmsPressureFL: ptr(data.VehicleState.TpmsPressureFL),
		TpmsPressureFR: ptr(data.VehicleState.TpmsPressureFR),
		TpmsPressureRL: ptr(data.VehicleState.TpmsPressureRL),
		TpmsPressureRR: ptr(data.VehicleState.TpmsPressureRR),
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
		// Source A (RM2-telemetry-add-charging-stats): nullable charge-enrichment columns.
		// nil → invalid pgtype (SQL NULL); non-nil → valid with the concrete value.
		// Same Valid-field pattern as boolPtrToPgBool. pgtype never leaks past this boundary.
		// latitude/longitude/fast_charger_type dropped in 20260801000001; not sent here.
		ChargeEnergyAdded:    float64PtrToPgFloat8(s.ChargeEnergyAdded),
		ChargerPower:         intPtrToPgInt4(s.ChargerPower),
		ChargerVoltage:       intPtrToPgInt4(s.ChargerVoltage),
		ChargerActualCurrent: intPtrToPgInt4(s.ChargerActualCurrent),
		UsableBatteryLevel:   intPtrToPgInt4(s.UsableBatteryLevel),
		// MaxRangeChargeCounter: same nil→NULL / non-nil→valid pattern (D12/DSA3).
		MaxRangeChargeCounter: intPtrToPgInt4(s.MaxRangeChargeCounter),
		// TPMS pressure fields: nullable REAL (pgtype.Float4). nil → invalid (NULL);
		// non-nil → valid Float32. Uses float64PtrToPgFloat4 (not Float8) because
		// the schema columns are REAL (float4). Precision is adequate for bar readings.
		TpmsPressureFl: float64PtrToPgFloat4(s.TpmsPressureFL),
		TpmsPressureFr: float64PtrToPgFloat4(s.TpmsPressureFR),
		TpmsPressureRl: float64PtrToPgFloat4(s.TpmsPressureRL),
		TpmsPressureRr: float64PtrToPgFloat4(s.TpmsPressureRR),
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

// snapshotsByVehicleSince implements the history read seam: it calls the
// SnapshotsByVehicleSince range-scan query generated by sqlc and maps each row
// to the domain Snapshot via rowToSnapshot (mapping.go). The since parameter is
// converted at the DB boundary so pgtype never leaks past service.go.
// Returns a non-nil empty slice when no rows exist (design D5 parity with
// latestSnapshotsByAccount).
func (d *dbStore) snapshotsByVehicleSince(ctx context.Context, accountID uuid.UUID, teslaID int64, since time.Time) ([]Snapshot, error) {
	rows, err := d.q.SnapshotsByVehicleSince(ctx, telemetrydb.SnapshotsByVehicleSinceParams{
		AccountID: accountID,
		TeslaID:   teslaID,
		Since:     timestamptzFrom(since),
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

// upsertSuperchargerSession maps a domain SuperchargerSession to
// telemetrydb.UpsertSuperchargerSessionParams at the DB boundary. This is the ONLY
// place pgtype is touched for supercharger session writes — pgtype never appears in
// SuperchargerSession or any method signature outside service.go/mapping.go
// (design DBS4/B4.2, ai/go-conventions.md §persistence).
func (d *dbStore) upsertSuperchargerSession(ctx context.Context, s SuperchargerSession) error {
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

	return d.q.UpsertSuperchargerSession(ctx, telemetrydb.UpsertSuperchargerSessionParams{
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
