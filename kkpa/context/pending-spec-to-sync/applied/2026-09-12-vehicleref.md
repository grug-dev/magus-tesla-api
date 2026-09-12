# Sync proposal — vehicleref

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `architecture/vehicle-ownership-proof.md`
Source spec:  `openspec/specs/vehicleref/spec.md`
Generated:    2026-09-12
Status: APPLIED 2026-09-12

---

<!--
Reviewer note (leader, 2026-09-12): the target guide does NOT exist yet. apply-sync will create
it from references/guide-template.md, then apply these blocks.

Deliberately NOT proposed here:
- `## Component map` — spec.md carries no file paths, and --with-filemap was not passed.
- Any edit to `architecture/gateway-reader-writer-ports.md`. Its existing `tenant ownership
  check` row is still TRUE: the change wired no handler to the new helper, so
  `WHERE account_id = $1` is still the live tenant boundary today. That guide goes stale only
  when MAG-67/68/69 actually drop the column.
-->

## [guide] ## Glossary — REPLACE

- **Known as:** `vehicle ownership proof`, `authorization seam`, `vehicle ref`
- **Internal name:** `vehicleref` (the capability), `vehicleref.Ref` (the proof value)

## [guide] ## How maintenance works — APPEND

- **Proving one vehicle.** The platform takes a caller's own list of owned vehicle identities
  plus one requested identity. If the requested one is in the list, it returns a proof value
  that names that same identity. If it is absent, or the owned list is empty, it returns no
  proof and says the authorization did not succeed.
  _Source: spec vehicleref — Requirement: Vehicle Ownership Proof._
- **Proving a whole fleet at once.** A caller can ask for a proof for every vehicle in its own
  owned list in one call, instead of repeating the single-vehicle check per vehicle. One proof
  comes back per identity, each naming the identity it was built from. An empty owned list
  yields no proofs. This exists so a read across the caller's whole fleet stays one call.
  _Source: spec vehicleref — Requirement: Proof For Every Owned Vehicle._
- **Where the check runs.** The gateway is the only layer that checks whether a signed-in user
  may act on a requested vehicle. It authorizes once, and the resulting proof is what any
  domain module call for that vehicle receives.
  _Source: spec vehicleref — Requirement: Vehicle Ownership Check Happens At The Gateway._

## [guide] ## Conventions & gotchas — APPEND

- **A proof cannot be built for a vehicle the caller does not own.** The proof value is shaped
  so it cannot be constructed for an identity absent from the owned list. This is the point of
  the capability: a caller that skipped the check has no proof to pass.
  _Source: spec vehicleref — Requirement: Vehicle Ownership Proof._
- **Do NOT re-check tenant ownership inside a domain module.** A module below the gateway
  trusts that the gateway already proved ownership before the call. Adding a second check
  there is not extra safety — it duplicates the boundary and hides where it really lives.
  _Source: spec vehicleref — Requirement: Vehicle Ownership Check Happens At The Gateway._
- **The rejection must not reveal whether the vehicle exists.** When the caller does not own
  the named vehicle, the response says nothing about whether that identity is real. Otherwise
  someone can probe identities one by one and learn which are real.
  _Source: spec vehicleref — Requirement: Vehicle Ownership Check Happens At The Gateway._
- **An empty owned list is a normal state, not an error.** A caller with no vehicles gets no
  proof back. Handle it as "nothing to show", not as a failure.
  _Source: spec vehicleref — Requirement: Proof For Every Owned Vehicle._

## [index] ## Architecture topics — ADD ROWS

| `vehicle ownership proof` (the gateway proves the signed-in user owns a vehicle once, and passes the proof down; modules below do not re-check tenancy) | `architecture/vehicle-ownership-proof.md` |
| `authorization seam` | synonym of `vehicle ownership proof` → `architecture/vehicle-ownership-proof.md` |
| `vehicleref` | the capability behind `vehicle ownership proof` → `architecture/vehicle-ownership-proof.md` |
| `can a module check tenancy itself` | no — the gateway proves ownership, modules trust it → `architecture/vehicle-ownership-proof.md` |
