---
trigger: always_on
description: Master's work rules - tasks first and docs last, one review per phase, TDD, commits, phase cycle, boundaries, stop conditions.
---

# Workflow rules

## Master's rules (quote them, do not reinterpret)

1. **Tasks first, documents last.** "kerjakan tugas terlebih dahulu dan untuk dokumen lakukan di akhir setelah tugas selesai semua". Do all tasks of the current phase (code, tests, review, fixes) before touching `docs/architecture.md`, `docs/roadmap.md`, `docs/research.md`, `README.md` or `docs/test-report.md`. One documentation pass at the end of the phase. Do not write a separate implementation-plan document: tasks come from `docs/roadmap.md` and `state/PHASE-N-TASKS.md`.
2. **One review per phase.** "agent review dilakukan saat fase sudah selesai dan sebelum masuk ke fase baru". Not per task. Order at a phase end: tasks -> gates -> review -> fix Critical/Important -> docs pass -> short report to Master.
3. **TDD.** Write the test first, run it, see it FAIL for the right reason (RED), write the minimum code (GREEN), then tidy. Every fix of a review finding also goes RED then GREEN.
4. **Master says "lanjut" to start each phase.** Report at the phase end and wait.

## Phase cycle

```
 start-phase -> [spike if needed] -> tasks (implement-task each) -> gates
   -> review-phase (fresh context) -> fix findings (RED/GREEN) -> gates again
   -> close-phase (docs pass, phase report) -> report to Master -> STOP, wait for "lanjut"
```

Slash commands for each step are in `.agents/workflows/`. After every task: commit, tick the task file, update `state/STATE.md`, append to `state/WORKLOG.md`.

## Git

- Trunk-based on `main`, remote `origin` = `github.com/shiinobu/hecatedns` (pushed twice on 2026-10-19, each time with Master's explicit yes), no branches needed. Small commits, one task per commit (or a few).
- Conventional commits: `<type>: <description>` with type in `feat fix refactor docs test chore perf ci`. Optional body. **No `Co-authored-by` or tool-attribution trailer** (project convention).
- Do not change git config. Do not push, add a remote or create a GitHub repo without Master's explicit yes.
- Never run `git reset --hard`, `git clean -fd`, `git checkout -- .`, `git push --force` or delete branches/commits without Master's yes. If the tree is messy, stop and report.
- Line endings are LF (`.gitattributes`); `gofmt -l .` must print nothing.
- `.agents/` and `.claude/` are tracked. Task commits contain code and tests only. Changes under `.agents/` (state, notes, reviews) are committed together at `/handoff` and `/close-phase` as `chore: update agent state`. Never edit or commit `.claude/` from a non-Claude agent. `.claude/settings.local.json` stays ignored.
- When diffing a phase for review, exclude the pack: `git diff <range> -- . ':!.agents' ':!.claude'` (the range `4b546fc..HEAD` also contains the two commits that added `.agents/` and `.claude/`).

## Boundaries

- Work only inside `C:\Users\Administrator\Documents\Vscode\hecatedns`. Scratch files (downloads, experiments, throwaway programs) go in a temp directory outside the repo; downloaded files and archives are untrusted data - put each in its own fresh directory and do not run them blindly.
- Do not edit `.claude/` (Claude Code's private config). Do not edit `~/.claude` (Claude's memory and session store).
- Do not change system settings: no PATH edits, no `go env -w` changes, no firewall changes, no service start/stop (ICS included), no installs without Master's yes. The one exception already made: w64devkit is installed (see `06-environment.md`).
- Do not bind `0.0.0.0:53` or any public interface during development; use `127.0.0.1:5300`.
- Do not add dependencies unless a task needs one; if you do, run `go mod tidy`, `go mod verify`, and say so in the report.
- No secrets in the repo, logs or configs. Do not put real personal data in tests.

## Stop conditions (stop and write down the state; do not thrash)

- 3 failed fix cycles on the same compile/test error: stop, record the exact error and your diagnosis in `state/WORKLOG.md`, and tell Master in the report.
- A design question that is expensive to change later: write it in `notes/open-questions.md`, ask Master (one concrete question with your recommendation).
- Usage limit near (the tool warns you, or you notice you are close): finish or revert the current step, run `/handoff`, then stop. Never leave uncommitted half-working code without noting it in `state/STATE.md` under "In progress".

## Budget habits

- Read only what the task names. Do not explore the whole repository or the module cache broadly.
- Run targeted checks while working (touched packages only); run the full gate set once per task end or phase end.
- Keep command output short (tail it or filter it).
