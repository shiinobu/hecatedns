# xdns - STATE (single source of truth for live status)

Update this file at the end of every task and in `/handoff`. If git disagrees with this file, git is right: fix this file.

Last updated: 2026-10-18 night by Claude (Sonnet 5.5) (`Phase 3 Clear` - Phase 3 closed).

## Snapshot

- **Phase:** 3 done (`Phase 3 Clear`), waiting for "lanjut" from Master before Phase 4 (`state/PHASE-4-TASKS.md`, native backend).
- **Done in Phase 3 (no Go code changed; code stays at `7fd1ac0`):** P3-01 G2 and UDP 53 (bind on `192.168.1.99:53` works while ICS holds the wildcard; ICS untouched; two narrow firewall rules, group `xdns`), P3-02 router DHCP reservation (PC is now `192.168.1.99`, was DHCP `.10`), P3-03 phone setup (static `192.168.1.150`, DNS 1 `192.168.1.99`, Chrome), P3-04 S1 (`adblock.turtlecute.org` 95% then 97% after two custom rules; `canyoublockit.com` eXtreme blocked everything per Master), P3-05 phone queries reach the log (no DoH lookups; S3 safe from the DNS side), P3-06 Shopee redirect traced to `s.shopee.co.id` and blocked (Play Store redirect never reproduced), P3-07 gates and docs (`docs/phase-3-report.md`, `docs/test-report.md`).
- **In progress:** none.
- **Code HEAD:** code at `7fd1ac0`; Phase 3 docs commit `docs: update documents after phase 3` and the agent-state commit after it (see `git log`). Nothing is pushed (no remote).
- **Gates at the end of Phase 3 (run 2026-10-18, no Go changes since Phase 2):** `gofmt -l .` empty, `go vet ./...` and `go build ./...` clean, `go test ./... -cover -count=1` all `ok` (`cmd/xdns` 91.9%, `backend` 100.0%, `conformance` 81.0%, `dnsproxybe` 95.4%, `config` 95.5%, `engine` 96.8%, `lists` 86.7%, `qlog` 89.1%, `selfcheck` 92.5%), `CGO_ENABLED=1 go test -race ./... -count=1` all `ok`, `go mod verify` clean, `go mod tidy -diff` no diff, `govulncheck` (now at `GOPATH\bin\govulncheck.exe`, not on PATH) 0 affected + 1 uncalled advisory, `xdns check -config configs/xdns.example.yaml` `uji mandiri: LULUS`.
- **Review:** none for Phase 3 (no Go files changed since `3112e50`). Phase 1 finding **I1 (TCP slots shared with UDP) remains accepted for the home LAN and MUST be fixed before Phase 6** (`notes/open-questions.md` Q4, roadmap Phase 6 task 8; also noted in `PHASE-4-TASKS.md` P4-03).
- **Live now (this PC):** a hidden `xdns run` on `192.168.1.99:53` (`bin\xdns.exe`, local gitignored `xdns.yaml`, rules in `data/rules.txt`: `block ad.doubleclick.net`, `block adservice.google.com`, `block s.shopee.co.id`). Stop with `Stop-Process -Name xdns`. Firewall rules: remove with `Remove-NetFirewallRule -Group xdns`. The phone keeps static IP `192.168.1.150` with DNS 1 `192.168.1.99`; if xdns is stopped the phone has no DNS (DNS 2 is empty on purpose) until the phone is set back to DHCP.

## Next

1. Wait for Master's "lanjut", then start Phase 4 at P4-01 (`state/PHASE-4-TASKS.md`). No decision gate is expected before Phase 6.

## Blockers / waiting for Master

- Phase 4 start: Master's "lanjut".
- Optional, offered and not answered: restrict `allowed_clients` to the phone only (`192.168.1.150/32`) instead of `192.168.1.0/24`.
- Gate G1 (optional PC use): inspect `xdns log -blocked` after real browsing and tune `allow` rules if needed (the Shopee short-link block means `s.shopee.co.id/...` links do not open on Master's phone; undo with `xdns rule remove block s.shopee.co.id`).
- Gate G3 (Phase 6): VPS provider, cost, subdomain.
- Pushing to any remote: only with Master's explicit yes.

## Open items carried over

- Q4 (TCP slot exhaustion) and Q5 (concurrent `xdns rule` edits can lose an update): see `notes/open-questions.md`.
- Not verified yet: Play Store redirect (never appeared), Firefox on the phone, IPv6 router DNS as a bypass path, EDE (RFC 8914), a complete "without xdns" baseline (the 5% screenshot was taken mid-test), graceful Ctrl+C on a real Windows console, full-disk behavior. Details in `docs/test-report.md` section 5.
- Four turtlecute items still pass (probably `googletagmanager`, `mopinion`, `iubenda`, `fluidplayer`; inference); Master chose to leave them.
- `.agents/` commit policy (Master, 2026-10-18): do not commit tiny state updates one by one; commit them once at a checkpoint (handoff, phase close).

## Environment changes

- w64devkit v2.10.1 at `C:\Users\Administrator\tools\w64devkit`; `go env CC` set to its gcc. PATH untouched. `CGO_ENABLED` stays 0 by default (race: per command only).
- `govulncheck` installed with `go install` into `GOPATH\bin` (not on PATH); run it as `& "$(go env GOPATH)\bin\govulncheck.exe" ./...`.
- Windows firewall: inbound rules `xdns UDP 53` and `xdns TCP 53` (group `xdns`) for `bin\xdns.exe`, local `192.168.1.99`, remote `LocalSubnet`. The Wi-Fi network category on the PC is Public.
- Router ZTE F663NV3a (`http://192.168.1.1`, Network-LAN-DHCP Binding): the PC MAC is reserved at `192.168.1.99`. Do not put the PC address into the router's DNS settings (that would send the whole family through xdns).
- Windows facts that bit us (also in `docs/architecture.md` section 12): `nslookup -port` is ignored (use `scripts/dnsquery.ps1`); excluded UDP port ranges make binds fail with WSAEACCES; renaming over an open file fails; file modes are not enforced. The phone clock is 12-hour, so a screenshot at "11:19" is 23:19 in the log.
