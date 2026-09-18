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

Count so far: 3 (RM59-analytics-rekey-vehicle-metrics-on-tesla-id, tier 1, 2026-09-15
— 5 leftover citations in `internal/analytics/db_integration_test.go`; RM59-gateway-
authorize-metrics-reads, tier 2, 2026-09-15 — 2 leftover citations, `RM38`/`(RM40)`,
in `internal/gateway/handlers/history_test.go` and `handlers_test.go`, both inside a
doc comment reflowed by the same rename; RM66-analytics-add-travel-progress-deltas,
tier 2, 2026-09-18 — 1 fresh `design.md` citation in a brand-new comment in
`internal/analytics/db_integration_test.go`, not a rename leftover this time — the
worker wrote new prose that copied this file's own pre-existing, pervasive
`design.md`-citing house style, which a grep found in ~70 other pre-existing lines
in that one file alone).

Reached 3 across different changes. Per the promotion test, report this to the
leader. This still is NOT an `AGENTS.md` candidate: a grep guard already covers it
(scan changed comment lines for `task \d|RM\d+|design\.md` in `_test.go`/doc
comments), and the trip-wire test requires no build/vet/guard could catch the
break — one can. Propose the guard, not a written rule. Also worth the leader's
attention: `internal/analytics/db_integration_test.go` itself is saturated with
pre-existing `design.md` citations (established house style, out of this
reviewer's scope to flag) — a guard here would need a baseline/warn split like
`delta-guard`'s, not a hard fail, or it fails on file open.
