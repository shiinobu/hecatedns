# xdns - STATE (single source of truth for live status)

Update this file at the end of every task and in `/handoff`. If git disagrees with this file, git is right: fix this file.

Last updated: 2026-10-18 night by Gemini 4 Argon (`Phase 2 Clear` — Phase 2 closed).

## Snapshot

- **Phase:** 3 IN PROGRESS (started 2026-10-18 on Master's "lanjut"; G2 closed). Done: P3-01 to P3-04 (phone is `192.168.1.150`; S1 95% with xdns). `xdns run` is running hidden on `192.168.1.99:53` (PID 5832 at the time; stop with `Stop-Process -Name xdns`); firewall rules group `xdns` exist (remove with `Remove-NetFirewallRule -Group xdns`). Next: P3-05 (IPv6 bypass, S3 from phone), P3-06 redirect hunting, P3-07.
- **Done in Phase 2:** P2-01 `lists/parser` (`74a646f`), P2-02 `lists/fetch` (`8c31c85`), P2-03 `lists/manager` (`084b377`), P2-04 wire manager into `xdns run` (`a7701ab`), P2-05 `engine/cname` (`8c34703`), P2-06 HTTPS/SVCB and special names (`cb99a20`), P2-07 `selfcheck` + `xdns check` + `xdns lists` (`af34c50`), P2-08 benchmarks (`a264c54`), P2-09 phase gates & review fixes (`e5c18e5`, `d3483b7`, `7fd1ac0`), docs pass (`3112e50`).
- **In progress:** none.
- **Code HEAD:** `3112e50` (`docs: update documents after phase 2 review`, code at `7fd1ac0`).
- **Gates at the end of Phase 2:** `gofmt -l .` empty, `go vet ./...` clean, `go build ./...` clean, `go test ./... -cover -count=1` all `ok` (`cmd/xdns` 92.2%, `backend` 100.0%, `conformance` 81.0%, `dnsproxybe` 95.4%, `config` 95.5%, `engine` 96.8%, `lists` 86.7%, `qlog` 89.1%, `selfcheck` 92.5%), `CGO_ENABLED=1 go test -race ./... -count=1` all `ok`, `go mod verify` and `go mod tidy -diff` clean, `govulncheck` 0 affected (1 uncalled module advisory GO-2026-5932), `xdns check -config configs/xdns.example.yaml` `uji mandiri: LULUS`, Linux cross-build `ok`, live binary check with all 3 real lists (250,089 entries; WorkingSet 71.74–74.20 MiB; lookup p99 2.01 µs) `ok`.
- **Review:** `reviews/phase-2-review.md` (0 Critical, 2 Important, 8 Minor — all fixed with RED -> GREEN in `e5c18e5`, `d3483b7`, `7fd1ac0`). Phase 1 finding **I1 (TCP slots shared with UDP) remains accepted for the home LAN and MUST be fixed before Phase 6** (`notes/open-questions.md` Q4, roadmap Phase 6 task 8).

## Next

1. Continue Phase 3 at P3-03 (`state/PHASE-3-TASKS.md`): guide Master through the phone DNS setup, then P3-04 to P3-07.

## Blockers / waiting for Master

- Phase 3 start: Master's "lanjut".
- Gate G1 (optional PC use): inspect `xdns log -blocked` after real browsing and tune `allow` rules if needed.
- Gate G2 (start of Phase 3), answered 2026-10-18: (1) checked by Claude, read-only: Hyper-V is enabled and WSL2 is installed; the Windows `SharedAccess` (ICS) service runs in svchost PID 3620 and holds `0.0.0.0:53` UDP (Hyper-V "Default Switch" NAT/DNS, `vEthernet` 172.25.144.1); Mobile Hotspot service `icssvc` is stopped; no excluded UDP range covers 53. So xdns cannot bind wildcard port 53 on this PC; binding the Wi-Fi address `192.168.1.99:53` is untested (needs Master's OK to bind port 53). (2) router access: YES, Master can open the router at `192.168.1.1` and will reserve the PC's DHCP address (PC Wi-Fi IP now reserved at `192.168.1.99` (was DHCP `192.168.1.10`; verified with Get-NetIPAddress after release/renew), gateway `192.168.1.1`); Master confirmed the PC Wi-Fi and the phone are on the same Wi-Fi network. (3) Browser for the first phone test: Google Chrome. (4) Master's permission (2026-10-18): xdns may be bound to port 53 on the PC's Wi-Fi address `192.168.1.99` only, for Phase 3 testing; never wildcard `0.0.0.0:53` and never touch ICS/Hyper-V settings. G2 is fully answered; Phase 3 still waits for "lanjut".
- Gate G3 (Phase 6): VPS provider, cost, subdomain.
- Pushing to any remote: only with Master's explicit yes.
- (Closed 2026-10-18) Root `AGENTS.md` pointer to `.agents/README.md` created at Master's request; `indirect` in `go.mod` explained to Master (14 modules, pulled in by dnsproxy and by yaml.v3's test dependencies; our code imports only dnsproxy, miekg/dns and yaml.v3).
- (Closed 2026-10-18) `govulncheck` installed to `GOPATH\bin` (not on PATH) and run by Claude: 0 affected, 1 uncalled module advisory, matching the Phase 2 gate.

## Open items carried over

- Docs pass (end of Phase 3): the PC LAN IP is now `192.168.1.99` (was `.10`) in `docs/architecture.md` (lines ~344, ~413), `docs/roadmap.md` (Phase 3), `docs/phase-2-report.md`, `configs/xdns.example.yaml` comment and `scripts/dnsquery.ps1` usage comment.
- Q4 (TCP slot exhaustion) and Q5 (concurrent `xdns rule` edits can lose an update): see `notes/open-questions.md`.
- Not verified live yet: real Firefox/Chrome reaction on the RedMagic 8S Pro phone to `use-application-dns.net` (NXDOMAIN), `_dns.resolver.arpa` (NODATA), and `0.0.0.0` (Phase 3); graceful Ctrl+C on a real Windows console; full-disk behavior.

## Environment changes

- w64devkit v2.10.1 at `C:\Users\Administrator\tools\w64devkit`; `go env CC` set to its gcc. PATH untouched. `CGO_ENABLED` stays 0 by default (race: per command only).
- Windows facts that bit us (also in `docs/architecture.md` section 12): `nslookup -port` is ignored (use `scripts/dnsquery.ps1`); excluded UDP port ranges make binds fail with WSAEACCES; renaming over an open file fails; file modes are not enforced.

