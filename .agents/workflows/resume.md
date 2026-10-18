---
description: Orient in the xdns project at the start of a session - read the state, verify the baseline against git, report to Master in Indonesian. Changes no files.
---

# /resume - orient before doing anything

Goal: know exactly where the project stands, without trusting memory. Do NOT start work and do NOT modify files.

1. Read every file in `.agents/rules/`.
2. Read `.agents/state/STATE.md` completely, then the last ~40 lines of `.agents/state/WORKLOG.md`, then the current phase's task file (`.agents/state/PHASE-N-TASKS.md`).
3. Read `.agents/notes/do-not-retry.md` and `.agents/notes/open-questions.md`.
4. Check git against the state file (run from the project root):
   - `git log --oneline -8` and `git status --short`
   - Compare HEAD with "Last verified HEAD" in `STATE.md`. If they differ, someone committed in between (maybe Claude): read `git log <old>..HEAD --stat` and list what changed.
   - Uncommitted changes other than `.agents/`? Say so; do not discard anything.
5. Verify the baseline:
   - `go build ./...` and `go test ./... -count=1` (all `ok`).
   - Anything red: report it first, before any planned work.
6. If the state file lists "In progress" work, read the files it names and check that the failing test/command it mentions still behaves as described.
7. Report to Master in Indonesian, at most 15 lines, in this order:
   - Fase dan status (what is done, what is in progress).
   - HEAD sekarang dan apakah sama dengan catatan.
   - Hasil build/test baseline.
   - Tugas berikutnya (first unchecked task in the task file).
   - Hambatan atau pertanyaan untuk Master (only real ones).
   - Penyimpangan yang ditemukan (state file vs git vs docs), if any.
   Then ask: "Lanjut, Master?" and wait.

Do not run `/implement-task` or any other workflow until Master says "lanjut" (or names the task).
