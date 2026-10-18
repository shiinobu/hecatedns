# xdns - WORKLOG (append-only)

Newest entry at the bottom. One entry per task or per working session. Format:

```
## YYYY-MM-DD HH:MM - <agent/model> - <task id or topic>
- Did: ...
- Evidence: <commands run and what they printed, RED -> GREEN lines, coverage, commit ids>
- Decided: <decision> because <reason>   (small rulings; the docs pass copies them into the phase report)
- Problems / not verified: ...
- Next: ...
```

Keep entries short (the full detail lives in code, tests and commits). Never rewrite old entries; add a correcting entry instead.

---

## 2026-10-18 15:40-18:00 - Claude (Sonnet 5.5 / Fable reviewer) - Phase 0 foundation
- Did: research (`docs/research.md`), architecture and roadmap, Go module and skeleton, strict config loader and validation, `xdns check`, example config, README.
- Evidence: gates green at `4b546fc` (92 tests; `cmd/xdns` 97.3%, `config` 96.2%); fresh-context review `509c5d1..5c635a4` found 0 Critical and 4 Important, all fixed RED -> GREEN; `govulncheck` 6 affected on Go 1.26.8 -> 0 after Go 1.26.9 + x/net v0.60.0. Full detail: `docs/phase-0-report.md`.
- Decided: 13 rulings (listed in `docs/phase-0-report.md`); ACL lives in the engine because dnsproxy has none.
- Next: Phase 1, waiting for Master's "lanjut".

## 2026-10-18 18:02-18:25 - Claude (Sonnet 5.5) - model split
- Did: chose "Sonnet main, Opus for hard parts"; created `.claude/agents/xdns-core.md` and `xdns-reviewer.md` (gitignored, never invoked). No product code changed.
- Decided: scout (Sonnet) -> architect (Opus) -> builder (Sonnet) pattern to save usage quota; one Opus review per phase.

## 2026-10-18 later - Claude (Sonnet 5.5) - race detector
- Did: installed w64devkit v2.10.1 (SHA-256 checked against the GitHub digest) to `C:\Users\Administrator\tools\w64devkit`; `go env -w CC=...gcc.exe`; PATH and `CGO_ENABLED` untouched.
- Evidence: `CGO_ENABLED=1 go test -race ./... -count=1` -> both packages `ok`; a deliberate data race in a scratch module printed `WARNING: DATA RACE`; `GOOS=linux GOARCH=amd64 go build ./...` still exits 0.
- Decided: `CGO_ENABLED=1` only per command, never permanent (it would break the Linux cross-build).

## 2026-10-18 later - Claude (Sonnet 5.5) - `.agents` pack
- Did: created this `.agents/` fallback pack (rules, workflows, skills, roles, prompts, notes, state) for continuing with Gemini 4 Argon if Claude's usage limit is hit.
- Problems / not verified: Antigravity's loading of `.agents/` is based on third-party descriptions; nothing in the pack has been executed by another agent yet. Found while writing: the `backend` registry as drawn in `docs/architecture.md` section 5.2 is an import cycle (`notes/open-questions.md` Q1).
- Next: Phase 1 after Master says "lanjut".

## 2026-10-18 18:55+ - Claude (Sonnet 5.5) - commit of the agent folders
- Did: Master asked "commit sekalian .claude". Removed `/.claude/` from `.gitignore` and ignored only `/.claude/settings.local.json` (local setting `ECC_GATEGUARD: off`; a convention-based choice, Master can reverse it). Secret scan over `.claude/` and `.agents/` found only guideline wording, no secrets. Two commits: `docs: add .agents fallback pack` and `chore: track .claude agents and rules` (ids via `git log --oneline -3`). Updated wording that said "untracked/gitignored" in this pack and in Claude's memory. New convention: `.agents/` changes are committed at `/handoff` and `/close-phase` as `chore: update agent state`; phase review diffs exclude the agent folders.
- Evidence: `git status --short` clean after the commits (see the final check in the session).
- Next: Phase 1 after Master says "lanjut".
