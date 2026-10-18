# dnsproxy v0.86.0 API facts

**Status: NOT VERIFIED.** Fill this in during task P1-01 (`/spike-dnsproxy`). Do not write adapter code from memory. Every entry below must be copied verbatim from the real source with file and line. Replace each `TODO` and delete this banner when the spike is complete.

Source location: `C:\Users\Administrator\go\pkg\mod\github.com\!adguard!team\dnsproxy@v0.86.0\proxy\` (non-test files seen on 2026-10-18: `bogusnxdomain.go cache.go config.go constructor.go dns64.go dnscontext.go errors.go exchange.go helpers.go lookup.go middleware.go optimisticresolver.go pending.go proxy.go proxycache.go recursiondetector.go requesthandler.go retry.go route.go server.go serverdnscrypt.go serverhttps.go serverquic.go servertcp.go serverudp.go stats.go upstreammode.go upstreams.go`). Known from a quick grep only: `config.go:67` has the field `RequestHandler Handler`.

## 1. Custom request handler

- `Handler` type, verbatim. Seen with `go doc github.com/AdguardTeam/dnsproxy/proxy.Handler` on 2026-10-18 (file:line still TODO): it is an INTERFACE, not a func type:
  ```go
  type Handler interface {
      // ServeDNS resolves the DNS request within *DNSContext.
      ServeDNS(ctx context.Context, p *Proxy, dctx *DNSContext) (err error)
  }
  ```
  So the adapter implements `ServeDNS`. `docs/architecture.md` section 5.3 speaks of a "handler function"; adjust the wording in the docs pass. How `ServeDNS` can delegate to the default resolution (so `After` can run) is still TODO.
- `proxy.Config` fields we need (listen addresses, upstream config, cache enabled/size/min TTL/max TTL, `RequestHandler`; the names are NOT known, look them up): TODO exact names and types
- How a handler produces a response and prevents normal resolution (`DNSContext.Res`? return value?): TODO
- Where the handler sits relative to the cache and the upstream exchange (order of operations, in `proxy.go` / `requesthandler.go`): TODO
- Concurrency: is the handler called from many goroutines? TODO

## 2. Hooking `After` (inspect or replace the upstream answer)

- Available hook(s) after the upstream answered (middleware, response callback, wrapping the upstream, ...): TODO
- If none: the design options (wrap `upstream.Upstream`, post-process in the handler by calling `Resolve` ourselves, ...) and the consequence for caching: TODO

## 3. Upstream configuration

- How to build `UpstreamConfig` from `https://1.1.1.1/dns-query` and bootstrap addresses (function names, options such as `upstream.Options`): TODO
- Does a DoH upstream given as an IP need `bootstrap`? Evidence (source lines or a throwaway test result): TODO
- Failover order among several upstreams (`UpstreamMode`?): TODO

## 4. Cache behavior with replaced answers

- Is the answer cached before or after our `After` replacement? TODO
- Consequence for the CNAME check on cache hits (the native backend caches before `After`): TODO

## 5. ACL / rate limiting / other options relevant to security

- Confirm dnsproxy has no per-client allow list (our engine enforces `allowed_clients`): TODO
- Options for rate limiting and request filtering (look for rate-limit, refuse-ANY and trusted-proxy options; names unknown): TODO exact names

## 6. Lifecycle

- Constructor name and signature, start and shutdown/stop methods (`constructor.go` and `proxy.go` are the first places to look): TODO
- Listener binding when a listen address is already taken: error type and behavior: TODO

## 7. Findings that change the design

- TODO (if the spike shows the architecture in `docs/architecture.md` section 5.3 does not hold, say so here and in `notes/open-questions.md`).
