## Why

The `internal/tesla` adapter is already built and in daily use, but its behavior is
undocumented in OpenSpec — there is no `tesla` capability spec. The roadmap builds the web
gateway and the vehicle dashboard directly on top of this adapter (tiers 4–5), so we need a
durable, reviewable record of what the adapter guarantees before other modules depend on it:
which operations it offers, how it signals an expired token, its wake-before-data ordering,
its stateless per-user credential model, and the metric values it exposes.

This is a **retro-spec** — it captures the behavior of already-shipped code as capability
requirements **without changing any code**. It establishes the `tesla` capability that later
tiers reference and sets the pattern for retro-speccing other already-built adapters.

## What Changes

- Add a `tesla` capability spec that retro-documents the already-built `internal/tesla`
  adapter. **No code changes, no new dependencies.**
- Assert the adapter's three operations as behavior: **list a vehicle inventory**, **fetch a
  full vehicle snapshot** (charge / climate / drive / vehicle-state), and **wake a sleeping
  vehicle**.
- Assert the **wake-before-data ordering**: a full snapshot requires the vehicle to be online;
  the documented path is wake + poll-until-online.
- Assert the **distinct unauthorized signal**: a Fleet API HTTP 401 surfaces as a detectable
  "unauthorized" error (the sentinel `tesla.ErrUnauthorized`) that a caller uses to trigger a
  token refresh.
- Assert the **stateless per-call credential model**: the adapter holds no identity; callers
  supply `Credentials` on every operation, so one shared instance serves any user.
- Assert **metric conversion**: every distance/speed value the adapter exposes in miles/mph has
  a companion kilometre / km-per-hour value, nil-safe for optional fields.

## Capabilities

### New Capabilities
- `tesla`: an anti-corruption adapter over the Tesla Fleet API — listing an account's vehicles,
  fetching a vehicle's full state snapshot, and waking a sleeping vehicle on behalf of
  caller-supplied credentials — signalling expired credentials distinctly and exposing
  metric-converted distance values.

### Modified Capabilities
- None (first spec for this capability).

## Impact

- **Non-breaking, code-free.** Artifacts only; the `internal/tesla` package (`client.go`,
  `vehicles.go`, `types.go`) is unchanged, and no dependencies are added.
- **New spec on archive:** `openspec/specs/tesla/spec.md`.
- **Affected modules:** documents `tesla`; no other module is touched. Later tiers
  (`gateway`, `account`) will depend on this capability's stated behavior, not on adapter
  internals.
- Characterization tests are intentionally out of scope and may be a later change.
