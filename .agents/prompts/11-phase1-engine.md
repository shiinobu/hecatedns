# 11 - Phase 1, steps 2-6: the engine (tasks P1-02 .. P1-06)

Use after the spike (P1-01) is ticked. This is the hard core: run it with your most careful setting. Adopt `.agents/roles/core-engineer.md`. Paste everything between the lines.

---

Kerjakan tugas **P1-02 sampai P1-06** dari `.agents/state/PHASE-1-TASKS.md`, berurutan: matcher, rules, response, acl, engine. Adopt the role in `.agents/roles/core-engineer.md`.

Read first (only these): `.agents/rules/03-go-standards.md`, `04-engine-contract.md`, `05-security-and-input.md`; skills `hecate-go-tdd` and `hecate-block-semantics`; `docs/architecture.md` sections 5.1, 6, 7, 8, 14; `notes/dnsproxy-api-facts.md` (for the engine you mostly need the DNS message types: miekg/dns v1.1.73); existing style in `internal/config`.

For each task use `.agents/workflows/implement-task.md`:
- Test first and show RED, then GREEN; table-driven; hostile inputs; a fuzz test for the rule-line parser; a benchmark skeleton for the matcher.
- The package is `internal/engine` (files like `matcher.go`, `rules.go`, `response.go`, `acl.go`, `engine.go`). It must not import `backend`, `lists` or `qlog`. Define a small logger interface inside `engine` (qlog implements it in P1-07).
- Contract semantics are fixed by `rules/04-engine-contract.md`; API names are yours to choose cleanly. Keep every function < 50 lines and every file < 800 lines.
- Immutable snapshots swapped with `atomic.Pointer`; a concurrency test for the engine and the matcher swap, run under `-race` (per-command `CGO_ENABLED=1`).
- `engine` package coverage >= 80%.
- One commit per task, e.g. `feat: add domain matcher with atomic snapshot`. After each task: tick the box, WORKLOG entry (RED -> GREEN evidence, coverage, commit ids, decisions), update `.agents/state/STATE.md`.
- Do not touch `docs/`. Put docs-worthy facts into STATE.md.

When P1-06 is done, run `/gates`, then report to Master (Indonesian, <= 12 lines): tasks done, coverage numbers, commit ids, decisions you made, anything unverified. Then continue with prompt 12 only if Master says "lanjut" or has already told you to keep going.

---
