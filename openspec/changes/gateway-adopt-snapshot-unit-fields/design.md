# Design — gateway-adopt-snapshot-unit-fields

## Context

The gateway maps `telemetry.Snapshot` onto logic-free view models: the template does no
arithmetic, no unit conversion and no time math (`ai/htmx-conventions.md`, and the gateway spec's
own "Templates contain no business logic" scenario). All derivation happens in the handler, which
today includes the unit conversion:

```go
fv.BatteryRange = fmt.Sprintf("%.1f km", snap.BatteryRangeKm())   // handlers.go:311
vm.Odometer     = formatKm(snap.OdometerKm())                     // handlers.go:400
d := pts[i].OdometerKm() - pts[i-1].OdometerKm()                  // history.go:147
```

RM7 tier 2 deletes those companions and hands the values over already converted. The handler layer
stays exactly where it is — it just stops converting.

## Goals / Non-Goals

**Goals:**
- Restore `go build ./...` for the whole repository (this is RM7's final tier).
- Remove the last read-time conversions in the codebase.
- Keep the rendered output byte-identical: same numbers, same rounding, same separators.

**Non-Goals:**
- Changing view-model fields, templates, routes, or any rendered string.
- Removing `formatKm` — see D2.
- Resolving the whole-vs-one-decimal inconsistency between `mapDashboardSnapshot` and
  `mapVehicles` (RM7 "Future work"). It predates this roadmap and is out of scope.

## Decisions

### D1 — Call-site swap only; the format strings do not change

`fmt.Sprintf("%.1f km", snap.BatteryRangeKm())` → `fmt.Sprintf("%.1f km", snap.BatteryRangeKm)`.
The literal `" km"` in the format string was already correct and stays. Since tier 2 applies the
identical factor at write time, the rendered number is unchanged, which is what makes this tier
verifiable end-to-end: the dashboard must show the same values it showed before RM7 started.

### D2 — `formatKm` survives, with a corrected comment

`formatKm` rounds to a whole number and inserts thousands separators (`19312.069… → "19,312 km"`).
That is presentation, it is the layer the user explicitly said should own separators, and it is
the gateway's job. It never converted anything — the conversion happened in its *argument*. Only
its doc comment is wrong after tier 2:

> "…arrives in km, converted from miles via `Snapshot.OdometerKm()`; this helper only rounds and
> groups." (`handlers.go:425`)

The second half stays true; the first half names a deleted method. Rewrite it to say the value
arrives already in kilometres from the telemetry port.

Worth stating explicitly because "remove all conversion from the gateway" could be misread as
"remove `formatKm`". The user's instruction separates the two: *"Thousand separator is something
on the UI scope, that's good.. but the value should be without it"* — the **stored** value carries
no formatting; the **rendered** one does.

### D3 — The charts get the largest win, and the delta stays a delta

`buildOdometerChart` currently calls `OdometerKm()` twice per bar to compute a consecutive-day
delta, plus once more for the tooltip's cumulative reading. With the field read, the delta is
`pts[i].OdometerKm - pts[i-1].OdometerKm` — same arithmetic, one fewer multiplication per operand.
The delta itself is *not* a unit conversion and stays in the handler: it is chart-specific
derivation (RM5 Decision 5) that only the gateway wants, so it does not belong in telemetry.

The negative-delta-clamps-to-zero rule and the tooltip strings are untouched.

### D4 — Spec text is corrected, behavior is not

Two gateway requirements name the companion methods inside their normative text. Left alone they
would describe methods that do not exist, so they are MODIFIED to say the values are read in
kilometres from the telemetry port. Every scenario's observable outcome — what the user sees on
the page — is preserved word for word wherever it does not name a method.

## Risks / Trade-offs

- **[Test fixtures written in miles now mean kilometres]** → `handlers_test.go:231-267,560-609`
  has fixtures with explicit intent comments (`Odometer: 12000.0, // miles → OdometerKm = 12000 * 1.609344`)
  and expectations derived from them (`wantOdometer := fmt.Sprintf("%.1f km", snap.OdometerKm())`).
  Renaming the field without revisiting the value silently changes what the test asserts, and
  expectations computed by calling the deleted method must be replaced with literals. Task 3.2
  covers this; it is the main correctness risk in an otherwise mechanical tier.
- **[Regression in rendered output would be silent]** → Mitigated by task 5.4: compare the
  `/dashboard` tiles against the pre-RM7 values for the same snapshot row. The numbers must match;
  if they do not, tier 2's migration factor and this tier's field reads disagree.

## Migration Plan

None — no persisted state and no external contract. This tier is, however, the point at which the
RM7 branch becomes shippable: nothing in tiers 2–4 should be deployed until this one is merged.

## Open Questions

None.
