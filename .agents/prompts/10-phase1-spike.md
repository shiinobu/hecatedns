# 10 - Phase 1, step 1: dnsproxy API spike (task P1-01)

Use after Master says "lanjut" for Phase 1 and the `/resume` baseline is green. Paste everything between the lines.

---

Master sudah bilang "lanjut" untuk Fase 1. Kerjakan HANYA tugas **P1-01** dari `.agents/state/PHASE-1-TASKS.md`.

Run the workflow `.agents/workflows/spike-dnsproxy.md` completely:

- Read the real dnsproxy v0.86.0 source in the module cache (path in the workflow) and `go doc` output; fill every `TODO` in `.agents/notes/dnsproxy-api-facts.md` with verbatim signatures and file:line. Mark what you cannot confirm as `UNVERIFIED` with the reason.
- Answer: (1) how the request handler is plugged in and how it returns its own answer; (2) where an `After`-style hook can inspect or replace the upstream answer, or what the alternatives are and their caching consequence; (3) whether a DoH upstream given as an IP needs bootstrap and how to construct the upstream config; (4) how the cache treats replaced answers and what that means for the CNAME check; (5) rate-limit/ACL options and lifecycle; (6) anything that contradicts `docs/architecture.md` section 5.3.
- Known already: `Handler` is an interface with `ServeDNS(ctx context.Context, p *Proxy, dctx *DNSContext) error` (seen via `go doc`). Do not assume anything else.
- No adapter code. No edits under `docs/`. Add a WORKLOG entry, tick P1-01, update `.agents/state/STATE.md` ("Docs to write at phase end" gets the corrections to architecture sections 5.3 and 18).
- Finish with a short Indonesian report: how the hook situation looks, the CNAME/cache consequence, whether the architecture holds as drawn, and whether you need a decision from Master (only if costly to change later; give one recommendation).

---
