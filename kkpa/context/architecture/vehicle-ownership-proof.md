# vehicleref — vehicle ownership proof

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `vehicle ownership proof`, `authorization seam`, `vehicle ref`
- **Internal name:** `vehicleref` (the capability), `vehicleref.Ref` (the proof value)

## Component map

**Not populated.** This guide was created by a spec sync. A capability `spec.md` carries
behaviour, not file paths, so no file map was proposed and none was invented.

Ask CodeGraph for the symbols named in the Glossary, or run
`/kkpa-context-curate vehicle ownership proof` to build this section properly.

## How maintenance works

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

## Conventions & gotchas

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

## Related KB

All KB links are relative to `kkpa/context/`, never to this file — so a guide can be moved
without recounting `../..` segments.

- Architecture: `architecture/gateway-reader-writer-ports.md` — the tenant ownership check as
  it works today, on `tesla_id`. This capability's proof value now has a real caller:
  `SuperchargerRowUpdate` calls `authorizeVehicle` before `charging.SessionVerifier.VerifySession`,
  the seam's first port rewired to require a `Ref` instead of a bare id.
