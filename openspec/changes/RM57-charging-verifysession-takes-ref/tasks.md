# Tasks — RM57-charging-verifysession-takes-ref

All work is inside `internal/charging`. No file outside it is edited by this change —
`internal/gateway/handlers/supercharger.go`'s one broken call site is tier 4's job,
not this change's (`design.md` §Risks).

**`make vehicleref-guard`'s `_test.go` exemption is already fixed** (leader-owned,
`Makefile` + `internal/vehicleref/AGENTS.md`, commit `9b39034`, `design.md` D7).
Nothing in this file redoes that work.

## Dependency graph

```
T1 (port signature) ──► T2 (implementation) ──► T3 (test helper) ──► T4 (test call-site fixups)
       │                                                                    │
       └──► T5 (docs, no Go file — parallel with T2-T4) ────────────────────┤
                                                                             ▼
                                                                      T6 (final verification)
```

Sequencing rules:

- **T1 → T2 is a hard chain.** `session_verifier.go` cannot compile until the
  interface it implements is retyped.
- **T2 → T3 → T4 is a hard chain.** The test helper calls `vehicleref.All`, which
  needs no upstream change, but every test file that uses it must compile against
  `VerifySession`'s new signature (T2) or `go vet` fails on a type mismatch before
  ever reaching the helper.
- **T5 (docs) touches no Go file** and may run any time after T1 fixes the final
  signature text — in parallel with T2–T4.
- **T6 depends on everything.**

---

## T1 — Port signature

Depends on: nothing.

- [ ] 1.1 `internal/charging/charging.go`: add
      `"github.com/cristianpena/magus-tesla-api/internal/vehicleref"` to the import
      block.
- [ ] 1.2 Same file: change `SessionVerifier.VerifySession`'s signature from
      `VerifySession(ctx context.Context, teslaID int64, id uuid.UUID, startBatteryPct, endBatteryPct *int) (Session, error)`
      to
      `VerifySession(ctx context.Context, ref vehicleref.Ref, id uuid.UUID, startBatteryPct, endBatteryPct *int) (Session, error)`.
- [ ] 1.3 Same file: replace the `id/teslaID scope the update` doc-comment paragraph
      with the `id/ref scope the update` text `design.md` D3 gives verbatim.
- [ ] 1.4 Same file: replace `Calling VerifySession(ctx, teslaID, id, nil, nil)` with
      `Calling VerifySession(ctx, ref, id, nil, nil)` in the doc comment a few lines
      above (D3).
- [ ] 1.5 Do NOT change `SessionReader`, `SuperchargerSessionAnalyticsReader`, or any
      `manual_charge_entries` port (`design.md` D2).

## T2 — Implementation

Depends on: T1.

- [ ] 2.1 `internal/charging/session_verifier.go`: add
      `"github.com/cristianpena/magus-tesla-api/internal/vehicleref"` to the import
      block.
- [ ] 2.2 Same file: change `VerifySession`'s signature to match T1.2, and add
      `teslaID := ref.TeslaID()` as the method's first line. Every other line in the
      method body is unchanged (`design.md` D4) — do not thread `ref` past this
      point.
- [ ] 2.3 Same file: correct the comment currently reading
      `// the teslaID parameter above -- it is NOT NULL...` to
      `// the teslaID local variable above -- it is NOT NULL...` (it is no longer a
      parameter).
- [ ] 2.4 `go build ./internal/charging/...` — expect clean (test files still fail
      until T3–T4; that is expected at this point).

## T3 — Test helper

Depends on: T2.

- [ ] 3.1 `internal/charging/db_session_verifier_integration_test.go`: add
      `"github.com/cristianpena/magus-tesla-api/internal/vehicleref"` to the import
      block.
- [ ] 3.2 Same file, next to `ptrIntV`: add the `refFor` helper exactly as
      `design.md` D5 gives it. `make vehicleref-guard` now exempts `_test.go` files
      (leader fix, `design.md` D7) — do NOT add a `vehicleref:allow` comment
      anywhere in this file or any other test file.
- [ ] 3.3 Do NOT add a second `refFor`-shaped helper in any other file. All five test
      files in T4 are `package charging_test`, the same package this one lives in.

## T4 — Test call-site fixups

Depends on: T3. All five files are disjoint — safe to do in any order or split
across workers.

- [ ] 4.1 `db_session_verifier_integration_test.go` — 20 call sites, current lines
      167, 184, 220, 256, 276, 310, 346, 379, 393, 417, 447, 573, 598, 625, 647, 668,
      676, 698, 702, 726. Each is a mechanical substitution: replace the bare
      `teslaID` (or its numeric literal) argument with `refFor(teslaID)` (or
      `refFor(<literal>)`). No other token on any of these lines changes.
- [ ] 4.2 `db_inferred_capacity_sessions_integration_test.go` — 2 call sites, current
      lines 137 and 210. Same substitution.
- [ ] 4.3 `db_session_mirror_change_detection_integration_test.go` — 2 call sites,
      current lines 242 and 301. Same substitution.
- [ ] 4.4 `db_monthly_capacity_integration_test.go` — 5 call sites, current lines
      274, 449, 639, 785, 803. Same substitution.
- [ ] 4.5 `db_session_reader_updated_since_integration_test.go` — 1 call site,
      current line 154. Same substitution.
- [ ] 4.6 After 4.1–4.5, grep the module for `VerifySession(ctx, ` followed
      immediately by a bare identifier or digit (not `refFor(` or `ref`) — expect
      zero hits inside `internal/charging`.
- [ ] 4.7 Do NOT change any expected value, assertion, or seeded fixture in any of
      these five files — `design.md` D6 is explicit that no test's expected outcome
      changes, only its call syntax.
- [ ] 4.8 `go vet ./internal/charging/...` and `gofmt -l internal/charging` — expect
      clean. `go vet` compiles these `_test.go` files, so this is what proves T4's
      substitutions are syntactically and type-correct.

## T5 — Docs

Depends on: T1 (needs the final signature text). Touches no Go file — safe to run in
parallel with T2–T4.

- [ ] 5.1 `internal/charging/AGENTS.md` §Allowed Imports: add
      `internal/vehicleref` — imported only by `charging.go` and
      `session_verifier.go` — following the section's existing "only the files that
      own it" convention (see how `chargingdb` and `pgtype` are each scoped there).
- [ ] 5.2 Same file, §"The Supercharger session verification port": add one sentence
      — `VerifySession` now requires a `vehicleref.Ref` instead of a bare vehicle id,
      so a caller must already have proven ownership through `internal/vehicleref`.
- [ ] 5.3 `kkpa/context/workflows/supercharger-stats-read.md` line 45: update the
      printed `VerifySession(ctx, teslaID, id, startBatteryPct, endBatteryPct *int) (Session, error)`
      to `VerifySession(ctx, ref vehicleref.Ref, id, startBatteryPct, endBatteryPct *int) (Session, error)`.
      Change nothing else on that line.
- [ ] 5.4 `kkpa/context/use-case/charging/verify-session-battery.md` line 47: update
      the printed `charging.SessionVerifier.VerifySession(ctx, teslaID, id, startBatteryPct, endBatteryPct)`
      to `charging.SessionVerifier.VerifySession(ctx, ref, id, startBatteryPct, endBatteryPct)`.
- [ ] 5.5 Do NOT edit `kkpa/context/architecture/gateway-reader-writer-ports.md` or
      `kkpa/context/input-port/charging/supercharger-stats.md` — both describe the
      gateway's current lack of an ownership check, which stays true until tier 4
      lands (`design.md` §Docs).
- [ ] 5.6 Do NOT edit anything under `openspec/changes/archive/` or
      `kkpa/context/pending-spec-to-sync/applied/` — both are immutable records.
      `make archive-guard` enforces the first.

## T6 — Final verification

Depends on: T1–T5.

- [ ] 6.1 `go build ./internal/charging/...`
- [ ] 6.2 `go vet ./internal/charging/...`
- [ ] 6.3 `gofmt -l internal/charging`
- [ ] 6.4 `make vehicleref-guard` — expect clean. `refFor`'s `vehicleref.All(` call
      needs no marker; the guard already exempts `_test.go` files (see T3.2).
- [ ] 6.5 `make boundary-guard` — expect clean and unaffected (this change touches
      neither `internal/gateway` nor `internal/telemetry`).
- [ ] 6.6 `make archive-guard` — expect clean.
- [ ] 6.7 `go build ./...` (whole repo) — expect a failure confined to
      `internal/gateway/handlers/supercharger.go` (`selected.TeslaID` no longer
      matches `VerifySession`'s parameter type). Confirm the failure is exactly that
      one call site and nothing else — this is the expected, documented gap until
      tier 4 (`design.md` §Risks). Record the confirmation in this change's own
      notes; do not attempt to fix it here.
- [ ] 6.8 `openspec validate RM57-charging-verifysession-takes-ref --strict`
- [ ] 6.9 Hand the owner the suite command this agent never runs:
      `go test ./internal/charging/ -run 'TestVerifySession|TestMonthlyCapacity' -v`,
      confirming every renamed call site still reports `PASS`, not `SKIP` or
      `FAIL`. Whole-repo `go test ./...` / `make test` will not build until tier 4
      lands — say so plainly rather than asking the owner to run it now.
