# HecateDNS roadmap

Phases 0 to 5 are done. Next is phase 6 (VPS + DoT); phase 7 is optional. Codes `D#`, `S#` and `R#` refer to the decisions, success criteria and risks in [architecture.md](architecture.md).

## Working method

- Each task: write the test first (red), implement the minimum (green), then tidy. Small commits with conventional prefixes (`feat:`, `fix:`, `test:`, `docs:`, `refactor:`, `chore:`).
- All tasks of a phase are done first; one fresh-context review runs per phase before the next one; documents are updated at the end.
- Phase gates: `gofmt -l .` empty, `go vet ./...`, `go test ./... -cover` green, `go test -race ./...` green, `govulncheck` with nothing affecting the code, and `hecate check -config configs/hecate.example.yaml` passing.
- Source code carries no comments; `backend/test/nocomment` enforces it.

## Phases

| Phase | Name | Result | Status |
|---|---|---|---|
| 0 | Foundation | Repository, `go.mod`, package skeleton, strict config loader | done |
| 1 | Core engine and `dnsproxy` backend | DoH forwarder on `127.0.0.1:5300`, custom rules, query log, CLI, client ACL in the engine | done |
| 2 | Lists, CNAME, special names | HaGeZi PRO and Pop-Up lists plus ABPindo (about 250,000 domains), CNAME-chain check, HTTPS/SVCB handling, `hecate check` | done |
| 3 | Real-phone test on home Wi-Fi | S1 95% then 97% on an ad-test page; S3 safe; a Shopee redirect traced and blocked (`s.shopee.co.id`) | done |
| 4 | `native` backend and conformance suite | Backend swap by one config line; same conformance suite green on both; 2,000 sample queries gave identical decisions | done |
| 5 | Stabilisation | Full review of phase 4 and fixes, phone test of `native` (97%), graceful Ctrl+C, consecutive-failure warning, bounded logs, Task Scheduler autostart | done |
| 6 | VPS + DoT | Phone protected outside the home through Android Private DNS | next |
| 7 | Optional | Local web UI, extra lists, Plan B APK | only if wanted |

Changes from the original plan: the 7-day stability run (S4) moved from phase 5 to phase 6 because the home PC is switched off every night; DoT as an upstream was deferred; the Task Scheduler task runs with ordinary rights and a trigger that repeats every minute, because "restart on failure" was tested and does not work.

---

## Phase 6: VPS + DoT

**Goal.** The phone is protected outside the home, on mobile data, through Android's Private DNS.

**Gate G3 (start of the phase).** The owner approves cost and provider. Current prices are checked (target at most Rp50,000 per month, a region near Indonesia) and one recommendation is made. Nothing is rented before the owner agrees.

**Tasks**
1. Order a small Linux VPS; harden it: non-root user, firewall (only DoT and SSH), automatic updates.
2. A domain name: a free subdomain pointing at the VPS.
3. A TLS certificate (Let's Encrypt) with automatic renewal.
4. A DoT listener (853) on both backends; client identification through a secret host name (SNI) and a rate limit; **public port 53 closed**.
5. systemd service, logs, configuration backup.
6. Phone: Private DNS set to the secret host name; test on other Wi-Fi and mobile data (repeat S1 and S2).
7. Verify that the server is not an open resolver (a client without the right host name is refused).
8. **Prerequisite from the phase 1 review:** a TCP slot pool separate from UDP (one LAN host can hold 256 idle TCP connections and stop UDP), a per-IP rate limiter, and a check of `SO_REUSEPORT` behaviour on Linux and Android.
9. **7-day run (S4)** on the always-on VPS: record memory, query count, errors and p95 latency of uncached queries per day, with no manual restart.
10. **Debt from the phase 4 and 5 reviews (required before exposure to the internet):** a full `native` rate-limit table releases new sources (overflow or eviction) and the limiter needs IPv6 `/64` keys, a per-address TCP cap and an upstream gate; TCP connections from clients outside the ACL can persist for about 16 minutes (check the ACL on accept or close after REFUSED); a TCP connection cap and rate limiter for `dnsproxy`; query deduplication for `native`; DoT as an upstream if still wanted; optionally an upstream gate that fails fast, a comparison tool (`cmd/hecate-compare`) that reports malformed lines instead of skipping them, and a better design for the flapping-upstream warning.
11. A full security review of everything that faces the network, before the server is opened.

**Done when.** The phone on mobile data blocks ads by the same criteria as at home, and the server does not answer unauthorised clients.

---

## Phase 7: optional

Only if the owner wants it:

- A local web UI: view the log, block and allow buttons.
- More lists (newly registered or algorithmically generated domains, DoH-bypass lists), basic adblock syntax, EDE (RFC 8914), statistics.
- **Plan B (APK):** a Private DNS switch (light) or a local filter based on the BlockAds approach (see [research.md](research.md), section 9).

---

## Decision gates

| Gate | When | Content |
|---|---|---|
| G1 | End of phase 2 | Review block statistics on the PC and tune the lists (done) |
| G2 | Start of phase 3 | UDP 53 conflict with Windows ICS (resolved: bind to the specific address) |
| G2b | End of phase 3 | If S2 failed: Firefox with uBlock Origin as a complement, discuss Plan B (not triggered) |
| G3 | Start of phase 6 | Choose the VPS, the domain and the cost |
