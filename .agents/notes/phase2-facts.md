# Phase 2 facts and code map (verified 2026-10-18 by Claude, before handing Phase 2 to another agent)

Everything marked VERIFIED was run or read on 2026-10-18 against the repo at `0a505f2`. Baseline at that time: `go build`, `go vet`, `gofmt -l` clean, `go test ./... -count=1` all ok.

## 1. The three default lists (VERIFIED with curl)

| name | URL host | entries | notes |
|---|---|---|---|
| hagezi-pro | cdn.jsdelivr.net (`.../wildcard/pro-onlydomains.txt`) | 202,297 | weak ETag `W/"..."`, `Cache-Control: max-age=604800, s-maxage=43200`, gzip + chunked (no Content-Length), about 3.8 MB decoded |
| hagezi-popups | cdn.jsdelivr.net (`.../wildcard/popupads-onlydomains.txt`) | 47,668 | same headers; header says `Expires: 8 hours` |
| abpindo | raw.githubusercontent.com (`.../subscriptions/domain.txt`) | 124 | strong ETag, `max-age=300`, 2,572 bytes, header says `Expires: 4 days`; its title says "(hosts)" but the data lines are plain domains |

- Total about 250 thousand entries before de-duplication (matches the roadmap figure).
- Line shape: header lines start with `#`; data lines are one lowercase ASCII domain, no spaces, no wildcards, no CR, no trailing dot, longest line 109 bytes (HaGeZi PRO). The parser must still treat all of this as untrusted: strip `#` comments and blanks, accept `\r\n`, bound the line length, bound the entry count, count rejected lines.
- Conditional GET works on both hosts: `If-None-Match` with the stored ETag returns `304` and an empty body. Send the stored ETag verbatim (including the `W/` prefix).
- Go's `http.Transport` adds `Accept-Encoding: gzip` and decompresses transparently, so the size cap MUST be applied to the decoded stream (`io.LimitReader` on `resp.Body`), otherwise a gzip bomb passes a compressed-size check. Treat hitting the cap as a failed download, never as a truncated-but-accepted list.
- jsDelivr `@latest` may lag the origin by up to its edge cache time (12 h); acceptable with a 24 h refresh.

## 2. Seams in the existing code (VERIFIED by reading the files)

- `internal/engine/lists.go`: `NamedSet{Name string; Set *DomainSet}`, `NewListSet(allow, block []NamedSet) *ListSet`, `(*ListSet).LookupAllow/LookupBlock/HasBlock`. `Engine.SwapLists(*ListSet)` (engine.go) publishes it atomically; nil means no lists. `HasBlock` is false for lists with zero entries.
- `internal/engine/matcher.go`: `NewDomainSet(raw []string) (set *DomainSet, rejected int)` already runs `ParseEntry` on each raw line and counts rejections; `Match`, `Lookup`, `Len`. `internal/engine/name.go`: `NormalizeName`, `ParseEntry(raw) (string, error)` (rejects IPs and single labels).
- `Engine.After(ctx, q, resp, m)` currently only records and returns `Decision{Verdict: Pass}` (engine.go:152). The CNAME check goes here. `Decision` has `Reason`, `Source`, `Via`; `ReasonCNAME` already exists and the query log already formats `cname:<list> via <target>`. Build the block answer with the existing `Responder.Block(q.Req)`; do not mutate `resp`.
- `Engine.filter` (engine.go:197): order is questionName check, `warnIfNoBlockLists`, `classify(rules, lists, name)`. The special-name step goes after the ACL/validation and before `classify` (docs/architecture.md section 6). `classify` has the priority custom allow > custom block > list allow > list block; reuse it for each CNAME target.
- `internal/backend/dnsproxybe/handler.go`: `ServeDNS` calls `Before`, then `p.Resolve`, then `h.after` which replaces `dctx.Res` with `dec.Resp` when `After` returns Block. So the CNAME check needs NO adapter change, and it already runs on cache hits (the cache holds the upstream answer). Add a conformance case with a cache hit (`internal/backend/conformance/suite.go`, `harness.go`; fake upstream on `127.0.0.1:0`).
- `cmd/hecate/run.go`: `serve()` prints the note "daftar blokir belum dimuat (Fase 2)..." at line 74, creates `engine.NewRulesFile`, the qlog recorder and `newEngine`; the lists manager must be started there with the engine and `cfg.DataDir` (lists live under `data_dir/lists/<name>.txt`), and the note removed once a list loads. Shut it down with the same `ctx`.
- `cmd/hecate/check.go` only loads and prints the config summary today; `hecate check` must grow the live checks (upstream reachable, S3 SafeSearch, example domain per list). `hecate lists` is still a stub in `cmd/hecate/main.go`.
- Atomic write with the Windows retry already exists: `cmd/hecate/rulesedit.go` (`renameWithRetry`, 5 attempts, 40 ms). It is in package `main`; the list fetcher in `internal/lists` cannot import it, so copy the small helper or move it to a shared internal package (decide, record in WORKLOG, keep DRY).
- Config (`internal/config/config.go`): `Lists{UpdateInterval time.Duration; Sources []Source}`, `Source{Name, URL, Kind, Format string; Enabled bool}`, `SpecialNames{FirefoxCanary, DDR bool}`, `Bootstrap []string` (IPs), `DataDir`. Source names are already validated as safe file names.
- Empty packages waiting for code: `internal/lists` and `internal/selfcheck` (only `doc.go`). `internal/engine` has no `cname.go` or `special.go` yet.

## 3. Design recommendations for the open points (not yet decided; the implementer decides and records in WORKLOG)

- **Downloader must not resolve through hecate (P2-02, Phase 0 ruling 12).** Recommended: an `http.Client` whose `Transport.DialContext` uses a `net.Resolver{PreferGo: true, Dial: ...}` that dials the configured `bootstrap` IPs on port 53 (fallback: system resolver only when `bootstrap` is empty, with a log line). HTTPS only: reject any other scheme, also after redirects (`CheckRedirect`). Timeout 30 s, cap 64 MB decoded.
- **Q3 (P2-07):** `config.Warnings()` entry when an upstream or bootstrap host:port equals a `listen` address, plus a `selfcheck` failure; do not refuse in `run`.
- **selfcheck DNS client:** use `miekg/dns` (`dns.Client`/`Exchange`) so any port works; never shell out to `nslookup`.
- **Benchmark (P2-08):** `testing.B` for `DomainSet.Match` on a generated set of about 250 thousand names with p99 measured by timing many lookups, plus `runtime.ReadMemStats` or the process working set after loading the real lists. Targets: p99 < 50 microseconds, memory < 150 MB; if missed, record real numbers and an optimization plan instead of hiding it.

## 4. Still unverified

- Run-time behavior of the downloader on a real connection loss, a disk-full write and a 304 loop; the live end-to-end load of all three lists (that is P2-04 and P2-09).
- Real memory and p99 with 250 thousand entries (P2-08).
