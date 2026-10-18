# Phase 5 - Stabilisation: task checklist

Derived from `docs/roadmap.md` Phase 5 and `docs/phase-4-report.md` ("Serah terima ke Fase 5"). Tick a box only when the task is verified and recorded.

**Do not start until Master says "lanjut".** Phase 4 is closed in code (P4-09, the phone test with `native`, may still be open: check `STATE.md`).

**Boundaries:** never push; no system change without Master's approval. P5-02 (Task Scheduler) changes Windows settings, so it needs Master's explicit yes first, as the firewall rules did. Never bind `0.0.0.0:53` or stop ICS. Development port stays `127.0.0.1:5300`; the live bind `192.168.1.99:53` is allowed.

Phase 5 is done when S4 holds (`docs/architecture.md` section 1): 7 days running without a crash or a manual restart, stable memory, pass-query (cache miss) latency p95 < 150 ms.

## Owed from Phase 4 (do these first)

- [ ] **P5-00 Full review of Phase 4.** Master allowed only a cheap review in Phase 4. Run `/review-phase` with the `xdns-reviewer` agent over the commit range `7fd1ac0..HEAD` (the whole `internal/backend/native/` package and `scripts/compare`), network-facing checks included. Fix Critical and Important findings with RED -> GREEN. Record in `.agents/state/reviews/phase-4-review.md`.
- [x] **P5-01 Phone test with `native` (P4-09), if not done yet.** Done 2026-10-19 (see `docs/test-report.md` section 5); the home server now runs `native`. The native rate limit was raised to 200/s, burst 500 (`4361b64`). Original text: Same setup, only `backend: native` changes in `xdns.yaml`; repeat S1 and the Shopee check; record in `docs/test-report.md`. Decide with Master which backend the home server runs.

## Tasks (from the roadmap)

- [ ] **P5-02 Start at boot.** Task Scheduler task (highest privileges, run at startup, restart on failure) for `bin\xdns.exe run -config xdns.yaml`. Needs Master's approval before it is created; document the rollback (`Unregister-ScheduledTask`). The firewall rules are tied to the path of `bin\xdns.exe`.
- [ ] **P5-03 Clean shutdown and failure warnings.** Verify graceful Ctrl+C on a real Windows console (not yet verified); log a warning when upstreams fail several times in a row (both backends).
- [ ] **P5-04 Log retention and disk bounds.** Rotation and retention already exist (`log.retention_days`, `max_mb_per_day`); check `data/` stays bounded over days and test the full-disk behaviour (not verified yet).
- [ ] **P5-05 Seven-day run.** Record memory, query count, errors and latency percentiles per day; no manual restart. Compute p95 of pass queries with cache misses from the raw log.
- [ ] **P5-06 Gates, docs pass, report.** `/gates`, `docs/phase-5-report.md`, `docs/architecture.md`, `docs/roadmap.md`, `README.md`; `STATE.md`; create `PHASE-6-TASKS.md`.

## Debt that must be paid before Phase 6 (VPS, open to the internet)

- Per-client-IP TCP connection limit for both backends (Q4 for `dnsproxy`; `native` has its own pool of 64 but no per-IP limit).
- Per-client rate limiter for `dnsproxy` (`native` has one).
- Query de-duplication for `native` (dnsproxy has `PendingRequests`).
- Private reverse lookups (ARPA) from non-private clients: dnsproxy answers NXDOMAIN, `native` forwards (see `architecture.md` section 5.4).
- DoT as an upstream for `native` (deferred by Master) if still wanted.
