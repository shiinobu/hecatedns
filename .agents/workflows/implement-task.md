---
description: Implement ONE task from the phase task file with strict TDD (RED, GREEN, refactor), targeted checks, a commit, and state updates.
---

# /implement-task [task id, e.g. P1-02]

Do exactly one task from `.agents/state/PHASE-N-TASKS.md`. If no id is given, take the first unchecked task whose dependencies are done. Never touch `docs/` during this workflow (Master's rule: documents last).

## 1. Prepare (read little)

- Re-read the task line, `rules/03-go-standards.md`, and the contract in `rules/04-engine-contract.md` if the task touches the engine.
- Open only the doc sections the task names (`docs/architecture.md` section numbers are in the task or in `rules/04`). Read the existing code you will touch. Do not explore the whole repository.
- Load the `hecate-go-tdd` skill for test patterns and `hecate-block-semantics` for matching/response/CNAME rules.
- If a needed fact is unknown (library signature, behavior), look it up in the real source; never guess. For dnsproxy see `notes/dnsproxy-api-facts.md`.

## 2. RED

- Write the tests first: table-driven, hostile inputs included (empty, huge, odd Unicode, trailing dot, uppercase, malformed DNS message).
- Run only the package: `go test ./backend/<pkg>/... -count=1`.
- Confirm the test FAILS for the RIGHT reason (assertion, or missing symbol that the next step defines) - not for a typo or a missing import. Paste the one or two decisive failing lines into the WORKLOG entry later.

## 3. GREEN

- Write the minimum code that passes. Keep functions under 50 lines, files cohesive (<800 lines), errors handled and wrapped.
- Re-run the package tests until green. Stop after 3 failed fix cycles on the same error (see `rules/02-workflow.md`).

## 4. Refactor and verify (targeted, touched packages only)

- `gofmt -l <touched dirs>` prints nothing; `go vet ./<pkg>/...`.
- `go test ./<pkg>/... -cover -count=1`; the package that holds logic is at least 80%.
- Concurrency or shared state touched: run under the race detector (`rules/06-environment.md`, per-command `CGO_ENABLED=1`).
- Parser touched: run the fuzz target briefly: `go test ./<pkg> -run xxx -fuzz FuzzName -fuzztime 20s`.
- Weak-test check for the riskiest behavior: temporarily break the code in a scratch copy of the repo (outside the working tree) and confirm a test fails.

## 5. Commit and record

- One conventional commit per task (or a few small ones): `feat: add domain matcher with atomic snapshot`. No co-author trailer. Commit code and tests only; the `.agents/` updates below are committed at `/handoff` or `/close-phase`.
- Tick the task in `state/PHASE-N-TASKS.md`.
- Append a WORKLOG entry: what was done, RED -> GREEN evidence, coverage numbers, commit id(s), small decisions with reasons, anything unverified.
- Update `state/STATE.md`: "Next", "In progress" (clear it), and add docs-worthy facts under "Docs to write at phase end".

## 6. Report (only if Master is waiting; otherwise continue with the next task)

Short Indonesian note: task id, result, evidence, next task. Detail stays in the WORKLOG.

## Quality checklist before you commit

- [ ] Test was seen failing first, then passing
- [ ] No mutation of shared or input data; snapshots immutable
- [ ] No network inside `backend/engine`; tests need no internet
- [ ] Hostile-input tests exist for anything parsing external data
- [ ] Exported symbols have doc comments; no magic numbers
- [ ] `gofmt`, `go vet` clean on touched packages
