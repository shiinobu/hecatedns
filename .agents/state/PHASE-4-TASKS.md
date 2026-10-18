# Phase 4 - Native backend + conformance suite: task checklist

Derived from `docs/roadmap.md` Phase 4 and `docs/phase-3-report.md` ("Serah terima ke Fase 4"). Tick a box only when the task is verified and recorded.

**Do not start until Master says "lanjut".** Phase 3 is closed; `STATE.md` says so.

**Boundaries (unchanged):** never push, never change system settings (PATH, `go env`, firewall, services), never bind `0.0.0.0:53` or stop ICS. The only allowed port-53 bind is `192.168.1.99:53` (Master's permission, 2026-10-18); development port is `127.0.0.1:5300`. TDD (RED then GREEN), docs only at phase end, one fresh-context review (`/review-phase`, include `security-reviewer`-style checks because `native` faces the network) before the next phase, `CGO_ENABLED=1` only per race command, do not edit `.claude/`.

Phase 4 is done when ALL of these hold (from the roadmap):
- The same conformance suite is green on BOTH backends (`dnsproxy` and `native`) from one table.
- Switching backend needs one config line (`backend: native`).
- Block/pass decisions on a sample of about 1,000 domains (from the query log and the lists) are identical on both backends; any difference is explained.
- A short phone test with `backend: native` repeats S1 and S2 (see "Phone test" below).

## What already exists (Phase 3 hand-off facts)

- Backend contract: `internal/backend` (`Backend`, `Deps`, `Factory`, `Registry`); `dnsproxybe` is the reference adapter; `internal/backend/native` is an empty stub (no test files). Callers (`cmd/xdns`, `conformance`) build the registry map, so `backend` never imports `dnsproxybe` or `native` (Q1).
- Conformance suite: `internal/backend/conformance` (`suite.go`, `harness.go`, fake upstream on `127.0.0.1:0`), currently run only against `dnsproxybe`.
- Engine contract: `Before` / `After` / `Failed`, see `.agents/rules/04-engine-contract.md`. The `native` handler must call the same engine; decisions must not live in the backend.
- miekg/dns v1.1.73 is already a dependency (`dns.Client`, `dns.Server`, `dns.Msg`); read the source before using any signature (`.agents/workflows/spike-dnsproxy.md` describes the method; do the same for miekg).
- Live setup: PC `192.168.1.99`, phone `192.168.1.150`, local `xdns.yaml` (gitignored), `bin\xdns.exe`, firewall rules group `xdns` are tied to the path `bin\xdns.exe`. A hidden `xdns run` may still be running (check `Get-Process xdns`; stop with `Stop-Process -Name xdns` before binding the same address with another binary).
- Q4 (TCP slots shared with UDP) still open; the native server must not repeat it: give TCP its own limit from day one if cheap, and record the choice. It is mandatory before Phase 6 either way.

## Tasks

- [ ] **P4-01 `native/cache`.** TTL-aware cache keyed by (name, type, class), min/max TTL from config, size cap in MB, negative caching. Tests first: expiry, TTL clamping, size eviction, concurrent access (`-race`). The cache must hold the UPSTREAM answer, so the engine's `After` (CNAME check) runs on every cache hit, same as the dnsproxy backend.
- [ ] **P4-02 `native/upstream`.** DoH (the default upstreams are `https://1.1.1.1/dns-query` and `https://8.8.8.8/dns-query`), DoT, plain UDP/TCP, with failover and weighted random choice. Accept an upstream answer only when its ID and question match the request. Bootstrap IPs for hostname upstreams. Tests with a fake upstream (never the real internet in unit tests).
- [ ] **P4-03 `native/server`.** UDP and TCP listeners on every `listen` address using `miekg/dns`; per-request goroutine limit; TCP idle timeout and a separate TCP connection limit (Q4). Tests: bind failure, UDP truncation to TCP, oversized messages, graceful shutdown.
- [ ] **P4-04 `native/handler` and `native/ratelimit`.** Handler: `Before` -> cache -> upstream -> `After`, `Failed` on upstream errors (fail-open rules from the engine contract). Per-client rate limit. Panic in `Before` answers REFUSED, as in the dnsproxy adapter.
- [ ] **P4-05 Register `native`.** Add the factory to the registry in `cmd/xdns`; `backend: native` works with `xdns run` and `xdns check`.
- [ ] **P4-06 Conformance on both backends.** Run the same table against `dnsproxy` and `native`; fix every behavior difference (do not special-case a backend in the table unless the difference is documented and accepted).
- [ ] **P4-07 Comparison script.** About 1,000 domains (sample from `data/logs/queries-*.jsonl` and the three lists): run through both backends, compare block/pass decisions, report differences with the reason.
- [ ] **P4-08 Security pass.** Bind addresses, ACL, rate limit, answer validation (ID and question), resource limits, message size limits; fuzz the handler input where cheap.
- [ ] **P4-09 Phone test with `native`.** Repeat S1 (`adblock.turtlecute.org`, `canyoublockit.com` eXtreme) and the Shopee check (`s.shopee.co.id` still blocked) with `backend: native` on the same setup. Ask Master to switch nothing on the phone; only the server config changes.
- [ ] **P4-10 Phase gates and review.** `/gates` (gofmt, vet, build, test with coverage, `-race`, `go mod verify`/`tidy -diff`, `govulncheck` at `GOPATH\bin\govulncheck.exe`, `xdns check`), then `/review-phase` with a fresh-context reviewer, fix Critical and Important findings with RED -> GREEN.

## Phone test (P4-09) notes

- The phone stays on static IP `192.168.1.150`, DNS 1 `192.168.1.99`, DNS 2 empty. Android caches answers for a short time; wait about a minute or toggle airplane mode after a server swap.
- The phone clock is 12-hour: screenshot "11:19" is 23:19 in the log.
- Expect the same results as Phase 3 (`docs/test-report.md`): 97% with the two custom rules (`ad.doubleclick.net`, `adservice.google.com`), `s.shopee.co.id` blocked. Custom rules live in `data/rules.txt` (gitignored); the native backend must read the same file.

## Review / close-out

- [ ] `/gates` green, `/review-phase` done, findings fixed.
- [ ] Docs pass (`/close-phase`): `docs/phase-4-report.md`, `docs/architecture.md` (native backend facts), `docs/roadmap.md`, `README.md`.
- [ ] `state/STATE.md` set to "Phase 4 done, waiting for lanjut", `state/PHASE-5-TASKS.md` created.
