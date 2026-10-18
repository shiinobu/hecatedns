# xdns - STATE (single source of truth for live status)

Update this file at the end of every task and in `/handoff`. If git disagrees with this file, git is right: fix this file.

Last updated: 2026-10-19 night by Claude (Sonnet 5.5) (Phase 4 closed in code; waiting for Master).

## Snapshot

- **Phase:** 4 done (code, tests and the phone test P4-09 with `backend: native`: turtlecute 97%, Shopee blocked, results in `docs/test-report.md` section 5). Next phase is 5 (`state/PHASE-5-TASKS.md`), waiting for "lanjut". The home server now runs `native`.
- **Finding from P4-09, fixed:** native rate limit (100/s, burst 200) dropped 1 query during the turtlecute burst (255 queries in about 0.5 s). Raised to 200/s, burst 500 in `4361b64` (RED test first, comment corrected, `architecture.md` updated); server restarted with the new binary.
- **Done in Phase 4:** `internal/backend/native` (cache, upstream pool with DoH/UDP/TCP and bootstrap, server, handler, rate limiter), registered in `cmd/xdns`; conformance suite (15 cases) green on both backends from one table; `scripts/compare` run on 2,000 queries: 0 decision differences (details `docs/phase-4-report.md`). Bug found and fixed: one oversized UDP datagram stopped miekg's UDP loop on Windows (`tolerantReader`, new suite case, dnsproxy passes it too).
- **Decisions by Master (2026-10-19):** DoT as an upstream deferred; phase review is the cheap kind; the FULL review is owed (first task of Phase 5, at the latest before Phase 6).
- **In progress:** none.
- **Code HEAD:** see `git log` (Phase 4 commits start at `94dd73a feat: add native backend answer cache`). Nothing is pushed (no remote).
- **Gates at the end of Phase 4 (run 2026-10-19):** `gofmt -l .` empty, `go vet ./...` and `go build ./...` clean, `go test ./... -cover -count=1` all `ok` (`native` 93.1%, `conformance` 80.9%, `dnsproxybe` 95.4%, `engine` 96.8%, `cmd/xdns` 92.2%), `CGO_ENABLED=1 go test -race ./... -count=1` all `ok`, `go mod verify` clean, `go mod tidy -diff` no diff, `govulncheck` (`GOPATH\bin\govulncheck.exe`) 0 affected + 1 uncalled advisory, `xdns check -config configs/xdns.example.yaml` passes.
- **Review:** cheap review only (one Sonnet agent, narrow scope), see `docs/phase-4-report.md` section "Review singkat". Phase 1 finding **I1 (TCP slots shared with UDP) remains accepted for the home LAN for `dnsproxy` and MUST be fixed before Phase 6** (Q4; `native` has its own TCP limit).
- **Live now (this PC):** a hidden `xdns run` (**native backend since 2026-10-19 00:29, PID 22184 after the rate-limit restart**; roll back by setting `backend: dnsproxy` in `xdns.yaml` and restarting) on `192.168.1.99:53` (`bin\xdns.exe` rebuilt from HEAD `4361b64`, local gitignored `xdns.yaml`, rules in `data/rules.txt`: `block ad.doubleclick.net`, `block adservice.google.com`, `block s.shopee.co.id`). Stop with `Stop-Process -Name xdns`. Firewall rules: remove with `Remove-NetFirewallRule -Group xdns`. The phone keeps static IP `192.168.1.150` with DNS 1 `192.168.1.99`; if xdns is stopped the phone has no DNS (DNS 2 is empty on purpose) until the phone is set back to DHCP. The Phase 4 test servers on ports 5300-5302 were stopped; temp files are in the session scratchpad only.

## Next

1. Master's "lanjut" for Phase 5 (`state/PHASE-5-TASKS.md`); P5-00 is the full Phase 4 review with `xdns-reviewer`.

## Blockers / waiting for Master

- Phase 5 start: Master's "lanjut". P5-02 (Task Scheduler) needs Master's approval because it changes Windows settings.
- Decided by Master (2026-10-19): keep `allowed_clients` at `192.168.1.0/24` because family and friends on the home Wi-Fi may use the server. Do not narrow it to `/32`. Consequence: the per-IP limits (rate limit, TCP cap, upstream gate) are the protection against one noisy LAN host, so they matter more than for a one-phone setup.
- Gate G1 (optional PC use): inspect `xdns log -blocked` after real browsing and tune `allow` rules if needed (the Shopee short-link block means `s.shopee.co.id/...` links do not open on Master's phone; undo with `xdns rule remove block s.shopee.co.id`).
- Gate G3 (Phase 6): VPS provider, cost, subdomain.
- Pushing to any remote: only with Master's explicit yes.

## Open items carried over

- Q4 (TCP slot exhaustion, dnsproxy) and Q5 (concurrent `xdns rule` edits can lose an update): see `notes/open-questions.md`.
- Not verified yet: phone test with `native`, Play Store redirect (never appeared), Firefox on the phone, IPv6 router DNS as a bypass path, EDE (RFC 8914), a complete "without xdns" baseline, graceful Ctrl+C on a real Windows console, full-disk behavior, DoT upstream (deferred). Details in `docs/test-report.md` section 5 and `docs/phase-4-report.md`.
- Four turtlecute items still pass (probably `googletagmanager`, `mopinion`, `iubenda`, `fluidplayer`; inference); Master chose to leave them.
- `.agents/` commit policy (Master, 2026-10-18): do not commit tiny state updates one by one; commit them once at a checkpoint (handoff, phase close).

## Environment changes

- w64devkit v2.10.1 at `C:\Users\Administrator\tools\w64devkit`; `go env CC` set to its gcc. PATH untouched. `CGO_ENABLED` stays 0 by default (race: per command only).
- `govulncheck` installed with `go install` into `GOPATH\bin` (not on PATH); run it as `& "$(go env GOPATH)\bin\govulncheck.exe" ./...`.
- Windows firewall: inbound rules `xdns UDP 53` and `xdns TCP 53` (group `xdns`) for `bin\xdns.exe`, local `192.168.1.99`, remote `LocalSubnet`. The Wi-Fi network category on the PC is Public.
- Router ZTE F663NV3a (`http://192.168.1.1`, Network-LAN-DHCP Binding): the PC MAC is reserved at `192.168.1.99`. Do not put the PC address into the router's DNS settings (that would send the whole family through xdns).
- Windows facts that bit us (also in `docs/architecture.md` section 12): `nslookup -port` is ignored (use `scripts/dnsquery.ps1`); excluded UDP port ranges make binds fail with WSAEACCES; renaming over an open file fails; file modes are not enforced; a UDP datagram bigger than the read buffer fails the read with WSAEMSGSIZE; a closed UDP socket's port is handed to the next bind (tests that need a free UDP+TCP port pair must keep the failed sockets open). The phone clock is 12-hour, so a screenshot at "11:19" is 23:19 in the log.
- Do not run a plain `python -` heredoc in the shell here: it waits for input and hangs the tool.
