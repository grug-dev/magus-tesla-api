> **Reverses an existing invariant — read design.md before starting.** This change makes
> `vehicle_snapshots` upsertable (SUPERSEDES the append-only invariant of
> `RM1-telemetry-add-nightly-snapshots` design D1). It adds a Go-computed `captured_date`
> column + `UNIQUE (account_id, tesla_id, captured_date)` constraint, turns
> `InsertVehicleSnapshot` into an upsert, and touches a `Config.Location` seam that a
> secondary file OUTSIDE `internal/telemetry` (`cmd/poller/main.go`) also needs.
>
> **Dependencies / parallelism:**
>
> - T1 (migration) has no dependencies.
> - T2 (`Config.Location` + `Snapshot.CapturedDate` struct fields) has no dependencies;
>   may run in parallel with T1.
> - T3 (`service.go`: `location()`, `dateOnly()`, `snapshotFrom`, `dbStore.insertSnapshot`,
>   `dateFrom`) depends on T2. Disjoint from T1's file.
> - T4 (sqlc query edits + regenerate) depends on T1. Disjoint from T2/T3's files until
>   T4.2, which depends on T3 too (params struct shape).
> - T5 (`mapping.go`: `rowToSnapshot`) depends on T3, T4.
> - T6 (`cmd/poller/main.go`) depends on T2. **OUTSIDE `internal/telemetry`'s sandbox —
>   see note on T6 before assigning.**
> - T7 (`AGENTS.md` "Data ownership" rewrite) depends on T1 (needs the final schema to
>   describe accurately); otherwise independent of T2–T6.
> - T8 (new offline unit tests: `dateOnly`, `location()` fallback) depends on T3.
> - T9 (update the four existing DB integration test files) depends on T1, T4, T5.
> - Verification (V) depends on all tasks.
>
> **Leader-integrated step:** run `make sqlc` after T4.1 (query.sql edits) to regenerate
> `telemetrydb`. The existing `sql:` entry in `sqlc.yaml` already covers the telemetry
> module; no structural `sqlc.yaml` change is needed.

---

## T1. Goose migration (`internal/telemetry/db/migrations/`) — no dependencies

- [x] T1.1 Create
      `internal/telemetry/db/migrations/20260805000001_dedupe_vehicle_snapshots_daily.sql`
      with the exact DDL from design.md's "Schema" section (Up: add `captured_date`
      nullable → backfill from `captured_at AT TIME ZONE 'America/Bogota'` → delete the
      older row of each `(account_id, tesla_id, captured_date)` duplicate group, tie-break
      by `id` → add the `vehicle_snapshots_account_tesla_date_unique` UNIQUE constraint →
      set `captured_date` NOT NULL → add `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`.
      Down: drop the UNIQUE constraint, drop `updated_at`, drop `captured_date`, in that
      order). Include the full header comments from design.md (supersession note, D2
      rejected-alternatives summary, D3a timezone-literal justification, D5 updated_at
      behavior). Do not deviate from the exact column names, constraint name, or migration
      order specified in design.md.
      Acceptance: `goose status` (or `make migrate-up`) shows the migration applied
      cleanly against the live DB (44 existing rows, 2 known duplicate pairs both dated
      2026-08-04); after applying, `SELECT count(*) FROM vehicle_snapshots` is 42 (44 − 2
      deleted duplicates); `SELECT count(*) FROM vehicle_snapshots WHERE captured_date IS
      NULL` is 0; `goose down` (one step) removes `updated_at` and `captured_date` and the
      constraint without error, but does NOT restore the 2 deleted rows (expected, D3).

## T2. `Config.Location` + `Snapshot.CapturedDate` (`internal/telemetry/telemetry.go`) — no dependencies

- [x] T2.1 Add `Location *time.Location` to `Config`, immediately after `Clock`, with the
      doc comment from design.md D2a (nil → `time.Local`, same `*time.Location` as
      `NewScheduler`'s `loc` param, set once in `cmd/poller/main.go`).
      Acceptance: `go build ./...` green; existing `Config{...}` literals across the
      codebase (tests, `cmd/poller`) remain compile-compatible (additive field).

- [x] T2.2 Add `CapturedDate time.Time` to `Snapshot`, immediately after `CapturedAt`, with
      the doc comment from design.md (calendar-date semantics, UTC-midnight normalized,
      backs the UNIQUE constraint, `CapturedAt` remains the authoritative "when").
      Acceptance: `go build ./...` green; existing named-field `Snapshot{...}` literals
      remain compile-compatible; the zero value is `time.Time{}` (documented as a footgun
      for tests in T9 — do not rely on the zero value being meaningful).

## T3. `service.go`: location seam + write-path wiring — depends on T2

- [x] T3.1 Add the `location()` helper method on `*service`, mirroring the existing `now()`
      helper exactly (falls back to `time.Local` when `s.cfg.Location` is nil), per
      design.md D2a.
      Acceptance: `go build ./...` green.

- [x] T3.2 Add the `dateOnly(t time.Time, loc *time.Location) time.Time` pure function to
      `service.go` (near `snapshotFrom`/`ptr`), per design.md D2a: returns the calendar
      date of `t` in `loc`, normalized to UTC midnight via
      `time.Date(y, m, d, 0, 0, 0, 0, time.UTC)`.
      Acceptance: `go build ./...` green; this function has no DB/network dependency and
      is directly unit-testable (see T8).

- [x] T3.3 Change `snapshotFrom`'s signature to accept a `loc *time.Location` parameter
      (immediately after `capturedAt`) and set `CapturedDate: dateOnly(capturedAt, loc)`
      on the returned `Snapshot`, immediately after the `CapturedAt` field. Update the
      call site in `attemptVehicle` to `snapshotFrom(v.AccountID, v.TeslaID, s.now(),
      s.location(), data, raw)`.
      Acceptance: `go build ./...` and `go vet ./...` green; every other `snapshotFrom`
      field assignment is unchanged.

- [x] T3.4 Add the `dateFrom(t time.Time) pgtype.Date` boundary helper to `service.go`,
      mirroring `timestamptzFrom` exactly (per design.md's Write Path section).
      Acceptance: `go build ./...` green.

- [x] T3.5 Extend `dbStore.insertSnapshot` to pass `CapturedDate:
      dateFrom(s.CapturedDate)` to `InsertVehicleSnapshotParams`. This step depends on T4.1
      (the sqlc query must include `captured_date` in the INSERT list before `make sqlc`
      regenerates the params struct) — implement after T4.1 and `make sqlc` have run.
      Do NOT pass `UpdatedAt`: the DB computes it entirely (DEFAULT on insert, `now()` on
      conflict-update, per design D5) — no application param exists for it.
      Acceptance: `InsertVehicleSnapshotParams` has a `CapturedDate pgtype.Date` field;
      `dbStore.insertSnapshot` assigns it; `go build ./...` green.

## T4. sqlc query edits + regenerate (`internal/telemetry/db/query.sql`) — depends on T1

- [x] T4.1 Rewrite `InsertVehicleSnapshot` in `query.sql` into the upsert from design.md's
      "Write Path" section: add `captured_date` to the INSERT column list and
      `@captured_date` to VALUES (at the end, matching the migration's physical
      column-append order), then add
      `ON CONFLICT (account_id, tesla_id, captured_date) DO UPDATE SET` refreshing every
      typed column, `raw_data`, `captured_at`, and `updated_at = now()` (do NOT refresh
      `account_id`, `tesla_id`, or `captured_date` — they are the conflict target and are
      identical between old and new by construction). Update the query's header comment to
      state the upsert/dedupe semantics and cite design D1/D2 (keep the existing
      nullable-column comment block intact — it still applies unchanged).
      Append `captured_date, updated_at` to the end of the explicit SELECT column lists in
      `ListSnapshotsByVehicle`, `SnapshotsByVehicleSince`, and `LatestSnapshotsByAccount`
      (matching the migration's physical append order — this preserves the shared
      `telemetrydb.VehicleSnapshot` return-type struct across all three query functions;
      see design.md's Read Path section for why this matters).
      After editing, the leader runs `make sqlc` to regenerate `telemetrydb`.
      Acceptance: `query.sql` compiles (goose/sqlc can parse it); after `make sqlc`,
      `telemetrydb.InsertVehicleSnapshotParams` has a `CapturedDate pgtype.Date` field, and
      `telemetrydb.VehicleSnapshot` (still the single shared struct across all three read
      queries) has `CapturedDate pgtype.Date` and `UpdatedAt pgtype.Timestamptz` fields.

## T5. `mapping.go`: `rowToSnapshot` — depends on T3, T4

- [x] T5.1 Extend `rowToSnapshot` in `internal/telemetry/mapping.go` to map
      `CapturedDate: r.CapturedDate.Time` onto the returned `Snapshot`, immediately after
      `CapturedAt`. Do NOT map `r.UpdatedAt` onto `Snapshot` — it stays unexposed, mirroring
      the existing precedent that `r.ID` is likewise selected but never surfaced on
      `Snapshot` (design.md's Read Path section).
      Acceptance: `go build ./...` and `go vet ./...` green after `make sqlc`; a snapshot
      round-tripped through `insertSnapshot` → `ListSnapshotsByVehicle`/`rowToSnapshot`
      carries the same `CapturedDate` it was written with.

## T6. `cmd/poller/main.go` wiring — depends on T2 — OUTSIDE `internal/telemetry`'s sandbox

**Leader: this task touches a file outside `internal/telemetry`. Either grant
`cmd/poller/main.go` explicitly to the worker implementing this change, or dispatch this
task separately. See design.md's "Scope Boundary" section.**

- [ ] T6.1 Move the existing `loc, err := time.LoadLocation(cfg.PollerTimezone)` call (and
      its `log.Fatalf` on error) so it runs **unconditionally**, before `tcfg :=
      telemetry.Config{...}` is constructed — today it runs only inside the `if !*once`
      branch, after `tcfg`/`collector` already exist. Add `Location: loc` to the `tcfg`
      literal. Remove the now-duplicate `time.LoadLocation` call from inside the `if
      !*once` branch and reuse the single `loc` variable for both `telemetry.Config.Location`
      and the existing `telemetry.NewScheduler(..., loc, tcfg)` call.
      Update the file's top doc comment if it references when `POLLER_TIMEZONE` is
      validated (today it says the nightly path "loads or validates POLLER_TIMEZONE" —
      make clear both paths now do).
      Acceptance: `go build ./...` green; `go run ./cmd/poller --once` with an invalid
      `POLLER_TIMEZONE` now fails fast with the same `log.Fatalf` message the nightly path
      already produced (documented Breaking-change behavior, design D2a); with a valid
      `POLLER_TIMEZONE` (or unset/default `"Local"`), `--once` behaves identically to
      before except that captures are now correctly dated in that zone.

## T7. `internal/telemetry/AGENTS.md` — "Data ownership" rewrite — depends on T1

- [x] T7.1 Rewrite the "Data ownership" section's `vehicle_snapshots` bullet to remove the
      "one immutable row per successful capture … Never overwritten or deleted
      (append-only)" framing and replace it with: at most one row per (account_id,
      tesla_id, captured_date); a same-day re-capture REPLACES the row (latest wins,
      design D1); `captured_date DATE` is Go-computed from the poller's configured
      timezone (design D2); `updated_at TIMESTAMPTZ` is the audit trail for a replace
      (design D5). State explicitly that `poll_attempts` is UNAFFECTED and remains
      append-only/immutable (design D4) — do not let the rewrite blur that line.
      Add a one-line pointer to `telemetry-dedupe-daily-snapshots` as the change that
      superseded the prior append-only invariant (mirroring how the file already cites
      other superseding changes by name elsewhere, e.g. "see migration 20260801000001").
      Acceptance: the section reads correctly on its own (a future worker/agent reading
      only `AGENTS.md` gets the accurate current invariant, not the superseded one).

## T8. New offline unit tests — depends on T3

- [x] T8.1 Add unit tests for `dateOnly` in a new small file,
      `internal/telemetry/dedupe_test.go` (no DB, no network — pure function tests). Cover:
      (a) a capture comfortably inside a calendar day in a non-UTC zone (e.g.
          `America/Bogota`, UTC-5) returns that same local calendar date;
      (b) a capture whose UTC instant is on one calendar day but whose local instant (in a
          negative-offset zone) is the PREVIOUS calendar day — asserting `dateOnly` follows
          the local day, not the UTC day (this is the scenario D2's rejected UTC-expression
          alternative would get wrong);
      (c) `time.UTC` as `loc` is a no-op (returns the same UTC calendar date as the input).
      Acceptance: `go test ./internal/telemetry/...` passes; tests are fast (no DB, no
      network, no live Tesla API call).

- [x] T8.2 Add a unit test for `(*service).location()`'s fallback behavior: `Config{}`
      (zero value, `Location` nil) returns `time.Local`; `Config{Location: someLoc}`
      returns `someLoc` unchanged. Place alongside the existing `now()`-fallback test if
      one exists in `service_test.go`, or in `dedupe_test.go`.
      Acceptance: `go test ./internal/telemetry/...` passes; no DB, no network.

## T9. Update existing DB integration tests — depends on T1, T4, T5

**Read design.md's "Test Blast Radius" section before starting — this is not limited to
the one test named below.**

- [x] T9.1 In `internal/telemetry/db_integration_test.go`: replace
      `TestStore_SnapshotAppendOnly` with two tests reflecting the new upsert semantics
      (naming is illustrative, keep it descriptive):
      - `TestStore_SnapshotUpsert_SameDayReplaces` — insert a snapshot with a given
        `CapturedAt`/`CapturedDate`, then insert a second snapshot for the SAME vehicle
        with the SAME `CapturedDate` but a LATER `CapturedAt` and different field values
        (e.g. `BatteryLevel`, `CarVersion`, `RawData`). Assert: exactly ONE row exists for
        that vehicle afterward; that row's fields match the SECOND capture's values, not
        the first's; that row's `captured_at` equals the second capture's `CapturedAt`.
      - `TestStore_SnapshotInsert_DifferentDayCreatesNewRow` — insert a snapshot for day 1,
        then a second snapshot for the SAME vehicle with a DIFFERENT `CapturedDate` (day
        2). Assert: exactly TWO rows exist; each row's fields match its own capture; the
        day-1 row is unchanged by the day-2 insert.
      Also add an explicit, consistent `CapturedDate` to the two existing sentry
      round-trip tests in this file (`TestStore_SnapshotRoundTrip_SentryNilIsNull`,
      `TestStore_SentryTrueAndFalseRoundTripFaithfully`) — each inserts only one snapshot
      per vehicle so there is no collision risk, but every `Snapshot{}` literal in this
      file should set `CapturedDate` explicitly going forward (hygiene: relying on the
      zero value is a footgun the moment a test is later extended to insert a second row).
      Acceptance: `go test ./internal/telemetry/...` (DB-gated) passes; no test relies on
      the old append-only assumption.

- [x] T9.2 In `internal/telemetry/db_read_integration_test.go`: audit every `Snapshot{}`
      literal passed to `st.insertSnapshot`. Several tests intentionally build MULTIPLE
      snapshots per vehicle across different `CapturedAt` values to exercise
      `SnapshotsByVehicleSince`'s window filter and `LatestSnapshotsByAccount`'s
      "newest wins" selection — for each such literal, set `CapturedDate` to the calendar
      date of that literal's own `CapturedAt` (e.g.
      `time.Date(y, m, d, 0, 0, 0, 0, time.UTC)` derived from the same `CapturedAt` value
      already in the literal), so distinct-day test fixtures remain distinct rows under
      the new constraint. Where a test's fixtures are same-day by construction (if any),
      confirm that is actually intended post-change and adjust the assertion accordingly
      (it will now legitimately collapse to one row).
      Acceptance: `go test ./internal/telemetry/...` (DB-gated) passes with the same
      number of rows each test originally expected wherever the fixtures were genuinely
      multi-day; any test whose fixtures turn out to be same-day is updated to assert the
      new (correct) one-row-replace outcome instead.

- [x] T9.3 In `internal/telemetry/db_sourcea_integration_test.go`: same audit as T9.2 —
      set `CapturedDate` explicitly and consistently with each literal's `CapturedAt` on
      every `Snapshot{}` passed to `st.insertSnapshot`.
      Acceptance: `go test ./internal/telemetry/...` (DB-gated) passes.

- [x] T9.4 In `internal/telemetry/db_tpms_integration_test.go`: same audit as T9.2 — set
      `CapturedDate` explicitly and consistently with each literal's `CapturedAt` on every
      `Snapshot{}` passed to `st.insertSnapshot`.
      Acceptance: `go test ./internal/telemetry/...` (DB-gated) passes.

---

## Verification — depends on all tasks

- [ ] V1. `go build ./...` and `go vet ./...` pass after all tasks are complete (including
      the `cmd/poller/main.go` change, T6).
- [ ] V2. `go test ./...` green and fast. DB integration tests self-skip without
      `DATABASE_URL`; with Docker the testcontainers helper provisions Postgres and
      applies goose migrations automatically, including the new
      `20260805000001_dedupe_vehicle_snapshots_daily.sql`. NO Tesla API call fires.
- [ ] V3. Dedupe correctness (live/test DB): a second capture for a vehicle on the SAME
      calendar day results in exactly one row, with the newer capture's values. Verified
      by T9.1's `TestStore_SnapshotUpsert_SameDayReplaces`.
- [ ] V4. New-day correctness: a capture on a NEW calendar day always inserts a new row and
      never touches a prior day's row. Verified by T9.1's
      `TestStore_SnapshotInsert_DifferentDayCreatesNewRow`.
- [ ] V5. Timezone correctness: `dateOnly` attributes a capture to the correct LOCAL
      calendar day, not the UTC day, for a moment that straddles the UTC/local boundary.
      Verified by T8.1(b).
- [ ] V6. `poll_attempts` is completely unaffected: no migration, query, or Go change in
      this entire change touches it (design D4). Confirmed by inspection — grep the final
      diff for `poll_attempts` and verify zero hits outside pre-existing, untouched code.
- [ ] V7. Boundary check: `internal/telemetry` still imports only `account` + `tesla`
      public packages; no `accountdb` or `internal/tesla` internals; `pgtype` does not
      appear in any public type or interface (`dateFrom`/`pgtype.Date` stay confined to
      `service.go`/`mapping.go`). The `cmd/poller/main.go` change (T6) is thin wiring only
      — zero business logic added, consistent with `ai/go-conventions.md`.
- [ ] V8. Docs: `internal/telemetry/AGENTS.md` "Data ownership" accurately reflects the new
      invariant (T7.1) with no lingering "append-only" claim about `vehicle_snapshots`
      specifically (the `poll_attempts` claim must remain, unchanged and accurate).
      `README.md`'s "Project Structure"/"Architecture" sections are confirmed NOT to need
      changes (no module added/removed, no new runnable) — this confirmation itself is
      part of verification, not an assumption to skip.
- [ ] V9. `openspec validate telemetry-dedupe-daily-snapshots --strict` passes.
