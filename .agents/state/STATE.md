# xdns - STATE (single source of truth for live status)

Update this file at the end of every task and in `/handoff`. If git disagrees with this file, git is right: fix this file.

Last updated: 2026-10-18 evening by Claude Sonnet 5.5 (Phase 1 in progress).

## Snapshot

- **Phase:** 1 (engine core + dnsproxy backend) IN PROGRESS. Master said "kerjakan Phase 1" and left it to Claude.
- **Code HEAD:** see `git log --oneline -15`. Phase 1 commits so far: matcher, rules, response, acl, engine record seam, qlog, config hardening, `xdns rule`, `xdns log`.
- **Who is working:** Claude Code. Main session Sonnet 5.5. Opus agent `xdns-core` was dispatched (in the background) for P1-06 engine (`engine.go`, `lists.go`) and P1-08 backend + dnsproxy adapter; its result is not integrated until it reports back.
- **Gates:** run per task on the touched packages (gofmt, vet, tests, race where concurrent). The full gate set + `govulncheck` + the "done when" checks on the real binary run in P1-12.

## Done in Phase 1

P1-00 baseline, P1-01 dnsproxy spike (`notes/dnsproxy-api-facts.md`), P1-02 matcher, P1-03 rules (+ mtime-polled reload), P1-04 response, P1-05 ACL, P1-07 qlog, P1-11 config hardening, and the CLI parts `xdns rule add|remove|list` and `xdns log` of P1-10.

## In progress

- Opus `xdns-core`: P1-06 engine + P1-08 `backend` / `dnsproxybe`. Files it is writing: `internal/engine/engine*.go`, `lists*.go`, `internal/backend/backend*.go`, `internal/backend/dnsproxybe/*`. Do not edit those until it reports; if it is gone and the tree has half-written files, run `go build ./... ; go vet ./...` and finish or revert them.
- Still to do after it: P1-09 conformance v1, P1-10 `xdns run`, P1-12 gates + review.

## Next

1. Integrate the Opus result: run `gofmt`, `go vet`, `go test -race` on engine/backend, read its report, commit its files (engine and backend separately).
2. P1-10 `xdns run` (build engine with ACL, RulesFile, Responder, qlog Writer; registry map in `cmd/xdns`; graceful Ctrl+C; print `Warnings()` and bound addresses).
3. P1-09 conformance v1 (table of backend constructors, fake upstream).
4. P1-12 gates, real-binary checks (`nslookup -port=5300`), `xdns-reviewer` agent over `4b546fc..HEAD` (exclude `.agents` and `.claude`), fixes RED then GREEN, docs pass, report to Master.

## Blockers / waiting for Master

- Nothing for Phase 1. Gate G2 (start of Phase 3): Mobile Hotspot/Hyper-V/WSL in use? router access? phone browser? Gate G3 (Phase 6): VPS provider, cost, subdomain.
- Pushing to any remote: only with Master's explicit yes.
- Master's `indirect` question in `go.mod` (one sentence when it comes up: it marks a module our dependencies need but our code does not import).

## Docs to write at phase end (do NOT edit `docs/` before the docs pass)

- `docs/architecture.md` section 5.3: `Handler` is an interface; the adapter is one wrapping handler (Before -> `p.Resolve` -> After). Section 13: rate limiting is the separate `ratelimit` middleware package, not a `proxy.Config` option. Section 10: `upstreams` comment "dicoba berurutan" is inexact for dnsproxy `load_balance` (weighted random with fail-over). Section 18: record items 1-2 as verified (see `notes/dnsproxy-api-facts.md`).
- `docs/architecture.md` sections 12 (~line 391) and 15 (~line 444): "no gcc / `-race` unavailable" is no longer true.
- Section 4/5.2: registry import cycle (Q1) is resolved by building the name -> factory map in the caller (`cmd/xdns`, conformance), `backend.Registry`; the engine now also has `lists.go` and `record.go`.
- Section 8/9: `xdns log` defaults to the last 10 minutes; `-since` accepts `Nd`; `-around HH:MM -window D` means HH:MM:00 +- D and the most recent HH:MM; `rule add` replaces an earlier rule for the same domain; the log tags are BLOK/LOLOS/GALAT.
- Section 10: a list source without `enabled:` is true; TTL values are capped at 2147483647 s; `listen` entries must be unique with a non-zero port.
- Phase 1 report `docs/phase-1-report.md` (results table with commit ids, evidence, review findings, rulings, deferred minors, hand-off to Phase 2), `README.md` status line and command list, `docs/roadmap.md` status + change-history row.

## Environment changes

- w64devkit v2.10.1 at `C:\Users\Administrator\tools\w64devkit`; `go env CC` set to its gcc. PATH untouched. `CGO_ENABLED` stays 0 by default (race: per command only).
