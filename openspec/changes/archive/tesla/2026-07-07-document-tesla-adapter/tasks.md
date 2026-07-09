# Tasks — document-tesla-adapter

> Retro-spec only. **No code changes** — the `internal/tesla` adapter is already built.

## 1. Retro-spec authoring
- [x] 1.1 Map each requirement to its `internal/tesla` source symbol (see the map in `design.md`)
- [x] 1.2 Confirm no code changes are required — every specced behavior is already implemented

## 2. Validation
- [x] 2.1 Run `openspec validate document-tesla-adapter --strict` and fix any errors

## 3. Verify & archive
- [x] 3.1 `/opsx:verify` — confirm the spec matches the current `internal/tesla` code
- [x] 3.2 `/opsx:archive` — merge the delta into `openspec/specs/tesla/spec.md`
- [x] 3.3 Update `openspec/roadmaps/multi-tenant-vehicle-access.progress.json` (tier 0 → archived) and tick the Tier 0 checklist box in the roadmap
