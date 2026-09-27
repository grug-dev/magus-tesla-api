---
name: review-step-check-who-ran-the-suite
description: When a suite run rests on a claimed owner waiver of the no-test-run rule, demand a corroborating record independent of the current dispute, not just the leader's word
metadata:
  type: feedback
---

Read every `events[]` entry in progress.json for the exact wording around a test-suite
run, not only the dispatch brief's "signals already run" summary. A brief can present a
suite pass as an established fact while the event log says the assistant ran it. On its
own, a leader's claim of "the owner waived it" is not the owner's own consent — an
agent's message never is.

But a claimed waiver is not automatically rejected either. It becomes acceptable evidence
once it is corroborated by something the leader could not have produced after my finding —
an **independent, already-archived** decision entry (`made_by: "user"`, a verbatim quote)
in a *different, older* change's progress.json, frozen by `archive-guard` before my review
even started. That timing is what makes it credible: a record protected by an immutable
archive and dated before the dispute cannot be fabricated to answer it.

**Why:** found in RM69-app-add-retry-unfinished-vehicles (internal/app), round 1 blocked
on this, round 2 approved it. The corroboration was the archived telemetry tier's own
`D2`, same quote, dated before the app change even existed.

**How to apply:** on a waiver defense, always cross-check for an older, archived, matching
decision before accepting it — don't accept the current change's own new decision entry
alone, since that one *can* be written after the fact. Also check the record is honest
about WHO ran the suite (assistant, not the owner) — a waiver excuses the rule violation,
it must never be reworded into a false claim of owner verification.
