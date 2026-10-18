---
description: Close a finished xdns phase - verify gates and review fixes, do the single documentation pass, write the phase report, report to Master, prepare the next phase's task file, then stop.
---

# /close-phase

Run only when ALL of the phase's tasks are ticked, `/review-phase` is done, and Critical and Important findings are fixed (RED -> GREEN) with the gates green again. This is the ONLY time `docs/` is edited (Master's rule: documents last).

## 1. Confirm readiness

- `state/PHASE-N-TASKS.md`: every task and the review/close-out lines are done (except the docs items below).
- `/gates` is green on the current HEAD (include `govulncheck` 0 affected and the race run).
- The phase's "Selesai bila" criteria in `docs/roadmap.md` are each checked on the real binary; list the evidence.

## 2. Documentation pass (one pass; use the list in `state/STATE.md` "Docs to write at phase end")

- `docs/architecture.md`: status line and version bump; section 18 items verified in this phase (move them to the verified record with evidence); fix the known stale lines (section 12 and 15 say there is no gcc / no `-race`); any design change made during the phase (for Phase 1: the registry-cycle ruling in sections 4 and 5.2, dnsproxy facts in 5.3).
- `docs/roadmap.md`: status line; tick the phase; add a row to "Riwayat perubahan".
- `README.md`: status line and the command list for what now works.
- `docs/research.md`: only if new researched facts exist.
- Create `docs/phase-N-report.md` modeled on `docs/phase-0-report.md`: result, task/commit table, verification evidence (exact commands and numbers), review table (finding -> fix test), numbered rulings with risks, deferred minor items, hand-off to the next phase. Indonesian, factual, no claims without evidence.
- Do not rewrite history; add, correct, date.

## 3. Commit

`docs: update documents after phase N review` (one commit; the report and the review file may be in it). Gates need not be re-run for a docs-only commit, but `gofmt -l .` must stay empty.

## 4. Prepare the next phase

- Create `state/PHASE-(N+1)-TASKS.md` from `docs/roadmap.md` in the same style as `PHASE-1-TASKS.md` (ids, tests-first notes, dependencies, "done when").
- Update `state/STATE.md`: phase status, HEAD, gates, open questions for the next gate, clear "Docs to write".
- Add a WORKLOG entry summarizing the phase (what was done, numbers, commit range).

## 5. Report to Master (Indonesian, 10-12 lines) and STOP

Contents: hasil fase (satu kalimat), bukti utama (tes, cakupan, govulncheck, review: jumlah temuan dan statusnya), keputusan yang diambil, hal yang belum terverifikasi, pertanyaan untuk gerbang berikutnya bila ada, path laporan `docs/phase-N-report.md`. Then wait for "lanjut". Do not start the next phase.
