# Tasks — RM62-app-improve-cycle-step-logging

Ownership: every task is **[module: app worker]**. `internal/app/` plus two
explicitly granted paths outside it: `.vscode/launch.json` and
`internal/app/AGENTS.md` (already inside the module).

**No unit tests** — roadmap Decision 1 / design.md "Tests excluded". No task
below writes a test.

## Parallel-safety

The two pieces touch disjoint files and have no dependency on each other:

- **Piece 1** (Wave 1) touches only `internal/app/processor.go`.
- **Piece 2** (Wave 2) touches only `.vscode/launch.json`.
- **Wave 3** (docs) touches `internal/app/AGENTS.md` and describes the
  post-change state of both pieces, so it depends on both waves for content —
  but that is a content dependency, not a file conflict. It could be written
  in parallel with Waves 1 and 2 by an agent willing to describe the target
  state ahead of the edit; it is sequenced last here so it reflects what
  actually shipped.

**Waves 1 and 2 are parallel-safe with each other.** Different agents may run
them at the same time.

---

## Wave 1 — Per-vehicle log labels (`internal/app/processor.go`)

- [x] **1.1** In `recalculateAnalytics`, delete the pre-loop line:
  `logging.Note("Processor", "recalculateAnalytics", "gap reconciliation: %s → %s", start, end)`.
  The `start`/`end` values it used stay — they are still needed by the
  per-vehicle gap line (task 1.3) and by `ConsumedByDay`/`ReconcileWindow`,
  unchanged.
  `depends_on`: — · `parallel_ok`: no (first edit in the function)

- [x] **1.2** Inside the per-vehicle loop, immediately before
  `p.recalculator.Reconcile(ctx, v.TeslaID)`, add:
  `logging.Note("Processor", "recalculateAnalytics", "metrics reconciliation: vehicle %d", v.TeslaID)`.
  Keep the `metrics reconciliation:` topic exactly as design.md D2 specifies —
  it must match the topic the function's existing error line
  (`"metrics reconciliation: vehicle %d: %v"`) already uses, a few lines below.
  `depends_on`: 1.1 · `parallel_ok`: no

- [x] **1.3** Inside the per-vehicle loop, immediately before the gap work
  starts (before `p.analyticsReader.ConsumedByDay(ctx, v.TeslaID, start, end)`),
  add:
  `logging.Note("Processor", "recalculateAnalytics", "gap reconciliation: vehicle %d: %s -> %s", v.TeslaID, start, end)`.
  Keep the `gap reconciliation:` topic exactly as design.md D2 specifies — it
  must match the two existing error lines lower in the function that already
  use it.
  `depends_on`: 1.2 · `parallel_ok`: no

- [x] **1.4** Update `recalculateAnalytics`'s own doc comment: it currently
  says log lines "keep their half (...) in the message so they remain
  greppable" — add one sentence stating each half's line now also names the
  vehicle and is printed once per vehicle, replacing the old single
  before-the-loop line. Do not cite this change's ID or the roadmap in the
  comment — state the reason itself (per-vehicle attribution), per the
  project's code-comment rule.
  `depends_on`: 1.3 · `parallel_ok`: no

---

## Wave 2 — VS Code launch entry (`.vscode/launch.json`)

- [x] **2.1** Add a new configuration to the `configurations` array, after the
  existing `"Launch web (cmd/web)"` entry, exactly as design.md D4 specifies:
  `name: "Debug poller (cmd/poller --once)"`, `type: "go"`,
  `request: "launch"`, `mode: "auto"`,
  `program: "${workspaceFolder}/cmd/poller"`, `cwd: "${workspaceFolder}"`,
  `args: ["--once"]`, `envFile: "${workspaceFolder}/.env"`. No `env` override
  block — `cmd/poller` has no dev-mode flag equivalent to `cmd/web`'s
  `MAGUS_DEV`.
  `depends_on`: — · `parallel_ok`: with Wave 1 (disjoint file)

---

## Wave 3 — Docs (`internal/app/AGENTS.md`)

- [x] **3.1** In the "Testing notes" section, next to the existing sentence
  *"The verification signal for the two uncovered steps is the owner's own
  `go run ./cmd/poller --once`"*, add: this same command now also runs under
  the VS Code debugger via the "Debug poller (cmd/poller --once)" launch
  entry in `.vscode/launch.json`. State plainly that running it wakes the
  real car and makes paid Fleet API calls — the same cost as running the
  command from a terminal, per design.md D4's warning.
  `depends_on`: 2.1 · `parallel_ok`: no

- [x] **3.2** In whichever section documents `recalculateAnalytics`'s log
  behavior (or add one short line to "Responsibility" if none exists yet),
  note that its two halves each log per vehicle now, not once for the whole
  step. Keep this to one sentence — the full behavior is in the function's
  own doc comment (task 1.4), and `AGENTS.md` should point there, not
  duplicate it.
  `depends_on`: 1.4 · `parallel_ok`: with 3.1

---

## Verification (owner-run; see Test-Execution-Policy)

After every wave above is implemented:

```
go build ./...
go vet ./...
gofmt -l .
make lint
```

Owner-only manual smoke check: `go run ./cmd/poller --once` (wakes the real
car, paid Fleet API calls) — or the new VS Code "Debug poller (cmd/poller
--once)" launch entry — confirming the log now shows, per vehicle, a
`metrics reconciliation: vehicle <id>` line followed by `Reader` query lines,
then a `gap reconciliation: vehicle <id>: <start> -> <end>` line followed by
its own query lines, matching the roadmap's target shape (RD2).
