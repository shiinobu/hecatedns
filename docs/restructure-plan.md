# Restructure plan

Status: **R0-R6 executed on branch `refactor/layout` (2026-10-19); waiting for the owner to approve the structure (R7), then the docs pass (R8).** The home server is stopped on the owner's order.
Date: 2026-10-19. Baseline: `main` at `e33d9fd`, working tree clean, all gates green.

**Execution log.** Answers: D1 = T1, D2 = `.agents/` and `.claude/` stay local only, D3 = no history rewrite. D5, D7 and D9 are still open. Commits on `refactor/layout`: `57a851b` (this plan), `86a45cc` (moves, 107 files, history follows with `git log --follow`), `6da3277` (all comments removed, `backend/test/nocomment` added). After each of them all ten gates pass and coverage equals the baseline (`engine` 97.2 vs 96.9 is the known run-to-run variance). The import-direction check of section 4.3 found no violation, and `hecate check -config hecate.yaml` passes on the real config.

Deviations, to save effort (the owner asked to keep the job short): no separate `registry.go` (`defaultRegistry` stays in `run.go`); no end-to-end test and no `fixtures/` folder (the conformance suite already covers both backends end to end); the `recovered` simplification and the `unparam` hints were skipped; no reviewer agent was run, because the change is mechanical and the gates guard it; no live smoke test of the server (it is stopped).

This is a working document. Once the restructure is finished its content moves into `docs/architecture.md` (section "Project layout") and this file is deleted.

## 1. Goal and scope

Turn the repository into something a stranger can read: a clear layered tree, no process leftovers, English docs only in `docs/`, and no behaviour change.

| In scope | Not in scope |
|---|---|
| Audit of every tracked and local file (used / unused) | Any change to DNS behaviour, config keys or CLI flags |
| New directory layout for `cmd/`, `internal/`, `configs/`, `scripts/` | Phase 6 (VPS + DoT) |
| Where tests live | Removing the scheduled task or firewall rules, or starting the server again (Phase 6) |
| Removing every comment from Go and PowerShell code (point 11) | New features or new dependencies |
| Whether `.agents/` and `.claude/` belong in the public tree | Pushing (needs the owner's explicit yes) |
| Final docs pass (last step, after the layout is approved) | Rewriting history |

Order of work, fixed by the owner: layout first, **docs last, only after the layout is approved**.

## 2. Decisions needed from the owner

Items marked **default** are what will be done if the owner does not object. Items marked **ask** block the step they belong to.

| # | Decision | Recommendation | Blocks |
|---|---|---|---|
| D1 | **Tests location (point 10).** See section 6: Go cannot compile white-box tests outside the package directory, and all 42 test files are white-box | Option T1: move only what can move into `backend/test/`; keep unit tests beside the code | **ask**, step R4 |
| D2 | Publish `.agents/` and `.claude/`? See section 5 | No. Untrack both, keep them on disk, rewrite `AGENTS.md` as a short English public guide | **ask**, step R8 |
| D3 | History: both folders are already in pushed commits. Rewrite history (force push) or leave it | Leave it. The scan found no credentials, only a LAN address, Windows paths and a phone model | **ask**, step R8 |
| D4 | **No comments in code (point 11).** Applies to every `.go` file (tests included) and both `.ps1` scripts; see section 7. It also removes the 48 `// agy wrote this` handover markers, which are no longer needed | Do it in step R3, after the moves | default |
| D5 | Translate user-facing strings in code (CLI usage, error messages), comments in `configs/hecate.example.yaml` and in `scripts/*.ps1`. Tests that assert Indonesian text change with them | Yes, as a separate commit after the docs pass (step R9) | **ask**, step R9 |
| D6 | `scripts/compare` is a dev tool for comparing two backends; nothing builds or tests it | Keep, move to `cmd/hecate-compare`, drop the unused `err` field | default |
| D7 | Public-repo extras: `LICENSE` (the repo has none, so by default all rights are reserved) and a CI workflow running the gates | Add both; the owner picks the license | **ask**, step R8 |
| D8 | This plan file after completion | Delete it; the facts live in `architecture.md` | default |
| D9 | `configs/hecate.example.yaml` has 9 comment lines that document the config keys. YAML is configuration, not code | Keep them (translated to English). The alternative is to strip them and document every key in `docs/` only | **ask**, step R9 |

## 3. Audit: used and unused

Method: `git ls-files` (181 files), the import graph of every package, `deadcode -test ./...`, `golangci-lint` with `unused` and `unparam`, and a search for references to each path. Read-only; nothing was changed.

### 3.1 Used, keep

| Path | Why it is needed |
|---|---|
| `cmd/hecate/` (6 source files) | The only binary entry point. `go build -o bin\hecate.exe ./cmd/hecate` is what the live server runs |
| `internal/config`, `engine`, `lists`, `qlog`, `runlog`, `selfcheck` | Imported by the binary |
| `internal/backend`, `backend/dnsproxybe`, `backend/native` | Imported by the binary (backend contract and the two implementations) |
| `internal/backend/conformance` (`harness.go`, `suite.go`) | Imported only by its own test, but it is the shared test suite for both backends. Not dead; it is test support |
| `configs/hecate.example.yaml` | Read by `hecate check`, by the README, and by two tests |
| `scripts/autostart.ps1` | Installs the Task Scheduler task on the home server (live) |
| `scripts/dnsquery.ps1` | `nslookup` ignores `-port`; this is the documented test client |
| `scripts/compare/` | On-demand dev tool, see D6 |
| `go.mod`, `go.sum`, `.gitignore`, `.gitattributes`, `README.md`, `AGENTS.md` | Repository basics |

Result of the code scan: **no unused package and no unreachable function in non-test code.** The code base is lean; the cleanup is mostly outside the Go code.

### 3.2 Tracked but not part of the product (candidates for removal)

| Path | Size | Verdict |
|---|---|---|
| `docs/phase-0-report.md` … `phase-5-report.md` | 6 files, 533 lines | Remove (owner's point 4). History keeps them |
| `docs/superpowers/plans/2026-10-18-phase-0-foundation.md` | 175 lines | Remove (owner's point 4) |
| `.agents/` | 46 files | Not for the public tree, see section 5 |
| `.claude/` | 12 files | Not for the public tree, see section 5 |

### 3.3 Local, not tracked

| Path | Verdict |
|---|---|
| `.superpowers/` (an empty `sdd/` folder from a plugin, already gitignored) | Delete |
| `bin/hecate.exe` | Keep. Build output, but the live scheduled task and firewall rules point at this exact path. Never delete or move it |
| `data/`, `hecate.yaml` | Keep. Live runtime data and live config, gitignored |
| `C:\Users\Administrator\hecate-migrate.ps1`, `C:\Users\Administrator\.claude.json.bak-xdns-rename` (outside the repo) | The owner deletes these |

### 3.4 Unused or sloppy inside files

| Where | Finding | Action |
|---|---|---|
| `internal/selfcheck/selfcheck_test.go:57` | `googleSafeSearchHandler` is never called | Remove |
| `cmd/hecate/main_test.go:17` | `const exampleConfig` is never used | Remove |
| `scripts/compare/main.go:46` | struct field `err` is never used | Remove |
| `internal/backend/dnsproxybe/handler.go:107` | `(*handler).recovered` always returns a nil error | Drop the result (small simplification) |
| 4 test helpers (`testLimiter`, `startNative`, `acceptWithin`, one closure) | `unparam` hints: a parameter that never varies or a result nobody reads | Tidy while moving, low value |
| 28 Go files | 48 `// agy wrote this` markers | Removed together with all other comments, see section 7 |
| `docs/architecture.md` section 4 | Describes a `testdata/` folder that does not exist | Fixed in the docs pass |
| `.claude/rules/ecc/common/*.md` (10 files) | Copied from a different project: they mention `npx tsc`, `esbuild`, `docs/implementation-rules.md` and say "this project has no automated test suite". All false here (Go, 11,169 lines of tests) | Remove with `.claude/`, see section 5 |

## 4. Target layout

### 4.1 Tree

```
hecatedns/
├── cmd/
│   ├── hecate/
│   │   └── main.go               # ~10 lines: os.Exit(controller.Execute(...))
│   └── hecate-compare/           # was scripts/compare (dev tool, D6)
├── backend/                      # all Go code that is not an entry point (was internal/)
│   ├── controller/               # CLI commands (was cmd/hecate/*.go)
│   │   ├── controller.go         #   Execute, usage text, exit codes
│   │   ├── registry.go           #   which backends this binary contains
│   │   └── run.go  check.go  lists.go  log.go  rule.go  rule_edit.go
│   ├── core/                     # the DNS servers (was internal/backend)
│   │   ├── core.go               #   Backend, Bound, Deps, Factory, Registry
│   │   ├── dnsproxy/             #   AdGuard dnsproxy adapter (was dnsproxybe)
│   │   └── native/               #   own server, upstream and cache
│   ├── engine/                   # the filter: ACL, rules, matcher, CNAME check, health
│   ├── service/                  # work done around the engine
│   │   ├── lists/                #   download, parse, cache, schedule blocklists
│   │   ├── qlog/                 #   query log writer, reader, retention
│   │   └── selfcheck/            #   `hecate check`
│   ├── lib/                      # no knowledge of DNS decisions
│   │   ├── config/               #   YAML schema, strict loader, validation
│   │   └── runlog/               #   operational log with size cap
│   └── test/                     # black-box tests and fixtures (see section 6)
│       ├── conformance/          #   one suite run against both backends
│       ├── nocomment/            #   gate: fails if any .go file contains a comment (section 7)
│       └── fixtures/             #   small sample lists, rules, configs
├── configs/
│   └── hecate.example.yaml
├── scripts/
│   └── windows/                  # autostart.ps1, dnsquery.ps1 (Linux scripts arrive in Phase 6)
├── docs/                         # section 8
├── go.mod  go.sum  README.md  AGENTS.md  LICENSE (D7)  .gitignore  .gitattributes
└── .github/workflows/ci.yml      # optional, D7
```

Local only, never published: `bin/`, `data/`, `hecate.yaml`, `.agents/`, `.claude/` (if D2 is accepted).

### 4.2 Old path to new path

| Old | New | Notes |
|---|---|---|
| `internal/backend/` | `backend/core/` | package `backend` becomes `core` (directory and package name match) |
| `internal/backend/dnsproxybe/` | `backend/core/dnsproxy/` | package `dnsproxybe` becomes `dnsproxy` |
| `internal/backend/native/` | `backend/core/native/` | |
| `internal/backend/conformance/` | `backend/test/conformance/` | already black-box: uses only exported API |
| `internal/engine/` | `backend/engine/` | |
| `internal/config/` | `backend/lib/config/` | |
| `internal/runlog/` | `backend/lib/runlog/` | |
| `internal/lists/` | `backend/service/lists/` | |
| `internal/qlog/` | `backend/service/qlog/` | |
| `internal/selfcheck/` | `backend/service/selfcheck/` | |
| `cmd/hecate/{check,lists,logcmd,rule,rulesedit,run}.go` | `backend/controller/` | `run()` becomes exported `Execute()`; `logcmd.go` becomes `log.go`, `rulesedit.go` becomes `rule_edit.go` |
| `cmd/hecate/main.go` | `cmd/hecate/main.go` | shrinks to the entry point; the binary path and build command do not change |
| `scripts/compare/` | `cmd/hecate-compare/` | D6 |
| `scripts/*.ps1` | `scripts/windows/` | `autostart.ps1` derives `$Root` as the parent of its own folder, so it needs one more `Split-Path -Parent` |
| `configs/hecate.example.yaml` | unchanged | |

The module path stays `github.com/shiinobu/hecatedns`; only import paths below it change. Note that moving out of `internal/` makes the packages importable by other modules. That is acceptable for an application, and it is the layout the owner asked for.

### 4.3 Dependency rule

Imports may only point downward. Checked against the current import graph; no cycle appears.

```
cmd/hecate
  └── controller
        ├── core ── core/dnsproxy, core/native
        └── service (lists, qlog, selfcheck)
              └── engine
                    └── lib (config, runlog)
```

- `lib` imports nothing from the project.
- `engine` imports `lib/config` only.
- `service` and `core` import `engine` and `lib`. `selfcheck` also imports `lists` (already true today).
- `controller` may import everything. `test` may import everything.
- Nothing imports `controller`.

Step R5 enforces this with a script that fails on any upward import.

## 5. Should `.agents/` and `.claude/` be public?

**No.** Recommendation for D2:

| Folder | What it is | Why not public |
|---|---|---|
| `.agents/` (46 files) | Fallback pack for another coding agent: prompts, handoff workflows, a 247-line worklog, per-phase review notes, private status | It is process noise to a reader, mostly Indonesian, and holds personal detail (the owner's address form, home network, workflow preferences) |
| `.claude/agents/` (2) | Project subagents for one person's workflow | Not useful to contributors |
| `.claude/rules/ecc/` (10) | A generic third-party rule pack, copied from another project and wrong for this one (section 3.4) | Misleading in a public repo. It is also loaded as project instructions in every Claude session here, so it is worth fixing locally either way |

What to do (step R8, after D2/D3): `git rm -r --cached .agents .claude`, add both to `.gitignore`. The files stay on disk, so the local workflow keeps working. Keep a short English `AGENTS.md` at the root (a widely recognised convention) that states the layout, the build/test commands and the code conventions, with no private preferences. The "address the user as Master, reply in Indonesian" rule is already in Claude's memory and can go in a gitignored local `CLAUDE.md`.

History caveat (D3): untracking changes the tree from the next commit on. The folders stay readable in the commits that are already on GitHub. Removing them from history means a rewrite plus a force push, which is risky and, since the scan found no credentials, not recommended.

## 6. Tests (owner's point 10)

Observed: 42 test files, 11,169 lines, **all in-package ("white-box")**. They use unexported names such as `startNative`, `fakeClock`, `testLimiter` and `(*handler).recovered`.

Why the folder does not matter: `test/` or `backend/test/` makes no difference. Go compiles a test only against the exported API of a package imported from another directory. White-box tests must sit in the package's own directory. So "all tests in one folder" cannot be done literally without either exporting internals or rewriting the tests.

| Option | What moves | Cost and risk |
|---|---|---|
| **T1 (recommended)** | `backend/test/` holds everything that can be black-box: the conformance suite, an end-to-end test of the built binary, fixtures, shared helpers. Unit tests stay next to the code as `*_test.go` | Low. It is the standard Go layout and keeps the 93-100% coverage |
| T2 | All 42 files move to `backend/test/<package>/` | High. Either export seams that pollute the public API or rewrite roughly 11k lines as black-box tests. Per-package coverage drops unless measured with `-coverpkg=./...`. This is a phase of its own |
| T3 | T1 plus a classification pass that moves each unit-test file that happens to use only exported names | Medium. Needs a per-file check; likely few files qualify |

The location `backend/test/` (the owner's alternative) is preferred over a root `test/`: it keeps the root short, and all Go code sits under `backend/`. Either works.

If the owner insists on T2: package by package, leaves first (`config`, `runlog`, `engine`, `lists`, `qlog`, `selfcheck`, backends, controller), each package merged only if its coverage under `-coverpkg` matches the baseline in section 9.

Assumption: "`.test`" means the `_test.go` files, not the compiled `*.test` binaries (those are already gitignored).

## 7. Comment-free code (owner's point 11)

Rule: no comments in any Go or PowerShell source, tests included. Decision D4 (default) applies it in step R3, after the moves, so rename detection in git still sees the files as moved.

Measured on `main` at `e33d9fd` (Go, via `go/parser`):

| | Comment blocks | Lines |
|---|---|---|
| Non-test source: doc comments on declarations | 491 | 999 |
| Non-test source: inline and other (includes the 48 `// agy wrote this`) | 56 | 69 |
| Non-test source: package comments | 15 | 32 |
| Test files: doc comments | 73 | 104 |
| Test files: inline and other | 79 | 97 |
| **Total Go** | **714** | **1,301** |
| PowerShell: help block plus `#` lines (`autostart.ps1` 24, `dnsquery.ps1` 17) | n/a | 41 |

Nothing functional hides in comments: no `//go:` directive, no build tag, no `nolint`, no cgo, and nothing in the code reads source line numbers.

Consequences the owner should know:

- No `go doc` text and nothing on pkg.go.dev for the exported API. This is the cost of the rule and is accepted.
- Nine `doc.go` files contain only package comments. They become empty and are deleted.
- The "why" that today lives in comments (fail-open policy, snapshot and locking rules, Windows quirks) leaves the code. Before stripping, an inventory `file:line: text` of all 714 blocks is saved outside the repo; the docs pass (R8) moves the design-relevant ones into `docs/architecture.md` under "Design notes". Test names keep carrying intent.
- The usage text in the two PowerShell help blocks moves to the README and `docs/operations.md`.
- Existing local agent rules conflict with the new rule and must change so no agent adds comments back: `.agents/rules/03-go-standards.md` ("doc comment on every exported symbol"), `.agents/skills/hecate-go-tdd`, `.claude/agents/hecate-core.md`. The public `AGENTS.md` states the new rule.
- `configs/hecate.example.yaml` is configuration, not code. Its 9 comment lines stay unless the owner says otherwise (D9).

Method:

1. Save the inventory (before).
2. Strip Go with a throwaway `go/ast` program kept outside the repo: parse with comments, drop them, print with `go/format`. Not a regex, because `//` and `#` also occur inside strings such as URLs. The program refuses to touch any directive and aborts if it finds one.
3. Delete the nine comment-only `doc.go` files; strip the PowerShell by hand (two files).
4. Add the gate `backend/test/nocomment`: a Go test that parses every `.go` file in the module and fails on any comment. It runs in `go test ./...` and in CI, so the rule stays true after this job.
5. Verify: all gates pass, coverage per package unchanged, `git diff --numstat` shows almost only deletions, and `go vet` stays clean.

## 8. `docs/` (last step, only after the layout is approved)

Rules from the owner: English only, public-oriented, no `phase-N-*`, no `superpowers/`.

```
docs/
├── architecture.md   # English; includes the final "Project layout" from section 4 and "Design notes" harvested from code comments (section 7); fixes stale paths
├── roadmap.md        # English; finished phases as a short list, then Phase 6 and Phase 7
├── research.md       # English; the ad-blocking research and its sources
├── testing.md        # English; test strategy (architecture section 15) plus the phone test results; personal details removed
├── operations.md     # English; running as a service: Windows Task Scheduler (from autostart.ps1 help), Linux systemd in Phase 6
└── restructure-plan.md   # deleted when done (D8)
```

| Action | Files |
|---|---|
| Delete | `phase-0-report.md` … `phase-5-report.md`, `superpowers/` |
| Translate and update | `architecture.md` (538 lines), `roadmap.md` (218), `research.md` (297) |
| Rewrite from two sources | `test-report.md` (88) + architecture section 15 → `testing.md` |
| Rewrite | root `README.md` (English, short: what it is, features, quick start, config, layout, docs links; today's status paragraph is one very long block) and `AGENTS.md` |
| Scrub | LAN IPs, router and phone models, Windows account paths, the owner's form of address |
| Check | No dangling links after the deletions; `git grep` for `internal/`, `xdns`, `phase-` |

## 9. Execution checklist

Work happens on a branch `refactor/layout`, so `main` stays deployable and rollback is "delete the branch". One commit per step. No push without the owner's yes. Gates after every step: `gofmt -l .` empty, `go vet ./...`, `go build ./...`, `go test ./... -count=1`.

**Baseline coverage to preserve** (from the last full run): `cmd/hecate` 92.9, `backend` 100, `conformance` 80.1, `dnsproxybe` 95.4, `native` 93.8, `config` 95.5, `engine` 96.9, `lists` 86.7, `qlog` 90.9, `runlog` 96.0, `selfcheck` 92.5. Run-to-run variance seen so far: about 0.3 points on `engine`.

### R0. Approval and baseline
- [x] Owner approves this plan and answers D1, D2, D3, D5, D7
- [x] `git status` clean, record HEAD, create branch `refactor/layout`
- [x] Re-run all 10 gates on the baseline and save the coverage table
- [x] Live server stopped on the owner's order, 2026-10-19: task `hecate` disabled, process ended, UDP and TCP `192.168.1.99:53` free. The task, the firewall group `hecate` and `bin\hecate.exe` stay in place. The server comes back in Phase 6 (the owner will test it there). Until then the phone's DNS (`192.168.1.99`) does not answer
- [ ] Restore later with: `Enable-ScheduledTask -TaskName hecate; Start-ScheduledTask -TaskName hecate` (elevated). Rebuilding `bin\hecate.exe` is now safe, but keep the path

### R1. Local cleanup (owner's point 2)
- [ ] Owner deletes the files listed in 3.3 (`.superpowers/`, the two files outside the repo)
- [x] Remove the dead items of 3.4 (`googleSafeSearchHandler`, `exampleConfig`, `compare` field `err`)

### R2. Mechanical moves (`git mv` only, plus import rewrites)
Each sub-step: `git mv`, rewrite import paths with `sed` on `github.com/shiinobu/hecatedns/internal/...`, run the gates, commit.
- [x] 2a `lib`: `config`, `runlog`
- [x] 2b `engine`
- [x] 2c `service`: `lists`, `qlog`, `selfcheck`
- [x] 2d `core`: `internal/backend` → `backend/core`, package rename `backend` → `core`; `dnsproxybe` → `dnsproxy` (rename identifiers with `gopls rename` or a careful pass, not a blind replace: the word "backend" also appears in config keys and strings)
- [x] 2e `controller`: move six files, export `Execute`, move `defaultRegistry` into `registry.go`, shrink `cmd/hecate/main.go`
- [x] 2f `cmd/hecate-compare`, `scripts/windows/` (fix `$Root` in `autostart.ps1`)
- [x] 2g Fix relative paths that tests use (`../../configs/hecate.example.yaml`)
- [x] 2h `git log --follow` works on three moved files (proves the history survives)

### R3. Polish
- [x] Save the comment inventory (section 7, step 1), then strip every comment from `.go` and `.ps1` files (D4); delete the nine `doc.go` files
- [x] Add `backend/test/nocomment` and make it pass
- [x] Update the local rules that demand doc comments (section 7)
- [ ] Simplify `recovered`, tidy the `unparam` hints
- [ ] Re-check file sizes (soft ceiling 800 lines) and function sizes in the moved controller

### R4. Tests (depends on D1)
- [x] T1: move `conformance` to `backend/test/conformance`, add `backend/test/fixtures`, add one end-to-end test that builds the binary and queries it on a random loopback port (never port 53)
- [ ] T2/T3 only if the owner chooses them

### R5. Verification
- [x] All 10 gates: `gofmt`, vet, build, `go test ./... -cover -count=1`, `CGO_ENABLED=1 go test -race ./...`, `go mod verify`, `go mod tidy -diff`, `govulncheck`, `hecate check -config configs/hecate.example.yaml`, linux/amd64 cross-build
- [x] Coverage per package equals the baseline within variance
- [x] `backend/test/nocomment` passes; `git diff --numstat` of the stripping commit shows almost only deletions
- [x] Dependency-direction script finds no upward import (rule in 4.3)
- [ ] `git grep -n 'internal/'` finds nothing stale in code, scripts or README
- [x] Build to a scratch folder and run `check -config hecate.yaml` against the real config
- [ ] Smoke test on `127.0.0.1:5300` with `scripts/windows/dnsquery.ps1`: a normal name resolves, a listed ad domain returns `0.0.0.0`
- [x] Same behaviour on both backends (conformance suite passes)

### R6. Review
- [ ] Fresh-context `hecate-reviewer` over the branch (project rule: one review per phase), fix CRITICAL and HIGH, report the rest
- [ ] Local `.agents/` and `.claude/agents/` notes updated to the new paths (they stay on disk even if untracked)

### R7. Owner gate: structure approved
- [ ] Owner reviews the tree and says yes (owner's point 9). Merge `refactor/layout` into `main`
- [ ] The server stays off (see R0). Rebuild `bin\hecate.exe` only if the owner asks; the first live run is part of Phase 6

### R8. Docs and public surface (last)
- [ ] Section 8: delete, translate, rewrite, scrub, link check; add "Design notes" from the comment inventory and `operations.md`
- [ ] D2/D3: untrack `.agents/` and `.claude/`, update `.gitignore`, rewrite `AGENTS.md`
- [ ] D7: add `LICENSE` and the CI workflow
- [ ] D8: delete this file
- [ ] Re-run the gates; one commit per topic

### R9. Optional English pass over code strings (D5)
- [ ] Translate CLI text, error messages, example-config and script comments; update the tests that assert on them; separate commit

## 10. Risks and rollback

| Risk | Mitigation |
|---|---|
| Home DNS is down while the server is off (the phone, and any other device that uses `192.168.1.99`, cannot resolve names on Wi-Fi) | Owner's decision. Reset those devices to automatic DNS, or run the restore command in R0 |
| Design knowledge lost with the comments | Inventory saved before stripping, harvested into `architecture.md` (section 7) |
| Comments come back later | `backend/test/nocomment` fails the build; local agent rules updated |
| Import cycle after a move | Graph checked in 4.3; leaves move first; gates after each sub-step |
| Package rename hits unrelated "backend" text | Use `gopls rename` or review every diff hunk; `backend:` stays a config key |
| History becomes hard to follow | `git mv` in separate commits from content edits; verify with `git log --follow` |
| Coverage drops silently | Baseline table in section 9, compared in R5 |
| `-race` flake seen once earlier in `native -count=2` | Unrelated to the move; if it shows up, re-run on `main` to tell the two apart before blaming the refactor |
| A step goes wrong | Delete the branch; `main` is unchanged and nothing was pushed |
