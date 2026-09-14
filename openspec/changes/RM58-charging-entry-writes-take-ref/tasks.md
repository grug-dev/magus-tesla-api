# Tasks — RM58-charging-entry-writes-take-ref

All work is inside `internal/charging`, except **T6**, which is leader-owned
and touches `internal/gateway`.

**Unit tests: EXCLUDED.** No new test file and no new test function, except two
existing tests renamed (same file, same package) because the axis they test
moves from account to vehicle. Every other existing test whose call site
changed keeps its name and expectations. That update is T4 (inside the
module).

## Dependency graph

```
T0 (offline grep, no deps) ────────────────────────────────────────┐
                                                                    │
T1 (queries + sqlc) ──► T2 (ports) ──► T3 (service) ──► T4 (tests) │
                                          │                          │
                                          ├──► T5 (docs, module-owned)
                                          └──► T6 (leader-owned: gateway)
                                                          │
                                          T7 (docs, cross-module-aware,
                                              depends on T6) ────────┤
                                                                    │
                                          T8 (final verification) ◄─┘
```

Sequencing rules that are not optional:

- **T1 → T2 → T3 → T4 is a hard chain.** Go cannot compile against
  sqlc-generated types until they are regenerated, and the service cannot be
  re-implemented until the ports and generated types exist.
- **T0 is a grep, with no dependency**, and may start immediately.
- **T5 and T6 may run in parallel** — disjoint files, disjoint modules. Both
  depend on T3 (the final signatures must exist before either doc set or the
  gateway call sites can be written correctly).
- **T6 must land in the same wave as T3/T4.** `go build ./...` is red outside
  `internal/charging` until it does (`design.md` D8, §Risks).
- **T7 depends on T6, not only T3.** Two KB use-case docs
  (`update-manual-charge.md`, `delete-manual-charge.md`) describe the gateway
  flow, including the new `authorizeVehicle` step T6 adds. Writing that step
  before T6 exists would describe code that is not there yet. T5 has no such
  dependency and may run alongside T6 instead of after it.

---

## T0 — Offline check

Depends on: nothing.

- [x] 0.1 Grep the module's five offline test files (`charging_test.go`,
      `entry_status_test.go`, `monthly_capacity_estimator_test.go`,
      `price_source_test.go`, `session_verifier_derivation_test.go`) for
      `.Update(` and `.Delete(`. Expect **zero** hits. If a hit appears, that
      file is not offline-only and belongs to T4 instead.
- [x] 0.2 Grep every `_test.go` file in `internal/charging` for `.Update(ctx`
      and `.Delete(ctx`. Confirm the count matches `design.md` §Test
      contract's table exactly (12 `Update` calls across four files, 2
      `Delete` calls, all in `db_integration_test.go`). If the count differs,
      `design.md` is out of date — stop and reconcile before continuing.
- [x] 0.3 Record in the change's notes that this change adds no new test
      function except the two renamed guard tests, and why: the ticket
      excludes unit tests, and every other call site's behaviour is unchanged
      by this tier — only which value the guard checks moves.

## T1 — Queries and sqlc

Depends on: nothing (no migration in this tier, so no schema dependency).

- [x] 1.1 `internal/charging/db/query.sql`, `UpdateEntry`: change `WHERE id =
      @id AND created_by_account_id = @created_by_account_id` to `WHERE id =
      @id AND tesla_id = @tesla_id`. `:one` annotation and `RETURNING *`
      unchanged. Rewrite the comment per `design.md` §Queries: the `Ref` is
      the real check, this clause is the second line of defence against a
      right-`Ref`-wrong-row mistake. Do not name a tier, a change id, or a
      decision id in the comment — write the reason itself.
- [x] 1.2 `DeleteEntry`: change the annotation from `:exec` to `:execrows`.
      Change the `WHERE` clause the same way as 1.1. Same comment-rewrite
      rule.
- [x] 1.3 Do NOT touch any other query in this file — no read query, no
      `supercharger_sessions`/`mirror_watermarks`/`monthly_effective_capacity`
      query, and not `CreateEntry`.
- [x] 1.4 `make sqlc`. Confirm the diff against `design.md` §What `make sqlc`
      will produce, point by point: `chargingdb.ManualChargeEntry` is
      **byte-for-byte unchanged**; `UpdateEntryParams` loses
      `CreatedByAccountID`, gains `TeslaID int64`, and survives as a struct;
      `DeleteEntryParams` has `CreatedByAccountID` replaced by `TeslaID
      int64` and survives as a two-field struct; `func (q *Queries)
      DeleteEntry(...)` returns `(int64, error)` instead of `error`; no new
      `Row` type appears anywhere.
- [x] 1.5 Do NOT write a test for this query change beyond what T4 already
      covers through the service layer — there is nothing migration-shaped
      here to separately verify.

## T2 — Ports (`charging.go`)

Depends on: T1 (needs the final generated param shapes to know the service
layer compiles against, even though this file itself does not import
`chargingdb`).

- [x] 2.1 Confirm `internal/vehicleref` is already imported in this file
      (`RM57-charging-verifysession-takes-ref` added it for
      `SessionVerifier.VerifySession`) — do not add a second import line.
- [x] 2.2 Change `Writer.Update`'s signature to `Update(ctx context.Context,
      ref vehicleref.Ref, e Entry) (Entry, error)`.
- [x] 2.3 Change `Writer.Delete`'s signature to `Delete(ctx context.Context,
      ref vehicleref.Ref, id uuid.UUID) error`. `uuid.UUID` stays imported for
      `id`'s type and for `Entry.ID`/`CreatedByAccountID` — do not remove the
      `github.com/google/uuid` import.
- [x] 2.4 Rewrite the `Writer` interface's doc comment exactly as `design.md`
      §Ports and implementation gives it. Do NOT leave the old sentence about
      "Delete takes accountID as a required argument so the SQL WHERE clause
      scopes to the account that typed the entry."
- [x] 2.5 Add the doc comment to `Entry.TeslaID` exactly as `design.md` gives
      it. This field has no comment today — confirm that before writing, so
      the new comment is added, not appended to a comment that does not
      exist.
- [x] 2.6 `Entry.CreatedByAccountID`'s existing comment says "Update and
      Delete still match on it, as the only guard they have until they can
      name the vehicle instead." Remove that sentence — it is no longer true.
      State instead that it is authorship only, recorded on create, never
      touched or checked again.
- [x] 2.7 Do NOT touch `Reader`, `SessionWriter`, `SessionReader`,
      `SessionVerifier`, or `MirrorWatermarkStore` in this file.

## T3 — Service (`service.go`)

Depends on: T2 (needs the final port signatures to implement against) and T1
(needs the final generated param/return shapes).

- [x] 3.1 Add the import `"github.com/cristianpena/magus-tesla-api/internal/vehicleref"`.
- [x] 3.2 Change the `store` interface's `deleteEntry` method to return
      `(int64, error)` instead of `error`.
- [x] 3.3 Change `dbStore.deleteEntry` to return `d.q.DeleteEntry(ctx, params)`
      directly — no wrapping, since the generated method already returns
      `(int64, error)` after T1.4.
- [x] 3.4 Rewrite `writerService.Update` exactly as `design.md` §Ports and
      implementation gives it: signature gains `ref vehicleref.Ref` as the
      second parameter; the FIRST line of the function body is `e.TeslaID =
      ref.TeslaID()`, before `normalizeStatus` and everything after it;
      `UpdateEntryParams`'s literal drops `CreatedByAccountID` and gains
      `TeslaID: e.TeslaID`. Every other line — `promoteIfComplete`,
      `missingFields`, `resolveEnergy`, numeric encoding, the rest of the
      params literal, `rowToEntry` — is unchanged.
- [x] 3.5 Rewrite `writerService.Delete` exactly as `design.md` gives it:
      signature's `accountID uuid.UUID` becomes `ref vehicleref.Ref`;
      `DeleteEntryParams`'s literal drops `CreatedByAccountID` and gains
      `TeslaID: ref.TeslaID()`; after the call, check the returned row count
      — zero becomes `fmt.Errorf("charging: delete entry: %w", pgx.ErrNoRows)`.
      `pgx` is already imported in this file — do not add a second import.
- [x] 3.6 Rewrite both methods' doc comments per `design.md` — the account
      language ("scoped to the account that typed it", "double-scope (id AND
      created_by_account_id)") is replaced with the `Ref`-pinned guard
      description.
- [x] 3.7 Do NOT touch `writerService.Create`, `resolvePriceSource`,
      `missingFieldsError`, `readerService`, or any of the four `list*`
      store methods.
- [x] 3.8 `go build ./internal/charging/...` — expect failures ONLY in this
      module's own `_test.go` files (T4's job), not in any non-test file.

## T4 — Tests (module-owned)

Depends on: T3 (needs the final signatures to compile against).

- [x] 4.1 Add the `refFor` helper to `db_integration_test.go`, next to
      `minEntry`, exactly as `design.md` §Test contract gives it. Add the
      `vehicleref` import to this file's import block.
- [x] 4.2 Update the 12 mechanical `Update`/`Delete` call sites listed in
      `design.md` §Test contract's table — insert `refFor(<teslaID>)` as the
      argument right after `ctx`, using the `teslaID` the test already has in
      scope for that entry. Do NOT change any assertion in these tests.
- [x] 4.3 Rename `TestUpdate_CrossAccountIsNoOp` to
      `TestUpdate_CrossVehicleIsNoOp` exactly as `design.md` §Test contract
      specifies: entry created for `teslaID = 333`, call
      `w.Update(ctx, refFor(999), tampered)`, expect a non-nil error, assert
      the owner's row (read via `ListEntriesByVehicle(ctx, 333, 10)`) is
      unchanged. Update the doc comment above the test to describe a vehicle
      mismatch, not an account mismatch.
- [x] 4.4 Rename `TestDelete_CrossAccountGuard` to
      `TestDelete_CrossVehicleGuard` exactly as `design.md` §Test contract
      specifies: entry created for `teslaID = 555`, call
      `w.Delete(ctx, refFor(998), created.ID)`. **This assertion inverts**:
      expect a non-nil error wrapping `pgx.ErrNoRows` (assert with
      `errors.Is`), where the test today asserts `err == nil`. Assert the
      owner's row (read via `ListEntriesByVehicle(ctx, 555, 10)`) still
      exists. Update the doc comment above the test to state the behaviour
      change plainly — a mismatched delete now errors instead of silently
      doing nothing.
- [x] 4.5 `go build ./internal/charging/...` and `go vet ./internal/charging/...`
      — expect clean. `go vet` compiles every `_test.go` file, so a missed
      call site or a wrong `refFor` argument type fails here, not later.
- [x] 4.6 `gofmt -l internal/charging` — expect no output.

## T5 — Docs (module-owned, no gateway dependency)

Depends on: T3 (needs the final port shape and doc comments). Touches no Go
file — safe to run in parallel with T6.

- [x] 5.1 `internal/charging/AGENTS.md` §Public Interface — replace the
      paragraph describing `Writer.Update`/`Delete` as scoped by
      `CreatedByAccountID`, "transitionally," with the `vehicleref.Ref`
      guard description. State plainly that the transitional period named in
      tier 1's own note has ended.
- [x] 5.2 `internal/charging/AGENTS.md` §Allowed Imports — add `service.go` to
      the `internal/vehicleref` bullet's file list (today it names only
      `charging.go` and `session_verifier.go`).
- [x] 5.3 `internal/charging/AGENTS.md` §Data Ownership — the
      `manual_charge_entries` row's "writes ... are the one exception: they
      still predicate on `created_by_account_id`, transitionally" sentence.
      Replace it with the `tesla_id` guard, and remove the "until a later
      tier" framing — this tier is that later tier.
- [x] 5.4 Apply the four spec-delta requirement changes from
      `openspec/changes/RM58-charging-entry-writes-take-ref/specs/manual-charge-log/spec.md`
      to `openspec/specs/manual-charge-log/spec.md` if the project's archive
      step does not do this automatically — confirm with the leader which
      convention this project uses before hand-editing the main spec (this
      project's OpenSpec workflow may sync deltas on archive rather than on
      apply; do not duplicate that step).
- [x] 5.5 `kkpa/context/architecture/charging-tables.md` — confirm by reading
      it that no edit is needed (`design.md` says this file is already
      correct after tier 1). If it turns out to need one, that is a finding
      to report, not something to silently skip.
- [x] 5.6 `kkpa/context/workflows/manual-charge-crud.md` — three edits per
      `design.md` §Docs: the `query.sql` row's "transitional" language, the
      Update flow bullet's `WHERE id AND created_by_account_id` text, and the
      "write guard is transitional" gotcha. State the guard is now
      `tesla_id`/`Ref`-keyed and no longer transitional. Also extend the
      authorship paragraph's "No read may filter... by it" to "No read or
      write".
- [x] 5.7 Do NOT edit anything under `openspec/changes/archive/`. A grep for
      `created_by_account_id` will hit tier 1's archived design; that hit is
      the record of what was true then, and `make archive-guard` enforces
      leaving it alone.

## T6 — Gateway call sites (leader-owned)

Depends on: T3 (needs the final port signatures). **Not this module's
sandbox — `internal/charging` workers must not edit these files.** Must land
in the same wave as T3/T4, per `design.md` §Risks: `go build ./...` is red
outside `internal/charging` until this lands.

- [ ] 6.1 `internal/gateway/handlers/external_charges.go`,
      `ExternalChargeRowUpdate`: after `h.fetchEntryTeslaIDAndChargedOn`
      resolves `_, oldChargedOn, hadOld` (today's shape captures only two of
      its three return values — this task needs the first one too, the
      entry's `tesla_id`), call `h.authorizeVehicle(ctx, uid, entryTeslaID)`.
      On error, render the existing not-authorized handling this handler's
      neighbours already use (mirror `SuperchargerRowUpdate`'s own
      `authorizeVehicle` error branch — same 404-not-403 reasoning
      `handlers.go`'s doc comment on `authorizeVehicle` states). On success,
      pass the returned `Ref` as `h.chargingWriter.Update`'s new second
      argument, ahead of `entry`.
- [ ] 6.2 `ExternalChargeRowDelete`: same shape. `entryTeslaID` is already
      captured (`entryTeslaID, entryChargedOn, hadEntry :=
      h.fetchEntryTeslaIDAndChargedOn(...)`). Call `h.authorizeVehicle(ctx,
      uid, entryTeslaID)` **only when `hadEntry` is true** — when the lookup
      missed, there is no vehicle to authorize against, and the existing
      "delete still proceeds on a miss" behaviour stays exactly as it is
      (that lookup-miss path is tier 3's, not this tier's, per `design.md`
      D8). When `hadEntry` is true and `authorizeVehicle` fails, render the
      same not-authorized handling as 6.1. When it succeeds, pass the `Ref`
      as `h.chargingWriter.Delete`'s new second argument.
- [ ] 6.3 `internal/gateway/handlers/external_charges_test.go`:
      `fakeChargeWriter.Update` and `fakeChargeWriter.Delete` re-sign to
      match `charging.Writer`'s new method signatures (add the
      `vehicleref.Ref` parameter to both; the fake does not need to inspect
      it — it is a fake, not the real guard). Add the `vehicleref` import if
      not already present.
- [ ] 6.4 Grep `internal/gateway` for every other `.chargingWriter.Update(` and
      `.chargingWriter.Delete(` call and every other type asserting
      `charging.Writer`. `design.md` §Modules affected states the full known
      list is `external_charges.go`'s two handlers and
      `external_charges_test.go`'s one fake — confirm the grep agrees, and
      flag anything it finds beyond that list rather than silently fixing it,
      since anything beyond this list was not scoped by this change.
- [ ] 6.5 `go build ./...` — expect clean across the whole repository.
- [ ] 6.6 `go vet ./...` — expect clean.
- [x] 6.7 **Appended after 6.1/6.2 landed.** Both handlers now answer 404
      when the entry lookup misses, because a miss leaves no vehicle to
      authorize and `Update`/`Delete` accept nothing but a `Ref`. Existing
      gateway tests were written against the old behaviour: they call the
      update/delete route with an empty `fakeChargeReader{}` and expect the
      write to proceed. Seed the reader with an entry whose `ID` matches the
      one the request names and whose `TeslaID` is `1001` — the id
      `newHandlerForExternalCharges`'s `fakeAccount` already registers — so
      the lookup hits and `authorizeVehicle` succeeds. Fix only the tests the
      new behaviour breaks. Do NOT weaken an assertion to make a test pass,
      and do NOT change handler code to suit a test.

## T7 — Cross-module-aware docs (module-owned, depends on T6)

Depends on: T6 (these two files describe the gateway flow T6 adds — writing
them before T6 exists would describe code that is not there).

- [ ] 7.1 `kkpa/context/use-case/charging/update-manual-charge.md` — insert a
      new flow step between the existing step 5
      (`fetchEntryTeslaIDAndChargedOn`) and step 6 (`charging.Writer.Update`):
      `Handler.authorizeVehicle` proving the entry's own `tesla_id`,
      producing the `Ref` the next step now requires. Renumber the steps
      after it.
- [ ] 7.2 `kkpa/context/use-case/charging/delete-manual-charge.md` — same
      shape: insert a step between the existing step 4
      (`fetchEntryTeslaIDAndChargedOn`) and step 5 (`Writer.Delete`).
      Rewrite step 5's text — it currently says "double-scoped `id AND
      created_by_account_id` (transitional...)" — to state the `tesla_id`/
      `Ref` guard, and remove the "transitional" framing.
- [ ] 7.3 `kkpa/context/architecture/charge-record-mutation.md` — the
      "Different ownership vocabulary, same shape now" bullet's premise (a
      remaining difference between the manual and Supercharger ownership
      checks) is gone once T6 lands: both now call `authorizeVehicle` and
      get a `Ref`. Restate the bullet as resolved — mirroring how
      `ai/architecture.md` §7 records the telemetry-boundary guard as
      "RESOLVED" rather than deleting its own history — or delete it if the
      leader prefers; either is acceptable, note which was chosen.
- [ ] 7.4 Do NOT edit anything under `openspec/changes/archive/` or
      `kkpa/context/pending-spec-to-sync/applied/`.

## T8 — Final verification

Depends on: T0–T7.

- [ ] 8.1 `go build ./...` — clean.
- [ ] 8.2 `go vet ./...` — clean.
- [ ] 8.3 `gofmt -l .` — no output.
- [ ] 8.4 `make vehicleref-guard` — clean, and confirm the output does not
      list any new call to `vehicleref.Authorize`/`vehicleref.All` in a
      non-`_test.go`, non-`internal/vehicleref`, non-`authorizeVehicle` file.
- [ ] 8.5 `make boundary-guard`, `make tz-guard`, `make money-guard`, `make
      i18n-guard`, `make ui-guard` — clean (all unaffected by this change,
      per `design.md` §Makefile).
- [ ] 8.6 `make archive-guard` — clean.
- [ ] 8.7 Grep the whole repository for `.Update(ctx` and `.Delete(ctx`
      resolving to `charging.Writer` outside `internal/charging` and
      `internal/gateway`. Expect zero hits — confirms `design.md` §Modules
      affected's claim that `internal/analytics` is untouched.
- [ ] 8.8 Re-read `design.md` §Docs this change invalidates top to bottom and
      confirm every row was actually done (T5, T7) or explicitly found
      unnecessary and stated why (5.5).
- [ ] 8.9 `openspec validate --strict` on this change — clean.
- [ ] 8.10 Do NOT run `go test ./...`, `make test`, `make test-with-db`, or
      `make check` — owner-only, per the Test-Execution-Policy. Hand the
      owner these exact commands to run:
      - `go test ./internal/charging/...`
      - `go test ./internal/gateway/...`
      - `go test ./...` (full suite, confirms `internal/analytics` truly
        needed no change)
