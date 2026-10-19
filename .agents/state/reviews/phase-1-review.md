# HecateDNS Phase 1 review

Reviewer: Opus agent `hecate-reviewer`, fresh context, read-only. Range `4b546fc..HEAD`, HEAD `ec0065d` (code as of `f825312`). Saved verbatim by the main session on 2026-10-18; the triage and what was done about each finding is in the section "Triage" at the end (added by the main session, not by the reviewer).

## Verdict
Phase 1 mostly holds up. Every "done when" criterion passes on the real binary. The engine core, the rules parsing, the qlog writer and reader, and the config hardening are solid, and the tests are hard to fool (most mutations were caught). Running the binary turned up three real problems, and none of them can be seen by reading the hecate code alone: (1) any host can stall the whole server with 256 idle TCP connections, and the ACL does not stop it; (2) refused queries from clients outside the ACL can fill the disk through the query log; (3) queries with an opcode other than QUERY skip the rules and are forwarded upstream. There is also one flaky test, which fails about a third of the time on this machine. **Counts: Critical 0, Important 4, Minor 12.** The Important findings should be fixed, or accepted explicitly as LAN-only risks with a recorded decision, before Phase 2.

## Gates re-run
- `gofmt -l .`: empty. `go vet ./...`: clean. `go mod verify`: all verified. `go mod tidy -diff`: empty.
- `govulncheck`: **0 affected**. One module-level advisory is not called by our code: GO-2026-5932, x/crypto/openpgp, no fix available.
- `go test ./... -cover -count=1`: all ok. Coverage: engine 96.9, dnsproxybe 94.9, qlog 88.9, config 97.3, cmd 92.2, backend 100, conformance 82.0.
- `CGO_ENABLED=1 go test -race ./...`: ok with `-count=1`, ok with `-count=3`, ok with `-count=2` under a 24-goroutine CPU burner. `GOMAXPROCS=1 go test -count=3` on the timing-sensitive packages: ok.
- `-run TestBindFailureEndsRunAndReleasesListeners -count=300`: **100 of 300 runs FAIL** (finding I4).
- Live binary, built from a HEAD copy in scratch: two instances on 127.0.0.1:5400 (allow 127/8) and 127.0.0.1:5401 (allow only 10/8). Queries were sent with a throwaway miekg/dns probe, garbage packets with Python. Everything was killed and deleted afterwards; the repo is untouched.

## Findings

**I1 - Important - `internal/backend/dnsproxybe/config.go:18-25,98` (MaxGoroutines 256). The source of the behavior is dnsproxy `proxy/servertcp.go:113-120` and `handleTCPConnection`.**
- What is wrong: each accepted TCP connection holds one slot of the request semaphore for its whole life. dnsproxy keeps an idle connection for 10 s, and every new request extends that. UDP shares the same semaphore. The ACL only runs per request inside our handler, so a host outside `allowed_clients` can take all 256 slots with idle connections. UDP service then stops for everyone. The code comment ("a flood queues in the socket buffer") only considers UDP.
- Live evidence: on the 10/8-only instance, a UDP query from 127.0.0.1 normally gets REFUSED in 1 ms. While 256 idle TCP connections were held: `ERROR after 4.001s: i/o timeout`. A TCP query took 2.9 s. The holder kept reconnecting and service stayed degraded (2.1 s). Each reset also writes an ERROR line to stderr (256 lines).
- Fix direction: give TCP its own slot pool, for example a second `proxy.Proxy` for TCP only with its own `MaxGoroutines`, sharing the handler. A cache split between the two is acceptable, since TCP is rare. At minimum, record this as an accepted LAN-only risk that must be fixed before Phase 6.

**I2 - Important - `internal/engine/engine.go:176-180` (every ACL refusal is recorded), `internal/qlog/writer.go` (no size cap), `internal/qlog/retention.go` (retention by days only).**
- What is wrong: a client outside `allowed_clients` decides how fast the query log grows.
- Live evidence: one 127.0.0.1 UDP sender against the 10/8-only instance, 5 s, about 441k packets of 271 bytes. `queries-20261018.jsonl` grew by **72,425,650 bytes in 5 s**, about 14 MB/s, or about 1 TB/day. No drops were reported because the writer kept up. This breaks "no unbounded growth from attacker-controlled input" (05-security).
- Fix direction: rate-limit or aggregate ACL refusals per client (for example, the first N per minute, then a counted summary). Add a per-day byte cap that drops and counts, with an optional `log.max_mb_per_day`.

**I3 - Important - `internal/engine/engine.go:139-142,185-188,218-225` and `internal/backend/dnsproxybe/handler.go:62-68`.**
- What is wrong: `Before` passes any opcode other than QUERY without filtering, on the documented assumption that "the backend can answer FORMERR or NOTIMP". dnsproxy does not do that: `validateRequest` only checks the question count, ANY and ARPA. So STATUS, NOTIFY and UPDATE queries from an allowed client are resolved upstream and skip every rule. This violates `04-engine-contract.md` step 2.
- Live evidence: with `rule add block wikipedia.org`: opcode 0: `www.wikipedia.org A 0.0.0.0`; opcode 2: `CNAME dyna.wikimedia.org` plus `A 103.102.166.224` from `https://1.1.1.1:443`; opcode 5: the same real answer; the log shows `LOLOS ... A www.wikipedia.org NOERROR 20ms https://1.1.1.1:443/dns-query`.
- Fix direction: the engine should answer NOTIMP itself (as a Block decision with a record), or the adapter should answer NOTIMP before `Resolve`. Add a conformance case "opcode != QUERY is not forwarded".

**I4 - Important (flaky gate) - `internal/backend/dnsproxybe/backend_test.go:249-288`.**
- What is wrong: the test takes a random TCP port and assumes the UDP port with the same number can be bound. On this PC the UDP excluded port ranges (Hyper-V/WinNAT: 49674-49973, 50161-50260, ...) make `Start` fail on the UDP bind instead. The final `net.ListenPacket` then fails with WSAEACCES.
- Evidence: 100 failures in 300 runs, e.g. `udp listener opened before the failure was not released: listen udp 127.0.0.1:50179: bind: An attempt was made to access a socket in a way forbidden by its access permissions`.
- Fix direction: bind UDP `:0` first, then take TCP on that same port number (retry until free). Close the UDP socket, keep the TCP one.

**M1 - Minor - `handler.go:71-73`, `engine.go:159-167` (RCode fixed to SERVFAIL at 162).** A PTR query for a private address from an allowed client gets NXDOMAIN from dnsproxy (no private upstream, `ErrNoUpstreams`). The log records `GALAT ... PTR ... SERVFAIL 0ms selecting upstream: no upstream specified`. Over TCP each such query also produces a `level=ERROR msg="handling request"` line. Fix: record the rcode of `dctx.Res`, treat `ErrNoUpstreams` as an answered query.

**M2 - Minor - dnsproxy `proxy/proxy.go:968-994` (answers before our handler).** These answers skip the ACL and the query log: ANY gets NOTIMP (with an OPT record even when the request had none); a question count other than 1 gets FORMERR; private ARPA from a non-private client and recursion detection get NXDOMAIN. Verified for clients outside the ACL: ANY gets NOTIMP, 0 and 2 questions get FORMERR. No cache data and no recursion reach those clients, and every such answer is no larger than the request. Docs item, plus a note that "exactly one record per query" covers only queries that reach the engine.

**M3 - Minor (latent) - `handler.go:96-106`.** A panic that escapes `Before` takes the fail-open path (`resolveUnfiltered`) without knowing whether the ACL passed, so a client outside the ACL would be resolved. Cannot happen today (`clientAllowed` and `filter` recover internally), fragile for Phase 2. Fix: in the filter stage, answer REFUSED unless the ACL check is known to have passed.

**M4 - Minor - `cmd/hecate/main.go:21-23`.** `hecate` with no arguments still lists `run`, `log` and `rule` as "(belum diimplementasi)".

**M5 - Minor - `cmd/hecate/rule.go:161-174` vs `engine/rules.go:168-172`.** A line of 4096 bytes or more is skipped by the server but shown by `rule list` as an active rule. Fix: same length check in the CLI.

**M6 - Minor - `engine/rules.go:145-147,185-194`.** With at least 100 issues before the 4 MiB cut, `ErrFileTooLarge` is swallowed by the issue cap, so truncation is silent. Fix: always report the file-level issue.

**M7 - Minor - `engine/response.go:81-88`.** Block answers are not compressed: a 271-byte HTTPS query gets a 598-byte answer (2.2x), allowed clients only. Fix: `resp.Compress = true`. Related: ACL REFUSED answers set RA=1; consider RA=0 for `ReasonACL`.

**M8 - Minor - config semantics.** `validate.go:174` rejects `min_ttl: 30s, max_ttl: 0s` although dnsproxybe treats 0 as "no bound". An empty `enabled:` (YAML null) means true, and `<<` merge keys are rejected inside sources. Decide and document.

**M9 - Minor - `cmd/hecate/rulesedit.go`, `rule.go:91-111`.** `rule add`/`remove` read-modify-write without a lock, so two concurrent invocations can lose an update (reasoning only; 12 parallel adds kept all 12). Adding a rule to a file just under 4 MiB can push the new rule past the server's cut-off.

**M10 - Minor - `scripts/dnsquery.ps1:42-48`.** `Skip-Name` never ends on a header-only reply (out-of-range index yields `$null`). Fix: bounds-check `$pos`.

**M11 - Minor - test gaps (survived mutations).** Not tested: the ACL `/96` mapped-prefix boundary; a rules change with the same mtime but a different size; the claim of `TestParseRules_FileOverTheSizeCapKeepsOnlyWholeLines` (the cut lands in comment padding); the drop counter after `Close`; SERVFAIL after a panic inside `Resolve`.

**M12 - Minor - CLI text.** Domain rejection reasons are English inside Indonesian messages and give no punycode hint. Fix commit 7777774 shipped without a regression test.

## Mutation results
| Mutation | Result |
|---|---|
| ACL: no `Unmap()` | caught |
| ACL: zone not dropped | caught |
| ACL: mapped prefix `>=96` changed to `>96` | **SURVIVED** |
| classify: list allow before custom block | caught |
| failClosed sets allowed=true | caught |
| RulesFile stamp ignores size | **SURVIVED** |
| Lookup: drop the 253-byte guard | caught |
| sanitize: only `IsControl` replaced | caught |
| ParseRules keeps the cut line at 4 MiB | **SURVIVED** |
| purge also deletes non-regular entries | caught |
| purge off by one | caught |
| Writer.Record without the closed check | **SURVIVED** |
| rule CLI without the size refusal | caught |
| config TTL cap removed | caught |
| config Source unknown-key check removed | caught |
| handler: no SERVFAIL after a panic in Resolve | **SURVIVED** |
| response: RA not set | caught |
| response: SOA minimum 0 | caught |
| Failed: rcode not set | caught |
| handler: Cached always false | caught |
| handler: Failed not called | caught |
| RefuseAny false | caught |
| run: query log closed before the backend | caught |

## Things the reviewer checked that are fine
ACL (mapped clients, zones, CIDR edges, REFUSED over UDP and TCP for A, DS, private PTR, STATUS/NOTIFY; no cache data or recursion for refused clients; empty list is an error). Malformed packets (empty, zero header, QDCOUNT without question, compression loop, bad OPT, random 512 bytes, 256-byte name): FORMERR or no reply, server stays up. Block answers live (A `0.0.0.0` TTL 10, HTTPS/TXT NOERROR + SOA, CD/RD copied, OPT mirrored, ID and question copied). Records: cache hits reach `After` with `Cached`; blocked queries never reach upstream or cache. Engine concurrency. qlog (drop and count, idempotent Close, midnight timer in Open, purge only regular `queries-YYYYMMDD.jsonl`, sanitizing, reader skips lines over 64 KiB). CLI and config (atomic writes, hostile `rule add` arguments rejected, `-config` anywhere, `run` exits 1 on a taken port, log closes last). Code rules (files < 800 lines, doc comments, no query names in server stderr).

## Docs-worthy facts (missing from STATE "Docs to write")
1. dnsproxy answers some queries before our handler, with no ACL and no log record: ANY gets NOTIMP (with OPT); a question count other than 1 gets FORMERR; private ARPA from non-private clients and recursion detection get NXDOMAIN; packets marked as responses are dropped.
2. dnsproxy forwards opcodes other than QUERY (I3).
3. TCP connections hold a MaxGoroutines slot for up to 10 s idle, extended by every request (I1).
4. Private PTR queries get NXDOMAIN because no private RDNS upstream is configured, and are logged as GALAT (M1).
5. The query log has no size cap, and ACL refusals are logged one per query (I2).
6. On Windows the modes 0600/0700 are not enforced (files inherit the parent folder's ACL), and renaming over an open file fails because Go opens files without FILE_SHARE_DELETE (hence the rename retry).
7. Windows excluded port ranges affect tests and listen ports (I4).
8. Windows `nslookup` ignores `-port`; the roadmap criterion should use `scripts/dnsquery.ps1` or a miekg/dns client.
9. The rules reload stamp is modification time plus size.
10. `cache.max_ttl: 0` semantics, YAML-null `enabled` means true, merge keys rejected in sources (M8).
11. Block answers are uncompressed (M7); REFUSED answers carry RA=1.
12. govulncheck reports one module-level advisory, GO-2026-5932 (x/crypto/openpgp, not called).

## "Done when" criteria (live, port 5400 instead of 5300)
DoH answers ok; `rule add block contoh.test` gives `0.0.0.0` after about 1.1 s with no restart ok; the log shows passed and blocked queries ok; a client outside `allowed_clients` gets REFUSED ok; conformance suite green ok; engine coverage 96.9% ok.

## Not verified by the reviewer
Graceful Ctrl+C on a real Windows console and the log flush on it; lost updates from concurrent `rule add` (reasoning only); behavior with a public-IP client (private-ARPA NXDOMAIN path, read from source); Linux/Android filesystems with coarse modification times (the stamp-size gap).

---

## Triage (main session)

See the section "Review triage" in `state/WORKLOG.md` and `docs/phase-1-report.md` for what was fixed, with commit ids. Status is filled in below as findings are closed.

| Id | Severity | Status | Commit / decision |
|---|---|---|---|
| I1 | Important | **Accepted as a LAN-only risk, must be fixed before Phase 6** | 256 idle TCP connections from one LAN host can hold all `MaxGoroutines` slots. Master's network is a home LAN with trusted devices; the fix (a second `proxy.Proxy` for TCP with its own pool, or a connection-level ACL) is deferred and written into the Phase 6 gate (`notes/open-questions.md` Q4, `STATE.md`). |
| I2 | Important | Fixed | `e9fb444` daily byte cap `log.max_mb_per_day` (default 256) in qlog + config; RED/GREEN, mutant caught |
| I3 | Important | Fixed | `c8e351b` engine answers FORMERR/NOTIMP itself (reason `invalid`, one record, after the ACL); new conformance case; mutant caught by conformance and engine tests |
| I4 | Important | Fixed | `3dbeaa1` TCP port chosen with a free UDP twin; 300/300 runs pass (was 200/300) |
| M1 | Minor | Fixed | `d35f0ae` a local no-upstream answer (private PTR) is recorded as answered with its real rcode |
| M2 | Minor | Docs | dnsproxy answers ANY/question-count/private-ARPA/recursion before our handler; "one record per query" covers only queries that reach the engine |
| M3 | Minor | Fixed | `d35f0ae` a panic before `Before` returned answers REFUSED |
| M4 | Minor | Fixed | `599a526` usage text |
| M5 | Minor | Fixed | `599a526` `rule list`/`remove` apply the 4096-byte line limit (`engine.MaxRuleLineBytes`) |
| M6 | Minor | Fixed | `fc7a1d3` file-level issue bypasses the issue cap |
| M7 | Minor | Fixed | `c8e351b` answers compressed, ACL REFUSED has RA=0 |
| M8 | Minor | Fixed + docs | `fc7a1d3` `max_ttl: 0` = no cap; YAML-null `enabled` = true and merge keys rejected in sources are documented behavior |
| M9 | Minor | Partly | `599a526` `rule add` refuses a result over 4 MiB; concurrent edits can still lose an update (documented, no lock) |
| M10 | Minor | Fixed | `b626fee` bounds checks, verified against a fake header-only server |
| M11 | Minor | Fixed | `fc7a1d3`, `b626fee`: /96 boundary, same-mtime-other-size, cut line, drop after Close; the "SERVFAIL after a panic in Resolve" mutant is equivalent (the nil fallback gives the same answer) |
| M12 | Minor | Fixed | `599a526` punycode hint; `b626fee` regression test for the midnight timer |

Gates after the fixes (HEAD of the fix series): gofmt empty, vet clean, `go test ./... -cover` ok (engine 97.1, dnsproxybe 95.4, qlog 89.1, config 97.3, cmd 92.4), `-race -count=2` ok, mod verify/tidy clean, govulncheck 0 affected, Linux build ok, live smoke test ok.
