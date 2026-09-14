# Tasks — RM57-gateway-authorize-supercharger-reads

All work is inside `internal/gateway`, plus the docs listed in T3. No other module's
code is touched.

## Dependency graph

```
T1 (handler write path) ──► T2 (test double + engine + call sites) ──► T4 (final verification)
       │
       └──► T3 (docs, no Go file — parallel with T2) ──────────────────┘
```

## T1 — Handler write path

Depends on: nothing (tier 3 already retyped `VerifySession`).

- [x] 1.1 `internal/gateway/handlers/supercharger.go`: in `SuperchargerRowUpdate`,
      replace the `resolveSelectedVehicle` call with `currentVehicle(c)` +
      `h.authorizeVehicle`, passing the resulting `vehicleref.Ref` to `VerifySession`
      (`design.md` D1, D3, D4).
- [x] 1.2 Same file: add a one-line comment above each of the two read-site
      `resolveSelectedVehicle` calls (`superchargerStatsViewFor`,
      `fetchSuperchargerRowVM`) stating that ownership is already proven there
      (`design.md` D2).
- [x] 1.3 Same file: rewrite `SuperchargerRowUpdate`'s doc comment to state its reasons
      directly, with no `design.md`/decision-id citation (`design.md` D5).
- [x] 1.4 `go build ./internal/gateway/...` — expect a failure confined to the test file
      until T2 lands (the fake's signature no longer matches the interface).

## T2 — Test double, engine, call sites

Depends on: T1.

- [x] 2.1 `internal/gateway/handlers/supercharger_test.go`: add the
      `internal/vehicleref` import.
- [x] 2.2 Same file: retype `fakeSessionVerifier.VerifySession`'s second parameter from
      `teslaID int64` to `ref vehicleref.Ref`, unwrapping with `ref.TeslaID()` into the
      existing `verifySessionCall.teslaID` field. No other field or line in the struct
      or its constructor changes.
- [x] 2.3 Same file: add `selTeslaID int64, selVIN string` parameters to
      `superchargerRowEngine`, seeding `sessionTeslaIDKey`/`sessionVINKey` in the
      `/_session` handler when `selTeslaID != 0` — mirrors the existing
      `superchargerEngine` pattern in this same file.
- [x] 2.4 Update all ten `superchargerRowEngine(...)` call sites per the Test Contract
      table in `design.md`: `42, "VIN42"` for the seven that share
      `newHandlerForSuperchargerRow`'s single registered vehicle, `0, ""` for
      `TestSuperchargerRowUpdate_NoResolvableVehicleIs404` (no vehicle registered at
      all), `22, "VIN22"` for `TestSuperchargerRowUpdate_VerifyUsesResolvedVehicle` (the
      OWNER vehicle). Do NOT change any expected status, call count, or argument value —
      only the engine call's two new trailing arguments.
- [x] 2.5 `TestSuperchargerRowUpdate_VerifyUsesResolvedVehicle`'s doc comment: rewrite to
      describe what it now proves (the session-selected vehicle is used, not
      `vehicles[0]`) — same assertion, corrected description.
- [x] 2.6 `TestSuperchargerRowUpdate_NoResolvableVehicleIs404`'s doc comment: correct
      "must resolve one from the signed-in account first" to describe reading the
      session, since the write no longer auto-selects.
- [x] 2.7 `go build ./...` (whole repo) — expect clean. This is the change's own point:
      the repo has not built since tier 3 landed.
- [x] 2.8 `go vet ./internal/gateway/...` and `gofmt -l internal/gateway` — expect clean
      (an unrelated pre-existing `gofmt` finding in `vehicle_image_test.go`, a file this
      change does not touch, is not this change's regression — confirm via `git diff`
      that the file is untouched before treating it as clean).

## T3 — Docs

Depends on: T1 (needs the final behavior). Touches no Go file — safe to run in parallel
with T2.

- [x] 3.1 `internal/gateway/AGENTS.md`: the write-apertures table's
      `SuperchargerRowUpdate` row; the "Three of them diverge" paragraph (now "Two," plus
      a new paragraph on the CSRF-before-ownership guard order); the "Vehicle ownership —
      proved once, at the gateway" section's "No handler calls it yet" bullet.
- [x] 3.2 `kkpa/context/architecture/gateway-reader-writer-ports.md`: the component-map
      row for "session battery verify," and the whole "Aperture 2" section.
- [x] 3.3 `kkpa/context/input-port/charging/supercharger-stats.md`: the "No
      `RegisteredVehicles` ownership check on the write" bullet.
- [x] 3.4 `kkpa/context/use-case/charging/verify-session-battery.md`: Flow step 1.
- [x] 3.5 `kkpa/context/architecture/charge-record-mutation.md`: the "Different ownership
      vocabulary" bullet.
- [x] 3.6 `kkpa/context/architecture/vehicle-ownership-proof.md`: the Related KB note.
- [x] 3.7 `kkpa/context/INDEX.md`: the `tenant ownership check` row.
- [x] 3.8 Do NOT edit `openspec/specs/gateway/spec.md` (the live spec) — this change's
      delta lives under `specs/gateway/spec.md` in this change folder; the live spec
      syncs from it at archive time.
- [x] 3.9 Do NOT edit anything under `openspec/changes/archive/` or
      `kkpa/context/pending-spec-to-sync/applied/` — both are immutable records.
      `make archive-guard` enforces the first.

## T4 — Final verification

Depends on: T1-T3.

- [x] 4.1 `go build ./...` (whole repo) — expect clean.
- [x] 4.2 `go vet ./internal/gateway/...` — expect clean.
- [x] 4.3 `gofmt -l internal/gateway` — expect clean modulo the pre-existing,
      untouched-by-this-change `vehicle_image_test.go` finding (T2.8).
- [x] 4.4 `make vehicleref-guard` — expect clean.
- [x] 4.5 `make boundary-guard` — expect clean.
- [x] 4.6 `make i18n-guard` — expect clean.
- [x] 4.7 `make archive-guard` — expect clean.
- [x] 4.8 `openspec validate RM57-gateway-authorize-supercharger-reads --strict`.
- [x] 4.9 Hand the owner the suite command this change never runs:
      `go test ./internal/gateway/... -run 'TestSuperchargerRowUpdate|TestSuperchargerRowEditFragment|TestSuperchargerRowStatic'`
      plus the full `go test ./...` / `make test` — both build again as of this change,
      unlike tier 3's handoff. Say plainly that neither was run.
