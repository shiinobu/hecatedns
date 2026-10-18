# xdns - STATE (single source of truth for live status)

Update this file at the end of every task and in `/handoff`. If git disagrees with this file, git is right: fix this file.

Last updated: 2026-10-18 night by Gemini 4 Argon (`Phase 2 Clear` — Phase 2 closed).

## Snapshot

- **Phase:** 2 done (`Phase 2 Clear`), waiting for "lanjut" from Master (plus Gate G1 and Gate G2 before Phase 3).
- **Done in Phase 2:** P2-01 `lists/parser` (`74a646f`), P2-02 `lists/fetch` (`8c31c85`), P2-03 `lists/manager` (`084b377`), P2-04 wire manager into `xdns run` (`a7701ab`), P2-05 `engine/cname` (`8c34703`), P2-06 HTTPS/SVCB and special names (`cb99a20`), P2-07 `selfcheck` + `xdns check` + `xdns lists` (`af34c50`), P2-08 benchmarks (`a264c54`), P2-09 phase gates & review fixes (`e5c18e5`, `d3483b7`, `7fd1ac0`), docs pass (`3112e50`).
- **In progress:** none.
- **Code HEAD:** `3112e50` (`docs: update documents after phase 2 review`, code at `7fd1ac0`).
- **Gates at the end of Phase 2:** `gofmt -l .` empty, `go vet ./...` clean, `go build ./...` clean, `go test ./... -cover -count=1` all `ok` (`cmd/xdns` 92.2%, `backend` 100.0%, `conformance` 81.0%, `dnsproxybe` 95.4%, `config` 95.5%, `engine` 96.8%, `lists` 86.7%, `qlog` 89.1%, `selfcheck` 92.5%), `CGO_ENABLED=1 go test -race ./... -count=1` all `ok`, `go mod verify` and `go mod tidy -diff` clean, `govulncheck` 0 affected (1 uncalled module advisory GO-2026-5932), `xdns check -config configs/xdns.example.yaml` `uji mandiri: LULUS`, Linux cross-build `ok`, live binary check with all 3 real lists (250,089 entries; WorkingSet 71.74–74.20 MiB; lookup p99 2.01 µs) `ok`.
- **Review:** `reviews/phase-2-review.md` (0 Critical, 2 Important, 8 Minor — all fixed with RED -> GREEN in `e5c18e5`, `d3483b7`, `7fd1ac0`). Phase 1 finding **I1 (TCP slots shared with UDP) remains accepted for the home LAN and MUST be fixed before Phase 6** (`notes/open-questions.md` Q4, roadmap Phase 6 task 8).

## Next

1. Wait for Master's "lanjut" (and Gate G2 answers) before starting Phase 3 (`state/PHASE-3-TASKS.md`).

## Blockers / waiting for Master

- Phase 3 start: Master's "lanjut".
- Gate G1 (optional PC use): inspect `xdns log -blocked` after real browsing and tune `allow` rules if needed.
- Gate G2 (start of Phase 3): Is Mobile Hotspot, Hyper-V, or WSL in use on this PC (ICS holds UDP 53)? Does Master have router access for DHCP IP reservation? Which browser is used on the RedMagic 8S Pro phone?
- Gate G3 (Phase 6): VPS provider, cost, subdomain.
- Pushing to any remote: only with Master's explicit yes.
- Master's `indirect` question in `go.mod` (one sentence when it comes up: it marks a module our dependencies need but our code does not import).
- Root `AGENTS.md` pointer to `.agents/README.md` (Master's call, still open).

## Open items carried over

- Q4 (TCP slot exhaustion) and Q5 (concurrent `xdns rule` edits can lose an update): see `notes/open-questions.md`.
- Not verified live yet: real Firefox/Chrome reaction on the RedMagic 8S Pro phone to `use-application-dns.net` (NXDOMAIN), `_dns.resolver.arpa` (NODATA), and `0.0.0.0` (Phase 3); graceful Ctrl+C on a real Windows console; full-disk behavior.

## Environment changes

- w64devkit v2.10.1 at `C:\Users\Administrator\tools\w64devkit`; `go env CC` set to its gcc. PATH untouched. `CGO_ENABLED` stays 0 by default (race: per command only).
- Windows facts that bit us (also in `docs/architecture.md` section 12): `nslookup -port` is ignored (use `scripts/dnsquery.ps1`); excluded UDP port ranges make binds fail with WSAEACCES; renaming over an open file fails; file modes are not enforced.

