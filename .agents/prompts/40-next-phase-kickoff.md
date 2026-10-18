# 40 - Kick off the next phase (template)

Use after Master says "lanjut" at the end of a phase. Replace `<N>`. Phase task lists live in `docs/roadmap.md`; the working checklist is `.agents/state/PHASE-<N>-TASKS.md` (created by `/close-phase` of the previous phase; if it is missing, create it first from the roadmap in the style of `PHASE-1-TASKS.md`).

---

Master bilang "lanjut" untuk **Fase <N>**.

1. Run `.agents/workflows/resume.md` (baseline must be green, HEAD must match the state file).
2. Read the Phase <N> section of `docs/roadmap.md`, the architecture sections it cites, and `.agents/state/PHASE-<N>-TASKS.md`. Check whether a decision gate applies at the start of this phase (see the roadmap table "Gerbang keputusan"): if yes, ask Master the gate questions from `.agents/notes/open-questions.md` first, and WAIT for the answers.
3. If the phase needs facts from outside the repo (an API, a list format, a service), do a short spike and write the facts into `.agents/notes/` before coding (pattern: `workflows/spike-dnsproxy.md`).
4. Work the tasks in order with `.agents/workflows/implement-task.md`, tests first, one commit per task, task file + WORKLOG + STATE updated after each. No `docs/` edits during the phase.
5. When all tasks are done: `/gates`, then STOP and tell Master the tasks are finished so the independent review (prompt 20) can start in a fresh session.

## Phase-specific reminders

- **Phase 2 (lists, CNAME, special names):** lists are untrusted input (https only, 30 s timeout, 64 MB cap, atomic write, metadata file, keep the old copy on failure); the CNAME check must also run on cache hits; HTTPS/SVCB on blocked names gets NODATA; the list downloader must not depend on xdns's own resolver loop (Phase 0 ruling 12); `xdns check` includes the SafeSearch self-check; benchmark the matcher at about 250k entries (targets: < 150 MB process memory, p99 < 50 microseconds). Gate G1 at the end: review block statistics after several hours of PC use and tune lists (Master involved).
- **Phase 3 (phone test):** starts with gate G2 (UDP 53 / ICS). Ask Master: Mobile Hotspot / Hyper-V / WSL in use? router access for DHCP reservation? phone browser? Do not stop ICS, edit the firewall or bind port 53 before Master answers. Results go to `docs/test-report.md` at the end.
- **Phase 4 (native backend):** the conformance suite must be green on both backends from one constructor table; compare block/pass decisions on a 1,000-domain sample.
- **Phase 6 (VPS + DoT):** starts with gate G3 (provider, cost, subdomain). Public port 53 stays closed; rate limiting is mandatory before public exposure; creating or paying for anything needs Master's explicit yes.

---
