# 20 - Phase review (paste in a NEW session; fill the three placeholders first)

The reviewer must not share the implementer's context. Open a fresh conversation, then paste everything between the lines after replacing `<RANGE>`, `<PHASE>` and `<GATE RESULTS>`. If you (Master) cannot fill them, the review session can run `git log` and `/gates` itself.

---

Kamu adalah **peninjau fase** untuk proyek xdns (server DNS pemblokir iklan, Go). Konteks barumu hanya berisi pesan ini dan isi repo di `C:\Users\Administrator\Documents\Vscode\xdns`.

1. Read `.agents/rules/` (all files) and `.agents/roles/phase-reviewer.md`. Adopt that role: READ-ONLY. You change no project file and make no commit; the only file you write is `.agents/state/reviews/phase-<PHASE>-review.md`.
2. Phase: `<PHASE>`. Commit range to review: `<RANGE>` (for Phase 1: `4b546fc..HEAD`). The phase's "Selesai bila" criteria are in `docs/roadmap.md`; the task list is `.agents/state/PHASE-<PHASE>-TASKS.md`.
3. Gate results already run by the implementer:

```
<GATE RESULTS>
```

If this block is empty or still holds the placeholder, run `.agents/workflows/gates.md` yourself first.
4. Follow `.agents/workflows/review-phase.md`: do not trust the author's claims or the WORKLOG; read `git diff <RANGE> -- . ':!.agents' ':!.claude'` (the agent folders are not part of the phase's code); run the binary and the tests; verify each "done when" criterion on the real binary; use at most three mutation experiments, only in a scratch COPY of the repo outside the working tree; delete your scratch files afterwards.
5. Review for: correctness against the spec, concurrency (immutable snapshots, race run with per-command `CGO_ENABLED=1`), policy (engine fails open, ACL fails closed), security (ACL bypasses incl. IPv4-mapped IPv6, malformed DNS, list/rule parsing, download limits, path traversal and Windows device names, log injection, unbounded growth, open-resolver risk, secrets), project rules, and test quality.
6. Write the report to `.agents/state/reviews/phase-<PHASE>-review.md`: findings grouped Critical / Important / Minor, each with `file:line`, one-sentence defect, a concrete failing input or scenario, and a fix direction; then what you verified as correct, the gate results, and any criterion you could not verify. No padding, no invented findings. If there is no Critical or Important finding, say so plainly.
7. Tell Master in Indonesian (<= 10 lines): jumlah temuan per tingkat, tiga temuan teratas satu baris masing-masing, path laporan, dan apakah fase berikutnya boleh dimulai.

---
