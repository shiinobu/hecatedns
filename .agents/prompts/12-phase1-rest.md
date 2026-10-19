# 12 - Phase 1, steps 7-12: qlog, backend, conformance, CLI, hardening, phase gates (tasks P1-07 .. P1-12)

Use after the engine (P1-06) is done and committed. Routine work plus the adapter; apply `core-engineer` care to P1-08 only. Paste everything between the lines.

---

Kerjakan tugas **P1-07 sampai P1-12** dari `.agents/state/PHASE-1-TASKS.md` berurutan, memakai `.agents/workflows/implement-task.md` untuk tiap tugas.

Order and notes:
1. **P1-07 qlog** - JSONL writer + reader with a fake clock in tests; a full buffer drops records and counts them, a query never waits for disk; sanitize logged strings; no ring buffer (Phase 7). The writer implements the logger interface that `engine` defines.
2. **P1-08 backend + dnsproxybe** - first decide and record the registry import-cycle ruling (`.agents/notes/open-questions.md` Q1); then the adapter strictly from `notes/dnsproxy-api-facts.md` (no memory). Unknown backend name = error at start. The adapter must call `Before`, and use `After`/`Failed` as the spike found possible. If the spike showed `After` cannot be done cleanly, implement the best documented alternative and write the consequence in STATE.md.
3. **P1-09 conformance v1** - one table of backend constructors (only `dnsproxy` now), fake upstream on `127.0.0.1:0`, backend on a random port. Cases: blocked A/AAAA/HTTPS, pass, allow beats block, ACL REFUSED, log record written. No internet.
4. **P1-10 CLI** - `run`, `log`, `rule add|remove|list` (atomic file writes), graceful shutdown on Ctrl+C, Indonesian messages, exit codes like `cmd/hecate/main.go`. `lists` stays a stub. Decide Q3 (refuse an upstream that equals our own listen address at run time) and record it.
5. **P1-11 config hardening** - each rule from the task line gets a failing test first; add `/hecate.yaml` to `.gitignore`.
6. **P1-12 phase gates** - `/gates` fully green (including race and `govulncheck` 0 affected), then verify the "done when" list from the task file on the REAL binary using a scratch config (never port 53): `hecate run` on `127.0.0.1:5300`, `nslookup -port=5300 example.com 127.0.0.1` over DoH, `hecate rule add block contoh.test` answering `0.0.0.0` within 10 s without restart, both queries in `hecate log`, a client outside `allowed_clients` getting REFUSED (use a scratch config whose `allowed_clients` excludes loopback). Record the exact commands and outputs in the WORKLOG.

Rules recap: tests first; one commit per task; tick boxes; WORKLOG + STATE updates; no `docs/` edits; no push. After P1-12 STOP and report to Master (Indonesian, <= 12 lines) that the phase tasks are done and the independent review is next. Do not review your own work in the same session: use prompt 20 in a fresh session.

---
