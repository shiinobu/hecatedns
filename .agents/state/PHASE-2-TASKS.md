# Phase 2 - Lists, CNAME, special names: task checklist

Derived from `docs/roadmap.md` Phase 2 and the Phase 1 hand-off (`docs/phase-1-report.md`, "Serah terima ke Fase 2"). Tick a box only when the task's tests are green, the touched packages pass `gofmt`/`vet`, and the touched concurrent packages pass `-race`. Same rules as Phase 1: tasks first, docs last, TDD (RED then GREEN, also for review fixes), one fresh-context review at the end, no push without Master's explicit yes.

**Do not start until Master says "lanjut".** Phase 1 is closed; STATE.md says so.

Phase 2 is done when ALL of these hold (real binary, not only tests):
- All three lists load (about 249 thousand entries before de-duplication) and one example domain of each list answers `0.0.0.0`.
- CNAME-chain and HTTPS/SVCB tests are green.
- `hecate check` PASSES, including S3 (no `forcesafesearch.google.com` in the `www.google.com` chain).
- Lookup p99 < 50 us and process memory < 150 MB, or the real numbers are recorded with an optimization plan.

## Hand-off from Phase 1 (read before starting)

- The engine is ready for lists: `engine.NewListSet(allow, block []NamedSet)` + `Engine.SwapLists`. The `lists` package only builds one `DomainSet` per list (use `engine.ParseEntry`, it rejects IPs and single labels) and calls `SwapLists` with a new immutable snapshot. Priority is already custom allow > custom block > list allow > list block.
- `After` must gain the CNAME check. The dnsproxy cache holds the upstream answer BEFORE `After`, so the check runs on every cache hit too (keep a conformance case for a cache hit).
- `Before` must gain the special-name step (after the ACL and validation, before the rule lookup, per `docs/architecture.md` section 6).
- `hecate run` prints the note "daftar blokir belum dimuat" until a list loads; remove it once the manager exists. `hecate lists` is still a stub in `cmd/hecate/main.go`.
- dnsproxy answers some queries before our handler (ANY, question count != 1, private ARPA, recursion detection) with no ACL and no log record. This is documented, not a bug; do not "fix" it in the engine.
- Test the server with `scripts/dnsquery.ps1`, never `nslookup -port` (Windows ignores it).
- The log is capped per day (`log.max_mb_per_day`, default 256). A list download or a long session does not change that; a flood of ACL refusals is dropped and counted.
- Decision pending from Phase 1 (Q3 in `notes/open-questions.md`): an upstream equal to hecate's own listen address would loop. Cheapest fix: a `Warnings()` entry plus a `selfcheck` failure, not a refusal in `run`. Decide in task P2-07.

## Tasks

- [x] **P2-01 `lists/parser`.** Formats `domains` and `hosts` (hosts: `0.0.0.0 domain` / `127.0.0.1 domain`, ignore the localhost entries). Table tests and a fuzz target (`go test -fuzz`, no panic). Count and expose the rejected lines. Reuse `engine.ParseEntry`. Bound the line length and the number of entries (a hostile list must not exhaust memory).
- [x] **P2-02 `lists/fetch`.** HTTPS only, ETag / If-None-Match, timeout and size cap, atomic write to `data/lists/<name>.txt` (temp file + rename, with the Windows retry used by `hecate rule`), metadata (time, ETag, entry count). Tested with `httptest`. The download must not go through hecate itself (Phase 0 ruling 12: a name resolved by the system resolver may loop back to hecate); decide and record how the host name is resolved (bootstrap servers, or a dialer with the configured `bootstrap`).
- [x] **P2-03 `lists/manager`.** Load the disk cache at start, refresh in the background, build a new snapshot, swap atomically (`SwapLists`); 24 h scheduler with jitter (`lists.update_interval`); a failed download keeps the old cache; a damaged cache file is ignored with an error log and the other lists still load. Race-tested.
- [x] **P2-04 Wire the manager into `hecate run`** as `block` lists (and `allow` kind when a source declares it). Remove the "daftar blokir belum dimuat" note once a list loaded. Live check: example domains of the three lists answer `0.0.0.0`.
- [x] **P2-05 `engine/cname`.** CNAME-chain check in `After`: normalize each CNAME target; a target excluded by `allow` continues; a blocked target returns `Block` for the original question (reason `cname`, `Via` = target). Tests: blocked target, allowed target, long chain, no CNAME, malformed answer. The block answer must not be cached (it is not: the cache holds the upstream answer). Add a conformance case, including a cache hit.
- [x] **P2-06 HTTPS/SVCB and special names.** HTTPS (65) and SVCB (64) for a blocked name answer NODATA (already the `null_ip` behavior; extend the conformance case to the CNAME path). `engine/special`: `use-application-dns.net` -> NXDOMAIN, `_dns.resolver.arpa` -> NODATA, each behind its `special_names` switch.
- [x] **P2-07 `selfcheck` and `hecate check`.** Upstream reachable, SafeSearch (S3: resolve `www.google.com` through the configured upstream, fail if `forcesafesearch.google.com` is in the chain), one example domain of each list answers `0.0.0.0`. Warn when an upstream or bootstrap equals a listen address (Q3). The DNS client must work on any port (do not shell out to `nslookup`). `hecate lists status|update`.
- [x] **P2-08 Benchmarks.** Lookup p99 on about 250 thousand entries and process memory (`docs/architecture.md` section 7); record the real numbers in the report.
- [x] **P2-09 Phase gates.** Full gate set + `-race` + `govulncheck` (0 affected) + the "done when" checks above on the real binary. Evidence in `state/WORKLOG.md`. Then `/review-phase`, fix findings, `/close-phase`.

## Review / close-out

- [x] Review done (report under `state/reviews/phase-2-review.md`), Critical and Important fixed with RED then GREEN, gates green again.
- [x] Docs pass done, `docs/phase-2-report.md` written, short report to Master, `state/STATE.md` set to "Phase 2 done, waiting for lanjut", `state/PHASE-3-TASKS.md` created from `docs/roadmap.md` Phase 3. Gate G1 (look at the block statistics after a few hours of real use) is Master's to start.
