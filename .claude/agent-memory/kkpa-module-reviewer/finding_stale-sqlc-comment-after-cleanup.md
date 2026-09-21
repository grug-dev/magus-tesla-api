---
name: finding_stale-sqlc-comment-after-cleanup
description: Review technique - after a leader strips RM/RD/design.md citations from a query.sql comment, check the paired *db/query.sql.go for the same edit
metadata:
  type: feedback
---

sqlc copies a query's SQL comment into its generated Go doc comment verbatim (confirmed
by comparing an unrelated pre-existing query's SQL comment against its own generated
comment — word for word match). So when a leader edits a `query.sql` comment late in a
change (e.g. to strip a forbidden `RM<N> tier <n>` or `RD<n>` citation per the B2 comment
rule), that edit only reaches the binary if `make sqlc` runs again afterward.

**How to apply:** whenever a decision log or commit message says a comment was cleaned
up to remove a roadmap/decision-ID citation, diff that file's SQL comment against the
matching generated Go doc comment in `db/query.sql.go` (or the module's equivalent
generated file). A mismatch means the generated file is stale — it still carries the
citation the source no longer has, and it no longer matches what a fresh `make sqlc`
would produce. This is a `major` finding (codegen reproducibility), not just the usual
`minor` comment-citation finding, because the fix requires re-running codegen, not
editing a comment by hand.

Count so far: 1 (RM67-charging-add-monthly-capacity-read, tier 1, 2026-09-18 —
`internal/charging/db/query.sql.go`'s `EffectiveCapacityForPeriod` doc comment still
read "(RM67 tier 1)" / "(RD4)" after the leader's own decision log said it had stripped
those citations from three comments; the actual `query.sql` source was clean, but
`make sqlc` was never re-run).

Not yet an `AGENTS.md` candidate — one occurrence. If it recurs, the fix is a guard
(the same `git-diff-comments` grep used for [[finding_leftover-citations-in-renamed-comments]],
extended to also grep the generated file), not a written rule — a guard can catch it
mechanically, so it fails the trip-wire test for a durable rule.
