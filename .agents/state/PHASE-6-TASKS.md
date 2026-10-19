# Phase 6 - VPS + DoT: task checklist

Derived from `docs/roadmap.md` Phase 6 (tasks 1-10), `docs/phase-5-report.md` ("Serah terima ke Fase 6") and `reviews/phase-4-review.md`. Tick a box only when the task is verified and recorded.

**Do not start until Master says "lanjut".** The first step is gate G3, which needs Master's decision on money.

**Boundaries:** never push without Master's explicit yes. Renting the VPS, buying or pointing a domain, and every change on the VPS or the router need Master's approval first (cost and security risk). The server must not answer the open internet on port 53; public access is DoT on 853 only, and only after the debt in section B is paid and the network-facing review (section D) has no open Critical or Important finding. Home PC rules still hold: never bind `0.0.0.0:53` on this PC, never stop ICS, dev port `127.0.0.1:5300`.

**Carried over from Phase 5 (check first, takes a minute):**

- The home PC's Task Scheduler task `hecate` was verified with a throwaway test task but never through a real boot. After the next PC start, check that hecate is running about a minute later (`powershell -File scripts/autostart.ps1 status`, or a DNS query to `192.168.1.99`). If it is not, read `data\logs\hecate.log` and the task's last result before changing anything.
- Decision by Master (2026-10-19): the 7-day run (criterion S4) is measured here, on the VPS, because the PC is switched off at night.
- Decision by Master (2026-10-19): the home server keeps `allowed_clients: 192.168.1.0/24` (family and friends use it). Per-IP limits matter more than for a one-phone setup.

## A. Gate G3 and the VPS

- [ ] **P6-00 Gate G3: provider, cost, subdomain.** Check current prices (target at most Rp50 thousand a month, region close to Indonesia), give Master ONE recommendation with the monthly cost and what is included, and ask for the subdomain name he wants. Nothing is rented before Master says yes. Record the choice in `STATE.md` and the report.
- [ ] **P6-01 Rent and harden the VPS (Linux, small).** Non-root user, SSH keys only, firewall that allows only DoT (853) and SSH, automatic security updates. Evidence: the firewall rules and an external port scan result.
- [ ] **P6-02 Domain and TLS.** Free subdomain pointing at the VPS; Let's Encrypt certificate with automatic renewal. Private key in `data/tls/` with strict permissions, git-ignored. Evidence: a renewal dry run.

## B. Debt that must be paid before the server is open (do these before the DoT listener goes public)

Write the failing test first (RED), then the fix (GREEN), one commit each. Decide first which backend runs on the VPS: the debt of a backend that is not exposed can stay in the docs, but say so in the report.

- [ ] **P6-03 Limiter overflow and IPv6 keys (review I2).** `native` limiter table of 4,096 clients stops limiting new sources once full (measured: 1,200 of 1,200 answered). Add an overflow bucket for untracked clients or evict the least recently seen entry; key IPv6 by /64 for the limiter, the TCP per-address cap and the per-client upstream gate together. RED idea from the review: `newLimiterAt(200, 5, 4, clock)`; 4 clients send one query, a 5th sends 20 at the same instant; at most 5 allowed.
- [ ] **P6-04 TCP slot held by clients outside the ACL.** About 16.5 minutes per connection (idle 10 s, 100 queries per connection, reconnect allowed). Check the ACL at accept, or close the connection after a REFUSED answer. Both backends.
- [ ] **P6-05 `dnsproxy` parity (only if `dnsproxy` can be exposed).** Per-client-IP TCP connection limit (Q4: a TCP connection holds one `MaxGoroutines` slot shared with UDP) and a per-client rate limiter (`ratelimit` middleware, not a `proxy.Config` option). Also review SO_REUSEPORT on Linux (dnsproxy enables it, so a second instance can bind the same port without an error).
- [ ] **P6-06 Query de-duplication for `native`** (dnsproxy has `PendingRequests`). Identical in-flight queries share one upstream exchange.
- [ ] **P6-07 Optional from the review.** M6: the upstream gate fails fast (a client's 65th concurrent upstream query gets SERVFAIL at once); count log lines "too many upstream queries in flight" during the long run and wait 1-2 s for a slot if any appear. M7: `scripts/compare` stops at the first undecodable log line without saying so; count and report skipped lines. DoT as an upstream for `native` if Master still wants it. From the Phase 5 review (`reviews/phase-5-review.md`): M3/M4, the upstream-failure warning (a flapping link alternates WARN and INFO with no throttle because recovery resets the one-minute throttle, which a test pins on purpose, so decide first which behaviour is wanted; `errBusy` from `native` is counted as an upstream failure; an upstream that answers SERVFAIL never warns); fold the counter and the throttle into one mutex-guarded struct when reworking it.

## C. DoT and the phone

- [ ] **P6-08 Review the access design at the start.** Android Private DNS sends no authentication header, so the planned identification is a secret hostname in the TLS SNI plus rate limits. Re-check the design before building it (`docs/architecture.md` section 13, "Akses publik") and record the ruling.
- [ ] **P6-09 DoT listener (853) on both backends.** TLS 1.2+, certificate from P6-02, client identified by the secret SNI name, unknown SNI refused, per-client rate limit, port 53 closed to the public. Conformance cases for the listener on both backends from one table. Cross-build check `GOOS=linux GOARCH=amd64 go build ./...` stays green.
- [ ] **P6-10 systemd service, logs, config backup.** Service runs as the dedicated user with the low-port bind capability only; operational log bounded (the run log is already capped at 5 MB + one backup); a backup of `hecate.yaml` and `data/rules.txt` that Master can restore.
- [ ] **P6-11 Phone and open-resolver test.** Phone: Private DNS set to the secret hostname; repeat S1 (turtlecute) and S2 on another Wi-Fi and on mobile data. Server: a client with a wrong or missing hostname is refused, and the server does not answer arbitrary clients on 53 or 853 (an external query from a host that is not the phone).

## D. Phase close

- [ ] **P6-12 Seven-day run on the VPS (S4, moved from Phase 5).** Record memory, query count, errors and p95 latency of passed, uncached queries per day; no manual restart; look for "too many upstream queries in flight" (P6-07) and for upstream-failure warnings. Compute the p95 from the raw query log.
- [ ] **P6-13 Network-facing review.** Full review (`hecate-reviewer`, Opus) of the Phase 6 range with attacker-style load against the DoT listener, plus `security-reviewer` on the TLS and identification code. Phase 4 had a cheap review by Master's decision, and Phase 5 used a cheap Sonnet review to spare the Opus quota; this phase opens the server to the internet, so a full review is required before it is opened. Fix Critical and Important findings RED -> GREEN.
- [ ] **P6-14 Gates, docs pass, report.** `/gates` (include `govulncheck` and the race run), `docs/phase-6-report.md`, `docs/architecture.md`, `docs/roadmap.md`, `README.md`, `STATE.md`; create `PHASE-7-TASKS.md` only if Master wants Phase 7 (optional: local web UI, extra lists, Plan B APK).

## Open for Master

- Gate G3 (P6-00): provider, cost, subdomain.
- Whether a full or a cheap review is wanted at the end of this phase (the plan above says full, because the server is exposed).
- Pushing to any remote: only with Master's explicit yes.
