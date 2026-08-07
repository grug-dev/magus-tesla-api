# Tasks — tesla-add-tpms-psi-companions

> RM7 tier 1 of 4. Single module (`internal/tesla`), additive only. One worker, serial —
> groups 1 and 2 touch the same file, so there is no parallel split worth taking.

## 1. Conversion constant and companions

- [ ] 1.1 Add `barToPSI = 14.503773773` as a package-level constant in `internal/tesla/types.go`,
      declared beside the existing `milesToKm`, with a doc comment stating it is the exact
      bar→PSI factor and that the factor must never be written inline at a call site (design D3).
- [ ] 1.2 Add `func (v VehicleStateTesla) TpmsPressureFLPSI() float64` returning
      `v.TpmsPressureFL * barToPSI`, immediately after the existing `OdometerKm()` method, with a
      doc comment matching that method's style.
- [ ] 1.3 Add the same companion for the front-right corner (`TpmsPressureFRPSI`).
- [ ] 1.4 Add the same companion for the rear-left corner (`TpmsPressureRLPSI`).
- [ ] 1.5 Add the same companion for the rear-right corner (`TpmsPressureRRPSI`).
- [ ] 1.6 Confirm the return type is plain `float64` on all four (NOT `*float64`) and that the
      DTO's four `TpmsPressure*` bar fields are left exactly as they are — the adapter mirrors the
      API payload and converts only in companions (design D2, spec scenario "The bar values the
      adapter exposes are unchanged").

## 2. Tests

- [ ] 2.1 Add a unit test asserting each of the four companions returns the corner's bar value
      multiplied by `14.503773773`, using a distinct value per corner so a copy-paste error that
      reads the wrong field fails the test.
- [ ] 2.2 Add a unit test asserting a `0.0` bar reading converts to `0.0` PSI and is returned as an
      ordinary value (spec scenario "A truthfully reported zero bar converts to zero PSI").
- [ ] 2.3 Verify no test added here calls any `Raw*` method or `cmd/explore-tesla-api` — the
      `tesla-exploration` capability must stay test-free (`CLAUDE.md`).

## 3. Docs

- [ ] 3.1 Update `internal/tesla/AGENTS.md` where it describes the companion-method convention, so
      it covers pressure (bar→PSI) alongside distance/speed (miles→km) and names `barToPSI` as the
      second conversion constant the module owns.
- [ ] 3.2 Confirm no doc change is needed in `cmd/explore-tesla-api/README.md` or
      `internal/tesla/raw.go` — no Fleet API call was added, so the sync rule does not fire
      (design D4). Record that conclusion in the change's `progress.json` notes rather than
      editing those files.

## 4. Verification gate

- [ ] 4.1 `go build ./internal/tesla/...` and `go vet ./internal/tesla/...` pass.
- [ ] 4.2 `go test ./internal/tesla/...` passes.
- [ ] 4.3 `go build ./...` passes — this tier is additive, so unlike tiers 2 and 3 the full tree
      must still compile (RM7 Decision 8).
- [ ] 4.4 `openspec validate tesla-add-tpms-psi-companions --strict` passes.
