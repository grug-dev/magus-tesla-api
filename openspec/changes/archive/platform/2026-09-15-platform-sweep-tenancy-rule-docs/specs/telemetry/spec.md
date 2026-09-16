## MODIFIED Requirements

### Requirement: Supercharger History Table Renamed
The table previously named `supercharger_sessions` and owned by this module SHALL be renamed
to `supercharger_history` in the same migration that moves it into the `telemetry` schema.
The old name over-claimed kinship with `vehicle_snapshots`: `vehicle_snapshots` is
append-only, one row per poll, whereas this table is **upserted** — Tesla settles fees after
a session ends, so a row is never final on first insert. `_history` also names the Fleet API
endpoint the rows come from. The name is additionally freed for correctness reasons outside
this module: the charging module already holds a table named `supercharger_sessions`, and two
live tables one schema apart sharing a base name is the ambiguity this rename retires.

Every row SHALL survive unchanged, and every constraint and index SHALL preserve its exact
prior definition, column set and column order. EVERY catalog object still carrying the old
table name SHALL be renamed to follow it — the primary key, the `session_id` unique
constraint, the five CHECK constraints PostgreSQL auto-named from inline column constraints,
and the two standalone indexes. **The completeness criterion is the catalog, not a list**:
after this migration no relation, index or constraint owned by this module SHALL have a name
beginning `supercharger_sessions`. PostgreSQL renames none of these automatically, and a
constraint name is read in exactly one place — an error message, under pressure — so a
surviving old name would print retired vocabulary against a table the whole system calls
`supercharger_history`.

The module's own domain type SHALL be renamed from `SuperchargerSession` to
`SuperchargerHistory`, and the sqlc-generated model for the table SHALL follow the table's new
name with an identical field list — a deliberate exception to the general schema-move-
preserves-Go-names rule, because the rename's whole purpose is to retire the old vocabulary
everywhere, including in the code that reads it most. The five sqlc query names that embedded
the old table name SHALL be renamed to name `SuperchargerHistory` instead, with no change to
any query's parameters, predicates, ordering or column effects.

**The public port's deliberate half-renamed state is now RETIRED, and the port has since
narrowed to ONE method.** The port interface previously named `SuperchargerReader` and its
constructor `NewSuperchargerReader` SHALL be renamed to match the domain type and table they
operate on: `SuperchargerReader` → `SuperchargerHistoryReader`; `NewSuperchargerReader` →
`NewSuperchargerHistoryReader`. The rename also covered the port's methods at the time, each
renamed from a `SuperchargerSessions*` name to the matching `SuperchargerHistory*` name — but
three of those four renamed names describe methods that are gone today, for two different
reasons stated plainly here so neither is mistaken for a currently-callable method:

- The rename table of the time listed `SuperchargerSessionsByAccount` renaming to
  `SuperchargerHistoryByAccount` — that target name never existed as a real Go symbol anywhere
  in the repository; the rename table was aspirational for that one entry, and no code ever
  called it under either name.
- `SuperchargerHistoryByVehicle` (renamed from `SuperchargerSessionsByVehicle`) and
  `SuperchargerHistoryByVehicleBetween` (renamed from `SuperchargerSessionsByVehicleBetween`)
  did exist after the rename, but were later DELETED as dead code: no caller outside
  `internal/telemetry` itself ever called either one.

The port SHALL now expose exactly ONE method, `SuperchargerHistoryByVehicleUpdatedSince`
(renamed from `SuperchargerSessionsByVehicleUpdatedSince`) — the nightly Supercharger mirror's
only read (`internal/app/processor.go`) — with its signature, predicate, ordering and returned
data unchanged by either the rename or the later deletion of its three now-gone siblings.

A same-named field belonging to another module (`internal/gateway`'s `Deps.SuperchargerReader`,
typed `charging.SessionReader`) is explicitly OUT OF SCOPE for this requirement and SHALL NOT
be renamed — it is a different, unrelated identifier that happens to share a name, and
`internal/gateway` SHALL continue to be forbidden from importing `internal/telemetry` at all
(the pre-existing "Exception: the gateway may not depend on `telemetry` at all" requirement is
unaffected by this rename).

#### Scenario: The renamed table resolves and the old name does not
- **GIVEN** this change's migration has been applied
- **WHEN** the database catalog is queried for `telemetry.supercharger_history`
- **THEN** it resolves to the same table previously identified as
  `public.supercharger_sessions`, by unchanged primary key values on every existing row
- **AND** neither `public.supercharger_sessions` nor `telemetry.supercharger_sessions`
  resolves to a relation

#### Scenario: No catalog object survives under the old table name
- **GIVEN** this change's migration has been applied
- **WHEN** the database catalog is queried for the constraints and indexes on
  `telemetry.supercharger_history`
- **THEN** the primary key, the `session_id` unique constraint, the five auto-named column
  CHECK constraints, and the two standalone indexes all carry names beginning
  `supercharger_history`
- **AND** each preserves the exact definition it had under its old name
- **AND** a catalog query for any constraint or index name beginning `supercharger_sessions`
  within this module's schema returns no rows

#### Scenario: The renamed Go type carries an identical field set
- **GIVEN** the module's generated database models have been regenerated against this
  change's migration
- **WHEN** the generated `SuperchargerHistory` struct is compared to the pre-change
  `SuperchargerSession` struct
- **THEN** every field name, type and declaration order is identical — only the struct's own
  type identifier changed

#### Scenario: The public port exposes one method, the bounded per-vehicle read
- **GIVEN** a caller of telemetry's Supercharger read port
- **WHEN** this change is applied
- **THEN** the port is named `SuperchargerHistoryReader`, its ONE method is named
  `SuperchargerHistoryByVehicleUpdatedSince`, and its constructor is named
  `NewSuperchargerHistoryReader`
- **AND** that method's parameter list, predicate, ordering and returned element type
  (`SuperchargerHistory`) are byte-identical to the pre-rename port's equivalent method
- **AND** the data returned by every call is identical to what the same call returned before
  the rename
- **AND** no name on the public port still contains the word "Session" in reference to this
  table (the old `SuperchargerSession*` vocabulary is fully retired from this port)
- **AND** the port defines no `SuperchargerHistoryByAccount`, `SuperchargerHistoryByVehicle` or
  `SuperchargerHistoryByVehicleBetween` method — the first never existed as a Go symbol, and
  the other two were deleted as dead code with no production caller

#### Scenario: A real consumer outside the module is broken only by the identifier, and visibly
- **GIVEN** code outside `internal/telemetry` that names the type `telemetry.SuperchargerReader`
  or calls one of its old method names
- **WHEN** this change is applied without that code being updated
- **THEN** that code fails to compile, rather than compiling against a silently different type
  or method
- **AND** the failure is reported by the standard Go build and vet signals, which compile test
  files as well as production files

#### Scenario: An unrelated, same-named identifier in another module is left untouched
- **GIVEN** `internal/gateway`'s `Deps.SuperchargerReader` field, typed `charging.SessionReader`
  and wired from `cmd/web/main.go` via `charging.NewSessionReader(pool)`
- **WHEN** this change is applied
- **THEN** that field's name, type, and wiring are completely unchanged
- **AND** `internal/gateway` still does not import `internal/telemetry` anywhere, verified by
  `make boundary-guard` continuing to pass with zero `// boundary:allow:` escape hatches
