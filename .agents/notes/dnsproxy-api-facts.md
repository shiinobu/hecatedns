# dnsproxy v0.86.0 API facts

**Status: VERIFIED from source on 2026-10-18 (task P1-01).** Everything below was read in the real source, not recalled. Items marked `NOT RUN` are read from source but not yet exercised at run time; the adapter task (P1-08) must prove them with tests. Paths are relative to `C:\Users\Administrator\go\pkg\mod\github.com\!adguard!team\dnsproxy@v0.86.0\`; `proxy/` = package `github.com/AdguardTeam/dnsproxy/proxy`, `upstream/` = package `.../upstream`.

## Verdict (read this first)

The architecture in `docs/architecture.md` section 5.3 HOLDS, with one wording fix: `Handler` is an interface, not a function type. The design is a **single wrapping handler**:

```go
func (h *handler) ServeDNS(ctx context.Context, p *proxy.Proxy, dctx *proxy.DNSContext) error {
    // 1. dec := engine.Before(...)        -> Block: dctx.Res = dec.Resp; return nil
    // 2. err := p.Resolve(ctx, dctx)      -> dnsproxy default path: cache lookup, else upstream, then cache store
    // 3. err != nil: engine.Failed(...)   (dctx.Res already holds SERVFAIL); return err
    // 4. dec := engine.After(..., dctx.Res, meta) -> Block: dctx.Res = dec.Resp
    // 5. return nil
}
```

There is no separate "After hook" and none is needed: `Resolve` returns after the cache and upstream work, so the handler inspects and may replace `dctx.Res` afterwards. **The cache stores the upstream answer BEFORE our replacement** (it packs the message into bytes, so replacing the pointer or mutating later cannot corrupt it). Therefore a CNAME check in `After` (Phase 2) runs on every cache hit, exactly like the native backend. No design change, no new open question.

## 1. Handler

- `proxy/requesthandler.go:14-17`
  ```go
  type Handler interface {
      // ServeDNS resolves the DNS request within *DNSContext.
      ServeDNS(ctx context.Context, p *Proxy, dctx *DNSContext) (err error)
  }
  ```
- `proxy/requesthandler.go:11` `const ErrDrop errors.Error = "drop response"` - returned by a handler to send NO reply (`server.go:100-103`). Do not use it for blocked queries (the client would time out); use it only if a deliberate silent drop is wanted.
- `proxy/requesthandler.go:21-29` `DefaultHandler` calls `p.Resolve(ctx, proxyCtx)`. `proxy/requesthandler.go:34` `type HandlerFunc func(ctx context.Context, p *Proxy, dctx *DNSContext) (err error)` adapts a function. `proxy/middleware.go:4-18` `Middleware` interface (`Wrap(handler Handler) (wrapped Handler)`) and `MiddlewareFunc` exist; not needed.
- `proxy/config.go:64-67` `RequestHandler Handler` ("optional custom handler ... used instead of DefaultHandler if set"). Wired in `proxy/proxy.go:441` `requestHandler: cmp.Or[Handler](c.RequestHandler, DefaultHandler{})`.
- Call site `proxy/server.go:84-111` `handleDNSRequest`: drops packets with `Response` set; sets `d.IsPrivateClient`; `d.Res = p.validateRequest(d)` (FORMERR for != 1 question, NXDOMAIN for recursion detection / private ARPA from a non-private client) and ONLY IF `d.Res == nil` calls `p.requestHandler.ServeDNS(ctx, p, d)`; then `p.respond(ctx, d)` writes `d.Res` whatever the handler returned (the returned error is only logged: `serverudp.go:179-181`). So a handler error does NOT stop the reply; `d.Res` is what the client gets.
- **Response contract.** To answer by itself the handler sets `dctx.Res` and returns nil. If `dctx.Res == nil` after the handler, `respondUDP` sends nothing (`proxy/serverudp.go:194-197`): the client just times out. The adapter must therefore always leave a non-nil `Res`.
- **Concurrency.** The handler runs on many goroutines: UDP spawns `go func(){ ... p.udpHandlePacket ... }()` per packet (`proxy/serverudp.go:113-117`), bounded only if `Config.MaxGoroutines > 0` (`proxy/proxy.go:470-476`, `proxy/config.go:168`); TCP/TLS per connection (`proxy/server.go:61-67`). The handler and the engine must be goroutine-safe.
- **No `recover()` anywhere in package `proxy`** (grep over `proxy/*.go`, 2026-10-18). A panic in our handler, engine or response builder kills the whole process. The adapter handler MUST recover, log, and fail open (see "Findings" below).
- `DNSContext` (`proxy/dnscontext.go:16-99`) exported fields we use: `Upstream upstream.Upstream` (`:31`, "In case of cached response it's nil"), `Req *dns.Msg` (`:55`), `Res *dns.Msg` (`:58`), `Proto Proto` (`:61`), `Addr netip.AddrPort` (`:72`, the client; may be IPv4-mapped IPv6, normalize with `Unmap()`), `RequestID uint64` (`:81`). `QueryStatistics()` (`:138`) gives per-upstream timing; not needed.
- `Proto` values (`proxy/proxy.go:47-65`): `ProtoUDP "udp"`, `ProtoTCP "tcp"`, `ProtoTLS "tls"`, `ProtoHTTPS "https"`, `ProtoQUIC "quic"`, `ProtoDNSCrypt "dnscrypt"`. These strings match `engine.Query.Proto`.

## 2. `After`: inspect or replace the upstream answer

- `func (p *Proxy) Resolve(ctx context.Context, dctx *DNSContext) (err error)` - `proxy/proxy.go:903`. Order inside (`:903-964`): ECS processing (only if `EnableEDNSClientSubnet`) -> `calcFlagsAndSize` -> `cacheWorks` (`:998`) -> if cache works: `addDO(dctx.Req)` (mutates `Req` ONLY when `DNSSECEnabled`, `:880-895`), pending-request queue, `replyFromCache` (`proxy/proxycache.go:21`) -> on hit `filterMsg`, `scrub`, return nil -> else `replyFromUpstream` (`:794`) -> `cacheResp` (`:951`, only if `ok && !Res.CheckingDisabled`) -> `filterMsg` -> `scrub` -> `return err`.
- On a cache hit `dctx.Upstream` stays nil and `dctx.Res` is a freshly unpacked message (`proxy/cache.go:116-158` `unpackItem` builds `res := (&dns.Msg{}).SetRcode(...)` from bytes). On an upstream answer `dctx.Upstream` is the answering upstream (`proxy/proxy.go:857-876` `handleExchangeResult`) and `Res.Authoritative` is cleared.
- **Failure.** If all upstreams fail, `Resolve` returns a non-nil error AND `dctx.Res` is already a SERVFAIL built by `handleExchangeResult` (`proxy/proxy.go:864-868`). With no usable upstream, `Res` is NXDOMAIN and the error wraps `upstream.ErrNoUpstreams` (`:797-802`). `Failed` is therefore: `err != nil` after `Resolve`.
- Meta for `engine.Meta`: `Upstream` = `dctx.Upstream.Address()` when non-nil (`upstream/upstream.go:43`); `Cached` = `dctx.Upstream == nil` after a successful `Resolve` with non-nil `Res`; `Latency` = measure `time.Since` around `Resolve` in the handler.
- `ServFailMaxCacheTTL` is referenced in `proxy/cache.go:501` (negative/SERVFAIL caching exists); not relevant to us.

## 3. Cache and replaced answers

- `proxy/proxycache.go:84-136` `cacheResp(d)` -> `cache.set(d.Req, d.Res, d.Upstream, p.logger)` (`proxy/cache.go:331`) -> `respToItem` then `item.pack()` (`proxy/cache.go:92-110`): the message is `Pack()`ed into a byte slice stored in a `glcache`. A `get` unpacks a new `dns.Msg` each time (`:116`, `:228`). Consequences:
  1. the cache always holds the UPSTREAM answer, never our block answer (we replace `dctx.Res` after `Resolve` has already cached);
  2. mutating `dctx.Res` after `Resolve` cannot poison the cache; still, the engine rule "never mutate input, copy first" stays;
  3. the block answer is rebuilt by the engine each time (no caching of blocks in dnsproxy). Blocked queries never reach `Resolve`, so they never touch cache or upstream.
- `Resolve` mutates `dctx.Req` only when `Config.DNSSECEnabled` (`addDO`) or `EnableEDNSClientSubnet` (`processECS`). Keep both false so `q.Req` handed to the engine is never changed behind its back.
- Cache config fields (`proxy/config.go`): `CacheEnabled bool` (`:209`), `CacheSizeBytes int` (`:147`), `CacheMinTTL uint32` seconds (`:150`), `CacheMaxTTL uint32` seconds (`:153`), `CacheOptimistic bool` (`:212`, keep false), `CacheOptimisticAnswerTTL`, `CacheOptimisticMaxAge`. Mapping: `cache.size_mb*1<<20 -> CacheSizeBytes`; `cache.min_ttl/max_ttl` durations -> seconds `uint32` (cap before converting; this is task P1-11). NOTE: a `CacheMaxTTL`/`CacheMinTTL` of 0 means "no override" (`respectTTLOverrides`, used in `proxy/server.go:194`); `NOT RUN`: confirm that `max_ttl: 0` means unlimited rather than "zero TTL" with a test.
- `PendingRequests *PendingRequestsConfig` (`proxy/config.go:55-58`): the comment says "If nil, it will be enabled by default" but the code does the opposite - `pendingRequestsOrDefault` (`proxy/proxy.go:501-507`) returns the real tracker ONLY when `conf != nil && conf.Enabled`. Set `&proxy.PendingRequestsConfig{Enabled: true}` explicitly (anti cache-poisoning dedupe; recommended).

## 4. Config fields we map (`proxy/config.go`)

| xdns | `proxy.Config` field | Type | Line |
|---|---|---|---|
| `listen` | `UDPListenAddr`, `TCPListenAddr` | `[]*net.UDPAddr`, `[]*net.TCPAddr` | 111, 115 |
| `upstreams`, `bootstrap` | `UpstreamConfig` | `*proxy.UpstreamConfig` | 70 |
| `cache.enabled` | `CacheEnabled` | `bool` | 209 |
| `cache.size_mb` | `CacheSizeBytes` | `int` | 147 |
| `cache.min_ttl`, `cache.max_ttl` | `CacheMinTTL`, `CacheMaxTTL` | `uint32` seconds | 150, 153 |
| - | `RequestHandler` | `proxy.Handler` | 67 |
| - | `Logger` | `*slog.Logger` (nil = slog.Default with prefix) | 39 |
| - | `UpstreamMode` | `proxy.UpstreamMode` | 107 |
| - | `MaxGoroutines` | `uint` | 168 |
| - | `RefuseAny` | `bool` (ANY -> NOTIMP, anti-amplification) | 180 |
| - | `BindRetryConfig` | `*proxy.BindRetryConfig{Interval time.Duration; Count uint; Enabled bool}` (`proxy/retry.go:12-22`), nil = no retry | 95 |

- `Config.Validate()` (`:284`) requires a valid `UpstreamConfig`; `Fallbacks` may be nil (not "empty"). `New(c *Config)` is `proxy/proxy.go:397`: `func New(c *Config) (p *Proxy, err error)`.
- UDP and TCP are separate listen lists: for each xdns `listen` entry build BOTH a `*net.UDPAddr` and a `*net.TCPAddr` (plain DNS needs both).
- **No client ACL in `Config`** (read all 230 lines of the struct): only `TrustedProxies` (for DoH proxies), `PrivateSubnets` (for PTR handling). The engine enforces `allowed_clients`, as designed.
- **Rate limiting is NOT in `proxy.Config`.** It is a separate package `ratelimit` of the same module: `ratelimit/ratelimit.go:34` `func NewMiddleware(c *Config) (m proxy.Middleware)`, which wraps a `proxy.Handler` and returns `proxy.ErrDrop` for limited UDP clients (`ratelimit/ratelimit.go:51-63`). So `docs/architecture.md` section 13 wording "opsi rate limit bawaannya" should read "paket ratelimit (middleware)". Phase 6 can wrap our handler with it; `ratelimit` pulls extra dependencies (`go-rate`, `go-cache`) - not needed in Phase 1.
- Upstream selection modes (`proxy/upstreammode.go:13-26`): `UpstreamModeLoadBalance` (default), `UpstreamModeParallel`, `UpstreamModeFastestAddr`. `load_balance` is a WEIGHTED RANDOM pick by past RTT with fail-over to the others on error (`proxy/exchange.go:49-71`), NOT strict "try in listed order". `docs/architecture.md` section 10 comment "dicoba berurutan" is therefore inexact for this backend. Options: keep the default and fix the comment in the docs pass (recommended), or `parallel` (queries all upstreams, fastest wins, doubles upstream traffic). Decide in P1-08; record it.

## 5. Upstream and bootstrap construction

- `proxy/upstreams.go:96-99`
  ```go
  func ParseUpstreamsConfig(lines []string, opts *upstream.Options) (conf *UpstreamConfig, err error)
  ```
  Each line is one upstream (`https://1.1.1.1/dns-query`, `tls://...`, `8.8.8.8`, `udp://...`). Returns a partially filled config plus a joined error that contains `*ParseError{Idx}` (`:123-143`) per bad line. `nil` opts is allowed.
- `upstream/upstream.go:62-110` `Options`: `Logger *slog.Logger`, `Bootstrap Resolver` (`:94`; "If nil, net.DefaultResolver will be used"), `HTTPVersions []HTTPVersion` (`:98`, default HTTP/1.1 and h2), `Timeout time.Duration` (`:102`, also bootstrap timeout; "Zero value disables the timeout"), `InsecureSkipVerify bool` (keep false), `PreferIPv6 bool`, `RootCAs`, `VerifyConnection`, ...
- `upstream/upstream.go:188` `func AddressToUpstream(addr string, opts *Options) (u Upstream, err error)` - single upstream. `Upstream` interface (`:33-47`): `Exchange(ctx, req) (resp, err)`, `Address() string`, `io.Closer`.
- **DoH upstream given by IP needs NO bootstrap**: `upstream/doh.go:93-94` `newDoH` calls `addPort(addr, defaultPortDoH)` (`upstream/upstream.go:152`, 443) and `upstream/doh.go:114` sets `getDialer: newDialerInitializer(addr, opts)`; `upstream/upstream.go:401-408` `newDialerInitializer`: `if netutil.IsValidIPPortString(u.Host)` it dials that IP directly without any resolution. `https://1.1.1.1/dns-query` becomes host `1.1.1.1:443` -> direct. The dialer is created lazily, so building the config contacts no network (`NOT RUN`: prove with a throwaway program/test that `ParseUpstreamsConfig` returns without error and without network).
- A hostname upstream needs `opts.Bootstrap`. Build it from xdns `bootstrap` (list of IPs): `upstream.NewUpstreamResolver(ip, nil)` per IP (`upstream/resolver.go:50`; returns a resolver plus an error that is `NotBootstrapError` when the address itself is a hostname), then `upstream.ConsequentResolver{r1, r2}` (`upstream/resolver.go:36`, tries in order; `ParallelResolver` at `:31` queries all). Config validation already requires an IP list in `bootstrap` when an upstream names a host. Close what you create (`UpstreamResolver` embeds `Upstream`; closing is the caller's job, `upstream/resolver.go:48-49`).
- Timeout: dnsproxy's `defaultTimeout = 10 * time.Second` (`proxy/proxy.go:39`) is used for client writes; the upstream `Options.Timeout` default is "disabled" if zero, so set it (suggest 5s, a named constant).

## 6. Lifecycle

- `New(c)` builds but does not listen. `func (p *Proxy) Start(ctx context.Context) (err error)` (`proxy/proxy.go:531`) validates listen addresses (`validateListenAddrs`, `proxy/config.go:346`: error if none), binds ALL listeners synchronously (`startListeners`, `proxy/server.go:21`), starts serving in goroutines (`serveListeners` with `context.WithoutCancel(ctx)`, `:560-562`) and **returns immediately** - it does not block. Calling it twice returns "server has been already started" (`:537-539`).
- `func (p *Proxy) Shutdown(ctx context.Context) (err error)` (`proxy/proxy.go:600`): closes listeners AND the upstream configs (`UpstreamConfig`, `PrivateRDNSUpstreamConfig`, `Fallbacks`; `:615-623`). A `Proxy` cannot be restarted after Shutdown in a clean way (comment at `proxy/proxy.go:119-123`); create a new one.
- So `Backend.Run(ctx)` = build proxy (or in the factory), `p.Start(ctx)`, `<-ctx.Done()`, `p.Shutdown(shutdownCtx)` with a fresh timeout context, return the shutdown error; a `Start` error is returned at once.
- **Bind failure.** A taken address makes `Start` return an error: `fmt.Errorf("listening on udp addr %s: %w", a, sErr)` / `"listening to udp socket: %w"` (`proxy/server.go`, `proxy/serverudp.go:24-27,44-51`) and `Start` closes listeners it opened before (`proxy/proxy.go:547-552`). With `BindRetryConfig == nil` there is no retry. `NOT RUN`: confirm with a test that binds a port first and expects `Start` to fail and leave nothing listening.
- Bound address after `Start`: `p.Addr(proxy.ProtoUDP)` (`proxy/proxy.go:733`, first listener) and `p.Addrs(proto)` (`:699`) return `net.Addr`; with `127.0.0.1:0` this tells a test the random port. Both panic on an invalid proto string.

## 7. Notes for the adapter (P1-08) that follow from the above

1. Wrap the whole `ServeDNS` body in a `defer recover()` that logs, fails open (`p.Resolve` or a SERVFAIL if even that panics) and never leaves `dctx.Res == nil`.
2. Convert `dctx.Addr.Addr().Unmap()` to `engine.Query.Client`; `engine.Query.Proto = string(dctx.Proto)`.
3. Never hand the engine anything it can mutate: `dctx.Req` is passed read-only; the engine copies.
4. A `Block` decision: `dctx.Res = dec.Resp`; return nil. No `Resolve`, no upstream traffic, no cache write.
5. Set `RefuseAny: true`? It answers ANY with NOTIMP (anti-amplification, `proxy/proxy.go:974-978`); harmless for browsers. Decide in P1-08 (recommended: true).
6. Keep `DNSSECEnabled`, `EnableEDNSClientSubnet`, `CacheOptimistic`, `UseDNS64` at false (ECS is off for privacy; D-list says ECS out of scope).
7. Test the adapter with a fake upstream (miekg/dns on `127.0.0.1:0`, plain UDP address as the upstream string) so no internet is needed, and a separate throwaway check that `https://1.1.1.1/dns-query` parses without bootstrap.
8. The module's own CLI wires all of this in `internal/cmd/proxy.go` (internal package, cannot be imported) - use it only as a reading reference if a detail is unclear.

## 8. Still UNVERIFIED (state plainly in reports)

- Run-time behavior of everything marked `NOT RUN` above.
- Real DoH latency, upstream failover timing, and behavior when both upstreams are unreachable (needs the live test).
- Whether `ServeDNS` is invoked for TCP queries on the same goroutine model (read `servertcp.go:128` `handleTCPConnection` only for the call site, not in depth).
