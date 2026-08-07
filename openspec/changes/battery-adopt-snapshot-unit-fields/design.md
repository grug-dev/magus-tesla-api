# Design — battery-adopt-snapshot-unit-fields

## Context

`internal/battery` owns no database. It derives rolling Wh/km from three sibling ports
(`telemetry.Reader`, `telemetry.SuperchargerReader`, `manualcharge.Reader`) plus
`account.Service`. Its distance input comes from `telemetry.Snapshot`, today via the
`OdometerKm()` companion:

```go
distance := end.OdometerKm() - start.OdometerKm()   // derive.go:32
...
FromKm: start.OdometerKm(),                          // derive.go:54
ToKm:   end.OdometerKm(),                            // derive.go:55
```

RM7 tier 2 renames the underlying field to `OdometerKm` and deletes the method (the two cannot
coexist on one Go type). This tier is the mechanical adoption.

## Goals / Non-Goals

**Goals:**
- Restore `internal/battery` to compiling against the tier-2 `telemetry.Snapshot`.
- Remove the module's last read-time unit conversion.
- Keep every derived number and every `ok=false` condition bit-for-bit what it was.

**Non-Goals:**
- Any change to the efficiency formula, window, capacity table, or public surface.
- Renaming `Efficiency.FromKm`/`ToKm` — they already carry the correct unit suffix and are the
  module's own public type, not a mirror of the snapshot's field names.
- Touching `internal/gateway` (RM7 tier 4).

## Decisions

### D1 — A pure call-site swap, with no formula change

`start.OdometerKm()` → `start.OdometerKm`. The value is numerically identical: tier 2 applies the
same `milesToKm` factor at write time that the method applied at read time, via the same
`internal/tesla` constant. So no test's expected Wh/km changes — only the fixtures' input
representation does (D2).

### D2 — Test fixtures change unit meaning, not just field name

`derive_test.go` builds `telemetry.Snapshot{Odometer: 1000}` fixtures that meant **1000 miles**.
Renaming the field to `OdometerKm` without touching the value silently reinterprets them as
**1000 km**, which changes the distance the test feeds the formula by a factor of 1.609. Any
expected Wh/km asserted from those fixtures must be recomputed, or the fixture values converted, so
the test still asserts what it was written to assert. This is the one place in an otherwise
mechanical tier where a careless find-and-replace produces a green build with wrong tests — hence
its own task (2.2) and its own risk entry.

### D3 — No `Efficiency` type change

`FromKm`/`ToKm` are already kilometres and already suffixed; `WhPerKm` is a computed ratio, not a
stored unit. The type is the battery module's own contract with the gateway, unaffected by where
the kilometres were converted. Renaming anything here would ripple into RM7 tier 4 for no benefit.

## Risks / Trade-offs

- **[Silent unit reinterpretation in test fixtures]** → See D2. Mitigated by task 2.2 requiring
  each fixture to be reviewed for what its number *means*, not just what the field is called, and
  by task 2.3 asserting at least one known-distance case end-to-end.
- **[This tier cannot be verified alone from a clean tree]** → It only compiles on top of tier 2.
  Its gate is therefore `go build ./internal/battery/...` after tier 2 is applied, not a
  standalone check; the full-tree build belongs to tier 4 (RM7 Decision 8).

## Migration Plan

None — no persisted state, no schema, no external contract. Rollback is reverting the call sites.

## Open Questions

None.
