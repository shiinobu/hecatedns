# HecateDNS architecture

HecateDNS is a personal, non-commercial DNS server that blocks browser ads (pop-ups, auto-redirects, click-redirects) on a phone, without a VPN app and without forcing Google SafeSearch. This document is the design specification. The work plan is in [roadmap.md](roadmap.md), the research behind it in [research.md](research.md), test results in [testing.md](testing.md), and how to run it in [operations.md](operations.md).

Status: phases 0 to 5 are done (both backends pass the same conformance suite and a real-phone test). Phase 6 (VPS + DNS-over-TLS) is next.

---

## 1. Goals, limits and success criteria

**Goal.** A DNS server that blocks ad and tracker domains for an Android phone, first on the home Wi-Fi and later anywhere through Android's Private DNS (DoT).

**Honest limits** (see research.md). DNS cannot block 100% of browser ads. It cannot reach ads served from the same domain as the content, redirect scripts served by the site itself, click overlays that open windows from page scripts, or the empty ad boxes left behind. The documented complement is Firefox with uBlock Origin; Plan B is an APK (section 16).

**Not goals of version 1:** web UI, HTTPS inspection, APK, DNS-over-QUIC, DNSSEC validation, per-client profiles, parental control.

| Code | Criterion | How it is measured |
|---|---|---|
| S1 | Domains on the block lists are blocked | `hecate check` (a sample domain from every list answers `0.0.0.0`); ad-test pages before and after; target at least 90% of the test hosts |
| S2 | The trigger of a redirect can be found and stopped | Query log around the time of the redirect, then a rule or list entry, then repeated attempts |
| S3 | SafeSearch is not forced | `www.google.com` does not chain to `forcesafesearch.google.com` |
| S4 | Stable | 7 days without a crash or manual restart, stable memory, p95 cache-miss latency under 150 ms (measured in phase 6 on the VPS) |
| S5 | A false positive is easy to fix | `hecate rule add allow <domain>` takes effect within 10 seconds, without a restart |

---

## 2. Design decisions

| # | Decision | Choice | Reason |
|---|---|---|---|
| D1 | Language | Go (`go.mod` pins 1.26.9) | One binary, mature DNS ecosystem. 1.26.9 because 1.26.8 is affected by six standard-library vulnerabilities |
| D2 | Backends | Two, switchable by config: `dnsproxy` (AdGuard dnsproxy v0.86.0, Apache-2.0) and `native` (own code on `miekg/dns` v1.1.73) | The owner wanted to try both. One shared engine keeps decisions identical and reusable for Plan B |
| D3 | Domain matching | Map of domains, suffix match (a domain and all its subdomains), immutable snapshot swapped atomically | Simple, fast, safe under concurrency |
| D4 | Default lists | HaGeZi Multi PRO, HaGeZi Pop-Up Ads, ABPindo `domain.txt` | PRO is the author's recommendation; ULTIMATE risks false positives |
| D5 | Blocked answers | `null_ip` (A `0.0.0.0`, AAAA `::`), TTL 10 s; other types NODATA with a synthetic SOA; mode is configurable | Research section 4.3 |
| D6 | CNAME | Every CNAME in an upstream answer is checked against the lists | Catches CNAME-cloaked trackers |
| D7 | HTTPS/SVCB and special names | Type 65 on a blocked name gives NODATA; `_dns.resolver.arpa` NODATA; `use-application-dns.net` NXDOMAIN (switchable) | Closes bypasses through service-binding records |
| D8 | Upstreams | DoH to Cloudflare `1.1.1.1` then Google `8.8.8.8` (not the filtering "family" variants) | Encrypted, and does not force SafeSearch |
| D9 | Configuration | Strict YAML (`gopkg.in/yaml.v3`, unknown keys are errors) | Readable, already in the dependency graph |
| D10 | Query log | Daily JSONL, 7-day retention | The "reproduce, read the log, block" workflow |
| D11 | CLI to server coordination | Through files (rules, list cache) polled by modification time; no admin socket | No extra port, no extra attack surface |
| D12 | Development port | `127.0.0.1:5300` | 5353 is mDNS; Windows Internet Connection Sharing holds UDP 53 |
| D13 | UI | None in version 1; log and rules through the CLI | YAGNI |
| D14 | Source style | Idiomatic Go with no comments in the source (enforced by `backend/test/nocomment`); the reasoning lives in this document | Owner's rule |

---

## 3. Overview

```
 Client (phone / PC)
      |  DNS (UDP/TCP 53; later DoT 853)
      v
+------------------------------------------------------+
| BACKEND   backend: dnsproxy | native                 |
|   listener, client ACL, cache, encrypted upstream    |
+-------+------------------------------^---------------+
   Before(q)                      After(q, answer)
        v                               |
+------------------------------------------------------+
| ENGINE (one, shared)                                 |
|   custom rules, block lists, CNAME check,            |
|   block answers, special names, query log            |
+------------------------------------------------------+
        ^                    ^
  data/rules.txt     data/lists/*.txt (downloaded periodically)
```

The engine knows nothing about networks, TLS or caching. A backend knows nothing about lists or rules. They meet only through three calls: `Before`, `After` and `Failed`.

---

## 4. Project layout

```
cmd/
  hecate/                 entry point (calls controller.Execute)
  hecate-compare/         dev tool: compares the decisions of two running servers
backend/
  controller/             CLI commands: run, check, log, rule, lists
  core/                   backend contract: Backend, Bound, Deps, Factory, Registry
    dnsproxy/             adapter around AdGuard dnsproxy
    native/               own server, upstreams, cache, rate limiter
  engine/                 the filter: ACL, rules, matcher, CNAME check, special names, health
  service/
    lists/                download, parse, cache and schedule block lists
    qlog/                 query log writer, reader, retention
    selfcheck/            `hecate check`: upstreams, SafeSearch, sample domains
  lib/
    config/               YAML schema, strict loader, validation
    runlog/               size-capped operational log
  test/
    conformance/          one suite run against both backends
    nocomment/            fails the build if a comment appears in Go or PowerShell sources
configs/hecate.example.yaml
scripts/windows/          autostart.ps1 (Task Scheduler), dnsquery.ps1 (query on any port)
data/                     runtime data, git-ignored: rules.txt, lists/, logs/, tls/
```

Imports point downward only: `controller` may import everything; `core` and `service` import `engine` and `lib`; `engine` imports only `lib/config`; `lib` imports nothing from the project. `lists` and `qlog` feed the engine through small interfaces defined in `engine`. The backends are registered by the caller (`controller` and the conformance tests build the `Registry`), so `core` never imports its implementations and no import cycle can occur.

Size rules: files 200 to 400 lines typical (800 soft ceiling), functions under 50 lines, nesting at most 4 levels.

---

## 5. Components and contracts

### 5.1 Engine

```go
type Query struct { Req *dns.Msg; Client netip.Addr; Proto string }
type Decision struct { Verdict Verdict; Resp *dns.Msg; Reason, Source, Via string }

func (e *Engine) Before(ctx context.Context, q Query) Decision
func (e *Engine) After(ctx context.Context, q Query, resp *dns.Msg, m Meta) Decision
func (e *Engine) Failed(ctx context.Context, q Query, err error)
```

Guarantees: safe for concurrent use; no network I/O; never modifies `q.Req` or the upstream answer; writes exactly one log record per query that reaches it; fails open (passes the query and warns once) when no list is loaded; a panic inside the engine fails open for filter decisions and fails closed (REFUSED) for the ACL check.

### 5.2 Backend

```go
type Backend interface { Name() string; Run(ctx context.Context) error }
type Deps struct { Cfg config.Config; Engine *engine.Engine; Log *slog.Logger }
type Factory func(Deps) (Backend, error)
type Registry map[string]Factory
type Bound interface { Ready() <-chan struct{}; Addr(network string) net.Addr }
```

`backend:` in the config selects the factory; an unknown name is a start-up error that lists the known ones. A backend runs at most once.

### 5.3 The `dnsproxy` adapter

`proxy.Config.RequestHandler` is an interface, so the adapter is one wrapping handler: it calls `Before`; on `Block` it fills `dctx.Res` with the synthetic answer; on `Pass` it calls `p.Resolve(dctx)` (upstream plus cache) and then `After`, which may replace the answer. Facts verified against v0.86.0 that shaped the design:

- The cache stores the upstream answer before `After`, so a block answer is never cached and the CNAME check runs on every cache hit.
- dnsproxy has no client ACL, so the engine enforces `allowed_clients` and both backends behave the same.
- dnsproxy answers some queries before the handler, without ACL or log: ANY gives NOTIMP, a question count other than 1 gives FORMERR, private ARPA from public clients gives NXDOMAIN. Other opcodes are forwarded upstream, so the engine answers them itself.
- The package does not use `recover()`; the adapter adds it per stage (before `Before`: REFUSED; in `Resolve`: SERVFAIL; afterwards: keep the answer).
- Every TCP connection holds one `MaxGoroutines` slot for its lifetime, shared with UDP. One LAN host can hold 256 connections and stall the service. Accepted at home, to be fixed before the server is exposed (phase 6).
- On Linux dnsproxy uses `SO_REUSEPORT`, so a second instance can bind the same port silently; on Windows the second bind fails.

### 5.4 The `native` backend

Own code on `miekg/dns`, serving the home server since 2026-10-19.

| Files | Behaviour |
|---|---|
| `native.go` | Factory and single-use `Run`; sockets are bound first so a bind error returns at once |
| `server.go` | UDP and TCP listeners on every `listen` address; non-QUERY opcodes go to the engine (NOTIMP and logged); malformed headers are dropped silently; own TCP limit (64 total, 8 per client); `tolerantReader` keeps the server alive when a UDP read fails (on Windows an oversized datagram fails with `WSAEMSGSIZE`); shutdown also closes sockets |
| `handler.go` | Rate limit, `Before`, cache, upstream, `After`; `Failed` when every upstream fails; ANY answered NOTIMP; panic recovery per stage; only the upstream exchange takes a slot (256 total, 64 per client), so cache hits and block answers keep working when upstreams are slow; 11 s per query; UDP size follows EDNS (512 to 1232) |
| `upstream*.go`, `bootstrap.go` | Weighted-random pool (1/srtt) with failover; plain UDP (retried over TCP when truncated), TCP, and DoH (RFC 8484 POST, ID 0, HTTP/2, no environment proxy, no redirects, body capped at 65,535 bytes). DoH host names resolve through plain-DNS bootstrap servers with a shared time budget. A reply is accepted only if QR, ID, opcode and question match. SERVFAIL or REFUSED from one upstream moves on to the next. **DoT as an upstream is not implemented** |
| `cache.go` | Byte-bounded LRU; key is lower-cased name, type, class and the DO and CD bits; TTL clamped by `min_ttl`/`max_ttl`; negative caching; only NOERROR and NXDOMAIN without TC are stored; upstream OPT is stripped |
| `ratelimit.go` | Token bucket per client IP (200/s, burst 500), table capped at 4,096 clients; a client over the limit gets no reply (a reply to a spoofed address would amplify) |
| `localreverse.go` | PTR, SOA and NS for RFC 6303 zones answered locally with NXDOMAIN |

Differences from the `dnsproxy` adapter, none relevant for private LAN clients: `native` does not deduplicate identical in-flight queries; `native` answers ANY after `Before`, so the ACL applies and the query is logged; the rate-limit table, IPv6 `/64` keys and TCP-per-address limits have known gaps listed in the roadmap as phase 6 debt.

---

## 6. Flow of one query

`Before`:
1. **ACL.** A client outside `allowed_clients` gets REFUSED (reason `acl`) before any matching. The list must hold at least one CIDR, so an empty list never means "allow all".
2. **Validation.** Exactly one question with opcode QUERY; otherwise the engine answers FORMERR or NOTIMP and logs it.
3. **Normalisation.** Lower case, trailing dot removed; IDNs are assumed to be in punycode.
4. **Special names** (if enabled): `use-application-dns.net` NXDOMAIN, `_dns.resolver.arpa` NODATA.
5. **Custom rules** (always win over lists): `allow` passes, then `block` blocks.
6. **Lists:** `allow` lists pass, then `block` lists block.
7. Otherwise `Pass`.

Priority: custom `allow` > custom `block` > list `allow` > list `block`. Matching is by suffix on label boundaries: `example.com` matches `a.b.example.com` but not `notexample.com`.

`After` (for passed queries): a missing answer or a non-NOERROR code is logged as is; otherwise every CNAME target is normalised and checked, and a blocked target turns the whole answer into a block (reason `cname`, `Via` = the target); otherwise the pass is logged with upstream, rcode and latency.

**Block answers** copy the ID and question, set RA, keep RD, mirror OPT, and are compressed. ACL refusals do not set RA.

| Mode | Behaviour |
|---|---|
| `null_ip` (default) | A `0.0.0.0`, AAAA `::` (TTL `block.ttl`); other types, including HTTPS/SVCB, NOERROR with no data plus a synthetic SOA |
| `nxdomain` | NXDOMAIN plus SOA |
| `nodata` | NOERROR, empty, plus SOA |
| `refused` | REFUSED |

The synthetic SOA uses the same short TTL, so a changed rule takes effect quickly.

---

## 7. Block lists

| Name | Source | Entries (2026-10-18) |
|---|---|---|
| `hagezi-pro` | HaGeZi Multi PRO, wildcard domains | 202,297 |
| `hagezi-popups` | HaGeZi Pop-Up Ads | 47,668 |
| `abpindo` | ABPindo `domain.txt` (Indonesian ads) | 124 |

The full URLs are in `configs/hecate.example.yaml`. A source name becomes a file name (`data/lists/<name>.txt`), so it is limited to 1 to 64 letters, digits, `-` or `_`, must start with a letter or digit, must not be a Windows device name, and must be unique ignoring case. A source without `enabled` is enabled.

Formats: `domains` (one per line) and `hosts` (`0.0.0.0 domain` or `127.0.0.1 domain`). Full adblock syntax is not supported.

**Parser** (lists are untrusted input): skip blank lines and comments (`#`, `!`, `;`), strip a BOM, lower-case ASCII only, remove `*.` and a trailing dot, validate labels (1 to 63 characters, total at most 253, only `a-z 0-9 - _`), reject IP addresses and single-label entries (so a bad line like `com` cannot block a whole TLD). Hard limits: line under 4,096 bytes, decoded stream at most 64 MiB, at most 1,000,000 unique domains per list, and a list with zero valid domains is rejected so an HTML error page can never wipe protection.

**Updates:** at start the disk cache is loaded first, then refreshed in the background every `lists.update_interval` (default 24 h, jitter up to 10%); the cache files are polled every 5 s so `hecate lists update` reaches a running server without a restart. `If-None-Match` is sent only when both the cache and its metadata exist, and a `304` reuses the in-memory set. Only `https://` is accepted (at most 5 redirects, none to `http://`), 30 s timeout, 64 MiB cap. Host names resolve through the configured bootstrap servers, so list downloads never loop through hecate itself. Files are written to a temporary file and renamed. A failed download keeps the old copy. Lists and custom rules are separate snapshots, so a rule change never rebuilds hundreds of thousands of entries.

**Resource budget** (measured with about 250,000 entries): about 16 MiB of heap for the set, working set 72 to 74 MiB for the running process, lookup 215 to 250 ns on average (zero allocations), p99 about 2 microseconds.

---

## 8. Custom rules and CLI

`data/rules.txt`:

```
# comment
block ads.example.net
allow cdn.broken-site.example
```

Same semantics as the lists. A bad line is skipped and reported with its line number. The file is capped at 4 MiB and 4,096 bytes per line. The server checks the modification time every `rules.poll_interval` (default 2 s).

| Command | Purpose |
|---|---|
| `run [-config f] [-backend dnsproxy\|native]` | Run the server |
| `check [-config f]` | Validate the config, load cached lists, run the self-check, print PASS or FAIL |
| `log [-since 10m] [-grep text] [-blocked] [-around 14:32 -window 30s]` | Read the query log |
| `rule add block\|allow <domain>` / `rule remove <domain>` / `rule list` | Edit `rules.txt` atomically |
| `lists status` / `lists update` | Show the list status or force a re-download |

Redirect-hunting workflow: trigger the redirect, note the time, run `hecate log -around 14:32 -window 30s`, find the trigger domain, run `hecate rule add block <domain>`, repeat.

---

## 9. Logging

**Query log:** `data/logs/queries-YYYYMMDD.jsonl`, one JSON object per line, local time with offset:

```json
{"ts":"2026-10-18T14:32:07.412+07:00","client":"192.168.1.23","proto":"udp","qname":"ads.example.com","qtype":"A","action":"block","reason":"list","source":"hagezi-popups","rcode":"NOERROR","ms":0}
```

- Written through a buffered channel; when it is full a record is dropped and counted, so a query never waits for the disk.
- Daily rotation by local date; retention `log.retention_days` (default 7); only regular `queries-YYYYMMDD.jsonl` files are ever deleted.
- Daily size cap `log.max_mb_per_day` (default 256): excess records are dropped and counted, with one warning per day. Without it one UDP sender could write about 14 MB/s through logged ACL refusals.
- A full disk no longer ends the log for the day: the buffer is reset after a failed write and unconfirmed records are counted as dropped.
- Names and errors are sanitised (control and bidi characters become `?`).
- The log contains browsing history. It lives only on the host and can be turned off (`log.enabled: false`).

**Operational log:** warnings and errors go to the screen and, if `log.enabled`, to `<log.dir>/hecate.log` (5 MB plus one backup), so a scheduled task without a console still leaves a trace. A failed write only warns. An error that ends the server (for example a bind failure at boot because Wi-Fi is not ready) is written there before exit; a config load error cannot be, because the log directory comes from the config, so run `hecate check` after editing it.

---

## 10. Configuration

See `configs/hecate.example.yaml` for every key with its default. Validation at start collects all problems and exits non-zero with clear messages: unknown or duplicate keys, more than one YAML document, an empty `allowed_clients` (to allow everyone write `0.0.0.0/0` and `::/0`), invalid CIDRs, host names in `listen`, host-name upstreams without `bootstrap`, non-`https` list URLs, unsafe or duplicate source names, unknown mode or backend, negative TTLs, empty `rules.file` or `log.dir`, duplicate `listen` entries, port 0, and a `log.max_mb_per_day` below 1. TTLs are capped before conversion to `uint32`; `cache.max_ttl: 0` means no upper bound. Relative paths resolve from the config file's directory. `Warnings()` flags an upstream or bootstrap that points at hecate's own listen address (a DNS loop), and `hecate check` fails on it.

---

## 11. SafeSearch

hecate never rewrites `www.google.com`. The upstream must not be a resolver that forces SafeSearch (so the "family" variants are not defaults), must be encrypted so the ISP or router cannot hijack plain DNS, and no rule or list may map Google to `forcesafesearch.google.com`. `hecate check` resolves `www.google.com` (type A) through every configured upstream and fails if any chain contains `forcesafesearch.google.com`. If SafeSearch is locked through a Google account or a network proxy, DNS cannot undo it.

---

## 12. Host notes (Windows)

- The Windows Internet Connection Sharing service holds UDP `0.0.0.0:53`, but binding to one specific address (`<LAN-IP>:53`, UDP and TCP) still works, so ICS does not have to be stopped. Android cannot set a DNS port, so a phone test needs port 53 on the LAN address.
- `nslookup -port` is accepted but ignored; use `scripts/windows/dnsquery.ps1`.
- Windows excludes some port ranges (Hyper-V/WinNAT), which makes binds fail with `WSAEACCES`; tests must not assume that the same TCP and UDP port are both free.
- File modes `0600`/`0700` are not enforced; renaming over an open file fails, so atomic writes retry briefly.
- Task Scheduler autostart is described in [operations.md](operations.md). "Restart on failure" does not work for this program (tested); a trigger that repeats every minute with `IgnoreNew` replaces it. `remove` and Windows shutdown end the process with `TerminateProcess`, so there is no graceful path there; the query log stays safe because the writer flushes whenever its queue is idle.

---

## 13. Security

Main threats: an open resolver abused for amplification (once on a VPS), hostile list or config input, browsing history leaking through logs, and manipulated upstreams or list downloads.

| Control | Implementation |
|---|---|
| Client restriction | `allowed_clients` defaults to loopback and private ranges; others get REFUSED |
| Specific bind | Listen on a specific IP, not `0.0.0.0`, unless there is a reason |
| Rate limit | Per client IP. `native`: active (200/s, burst 500). `dnsproxy`: its separate `ratelimit` middleware is not wired yet; required before phase 6 |
| TCP slot exhaustion | `dnsproxy`: see 5.3. `native`: own TCP limits (64 total, 8 per client) and own upstream limits (256 total, 64 per client) |
| Oversized UDP datagram | Handled by `native` (`tolerantReader`); covered by the conformance suite for both backends |
| Log growth | Daily size cap; ACL refusals logged one per query |
| Untrusted lists | HTTPS only, size and time limits, per-line validation, never executed |
| Public access (phase 6) | DoT on 853 with a valid certificate; clients identified by a secret host name in SNI (Android Private DNS sends no auth header); detailed design reviewed at the start of phase 6 |
| Secrets | TLS private keys in `data/tls/` (strict permissions, git-ignored); no secrets in the repository or example config |
| Supply chain | Pinned module versions, `go mod verify`, `govulncheck` at the end of each phase |
| Error messages | Server logs may be detailed; DNS answers never carry internal detail |
| Privileges | Run as an ordinary user (on Linux a dedicated user with the capability to bind low ports) |

---

## 14. Errors and resilience

- **Fail-open:** if the engine is not ready or fails internally, the query is forwarded and the error logged. Exception: ACL violations stay REFUSED, also when the ACL check or a handler before `Before` panics.
- **Upstream failure:** try the next upstream; if all fail, SERVFAIL and `Failed` logs it. Five consecutive failures produce one warning per minute (same for both backends); the first success afterwards logs the recovery. Cache hits, local answers and client cancellations are ignored.
- **Lists:** a failed download keeps the old cache; a corrupt cache file is ignored and logged.
- **Rules:** bad lines are skipped; a missing file means no rules.
- **Logs:** a full disk, a full channel or the daily cap never block a query.
- **Shutdown:** Ctrl+C cancels the context; listeners close cleanly, the log is flushed, and queries stuck on a silent plain upstream are cancelled by closing their connection.
- Every error is wrapped with context (`%w`) and logged once, by the layer that handles it.

---

## 15. Design notes

Reasoning that used to live in code comments:

- The ACL treats an empty list as an error because "no entries" must never read as "everybody". Clients are matched in their IPv4-unmapped form.
- Domain folding is ASCII only on purpose: Unicode folding can map different characters onto the same name.
- The matcher walks the full name up to the registrable part and never tests the bare last label, so an entry can never match a whole TLD.
- Errors from `ParseEntry` never echo the input, so a hostile list line cannot inject text into logs.
- Snapshots (`DomainSet`, `ListSet`, rules) are built once and never modified; readers load them through atomic pointers and never block.
- The native cache key includes the DO and CD bits, so a client asking for DNSSEC records never receives a cached answer built for one that did not.
- Bootstrap resolution uses plain DNS servers given by IP, because hecate's own resolver cannot be used to find the upstream it needs; each server gets a share of the remaining time so one dead server cannot use it all.
- DoH uses ID 0 (RFC 8484) so identical queries are cacheable, and never the environment proxy, because DNS must not leave through one.
- `dnsproxy` sends nothing for a nil `dctx.Res`, so the adapter never leaves it nil. A proxy that never started is released through `close`.
- `hecate rule` rewrites the rules file atomically (temporary file in the same directory, then rename), keeps its comments, and refuses to edit a file the server would reject, because rewriting it would hide the problem.
- Block answers are built as copies; a block decision without an answer message is an error, because the handler shapes the message in place.

---

## 16. Plan B and out of scope

**Plan B: APK.** Considered if phase 3 tuning does not stop the redirects or stronger blocking is wanted: a local DNS filter (same power as DNS), a DNS plus HTTPS filter (stronger but heavy and risky), or a Private DNS on/off switch (light). The open-source app BlockAds (GPL-3.0) already does the first two and is a reference. The engine is plain Go without network dependencies, so it could be compiled for Android later (inference; BlockAds uses Go with gomobile).

**Out of scope:** web UI (phase 7 if wanted), full adblock syntax, DoQ, DNSSEC validation, per-client profiles, parental control, ECS (disabled for privacy), DHCP.

---

## 17. Risks

| # | Risk | Mitigation |
|---|---|---|
| R1 | dnsproxy's API is 0.x and may change | Pinned version, thin adapter, `native` as a fallback |
| R2 | Windows ICS holds UDP 53 | Develop on port 5300; bind a specific address |
| R3 | False positives from aggressive lists | Start from PRO; `rule add allow` takes effect within 10 s; the log shows the source |
| R4 | The phone bypasses hecate (router IPv6 DNS, browser DoH, Private DNS on) | Check that phone queries appear in the log; check Secure DNS in Chrome and DoH in Firefox |
| R5 | The two backends behave differently | Same conformance suite; decision comparison on a sample |
| R6 | The Go toolchain is downloaded automatically | `govulncheck` at the end of each phase |
| R7 | Host off means the phone has no DNS if only that host is configured | Accepted for testing; the fix is the VPS (phase 6) |
| R8 | A list source dies or changes format | Disk cache, per-line validation, warning in `lists status` |
| R9 | Ad domains keep changing | Daily updates; newly-registered-domain lists considered in phase 7 |
| R10 | The redirect trigger is a script from the site's own domain | Said plainly; Firefox with uBlock Origin as a complement; Plan B |
| R11 | hecate is not up after boot (Wi-Fi not ready) | Repeating Task Scheduler trigger; a real boot is not yet tested |
| R12 | Family and friends use the home server | Per-IP limits matter more; known gap: a full rate-limit table releases new sources (phase 6) |
