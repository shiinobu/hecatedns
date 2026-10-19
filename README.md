<p align="center">
  <img src="docs/assets/banner.jpg" alt="HecateDNS: secure and private DNS" width="100%">
</p>

# HecateDNS

A personal DNS server that blocks browser ads (pop-ups, redirects, click-redirects) without a VPN app and without forcing Google SafeSearch. Written in Go. Formerly called xdns.

It resolves queries over encrypted upstreams (DoH), drops ad and tracker domains using the HaGeZi and ABPindo lists (about 250,000 domains), follows CNAME chains to catch cloaked trackers, and lets you add your own allow and block rules that take effect within seconds. A phone uses it as its DNS server on the home Wi-Fi, and later anywhere through Android Private DNS (DoT, planned).

**Status:** phases 0 to 5 are done: both backends pass the same conformance suite, and a real-phone test blocked 95 to 97% of a public ad-test page. Phase 6 (VPS + DNS-over-TLS) is next. See [docs/roadmap.md](docs/roadmap.md).

**Honest limits:** DNS cannot block everything. Ads served from the site's own domain, redirect scripts hosted on the site, and the empty boxes left behind are out of reach. Firefox with uBlock Origin is the recommended complement ([docs/research.md](docs/research.md)).

## Two backends, one engine

One shared filter engine runs under two interchangeable servers, chosen by `backend:` in the config:

- `dnsproxy`: a thin adapter around [AdGuard dnsproxy](https://github.com/AdguardTeam/dnsproxy).
- `native`: own server, cache, rate limiter and upstream pool on [`miekg/dns`](https://github.com/miekg/dns).

## Quick start

```
go build -o bin/hecate ./cmd/hecate
cp configs/hecate.example.yaml hecate.yaml
./bin/hecate check -config hecate.yaml          # validate the config, run the self-check
./bin/hecate lists update -config hecate.yaml   # download the block lists
./bin/hecate run -config hecate.yaml            # serve on 127.0.0.1:5300
```

On Windows use `bin\hecate.exe` and `scripts\windows\dnsquery.ps1 example.com -Port 5300` to test a query (Windows `nslookup` ignores `-port`).

## Commands

```
hecate run   -config hecate.yaml
hecate check -config hecate.yaml
hecate log   -since 10m -blocked -config hecate.yaml
hecate rule add block example.com -config hecate.yaml      # blocks the domain and its subdomains
hecate rule add allow ok.example.com -config hecate.yaml   # allow beats block
hecate rule list | remove example.com -config hecate.yaml
hecate lists status | update -config hecate.yaml
```

Keys left out of the config use defaults; unknown or duplicate keys are rejected with a clear error. The server re-reads the rules file and the list cache by itself, so rule and list changes need no restart.

## Layout

```
cmd/hecate/          entry point
backend/controller/  CLI commands
backend/core/        backend contract, dnsproxy adapter, native server
backend/engine/      the filter (ACL, rules, matcher, CNAME check)
backend/service/     lists, query log, self-check
backend/lib/         config, operational log
backend/test/        conformance suite and source-rule gates
configs/             example configuration
scripts/windows/     Task Scheduler autostart, DNS query helper
docs/                architecture, roadmap, research, testing, operations, assets
```

## Documentation

- [docs/architecture.md](docs/architecture.md): design, decisions, security
- [docs/roadmap.md](docs/roadmap.md): phases and what is next
- [docs/research.md](docs/research.md): the research behind the design, with sources
- [docs/testing.md](docs/testing.md): automated tests and real-phone results
- [docs/operations.md](docs/operations.md): running it, autostart, deployment

## Development

```
gofmt -l .
go vet ./...
go test ./... -cover
CGO_ENABLED=1 go test -race ./...
```

Source files carry no comments (enforced by a test in `backend/test/nocomment`); the reasoning lives in `docs/`.

## License

[MIT](LICENSE). The block lists it downloads (HaGeZi, ABPindo) have their own licenses and are not part of this repository.
