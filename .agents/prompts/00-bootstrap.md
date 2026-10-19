# 00 - Bootstrap prompt (paste as the FIRST message of a new session)

---

Kamu melanjutkan proyek **hecate** milik Master: server DNS pribadi (Go) yang memblokir iklan di browser HP Android, tanpa memaksa SafeSearch dan tanpa aplikasi VPN. Sesi sebelumnya dikerjakan Claude Code dan berhenti (batas pemakaian). Kamu tidak punya riwayat percakapan itu; semua yang kamu butuhkan ada di disk.

You are continuing the hecate project in the workspace `C:\Users\Administrator\Documents\Vscode\hecatedns` (open this folder as the workspace root). Do the following IN ORDER, and do not start any implementation work yet:

1. Read EVERY file in `.agents/rules/` (00 to 06). They are binding. If your tool also loads them automatically, read them anyway to be sure.
2. Read `.agents/state/STATE.md`, then the last 40 lines of `.agents/state/WORKLOG.md`, then `.agents/state/PHASE-6-TASKS.md` (Phase 5 is closed; Phase 6 starts only after Master says "lanjut"; use the newest `PHASE-N-TASKS.md`).
3. Read `.agents/notes/do-not-retry.md` and `.agents/notes/open-questions.md`.
4. Run the procedure in `.agents/workflows/resume.md`: compare `git log --oneline -8` and `git status --short` with the state file, then `go build ./...` and `go test ./... -count=1`.
5. Reply to Master in INDONESIAN, at most 15 lines: fase dan status, HEAD vs catatan, hasil build/test, tugas berikutnya, hambatan/pertanyaan, penyimpangan yang ditemukan. End with "Lanjut, Master?" and WAIT.

Hard rules (the full text is in `.agents/rules/`):
- Address the user as **Master**; reply in Indonesian; answer the exact question first; chat short, long output into files and give the path.
- **Tasks first, documents last.** Do not edit anything under `docs/` or `README.md` until the phase's closing documentation pass (`.agents/workflows/close-phase.md`).
- **TDD**: test first, see it fail for the right reason, then minimum code. Small conventional commits on `main`, no co-author trailer.
- **One independent review per phase** (`.agents/workflows/review-phase.md`), after all tasks and before the next phase.
- Never push, create a remote, change git config, change system settings (PATH, `go env`, firewall, services), or bind port 53. Development port is `127.0.0.1:5300`. Do not edit `.claude/`.
- Never claim something works unless you ran it. Never guess dnsproxy/miekg-dns signatures; read the source (`.agents/workflows/spike-dnsproxy.md`).
- Decide technical details yourself and record them in `.agents/state/WORKLOG.md`; ask Master only for costly-to-reverse doubts, decision gates, money or security risk, and outward-facing actions.
- Race detector: `CGO_ENABLED=1` for that one command only (see `.agents/rules/06-environment.md`).
- Before stopping or when your usage limit is near: run `.agents/workflows/handoff.md`.

Available slash commands: `/resume`, `/implement-task`, `/gates`, `/review-phase`, `/close-phase`, `/handoff`, `/debug`, `/spike-dnsproxy`. Skills: `hecate-go-tdd`, `hecate-block-semantics`.

---
