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

## 2026-10-18 evening - Claude (Sonnet 5.5) - Phase 1 start, P1-00 and P1-01
- Did: Master said "kerjakan Phase 1" and left it to Claude (full delegation). P1-00 baseline: HEAD `f8f68a7`, tree clean, `go test ./... -count=1` printed `ok` for `cmd/xdns` and `internal/config`. P1-01 spike: read `proxy/{requesthandler,middleware,config,dnscontext,proxycache,proxy,server,serverudp,exchange,cache,pending,upstreams,upstreammode}.go`, `upstream/{upstream,resolver,doh}.go` and `ratelimit/ratelimit.go` in dnsproxy v0.86.0 and filled `notes/dnsproxy-api-facts.md` (all TODOs replaced, file:line for every signature).
- Evidence: `docs/architecture.md` section 5.3 holds. `Handler` is an interface; the adapter is one wrapping handler: `Before` -> `p.Resolve` (default cache+upstream path) -> `After` on `dctx.Res`. The cache packs the upstream answer into bytes before our replacement, so the CNAME check (Phase 2) will run on cache hits. A DoH upstream given as an IP needs no bootstrap (`upstream/upstream.go:401-408`). Rate limiting is a separate `ratelimit` middleware package, not a `proxy.Config` field. No `recover()` exists in package `proxy`, so a panic in our handler would kill the process.
- Decided: single wrapping handler with its own `recover()` (fail open); `PendingRequests` enabled explicitly (the code default is OFF although the comment says on); keep `DNSSECEnabled`/ECS/optimistic cache off so `Resolve` never mutates `Req`.
- Problems / not verified: items marked `NOT RUN` in the facts file (max_ttl 0 semantics, bind-failure behavior, config build without network) must be proven by P1-08 tests.
- Docs-worthy: architecture 5.3 says "handler function" (it is an interface); section 13 says dnsproxy has a built-in rate-limit option (it is the `ratelimit` middleware package); section 10 comment "dicoba berurutan" is inexact for dnsproxy `load_balance` (weighted random with fail-over).
- Next: P1-02 matcher (Sonnet), P1-03 rules, P1-04 response, P1-05 acl; then Opus `xdns-core` for the engine and the adapter.

## 2026-10-18 evening - Claude (Sonnet 5.5) - P1-02 .. P1-05 (engine foundation)
- Did: `internal/engine` `name.go` (NormalizeName ASCII-only fold, ParseEntry), `matcher.go` (DomainSet suffix match + Matcher atomic swap), `rules.go` + `rulesfile.go` (rule line parser, ParseRules with line-numbered issues, 4 MiB / 4096-byte caps, mtime+size polling RulesFile), `response.go` (Responder with 4 modes, Refuse), `acl.go` (fail-closed ACL, unmaps IPv4-mapped, drops zone), `record.go` (Entry, Recorder seam). Commits `b4fe654` matcher, `b030ecf` rules, `1797d12` response, `ae1f353` acl, `9c5815e` record.
- Evidence: RED = undefined symbols before each implementation; GREEN first run each time. `internal/engine` coverage 95.9%; `-race` clean; `FuzzParseRuleLine` 20 s and `FuzzACLAllows` 10 s without a failure. Mutations in scratch copies were caught (slice-by-char instead of by label, BOM strip removed, mtime check disabled, line cap removed, RA bit dropped, SOA minimum zeroed, AAAA case removed, unmap/zone removed). Two surviving mutants are equivalent (netip.Prefix.Contains already rejects an invalid Addr and masks host bits). Benchmark skeleton: 48 ns/op, 0 allocs at 20k entries.
- Decided: ASCII-only case folding (strings.ToLower maps U+212A Kelvin to `k`); engine imports `config` only for the Mode constants; `Responder` answers FORMERR for requests without exactly one question; OPT mirrored only when the request had one; a missing rules file means no rules, a failed reload keeps the old rules.
- Next: Opus agent `xdns-core` builds P1-06 engine + P1-08 backend/adapter; meanwhile Sonnet does P1-07 qlog and P1-11 config hardening.

## 2026-10-18 evening - Claude (Sonnet 5.5) - P1-07 qlog and P1-11 config hardening (while Opus builds the engine)
- Did: `internal/qlog`: `record.go` (Record in the documented field order, `sanitize`), `writer.go` (Writer = engine.Recorder, bounded queue that drops and counts when full, daily files by local date, idle flush, retention sweep at Open / on rotation / after midnight), `retention.go` (`purgeOld` touches only regular files named exactly `queries-YYYYMMDD.jsonl`), `reader.go` (`Read` with Since/Until/Grep/BlockedOnly, damaged and over-long lines counted, never fatal), `format.go` (`Record.Line`, Indonesian tags BLOK/LOLOS/GALAT), `around.go` (`ParseAround`, `ParseSince` with `Nd`). `internal/config`: source without `enabled:` is true (custom `UnmarshalYAML` with manual unknown-key check because `Node.Decode` is not strict), non-empty `rules.file` and `log.dir` (while logging), unique non-zero listen ports compared as parsed addresses, TTL cap = MaxInt32 seconds for `block.ttl`, `cache.min_ttl`, `cache.max_ttl`; `/xdns.yaml` in `.gitignore`. Commits `f3452f2` qlog, `eeac3ed` config.
- Evidence: qlog coverage 88.9%, `-race` clean, `FuzzSanitize` 10 s clean; config coverage 97.3%. RED first for every group (undefined symbols, then assertion failures for config). Mutants caught: retention off by one, reader since-bound made exclusive, sanitize disabled, idle flush disabled (found a missing test, added `TestWriter_RecordsBecomeReadableWithoutClose`), unknown source key accepted, `enabled` default removed, port 0 accepted.
- Decided: window of `-around HH:MM` is `HH:MM:00 +- window`; `-since` accepts `Nd` days; the log never stores control characters (replaced by `?`); a record after Close is counted as dropped; `purgeOld` ignores directories and look-alike names.
- Not verified: behavior on a full disk (write errors are counted and logged once per minute; no test fills a disk).

## 2026-10-18 evening - Claude (Sonnet 5.5 + Opus xdns-core) - P1-06, P1-08, P1-09, P1-10 (run), P1-12 gates and live checks
- Did: Opus `xdns-core` (one pass, 71 tool calls) wrote `engine/engine.go engine_record.go lists.go`, `backend/backend.go`, `backend/dnsproxybe/{dnsproxybe,config,handler}.go` with tests; the main session re-ran everything itself, read the core files, fixed a flaky qlog test cause (timer armed inside the goroutine; moved to `start()`), wrote `xdns run` (`cmd/xdns/run.go`, registry map built in `cmd/xdns`, fake-backend tests, a failed start is never announced), the conformance suite (`internal/backend/conformance`, 11 cases per backend), and `scripts/dnsquery.ps1`. Commits: `7777774` qlog timer, `261fe49` engine, `a03f1a1` backend+adapter, `78a1310` run, `b5cc616` conformance, `f825312` script.
- Evidence (gates, HEAD `f825312`): `gofmt -l .` empty; `go vet ./...` clean; `go build ./...` ok; `go test ./... -cover -count=1` all ok (cmd 92.2%, backend 100%, conformance 82.0%, dnsproxybe 94.9%, config 97.3%, engine 96.9%, qlog 88.9%); `CGO_ENABLED=1 go test -race ./...` all ok; `go mod verify` ok; `go mod tidy -diff` empty; `govulncheck` 0 affected (1 in a required module not called); Linux cross build exit 0; `xdns check -config configs/xdns.example.yaml` ok. Mutants caught by the conformance suite: Before skipped, After skipped, custom allow disabled.
- Live checks on the real `xdns.exe` (scratch dir, `listen 127.0.0.1:5300`, real DoH to 1.1.1.1/8.8.8.8): `example.com` answered A+AAAA over DoH in 80 ms; `xdns rule add block contoh.test` -> `www.contoh.test` A `0.0.0.0` after 0.88 s, AAAA `::`, HTTPS NOERROR empty; `xdns rule add allow contoh.test` took effect after 1.93 s (S5: 10 s limit); second instance with `allowed_clients: [10.0.0.0/8]` answered REFUSED (rcode 5) and logged `BLOK ... acl`; `xdns log` printed both the passes and the blocks, the allow shows as `allow:allow contoh.test`.
- Decided: priority custom allow > custom block > list allow > list block (the numbered steps in the docs would let a list allow beat a custom block; the owner's rule must win); `MaxGoroutines` 256, upstream timeout 5 s; Windows `nslookup` ignores `-port`, so the Phase 1 check uses `scripts/dnsquery.ps1` (rules/06 and the pack corrected; `docs/roadmap.md` line ~72 still says nslookup, fix in the docs pass).
- Problems / not verified: graceful Ctrl+C on the real Windows console (only the context-cancel path is tested; a background process cannot receive Ctrl+C); the Windows file listing shows size 0 for the log file while the server has it open (real content is readable with `xdns log`); dnsproxy forwards non-QUERY opcodes instead of answering NOTIMP; SO_REUSEPORT on Linux/Android (read in source, Phase 6); `Shutdown` does not wait for in-flight requests; a zero cache size is passed to dnsproxy as is (config validation requires >= 1 when the cache is on).
- Next: fresh-context review by `xdns-reviewer` over `4b546fc..HEAD` (excluding `.agents` and `.claude`), fix Critical/Important RED then GREEN, gates again, docs pass, phase report.

## 2026-10-18 evening - Claude (Sonnet 5.5 + Opus xdns-reviewer) - Phase 1 review and fixes
- Did: fresh-context review by `xdns-reviewer` (107 tool calls, ran the real binary under attack-style load): 0 Critical, 4 Important, 12 Minor; saved verbatim in `state/reviews/phase-1-review.md` with a triage table. Verified and fixed with RED then GREEN: I2 (log cap), I3 (non-QUERY opcodes forwarded), I4 (flaky bind-failure test), M1, M3-M12. I1 accepted as a LAN-only risk (Q4 in `notes/open-questions.md`).
- Evidence: commits `3dbeaa1 c8e351b d35f0ae e9fb444 fc7a1d3 599a526 b626fee` (see the table). Full gates re-run: gofmt, vet, build, `go test ./... -cover -count=1`, `-race -count=2`, mod verify, tidy -diff, govulncheck (0 affected), Linux build, example check: all green. Live smoke test: DoH answer, block `0.0.0.0` (answer now 49 bytes instead of 64), `xdns log` ok.
- Decided: an unknown-ACL-state panic in the adapter answers REFUSED (fail closed) instead of resolving unfiltered; `rule add` replaces and refuses to exceed 4 MiB; the log cap is mandatory (>= 1 MB/day) while the log is on.
- Problems / not verified: graceful Ctrl+C on a real Windows console; behavior on a full disk; `log.max_mb_per_day` interacts with the idle flush only by dropping (tested), not by rotating.
- Next: docs pass (architecture, roadmap, README, example config, `docs/phase-1-report.md`), STATE, `PHASE-2-TASKS.md`, report to Master, then `/save-session`.
