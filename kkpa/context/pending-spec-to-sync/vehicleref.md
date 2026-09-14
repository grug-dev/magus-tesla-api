# Sync proposal — vehicleref

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `architecture/vehicle-ownership-proof.md`
Source spec:  `openspec/specs/vehicleref/spec.md`
Generated:    2026-09-14
Status: PENDING REVIEW

---

Scope of this delta: one requirement was ADDED when RM58 tier 3 archived — "A Fleet-Wide Read
Uses The Bulk Ownership Proof". The guide already describes the bulk proof as a thing that
exists. It does not yet say that a fleet-wide read must **use** it, nor that such a read fails
closed. Both are new rules.

## [guide] ## How maintenance works — APPEND

- **Using the fleet proof for a read.** A read scoped to the caller's whole fleet takes its
  vehicle identities from the bulk proof, not from a list built beside the call. The owned set
  is proved once and reused, never re-derived per read. When the owned set is empty, or cannot
  be determined at all, the read does not run.
  _Source: spec vehicleref — Requirement: A Fleet-Wide Read Uses The Bulk Ownership Proof._

## [guide] ## Conventions & gotchas — APPEND

- **Never build an owned-id list beside the call.** A fleet-wide read gets its identities from
  the bulk proof. A local loop that turns the caller's vehicles into a plain id slice looks
  harmless and is the thing this rule forbids: it puts an owned set outside the one place the
  guard watches. If you find yourself writing that loop, you want the bulk proof instead.
  _Source: spec vehicleref — Requirement: A Fleet-Wide Read Uses The Bulk Ownership Proof._
- **A fleet-wide read fails closed — empty and unknown both mean "no read".** An empty owned
  set and a failure to determine the owned set produce the same outcome: the read does not run,
  and the caller is told nothing was found. Never fall through to an unfiltered read. This is
  the security-relevant half of the rule: "no filter" would return every vehicle's rows.
  _Source: spec vehicleref — Requirement: A Fleet-Wide Read Uses The Bulk Ownership Proof._
- **This sharpens the existing "an empty owned list is a normal state" bullet, it does not
  contradict it.** That bullet is about asking for proofs and getting none back, which is
  normal. This one is about what a *read* must then do: stop. Both are true.
  _Source: spec vehicleref — Requirement: A Fleet-Wide Read Uses The Bulk Ownership Proof;
  Requirement: Proof For Every Owned Vehicle._

## [index] ## Architecture topics — ADD ROWS

| `bulk ownership proof` | synonym of `vehicle ownership proof` → `architecture/vehicle-ownership-proof.md` |
| `fleet-wide read` | a read scoped to every vehicle the caller owns → `architecture/vehicle-ownership-proof.md` |
