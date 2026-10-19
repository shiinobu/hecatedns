---
description: Fresh-context end-of-phase review of a commit range - correctness, concurrency, input-handling security, project rules. Read-only; writes only the review report file.
---

# /review-phase <commit-range>   (example: `4b546fc..HEAD`)

Master's rule: ONE review per phase, after all of the phase's tasks and before the next phase. Not per task.

## Independence (important when you are the same agent that wrote the code)

Run this review in a NEW conversation/session that contains only: this workflow, `roles/phase-reviewer.md`, the commit range, the phase "done when" criteria and the gate results. Do not carry over the implementer's reasoning. If your tool cannot open a fresh session, start by writing down that you are switching role and re-derive everything from the code and the running binary, not from the WORKLOG's claims. Do not trust the author's claims.

## Steps

1. Inputs: the range, the phase section of `docs/roadmap.md` ("Selesai bila"), the gate results (run `/gates` first and paste its table; if there are none, run them yourself).
2. `git log <range> --stat` and `git diff <range> -- . ':!.agents' ':!.claude'` (the range `4b546fc..HEAD` also holds the commits that added the agent folders; skip them unless a task touched them). Read surrounding code and `docs/architecture.md` only where the diff or a suspected finding needs it.
3. Review against `roles/phase-reviewer.md` (correctness, concurrency, policy, security, project rules, test quality).
4. Check every "done when" criterion on the real binary where it can run (for Phase 1: `hecate run`, `scripts/dnsquery.ps1 ... -Port 5300`, `hecate rule add`, REFUSED for a client outside the ACL, the log). Experiments happen in a scratch copy or temp directory outside the working tree; limit mutation experiments to three.
5. Save the report to `.agents/state/reviews/phase-N-review.md` (create the folder). Do not edit any other file and do not commit code. The report may be committed together with the fixes later.
6. Chat output to Master (Indonesian, short): counts per severity, the top findings in one line each, the file path of the report, and whether the next phase may start.

## Report format

Group by severity, most severe first:
- **Critical** - security hole or data loss; blocks the next phase.
- **Important** - bug or significant quality issue; fix before the next phase.
- **Minor** - style or small improvement.

Per finding: `file:line`, the defect in one sentence, a concrete failing input or scenario, a suggested fix direction. Then: what you verified and found correct, the gate results, and any "done when" criterion you could not verify. If there is no Critical or Important finding, say so plainly. Do not pad and do not invent findings.

## After the review

The implementer (any session) fixes Critical and Important findings with a failing test first (RED), then the fix (GREEN), one commit each (`fix: ...`), re-runs `/gates`, and records the fixes in the WORKLOG. Minor findings go to the phase report's "deferred" list unless trivial. Then `/close-phase`.
