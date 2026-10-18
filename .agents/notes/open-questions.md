# Open questions, blockers and unverified items

Add new questions at the bottom with a number. When one is answered, move it to the "Answered" section with the answer and the date.

## For the implementing agent to resolve (technical)

*(No open technical blockers for Phase 3; see Q4 below for Phase 6.)*

## For Master (do not ask early; ask at the gate)

- **G1, end of Phase 2 / during PC use:** Review block statistics in `xdns log -blocked` after real browsing and add `xdns rule add allow <domain>` if any false positive occurs.
- **G2, start of Phase 3 (partly answered 2026-10-18):** Hyper-V + WSL2 are present and ICS (`SharedAccess`) holds `0.0.0.0:53` UDP, so wildcard port 53 is taken (details in `state/STATE.md`). Browser for the first test: Chrome. Answered the same day: Master can open the router at `192.168.1.1` and reserved the PC at `192.168.1.99` (was DHCP `.10`; verified 2026-10-18); PC and phone share the same Wi-Fi; Master allowed binding port 53 on `192.168.1.99` only (not wildcard) for Phase 3 tests. G2 is closed.
- **G3, start of Phase 6:** VPS provider and cost approval (check current prices first), subdomain name.
- Whether to push the repo anywhere as a backup: needs Master's explicit yes.

## Unverified (never state as fact)

- `docs/architecture.md` section 18 items 3, 6, 7, 8: canary/DDR behavior in Firefox and Chrome on the phone, RedMagic reaction to `0.0.0.0` vs NODATA vs NXDOMAIN, whether EDE (RFC 8914) helps, the ICS / UDP 53 resolution (all scheduled for Phase 3 / Phase 7).
- `docs/research.md` section 8 (research gaps and unverified claims).
- Which domain triggers the Play Store redirect on Master's phone (to be captured in Phase 3).
- Real quota cost of Opus vs Sonnet in Claude (measured with `/usage`; Claude-side only).

## Answered

- 2026-10-18: Commit `.agents/`? Master said yes, and also asked for `.claude/` ("commit sekalian .claude"). Done in two commits; `.claude/settings.local.json` stays gitignored (local setting, turns the ECC guard off on this PC only). Master may ask to track it too.
- Still open from Master: a root `AGENTS.md` pointer to `.agents/` (offered, not created).

**Q1. Registry import cycle (answered 2026-10-18, Phase 1).** `Backend`, `Deps`, `Factory`, and `type Registry map[string]Factory` live in `internal/backend`, while callers (`cmd/xdns` and `conformance`) construct the map so `backend` never imports `dnsproxybe` or `native`.

**Q2. dnsproxy integration points (answered 2026-10-18, Phase 1).** Verified in `.agents/notes/dnsproxy-api-facts.md` and `docs/architecture.md` §5.3: `proxy.Config.RequestHandler` wraps `Before` -> `p.Resolve` -> `After`, and the dnsproxy cache stores upstream answers *before* `After` so CNAME inspection runs on every cache hit.

**Q3. Run-time loop detection (answered 2026-10-18, Phase 2).** `Config.LoopWarnings()` (included in `Warnings()`) detects when an upstream or bootstrap address resolves to one of xdns's own `listen` addresses (including wildcard/loopback combinations and `localhost`), and `selfcheck.Run` (`xdns check`) fails when a loop is present (`af34c50`, `7fd1ac0`).

**Q4. TCP slot exhaustion (Phase 1 review I1, accepted until Phase 6).** dnsproxy gives every accepted TCP connection one slot of the shared request semaphore (`MaxGoroutines`, 256) for the life of the connection (idle timeout 10 s, extended by each request). The ACL runs per request inside our handler, so any host that can reach the port can open 256 idle TCP connections and stall UDP service too. Live measurement: a UDP query went from 1 ms to a 4 s timeout while 256 connections were held. Accepted for the home LAN (Phases 1-5). MUST be fixed before any public deployment (Phase 6): a second `proxy.Proxy` for TCP only with its own pool (a split cache is acceptable), or a connection-level ACL / per-IP connection limit in front of dnsproxy. **Update 2026-10-19 (Phase 4):** the `native` backend does not have this problem: its TCP connections have their own pool (64 total, 8 per client address, `server.go`) and upstream queries have their own gate (256 total, 64 per client, `gate.go`), so held TCP connections cannot stall UDP. It still has no ACL check at accept. The `dnsproxy` backend still needs the fix before Phase 6.

**Q5. Concurrent `xdns rule` edits.** `rule add`/`remove` read-modify-write without a lock; two simultaneous invocations can lose an update (12 parallel adds kept all 12 in the review, so it is rare). Revisit only if it ever bites.


**Q6. Upstream-failure warning when the link flaps (Phase 5 review M3, open).** `internal/engine/health.go` resets the one-minute throttle on recovery, a rule `TestAfter_SaysWhenTheUpstreamsAreBackAfterAWarning` pins on purpose ("a new outage a second later is announced at once"). A link that fails 5 times, answers once, and repeats produces a WARN and an INFO per cycle with no throttle (bounded by the 5 MB `xdns.log` cap). Decide which behaviour is wanted before changing it. Related: `errBusy` ("too many upstream queries in flight", a local limit in `native`) counts as an upstream failure, and an upstream that answers SERVFAIL never warns.

**Q7. Which backend runs on the VPS (Phase 6, open).** The debt in `PHASE-6-TASKS.md` section B differs per backend (`dnsproxy` needs a TCP slot pool or per-IP limit and a rate limiter; `native` needs de-duplication and the limiter overflow fix). Deciding first can let one backend's debt stay documented instead of paid. Decide at the start of Phase 6, with Master.

**Q8. First real boot of the Task Scheduler task (Phase 5, open).** Verified with throwaway tasks and a manual start, never through a PC boot. After the next start of the PC check `powershell -File scripts/autostart.ps1 status` and `data\logs\xdns.log`; if xdns is not running about a minute after boot, read the log (a bind failure is now written there) and the task's last result before changing anything.
