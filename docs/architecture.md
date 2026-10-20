# HecateDNS architecture

HecateDNS is a personal, non-commercial DNS server that blocks browser ads (pop-ups, auto-redirects, click-redirects) on a phone, without a VPN app and without forcing Google SafeSearch. This document is the design specification. The research behind it is in [research.md](research.md), test results in [testing.md](testing.md), and how to run it in [operations.md](operations.md).

Status: both backends pass the same conformance suite and a real-phone test. The `native` backend also serves DNS-over-TLS (section 13.1), tested with generated certificates and attacker-style load on loopback, and since 2026-10-20 on a real VPS from an Android phone on mobile data and on Wi-Fi. An optional web dashboard (section 18) runs on the same VPS and has been used from the phone since 2026-10-21. The seven-day stability run (S4) is in progress (restarted on 2026-10-21 when the names file was switched on), and a real certificate renewal has not been seen yet.

---

## 1. Goals, limits and success criteria

**Goal.** A DNS server that blocks ad and tracker domains for an Android phone, first on the home Wi-Fi and later anywhere through Android's Private DNS (DoT).

**Honest limits** (see research.md). DNS cannot block 100% of browser ads. It cannot reach ads served from the same domain as the content, redirect scripts served by the site itself, click overlays that open windows from page scripts, or the empty ad boxes left behind. The documented complement is Firefox with uBlock Origin; Plan B is an APK (section 16).

**Not goals:** HTTPS inspection, APK, DNS-over-QUIC, DNSSEC validation, per-client profiles, parental control. The web dashboard is an optional, separate process (section 18); the DNS server does not depend on it.

| Code | Criterion | How it is measured |
|---|---|---|
| S1 | Domains on the block lists are blocked | `hecate check` (a sample domain from every list answers `0.0.0.0`); ad-test pages before and after; target at least 90% of the test hosts |
| S2 | The trigger of a redirect can be found and stopped | Query log around the time of the redirect, then a rule or list entry, then repeated attempts |
| S3 | SafeSearch is not forced | `www.google.com` does not chain to `forcesafesearch.google.com` |
| S4 | Stable | 7 days without a crash or manual restart, stable memory, p95 cache-miss latency under 150 ms (measured on the VPS) |
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
| D11 | CLI and dashboard coordination with the server | Through files (rules, list cache, the DoT names file) polled by modification time; no admin socket. The dashboard adds one real DoT query to the loopback listener for its status, and a spool directory for fixed operations (D17) | No extra control port, no extra attack surface |
| D12 | Development port | `127.0.0.1:5300` | 5353 is mDNS; Windows Internet Connection Sharing holds UDP 53 |
| D13 | UI | The CLI, plus an optional dashboard (`hecate dashboard`): a separate process with its own unit that serves one embedded page (vanilla JavaScript, no framework, no CDN) | The owner wants to tune rules and rotate the DoT name from the phone without the PC; a separate process leaves the DNS server untouched |
| D14 | Source style | Idiomatic Go with no comments in the source (enforced by `backend/test/nocomment`); the reasoning lives in this document | Owner's rule |
| D15 | Dashboard access | A secret host name under the wildcard certificate plus an access key whose SHA-256 is the only thing stored on the server; a signed session cookie | Two factors, because the server name travels in clear text |
| D16 | Dashboard privileges | The same user as the DNS server with a hardened unit, no shell-outs and no new dependencies; anything that needs root goes through D17 | A separate user is possible later; the limit is stated in 18.10 |
| D17 | Operations from the dashboard | Five fixed actions (health, backup, certificate renewal test, renewal, forced renewal) through a spool directory and root-owned systemd path units; no parameters, no downloads, no shell | The dashboard never runs anything itself |
| D18 | Connection names | Random 20-character labels under the wildcard certificate, kept in a names file that the dashboard edits (Rotate, Remove old) and the DNS server re-reads every 2 s; Rotate keeps a name only if the server serves it and the public addresses lead to this server | A name the phone cannot use cuts a strict Private DNS phone off the internet |
| D19 | Base domain | Names are `<random>.<base>` under `*.<base>`, with no fixed intermediate level | A fixed name cannot work: the server refuses every name that is not on its list |

---

## 3. Overview

```
 Client (phone / PC)
      |  DNS (UDP/TCP 53; DoT 853 on the native backend)
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
  controller/             CLI commands: run, check, log, rule, lists, dashboard
  core/                   backend contract: Backend, Bound, Deps, Factory, Registry
    dnsproxy/             adapter around AdGuard dnsproxy
    native/               own server, DoT listener, names file reader, upstreams, cache, rate limiter
  dashboard/              the web dashboard: TLS server, login, API, rotation and reach checks, assets
  engine/                 the filter: ACL, rules, matcher, CNAME check, special names, health
  service/
    lists/                download, parse, cache and schedule block lists
    ops/                  the spool-directory client for the fixed operations
    qlog/                 query log writer, reader, retention
    ruleedit/             atomic edits of the rules file, shared by the CLI and the dashboard
    selfcheck/            `hecate check`: upstreams, SafeSearch, sample domains
  lib/
    certstore/            certificate pairs reloaded by modification time, picked by server name
    config/               YAML schema, strict loader, validation
    dotnames/             the DoT names file: parse, atomic write, new labels
    runlog/               size-capped operational log
  test/
    conformance/          one suite run against both backends
    nocomment/            fails the build if a comment appears in Go or PowerShell sources
frontend/                 the dashboard page: HTML, CSS, JavaScript, icons (embedded with go:embed)
configs/                  hecate.example.yaml (home), hecate.vps.example.yaml (DoT, names file, dashboard)
deploy/                   Linux files: systemd units, certificate renewal and its user, nftables rules, backup,
                          the operations wrapper (ops/), install scripts for the dashboard and the names file
scripts/                  make-bundle.sh (the install bundle for the VPS); windows/: autostart.ps1, dnsquery.ps1
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
- dnsproxy has no accept hook, so for a client outside `allowed_clients` the handler leaves `Res` nil over TCP and TLS, which makes the library close the connection after the first query (UDP still gets REFUSED).
- Every TCP connection holds one `MaxGoroutines` slot for its lifetime, shared with UDP. One LAN host can hold 256 connections and stall the service, and there is no per-client rate limiter or TCP limit. Accepted at home. This is why `dnsproxy` is never exposed to the internet and why a `dot` setting is rejected with `backend: dnsproxy`.
- On Linux dnsproxy uses `SO_REUSEPORT`, so a second instance can bind the same port silently; on Windows the second bind fails.

### 5.4 The `native` backend

Own code on `miekg/dns`, serving the home server since 2026-10-19.

| Files | Behaviour |
|---|---|
| `native.go` | Factory and single-use `Run`; sockets are bound first so a bind error returns at once |
| `server.go` | UDP and TCP listeners on every `listen` address, each bound on its own IP family (`tcp4`/`tcp6`, `udp4`/`udp6`, because Go binds `0.0.0.0` on network `tcp` as a dual-stack `[::]` socket and a second entry for `[::]` would fail); non-QUERY opcodes go to the engine (NOTIMP and logged); malformed headers are dropped silently; own TCP limit (64 total, 8 per client, IPv6 clients counted per `/64`); a TCP client outside `allowed_clients` is closed at accept, before it takes a slot, with no answer; every write has a 5 s deadline and a failed write closes the connection (miekg/dns declares `Server.WriteTimeout` but never applies it); a temporary accept error pauses the accept loop (5 ms doubling to 1 s); `tolerantReader` keeps the server alive when a UDP read fails (on Windows an oversized datagram fails with `WSAEMSGSIZE`); shutdown also closes sockets |
| `handler.go` | Rate limit, `Before`, cache, de-duplication of identical in-flight queries, upstream, `After`; `Failed` when every upstream fails; ANY answered NOTIMP; panic recovery per stage; only the upstream exchange takes a slot (256 total, 64 per client), so cache hits and block answers keep working when upstreams are slow; 11 s per query; UDP size follows EDNS (512 to 1232); the "dropping queries" warning is throttled per reason (10 s) |
| `flight.go` | De-duplication: queries with the same cache key (name, type, class, DO, CD) share one upstream exchange. Only the leader takes a gate slot; every waiter gets its own copy of the answer and its own `Failed` call, so every query is still logged, and a waiter gives up at its own deadline. A leader that panics releases its waiters |
| `dot.go`, `dotserver.go` | DNS-over-TLS listener, described in section 13.1: TLS config with the server-name allow-list, the certificate store, and the own connection loop with its two connection pools |
| `upstream*.go`, `bootstrap.go` | Weighted-random pool (1/srtt) with failover; plain UDP (retried over TCP when truncated), TCP, and DoH (RFC 8484 POST, ID 0, HTTP/2, no environment proxy, no redirects, body capped at 65,535 bytes). DoH host names resolve through plain-DNS bootstrap servers with a shared time budget. A reply is accepted only if QR, ID, opcode and question match. SERVFAIL or REFUSED from one upstream moves on to the next. **DoT as an upstream is not implemented** |
| `cache.go` | Byte-bounded LRU; key is lower-cased name, type, class and the DO and CD bits; TTL clamped by `min_ttl`/`max_ttl`; negative caching; only NOERROR and NXDOMAIN without TC are stored; upstream OPT is stripped |
| `ratelimit.go` | Token bucket per client (200/s, burst 500), IPv6 clients keyed by `/64` and IPv4-mapped addresses as IPv4 (`limitKey`, also used by the upstream gate and the TCP caps); the table holds 4,096 clients and sources that do not fit share ONE overflow bucket with the same rate, so a flood of new addresses cannot get unlimited service; a client over the limit gets no reply (a reply to a spoofed address would amplify) |
| `localreverse.go` | PTR, SOA and NS for RFC 6303 zones answered locally with NXDOMAIN |

Differences from the `dnsproxy` adapter: `native` answers ANY after `Before`, so the ACL applies and the query is logged; `native` has its own rate limiter and TCP caps (dnsproxy has none, see 5.3); only `native` serves DoT. The known trade-off of the overflow bucket: while a flood from more than 4,096 tracked sources keeps the table full, a legitimate NEW client shares the overflow budget with the flood.

---

## 6. Flow of one query

`Before`:
1. **ACL.** A client outside `allowed_clients` gets REFUSED (reason `acl`) before any matching. The list must hold at least one CIDR, so an empty list never means "allow all". Over TCP and TLS such a client gets no answer at all: `native` closes the connection at accept and `dnsproxy` after the first query.
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

See `configs/hecate.example.yaml` for every key with its default. Validation at start collects all problems and exits non-zero with clear messages: unknown or duplicate keys, more than one YAML document, an empty `allowed_clients` (to allow everyone write `0.0.0.0/0` and `::/0`), invalid CIDRs, host names in `listen`, host-name upstreams without `bootstrap`, non-`https` list URLs, unsafe or duplicate source names, unknown mode or backend, negative TTLs, empty `rules.file` or `log.dir`, duplicate `listen` entries, port 0, and a `log.max_mb_per_day` below 1. TTLs are capped before conversion to `uint32`; `cache.max_ttl: 0` means no upper bound. Relative paths resolve from the config file's directory. `Warnings()` flags an upstream or bootstrap that points at hecate's own listen address (a DNS loop), an `allowed_clients` entry of `0.0.0.0/0` or `::/0` together with a plain listener on a public address (an open resolver), and a `dot.names` entry whose first label is under 16 characters; `hecate check` fails on the loop.

The `dot` section (off by default; `configs/hecate.vps.example.yaml` is a complete DoT-only example) has `enabled`, `listen` (IP:port entries; `0.0.0.0:853` and `[::]:853` can be listed together, each is bound on its own IP family and `[::]` is IPv6 only), `cert_file`, `key_file`, `names` (see 13.1), `names_file` (optional: more names, one per line with a time stamp, edited by the dashboard and re-read every 2 seconds; a malformed line is skipped with a warning at start, and a reloaded certificate that no longer covers a served name is refused) and `reload_every` (default 10 minutes, minimum 1 second). The `dashboard` section (off by default) is described in 18.2. It is validated only when enabled and is rejected with `backend: dnsproxy`. `listen` (plain DNS) may be empty when DoT is enabled. Error messages about `dot.names` name the index, never the value. `hecate check` also loads the certificate pair and fails when it is expired, not yet valid, or does not cover every name.

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
| Rate limit | Per client (IPv6 per `/64`). `native`: active (200/s, burst 500) with an overflow bucket for sources the table cannot track. `dnsproxy`: its separate `ratelimit` middleware is not wired, which is one reason it is never exposed to the internet |
| TCP slot exhaustion | `dnsproxy`: see 5.3. `native`: own TCP limits (64 total, 8 per client), clients outside the ACL closed at accept, write deadlines, and upstream limits (256 total, 64 per client) |
| Query de-duplication | `native`: identical in-flight queries share one upstream exchange, so a burst of the same name costs one upstream query |
| Oversized UDP datagram | Handled by `native` (`tolerantReader`); covered by the conformance suite for both backends |
| Log growth | Daily size cap; ACL refusals logged one per query |
| Untrusted lists | HTTPS only, size and time limits, per-line validation, never executed |
| Public access | DoT on 853 (`native` only) with a valid certificate; clients identified by a secret host name in the TLS server name (Android Private DNS sends no auth header); two connection pools, deadlines and per-source limits; details in 13.1 |
| Secrets | TLS private keys in `data/tls/` (strict permissions, git-ignored); no secrets in the repository or example config |
| Supply chain | Pinned module versions, `go mod verify`, `govulncheck` at the end of each milestone |
| Error messages | Server logs may be detailed; DNS answers never carry internal detail |
| Privileges | Run as an ordinary user (on Linux a dedicated user with the capability to bind low ports) |

### 13.1 Public access with DNS-over-TLS

**Threat model.** One public IPv4 address (maybe IPv6) with TCP 853 open, scanned by the whole internet all day. The users are one phone (mobile data, changing addresses, carrier NAT) and maybe family. Android Private DNS in strict mode sends only a TLS handshake with the host name as the server name (SNI): no password, no header, no client certificate, no ECH.

**Identification.** `dot.names` is an allow-list of exact server names. A handshake whose SNI is not on it, or that has no SNI (someone connecting to the bare IP), is aborted before any key exchange and before any DNS message is read; nothing is answered. The comparison folds only ASCII `A-Z`, because Unicode folding maps the Kelvin sign onto `k`. This is a speed bump, not a lock: the SNI travels in clear text, and the phone also resolves the name with its ordinary resolver before it connects, so the label can appear in that resolver's logs. Accepted for a personal server. The name is a long random label (at least 16 characters from `a-z0-9`, never a dictionary word) under a domain you control; `hecate check` warns about a shorter first label. To rotate the secret without a new certificate, use Rotate in the dashboard (18.6), or put a new label in `dot.names` and in the phone's Private DNS field.

**Certificate.** One wildcard certificate (`<base>` and `*.<base>`, obtained with DNS-01) covers every label, so the secret label never appears in the public Certificate Transparency logs the way a certificate for the label itself would. The certificate must be valid and cover every configured name at start (checked with `VerifyHostname`); at reload a certificate that fails a check is refused and the old one stays in use. The files are re-read when their size or modification time changes (every `dot.reload_every`), so a renewal needs no restart; a half-written renewal (certificate before key) is rejected and picked up on the next check. A rejection for the validity dates is retried; a rejection for any other reason is remembered for that file stamp only. Unreadable files are reported once an hour. While the certificate expires within 14 days an hourly WARN is logged, and an ERROR once it has expired: with Private DNS in strict mode an expired certificate means the phone has no DNS at all.

**No client authentication and no IP allow-list for DoT.** Android cannot do client certificates, and mobile addresses change and are shared. `allowed_clients` stays in front of the TLS handshake (`0.0.0.0/0` and `::/0` on the VPS) so a narrower list still works if it is ever wanted.

**Connection handling.** DoT does not use the miekg/dns server (it serves a connection inline, applies no write deadline and takes its connection slot before the handshake). `dotserver.go` has its own accept loop and two pools, both keyed per source (IPv4 address, IPv6 `/64`):

| Stage | Cap | Deadline |
|---|---|---|
| Waiting for the TLS handshake | 512 total, 8 per source | 4 s for the whole handshake |
| Established | 64 total, 8 per source | first query within 5 s; 30 s idle between queries; 5 s to receive the rest of a frame; 5 s per write |

A silent or slow connection can only fill the first pool, so connections that finished a handshake keep working during a flood. The waiting pool can still be filled, by about 64 sources (IPv4 addresses or IPv6 `/64` prefixes, 8 each); new connections are then refused at once until the 4 s deadline frees slots. With the per-source limits of `deploy/nftables.conf` an attacker needs about 128 sources, which IPv6 makes cheap. A cap on a wider IPv6 prefix was considered and left out. After a failed write the raw connection is closed at once (closing the TLS connection would first try to send a close alert, which blocks again on a peer that does not read). Each query runs in its own goroutine (16 in flight per connection and 256 across all connections, then reading pauses) and answers are written, framed, under a per-connection mutex as they finish, so one slow query does not delay the ones pipelined behind it (RFC 7766). A connection ends after 1,000 queries, on a failed write, or on a frame shorter than a DNS header or longer than 4,096 bytes. A message with the response bit set is ignored; a message that cannot be unpacked gets FORMERR. Refusals by a cap and failed handshakes are logged as "dropping queries" with a reason (`too many tls connections`, `too many tls connections from one address`, `unknown tls server name`, `tls handshake failed`), throttled per reason, with the client address and never the server name. The query log records protocol `tls`.

**Other layers.** The per-client rate limiter and the upstream gate (section 5.4) apply to DoT queries too. On the VPS `deploy/nftables.conf` adds per-source limits on new connections and open connections for 853 and for SSH, and closes port 53; plain DNS is not served at all (`listen` is empty).

**Left out on purpose:** ECH (Android does not support it), DoH, QUIC, client-IP pinning, DoT as an upstream. Restricting TLS 1.2 to AEAD cipher suites is possible but not done.

---

## 14. Errors and resilience

- **Fail-open:** if the engine is not ready or fails internally, the query is forwarded and the error logged. Exception: ACL violations stay REFUSED, also when the ACL check or a handler before `Before` panics.
- **Upstream failure:** try the next upstream; if all fail, SERVFAIL and `Failed` logs it. Five consecutive failures produce one warning per minute (same for both backends); the first success afterwards logs the recovery. Cache hits, local answers and client cancellations are ignored.
- **Lists:** a failed download keeps the old cache; a corrupt cache file is ignored and logged.
- **Rules:** bad lines are skipped; a missing file means no rules.
- **Logs:** a full disk, a full channel or the daily cap never block a query.
- **Shutdown:** Ctrl+C cancels the context; listeners close cleanly, open DoT connections are closed, the log is flushed, and queries stuck on a silent plain upstream are cancelled by closing their connection.
- **DoT certificate:** refused at start when expired, not yet valid, unreadable, not matching its key or not covering every name; at reload the old certificate stays in use and the error is logged (section 13.1).
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
- IPv6 clients are keyed by `/64` (the usual smallest allocation to one site) for the rate limiter, the upstream gate and the connection caps, so one host cannot multiply its budget by using many addresses of its prefix. IPv4-mapped IPv6 addresses count as the IPv4 address.
- The query de-duplication is an own small implementation instead of `golang.org/x/sync/singleflight`: it needs a copy of the answer per waiter, a separate `Failed` call per waiter and each waiter's own deadline, and it avoids a new direct dependency.
- Go binds `0.0.0.0` on network `tcp` as a dual-stack socket on `[::]`, so a config that lists `0.0.0.0:853` and `[::]:853` failed with "address in use" while `hecate check` called it valid. Every listener is bound with `tcp4`/`udp4` or `tcp6`/`udp6` by address family.
- The messages and logs about `dot.names` carry the index only. The certificate error from x509 repeats the host name, so it is not wrapped.
- When the `miekg/dns` server is reused for TCP, its `WriteTimeout` field has no effect (v1.1.73 never calls `SetWriteDeadline`); the deadline is set in the connection wrapper instead.

---

## 16. Plan B and out of scope

**Plan B: APK.** Considered if tuning does not stop the redirects or stronger blocking is wanted: a local DNS filter (same power as DNS), a DNS plus HTTPS filter (stronger but heavy and risky), or a Private DNS on/off switch (light). The open-source app BlockAds (GPL-3.0) already does the first two and is a reference. The engine is plain Go without network dependencies, so it could be compiled for Android later (inference; BlockAds uses Go with gomobile).

**Out of scope:** full adblock syntax, DoQ, DNSSEC validation, per-client profiles, parental control, ECS (disabled for privacy), DHCP.

---

## 17. Risks

| # | Risk | Mitigation |
|---|---|---|
| R1 | dnsproxy's API is 0.x and may change | Pinned version, thin adapter, `native` as a fallback |
| R2 | Windows ICS holds UDP 53 | Develop on port 5300; bind a specific address |
| R3 | False positives from aggressive lists | Start from PRO; `rule add allow` takes effect within 10 s; the log shows the source |
| R4 | The phone bypasses hecate (router IPv6 DNS, browser DoH, Private DNS on) | Check that phone queries appear in the log; check Secure DNS in Chrome and DoH in Firefox |
| R5 | The two backends behave differently | Same conformance suite; decision comparison on a sample |
| R6 | The Go toolchain is downloaded automatically | `govulncheck` at the end of each milestone |
| R7 | Host off means the phone has no DNS if only that host is configured | Accepted for testing; the VPS, which runs around the clock (live since 2026-10-20), removes the dependency on the home host |
| R8 | A list source dies or changes format | Disk cache, per-line validation, warning in `lists status` |
| R9 | Ad domains keep changing | Daily updates; newly-registered-domain lists considered later |
| R10 | The redirect trigger is a script from the site's own domain | Said plainly; Firefox with uBlock Origin as a complement; Plan B |
| R11 | hecate is not up after boot (Wi-Fi not ready) | Repeating Task Scheduler trigger; a real boot is not yet tested |
| R12 | Family and friends use the home server | Per-client limits (IPv6 per `/64`) with an overflow bucket; while a flood of 4,096+ sources keeps the table full a new legitimate client shares the overflow budget |
| R13 | The secret DoT name leaks (SNI in clear text, the phone's ordinary resolver logs) | It is a speed bump by design; rotate the label in `dot.names` and on the phone; limits and deadlines apply to anyone who knows it |
| R14 | The DoT certificate expires and strict Private DNS leaves the phone without DNS | Renewal timer with a 30-day threshold, expiry warnings from 14 days, the old certificate kept if a renewal is broken |
| R15 | `deploy/` (systemd, nftables, lego units) is a small set of files that behave differently per kernel and distribution | First run on Ubuntu 24.04 on 2026-10-20 found one defect (a dynamic set used with `ct count` cannot have a timeout there), fixed; the file headers record what the first run showed, `nft -c` and a safe loading procedure are in operations.md. A real certificate renewal has not been seen yet |
| R16 | Android strict-mode handshake, ALPN and carrier NAT behaviour | Seen working on 2026-10-20 on mobile data and on Wi-Fi. The ad-test score was 95% on mobile data against 97% on Wi-Fi; the server answered every query it received, so the cause is on the phone or carrier side (not found). Fall back to opportunistic mode or to the home server if a carrier ever blocks 853 |
| R17 | A botnet of about 64+ sources (cheap with IPv6) fills the pool of connections waiting for a handshake and new clients are refused | Established connections keep working; the firewall limits per source raise the number of sources needed; a provider-level DDoS defence is out of scope for a personal server |
| R18 | Whoever knows the secret name can hold slots or ask for large answers | Deadlines, 8 connections per source, 16 queries in flight per connection and 256 overall, 1,000 queries per connection; rotate the label |
| R19 | A rotated DoT name does not lead to this server, for example because the wildcard DNS record is proxied (the proxy answers with its own addresses and does not carry port 853), and strict Private DNS then leaves the phone without internet | The DNS record for `*.<base>` must be DNS only. Rotate now asks the public resolvers for the name, runs a real DoT query against every answered address, and rolls back with `not_reachable` if one fails (18.6). It saw this exact failure on 2026-10-21 |
| R20 | A new release reaches a phone as a mix of a new page and cached old scripts or styles | Every dashboard file is served with `Cache-Control: no-cache` and an ETag, so the browser asks on each load and gets a cheap 304 (18.8) |
| R21 | The dashboard and the DNS server run as the same user, so a hijacked dashboard process reaches the rules, the logs and the certificate key | Hardened unit, no shell-outs, no new dependencies, a TLS front that needs two factors; the renewal runs as its own user, and the dashboard as its own user is possible later (18.10) |
| R22 | The dashboard's reach check dials the server's own public address from the server | It works where that address is on the host (true on this VPS); behind NAT the check would need to be switched off. It checks A records only, not AAAA (18.6) |

---

## 18. Dashboard

### 18.1 What it is

`hecate dashboard -config <file>` is a separate process with its own systemd unit and the same binary as the DNS server. It serves one page (embedded from `frontend/`) with five screens: **Status** (resolver check, certificate, lists, 24-hour counts), **Queries** (the query log grouped by name, with Block and Allow), **Rules**, **Connection** (the DoT names, the network test, Rotate and Remove old) and **Control** (the fixed operations). It talks to the DNS server only through files and one real DoT query to the loopback listener (D11). The DNS server runs unchanged without it.

```
phone browser --TLS 443--> hecate dashboard (user hecate, own unit)
                              reads   <log.dir>/queries-*.jsonl, <data_dir>/lists/*.meta.json
                              writes  <rules.file>, <dot.names_file>   (atomic, polled by the DNS server)
                              asks    127.0.0.1:853 for one real DoT query (status, and Rotate)
                              drops   an empty file in /run/hecate-ops/req/<action>   (Control, 18.7)
```

### 18.2 Configuration and commands

```
dashboard:
  enabled: true
  listen: ["0.0.0.0:443", "[::]:443"]      # a loopback-only listen may run without TLS
  names: ["<secret label>.<base>"]         # the host names it answers to
  probe_domains: [<base>]                  # bases for the network test and for Rotate
  certs: []                                # empty: use dot.cert_file and dot.key_file
  key_hash_file: /var/lib/hecate/dashboard/key.sha256
  secret_file: /var/lib/hecate/dashboard/cookie.secret     # created 0600 on first start
  session_days: 30
  ops: { request_dir: /run/hecate-ops/req, result_dir: /var/lib/hecate-ops/result }
```

Validation reports every problem at once: valid lower-case host names, a first label of at least 16 characters (a warning, as for DoT), a `listen` that differs from `listen` and `dot.listen`, certificates that together cover every name, `session_days` between 1 and 90, a hash file when enabled, and absolute, distinct `ops` directories (both empty switches the Control screen off). Commands: `hecate dashboard` (run), `hecate dashboard genkey` (writes a new access key and its hash to files, never to the terminal), `hecate dashboard hashkey` (key on stdin, hash on stdout).

### 18.3 Reaching it and logging in

1. **A secret host name** `<label>.<base>` under the wildcard certificate, which keeps it out of the public Certificate Transparency logs. An unknown server name aborts the handshake; an unknown `Host` header closes the connection without a body (DNS rebinding and host tricks).
2. **An access key** of 26 characters of Crockford base32 from `crypto/rand` (about 130 bits). It is generated on the PC, stays there (git-ignored), and only its SHA-256 is installed on the server; the comparison is constant time and the key never appears in a URL, a log or a command line.

A correct key sets the cookie `__Host-hecate_session` (Secure, HttpOnly, SameSite=Strict, Path=/, no Domain). Its value is an expiry time plus an HMAC over the expiry and the key hash, so replacing the key hash revokes every session without a restart. The lifetime is fixed at `session_days`. Login failures are limited to 5 a minute per source (IPv6 by `/64`) and 60 a minute overall, then 429 with `Retry-After`.

Transport is TLS 1.3 and HTTP/1.1 only (HTTP/2 is off on purpose: the traffic is tiny and it removes a class of protocol-level attacks). Every page response carries a Content-Security-Policy of `default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'` plus one `https://*.<probe domain>` source per probe domain, `base-uri 'none'`, `form-action 'self'` and `frame-ancestors 'none'`, with `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Cross-Origin-Opener-Policy: same-origin` and an empty `Permissions-Policy`. There is no inline script or style, no `eval` and no CDN, and the page writes data with `textContent` only. A mutating request must be a POST with `Content-Type: application/json`, the header `X-Hecate-Dashboard: 1`, an `Origin` equal to the request host, a body under 4 KiB, and the cookie.

Limits against floods: read-header 5 s, read 10 s, write 15 s, idle 30 s, 8 KiB of headers, at most 32 connections and 16 requests in flight, the per-source limits of `deploy/nftables.conf` for port 443 (60 new connections a minute with a burst of 30, 16 open), and the unit's `MemoryMax=96M`, `TasksMax=64` and `CPUQuota=50%`.

### 18.4 Rules and the query log

`POST /api/rules` parses the domain with the same function as the CLI. It refuses a public suffix (`co.id` would block a whole registry), refuses Block for protected names (every dashboard and DoT name, their parents, the probe domains and the upstream host names, so a mis-tap cannot cut the phone off from its own server), checks the size cap, and writes through the shared atomic edit (a temporary file, then rename). The answer names the rule it replaced, which makes Undo possible.

Reading the query log is bounded, because the VPS has 1 GB of memory: the window is at most 24 hours, the read stops after 2,000,000 records or 5 seconds (the answer then says it is partial), at most 200 groups are returned, answers are cached for 5 seconds (queries) and 60 seconds (status), and only one scan runs at a time.

### 18.5 The network test

`GET /probe` answers 204, with no cookie and no content, for a request to any one-label host under a probe domain. The Connection screen fetches a fresh label under each probe domain from the browser and reports which ones the network resolved and reached, with advice on which host name to use. It shows what the browser resolves, which can differ from the phone's system path when the browser's own Secure DNS is on. Every other path on a probe host is closed.

### 18.6 Connection names, Rotate and Remove old

The names the DNS server serves are `dot.names` (from the config file, read-only in the dashboard) plus the entries of `dot.names_file`. Each line of the file is a host name and a time, `#` starts a comment, writes are atomic, and at most 4 file names exist. The DNS server re-reads the file every 2 seconds, so a change needs no restart. A removed name is refused at the next poll.

**Rotate** (session and CSRF; the Rotate button waits for a passing network test for that domain, and "Rotate anyway" skips the wait) creates a label of 20 characters of `a-z0-9` from `crypto/rand` under one of the probe domains and keeps it only if all of this holds, in order:

1. the certificate covers it;
2. both public resolvers (1.1.1.1 and 8.8.8.8) answer for it;
3. it is written to the names file and the live DoT server accepts it, checked with a handshake to the loopback listener for up to 6 seconds (`not_served` otherwise);
4. **it reaches this server** (`not_reachable` otherwise): the A records from the public resolvers are collected, and every address must be a public one (loopback, unspecified, private, link-local, multicast and carrier-grade NAT addresses fail the check, because the server would reach them through itself) and must answer a real DoT query with the new name as the server name and a valid certificate, within 4 seconds.

A failure in 3 or 4 restores the previous names file, and the answer says so, or says that the file could not be restored. The messages never contain the name or an address. A restart of the dashboard waits up to 20 seconds (longer than the checks of a Rotate can take) for a running Rotate to finish or roll back, so a restart cannot leave an unchecked name in the file. The reach check exists because a proxied wildcard DNS record once produced a name that resolved to proxy addresses; resolving was not enough. It dials the server's own public address from the server, and it checks A records only.

**Remove old** refuses names that come from the config file and the last remaining name; a phone that still uses a removed name loses internet until its Private DNS field is changed, which is why the order is Rotate, type the new name on the phone, check that internet works, then Remove old.

### 18.7 Control: fixed operations without a shell

The dashboard process never executes anything. A button press creates an empty request file in `/run/hecate-ops/req/<action>`. A root-owned systemd template, `hecate-op@.path` (`PathExists=/run/hecate-ops/req/%i`), starts `hecate-op@.service`, which runs `/opt/hecate/ops/run.sh <action>`. The script is root-owned, accepts only the five names below, never reads a request file (it only removes it, without following links), writes the result with a temporary file and `mv` into `/var/lib/hecate-ops/result/` (key=value lines, then the output, at most 64 KiB, with host labels redacted), and enforces a cooldown that is measured from the previous result, so it holds even if the dashboard is hijacked.

| Action | What it runs | Cooldown |
|---|---|---|
| `health` | `deploy/health.sh` (read-only report) | 10 s |
| `backup` | `/opt/hecate/backup.sh`, then the list of the 14 newest archives | 60 s |
| `renew-test` | a certificate run on the Let's Encrypt staging CA in a private directory | 1 h |
| `renew` | `hecate-renew.service` (renews only when 30 days or fewer remain) | 1 h |
| `renew-force` | the same with `--days 90`, with a confirmation in the page | 48 h |

Not offered, on purpose: restarting `hecate`, upgrading the binary, a shell, any parameter, any upload or download. Backup archives hold the secret names, so they are never served or read by the dashboard. `POST /api/ops/run` answers 202, 404 (unknown action), 409 (already queued or running), 429 (cooldown) or 503 (feature off); a `running` result older than 30 minutes counts as failed so a killed run cannot lock an action.

### 18.8 Files, caching and the page

The server serves `index.html` and the `.js`, `.css`, `.svg` and `.png` files of the embedded folder: names of lower-case letters, digits, `-`, `_` and `.`, at most 16 files of 1 MiB each, nothing else (no sub-folders, no `.map`, no other types). Every one of them, and the page, is sent with `Cache-Control: no-cache` and an ETag. A release changes the page, the styles and the scripts together, and an earlier setting of `max-age=86400` once left a phone with a new page and old scripts and styles (the tab buttons showed in the browser's default style and did nothing). `/favicon.ico` answers an empty 204 because the page links an SVG icon, a 192 px PNG and an apple-touch icon. The bottom tabs are buttons, not links, so a long press cannot open one in a new tab; a tap changes the hash without adding a history entry, and addresses such as `#/rules` still work on reload. The page turns off the browser's tap highlight and gives its own pressed states.

### 18.9 Certificates and the renewal user

The wildcard certificate `*.<base>` is requested with DNS-01 on Cloudflare. `lego` names its files after the first domain, `_.<base>.crt` and `.key`, and `dot.cert_file` and `dot.key_file` point at them by symlinks, so a renewal is picked up by modification time without a restart. The renewal runs as its own user `hecate-acme` (so a hijacked DNS or dashboard process cannot read the Cloudflare token from `/proc`); the files are group-readable, and `post-renew.sh` sets mode 0640 and touches them, because the readers skip a file stamp they have already rejected. The dashboard reuses `dot.cert_file` and `dot.key_file` when `dashboard.certs` is empty.

### 18.10 Threat summary

| Threat | Control |
|---|---|
| Key brute force | 130-bit key, constant-time compare, 5 failures a minute per source and 60 overall, then 429 |
| The label seen by an observer (the server name is in clear text, and the phone's resolver sees it) | The second factor (the key); a label can be rotated; nothing sensitive before login |
| XSS through a hostile query name (any page can make the phone ask for any name) | `textContent` only, a CSP with no inline code, `nosniff`, names sanitized by the log and length-capped |
| CSRF | SameSite=Strict, a custom header, Origin equal to Host, JSON only |
| DNS rebinding, Host tricks | The Host must be a configured name, otherwise the connection is closed |
| Session theft | `__Host-` prefix, HttpOnly, Secure, fixed lifetime, replacing the key hash revokes all |
| Slow requests and floods | The timeouts and caps of 18.3 |
| Self-lockout by a rule | Public suffixes and protected names are refused; size and line caps |
| Cost of reading the log | The bounds of 18.4 |
| Abuse of the probe endpoint | 204 only, one-label hosts under probe domains, same connection limits |
| Rotation abuse | Needs a session and CSRF; at most 4 file names; random labels; the four checks of 18.6; the last name cannot be removed |
| A hijacked session using Control | Five fixed buttons with root-side cooldowns; no parameters and no output beyond a redacted 64 KiB |
| A planted file in the request directory | Harmless: the wrapper only removes the name |
| Supply chain | No new modules for the dashboard; `go mod verify` and `govulncheck` at each review |
| **Accepted:** a compromise of the dashboard process | It runs as the same user as the DNS server, so it reaches the rules, the logs and the certificate key. The unit is hardened and has no shell-outs; the renewal already has its own user, and the dashboard as its own user is possible later (it needs group-readable log and list files, the rules and names files and the dashboard secrets in a directory the dashboard owns, and one restart of the DNS server) |

### 18.11 Honest limits

- The dashboard cannot be reached when the VPS is down, when the network blocks the port, or when a resolver on the way refuses the whole domain (a stale negative answer for the base domain caused two outages on 2026-10-20). A backup domain under another registry was considered and is not set up; Android takes one Private DNS host name, so switching is a manual retype either way.
- Rotation helps only against a name that is blocked or throttled by its name, not against a network that blocks port 853.
- DNS cannot stop a page from opening a new window; only a browser blocker can.
