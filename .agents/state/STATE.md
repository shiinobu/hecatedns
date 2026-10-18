# xdns - STATE (single source of truth for live status)

Update this file at the end of every task and in `/handoff`. If git disagrees with this file, git is right: fix this file.

Last updated: 2026-10-18 night by Claude Sonnet 5.5 (Phase 1 closed).

## Snapshot

- **Phase:** 1 (engine core + dnsproxy backend) DONE and reviewed. **Waiting for Master to say "lanjut" before Phase 2.** Do not start Phase 2 on your own.
- **What works now:** `xdns run` serves DNS on the configured `listen` addresses (UDP + TCP) through the dnsproxy backend with a DoH upstream, enforces `allowed_clients` (REFUSED outside), blocks names from custom rules (`xdns rule add|remove|list`, reloaded within `rules.poll_interval`), and writes a capped JSONL query log (`xdns log`). **No blocklists are loaded yet** (Phase 2): only custom rules block.
- **Code HEAD:** see `git log --oneline -20`. Phase 1 = 26 commits since `4b546fc`, then the docs pass.
- **Gates at the end of Phase 1:** gofmt empty, vet clean, `go test ./... -cover -count=1` ok (engine 97.1, backend 100, dnsproxybe 95.4, config 97.3, qlog 89.1, cmd 92.4, conformance 81.9), `-race -count=2` ok, mod verify and tidy clean, govulncheck 0 affected (one advisory in a required module, GO-2026-5932 x/crypto/openpgp, not called), Linux cross-build ok, live smoke test ok.
- **Review:** `reviews/phase-1-review.md` (0 Critical, 4 Important, 12 Minor). I2, I3, I4 and the Minors are fixed. **I1 (TCP slots shared with UDP) is accepted for the home LAN and MUST be fixed before Phase 6** (`notes/open-questions.md` Q4, roadmap Phase 6 task 8).

## Next

1. Wait for Master ("lanjut"). Then start `state/PHASE-2-TASKS.md` (lists, CNAME, special names, `xdns check`).
2. Phase 2 hand-off notes are at the top of `PHASE-2-TASKS.md`; read them first.

## Blockers / waiting for Master

- Phase 2 start: Master's "lanjut".
- Gate G2 (start of Phase 3): Mobile Hotspot/Hyper-V/WSL in use? router access? phone browser? Gate G3 (Phase 6): VPS provider, cost, subdomain.
- Pushing to any remote: only with Master's explicit yes.
- Master's `indirect` question in `go.mod` (one sentence when it comes up: it marks a module our dependencies need but our code does not import).
- Root `AGENTS.md` pointer to `.agents/README.md` (Master's call, still open).

## Open items carried over

- Q3 (an upstream equal to our own listen address loops): decided to handle in Phase 2 task P2-07 as a `Warnings()` entry plus a `selfcheck` failure, not a refusal in `run`.
- Q4 (TCP slot exhaustion) and Q5 (concurrent `xdns rule` edits can lose an update): see `notes/open-questions.md`.
- Not verified live: graceful Ctrl+C on a real Windows console, full-disk behavior.

## Environment changes

- w64devkit v2.10.1 at `C:\Users\Administrator\tools\w64devkit`; `go env CC` set to its gcc. PATH untouched. `CGO_ENABLED` stays 0 by default (race: per command only).
- Windows facts that bit us (also in `docs/architecture.md` section 12): `nslookup -port` is ignored (use `scripts/dnsquery.ps1`); excluded UDP port ranges make binds fail with WSAEACCES; renaming over an open file fails; file modes are not enforced.
