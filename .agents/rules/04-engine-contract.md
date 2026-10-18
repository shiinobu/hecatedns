---
trigger: always_on
description: The engine and backend contracts (Before/After/Failed), the per-query flow, block-answer modes and the fail-open/fail-closed policy. Source - docs/architecture.md sections 5, 6, 14.
---

# Engine contract and query flow

Authoritative text: `docs/architecture.md` sections 5-6 and 14. This is the working summary. The Go shapes below are the documented contract; the implementer may refine names but must keep the semantics.

```go
package engine

type Query struct {
    Req    *dns.Msg    // miekg/dns v1 message
    Client netip.Addr
    Proto  string      // "udp" | "tcp" | "tls" | "https"
}
type Verdict uint8
const ( Pass Verdict = iota; Block )
type Decision struct {
    Verdict Verdict
    Resp    *dns.Msg // set when Block
    Reason  string   // "rule" | "list" | "cname" | "special" | "acl" | "allow"
    Source  string   // list name or matching rule line
    Via     string   // matching CNAME target (Reason == "cname")
}
type Meta struct { Upstream string; Latency time.Duration; Cached bool }

func (e *Engine) Before(ctx context.Context, q Query) Decision
func (e *Engine) After(ctx context.Context, q Query, resp *dns.Msg, m Meta) Decision
func (e *Engine) Failed(ctx context.Context, q Query, err error)
```

```go
package backend
type Backend interface { Name() string; Run(ctx context.Context) error } // Run blocks until ctx ends or a fatal error
type Deps struct { Cfg config.Config; Engine *engine.Engine; Log *slog.Logger }
type Factory func(Deps) (Backend, error)
```

## Guarantees the engine must give

- Safe for many goroutines at once. No network I/O. Never mutates `q.Req` or the `resp` it receives (copy when needed).
- Writes exactly one query-log record per query: in `Before` when blocked, in `After` or `Failed` when forwarded.
- If no list is loaded yet, `Before` passes the query (fail-open) and logs one warning once.
- State that changes at runtime (lists, rules) is an immutable snapshot swapped with `atomic.Pointer`. Two separate snapshots: lists and custom rules, so a rule change does not rebuild hundreds of thousands of entries.

## `Before` order

1. **ACL.** Client outside `allowed_clients` -> `Block` with REFUSED (reason `acl`), without matching lists. `allowed_clients` is never empty (config validation), so an empty list never means "allow all". Normalize IPv4-mapped IPv6 before matching.
2. **Validate.** Exactly one question and opcode QUERY, else `Pass` (the backend answers FORMERR/NOTIMP).
3. **Normalize.** Lowercase, strip the trailing dot. IDN names are already punycode on the wire.
4. **Special names** (switchable in config): `use-application-dns.net` -> `Block` NXDOMAIN; `_dns.resolver.arpa` -> NODATA. (Phase 2.)
5. **Allow?** Custom `allow` rule, then `allow`-kind lists -> `Pass` (reason `allow`).
6. **Block?** Custom `block` rule, then `block`-kind lists -> `Block` with `Source` set.
7. Otherwise `Pass`.

Priority overall: custom allow > custom block > list allow > list block.

## `After` order (after the backend resolved a passed query)

1. No answer or rcode != NOERROR: log and return the answer unchanged.
2. **CNAME chain** (Phase 2): for each CNAME in the answer section, normalize the target. If the target is allow-listed, continue; if it matches a block entry, return `Block` for the ORIGINAL question (reason `cname`, `Via` = target).
3. Otherwise log as passed, with upstream, rcode and latency.

`Failed` logs an upstream failure (all upstreams failed -> the backend returns SERVFAIL).

## Blocked answers (`response.go`)

Always copy ID and question section, set RA, keep RD. TTL and synthetic SOA minimum both equal `block.ttl` so clients cache negatives briefly (new rules take effect fast).

| Mode | Behavior |
|---|---|
| `null_ip` (default) | A -> `0.0.0.0`, AAAA -> `::`; every other type including HTTPS/SVCB (65) -> NOERROR empty + synthetic SOA |
| `nxdomain` | NXDOMAIN + synthetic SOA |
| `nodata` | NOERROR empty + synthetic SOA |
| `refused` | REFUSED |

## Backend adapter facts (dnsproxy, Phase 1)

The dnsproxy handler (`proxy.Config.RequestHandler`) calls `Before`. On `Block` it fills the response into its context. On `Pass` dnsproxy does its normal resolve (upstream + cache), then `After` may replace the answer. dnsproxy has NO client ACL, so the engine enforces `allowed_clients` for both backends. The exact handler signature and cache behavior for replaced answers are NOT verified yet: verify from source before coding (`workflows/spike-dnsproxy.md`, results in `notes/dnsproxy-api-facts.md`). The native backend caches the upstream answer BEFORE `After` so the CNAME check runs on every cache hit; match that in dnsproxy if its cache allows it.

## Failure policy (`docs/architecture.md` section 14)

- Engine not ready or internal error -> forward the query (fail-open) and log. Exception: ACL violation stays REFUSED (fail-closed).
- Upstream failed -> try the next upstream; all failed -> SERVFAIL + `Failed`.
- Lists: download failure keeps the old copy; corrupt cache file is ignored with a logged error. Rules: a bad line is skipped with its line number; a missing file means no rules, not a fatal error.
- Log: a full buffer or full disk must never block a query (drop and count).
- Shutdown on ctx cancel (Ctrl+C): close listeners cleanly and flush the log.
