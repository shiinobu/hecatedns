# Phase 4 review (full, P5-00)

Reviewer: `xdns-reviewer` (fresh context, read-only), 2026-10-19. Range `7fd1ac0..HEAD` (HEAD `4c7220e`): all of `internal/backend/native/`, the conformance changes, the registry change in `cmd/xdns/run.go`, and `scripts/compare/main.go`. Report saved by the main session; status column at the end tracks the fixes.

The live server (PID 22184, `192.168.1.99:53`) was not touched. The reviewer's experiments ran on `127.0.0.1:5341-5344` (fake upstream on 5399) and were stopped by PID.

## Verdict

0 Critical, 2 Important, 7 Minor. Engine contract holds, concurrency is clean, resource limits work on the real binary, both backends pass the same 15 conformance cases. Two of the "known and accepted" items rest on wrong reasoning (ACL at TCP accept, private ARPA). Private ARPA affects LAN clients today.

## Gates (run by the reviewer on `4c7220e`)

`gofmt -l .` empty; `go vet ./...`, `go build ./...` and the linux/amd64 cross-build clean; `go test ./... -cover -count=1` all ok (`cmd/xdns` 92.2%, `backend` 100%, `conformance` 80.9%, `dnsproxybe` 95.4%, `native` 93.6%, `config` 95.5%, `engine` 96.8%, `lists` 86.7%, `qlog` 89.1%, `selfcheck` 92.5%, `scripts/compare` 0%); `CGO_ENABLED=1 go test -race ./...` all ok, 0 races; `go mod verify` and `go mod tidy -diff` clean; `govulncheck` 0 affecting, 1 uncalled advisory; `xdns check -config configs/xdns.example.yaml` passes.

## Important

**I1 - fix now - `native/bootstrap.go:32-48,94-103`, `upstream_parse.go:64-71`, `upstream_doh.go:80`.** Bootstrap servers and the DoH upstream share `upstreamTimeout`, and the bootstrap lookup runs inside the DoH request context. If the first bootstrap server does not answer, it uses the whole deadline: the second server is never asked, AAAA and server 2 fail on the expired context. The dial loop over resolved IPs has the same problem (a blackholed first IP uses the deadline). Proof: scratch test with `newBootstrap([dead, good], testTimeout)` and a hostname DoH upstream: `context deadline exceeded` after 2.0 s, the good server asked 0 times. `TestBootstrap_FallsBackToTheNextServer` passes only because it calls `lookup` directly with `context.Background()`. Scenario: `https://dns.google/dns-query` with `bootstrap: [1.1.1.1, 8.8.8.8]` and UDP to 1.1.1.1 dropped: that upstream is dead although 8.8.8.8 works. Only configs with hostname upstreams (the example config and the live config use IPs; the live `xdns.yaml` was not read by the reviewer). Fix: give each bootstrap server its share of the remaining deadline and ask A/AAAA in parallel, or resolve under a separate budget and cache for the TTL; split or race the dial deadline across IPs. RED test: `TestDoH_HostnameWithDeadFirstBootstrap` in `upstream_test.go` (dead first bootstrap, good second, hostname DoH upstream, `exchange` must succeed).

**I2 - before Phase 6 (Minor on the home LAN) - `native/ratelimit.go:69-78`.** The limiter table holds 4,096 clients; once full of clients active in the last minute, every new source is served with no limit, so filling the table switches the limiter off. Proof on the binary: 4,200 loopback sources, then 1,200 blocked queries from a fresh source: 1,200 of 1,200 answered (control after 66 s idle: 503). Filling costs about 68 queries/s (spoofed UDP, or TCP/DoT from one IPv6 /64). Separate from the /64 key item (an attacker with a /48 has 65,536 /64s). Fix: one shared overflow bucket for untracked clients (for example 200/s, burst 500 total) or evict the least recently seen entry; key IPv6 by /64 at the same time. RED: `newLimiterAt(200, 5, 4, clock)`; 4 clients send one query, a 5th sends 20 at the same instant; at most 5 allowed.

## Minor

- **M1 - fix now - `native/handler.go:221` (`shapeReply`).** Always sets RA=1, so an ACL REFUSED from native carries RA=1, against `architecture.md:251` and `engine.Refuse`; dnsproxy answers RA=0. Proof: native `REFUSED RA=true`, dnsproxy `REFUSED RA=false`. Fix: keep the engine's RA on blocked and refused replies; RA=1 only on upstream, cached and SERVFAIL replies. RED: assert `!resp.RecursionAvailable` in the conformance case `aclRefuses`.
- **M2 - docs and a pinning test; revisit if plain UDP is ever exposed - `native/server.go:61-78` with miekg `server.go:640-660`.** `acceptMsg` only sees the header: a message with a valid header (qdcount=1) and a broken body passes accept, fails unpack, and miekg answers FORMERR before the rate limiter, ACL and log. The docs claim such messages are ignored (`phase-4-report.md:88`, `architecture.md:220`), true only for odd header counts. Proof: a 15-byte truncated-question message from an ACL-denied client got a 12-byte FORMERR; 1,200 of them with the bucket drained got 1,200 replies. Never larger than the request, so not an amplifier. Fix: correct the docs; add a server test pinning "reply size <= request size".
- **M3 - fix now - `native/server.go:267-327`.** `serve` is 61 lines (rule: under 50). Split into a wait phase and a shutdown phase.
- **M4 - fix now (tests), found by mutation.** (a) Deleting `<-l.slots` in the per-IP rejection branch (`server.go:155`) passes all tests; with that bug 64 rejected connections stop TCP for good. `TestLimitListener_CapsConnectionsPerClientAddress` (`server_test.go:323`, limit 10, perIP 2) makes only one rejection. RED: limit 2, perIP 1, make 5 rejected connections, close the accepted one, expect the next to be accepted. (b) Replacing `stopQueries` (`native.go:99`) with a no-op passes all tests, because `TestHandler_ShutdownCancelsQueriesInFlight` only tests `handler.base`. RED: server-level test with an upstream that never answers, one query, cancel `Run`, it must return without error within about 1 s. (c) The weak bootstrap test (see I1).
- **M5 - docs now - `architecture.md:216,219,220`, `phase-4-report.md:74`.** Line 216 says native answers qdcount!=1 with FORMERR, line 220 and the code say ignored. The "5 detik" TCP hold time (line 219, report line 74) is wrong (see ACL at TCP accept below).
- **M6 - optional - `native/gate.go:33-43`, `handler.go:154-157`.** The upstream gate fails fast: a client's 65th concurrent upstream query gets SERVFAIL at once. A page-load burst during a slow upstream (255 queries in 0.5 s with a 300 ms DoH reconnect is about 150 in flight) could fail part of a page. Not seen in practice. Suggestion: count error records containing "too many upstream queries in flight" during the long run; if any, wait 1-2 s for a slot before SERVFAIL.
- **M7 - optional - `scripts/compare/main.go:188-193,290-292`.** No tests; `eachRecord` and `readLog` stop at the first undecodable line and say nothing, so later records show as "missing log record". Fix: count and report skipped lines.

## Known items: reasoning check

- **IPv6 /64 rate-limit key:** reasoning holds (the listener is IPv4-only). The TCP per-IP cap (`server.go:172`) and the per-client upstream gate also use the full address; fix all three together before Phase 6.
- **ACL check at TCP accept: the stated bound is wrong.** Docs say a host outside the ACL holds a slot "5 detik". After its first query the 10 s idle timeout and `MaxTCPQueries=100` apply: one query every 9 s keeps a connection about 5 s + 99 x 10 s = 16.5 min, and the client can reconnect. Proof: a denied client still used the same connection after 16 s (three REFUSED answers 8 s apart). Harmless today (firewall `LocalSubnet` and /24 ACL). Phase 6 fix: check the ACL at accept, or close the TCP connection after a REFUSED answer.
- **Private ARPA parity: the reasoning is wrong and the issue applies today.** dnsproxy answers NXDOMAIN for private reverse lookups also from private clients (no private upstream configured, `architecture.md` 5.3); native forwards them for every client. Proof: `PTR 1.1.168.192.in-addr.arpa` from 127.0.0.1: NXDOMAIN on dnsproxy (5344), forwarded by native (5342). LAN reverse lookups (for example Windows `nslookup` asking the PTR of 192.168.1.99) go to Cloudflare/Google: a small privacy leak, against RFC 6303. Recommendation: fix now. Answer the RFC 6303 locally served zones (10/8, 172.16/12, 192.168/16, 127/8, 169.254/16, fc00::/7, fe80::/10) with NXDOMAIN plus SOA before the cache and log through `After`. RED: conformance case "private PTR never reaches the upstream" (passes on dnsproxy, fails on native).
- **Query de-duplication, DoT upstream/listener, per-IP TCP limit and rate limiter for `dnsproxy`:** reasoning holds.
- **Native rate limit 200/s, burst 500:** fine; a 1,200-query burst got 502-503 answers on the binary.

## Fix before Phase 6 (beyond the deferred items)

1. I2 (overflow bucket or eviction, plus /64 keys for limiter, TCP cap and gate).
2. TCP slot hold by clients outside the ACL.
3. M2, only if plain UDP 53 is ever exposed.
4. I1, if not fixed now; a VPS config is likely to use hostname upstreams.

## Verified correct

Query flow and logging (rate limit -> `Before`/ACL -> ANY/NOTIMP -> cache -> gate -> upstream -> `After`; `Failed` on upstream error and when the gate is full; `blockReply` copies; blocked queries never reach the upstream; CNAME check ran on both the upstream answer and the cache hit). Answer shapes on the binary (A `0.0.0.0` TTL 10, AAAA `::` TTL 10, HTTPS NOERROR with empty answer and SOA, ANY NOTIMP after the ACL; UDP truncation 468 B with TC without EDNS, 1,142 B with EDNS 4096 and never above 1,232, TCP full 8,877 B; cached TTL counts down). Cache (key, copies, TTL clamping, negative caching, no storing of TC/SERVFAIL/REFUSED, OPT stripped, LRU). Upstream (random plain IDs, DoH ID 0, reply checks, no env proxy, no redirects, content type and 65,535-byte cap, TLS 1.2+, TCP retry on truncation, rebuilt upstream query, failover on SERVFAIL/REFUSED). Concurrency (mutexes or atomics, no nested locks, `defer` gate release, clean shutdown, panics recovered by stage, race detector clean). Rules (no file over 800 lines, errors wrapped, no secrets, registry change correct and tested).

## Not verified by the reviewer

Graceful Ctrl+C on a real Windows console (done afterwards by the main session, see `WORKLOG.md`: exit 0 on both backends); real DoH providers and IPv6 upstreams (fakes only); Windows UDP read errors other than `WSAEMSGSIZE`; accuracy of the cache memory estimate under real load; whether the live `xdns.yaml` uses hostname upstreams (it does not: both upstreams are IP addresses); whether tests were written before the code.

## Fix status (updated by the main session)

| ID | Status |
| -- | ------ |
| I1 | Fixed `e10a006`: the lookup and the dial get their own budget (net/http dials with a context that has lost the request deadline, which is why the first attempt at a fix did not help) and each bootstrap server and address gets its share of what is left. RED first (`TestDoH_HostnameStillWorksWhenTheFirstBootstrapServerIsDead`, `TestShareOfDeadline`). Also made the fake DNS helper try up to 1,000 ports: Windows excludes 100-port blocks of TCP ports and a 1-in-25 flake appeared. |
| I2 | Deferred to Phase 6 (see `PHASE-5-TASKS.md`, debt list). |
| M1 | Fixed `1db1d1d`: `shapeReply` leaves RA alone, `resolve` sets it on resolved answers; conformance case `aclRefuses` asserts RA unset (RED on native first), plus a native unit test. |
| M2 | Pin test added `28e0769`; docs correction goes in the P5-06 docs pass. |
| M3 | Fixed `ed9e3d4`: `serve` split into `awaitStop` and `shutdownServers`. |
| M4 | Fixed `ed9e3d4`: both mutations are now caught (checked by repeating the mutation). The server-level shutdown test also exposed a real bug the reviewer could not see: miekg obeys the deadline of a context but not its cancellation, so `stopQueries` did nothing for UDP/TCP upstreams and a shutdown with a silent upstream waited out the timeout. Plain upstream queries now close their connection when the context is cancelled. |
| M5 | Docs, P5-06 pass. |
| M6, M7 | Optional, left open (see the debt list). |
| Private ARPA | Fixed `dcc5347`: native answers PTR/SOA/NS of RFC 6303 locally served reverse names with NXDOMAIN and an SOA, using the same `netutil.IsLocallyServed` as dnsproxy; conformance case on both backends (RED on native first). The engine now counts only an answer that names an upstream as proof of recovery. |
| TCP slot after REFUSED | Deferred to Phase 6. |
