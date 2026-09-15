# Design — RM58-charging-entry-writes-take-ref

Source ticket: MAG-68. Roadmap tier 2.
This change touches no database object, but it changes two public port signatures
and a query's `WHERE` clause, so this file still carries the full test contract
(unit tests are excluded) and a checked list of what `make sqlc` produces.

## Overview

`Writer.Update` and `Writer.Delete` stop trusting an account value and start
requiring a `vehicleref.Ref` — a value only `internal/gateway`'s
`authorizeVehicle` can build. Four things move together:

1. `Update` gains a `vehicleref.Ref` parameter. `Delete`'s `accountID
   uuid.UUID` parameter becomes a `vehicleref.Ref`.
2. `UpdateEntry` and `DeleteEntry` swap their `WHERE ... AND
   created_by_account_id = @created_by_account_id` predicate for `WHERE ... AND
   tesla_id = @tesla_id`, the `tesla_id` unwrapped from the `Ref`.
3. `DeleteEntry` becomes `:execrows`. `writerService.Delete` reports
   `pgx.ErrNoRows` when zero rows matched — the same not-found shape `Update`
   already has from `RETURNING *`.
4. `Update` overwrites `e.TeslaID` from the `Ref` before anything else runs, so
   energy derivation (`resolveEnergy` → `packCapacityKWh`) reads the proven car,
   never whatever the caller sent.

`Create` is untouched. It still takes a plain `Entry` and still writes
`e.CreatedByAccountID` — authorship, not a guard, so nothing here demands a
`Ref` for it (roadmap decision, restated in §D6).

The database is untouched: no table, column, index, constraint, view, or
migration. See §Schema.

---

## Decisions

### D1 — The type is the real guarantee; the SQL guard is the second line

`vehicleref.Ref`'s field is unexported. The only way to produce one is
`vehicleref.Authorize` or `vehicleref.All`, and `make vehicleref-guard` allows
calling either only inside `internal/vehicleref` itself, in `_test.go` files,
and in the gateway's `authorizeVehicle`. So a handler that never checked
ownership has no `Ref` to pass — the mistake is a compile error, not a runtime
check a future change can forget to call.

That is the real guarantee. `WHERE id = $1 AND tesla_id = $2` is the second
line of defence, for the one case the type cannot rule out: a genuine `Ref`,
proven for the right vehicle, applied to the wrong row — an id typo, a stale
id from before a delete, a race with another tab. Dropping the `WHERE`
predicate once the `Ref` is required would still be memory-safe in Go, but it
would let any id under a vehicle the caller owns collide with a row that
belongs to a different vehicle if the two ever shared an id space by
accident. The clause costs one SQL predicate and removes that class of bug
entirely — this is exactly the reasoning `session_verifier.go`'s
`VerifySession` already applies for `supercharger_sessions`, which this
change mirrors on the manual-entry writes.

Rejected alternative — **keep only the `Ref`, drop the `WHERE` predicate**.
Rejected: it is a real weakening, however small, for a two-line cost, and it
breaks the pattern every other proven-vehicle write in this module already
follows.

### D2 — `Update`'s `Ref` sits between `ctx` and the entry, mirroring `VerifySession`

`VerifySession(ctx context.Context, ref vehicleref.Ref, id uuid.UUID, ...)` is
this module's only existing precedent for a port that takes a `Ref`. `Update`
and `Delete` follow its parameter order exactly:

```go
Update(ctx context.Context, ref vehicleref.Ref, e Entry) (Entry, error)
Delete(ctx context.Context, ref vehicleref.Ref, id uuid.UUID) error
```

Rejected alternative — **add a `Ref` field to `Entry` instead of a parameter**.
Rejected: `Entry` is also `Create`'s input, which takes no `Ref` (§D6) — adding
the field there would make it meaningful on two of three `Writer` methods and
silently ignored on the third, exactly the shape this project's "module-owned
field, ignored when supplied" comments exist to warn against, except here the
field would not even be module-computed, just unused. A parameter says "this
method needs a proven vehicle" where the signature is read, not where the
struct is read.

### D3 — `Update` overwrites `e.TeslaID` from the `Ref` before any other work

`resolveEnergy(ctx, s store, e Entry)` — called from inside `Update` — reads
`e.TeslaID` to resolve pack capacity (`packCapacityKWh(ctx, s, e.TeslaID)`,
`service.go:297`). If `Update` bound `params.TeslaID` from `ref.TeslaID()` for
the `WHERE` clause but left `e.TeslaID` as whatever the caller sent, the two
could disagree: the row would be correctly guarded, but energy would be
derived against a capacity that belongs to a different car than the one being
written.

The fix is the first line of the method: `e.TeslaID = ref.TeslaID()`, before
`normalizeStatus`, before `resolveEnergy`, before anything else touches `e`.
Every later read of `e.TeslaID` — in `resolveEnergy` and in the params literal
— then sees the same proven value the `WHERE` clause uses. This is the same
"module owns this value, a caller's value is overwritten" shape
`EnergySource` and `PriceSource` already follow on both `Create` and `Update`;
the difference is that those two are always overwritten, while `TeslaID` is
overwritten only on `Update` — `Create` has no `Ref` to overwrite it from
(§D6), so `Create`'s `e.TeslaID` stays caller-supplied and trusted, exactly as
today.

Rejected alternative — **bind the `WHERE` clause from `ref.TeslaID()` directly,
leave `e.TeslaID` alone**. Rejected: it fixes the `WHERE` clause but not
`resolveEnergy`, which is the actual bug this decision closes. It also means
two different values both called "this entry's vehicle" exist inside one
function call, which is the kind of drift this project's field-ownership
comments exist to prevent.

### D4 — `DeleteEntry` becomes `:execrows`; `Delete` reports `pgx.ErrNoRows` on a miss

Today `DeleteEntry` is `:exec` — sqlc generates `func (q *Queries) DeleteEntry(ctx
context.Context, arg DeleteEntryParams) error`, and a `WHERE` clause matching
zero rows is not an error; the row is simply still there. That was acceptable
while the predicate was a soft authorial guard (tier 1). It is not acceptable
now: the predicate is the authorization boundary, and "nothing happened, no
error" is exactly the shape that lets a caller mistake a rejected delete for
a successful one.

`:execrows` generates `func (q *Queries) DeleteEntry(ctx context.Context, arg
DeleteEntryParams) (int64, error)` — the `pgx.CommandTag.RowsAffected()` value
sqlc/pgx already compute internally, now returned instead of discarded.
`writerService.Delete` checks it: zero rows becomes an error wrapping
`pgx.ErrNoRows`, matching exactly the not-found shape `Update` already has —
`UpdateEntry` is `:one`, and pgx's `QueryRow`+`Scan` already returns
`pgx.ErrNoRows` when `RETURNING *` finds nothing. After this change both
writes fail the same recognizable way on a vehicle mismatch, where today only
`Update` did.

**Behaviour change to state plainly.** Today, a delete naming the wrong
account returns `nil` and the row survives silently. After this change, a
delete naming the wrong vehicle returns an error wrapping `pgx.ErrNoRows` and
the row survives. `internal/gateway/handlers/external_charges.go`'s
`ExternalChargeRowDelete` already treats a `Delete` error as a rendered error
message (`h.chargingWriter.Delete` → `err != nil` → `i18n.KeyChargesErrorCouldNotDeleteEntry`),
so no gateway change is needed to handle it — the existing error branch now
also covers this case. This is the roadmap's own D9.

Rejected alternative — **keep `:exec`, have the gateway re-check with a
`GetEntry` call before deleting**. Rejected: this module has no `GetEntry`
port (a known, separately tracked gap — see
`kkpa/context/use-case/charging/delete-manual-charge.md`'s "100-row lookup
cap" note), adding one is out of scope here, and it would still leave the
port itself silently accepting a mismatched delete for any caller that skips
the extra check.

### D5 — The read ports are untouched

`Reader`'s four methods keep their plain `teslaID int64` / `teslaIDs []int64`
parameters. `internal/analytics` calls three of them
(`ListEntriesByVehicle`, `ListEntriesByVehicleBetween`,
`ListEntriesByVehicleUpdatedSince`, per tier 1's own D3), and
`make vehicleref-guard` forbids building a `Ref` anywhere outside
`internal/vehicleref`, `_test.go` files, and the gateway's `authorizeVehicle`
— `analytics` runs on the nightly path with no signed-in user and no `Ref` to
build. This change does not reopen that decision; it is restated here only so
a reader of this file does not wonder why the read ports look untouched next
to two retyped write ports.

### D6 — `Create` is untouched

`Create` keeps `Create(ctx context.Context, e Entry) (Entry, error)` and keeps
writing `e.CreatedByAccountID` on every row. The roadmap's own scope line says
so directly: `Create` "keeps the account to record authorship." Authorship is
not an authorization check — nothing needs to be proven about a vehicle
before recording who typed an entry, because creating a new row cannot
collide with an existing one the caller does not own. The `Ref` retype exists
to guard a write against an **existing** row; `Create` has no existing row to
guard.

### D7 — No query stops selecting `created_by_account_id` — the known sqlc trap does not fire here

The roadmap's own warning: `sqlc` emits a per-query `Row` struct when a query
stops **selecting** a column the table still has, and that stale codegen
still compiles. This change touches only `WHERE` clauses, never a `SELECT` or
`RETURNING` list.

- `UpdateEntry` keeps `RETURNING *` — unchanged. `created_by_account_id` is
  still in the result set; only the `WHERE` clause's second predicate moves
  from `created_by_account_id = @created_by_account_id` to `tesla_id =
  @tesla_id`.
- `DeleteEntry` returns no row today and returns none after this change
  either (`:execrows` returns a count, not a row) — there was never a `Row`
  type for it to lose.

So `chargingdb.ManualChargeEntry` is untouched by this change — every field,
including `CreatedByAccountID`, stays exactly as tier 1 left it. The only
generated shapes that move are the two write `Params` structs and
`DeleteEntry`'s own return type. See §What `make sqlc` will produce.

### D8 — The gateway wiring in this same tier is leader-owned and deliberately minimal

Making `go build ./...` pass outside `internal/charging` requires
`ExternalChargeRowUpdate` and `ExternalChargeRowDelete`
(`internal/gateway/handlers/external_charges.go`) to obtain a `Ref` before
calling the retyped ports. Both handlers already resolve the entry's own
`tesla_id` before the write — `fetchEntryVM` and
`fetchEntryTeslaIDAndChargedOn` both return it (tier 1's own shape, reading
over `acct.RegisteredVehicles`). The minimal wiring is: call
`h.authorizeVehicle(ctx, uid, entryTeslaID)` with that already-resolved value,
and hand the returned `Ref` to `Update`/`Delete`.

This already closes the authorization gap tier 1's own design.md named as a
known cost: after tier 1, reads were car-wide but writes were author-only, so
a co-owner's entry appeared in the External Charges page with edit/delete
controls that then failed silently or loudly depending on the method. After
this tier's gateway wiring, a co-owner's write succeeds, because
`authorizeVehicle` proves the account against its own registered vehicles,
and a car shared between two accounts is registered to both.

**What this tier's gateway wiring is not.** It does not re-feed
`fetchEntryVM`/`fetchEntryTeslaIDAndChargedOn` through `vehicleref.All` /
`vehicleref.TeslaIDs` — they keep reading over
`acct.RegisteredVehicles(ctx, uid)` exactly as tier 1 left them. It does not
touch `external_charges_tiles.go`. Those
three are roadmap tier 3's job
(`RM58-gateway-authorize-manual-charge-writes`), because they are about
*how* the gateway resolves the vehicle set it authorizes against, not about
whether a write is authorized at all — which this tier already settles.

**Correction, made while implementing.** An earlier draft of this section said
the entry-lookup miss path was unaffected, because `h.chargingWriter.Delete`
was never reached on a miss. That was wrong. `ExternalChargeRowDelete` called
`Delete` unconditionally, and its own comment said the delete still proceeded.
Once `Delete` requires a `Ref`, a miss leaves no vehicle to authorize, so the
old behaviour cannot continue. The owner chose 404 on a miss, for both
handlers: the lookup reads only over the account's registered vehicles, so a
miss means the entry is not this account's to touch. 404 and never 403, so a
probed id cannot be confirmed as real. This closes the silent no-op instead of
carrying it to tier 3. It broke existing gateway tests written against the old
behaviour — see `tasks.md` T6.7.

**Not this worker's sandbox.** `internal/gateway` is a different module. The
exact edit, file by file, is in `tasks.md` T6, marked leader-owned.

### D9 — No database object changes

No table, column, index, constraint, view, or migration is added, dropped, or
altered by this change. `charging.manual_charge_entries`'s shape is exactly
what tier 1 left it. This is a design-gate requirement
(`openspec/config.yaml` — "design" rules require this file for any DB-touching
change) stated explicitly so the reviewer can confirm it by reading this
sentence rather than diffing a migrations folder that does not gain a file.

If implementation turns up a reason a database object must change, that is a
STOP — report it as blocked, because a DB design needs the owner's
confirmation first, and this tier was never scoped to need one.

---

## Schema

**Unchanged.** No migration file is added by this change.

| Object | Shape today, and after this change |
|---|---|
| `manual_charge_entries_pkey` | `PRIMARY KEY (id)` — untouched |
| `idx_manual_charge_entries_vehicle_time` | `(tesla_id, charged_on DESC)` — untouched; it already serves `UpdateEntry`'s and `DeleteEntry`'s point lookup by `id` the same way it did before this change, because both queries still resolve the row by primary key first and evaluate the second predicate on that single row |
| `created_by_account_id` | Still `UUID NOT NULL`, still written by `CreateEntry`. Never read by `UpdateEntry` or `DeleteEntry` after this change — it drops out of both queries' `WHERE` clause and was never in either query's `SET`/column list to begin with |

No index plan changes: `UpdateEntry`/`DeleteEntry` resolve their row by the
primary key (`id`), and the `WHERE` clause's second predicate — whichever
column it names — is evaluated on that one already-located row, not through a
separate index scan. Swapping which column the second predicate names does
not change which index either query uses.

---

## Queries (`internal/charging/db/query.sql`)

| Query | Before | After |
|---|---|---|
| `UpdateEntry` | `:one`. `WHERE id = @id AND created_by_account_id = @created_by_account_id` | `:one`, unchanged annotation. `WHERE id = @id AND tesla_id = @tesla_id`. `RETURNING *` unchanged. Comment rewritten: the `Ref` is the real check (only `internal/gateway`'s `authorizeVehicle` can build one); this clause is the second line of defence against a right-`Ref`-wrong-row mistake. Do not name a tier or a decision id in the comment — write the reason itself. |
| `DeleteEntry` | `:exec`. `WHERE id = @id AND created_by_account_id = @created_by_account_id` | **`:execrows`**. `WHERE id = @id AND tesla_id = @tesla_id`. Comment rewritten: same guard as `UpdateEntry`; returns the row count so the caller can tell a real delete from a no-op. |
| Every other query | Unchanged | Unchanged |

### What `make sqlc` will produce — checked against §D7

| Generated symbol | After `make sqlc` |
|---|---|
| `chargingdb.ManualChargeEntry` | **Unchanged.** No field added, removed, or renamed. |
| `UpdateEntryParams` | Loses `CreatedByAccountID uuid.UUID`. Gains `TeslaID int64`. Every other field unchanged. The struct survives (it still has more than one field): `ID`, `TeslaID`, `ChargedOn`, `EnergyAddedKwh`, `Price`, `Currency`, `StartedAt`, `EndedAt`, `StartBatteryPct`, `EndBatteryPct`, `ChargingType`, `LocationKind`, `LocationLabel`, `Notes`, `Status`, `EnergySource`, `OdometerKm`, `PriceSource`. |
| `DeleteEntryParams` | `CreatedByAccountID uuid.UUID` becomes `TeslaID int64`. Still two fields (`ID`, `TeslaID`) — the struct survives, as tier 1's design.md flagged it might not once a tier reduced it to one parameter. It did not: `id` and `tesla_id` are both still needed. |
| `func (q *Queries) DeleteEntry(...)` | Return type changes from `error` to `(int64, error)` — the `:execrows` shape. Body changes from `_, err := q.db.Exec(...); return err` to `result, err := q.db.Exec(...); if err != nil { return 0, err }; return result.RowsAffected(), nil`. |
| `func (q *Queries) UpdateEntry(...)` | Unchanged shape: `(ManualChargeEntry, error)`. Only the bound arguments change (`arg.TeslaID` replaces `arg.CreatedByAccountID`). |
| Every read query's generated code | **Unchanged.** This change edits no `SELECT`. |

`make sqlc` must be run after the query edits, and the diff checked against
this table before the change is considered implemented.

---

## Ports and implementation

### `charging.go`

```go
// Writer is the full CRUD port for manual charge entries. Create keeps the
// account argument on Entry.CreatedByAccountID -- authorship, not a guard: no
// existing row can collide with a new one, so nothing needs proving before a
// create. Update and Delete instead require a vehicleref.Ref, proving the
// caller's account owns the entry's OWN vehicle -- only internal/gateway's
// authorizeVehicle can build one. Update overwrites Entry.TeslaID from the
// Ref before doing anything else (see the field's own comment), so every
// downstream derivation reads the proven car, never the caller's value.
// Create and Update return the stored Entry (with server-assigned id,
// created_at, updated_at) so the gateway can display the result without a
// second round-trip. A Ref naming the wrong vehicle for a given id matches no
// row: Update returns an error wrapping pgx.ErrNoRows (from RETURNING *
// finding nothing), and Delete reports the same, from a zero row count.
type Writer interface {
	Create(ctx context.Context, e Entry) (Entry, error)
	Update(ctx context.Context, ref vehicleref.Ref, e Entry) (Entry, error)
	Delete(ctx context.Context, ref vehicleref.Ref, id uuid.UUID) error
}
```

`Entry.TeslaID`'s own field comment (today undocumented) gains one:

```go
	// TeslaID is the vehicle this entry belongs to. On Create it is
	// caller-supplied and trusted -- the gateway already checked the vehicle
	// against the account before calling. On Update it is IGNORED: the
	// module overwrites it from the caller's vehicleref.Ref before anything
	// else runs, the same "module owns this value" rule EnergySource and
	// PriceSource already follow -- so a stale or mismatched caller value can
	// never leak into energy derivation or the stored row.
	TeslaID int64
```

`Entry.CreatedByAccountID`'s existing comment loses its "Update and Delete
still match on it" sentence — that stopped being true — and states plainly
that authorship is recorded on create and never touched again.

### `service.go`

- `import`: add `"github.com/cristianpena/magus-tesla-api/internal/vehicleref"`.
- `store` interface: `deleteEntry` changes from `(ctx, params) error` to `(ctx,
  params) (int64, error)`.
- `dbStore.deleteEntry`: returns `d.q.DeleteEntry(ctx, params)` directly — the
  generated method already returns `(int64, error)` after `make sqlc`, so no
  wrapping is needed here.
- `writerService.Update`:

  ```go
  func (w *writerService) Update(ctx context.Context, ref vehicleref.Ref, e Entry) (Entry, error) {
  	e.TeslaID = ref.TeslaID() // D3: the proven car wins over whatever the caller sent
  	status, err := normalizeStatus(e.Status)
  	// ... unchanged: promoteIfComplete, missingFields, resolveEnergy, numeric encoding ...
  	params := chargingdb.UpdateEntryParams{
  		ID:      e.ID,
  		TeslaID: e.TeslaID,
  		// CreatedByAccountID is gone from this struct (D7) -- do not bind it.
  		ChargedOn: dateFromTime(e.ChargedOn),
  		// ... every other field exactly as today ...
  	}
  	row, err := w.store.updateEntry(ctx, params)
  	if err != nil {
  		return Entry{}, fmt.Errorf("charging: update entry: %w", err)
  	}
  	return rowToEntry(row)
  }
  ```

- `writerService.Delete`:

  ```go
  func (w *writerService) Delete(ctx context.Context, ref vehicleref.Ref, id uuid.UUID) error {
  	params := chargingdb.DeleteEntryParams{
  		ID:      id,
  		TeslaID: ref.TeslaID(),
  	}
  	rows, err := w.store.deleteEntry(ctx, params)
  	if err != nil {
  		return fmt.Errorf("charging: delete entry: %w", err)
  	}
  	if rows == 0 {
  		return fmt.Errorf("charging: delete entry: %w", pgx.ErrNoRows)
  	}
  	return nil
  }
  ```

  `pgx` is already imported in `service.go` (`"github.com/jackc/pgx/v5"`), used
  today by `dbStore.latestMeasuredCapacity`'s `errors.Is(err, pgx.ErrNoRows)`
  check — no new import needed for this reference.

- Both methods' doc comments are rewritten to describe the `Ref`-pinned guard
  in place of the account-pinned one, mirroring the interface comment above.

Nothing else in `internal/charging` changes. `session_writer.go`,
`session_reader.go`, `session_verifier.go`, `mirror_watermark.go`,
`monthly_capacity.go`, `capacity.go`, `validation.go`, `mapping.go` are
untouched — none of them reference `Writer.Update`, `Writer.Delete`, or the
two changed queries.

---

## Test contract — unit tests are EXCLUDED

The ticket excludes unit tests. This change adds **no new test file and no new
test function**, except the two existing cross-guard tests being renamed
(same file, same package, new names because the axis they test changes from
account to vehicle). Every other existing test whose call site changed keeps
its name and its expectations, with one exception named below.

### Offline tests — unaffected

`charging_test.go`, `entry_status_test.go`,
`monthly_capacity_estimator_test.go`, `price_source_test.go`,
`session_verifier_derivation_test.go` call neither `Update` nor `Delete` —
checked by grep, zero hits. No pure function's behaviour changes here.

### A shared test helper is needed: `refFor`

Every `_test.go` file below calls `w.Update` or `w.Delete` and must now supply
a `vehicleref.Ref`. `vehicleref.All` is exported precisely so a caller that
already trusts its own vehicle id can build one without a lookup, and
`make vehicleref-guard` allows calling it from any `_test.go` file. Add one
small helper next to `minEntry` in `db_integration_test.go` (package
`charging_test`, so every other `_test.go` file in the package sees it):

```go
// refFor builds a vehicleref.Ref for teslaID, for tests that must supply one
// to Writer.Update / Writer.Delete. vehicleref.All is exported for exactly
// this: a caller that already trusts its own vehicle id needs a Ref without
// a lookup, and make vehicleref-guard allows calling it from any _test.go
// file.
func refFor(teslaID int64) vehicleref.Ref {
	return vehicleref.All([]int64{teslaID})[0]
}
```

`db_integration_test.go` gains one new import:
`"github.com/cristianpena/magus-tesla-api/internal/vehicleref"`.

### Mechanical call-site updates — no expectation changes

Every one of these calls passes an entry (or an id) for a vehicle the test
itself created, so the `Ref` must name that same vehicle. The rule is
mechanical: insert `refFor(<teslaID>)` as the argument right after `ctx`,
where `<teslaID>` is the `teslaID` local the test already has in scope (a
named constant, a loop variable, or `created.TeslaID`/`tampered.TeslaID` —
whichever the call site already uses to identify the entry). No assertion in
any of these tests changes.

| File | Line(s) (today) | Call |
|---|---|---|
| `db_integration_test.go` | 362 | `w.Update(ctx, updated)` → `w.Update(ctx, refFor(teslaID), updated)` |
| `db_integration_test.go` | 463 | `w.Delete(ctx, accountID, created.ID)` → `w.Delete(ctx, refFor(teslaID), created.ID)` |
| `db_integration_test.go` | 1033 | `w.Update(ctx, tampered)` → `w.Update(ctx, refFor(1006), tampered)` |
| `db_integration_test.go` | 1356 | `w.Update(ctx, updated)` → `w.Update(ctx, refFor(1007), updated)` |
| `db_inferred_capacity_entries_integration_test.go` | 183 | `w.Update(ctx, updated)` → `w.Update(ctx, refFor(teslaID), updated)` |
| `db_promotion_price_source_integration_test.go` | 260 | `w.Update(ctx, updated)` → `w.Update(ctx, refFor(340006), updated)` |
| `db_promotion_price_source_integration_test.go` | 322 | `w.Update(ctx, updated)` → `w.Update(ctx, refFor(340008), updated)` |
| `db_promotion_price_source_integration_test.go` | 418 | `w.Update(ctx, updated)` → `w.Update(ctx, refFor(340011), updated)` |
| `db_entry_status_integration_test.go` | 652 | `w.Update(ctx, updated)` → `w.Update(ctx, refFor(320015), updated)` |
| `db_entry_status_integration_test.go` | 689 | `w.Update(ctx, updated)` → `w.Update(ctx, refFor(320016), updated)` |
| `db_entry_status_integration_test.go` | 725 | `w.Update(ctx, tampered)` → `w.Update(ctx, refFor(320017), tampered)` |
| `db_entry_status_integration_test.go` | 767 | `w.Update(ctx, updated)` → `w.Update(ctx, refFor(320018), updated)` |

These line numbers are for locating the calls today; they will drift as the
`refFor` helper and any renamed tests are added above them in the same file.
Locate by the `func Test...` name in the table's neighbourhood, not by line
number.

### The two guard tests — renamed AND behaviour-changed

**`TestUpdate_CrossAccountIsNoOp` → `TestUpdate_CrossVehicleIsNoOp`.**

- **Given:** an entry created for `teslaID = 333` (unchanged from today).
- **When:** `w.Update(ctx, refFor(999), tampered)` — a `Ref` for a DIFFERENT
  vehicle (`999`) than the entry's own (`333`); `tampered.Price = 1.0`.
- **Then:** a non-nil error (still `pgx.ErrNoRows` via `RETURNING *` finding
  zero rows — the mechanism is unchanged, only which column fails to match
  is). The owner's row, read back via `ListEntriesByVehicle(ctx, 333, 10)`,
  is unchanged: still 1 entry, `Price` still the original value.
- **What does NOT change:** the assertion shape. Only `tampered.CreatedByAccountID
  = attackerID` is replaced by a `Ref` for the wrong vehicle, and the doc
  comment above the test is rewritten to describe a vehicle mismatch instead
  of an account mismatch.

**`TestDelete_CrossAccountGuard` → `TestDelete_CrossVehicleGuard`.**

- **Given:** an entry created for `teslaID = 555` (unchanged from today).
- **When:** `w.Delete(ctx, refFor(998), created.ID)` — a `Ref` for a
  DIFFERENT vehicle (`998`) than the entry's own (`555`).
- **Then — BEHAVIOUR CHANGE (D4):** `err` is now **non-nil**, wrapping
  `pgx.ErrNoRows` (`errors.Is(err, pgx.ErrNoRows)` must be true). Today this
  call returns `nil`. The owner's row, read back via
  `ListEntriesByVehicle(ctx, 555, 10)`, is unchanged: still 1 entry — the
  guard still holds, only how the caller learns the write was rejected
  changes.
- Assert the error with `errors.Is`, not a string match — the wrapped error is
  `pgx.ErrNoRows`, imported the same way `dbStore.latestMeasuredCapacity`
  already imports and checks it in `service.go`.

Both renamed tests keep every other line unchanged (setup, cleanup,
`newTestPool`). They are the proof that no commit on this branch has an
unauthorized write path — exactly the role tier 1's design.md assigned them,
now discharged.

---

## Docs this change invalidates

| File | What is wrong after this change |
|---|---|
| `internal/charging/AGENTS.md` | §Public Interface's whole "transitional" paragraph about `Writer.Update`/`Delete` scoping by `CreatedByAccountID` — replace with the `vehicleref.Ref` guard. §Allowed Imports' `internal/vehicleref` line, which names only `charging.go` and `session_verifier.go` — add `service.go`. §Data Ownership's "`manual_charge_entries` writes ... are the one exception: they still predicate on `created_by_account_id`, transitionally" sentence — the exception is gone; state the `tesla_id` guard instead. |
| `openspec/specs/manual-charge-log/spec.md` | Four requirements modified — this change's delta under `specs/manual-charge-log/spec.md` below. |
| `kkpa/context/architecture/charging-tables.md` | **Unaffected — checked.** This file's `manual_charge_entries` section already describes the `tesla_id`-keyed shape tier 1 left; no index, column, or constraint changes in this tier. |
| `kkpa/context/workflows/manual-charge-crud.md` | The `query.sql` row (line ~40) says the `created_by_account_id` guard on `Update`/`Delete` is transitional, pending a later tier. The Update flow bullet (line ~66) says the same, and names the guard `WHERE id AND created_by_account_id`. The "write guard is transitional, and it is narrower than the read" gotcha (line ~198) describes the exact gap this tier closes. All three need the `tesla_id`/`Ref` guard and the "no longer transitional" statement. The authorship paragraph (line ~190) says "No read may filter... by it" — extend to "No read or write". |
| `kkpa/context/use-case/charging/update-manual-charge.md` | Flow step 6 ("`charging.Writer.Update`... `normalizeStatus` → `missingFields` → `resolveEnergy` → `UpdateEntry`") needs a new step between the existing step 5 (`fetchEntryTeslaIDAndChargedOn`) and it: `Handler.authorizeVehicle` proving the entry's own `tesla_id`, producing the `Ref` step 6 now requires. This step depends on tier 3's gateway wiring (`tasks.md` T6, leader-owned) actually existing before the doc can describe it truthfully — see `tasks.md` T7's dependency note. |
| `kkpa/context/use-case/charging/delete-manual-charge.md` | Same shape: a new step between the existing step 4 (`fetchEntryTeslaIDAndChargedOn`) and step 5 (`Writer.Delete`), and step 5's own text ("double-scoped `id AND created_by_account_id` (transitional...)") needs the `tesla_id`/`Ref` guard. Same T6-dependency note as above. |
| `kkpa/context/architecture/charge-record-mutation.md` | The "Different ownership vocabulary, same shape now" bullet (line ~136) says the manual handlers call `acct.RegisteredVehicles` + `vehicleOwned` while only the Supercharger path uses `authorizeVehicle`'s typed `Ref`. After T6 lands, both paths use `authorizeVehicle` + `Ref` — the bullet's whole premise (a remaining difference) is gone; either delete the bullet or restate it as resolved, mirroring how §7 of `ai/architecture.md` records the telemetry-boundary guard as "RESOLVED" rather than deleting its own history. |

`openspec/changes/archive/` is **not** swept, per `ai/go-conventions.md`'s own
rule — several archived tier-1 documents describe the transitional guard this
tier removes, and that is the record of what was true then. `make
archive-guard` enforces it.

---

## Makefile, guards and codegen — checked, with findings

| Thing checked | Finding |
|---|---|
| `MIGRATIONS_DIRS` order | **Not applicable.** This change adds no migration. |
| `make migration-guard` | **Not applicable**, for the same reason — nothing to check a version number against. |
| `sqlc` inputs | `sqlc.yaml`'s charging entry is unaffected — same `schema:`, same `queries:`, same `rename:` table mapping (a table-level mapping, untouched by a `WHERE`-clause or annotation change). Run `make sqlc` after the query edits; diff checked against §What `make sqlc` will produce. |
| `make vehicleref-guard` | **Checked — passes.** This change adds no call to `vehicleref.Authorize` or `vehicleref.All` in any production file. `service.go` only accepts the `vehicleref.Ref` type as a parameter and calls its exported `.TeslaID()` method — exactly what `session_verifier.go` already does, and the guard does not restrict either. |
| `make boundary-guard` | Unaffected — no `internal/telemetry` import touched. |
| `make tz-guard`, `money-guard`, `i18n-guard`, `ui-guard` | Unaffected — no time-zone call, no monetary column, no user-facing string, no template. |
| `make db-setup` / `db-setup-test` / `db-reset` | Unaffected — no schema change. |
| `make archive-guard` | Nothing under `openspec/changes/archive/` is edited by this change. |

---

## Risks

- **`Update` and `Delete` change their exported signature.** Every caller
  outside `internal/charging` breaks until it is updated. Inside this module
  the break is fixed in the same change (T2–T5). Outside it, `internal/gateway`
  breaks in two places: the production call sites
  (`ExternalChargeRowUpdate`/`ExternalChargeRowDelete`, T6, leader-owned) and
  a test double, `fakeChargeWriter` in
  `internal/gateway/handlers/external_charges_test.go`, whose `Update` and
  `Delete` methods must be re-signed to keep implementing `charging.Writer`
  (also T6 — a fake implementing a changed interface is exactly as leader-owned
  as the production call site, because both live in `internal/gateway`).
  `internal/analytics` is unaffected: it calls only `Reader` and `Create`
  (seeding, in its own tests), neither of which changes here.
- **The build is red outside `internal/charging` until T6 lands.** Mitigation:
  T6 names every file and the exact edit, same discipline tier 1's T6 used.
  Run it in the same wave as the module implementation, not a later one.
- **`Delete`'s behaviour changes for an existing caller that relied on
  silence.** Today a mismatched delete is a silent no-op; after this change it
  is an error. `ExternalChargeRowDelete` already has an error branch that
  renders `KeyChargesErrorCouldNotDeleteEntry` — checked, and it is reachable
  from this new error path with no code change (D4). No other caller of
  `Writer.Delete` exists in the repository (checked — see `proposal.md`
  §Modules affected).
- **A test asserting `err == nil` on the old cross-account delete would now
  fail if left unchanged.** This is exactly `TestDelete_CrossAccountGuard`,
  and it is renamed and rewritten to assert the opposite, in this same change
  (§Test contract). No other test asserts a nil error from `Writer.Delete`
  under a mismatch — checked by reading every `.Delete(ctx` call site in
  `internal/charging/*_test.go` (§Test contract's table is the full list).
