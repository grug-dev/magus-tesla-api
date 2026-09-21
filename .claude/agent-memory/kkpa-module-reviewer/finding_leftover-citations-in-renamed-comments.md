---
name: finding_leftover-citations-in-renamed-comments
description: Recurring review finding - new or renamed comment lines keep a task/RM/RD/design.md citation
metadata:
  type: feedback
---

Two shapes of the same finding. (a) A rename touches only the identifier in a doc
comment and leaves a nearby citation untouched (task number, `RM<N>`, `design.md`).
(b) A brand-new comment echoes an existing house style that itself cites `design.md`
or an `RD<N>`/`RM<N>` id — no rename involved, the worker just copied the pattern.

**Why:** the B2 rule only asks the reviewer to check "added and changed" lines, and
the model editing a line tends to patch only the token it was told to patch, not
re-read the rest for a citation; when writing fresh prose, it also tends to copy the
surrounding file's own citing style verbatim.

**How to apply:** grep touched comment/doc lines for `task \d|RM\d+|RD\d+|CH\d+|design\.md`
before approving, in any file the diff added or touched, not only renamed lines. Open
a minor finding per occurrence, quoting the exact phrase.

Count: 6 across 6 different changes (2026-09-15 to 2026-09-21) — RM59-analytics
tier 1, RM59-gateway tier 2 (shape a, both); RM66-analytics tier 2, RM66-gateway
tier 3, RM67-analytics tier 3, RM67-app tier 4 (shape b, all four: fresh prose citing
`design.md`'s own "Test Contract" header, once also a fresh `(RD13)`). RM67-app's own
round also found shape (a) again: an edited doc comment on `Processor.ProcessVehicleData`
(app.go) and on `newTestProcessor` (processor_test.go) each kept a pre-existing
`design.md`/`RM52` reference next to the new sentence, while the *parallel* comment on
the same function in `processor.go`, edited in the same commit, was fully scrubbed —
proof the worker can do the scrub correctly in one file and still miss the sibling file.

Reached 3+ rounds long ago. Per the promotion test this is NOT an `AGENTS.md` candidate —
no build/vet/guard could catch a rename leftover today, but a grep guard could: scan
every comment line a diff adds or changes for `task \d|RM\d+|RD\d+|design\.md|Test Contract`,
not only in `_test.go` files. Propose that guard to the leader again — 6 rounds in and
still not built. Note for whoever builds it: some files (e.g.
`internal/analytics/db_integration_test.go`) are saturated with pre-existing,
legitimate citations, so it needs a baseline/warn split like `delta-guard`'s, not a
hard fail on file open.
