# xdns - STATE (single source of truth for live status)

Update this file at the end of every task and in `/handoff`. If git disagrees with this file, git is right: fix this file.

Last updated: 2026-10-18 by Claude Sonnet 5.5 (creation of the `.agents` pack).

## Snapshot

- **Phase:** 0 is done, reviewed and committed. **Phase 1 has NOT started** - it waits for Master to say "lanjut".
- **Last verified code HEAD:** `4b546fc` on `main` (all Go code, docs and configs are unchanged since). Two later commits only add `.agents/` (`docs: add .agents fallback pack`) and `.claude/` (`chore: track .claude agents and rules`). Working tree clean, no remote; the code exists only on this PC. A `/resume` HEAD that differs from `4b546fc` only by those two commits is expected.
- **Who has been working:** Claude Code (main session Sonnet 5.5; Opus agents `xdns-core` for hard parts and `xdns-reviewer` for the phase review, both in `.claude/agents/`, never invoked yet). Fallback agent: Gemini 4 Argon, used only if Claude's usage limit is hit.
- **Gates:** full gate set was last run at the end of Phase 0 (all green, 92 tests, `govulncheck` 0 affected). On 2026-10-18 after that, `go test -race ./...` was run once and passed (nothing concurrent exists yet).

## Done (Phase 0 - foundation)

Repo + module `xdns` (`go 1.26.9`); strict YAML config loader with defaults and validation (`internal/config`); `xdns check -config <file>`; `configs/xdns.example.yaml`; README; docs (`architecture.md` v1.1, `roadmap.md` v1.1, `research.md`, `phase-0-report.md`); fresh-context review (0 Critical, 4 Important, all fixed); Go 1.26.9 + x/net v0.60.0 security bump. 11 commits.

Also done since: w64devkit installed, race detector works (see `rules/06-environment.md`); Claude agent files updated accordingly; this `.agents` pack created.

## In progress

- Nothing. (If you stop mid-task, write here: task id, what is done, the exact failing test/command, and what you would do next.)

## Next

1. Master says "lanjut".
2. `/resume` baseline check, then `/spike-dnsproxy` (task P1-01), then continue with `state/PHASE-1-TASKS.md` in order.

## Blockers / waiting for Master

- "lanjut" for Phase 1.
- Gate G2 (start of Phase 3): Does Master use Mobile Hotspot, Hyper-V or WSL on this PC? Can Master open the router settings (DHCP reservation)? Which browser on the phone?
- Gate G3 (start of Phase 6): VPS provider, cost approval, subdomain name.
- Pushing to GitHub or any remote: only with Master's explicit yes.
- Master's unanswered side question: why `indirect` appears in `go.mod` line 15 (answer: it marks a module that our dependencies need but our own code does not import). One sentence is enough when it comes up.

## Docs to write at phase end (do NOT edit `docs/` before the docs pass)

- `docs/architecture.md` section 12 (line ~391) and section 15 (line ~444) still say "no gcc, `go test -race` unavailable". Now false: w64devkit + `CGO_ENABLED=1` per command works.
- `docs/architecture.md` section 18: record the verified answers from `notes/dnsproxy-api-facts.md` (items 1 and 2) when the spike is done.
- Registry import cycle (see `notes/open-questions.md` Q1): document whichever resolution is chosen in section 4/5.2.
- Phase 1 report `docs/phase-1-report.md`, modeled on `docs/phase-0-report.md` (results table with commit ids, evidence, review findings table, rulings, deferred minors, hand-off to Phase 2).
- `README.md` status line and command list; `docs/roadmap.md` status line and change-history row; `.gitignore` gains `/xdns.yaml` (task P1-11).

## Environment changes since Phase 0

- w64devkit v2.10.1 at `C:\Users\Administrator\tools\w64devkit`; `go env CC` set to its gcc. PATH untouched. `CGO_ENABLED` stays 0 by default.
