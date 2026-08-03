## Context

`internal/telemetry/service.go` runs the nightly `Collector.CollectAll` cycle. Its per-account
loop (`collectAccount`, service.go:143) resolves one access token, makes one `ListVehicles` call
to read every vehicle's live `state`, then for each `account.OwnedVehicle` in `owned` calls
`collectVehicle(ctx, creds, accountID, v.TeslaID, states[v.TeslaID])` (service.go:181) followed by
`s.record(ctx, accountID, v.TeslaID, reason, report)` (service.go:182). `collectVehicle`
(service.go:282) wraps `attemptVehicle` with a single bounded retry for `ReasonAPIError`.
`attemptVehicle` (service.go:306) does `[wake→]fetch→map→store`: it calls `s.tsla.VehicleData(ctx,
creds, teslaID)`, maps the result via `snapshotFrom` into a `Snapshot`, and stores it — returning
only a `Reason`, nothing else, to the caller.

Tier 1 (`RM6-account-add-vehicle-config-fields`, archived) added `ExteriorColor *string` and
`CarType *string` to both `account.Vehicle` and `account.OwnedVehicle`, and a new port method:

```go
// account.Service
SetVehicleConfigIfEmpty(ctx context.Context, accountID uuid.UUID, teslaID int64, exteriorColor, carType string) error
```

backed by `UPDATE vehicles SET exterior_color=@x, car_type=@y, updated_at=now() WHERE account_id=@a
AND tesla_id=@t AND (exterior_color IS NULL OR car_type IS NULL)`. Nothing calls this method today.
`data.VehicleConfig` does not exist yet on `tesla.VehicleDataTesla` — the Fleet API's
`vehicle_config` sub-object (which carries `exterior_color` and `car_type`, among ~47 keys) is
never extracted anywhere in the adapter.

This tier's job: extract exactly the two fields (RD3, tier 1's scope, unchanged here), and call the
port method from the one place in the codebase that already has both a live `VehicleData` fetch and
the `account.OwnedVehicle` needed to know whether the write-back is even necessary.

Performance profile: **read-heavy** (`ai/architecture.md` §7). This tier adds zero new reads — it
adds, at most, one extra `UPDATE` per vehicle, ever (the very first cycle after this change ships,
for every pre-existing registered vehicle; zero thereafter once both columns are non-NULL). No read
path is touched.

## Goals / Non-Goals

**Goals:**
- Extract `exterior_color`/`car_type` from the `VehicleData` response the nightly collector already
  fetches, with zero additional Tesla Fleet API calls (RD1).
- Call tier 1's `SetVehicleConfigIfEmpty` exactly when it can plausibly change anything, and skip it
  the rest of the time, at both the Go-guard layer (RD2) and by never writing an empty string
  (RD5).
- Keep `attemptVehicle` a pure fetch/map/store function with no `s.acct` access (RD6) — the account
  port call is a `collectAccount`-level concern, symmetric with how `record(...)` already is.
- Guarantee at most one write-back attempt per vehicle per cycle, even though `collectVehicle`'s
  bounded retry can call `attemptVehicle` twice (RD6).
- Surface write-back failures as a counted, non-fatal signal (RD4), mirroring the existing
  `ChargingFetchFailures` precedent exactly.

**Non-Goals:**
- Any additional `vehicle_config` field beyond the two tier 1 already persists — out of scope
  (RD3, decided at the roadmap level, not revisited here).
- Any change to `internal/account` — tier 1 already provides everything this tier consumes.
- Any gateway display of colour/model — not proposed by this roadmap at all.
- Any database migration, table, column, or index — this tier is pure application-code wiring on
  top of tier 1's already-landed schema.

## No Database Objects Touched

No database objects are modified, added, or removed by this change. `vehicle_snapshots` and
`poll_attempts` (owned by `internal/telemetry`) are unchanged — the two config values are written
to the `account` module's `vehicles` table through the port method tier 1 already added, not to any
table this module owns. The `database` design gate does NOT apply: there is no new/changed table,
column, index, constraint, view, or migration to justify against the project's read patterns, in
either `internal/telemetry` or `internal/account`.

## Decisions

### D0 — Tesla adapter DTO enrichment (precondition, RD8/T1)

`VehicleDataTesla` in `internal/tesla/types.go` gets a new nested DTO and field, placed after
`VehicleState` (the last existing field):

```go
// VehicleConfigTesla carries the subset of the vehicle_config sub-object this platform
// extracts today (RD3 — exactly these two fields; the live payload has ~47 keys). Both
// values are static: they never change for a given vehicle after manufacture.
type VehicleConfigTesla struct {
	// ExteriorColor is the paint colour code (e.g. "PearlWhite").
	ExteriorColor string `json:"exterior_color"`
	// CarType is the model code (e.g. "modely").
	CarType string `json:"car_type"`
}
```

```go
type VehicleDataTesla struct {
	ID           int64              `json:"id"`
	VIN          string             `json:"vin"`
	DisplayName  string             `json:"display_name"`
	State        string             `json:"state"`
	ChargeState  ChargeStateTesla   `json:"charge_state"`
	ClimateState ClimateStateTesla  `json:"climate_state"`
	DriveState   DriveStateTesla    `json:"drive_state"`
	VehicleState VehicleStateTesla  `json:"vehicle_state"`
	VehicleConfig VehicleConfigTesla `json:"vehicle_config"`
}
```

**No `Raw*` method needed and no `explore-tesla-api` surface change.** `VehicleDataRaw` (via
`(*Client).VehicleData`'s raw-bytes return) already returns the verbatim `vehicle_data` payload,
which includes `vehicle_config` in full; this is a typed-extraction addition only, exactly the
`telemetry-add-tire-pressure-columns` T1/D0 precedent for `TpmsPressure*`. The `raw.go`/
explore-tesla-api sync rule (`CLAUDE.md` "Tesla API Exploration") applies only to new
`VehicleService` **methods**, not to additive DTO field extraction — no new method is added here.

**Why plain `string`, not `*string`.** Unlike `SentryMode` (a field Tesla sometimes omits
entirely), `vehicle_config.exterior_color`/`car_type` are always present in a `VehicleData`
response for a real vehicle — Tesla may send an empty string for an edge case (e.g. a
just-registered vehicle before its build sheet is finalized) but does not omit the keys. Plain
`string` matches the account module's own DTO-free `*string` domain fields (nil there means
"not yet captured in our DB", not "absent from Tesla") and matches tier 1's own choice of `string`
(non-pointer) parameters on `SetVehicleConfigIfEmpty`. An empty string is a valid, representable
zero value this tier explicitly checks for (RD5) — no pointer indirection is needed to detect it.

### D1 — `vehicleConfig` carrier type + re-signatured `collectVehicle`/`attemptVehicle` (RD6, RD7)

A small unexported struct carries what `attemptVehicle` observed, decoupled from whether a
write-back should happen (that decision is `collectAccount`'s, per D2 below):

```go
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
```

Signatures change from `(ctx, creds, accountID uuid.UUID, teslaID int64, state string)` to `(ctx,
creds, v account.OwnedVehicle, state string)` — a net parameter *reduction* (RD7), since
`account.OwnedVehicle` already carries both `AccountID` and `TeslaID`. `attemptVehicle` additionally
returns `vehicleConfig` alongside its existing `Reason`:

```go
func (s *service) collectVehicle(ctx context.Context, creds tesla.Credentials, v account.OwnedVehicle, state string) (Reason, vehicleConfig)
func (s *service) attemptVehicle(ctx context.Context, creds tesla.Credentials, v account.OwnedVehicle, state string) (Reason, vehicleConfig)
```

Every early return in `attemptVehicle` before the `VehicleData` fetch succeeds (`wake` error,
asleep-timeout, `VehicleData` error) returns the zero `vehicleConfig{}` alongside its `Reason`. Only
the terminal `return ReasonOK, ...` line — reached after `insertSnapshot` succeeds — populates it:

```go
return ReasonOK, vehicleConfig{exteriorColor: data.VehicleConfig.ExteriorColor, carType: data.VehicleConfig.CarType}
```

`collectVehicle`'s bounded retry threads the tuple through unchanged (it already returns whatever
the last `attemptVehicle` call produced — no special-casing needed since the retry always
*replaces* the prior `reason`/`cfg` pair with the new attempt's, and only the final pair reaches
`collectAccount`). This is precisely how RD6's "at most one write-back attempt per vehicle per
cycle even when the bounded retry fires attemptVehicle twice" falls out for free: `collectAccount`
never sees the first (failed) attempt's `vehicleConfig` at all, only the retry's.

**Why a named struct instead of two bare `string` return values.** Two bare strings
(`(Reason, string, string)`) would leave call sites reading `reason, ec, ct := ...` with no
self-documenting field names at the call site, and adding a third piece of per-attempt data later
(unlikely, but this is exactly the kind of two-value tuple that tends to grow) would be a breaking
signature change instead of an additive field. A two-field unexported struct costs nothing extra to
resolve (this project's own AI-efficiency principle: don't over-abstract, but *do* name a
multi-value return that crosses a function boundary) and mirrors `Attempt`/`Snapshot`'s existing
convention of named-field domain carriers over bare tuples.

### D2 — The guard + port call + counter live in `collectAccount`, beside `record(...)` (RD6)

A new unexported helper, called from `collectAccount`'s per-vehicle loop (service.go:180-183)
immediately after `record(...)`:

```go
for _, v := range owned {
	reason, cfg := s.collectVehicle(ctx, creds, v, states[v.TeslaID])
	s.record(ctx, v.AccountID, v.TeslaID, reason, report)
	s.captureVehicleConfig(ctx, v, cfg, report)
}
```

```go
// captureVehicleConfig persists the two static vehicle_config values observed during this
// cycle's collectVehicle pass for v, through the account port, at most once per vehicle per
// cycle. It never touches s.tsla and is called only from collectAccount (RD6) — attemptVehicle
// stays a pure fetch/map/store pass with no account-port access.
//
// Two independent skip conditions, checked in order:
//  1. RD2 (Go-side guard): v's already-known registry record has BOTH ExteriorColor and
//     CarType non-nil — already captured in a prior cycle, nothing to do. (The account
//     module's own SQL WHERE (exterior_color IS NULL OR car_type IS NULL) is the second,
//     independent guard — tier 1 design D4 — so this Go-side check is belt-and-braces, not
//     the only thing preventing a clobber.)
//  2. RD5: either observed value in cfg is "" — either Tesla reported an empty string, or the
//     vehicle's capture attempt never reached VehicleData this cycle (cfg is the zero value).
//     Writing "" would satisfy the account port's own IS-NULL guard forever, permanently
//     freezing the vehicle in a "captured" state with no real value and no way to self-heal.
//
// On a genuine write-back attempt, a returned error increments report.ConfigCaptureFailures
// (RD4) — it does NOT alter the vehicle's already-recorded Reason (record() already ran, one
// line above) and does NOT write a poll_attempts row of its own; the failure is retried for
// free on the next cycle since the registry row is still missing at least one value.
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
```

**Why only the main per-vehicle loop's call site, not the two account-wide short-circuit loops
(service.go:150-153, :164-168, :174-177).** Those three loops record every vehicle as
`ReasonUnauthorized`/`ReasonAPIError` **without ever calling `collectVehicle`** — there is no
`vehicleConfig` to pass because `VehicleData` was never fetched for any vehicle in the account that
cycle. Calling `captureVehicleConfig(ctx, v, vehicleConfig{}, report)` there would be a guaranteed
no-op (RD5's empty-string guard always fires), so it is simply not called — same outcome, zero
wasted calls, no divergence in behavior. This is documented here so a future reader does not
mistake the omission for an oversight.

### D3 — Empty-string vs. nil-vs-not-yet-observed: one guard, one meaning (RD5)

`vehicleConfig{}`'s zero value uses the same sentinel (`""`) for two different real-world causes —
"Tesla returned empty" and "we never asked this cycle" — and `captureVehicleConfig` does not need
to distinguish them: both mean "there is nothing trustworthy to write back right now," and both
resolve identically next cycle (retry for free, no special retry logic needed here beyond the
cycle's own recurrence). This keeps the guard a single `if cfg.exteriorColor == "" || cfg.carType
== ""` check rather than a three-way switch on "empty from Tesla" vs. "never fetched" vs. "fetched
and non-empty" — the two failure causes are operationally indistinguishable from this function's
point of view and do not need to be.

### D4 — `ConfigCaptureFailures` on `CycleReport`, logged by the scheduler (RD4)

```go
// CycleReport (telemetry.go) gains one field, placed after ChargingFetchFailures:
// ConfigCaptureFailures is the number of vehicle_config write-back attempts that failed
// (the account port's SetVehicleConfigIfEmpty call returned an error). A failed write-back
// never changes the vehicle's Reason and never writes a poll_attempts row — it is retried
// for free on the next cycle since the registry row is still missing at least one column.
ConfigCaptureFailures int
```

`LogCycle` (scheduler.go:88-95) gains `config_capture_failures=%d` to the existing single log
line, immediately after `charging_failures=%d` — mirroring exactly how `ChargingFetchFailures` was
added to that same line, so a permanently-failing back-fill (e.g. a vehicle whose account has since
been deleted, if that ever becomes reachable) is visible in unattended-poller logs rather than
silently swallowed.

### D5 — Test doubles: `fakeAccount.SetVehicleConfigIfEmpty` becomes a recording double

`service_test.go`'s `fakeAccount.SetVehicleConfigIfEmpty` (added by tier 1 purely to satisfy the
widened `account.Service` interface — its doc comment literally says "the collector does not call
it yet ... this stub with a recording double") must actually record calls now:

```go
type configCapture struct {
	accountID     uuid.UUID
	teslaID       int64
	exteriorColor string
	carType       string
}

type fakeAccount struct {
	// ... existing fields unchanged ...
	configCaptures []configCapture
	// configCaptureErr, when set, is returned by every SetVehicleConfigIfEmpty call.
	configCaptureErr error
}

func (f *fakeAccount) SetVehicleConfigIfEmpty(_ context.Context, accountID uuid.UUID, teslaID int64, exteriorColor, carType string) error {
	f.configCaptures = append(f.configCaptures, configCapture{accountID, teslaID, exteriorColor, carType})
	if f.configCaptureErr != nil {
		return f.configCaptureErr
	}
	return nil
}
```

`onlineData` (the shared test DTO builder) needs an optional way to set `VehicleConfig` on the
returned `*tesla.VehicleDataTesla` — either a new parameter or per-test direct field assignment
after calling it (`d := onlineData(...); d.VehicleConfig = tesla.VehicleConfigTesla{...}`); the
latter is less invasive (no signature change to an existing, widely-used helper) and is the
recommended approach — left to the implementing task to choose, since both are equivalent in
effect.

## Risks / Trade-offs

- **A vehicle that is always asleep/unauthorized never gets its config captured.** Accepted: the
  same is already true of every other extracted field on `Snapshot` — a vehicle that never
  successfully completes `attemptVehicle` gets nothing written for that cycle, by design (per-vehicle
  isolation). Config capture rides the same fetch; there is no separate capture path to add.
- **`vehicleConfig`'s zero value collides "Tesla sent empty" with "we never asked."** Accepted per
  D3 — both are operationally identical from `captureVehicleConfig`'s point of view, and neither
  case is distinguishable (or needs to be) from the caller's side.
- **Test double now carries mutable slice state (`configCaptures`).** No new risk beyond the
  existing `fakeStore`/`fakeTesla` doubles' own mutable-state pattern (`snapshots`, `attempts`,
  `upsertedSessions`) — tests remain single-goroutine per `CollectAll` call in this module's
  existing suite, so no additional locking is needed beyond what `fakeAccount` already has (none —
  it has no mutex today either, and this change does not introduce concurrent access to it).

## Migration Plan (implementation order for the workers)

1. `internal/tesla/types.go` — add `VehicleConfigTesla` + `VehicleDataTesla.VehicleConfig` (D0).
2. `internal/telemetry/telemetry.go` — add `CycleReport.ConfigCaptureFailures` (D4).
3. `internal/telemetry/scheduler.go` — add `config_capture_failures=%d` to `LogCycle` (D4).
4. `internal/telemetry/service.go` — add `vehicleConfig` type; re-signature `collectVehicle`/
   `attemptVehicle` to take `v account.OwnedVehicle` and return `(Reason, vehicleConfig)` (D1); add
   `captureVehicleConfig`; wire it into `collectAccount`'s main per-vehicle loop (D2).
5. `internal/telemetry/service_test.go` — extend `fakeAccount` into a recording double (D5); add
   tests covering the skip-if-already-captured, skip-if-empty, successful-write, failed-write
   (counter increments, `Reason`/`poll_attempts` unaffected), and at-most-once-across-retry cases.
6. `go build ./...`, `go vet ./...`, `go test ./...` (offline suite; no live Tesla call; DB-backed
   tests in this module self-skip without `DATABASE_URL`/Docker, unaffected by this tier since no
   schema changed). `openspec validate RM6-telemetry-capture-vehicle-config --strict`.

**Rollback:** revert the five touched files; no migration to reverse (none was created).

## Open Questions

None — RD1, RD2, RD4, RD5, RD6, RD7, and RD8 (this tier's applicable binding decisions from the
roadmap) are settled at the roadmap level and implemented as designed above.
