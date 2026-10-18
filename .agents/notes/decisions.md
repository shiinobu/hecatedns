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
- Upstream/bootstrap equal to xdns's own listen address is accepted (needs run-time knowledge; Phase 1), and list-download hostnames that resolve through xdns itself are a Phase 2 design point.

## Phase 1 hand-off decisions

- An omitted `enabled:` on a list source means true. `run` prints config `Warnings()`. Validation gains: non-empty `rules.file` and `log.dir`, unique `listen`, port != 0, TTL caps before `uint32` conversion.
- Tasks come straight from `docs/roadmap.md` (no separate plan document). Task order in `state/PHASE-1-TASKS.md` puts `acl` before `engine`, because `Before` calls it first (the earlier note listed `engine` first; either order is fine as long as the ACL step and its tests exist before the phase ends).
- Model split used by Claude (Sonnet main + Opus agents) exists only to stretch Claude's usage window. It does not apply to other agents: a single agent does everything, but runs the review in a fresh session (`/review-phase`).
