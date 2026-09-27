---
name: finding_leftover-citations-in-renamed-comments
description: Recurring review finding - new/renamed comments OR new string literals (e.g. t.Run names) keep a task/RM/RD/TR/design.md citation
metadata:
  type: feedback
---

Three shapes of the same finding. (a) A rename touches only the identifier in a doc
comment and leaves a nearby citation untouched (task number, `RM<N>`, `design.md`).
(b) A brand-new comment echoes an existing house style that itself cites `design.md`
or an `RD<N>`/`RM<N>` id — no rename involved, the worker just copied the pattern.
(c) The citation is not in a `//` comment at all but in a string literal the diff
adds — a `t.Run("TR-2 ...")` subtest name copied straight from design.md's own
test-reference labels. A comment-line grep misses shape (c) entirely.

**Why:** the B2 rule only asks the reviewer to check "added and changed" lines, and
the model editing a line tends to patch only the token it was told to patch, not
re-read the rest for a citation; when writing fresh prose, it also tends to copy the
surrounding file's own citing style verbatim.

**How to apply:** grep touched comment/doc lines for `task \d|RM\d+|RD\d+|CH\d+|design\.md`
before approving, in any file the diff added or touched, not only renamed lines. Open
a minor finding per occurrence, quoting the exact phrase.

Count: 7 across 7 different changes (2026-09-15 to 2026-09-27) — RM59-analytics
tier 1, RM59-gateway tier 2 (shape a, both); RM66-analytics tier 2, RM66-gateway
tier 3, RM67-analytics tier 3, RM67-app tier 4 (shape b, all four: fresh prose citing
`design.md`'s own "Test Contract" header, once also a fresh `(RD13)`). RM67-app's own
round also found shape (a) again: an edited doc comment on `Processor.ProcessVehicleData`
(app.go) and on `newTestProcessor` (processor_test.go) each kept a pre-existing
`design.md`/`RM52` reference next to the new sentence, while the *parallel* comment on
the same function in `processor.go`, edited in the same commit, was fully scrubbed —
proof the worker can do the scrub correctly in one file and still miss the sibling file.
RM69-telemetry tier 1 (2026-09-27, shape b again): two brand-new `_test.go` file-header
comments named `design.md` directly ("...this change's design.md test contract",
"written from design.md's test contract") — same house-style-copying cause, this time
in a file the worker created from scratch rather than one it edited.
RM69-analytics-add-unfinished-for-date (2026-09-27, shape c, new): a brand-new
`db_unfinished_integration_test.go` has four `t.Run("TR-2 ...")`..`"TR-5 ..."` subtest
names lifted verbatim from design.md's "Test contract" labels. The same commit's leader
event log claims "removed design.md/TR-N pointers from new test comments" — the sweep
covered comments and missed the subtest-name strings entirely, confirming a
comment-only grep is not enough.

Reached 3+ rounds long ago (now 8 across 8 changes). Per the promotion test this is
NOT an `AGENTS.md` candidate — no build/vet/guard could catch a rename leftover
today, but a grep guard could: scan every comment line AND every string literal a
diff adds or changes for `task \d|RM\d+|RD\d+|TR-\d+|design\.md|Test Contract`, not
only `//` comments and not only in `_test.go` files. Propose that guard to the leader
again — 7 rounds in and still not built. Note for whoever builds it: some files (e.g.
`internal/analytics/db_integration_test.go`) are saturated with pre-existing,
legitimate citations, so it needs a baseline/warn split like `delta-guard`'s, not a
hard fail on file open.
