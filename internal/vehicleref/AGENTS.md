# Vehicleref Sub-Agent

Agent-Name: `vehicleref`

Per-module instructions for `internal/vehicleref/` — merged with the global rules
(`CLAUDE.md`, `ai/*.md`) by any assistant working here (see `ai/agentic-workflow.md`).

## Doc-Pack (module)

Extends the project base Doc-Pack (`CLAUDE.md` → "Pipeline config") — never replaces it.
A dispatched worker/reviewer reads: base pack + this list + this file, before any write.

*(empty — `internal/vehicleref` needs nothing beyond the base pack)*

## Responsibility

Proves vehicle ownership. It turns "an account's own vehicle list" and "one requested
vehicle id" into a value that a handler cannot build unless the check already passed. A
handler that never calls this package has nothing to pass to a module port that requires
the proof — the mistake fails to compile instead of failing a runtime check nobody
noticed. It is a pure, in-memory function library — no table, no query, no config, no
external state.

## Public interface (the module's port)

Consumers call these functions and the one method directly — this module has no data to
protect behind a service interface (see `internal/vehicleref/vehicleref.go`):

- `type Ref struct { teslaID int64 }` — proof that one vehicle id was checked. The field
  is unexported, so a `Ref` can only come from `Authorize` or `All`. A bare
  `vehicleref.Ref{}` compiles (the zero value), but it carries no proof and must never be
  treated as one.
- `Authorize(owned []int64, want int64) (Ref, bool)` — checks whether `want` is in
  `owned`. Returns a `Ref` and `true` on a match, the zero `Ref` and `false` otherwise
  (including when `owned` is empty). Callers must check the bool.
- `All(owned []int64) []Ref` — wraps every id in `owned` into a `Ref`, unchecked, in
  order. For the case where the caller already holds its own resolved ownership list and
  needs a `Ref` per vehicle.
- `TeslaIDs(refs []Ref) []int64` — the inverse of `All`. Unwraps a `[]Ref` back to
  `[]int64`, in order, for building a SQL `= ANY($1)` parameter.
- `(Ref).TeslaID() int64` — the only way to read the wrapped id back out.

## Why not `internal/account`

`internal/charging` may not import `internal/account` — a rule already settled for that
module. If this type lived in `account`, a `charging` port could never accept it. A type
every module needs to accept lives in its own package, not inside one domain module.

## Data ownership

None. No table, no migration, no `db/` package, no config, no env var, no external state
of any kind. This module cannot create an import cycle with any other module because it
imports nothing project-local.

## Boundaries

- Does not read config, environment variables, or any other module's internals.
- Only the gateway's `authorizeVehicle` helper (in
  `internal/gateway/handlers/handlers.go`) calls `Authorize` or `All` outside this
  package's own code. `make vehicleref-guard` enforces this — read its grep pattern in
  the `Makefile` before adding a `vehicleref:allow` marker.

## Testing

- Pure, offline, table-driven tests only — no DB, no container, no network, no test
  fixture seeding. `vehicleref_test.go` covers `Authorize` (hit, miss, empty-owned miss),
  `All` (normal case, nil case), and `TeslaIDs` (round-trip with `All`, nil case, and
  refs built one-by-one via `Authorize`). Expected values are transcribed verbatim from
  `design.md`'s Test Contract, authored before the implementation existed
  (`ai/go-conventions.md` §Testing "author their expected values up front").
