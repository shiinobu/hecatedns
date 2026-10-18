# .agents - paket cadangan xdns / xdns fallback pack

Backup hand-off pack for the xdns project. If Claude's usage limit runs out, any other AI coding agent (Gemini 4 Argon in Antigravity, Gemini CLI, Codex, ...) can continue from here without the Claude conversation. Written 2026-10-18 by Claude Sonnet 5.5 at Master's request. The pack describes the project as of commit `4b546fc` (end of Phase 0); `state/STATE.md` is always the live status.

## Untuk Master (Bahasa Indonesia)

**Isi.** Aturan proyek, alur kerja (slash command), prompt siap tempel, catatan keputusan, dan status terkini. Semua disusun supaya agent yang tidak tahu apa-apa tentang percakapan sebelumnya tetap bekerja dengan aturan Master yang sama (tugas dulu, dokumen di akhir, satu review per fase, jawab singkat dalam Bahasa Indonesia).

**Cara pakai (Gemini 4 Argon / Antigravity)**
1. Buka folder `C:\Users\Administrator\Documents\Vscode\xdns` sebagai workspace (folder induknya, bukan subfolder).
2. Pesan pertama di sesi baru: tempel seluruh isi `prompts/00-bootstrap.md`.
3. Setelah agent melapor siap, ketik "lanjut". Untuk tugas besar pakai prompt di `prompts/` (urut dari nomor kecil) atau slash command `/resume`, `/implement-task`, `/gates`, `/review-phase`, `/close-phase`, `/handoff`, `/debug`, `/spike-dnsproxy`.
4. Sebelum berhenti, atau saat batas pemakaian hampir habis: ketik `/handoff`. Itu memperbarui `state/STATE.md` dan `state/WORKLOG.md`.
5. Kembali ke Claude: tempel isi `prompts/90-handoff-to-claude.md`. Claude tidak otomatis membaca folder ini; `/resume-session` Claude hanya membaca file sesi di `~/.claude/session-data`.

**Catatan jujur**
- Struktur `rules/`, `workflows/`, dan `skills/<nama>/SKILL.md` saya ambil dari sumber pihak ketiga tentang Antigravity (termasuk batas 12.000 karakter per file rules dan workflows, yang sudah dipenuhi). Saya belum bisa memverifikasi cara versi Antigravity Master memuatnya, jadi `00-bootstrap.md` menyuruh agent membaca semua file `rules/` secara eksplisit.
- Gemini 4 Argon baru saya cari lewat web, bukan pengetahuan saya sendiri. Isi paket ini tidak bergantung pada nama alat atau kemampuan khusus model mana pun.
- Folder ini sudah di-commit ke git bersama `.claude/` (kecuali `.claude/settings.local.json`, pengaturan lokal yang tetap diabaikan git), jadi ikut saat clone atau backup. Perubahan di `.agents/` (terutama `state/` dan `notes/`) di-commit saat `/handoff` dan `/close-phase` dengan pesan `chore: update agent state`, bukan di tiap commit tugas.
- Dokumen di `docs/` (Bahasa Indonesia) tetap sumber kebenaran desain. Paket ini ringkasan operasional, bukan pengganti.
- Pekerjaan agent lain tidak saya awasi. Setelah kembali ke Claude, cek `git log` dan jalankan gate sebelum melanjutkan.

## Layout

```
.agents/
├── README.md
├── rules/                  always-on rules (each file < 12,000 chars)
│   ├── 00-project-brief.md       what xdns is, architecture, repo map, success criteria
│   ├── 01-communication.md       Master, Indonesian, short chat, when to ask
│   ├── 02-workflow.md            Master's work rules, phase cycle, commits, boundaries
│   ├── 03-go-standards.md        code, test and file standards for this repo
│   ├── 04-engine-contract.md     Before/After/Failed contract and query flow
│   ├── 05-security-and-input.md  threat model, untrusted input, resource limits
│   └── 06-environment.md         Windows, Go, shells, gates, race, ports
├── workflows/              slash commands (/resume, /implement-task, ...)
├── skills/                 on-demand knowledge: xdns-go-tdd, xdns-block-semantics
├── roles/                  role briefs: core-engineer, phase-reviewer
├── prompts/                paste-ready prompts, numbered in the order they are needed
├── notes/                  decisions, do-not-retry, open questions, dnsproxy API facts
└── state/                  STATE.md (live status), PHASE-1-TASKS.md, WORKLOG.md, reviews/
```

## Read order for an agent

1. Every file in `rules/`.
2. `state/STATE.md`, then the current phase's task file (now `state/PHASE-6-TASKS.md`; Phase 5 is closed, see `docs/phase-5-report.md`; Phase 6 starts only after Master says "lanjut").
3. `notes/do-not-retry.md` and `notes/open-questions.md`.
4. Only the sections of `docs/architecture.md` and `docs/roadmap.md` that the current task names.

## Keeping the pack current

- Whoever works updates `state/STATE.md` after every task and appends to `state/WORKLOG.md`. Never leave the pack describing a state that git no longer matches.
- Do not edit `docs/` mid-phase (Master's rule). Put docs-worthy facts in the "Docs to write at phase end" list in `state/STATE.md`.
- When a phase closes, the closing agent creates `state/PHASE-N-TASKS.md` for the next phase from `docs/roadmap.md` and archives nothing else.
- `.claude/` belongs to Claude Code (agents, rules; tracked in git except `settings.local.json`). Other agents must not edit it. Its HackHub-era rules do not apply to this project; the relevant parts are distilled into `rules/`.
