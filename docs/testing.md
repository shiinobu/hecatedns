# Testing

## Automated tests

The project is developed test first: a failing test, the minimum code to pass it, then cleanup. Unit tests sit beside the code they test (they use unexported names). Black-box tests and gates live in `backend/test/`.

| Layer | Content | Target |
|---|---|---|
| Unit | `engine` (matching, priority, block answers, CNAME, special names), `lists` (parser, fetcher, manager), `qlog`, `config`, `native` (cache, ACL, rate limiter with its overflow bucket, de-duplication, upstreams against fake servers, the DoT listener with generated certificates), `controller` (commands, the certificate check) | Coverage of 80% or more |
| Fuzz | List parser and rule parser (`go test -fuzz`) | No panic |
| Benchmark | Lookup against about 250,000 entries; process memory | p99 under 50 microseconds; under 150 MB |
| Conformance | `backend/test/conformance`: a fake upstream (`miekg/dns` on loopback) plus the backend under test on a random port. Cases: blocked A/AAAA/HTTPS, pass-through, `allow` beats `block`, blocked CNAME, ACL REFUSED (no RA bit), log written, oversized or malformed UDP datagrams do not stop the server, reverse lookups of private addresses answered locally, a TCP client outside `allowed_clients` is disconnected with no answer. Run for **both** backends from one constructor table (DoT is `native` only, so its cases are unit tests) | Identical results |
| Source rule | `backend/test/nocomment` fails if a Go or PowerShell source contains a comment | Always green |
| Manual integration | `scripts/windows/dnsquery.ps1` (or `dig`) from the PC, then tests from a phone | Criteria S1 to S3 |

Coverage at the last full run: `controller` 93.4%, `core` 100%, `core/dnsproxy` 95.6%, `core/native` 92.7%, `engine` 97.2%, `lib/config` 97.0%, `lib/runlog` 96.0%, `service/lists` 86.7%, `service/qlog` 90.9%, `service/selfcheck` 92.5%, `test/conformance` 79.7%.

Verification commands (all must be clean before a merge):

```
gofmt -l .
go vet ./...
go build ./...
go test ./... -cover -count=1
CGO_ENABLED=1 go test -race ./... -count=1
go mod verify
go mod tidy -diff
govulncheck ./...
go run ./cmd/hecate check -config configs/hecate.example.yaml
GOOS=linux GOARCH=amd64 go build ./...
```

The race detector needs a C compiler (on Windows, w64devkit); enable cgo for that command only, never globally, because it breaks the Linux cross-build. Concurrent code relies on immutable snapshots and no global state, backed by load tests. Deliberate mutations on a scratch copy were used to confirm that the tests catch behaviour changes.

---

## DNS-over-TLS: attacker-style checks on loopback

The DoT listener was reviewed twice by a reviewer with fresh context, who ran hostile clients against it on 127.0.0.1 with a generated certificate (several source addresses from 127.0.0.2 upwards) and measured the result. The first review found four significant problems, all measured: about 16 sources opening silent connections left a legitimate client with no answer; answers to pipelined queries came strictly in order (a 2 s query delayed the next by 2 s); a client that sent queries and never read pinned its slot until it left; the shipped VPS example (`0.0.0.0:853` plus `[::]:853`) could not start. All four were fixed test first and the experiments were run again on the fixed code. Measurements on the fixed code (a re-run by the reviewer, on the commit before the last small fixes):

| Probe | Result |
|---|---|
| Silent connects from 8 and from 16 sources, reconnecting, while a valid client asks | Valid client answered 15 of 15 times in 7 to 29 ms (before the fix: 4.0 to 4.5 s, then 0 of 10) |
| 100 silent connects from one address | 8 held until the 4 s handshake deadline, 92 refused at once; a valid client from another address is served |
| Slowloris (one byte every 500 ms), a valid hello followed by silence, a handshake without a query | Closed at 4.0 s, 4.0 s and 5.0 s (the first-query deadline) |
| Wrong, empty, 1,000, 16,000 and 60,000 character server name, a name with a newline, ALPN `h2`, TLS 1.0, TLS 1.1 | Refused in 0 to 1 ms with a 7-byte alert; the engine saw 0 queries; goroutines 7 before and after |
| Garbage, plain DNS or an HTTP request on 853 | Connection closed at once, 0 bytes sent |
| Pipelined `slow` (2 s upstream) then `fast` query | `fast` answered after 1 ms, `slow` after 2.001 s |
| One connection: 1,100 pipelined and 1,001 sequential queries | 502 answered (burst 500 plus refill), the rest dropped by the rate limiter; closed after the 1,000th frame |
| A name-holder that writes 1,000 queries and never reads | Its slot is free again after about 10 s (5 s write deadline plus a close alert that blocked again; fixed afterwards to close the raw connection at once); shutdown with that client still connected took 1 to 3 ms |
| 64 non-reading connections asking for a 57.6 KB answer | Heap grew by 80 MB for about 10 s (fixed afterwards with a limit of 256 queries in flight across all connections) |
| Certificate replaced six rounds under load (half-written pair, corrupt file, renewed) with 8 workers asking continuously | 7,460 queries, 0 handshake failures, 0 query failures; every serial served in turn; the old certificate stayed in use while files were broken |
| Shutdown with 24 idle, 8 silent and 8 half-finished connections | Returned in 1 to 5 ms, no error, goroutines back to the starting value |
| 1,000 identical queries with random case and four DO/CD combinations while the upstream hangs | 4 upstream calls; all answered SERVFAIL at 11.0 s; `Failed` called 1,000 times; goroutines back to the baseline |
| 20,000 distinct IPv6 `/64` sources against the rate limiter | 4,096 tracked plus the shared overflow bucket; heap +405 KB |
| Fuzzing the frame parser (header, accept check, unpack, FORMERR reply) | 3.56 million inputs in 60 s, no panic |

Known limit from the same review: about 64 sources (IPv4 addresses or IPv6 `/64` prefixes, 8 each) can fill the pool of 512 connections waiting for a handshake; established connections keep working and new ones are refused at once until the 4 s deadline frees slots (measured: 63 sources hold 504, a valid query is still answered; 66 sources fill the pool and a valid query is refused).

Mutation checks used to confirm that the tests notice a broken behaviour: DoT caps removed, the waiting pool shared with the established pool, queries served inline instead of in parallel, no write deadline, the cross-connection limit removed, pools keyed by the exact address instead of the `/64`, the frame length check removed, the panic release in the de-duplication removed, and a waiter that ignores its own deadline. Each makes a test fail.

---

## Real-phone tests (Android, Chrome, home Wi-Fi)

Setup: the server on a PC with a reserved LAN address; Wi-Fi with a static phone address, DNS 1 pointing at the PC and DNS 2 empty on purpose (so measurements cannot leak to another DNS); Private DNS off. Windows ICS did not have to be stopped (see architecture.md, section 12). `allowed_clients` was limited to loopback and the local `/24`. Measurements come from the owner's screenshots, hecate's query log and the commands run on the test day. Observations without evidence are marked.

### S1: ads on the block lists

| Test | Result |
|---|---|
| `adblock.turtlecute.org`, with hecate (`dnsproxy` backend) | **95%** (126 of 132 blocked) |
| Same, after two custom rules (`ad.doubleclick.net`, `adservice.google.com`) | **97%** (128 of 132) |
| Same with the `native` backend | **97%**, all safe |
| Same, without hecate | 5% (provisional: the screenshot was taken before the test finished) |
| `canyoublockit.com`, eXtreme level, with hecate | Observation: pop-ups, redirects and the initial video ad all blocked |
| Same, without hecate | Observation: all ads and redirects appear |

The first target (at least 90%) was met. The Simple level of `canyoublockit.com` is not used because its ads are hosted on the site itself. Four domains still passed (inferred from the log, not proven): `www.googletagmanager.com`, `deploy.mopinion.com`, `cdn.iubenda.com`, `cdn.fluidplayer.com`. The owner chose to leave them because blocking them risks breaking legitimate sites.

### S2: redirects to a store or app

- **Shopping app: passed.** Opening a social site full of ad links redirected into the shopping app. The phone's log showed the sequence: the site, an ad CDN, `google.com`, then **`s.shopee.co.id`** (the app's short-link host), then the app opened. After `hecate rule add block s.shopee.co.id` the same link ended at "This site can't be reached". Five later queries for that host were all blocked. That was about three presses, fewer than the ten originally required; the owner accepted it.
- **Play Store: not reproduced.** No redirect appeared and the log had no suspicious store host. Recorded as "never appeared", not "blocked".
- **Accepted side effect:** short links of the form `s.shopee.co.id/...` no longer open on the phone; `shopee.co.id` and `api.shopee.co.id` still resolve, so the app opened manually works. Undo with `hecate rule remove block s.shopee.co.id`.

### S3: SafeSearch is not forced

The phone never asked for `forcesafesearch.google.com` (0 queries) and `www.google.com` was answered normally by the upstream. `hecate check` passes its S3 test. The SafeSearch setting on the phone itself was not inspected.

### Latency and statistics (23-minute window, `dnsproxy` backend)

| Measure | Value |
|---|---|
| Queries from the phone | 1,040 (724 blocked = 69.6%, 316 passed); the window was dominated by the test page, so this is not a normal browsing ratio |
| Blocks by source | `hagezi-pro` 711; custom rules 13 |
| Upstream answers (uncached), n = 262 | median 31 ms, p95 123 ms, max 419 ms |
| Cache answers | 54 |
| Block answers | logged as 0 ms (decided in-process) |

Upstream latency is DoH from the PC to Cloudflare and Google, not the phone-to-PC network.

### Re-test with the `native` backend (about 2.5 minutes)

Same setup; only `backend: native` changed. 484 queries from the phone: 322 blocked by `hagezi-pro`, 6 by custom rules, 156 passed; 472 NOERROR and 12 NXDOMAIN (random names that really do not exist). Uncached upstream answers (n = 116): median 29 ms, p95 88 ms, max 156 ms. 40 answers from cache. Process memory 41 MB. No errors.

**Finding:** the `native` rate limit was too close to a legitimate burst. The test page sends 254 queries in two seconds; a limit of 100/s with burst 200 dropped one query. The phone retried and the result stayed at 97%. The limit is still needed (family and friends can use the server), so it was raised to 200/s with burst 500, with two tests: the new limit absorbs that burst and still stops a flood of 10,000 queries per second.

### Other findings

1. The Windows ICS conflict was resolved without touching ICS: binding to the PC's specific Wi-Fi address works (UDP and TCP).
2. The apex `doubleclick.net` and Google's ad host are not in HaGeZi PRO (only specific subdomains are), so `ad.doubleclick.net` and `adservice.google.com` were added as custom rules.
3. Chrome never asked for `use-application-dns.net` or `_dns.resolver.arpa`; Firefox's reaction is untested.
4. Chrome did not bypass hecate: no lookups of DoH/DoT hosts from the phone, and effective blocking was 95 to 97%.
5. A `0.0.0.0` answer caused no observable problem in Chrome; blocked requests fail fast. Not compared with NODATA or NXDOMAIN.
6. Ordinary apps kept working (messaging, social, shopping opened manually); only their tracker hosts were blocked.

---

## Stability and robustness checks done

- Both backends: graceful Ctrl+C on a real Windows console (exit code 0, log closed, UDP port released); queries stuck on a silent plain upstream are cancelled by closing their connection.
- Consecutive upstream failures produce a warning; the query log recovers after a full disk (tested with a fake disk).
- The Task Scheduler task installs and runs hidden in session 0 with DNS answering and `hecate.log` growing.
- A single oversized UDP datagram used to stop miekg's UDP loop on Windows; fixed in `native` and added to the conformance suite.
- 2,000 sample queries (1,000 names, A and HTTPS) gave identical engine decisions on both backends (1,732 blocked on each, 0 different decisions).

## Not verified yet

- A real PC boot with the scheduled task (including late Wi-Fi).
- Behaviour on a real full disk (only a fake disk was tested).
- Real DoH providers and IPv6 upstreams under long load; Windows UDP read errors other than `WSAEMSGSIZE`; accuracy of the cache memory estimate under real load.
- The 7-day stability run (S4), planned on the VPS.
- DoT on a real VPS and from a real phone: the Android strict-mode handshake and ALPN against this server, a real Let's Encrypt wildcard certificate and its renewal, mobile-carrier NAT behaviour, real IPv6 `/64` sockets (loopback only has `::1`), and an external port scan.
- Everything in `deploy/` on Linux: the nftables syntax and semantics (`ct count` with a masked IPv6 key, dynamic sets), the systemd options, the lego flags and file order, and the Linux behaviour of the dual-family bind (checked on Windows and from the Go source only).
- DoT as an upstream (deferred).
- Redirect to the Play Store (never appeared), Firefox on the phone, IPv6 DNS from the router as a bypass path, EDE (RFC 8914) on block answers, and a complete "without hecate" baseline.
