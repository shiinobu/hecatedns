# Decisions (compact; full reasons in `docs/architecture.md` section 2 and `docs/phase-0-report.md`)

Master delegated technical decisions to the implementing agent on 2026-10-18 and may overturn any of them.

## Design decisions D1-D16

| # | Decision | Choice |
|---|---|---|
| D1 | Language | Go, `go 1.26.9` (1.26.8 carries 6 called stdlib/x-net vulnerabilities: GO-2026-6603, 6607, 6611, 6612, 6613, 6617) |
| D2 | Backends | `dnsproxy` (AdGuard dnsproxy v0.86.0) and `native` (miekg/dns v1.1.73), switchable in config; one shared engine |
| D3 | Matching | Domain map, domain-suffix match (domain + all subdomains), immutable snapshot swapped atomically |
| D4 | Default lists | HaGeZi Multi PRO, HaGeZi Pop-Up Ads, ABPindo `domain.txt` (not ULTIMATE: false-positive risk) |
| D5 | Blocked answer | `null_ip` (A `0.0.0.0`, AAAA `::`), TTL 10 s; other types NODATA + synthetic SOA; mode configurable |
| D6 | CNAME | Check every CNAME target in upstream answers against blocklists (unless allowed) |
| D7 | HTTPS/SVCB, special names | type 65 on a blocked name -> NODATA; `_dns.resolver.arpa` -> NODATA; `use-application-dns.net` -> NXDOMAIN (switchable) |
| D8 | Upstreams | DoH Cloudflare `1.1.1.1` then Google `8.8.8.8` - NOT the family/filtering variants (never force SafeSearch) |
| D9 | Config | Strict YAML (`yaml.v3`, unknown key = error) |
| D10 | Query log | Daily JSONL, 7-day retention |
| D11 | CLI <-> server | Through files (rules, list cache) with mtime polling; no admin socket |
| D12 | Testing | Standard `testing`, tables, fuzz for parsers, conformance suite for both backends, TDD |
| D13 | Dev port | `127.0.0.1:5300` (5353 is mDNS, UDP 53 is held by ICS) |
| D14 | UI | None in v1 (web UI is Phase 7, only if Master wants it) |
| D15 | Code standard | Idiomatic Go; HackHub rules in `.claude/rules` do not apply |
| D16 | Git | Local, trunk-based on `main`, conventional commits |

## Master's decisions (quote when it matters)

- Build it themselves, not AdGuard Home / Blocky / Technitium. DNS system only, no VPN icon. The APK is plan B: "apk jadi plan B atau cadangan saja ... kita buat model DNS terlebih dahulu".
- Two backends, switchable, to try both: "kita coba buat 1 model saja Pendekatan 1 dan 4 (bisa di switch)".
- Test at home first (Phase 3), VPS later (budget up to about Rp50,000/month). The Android "Open supported links" toggle does not help against the Play Store redirect.
- "semua kamu yang menentukan (SAYA BERI HAK AKSES PENUH) dan jika kamu ragu kamu bisa bertanya pada saya".
- "kalau butuh plugins/skill github download saja dan langsung pakai" (read the files first, download into a fresh scratch directory).
- "kerjakan tugas terlebih dahulu dan untuk dokumen lakukan di akhir setelah tugas selesai semua".
- "agent review dilakukan saat fase sudah selesai dan sebelum masuk ke fase baru".
- Chat short, detail in files: "hasilnya disini laporan singkat saja dan detail buat file research.md".
- Declined an extra AI agent as a product component. Gemini's earlier note (local VPN app vs own Private DNS server; no `/etc/hosts` without root) matches `docs/research.md`.

## Rulings made during Phase 0 (details: `docs/phase-0-report.md`)

- ACL (`allowed_clients`) lives in the engine (dnsproxy has no client ACL); first step of `Before`; empty `allowed_clients` is a config error (use `0.0.0.0/0` and `::/0` for everyone).
- List source names double as file names: 1-64 chars `[A-Za-z0-9_-]`, start alphanumeric, not a Windows device name, unique case-insensitively.
- `.gitattributes` `* text=auto eol=lf` (Windows autocrlf made `gofmt -l` list everything).
- Work directly on `main`; no worktrees; no remote.
- Config rejects a second YAML document and unexpected CLI arguments to `check`.
- Upstream/bootstrap equal to hecate's own listen address is accepted (needs run-time knowledge; Phase 1), and list-download hostnames that resolve through hecate itself are a Phase 2 design point.

## Phase 1 hand-off decisions

- An omitted `enabled:` on a list source means true. `run` prints config `Warnings()`. Validation gains: non-empty `rules.file` and `log.dir`, unique `listen`, port != 0, TTL caps before `uint32` conversion.
- Tasks come straight from `docs/roadmap.md` (no separate plan document). Task order in `state/PHASE-1-TASKS.md` puts `acl` before `engine`, because `Before` calls it first (the earlier note listed `engine` first; either order is fine as long as the ACL step and its tests exist before the phase ends).
- Model split used by Claude (Sonnet main + Opus agents) exists only to stretch Claude's usage window. It does not apply to other agents: a single agent does everything, but runs the review in a fresh session (`/review-phase`).

## Phase 1 close-out decisions (2026-10-18)

- Priority is custom allow > custom block > list allow > list block (architecture section 6 steps 5-6 were reworded to match). The earlier numbered steps let a list `allow` beat the owner's own `block`; that was never implemented.
- The engine answers malformed requests and opcodes other than QUERY itself (FORMERR/NOTIMP, reason `invalid`, one record, after the ACL), because dnsproxy forwards odd opcodes upstream past every rule.
- A panic before `Before` returns fails closed (REFUSED) in the adapter; panics inside the engine fail open except for the ACL.
- `log.max_mb_per_day` (default 256, minimum 1 while the log is on) caps the query log; excess records are dropped and counted.
- I1 (TCP connections hold shared request slots) is accepted for the home LAN and becomes a Phase 6 prerequisite.
- `hecate rule add` refuses a result over 4 MiB (the server ignores the rest of a file that large); a rules line of 4096 bytes or more is ignored by the server and not shown by `rule list`.
- `scripts/dnsquery.ps1` was added although it was not a listed task: `nslookup -port` is ignored on Windows, so the roadmap criterion needed a replacement.

## Phase 2 close-out decisions (2026-10-18, by Gemini 4 Argon / agy)

- `DomainSet` keeps `map[string]struct{}` (Q1 closed): 250k entries across 3 lists take 16.15 MiB heap (`TestListSet_Budget250k`), 214.9–250.1 ns/op (0 allocs/op), p95 = 1.04 µs, p99 = 2.01 µs (< 50 µs budget), and full `hecate run` WorkingSet is 71.74–74.20 MiB (< 150 MB budget).
- List downloader (`internal/lists/fetch.go`) resolves HTTPS hostnames via `miekg/dns` sequentially against `bootstrap:` servers (UDP with TCP fallback on `TC=1` or network error) so a dead first bootstrap server fails over deterministically (`I1`) and hecate never queries itself while downloading lists (Q3). It reuses a cloned default `*http.Transport` (`M2a`), caps redirects at 5 (`M3`), and reuses the in-memory `*engine.DomainSet` on HTTP `304 Not Modified` when the disk stamp matches (`M2b`).
- `lists.Manager` polls cached list files every 5 s (`modTime` + `size`) so `hecate lists update` from another CLI process hot-reloads the running server within 5 s without restarting (Q2 closed).
- `selfcheck.Run` (`hecate check`) treats missing cached list files (`os.ErrNotExist`) as a warning so `hecate check -config configs/hecate.example.yaml` succeeds on a clean repo without untracked files, while verifying each present blocklist's own un-allowed sample domain (`dec.Source == src.Name`, `I2`) and checking `rules.Reload()` errors.
- `Config.LoopWarnings()` (Q3 closed) rejects upstream/bootstrap endpoints matching any configured `listen` IP:port, wildcard (`0.0.0.0`/`::`) vs local interface IP on the same port, loopback (`127.0.0.0/8`, `::1`, `localhost`) on the same port when listening on loopback or wildcard, or any local interface IP on the same port (`M5`).
