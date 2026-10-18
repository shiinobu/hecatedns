---
description: Save the exact working state for the next agent (Claude or another) - update STATE.md and WORKLOG.md, leave git in a describable condition, and print the paste-ready first message.
---

# /handoff - run before stopping or when the usage limit is near

The next agent has none of this conversation. Everything it needs must be on disk.

1. **Stop cleanly.** Finish the current micro-step. If the code is green, commit it (`feat:`/`fix:`/`test:`, no co-author trailer). If it is red or half-done, do NOT commit broken code to `main` and do NOT discard it: leave the files, and describe them in step 3 (task id, files touched, the exact failing test/command and its output, what you would do next).
2. **Run the cheap checks** and note the results: `git status --short`, `git log --oneline -3`, `gofmt -l .`, `go build ./...`, `go test ./... -count=1`.
3. **Update `state/STATE.md`:** snapshot (phase, HEAD, who worked, gates and when they were last fully run), "In progress" (see step 1), "Next" (the single next action and the command to start it), blockers, and the "Docs to write at phase end" list with anything learned this session.
4. **Update the task file** `state/PHASE-N-TASKS.md`: tick finished tasks only; add a note under a task if it is partly done.
5. **Append a WORKLOG entry:** what was done, evidence (RED/GREEN lines, coverage, commit ids), decisions with reasons, problems and unverified items, next step.
6. **Update `notes/`:** new failed approaches -> `do-not-retry.md`; new questions -> `open-questions.md`; verified dnsproxy facts -> `dnsproxy-api-facts.md`.
6b. **Commit the pack:** `git add .agents` then `git commit -m "chore: update agent state"` (only `.agents/` paths; never `.claude/`). Code commits stay separate. If you committed code in step 1, note that commit id in the WORKLOG entry before this commit.
7. **Tell Master** (Indonesian, <= 10 lines): where things stand, the HEAD commit id, whether the tree is clean, what the next agent must do first. Mention anything uncommitted.
8. **Print the paste-ready message** for the next session:
   - If the next agent is Gemini or another non-Claude agent: tell Master to paste `prompts/00-bootstrap.md`.
   - If the next agent is Claude Code: tell Master to paste `prompts/90-handoff-to-claude.md`.

Do not push, do not create a remote, do not edit `docs/`.
