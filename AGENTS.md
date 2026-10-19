# AGENTS.md - HecateDNS

Personal DNS-only browser ad blocker in Go (HecateDNS, binary `hecate`, formerly `xdns`), owned by Master. Any AI coding agent working in this repo (Claude Code, Gemini, Codex, ...) starts here.

1. Read `.agents/README.md` first. It lists the binding rules (`.agents/rules/`), the live status (`.agents/state/STATE.md`) and the current phase's task file.
2. Address the user as **Master** and reply in Indonesian; keep chat short, put long output in files.
3. Design truth lives in `docs/` (Indonesian). `.agents/` is the operational summary, not a replacement.
4. Never push, never touch system settings, never bind port 53 (dev port is `127.0.0.1:5300`). `.claude/` belongs to Claude Code; other agents must not edit it.

Dokumen ini hanya penunjuk. Aturan lengkap ada di `.agents/rules/`.
