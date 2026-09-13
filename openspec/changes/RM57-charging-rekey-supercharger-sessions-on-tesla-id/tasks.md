# Tasks — RM57-charging-rekey-supercharger-sessions-on-tesla-id

All work is inside `internal/charging`, except **T8**, which is leader-owned and
touches `internal/app`, `internal/analytics` and `internal/gateway`.

## Dependency graph

```
T0 (offline check, no deps) ──────────────────────────────┐
                                                          │
T1 (migrations) ──► T2 (queries + sqlc) ──► T3 (ports) ──► T4 (implementation)
                                             │                  │
                                             │                  ├──► T5 (DB integration tests)
                                             │                  ├──► T6 (remaining test fixups)
                                             │                  └──► T8 (leader-owned: app + analytics + gateway)
                                             └──► T7 (docs) — parallel with T4–T8
                                                          T9 (final verification) — depends on all
```

Sequencing rules that are not optional:

- **T1 → T2 → T3 → T4 is a hard chain.** Go cannot compile against
  sqlc-generated types until they are regenerated, and the ports cannot be
  re-implemented until those types exist.
- **T0 is a grep and a `go vet`, with no schema dependency**, and may start
  immediately.
- **T5 and T6 may run in parallel** — disjoint files. T5 owns the
  `db_session_*`, `db_mirror_*` and `db_inferred_capacity_sessions_*` files;
  T6 owns everything else.
- **T7 (docs) touches no Go file** and may run in parallel with T4–T8.
- **T8 must land in the same wave as T4.** `go build ./...` is red outside
  `internal/charging` until it does (`design.md` D5).

---

## T0 — Offline check

Depends on: nothing.

- [x] 0.1 Grep the module's five offline test files (`charging_test.go`,
      `entry_status_test.go`, `monthly_capacity_estimator_test.go`,
      `price_source_test.go`, `session_verifier_derivation_test.go`) for
      `AccountID`, `accountID` and `account_id`. Expect **zero** hits. If a hit
      appears, the file is not offline-only and belongs to T6 instead.
- [x] 0.2 Record in the change's notes that this change adds **no new offline
      test**, and why: no pure function changes behaviour; the whole change is
      schema, queries and port signatures (`design.md` §Test contract).

## T1 — Migrations

Depends on: nothing.

- [ ] 1.0 **Owner pre-check, before the migration is applied** (`design.md` D2).
      Hand the owner these two queries and record the answers in the change:
      `SELECT count(*) FROM charging.supercharger_sessions WHERE tesla_id IS NULL;`
      and
      `SELECT session_id, count(*) FROM charging.supercharger_sessions GROUP BY session_id HAVING count(*) > 1;`
      A non-empty second result means a duplicate pair exists, and the owner
      must confirm which copy to keep before the migration runs.
      **Not run by this worker — needs a live database connection the owner
      must check. See the final report.**
- [x] 1.1 Write `internal/charging/db/migrations/20260912000002_rekey_supercharger_sessions_on_tesla_id.sql`
      exactly as `design.md` §Schema gives it — Up and Down, comments included.
      Do not renumber: `20260912000002` is free across all four module
      directories (`design.md` §Makefile).
- [x] 1.2 Write `internal/charging/db/migrations/20260912000003_rekey_mirror_watermarks_on_tesla_id.sql`
      exactly as `design.md` §Schema gives it — Up and Down, comments included.
- [x] 1.3 Do NOT touch `COMMENT ON TABLE` or any `COMMENT ON COLUMN`
      (`design.md` D8), the primary keys, or any CHECK constraint.
- [x] 1.4 Do NOT touch `charging.manual_charge_entries` in either file. MAG-68
      owns that table.
- [x] 1.5 `make migration-guard` — expect clean.
- [x] 1.6 Do NOT write a test for either migration. This project does not test
      migrations; the owner verifies them against the database directly.

## T2 — Queries and sqlc

Depends on: T1.

- [x] 2.1 `internal/charging/db/query.sql`, `MirrorSuperchargerSession`: remove
      `account_id` from the INSERT column list and from `VALUES`; change
      `ON CONFLICT (account_id, session_id)` to `ON CONFLICT (session_id)`;
      remove `account_id` from **both** `'{…}'::text[]` deny-list arrays. Leave
      the load-bearing comment about the human-owned battery-% trio word for
      word.
- [x] 2.2 Same query: rewrite the closing `tesla_id` paragraph. It stays inside
      the change comparison because telemetry refreshes it and a mirrored column
      takes its source's write semantics — the old orphan-recovery reason no
      longer exists.
- [x] 2.3 `ListSessionsByVehicleBetween`, `ListSessionsByVehicleUpdatedSince`,
      `ListSessionsByVehicle`: drop the `account_id` predicate from each. Update
      each index comment to `(tesla_id, charge_stop_date_time)`, and delete the
      closing paragraph in each about `tesla_id = @tesla_id` excluding NULL rows
      — the column is `NOT NULL` now.
- [x] 2.4 `ListSessionsByVehicleUpdatedSince`: keep the "no new index"
      paragraph and restate its reason — this query orders by
      `charge_stop_date_time`, so an `updated_at` index would force a sort
      (`design.md` D6).
- [x] 2.5 `LockSessionForVerification` and `VerifySuperchargerSession`:
      `WHERE id = @id AND tesla_id = @tesla_id`. Update both comments — the
      scope is the vehicle now, and on `VerifySuperchargerSession` say plainly
      that this predicate is the write path's tenant boundary.
- [x] 2.6 `GetMirrorWatermark`: `WHERE tesla_id = @tesla_id`. Update the comment
      — one cursor per vehicle, served by `mirror_watermarks_vehicle_unique`.
- [x] 2.7 `UpsertMirrorWatermark`: `INSERT … (tesla_id, source_updated_at)` and
      `ON CONFLICT (tesla_id)`. `created_at` stays out of the SET clause.
- [x] 2.8 `ListValidSessionCapacitiesForPeriod`: delete `AND tesla_id IS NOT NULL`
      and the sentence explaining it — the column is `NOT NULL`.
- [x] 2.9 Do NOT touch any `manual_charge_entries` query
      (`CreateEntry`, `UpdateEntry`, `DeleteEntry`, `ListEntries*`,
      `ListValidManualEntryCapacitiesForPeriod`). They keep `account_id`.
- [x] 2.10 `make sqlc`. Confirm in the diff that
      `chargingdb.SuperchargerSession` lost `AccountID` and that its `TeslaID`
      is `int64`, not `pgtype.Int8`; that `LockSessionForVerificationParams` and
      `VerifySuperchargerSessionParams` now carry `TeslaID int64`; that
      `GetMirrorWatermark` takes a plain `int64`; and that
      `UpsertMirrorWatermarkParams` carries `TeslaID int64` instead of
      `AccountID`.

## T3 — Ports and domain types

Depends on: T2.

- [x] 3.1 `internal/charging/charging.go`: `SessionMirror` drops `AccountID`;
      `TeslaID` becomes `int64`. Replace the field's "nil when the VIN is not a
      currently-registered vehicle" comment with the real reason — a session is
      only mirrored for a registered vehicle.
- [x] 3.2 Same file: `Session` drops `AccountID`; `TeslaID` becomes `int64`.
      Update the type's doc comment — it says "nineteen fields, one per
      `supercharger_sessions` column"; there are eighteen columns now.
- [x] 3.3 `SessionWriter.MirrorSessions(ctx, sessions []SessionMirror) error`.
      Delete the "every entry's `AccountID` must equal `accountID`" sentence from
      the doc comment — there is no scope argument left to match against.
- [x] 3.4 `SessionReader.ListSessionsByVehicleBetween`,
      `SuperchargerSessionAnalyticsReader.ListSessionsByVehicleUpdatedSince`
      and `…ListSessionsByVehicle`: drop `accountID` from each signature.
      Delete the doc-comment sentences promising that a session with a nil
      `TeslaID` is never returned — no such session can exist.
- [x] 3.5 `SessionVerifier.VerifySession(ctx, teslaID int64, id uuid.UUID, startBatteryPct, endBatteryPct *int)`.
      Replace `accountID` with `teslaID` — do NOT simply drop it. Document why
      in the doc comment, in plain words: this predicate is the only tenant
      boundary on the Supercharger write path, and a mismatched vehicle is
      reported exactly like an unknown id so the two cannot be told apart.
- [x] 3.6 `MirrorWatermarkStore.MirrorWatermark(ctx, teslaID int64)` and
      `AdvanceMirrorWatermark(ctx, teslaID int64, observed time.Time)`. Update
      the doc comments: one cursor per vehicle, an absent cursor still means
      epoch, and the "never advance to `now()`" rule is unchanged.
- [x] 3.7 Do NOT change `Writer`, `Reader`, `Entry`, or any
      `manual_charge_entries` port. They keep `accountID`.

## T4 — Implementation

Depends on: T3.

- [x] 4.1 `internal/charging/session_writer.go`: delete the loop that validated
      each entry's `AccountID` against the call scope. Keep the empty-slice
      short circuit and the single-transaction shape. Drop `AccountID` from the
      params literal and pass `TeslaID: s.TeslaID` directly.
- [x] 4.2 `internal/charging/session_reader.go`: the three methods drop
      `accountID` from their signature and their `…Params` literal.
      `rowToSession` drops `AccountID` and assigns `TeslaID: r.TeslaID`.
- [x] 4.3 `internal/charging/session_verifier.go`: `VerifySession` takes
      `teslaID int64` and passes it to both `LockSessionForVerification` and
      `VerifySuperchargerSession`.
- [x] 4.4 Same file: the derived-start branch loses its nil check.
      `row.TeslaID` is a plain `int64`, so `packCapacityKWh(ctx, v, row.TeslaID)`
      always runs and the local `defaultPackCapacityKWh` fallback branch is
      deleted. The fallback still exists one level down inside `packCapacityKWh`
      — do not duplicate it here.
- [x] 4.5 `internal/charging/mirror_watermark.go`: both methods take
      `teslaID int64`; the error messages say `vehicle %d` instead of
      `account %s`.
- [x] 4.6 `internal/charging/monthly_capacity.go`: the session branch drops the
      `pgInt8ToInt64Ptr` call and the `tid == nil` guard, and groups under
      `r.TeslaID` exactly like the manual-entry branch above it.
- [x] 4.7 Grep the module for `int64PtrToPgInt8` and `pgInt8ToInt64Ptr`. Delete
      either helper only if it has no call site left; both are in
      `session_writer.go`. Confirm with the grep before deleting — do not assume.
- [x] 4.8 `go build ./internal/charging/...` and `go vet ./internal/charging/...`
      — expect clean. The rest of the repo is still red until T8.

## T5 — Database-backed integration tests

Depends on: T4. May run in parallel with T6 — disjoint files.

- [ ] 5.1 Delete `internal/charging/db_backfill_integration_test.go` entirely
      (`design.md` D7). It re-executes a shipped migration's backfill, which
      this project does not test, and that statement names a column this change
      drops.
- [ ] 5.2 `db_session_integration_test.go`: drop `account_id` from every seed
      `INSERT` and every cleanup `DELETE`; re-key every call to the new
      signatures. Add T-1 and T-2's exact seeds and expectations — one row per
      session id, and a re-mirror under a different vehicle refreshing
      `tesla_id` instead of inserting a second row.
- [ ] 5.3 `db_session_reader_integration_test.go` and
      `db_session_reader_by_vehicle_integration_test.go`: re-key to T-3's and
      T-4's seeds and expected orders — `[9101, 9102]` for the bounded window,
      `[9203, 9202]` for the newest-first limited read, with the other
      vehicle's row present and never returned.
- [ ] 5.4 `db_session_reader_updated_since_integration_test.go`: re-key to
      T-5 — the three `updated_at` values, the second-vehicle decoy, and the
      three `since` assertions (`t2 → [9302, 9303]`, `t3 → [9303]`,
      `t3 + 1µs → empty non-nil`).
- [ ] 5.5 Add T-10 and T-11: `EXPLAIN` the bounded-window query and the
      newest-first query, assert both plans name
      `idx_supercharger_sessions_vehicle_stop`, that the second reports a
      backward scan, and that neither contains a `Sort` node.
- [ ] 5.6 `db_session_verifier_integration_test.go`: re-key every seed and call.
      Add T-6 — a verify naming the wrong vehicle returns an error wrapping
      `pgx.ErrNoRows` AND leaves the row's percentages and status untouched,
      proven by a direct `SELECT`, not only by the returned error. Add T-7 —
      the derived-start path storing `30` from `energy_kwh = 31.0` and
      `end = 80`, with `Status == DONE_CALCULATED`.
- [ ] 5.7 `db_mirror_schema_selfcheck_integration_test.go`: remove `account_id`
      from the deny-list constant (13 entries left) and from its comment. Do not
      change the partition logic. T-8's numbers: 5 + 13 = 18.
- [ ] 5.8 `db_session_mirror_change_detection_integration_test.go`: drop
      `account_id` from every seed and from the "poke every settable
      deny-listed column" loop (T-9).
- [ ] 5.9 `db_mirror_watermark_integration_test.go`: re-key every call to
      `teslaID`. Add T-12's full sequence, including the assertion that a second
      vehicle of the same account still reports the epoch.
- [ ] 5.10 `db_inferred_capacity_sessions_integration_test.go`: drop
      `account_id` from every session seed. The generated-column assertions are
      unchanged.
- [ ] 5.11 `db_monthly_capacity_integration_test.go`: drop `account_id` from
      every `supercharger_sessions` seed (leave the `manual_charge_entries`
      seeds alone). Add T-13's expectations — `VehiclesFound == 1`,
      `Measured == 0`, `Thin == 1`, `candidate_count = 1`, `sample_count = 1`,
      `effective_capacity_kwh` NULL.
- [ ] 5.12 Assert `RowsAffected()` on every fixture `UPDATE`/`DELETE` this task
      touches — a fixture write that matches no row does not error, and the
      assertions after it then pass or fail for an unrelated reason
      (`design.md` §Risks).

## T6 — Remaining test fixups inside charging

Depends on: T4. May run in parallel with T5.

- [ ] 6.1 Grep every `_test.go` in `internal/charging` for
      `supercharger_sessions` and `mirror_watermarks` — **across line breaks**,
      not line by line: a table name and its column often sit on different
      lines, so a line-based grep finds some hits and misses others.
- [ ] 6.2 `testdb_test.go` and any shared seed helper: drop `account_id` from
      every `supercharger_sessions` / `mirror_watermarks` insert. Leave every
      `manual_charge_entries` helper untouched.
- [ ] 6.3 `db_integration_test.go`, `db_entry_status_integration_test.go`,
      `db_promotion_price_source_integration_test.go`,
      `db_inferred_capacity_entries_integration_test.go`: these are
      `manual_charge_entries` suites and must keep `account_id`. Confirm each
      one compiles unchanged; change only a line that touches one of this
      change's two tables.
- [ ] 6.4 `go vet ./internal/charging/...` — expect clean. `go vet` compiles
      `_test.go` files, so this is what proves T5's signatures are right.

## T7 — Docs

Depends on: T3 (needs the final port shape). Touches no Go file — safe to run
in parallel with T4–T8.

- [ ] 7.1 `internal/charging/AGENTS.md` — six corrections, all named in
      `design.md` §Docs: the `mirror_watermarks` section's "NO `tesla_id`
      column" paragraph and its per-account cursor description; the mirror
      section's deny-list, which names `account_id` as write-once mirrored; the
      `SessionMirror` / `Session` notes saying `TeslaID` is nil for an
      unregistered VIN; the Data Ownership table's "Written by" column and the
      `WHERE account_id = @account_id` tenant-scoping rule; the Testing Notes
      bullet describing `db_backfill_integration_test.go`, which no longer
      exists.
- [ ] 7.2 `kkpa/context/architecture/charging-tables.md` — the
      `supercharger_sessions` column list, the index entry, and the unique
      constraint. State the new key: `tesla_id NOT NULL`, no `account_id`,
      `UNIQUE (session_id)`.
- [ ] 7.3 `kkpa/context/architecture/nightly-cycle.md` — the `MirrorSessions`
      row ("one transaction per account, rejects a mis-scoped `AccountID`"), the
      consumer table, and the per-table read/write table. The mirror runs one
      pass per vehicle now.
- [ ] 7.4 `kkpa/context/architecture/gateway-reader-writer-ports.md` — the
      `VerifySession` row's `WHERE id = @id AND account_id = @account_id`
      statement and the "**No ownership check**" note.
- [ ] 7.5 `kkpa/context/workflows/supercharger-stats-read.md` — the printed
      `VerifySession` signature, the "(no `TeslaID` predicate)" clause, and the
      tenant-boundary bullet.
- [ ] 7.6 `kkpa/context/use-case/charging/verify-session-battery.md` and
      `kkpa/context/input-port/charging/supercharger-stats.md` — both describe
      the account-scoped verify call.
- [ ] 7.7 `kkpa/context/architecture/charge-record-mutation.md` — the line
      saying `SuperchargerRowUpdate` "relies solely on the SQL `AND account_id`
      scope".
- [ ] 7.8 Do NOT edit anything under `openspec/changes/archive/`. A grep for
      `account_id` will hit archived designs; those hits are the record of what
      was decided then and are not yours to fix. `make archive-guard` enforces
      it.
- [ ] 7.9 Do NOT edit anything under `kkpa/context/pending-spec-to-sync/applied/`.
      Those are applied proposals, a record, not live guides.

## T8 — Cross-module bridge (leader-owned)

Depends on: T3 (needs the final port signatures). **Not this module's sandbox —
`internal/charging` workers must not edit these files.** Must land in the same
wave as T4, or `go build ./...` stays red (`design.md` D5).

- [ ] 8.1 `internal/app/processor.go`, `processChargingData`: replace the
      per-account grouping with one pass per distinct `tesla_id`. For each
      vehicle: read `MirrorWatermark(ctx, teslaID)`, call
      `SuperchargerHistoryByVehicleUpdatedSince(ctx, teslaID, cursor.Add(-mirrorOverlap))`,
      skip on a zero-row read, build the `[]charging.SessionMirror`, call
      `MirrorSessions(ctx, mirrored)`, then
      `AdvanceMirrorWatermark(ctx, teslaID, maxUpdated)`. The "never advance to
      `now()`" and "never advance on a zero-row read" rules are unchanged.
- [ ] 8.2 Same file: `charging.SessionMirror` no longer has `AccountID`, and
      `TeslaID` is a plain `int64` — pass `s.TeslaID` directly, not its address.
- [ ] 8.3 Same file: rewrite the function's doc comment. "Why per ACCOUNT and
      not per vehicle" is now wrong, and so is the orphan-recovery paragraph it
      rests on. Say instead that the pass is per vehicle because the cursor is,
      and that a car registered to two accounts is mirrored once.
- [ ] 8.4 `internal/app/processor_test.go`: the `charging.SessionWriter` and
      `charging.MirrorWatermarkStore` fakes must match the new signatures.
      Re-shape the two mirror tests for the per-vehicle loop and keep the
      watermark assertions (zero rows leaves the cursor untouched; the new
      cursor is the highest observed `updated_at`, never `now()`).
- [ ] 8.5 `internal/analytics/reader.go:141`, `recalculate.go:158` and
      `recalculate.go:279`: drop the `accountID` argument from the three
      `ListSessionsByVehicle*` calls. Check whether `accountID` is still used
      elsewhere in each function before deleting the parameter that carries it.
- [ ] 8.6 `internal/analytics` tests: every fake implementing
      `charging.SuperchargerSessionAnalyticsReader` re-signs its methods, and
      every raw `supercharger_sessions` seed drops `account_id`. Any fake that
      records a `gotAccountID` for these ports records the vehicle instead.
- [ ] 8.7 `internal/gateway/handlers/supercharger.go`: the two
      `ListSessionsByVehicleBetween` calls drop `uid`. The `VerifySession` call
      passes the resolved vehicle's `TeslaID` in place of `uid` — resolve it
      with the handler's existing `resolveSelectedVehicle` before the call.
      Tier 3 then replaces that resolve with `authorizeVehicle`.
- [ ] 8.8 Same file: `updated.TeslaID` is a plain `int64`, so the
      `if updated.TeslaID != nil` branch and its "skipping recalculation" log
      line are deleted — `recalculateAfterSessionVerify` always runs.
- [ ] 8.9 `internal/gateway/gateway.go` and `cmd/web/main.go`: update the two
      doc comments that describe `VerifySession` as account-scoped.
- [ ] 8.10 `go build ./internal/app/... ./internal/analytics/... ./internal/gateway/... ./cmd/...`
      and `go vet` the same packages — expect clean.

## T9 — Final verification

Depends on: T0–T8.

- [ ] 9.1 `go build ./...`
- [ ] 9.2 `go vet ./...`
- [ ] 9.3 `make migration-guard`
- [ ] 9.4 `make boundary-guard`
- [ ] 9.5 `make vehicleref-guard`
- [ ] 9.6 `make archive-guard`
- [ ] 9.7 `gofmt -l internal/ cmd/` — expect no output.
- [ ] 9.8 Grep the whole repo for `account_id` within 3 lines of
      `supercharger_sessions` and of `mirror_watermarks`, and for
      `MirrorSessions(ctx, ` followed by a UUID argument. Expect zero hits
      outside `openspec/changes/archive/`,
      `kkpa/context/pending-spec-to-sync/applied/` and the untouched historic
      migration files.
- [ ] 9.9 `openspec validate RM57-charging-rekey-supercharger-sessions-on-tesla-id --strict`
- [ ] 9.10 Hand the owner the suite commands — this agent never runs them:
      `make test` (disposable container) and, for the DB-backed charging tests
      specifically,
      `go test ./internal/charging/ -run 'TestSession|TestMirror|TestVerify|TestMonthlyCapacity' -v`,
      confirming the output says `PASS` and not `SKIP`. If a local Postgres is
      used instead, `make db-setup-test` first.
- [ ] 9.11 Hand the owner the post-`migrate-up` verification queries, since this
      project verifies migrations by inspection, not by test:
      `\d charging.supercharger_sessions` and `\d charging.mirror_watermarks`
      (expect no `account_id`, `tesla_id NOT NULL`,
      `supercharger_sessions_session_id_unique`,
      `idx_supercharger_sessions_vehicle_stop (tesla_id, charge_stop_date_time)`,
      `mirror_watermarks_vehicle_unique`), plus
      `SELECT count(*) FROM charging.mirror_watermarks;` (expect 0 until the
      next nightly run).
