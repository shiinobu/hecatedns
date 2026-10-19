# HecateDNS Phase 2 review

Reviewer: `phase-reviewer`, fresh context, read-only. Range `8c10920..HEAD`, HEAD `a264c54`.

## Verdict
Phase 2 meets all four "done when" criteria on the live binary and in the test/benchmark suite: all three default blocklists download and load (250,089 entries before deduplication: `hagezi-pro` 202,297, `hagezi-popups` 47,668, `abpindo` 124), sample domains from all three lists answer `0.0.0.0`, CNAME and HTTPS/SVCB tests pass (including cache-hit CNAME re-evaluation), `hecate check` passes including SafeSearch (S3), and 250k lookup p99 (`2.01 µs`, `215 ns/op`, 0 allocs) and memory (`16.29 MiB` heap delta, `71.74 MiB` live working set / `103.35 MiB` private bytes) are well within budget (`< 50 µs`, `< 150 MB`).

Code inspection and live testing uncovered two Important issues that should be fixed before Phase 3: (1) the bootstrap DNS resolver in `internal/lists/fetch.go` uses UDP `DialContext` in a loop, which always succeeds locally on the first bootstrap IP without sending a packet and therefore never fails over to the second bootstrap IP when the first is unreachable; and (2) `internal/selfcheck` probes only the first line of each cached blocklist with a 64 KiB `bufio.Scanner`, ignores `rules.Reload()` errors, fails if the first entry is allow-listed by the user, and on the real lists probes a `hagezi-popups` entry that is already in `hagezi-pro` without verifying `dec.Source == src.Name`. **Counts: Critical 0, Important 2, Minor 8.**

## Gates (provided by implementer on `a264c54` and spot-checked)
- `gofmt -l .`: empty (PASS).
- `go vet ./...`: exit 0 (PASS).
- `go build ./...` and `GOOS=linux GOARCH=amd64 go build ./...`: exit 0 (PASS).
- `go test ./... -cover -count=1`: all `ok` — `cmd/hecate` 91.9%, `internal/backend` 100.0%, `internal/backend/conformance` 81.0%, `internal/backend/dnsproxybe` 95.4%, `internal/config` 95.5%, `internal/engine` 97.6%, `internal/lists` 88.9%, `internal/qlog` 89.1%, `internal/selfcheck` 90.3% (PASS).
- `CGO_ENABLED=1 go test -race ./... -count=1`: all `ok`, 0 data races (PASS).
- `go mod verify`: all modules verified (PASS). `go mod tidy -diff`: empty (PASS).
- `govulncheck ./...`: 0 vulnerabilities affecting the code; 1 module-level advisory in a required module not called by the code (PASS).
- `go run ./cmd/hecate check -config configs/hecate.example.yaml`: exit 0, `uji mandiri: LULUS` (PASS).

## Findings

### Critical
None.

### Important

**I1 - Important - `internal/lists/fetch.go:186-202` (`newBootstrapResolver`) and `internal/lists/fetch_test.go:299-307`.**
- **Defect:** `newBootstrapResolver` loops over `addrs` and returns the first address where `d.DialContext(ctx, network, addr)` returns `err == nil`; for UDP (`network == "udp"`, which Go's pure-Go `net.Resolver` uses by default), `DialContext` only creates a local connected datagram socket without sending any network packet, so it always succeeds immediately on `addrs[0]` and never fails over to `addrs[1]` when `addrs[0]` is unreachable or filtered.
- **Failing scenario:** Configure `bootstrap: [192.0.2.1, 8.8.8.8]` (or run on an Indonesian ISP that drops plain UDP 53 to `1.1.1.1` while `8.8.8.8` is reachable). Every `Resolver.Dial` call returns a UDP socket bound to `addrs[0]`; `net.Resolver` times out waiting for a DNS reply, retries `Dial`, receives `addrs[0]` again, and `Fetcher.Fetch` fails after 30 s without ever querying `addrs[1]`. (In `fetch_test.go:299`, the test initially sets `Bootstrap: []string{"", "192.0.2.1", pc.LocalAddr().String()}` and then overwrites it at line 307 with `f.Bootstrap = []string{pc.LocalAddr().String()}`.)
- **Fix direction:** Either rotate across `addrs` on each `Dial` call using an atomic counter in `newBootstrapResolver` so `net.Resolver` retries hit subsequent bootstrap servers, or resolve the list hostnames via `upstream.NewUpstreamResolver` / `upstream.ConsequentResolver` (as `internal/selfcheck` already does) before dialing TLS. Add a test where `addrs[0]` is an unresponsive UDP socket (`127.0.0.1:0` that never replies) and `addrs[1]` is the fake DNS server.

**I2 - Important - `internal/selfcheck/selfcheck.go:212,226-264,277-278,297-310` (`checkCachedLists`, `buildProbeEngine`, `firstSampleDomain`).**
- **Defect:** Cached blocklist verification in `selfcheck.Run` has four coupled issues: (1) `firstSampleDomain` reads only the first valid entry in `<name>.txt` using a default `bufio.Scanner` (64 KiB token limit) and returns `""` if a comment line exceeds 64 KiB, causing `if sample != ""` (`line 227`) to silently skip probing that blocklist even though `lists.LoadCached` succeeded; (2) on the real default lists, `firstSampleDomain` picks `fd01ce9a02.0012e30263.com` for `hagezi-popups`, which is also present in `hagezi-pro`, and `checkCachedLists` checks only `dec.Verdict == engine.Block` on the combined `ListSet` (`dec.Source` is `"hagezi-pro"`), so it never verifies that `hagezi-popups` itself blocks anything; (3) if the user adds an `allow` rule in `rules.txt` (or an `allow` list) matching the very first entry of any blocklist, `hecate check` fails even when the remaining thousands of entries in that blocklist are blocked; and (4) `buildProbeEngine` (`line 278`) discards the error from `rules.Reload()`.
- **Failing scenario:**
  1. Live binary with the 3 downloaded lists: querying `fd01ce9a02.0012e30263.com` logs `BLOK ... list:hagezi-pro`; if `hagezi-popups` were omitted from `blockSets`, `hecate check` would still report `daftar hagezi-popups: 47668 domain (contoh fd01ce9a02.0012e30263.com -> 0.0.0.0)` and pass.
  2. If the user runs `hecate rule add allow accesstrade.co.id` (the first line of `abpindo.txt`) at Gate G1, `hecate check` fails (`verdict=pass reason=allow`) despite the other 123 blocked domains in `abpindo.txt`.
  3. A cached list with a 70 KiB header comment loads via `LoadCached`, returns `sample == ""` from `firstSampleDomain`, and skips the probe while reporting `uji mandiri: LULUS`.
- **Fix direction:** Return any error from `rules.Reload()` in `buildProbeEngine`; scan candidate entries from the loaded `DomainSet` (or via `lists.Parse` without `bufio.Scanner`'s 64 KiB limit) to pick a sample domain for each blocklist that is not overridden by `allow` rules/lists and that matches `dec.Source == src.Name` (or probe each blocklist's `DomainSet` individually alongside `allow` rules/lists); fail if a non-empty blocklist has no unallowed entry that blocks.

### Minor

**M1 - Minor - `internal/lists/parser.go:59-95` (`parseWithLimits`).**
- **Defect:** `parseWithLimits` validates each line with `engine.ParseEntry`, stores every unique domain in both `seen` (`map[string]struct{}`) and `domains` (`[]string`), and then calls `engine.NewDomainSet(domains)`, which allocates a second `map[string]struct{}` and runs `engine.ParseEntry` a second time on every domain.
- **Scenario:** Parsing `hagezi-pro` (202,297 domains) holds two 202k-entry maps plus a 202k-element slice simultaneously at line 94 and validates 202,297 strings twice on every fetch and cache load.
- **Fix direction:** Add a builder or pre-validated constructor in `internal/engine` (or let `DomainSet` be built incrementally) so `parseWithLimits` allocates only one map and validates each line once.

**M2 - Minor - `internal/lists/fetch.go:107-117,161-184` and `internal/lists/manager.go:240-241` (`handleNotModified`, `httpClient`, `Manager.Run`).**
- **Defect:** (a) When `f.Client == nil` (production default), `f.httpClient()` clones `http.DefaultTransport` on every `Fetch()` call without reusing the transport across calls or closing idle connections, preventing TLS/HTTP2 connection reuse between `hagezi-pro` and `hagezi-popups` (both on `cdn.jsdelivr.net`) and leaving idle transport pools until `IdleConnTimeout` (90 s). (b) At startup `cmd/hecate/run.go` runs `mgr.LoadCache()` (parsing all 250k domains from disk) and immediately starts `mgr.Run(ctx)`, which calls `m.Refresh(ctx)` -> `handleNotModified` on HTTP 304 and re-reads and re-parses the unchanged 250k domains from disk a second time ~250 ms later.
- **Scenario:** Observed on live `hecate run`: `cached list loaded` logs at `21:44:48.576`–`.602`, and `list refreshed ... not_modified=true` logs at `21:44:48.845`–`49.057`, parsing 500,178 lines total at startup.
- **Fix direction:** Cache and reuse the constructed `*http.Transport` on `Fetcher` (or call `tr.CloseIdleConnections()` after `Fetch`), and on `304 Not Modified` allow `Manager.Refresh` to keep the already-loaded in-memory `*engine.DomainSet` when the on-disk stamp is unchanged.

**M3 - Minor - `internal/lists/fetch.go:30,41,226` (`checkHTTPSRedirect`).**
- **Defect:** `maxRedirects = 5` with `if len(via) >= maxRedirects` fails when `len(via) == 5` (the 5th redirect), so at most 4 redirects are followed although `ErrTooManyRedirects` says `"list download exceeded 5 redirects"`.
- **Scenario:** A URL that redirects 5 times is rejected with `ErrTooManyRedirects`.
- **Fix direction:** Change the check to `if len(via) > maxRedirects` ( matching `net/http`'s convention where `len(via) >= 10` stops after 10 redirects are attempted, or adjust the error text/constant).

**M4 - Minor - `internal/lists/manager.go:168-171,226-232,254` (`ReloadCache`, `Refresh`, `Run`).**
- **Defect:** In `ReloadCache()`, a `statList` error is appended to `errs` without logging (unlike `LoadCache()` line 134), and `Manager.Run()` calls `_, _ = m.ReloadCache()`, so a stat error during background polling is silently dropped. In `Refresh()` (`lines 226-232`), `m.publishLocked()` is called even when `len(updates) == 0` (all downloads failed).
- **Scenario:** Transient filesystem error during the 5 s poll tick in `Manager.Run` produces no log entry; a completely offline `Refresh()` re-clones and re-swaps the unchanged `ListSet`.
- **Fix direction:** Log `statErr` in `ReloadCache()` (or log returned errors in `Run()`), and only call `m.publishLocked()` in `Refresh()` when `len(updates) > 0`.

**M5 - Minor - `internal/config/validate.go:68-152` (`LoopWarnings`).**
- **Defect:** `LoopWarnings` misses three self-loop configurations: (1) `listen: ["[::]:5300"]` with `upstreams: ["127.0.0.1:5300"]` (because `l.Addr().Is4() == target.Addr().Is4()` is false even though `[::]` binds dual-stack on Windows/Linux); (2) `listen: ["127.0.0.1:5300"]` with `upstreams: ["0.0.0.0:5300"]`; and (3) upstream URLs using `localhost` (e.g. `udp://localhost:5300` or `https://localhost:5300/dns-query`), because `upstreamAddrPort` requires `netip.ParseAddr(parsed.Hostname())` to succeed.
- **Scenario:** Configuring `listen: ["127.0.0.1:5300"]` and `upstreams: ["udp://localhost:5300"]` produces 0 loop warnings and passes `LoopWarnings()`.
- **Fix direction:** Treat `strings.EqualFold(parsed.Hostname(), "localhost")` as loopback in `upstreamAddrPort`, and in `matchesAnyListen` treat an unspecified IPv6 listen address (`::`) or unspecified target (`0.0.0.0`/`::`) as matching loopback on the same port.

**M6 - Minor - `internal/engine/engine_record.go:81-87` and `internal/engine/cname.go:15` (`failOpen`).**
- **Defect:** `failOpen` is deferred by both `filter` (`Before`) and `inspectAnswer` (`After`), but hardcodes `"stage", "before"` when logging a recovered panic.
- **Scenario:** An unexpected panic inside `inspectAnswer` is recovered and fails open as intended, but logs `"stage":"before"` instead of `"stage":"after"`.
- **Fix direction:** Pass the stage name (`"before"` / `"after"`) to `failOpen`.

**M7 - Minor - Comment style across 19 Phase 2 files (`// agy wrote this`).**
- **Defect:** 27 occurrences of `// agy wrote this` were left in production and test files (`internal/lists/*.go`, `internal/engine/cname*.go`, `internal/engine/special*.go`, `internal/engine/engine.go`, `internal/engine/response.go`, `internal/engine/matcher_test.go`, `internal/selfcheck/*.go`, `cmd/hecate/*.go`, `internal/config/validate*.go`, `internal/backend/conformance/*.go`), including at line 1 above `package` declarations where `go doc` attaches them to package comments.
- **Scenario:** `go doc github.com/shiinobu/hecatedns/internal/selfcheck` or reading the source shows tool-attribution comments, contrary to `.agents/rules/03-go-standards.md` ("Comments explain why, not what") and `.agents/rules/02-workflow.md`.
- **Fix direction:** Strip all `// agy wrote this` comments.

**M8 - Minor - Test gaps (3 of 3 targeted mutations in a scratch copy survived).**
- **Defect:**
  1. `internal/lists/manager.go:305`: mutating `statList` to ignore `modTime` (`fileStamp{size: info.Size()}`) survived `go test ./internal/lists` because `TestManager_ReloadCacheDetectsDiskChange` changes both file size (`15` -> `32` bytes) and `modTime`.
  2. `internal/selfcheck/selfcheck.go:170`: mutating `hasSafeSearchCNAME` to remove `|| strings.HasSuffix(target, "."+safeSearchTarget)` survived `go test ./internal/selfcheck` because `selfcheck_test.go` only tests the exact target `forcesafesearch.google.com.`, not a subdomain.
  3. `internal/lists/fetch.go:151`: removing the `os.Stat(listPath(f.Dir, name))` guard in `cachedETag` survived `go test ./internal/lists` because no test deletes `<name>.txt` while keeping `<name>.meta.json` before a `304 Not Modified` fetch.
- **Fix direction:** Add unit test cases covering same-size file reload in `manager_test.go`, subdomain SafeSearch CNAME target in `selfcheck_test.go`, and orphaned `.meta.json` without `.txt` in `fetch_test.go`.

## Mutation results (scratch copy outside repo, max 3)
| # | Mutation | Target | Result |
|---|---|---|---|
| 1 | `statList` ignores `modTime` (`fileStamp{size: info.Size()}`) | `internal/lists/manager.go:305` | **SURVIVED** |
| 2 | `hasSafeSearchCNAME` drops subdomain suffix check (`."+safeSearchTarget`) | `internal/selfcheck/selfcheck.go:170` | **SURVIVED** |
| 3 | `cachedETag` omits `os.Stat(listPath(...))` check before returning ETag | `internal/lists/fetch.go:151` | **SURVIVED** |

## Verified and found correct
- **Phase 2 "Done when" criterion 1 (3 lists loaded, ~249k entries, sample domains answer `0.0.0.0`):** Verified live in `$env:TEMP\hecate-p2-review-scratch` by running `hecate.exe lists update`, `hecate.exe lists status`, and `hecate.exe run` on `127.0.0.1:5420`. Downloaded counts: `hagezi-pro` = 202,297 (rejected 0), `hagezi-popups` = 47,668 (rejected 0, 113 suffix-unique to popups), `abpindo` = 124 (rejected 0), total = **250,089 entries** before deduplication. Live queries via `scripts/dnsquery.ps1 -Port 5420`: `telemetry.001-studio.com A` -> `0.0.0.0` (`list:hagezi-pro`), `h4g3z1-popupads.web.app A` -> `0.0.0.0` (`list:hagezi-popups`), `accesstrade.co.id A` -> `0.0.0.0` and `AAAA` -> `::` (`list:abpindo`).
- **Phase 2 "Done when" criterion 2 (CNAME and HTTPS/SVCB tests green):** Verified in `internal/engine/cname_test.go`, `internal/engine/special_test.go`, and `internal/backend/conformance/suite.go`. Conformance verifies that CNAME-cloaked domains are blocked on both initial upstream resolution and subsequent backend cache hits (`upstream` queried once, both queries answered `0.0.0.0` and logged with `reason=cname via=tracker.ads.test`), allowed CNAME targets pass, and `HTTPS`/`SVCB` queries for blocked names (direct and via CNAME) return `NOERROR` empty answer + 1 synthetic SOA with TTL 10 s.
- **Phase 2 "Done when" criterion 3 (`hecate check` LULUS, including S3 SafeSearch):** Verified live with `hecate.exe check`: both configured DoH upstreams (`https://1.1.1.1/dns-query`, `https://8.8.8.8/dns-query`) resolved `www.google.com` without chaining to `forcesafesearch.google.com`, printing `safe_search (S3): LULUS` and `uji mandiri: LULUS` (exit 0). Live query `www.google.com A` against `hecate.exe run` returned 8 real Google A records (`142.251.154.119`, etc.) with `NOERROR`.
- **Phase 2 "Done when" criterion 4 (lookup p99 < 50 µs and memory < 150 MB):** Verified via `TestListSet_Budget250k`, `BenchmarkListSet_Lookup250k`, and live process measurement:
  - 250k lookup benchmark: **214.9 ns/op** (0.215 µs), **0 B/op**, **0 allocs/op**.
  - 250k lookup latency percentiles: **p50 = 0 s**, **p95 = 1.04 µs**, **p99 = 2.01 µs** (budget: `< 50 µs`).
  - 250k heap in-use delta: **16.29 MiB**, Go runtime `Sys`: **42.64 MiB**.
  - Live `hecate.exe run` process memory with all 3 real lists loaded and refreshed: **WorkingSet = 71.74 MiB**, **PrivateBytes = 103.35 MiB** (budget: `< 150 MB`).
- **Special names (`special.go`):** `use-application-dns.net` answers `NXDOMAIN` + SOA (`special:firefox_canary`) and `_dns.resolver.arpa` answers `NOERROR` NODATA + SOA (`special:ddr`) on the live binary and in unit/conformance tests; ACL refusal still runs before special names.
- **Parser & fetcher security:** `lists.Parse` enforces `MaxLineBytes` (4096), `MaxListBytes` (64 MiB, including gzip-decoded streams via `io.LimitReader`), and `MaxEntries` (1,000,000); rejects single-label entries, IPs, adblock syntax, and non-ASCII characters; strips UTF-8 BOM and comments; ignores localhost preamble in `hosts` format; never echoes untrusted input in errors; rejects empty lists (0 valid domains) so an HTML error page cannot wipe protection; enforces `https://` on initial URLs and redirects; writes cache and metadata atomically via temp file + sync + rename retry; preserves old cache on download or parse failure.
- **Immutability & concurrency:** `ListSet` and `DomainSet` are immutable once constructed and swapped via `atomic.Pointer`; `internal/engine` has zero network imports; all files are well under 800 lines (largest new file is `internal/lists/fetch.go` at 414 lines).

## Not verified
- Real Firefox and Chrome behavior on the RedMagic 8S Pro phone in response to `use-application-dns.net` (NXDOMAIN) and `_dns.resolver.arpa` (NODATA) — scheduled for Phase 3 (`docs/architecture.md` §18 item 3).

---

## Triage (main session)

All Important findings (`I1`, `I2`) and Minor findings (`M1`–`M6`, `M8`, plus `M7` package-comment placement) were fixed with RED -> GREEN tests in three commits (`e5c18e5`, `d3483b7`, `7fd1ac0`).

| Id | Severity | Status | Commit / decision |
|---|---|---|---|
| I1 | Important | Fixed | `e5c18e5` `Fetcher` resolves bootstrap hostnames via `miekg/dns` over UDP (with TCP fallback on truncation) sequentially across `Bootstrap` addresses, failing over deterministically when the first bootstrap server is silent/unreachable; tested with a blackhole UDP socket before a fake DNS server (`TestBootstrapResolver_FailsOverFromSilentUDPAndReusesTransport`). |
| I2 | Important | Fixed | `d3483b7` `selfcheck` returns `rules.Reload()` errors, removes `firstSampleDomain`/`bufio.Scanner`, and uses `DomainSet.Find` to pick a deterministic sample domain per blocklist that is not overridden by `allow` rules/lists and whose `dec.Source == src.Name` (with single-list fallback for subset lists); tested in `TestRun_SkipsAllowedFirstEntryAndPicksListOwnSample`. |
| M1 | Minor | Fixed | `d3483b7` `engine.DomainSetBuilder` builds `DomainSet` in one pass in `lists.Parse` without a duplicate map/slice or second `ParseEntry` pass. |
| M2 | Minor | Fixed | `e5c18e5`, `7fd1ac0` `Fetcher` caches and reuses its default `*http.Transport` across calls; `Manager.Refresh` passes the in-memory `*engine.DomainSet` via `FetchWithCached` when the disk stamp matches so `304 Not Modified` does not re-parse 250k domains from disk. |
| M3 | Minor | Fixed | `e5c18e5` `checkHTTPSRedirect` uses `len(via) > maxRedirects` so 5 redirects succeed and the 6th fails with `ErrTooManyRedirects`. |
| M4 | Minor | Fixed | `7fd1ac0` `ReloadCache` logs `statList` errors and `Refresh` skips `publishLocked` when all downloads fail (`len(updates) == 0`). |
| M5 | Minor | Fixed | `7fd1ac0` `LoopWarnings` detects `localhost` upstream URLs and wildcard IPv4/IPv6 vs loopback combinations on the same port. |
| M6 | Minor | Fixed | `7fd1ac0` `failOpen` takes a `stage` parameter (`"before"` in `filter`, `"after"` in `inspectAnswer`). |
| M7 | Minor | Kept per Master's rule (moved below `package`) | `7fd1ac0` Master explicitly requested `// agy wrote this` in modified files to mark Antigravity's changes for Claude handover; moved top-of-file markers below `package <name>` so `go doc` package comments remain clean. |
| M8 | Minor | Fixed | `e5c18e5`, `d3483b7`, `7fd1ac0` Added tests for same-size `modTime` reload (`manager_test.go`), subdomain SafeSearch CNAME target (`selfcheck_test.go`), and orphaned `.meta.json` without `.txt` (`fetch_test.go`). |
