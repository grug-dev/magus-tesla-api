# Sync proposal — account-vehicle-registry

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `architecture/schema-per-module.md`
Source spec:  `openspec/specs/account-vehicle-registry/spec.md`
Generated:    2026-09-02
Status: APPLIED 2026-09-03

> **Curator's routing note.** This capability's new requirement is the same cross-cutting
> invariant as the `account` capability's, applied to the `vehicles` table. It therefore appends
> to the SAME architecture guide rather than opening a second one. **Apply `account.md` first** —
> it is the proposal that creates the guide. This one only adds what is specific to the registry.
> The blocks here deliberately do NOT repeat the shared conventions; duplicating them would put
> the same rule in one file twice.

---

## [guide] ## Conventions & gotchas — APPEND

- **`vehicles` lives in the `account` schema, not a `vehicle` one.** The registry has its own
  OpenSpec capability but no module of its own — `internal/account` owns it. Schemas follow module
  ownership, not capability boundaries, so the table sits beside `accounts` and `tesla_tokens`.
  _Source: spec account-vehicle-registry — Requirement: Module-Scoped Database Schema._
- **The registry's constraints are the concrete proof that the move is catalog-only.** After the
  schema move, `UNIQUE (account_id, tesla_id)`, the `access_type` and `status` check constraints,
  the foreign key to `accounts`, and the `vin` index all continue to be enforced exactly as
  before, and every stored `access_type` / `exterior_color` / `car_type` / `status` value is
  preserved. _Source: spec account-vehicle-registry — Requirement: Module-Scoped Database Schema._

## [index] ## Architecture — ADD ROWS

| `vehicles table schema` | `account.vehicles` — the registry lives in its owning module's schema → `architecture/schema-per-module.md` |
