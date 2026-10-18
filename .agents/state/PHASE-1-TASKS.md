# Phase 1 - Engine core + dnsproxy backend: task checklist

Derived from `docs/roadmap.md` Phase 1 and the Phase 0 hand-off (no separate plan document, by Master's rule). Tick a box only when the task's tests are green, the touched packages pass `gofmt`/`vet`, and the work is committed. Order matters; dependencies are noted. Use `/implement-task` for each.

Suggested ids are for tracking only. API names inside tasks are suggestions: keep the documented semantics (`rules/04-engine-contract.md`), choose clean names yourself.

Phase 1 is done when ALL of these hold (real binary, not only tests):
- `xdns run` listens on `127.0.0.1:5300` and `scripts/dnsquery.ps1 example.com -Port 5300` answers over DoH (Windows nslookup ignores -port).
- `xdns rule add block contoh.test` makes that name answer `0.0.0.0` within 10 s without restart (S5).
- The query log holds both queries.
- A client outside `allowed_clients` gets REFUSED.
- The conformance suite is green on the `dnsproxy` backend.
- `engine` package coverage is at least 80%.

## Tasks

- [x] **P1-00 Baseline.** `git log --oneline -3` shows `4b546fc` on top (or later commits listed in `state/WORKLOG.md`); `go test ./... -count=1` prints two `ok` lines. Record anything else in the log.
- [x] **P1-01 dnsproxy API spike** (`/spike-dnsproxy`). Produce `notes/dnsproxy-api-facts.md` with verbatim signatures and file:line. No adapter code before this is done. Needs: nothing.
- [x] **P1-02 `engine/matcher`.** `DomainSet` with domain-suffix matching (the entry and all its subdomains) and an atomically swapped immutable snapshot. Tests first: exact match, subdomain, deeper subdomain, label in the middle does not match, `notexample.com` does NOT match `example.com`, uppercase, trailing dot, single-label entry rejected, empty set, readers running while a new snapshot is swapped (under `-race`). Add a benchmark skeleton (full-size benchmark is Phase 2).
- [x] **P1-03 `engine/rules`.** Parser for `data/rules.txt` lines `block <domain>` / `allow <domain>`, `#` comments, bad lines skipped with their line numbers (never fatal), priority allow > block, reload when the file's mtime changes (poll interval from config, default 2 s), missing file = no rules. Tests include broken lines, duplicate entries, CRLF, BOM, huge line. Add a fuzz test for the line parser.
- [x] **P1-04 `engine/response`.** The four block modes (`null_ip`, `nxdomain`, `nodata`, `refused`) with synthetic SOA, TTL = `block.ttl`; copy ID and question, set RA, keep RD. Tests per type: A, AAAA, HTTPS (65), TXT, plus a question with multiple/zero entries handled safely.
- [x] **P1-05 `engine/acl`.** Parse `allowed_clients` CIDRs into a fail-closed checker; REFUSED outside the set. Tests: IPv4 and IPv6, IPv4-mapped IPv6 (`::ffff:192.168.1.5`), first/last address of a CIDR and the address just outside, single-CIDR list, `0.0.0.0/0` + `::/0` allows all, invalid client address -> refused.
- [x] **P1-06 `engine/engine`.** `Before`, `After`, `Failed` exactly as `rules/04-engine-contract.md` (ACL -> validate -> normalize -> allow -> block -> pass; Phase-1 `After` only passes non-NOERROR and logs; CNAME check comes in Phase 2). Fail-open on internal error (ACL stays closed), one-time warning when no lists are loaded, exactly one log record per query, never mutate inputs. Define the small logger interface in `engine`. Tests including a concurrency test hitting the real path from many goroutines (run under `-race`). Needs: P1-02..P1-05.
- [x] **P1-07 `qlog`.** JSONL writer (`data/logs/queries-YYYYMMDD.jsonl`, one JSON per line, local time with offset), buffered channel (full -> drop and count, a query never waits for disk), daily rotation, retention (`log.retention_days`, delete old files at start and at midnight), sanitized strings; reader with filters for `xdns log` (`-since`, `-grep`, `-blocked`, `-around HH:MM -window 30s`). Tests with a temp dir and a fake clock. Do NOT build the 5,000-record ring buffer (that is Phase 7).
- [x] **P1-08 `backend` + `dnsproxybe`.** `Backend` interface and `Deps`, backend selection by name (unknown name = error at start), and the dnsproxy adapter: map `listen`, `upstreams`, `bootstrap`, `cache` to `proxy.Config`; handler calls `Before`; use `After`/`Failed` per the spike findings. Resolve the registry import cycle first (`notes/open-questions.md` Q1). Needs: P1-01, P1-06, P1-07.
- [x] **P1-09 `conformance` v1.** One table of backend constructors (only `dnsproxy` now; `native` joins in Phase 4). Fake upstream = miekg/dns server on `127.0.0.1:0`; backend under test on a random port. Cases: blocked A/AAAA/HTTPS, passed query, allow beats block, ACL REFUSED, log record written. Needs: P1-08.
- [x] **P1-10 CLI.** `xdns run [-config f] [-backend dnsproxy|native]` (prints `Warnings()`, graceful shutdown on Ctrl+C via context), `xdns log [-since] [-grep] [-blocked] [-around] [-window]`, `xdns rule add block|allow <domain>` / `rule remove <domain>` / `rule list` (atomic write: temp file + rename). Indonesian messages, exit codes as in `main.go`. Keep `lists` as a stub (Phase 2). Needs: P1-06..P1-08.
- [x] **P1-11 Config hardening** (roadmap task 10): a source without `enabled:` means true; `rules.file` must be non-empty; `log.dir` non-empty when logging is on; `listen` entries unique and port != 0; TTL values capped before the `uint32` conversion; `run` prints `Warnings()`; add `/xdns.yaml` to `.gitignore`. Each rule gets a failing test first.
- [~] **P1-12 Phase gates.** Full gate set + `-race` + `govulncheck` (0 affected) + the "done when" checks above on the real binary. Evidence goes into `state/WORKLOG.md`. Then `/review-phase`, fix findings, `/close-phase`.

## Review / close-out

- [ ] Review done (report saved under `state/reviews/phase-1-review.md`), Critical and Important fixed with RED then GREEN, gates green again.
- [ ] Docs pass done (list in `state/STATE.md`), `docs/phase-1-report.md` written, short report given to Master, `state/STATE.md` set to "Phase 1 done, waiting for lanjut", `state/PHASE-2-TASKS.md` created from `docs/roadmap.md` Phase 2.
