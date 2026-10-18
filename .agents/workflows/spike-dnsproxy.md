---
description: Phase 1 task P1-01 - read the real dnsproxy v0.86.0 source and record verbatim API facts in notes/dnsproxy-api-facts.md before any adapter code is written.
---

# /spike-dnsproxy

Purpose: answer `docs/architecture.md` section 18 items 1-2 from the real source, so the adapter (P1-08) is built on facts. No adapter code in this task. No edits under `docs/`.

## Where to read

- Source: `C:\Users\Administrator\go\pkg\mod\github.com\!adguard!team\dnsproxy@v0.86.0\proxy\` (the module is already downloaded because the repo imports it). Files to start with: `config.go` (line 67 has `RequestHandler Handler`), `requesthandler.go`, `dnscontext.go`, `middleware.go`, `proxy.go`, `proxycache.go`, `constructor.go`, `upstreams.go`, `exchange.go`.
- `go doc github.com/AdguardTeam/dnsproxy/proxy.Config` and `go doc -all github.com/AdguardTeam/dnsproxy/proxy.Handler` work from inside the repo (module-aware).
- The upstream package (DoH/DoT/plain upstream construction, bootstrap) is in the same module: `...dnsproxy@v0.86.0\upstream\`. The miekg/dns v1 types are in `...\pkg\mod\github.com\miekg\dns@v1.1.73\`.
- pkg.go.dev for dnsproxy v0.86.0 is a cross-check only (its docs still contain TODOs).

## Questions to answer (all in `notes/dnsproxy-api-facts.md`)

1. Exact `Handler` type and `proxy.Config` fields needed (listen addresses, upstream config, cache settings, `RequestHandler`) with file:line.
2. How a handler returns its own response and stops the default resolution; the order of operations (handler, cache, upstream exchange) in the request path; whether the handler runs concurrently.
3. A hook point for `After` (inspecting or replacing the upstream answer). If none exists, list the real options (for example wrapping the upstream, or resolving inside the handler) and what each does to caching.
4. Whether a DoH upstream given as an IP (`https://1.1.1.1/dns-query`) needs a bootstrap; how to build the upstream config (function names, options). Prove it from source; a tiny throwaway program in a scratch directory that builds the config is fine (do not contact the internet from tests in the repo).
5. How the cache treats answers replaced after the fact; the consequence for the CNAME check on cache hits.
6. Rate-limit/ACL-related options (confirm there is no per-client allow list), constructor/start/stop lifecycle, bind-failure behavior.

## Output

- Fill every `TODO` in `notes/dnsproxy-api-facts.md` with verbatim signatures and file:line. Mark anything you could not confirm as `UNVERIFIED` with the reason; never fill a gap from memory.
- If the findings contradict `docs/architecture.md` section 5.3 (for example no usable `After` hook), write the consequence and your recommended design into the file and `notes/open-questions.md` Q2. Decide yourself unless it is expensive to change later; then ask Master with one concrete recommendation.
- Add a WORKLOG entry and tick P1-01. Commit no Go code (there is none); the notes file and state updates are committed with the next `/handoff` or `/close-phase` as `chore: update agent state`.
- Short Indonesian report: what the handler/hook situation is, what the CNAME/cache consequence is, whether the architecture holds.
