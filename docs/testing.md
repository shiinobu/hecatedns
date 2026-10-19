# Testing

## Automated tests

The project is developed test first: a failing test, the minimum code to pass it, then cleanup. Unit tests sit beside the code they test (they use unexported names). Black-box tests and gates live in `backend/test/`.

| Layer | Content | Target |
|---|---|---|
| Unit | `engine` (matching, priority, block answers, CNAME, special names), `lists` (parser, fetcher, manager), `qlog`, `config`, `native` (cache, ACL, rate limiter, upstreams against fake servers), `controller` (commands) | Coverage of 80% or more |
| Fuzz | List parser and rule parser (`go test -fuzz`) | No panic |
| Benchmark | Lookup against about 250,000 entries; process memory | p99 under 50 microseconds; under 150 MB |
| Conformance | `backend/test/conformance`: a fake upstream (`miekg/dns` on loopback) plus the backend under test on a random port. Cases: blocked A/AAAA/HTTPS, pass-through, `allow` beats `block`, blocked CNAME, ACL REFUSED (no RA bit), log written, oversized or malformed UDP datagrams do not stop the server, reverse lookups of private addresses answered locally. Run for **both** backends from one constructor table | Identical results |
| Source rule | `backend/test/nocomment` fails if a Go or PowerShell source contains a comment | Always green |
| Manual integration | `scripts/windows/dnsquery.ps1` (or `dig`) from the PC, then tests from a phone | Criteria S1 to S3 |

Coverage at the last full run: `controller` 93.1%, `core` 100%, `core/dnsproxy` 95.4%, `core/native` 93.8%, `engine` 96.9 to 97.2%, `lib/config` 95.5%, `lib/runlog` 96.0%, `service/lists` 86.7%, `service/qlog` 90.9%, `service/selfcheck` 92.5%, `test/conformance` 80.1%.

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
- DoT as an upstream (deferred).
- Redirect to the Play Store (never appeared), Firefox on the phone, IPv6 DNS from the router as a bypass path, EDE (RFC 8914) on block answers, and a complete "without hecate" baseline.
