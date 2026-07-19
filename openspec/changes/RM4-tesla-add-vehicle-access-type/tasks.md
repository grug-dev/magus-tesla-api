> **No tests for the exploration capability.** Per `CLAUDE.md`, `internal/tesla/raw.go` and
> everything under `cmd/explore-tesla-api/` must have zero `_test.go` files and no test may
> call any `Raw*` method. The new `AccessType` field is on an existing DTO struct; the sync
> rule does not apply (no new method was added). Do NOT add tests for `raw.go` or the
> explorer. The new decode test covers `ListVehicles` only.
>
> **Dependencies / parallelism:**
> T1 and T2 are independent — they touch disjoint files (`types.go` vs `vehicles_test.go`
> and `AGENTS.md`). They MAY be implemented in parallel by separate sub-tasks.
> T3 (verification) depends on T1 and T2.

## T1 — Add `AccessType` field to `VehicleTesla` DTO — no dependencies

- [x] T1.1 In `internal/tesla/types.go`, add `AccessType string \`json:"access_type"\`` to
      the `VehicleTesla` struct. Place the field after the existing identity/state fields
      (after `State string`, before any blank line closing the struct). No pointer, no enum
      type, no companion method — plain `string` per D1.
- [x] T1.2 Confirm no `Km()` or `Kmh()` companion method is added for `AccessType` (string
      classification field, not a distance/speed value). Document this fact with a brief
      inline comment on the field if needed for reviewer clarity.

## T2 — Decode test + `AGENTS.md` interface note update — no dependencies

- [x] T2.1 In `internal/tesla/vehicles_test.go`, add a test (same-package, offline) that
      serves a canned `/api/1/vehicles` JSON response via `httptest.Server` with a vehicle
      whose `"access_type"` is `"OWNER"`. Assert that `ListVehicles` returns
      `vehicles[0].AccessType == "OWNER"`.
- [x] T2.2 Add a complementary assertion in the same test (or a separate sub-case) that a
      vehicle with `"access_type"` omitted from the JSON response decodes to
      `AccessType == ""` (zero value, no error).
- [x] T2.3 In `internal/tesla/AGENTS.md`, update the "Other public symbols" sentence that
      lists `VehicleTesla` and its fields to include `AccessType string` so the documented
      DTO shape matches the code.

## T3 — Verification — depends on T1 and T2

- [ ] T3.1 `go build ./...` passes with no errors.
- [ ] T3.2 `go vet ./...` passes with no warnings.
- [ ] T3.3 `go test ./internal/tesla/...` is green and runs entirely offline (no live Tesla
      call, no `.env` required, no DB).
- [ ] T3.4 `openspec validate RM4-tesla-add-vehicle-access-type --strict` passes and
      tasks.md checkboxes reflect real completion.
