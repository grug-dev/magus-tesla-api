---
name: finding_leftover-citations-in-renamed-comments
description: Recurring review finding - a comment line touched only to rename an identifier keeps its old task/RM/design.md citation
metadata:
  type: feedback
---

When a task renames a function or a port method (e.g. `LatestMetricsByAccount` to
`LatestMetricsForVehicles`), the worker often rewrites only the identifier inside a
doc comment and leaves a nearby citation untouched — a task number ("task 6.4(b)"),
a `Task N.N` header, an `RM<N>-A/RM<N>-C` fixture reference, or `design.md`. The same
diff sometimes shows the correct behavior a few lines away (a citation dropped
cleanly), so the miss is inconsistent, not a blanket habit.

**Why:** the B2 comment rule only asks the reviewer to check "added and changed"
lines. A line changes because one word in it changed (the identifier), so the whole
line counts — but the model doing the edit tends to patch only the token it was told
to patch, not re-read the rest of the line for a citation.

**How to apply:** when a change renames a port, method, or type, grep the touched
test/doc-comment lines for `task \d`, `Task \d`, `RM\d+`, `CH\d+`, `design\.md` before
approving. Open a minor finding per file, quoting the exact leftover phrase, not a
generic "check comments" note.

Count so far: 2 (RM59-analytics-rekey-vehicle-metrics-on-tesla-id, tier 1, 2026-09-15
— 5 leftover citations in `internal/analytics/db_integration_test.go`; RM59-gateway-
authorize-metrics-reads, tier 2, 2026-09-15 — 2 leftover citations, `RM38`/`(RM40)`,
in `internal/gateway/handlers/history_test.go` and `handlers_test.go`, both inside a
doc comment reflowed by the same rename). Needs to reach 3 across different changes
before proposing an `AGENTS.md` rule per the promotion test — and even then, this is
grep-able by a guard (a `make` target scanning changed comment lines for
`task \d|RM\d+|design\.md` in `_test.go`/doc comments), so a guard may be the better
fix than a written rule.
