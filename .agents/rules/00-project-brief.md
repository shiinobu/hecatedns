---
trigger: always_on
description: What hecate is, why it exists, its architecture, repository map and success criteria.
---

# HecateDNS - project brief

hecate is Master's personal, non-commercial DNS server written in Go. It blocks ads in the browser of Master's Android phone (RedMagic 8S Pro): pop-up, pop-under, auto-redirect and click-redirect, including redirects that open the Shopee app or the Play Store. It must not force Google SafeSearch and must not need a VPN app. The phone uses it as its DNS server: home Wi-Fi first (Phase 3), later a small VPS with DNS-over-TLS plus Android Private DNS (Phase 6, budget up to about Rp50,000/month). An Android app that is only an on/off switch is Plan B.

## The honest limit (say it, never oversell)

DNS cannot block 100% of browser ads. Out of reach: ads served from the same domain as the content, redirect scripts served from the site's own domain, overlay click hijacks, leftover empty boxes. Documented complement: Firefox for Android + uBlock Origin (Chrome for Android has no extensions). Never quote an effectiveness percentage; the only data found is vendor data (`docs/research.md` section 2).

## Architecture on one screen

```
 Client (phone / PC)  --DNS (UDP/TCP 53; later DoT 853)-->
┌───────────────────────────────────────────────────────┐
│ BACKEND   backend: dnsproxy | native                  │
│   listener, cache, encrypted upstreams (DoH/DoT)      │
└───────┬───────────────────────────────▲───────────────┘
   Before(q)                        After(q, answer)      (+ Failed on upstream error)
        ▼                                │
┌───────────────────────────────────────────────────────┐
│ ENGINE (one, shared, no network)                      │
│   client ACL, custom rules, blocklists, CNAME check,  │
│   blocked-answer builder, special names, query log    │
└───────────────────────────────────────────────────────┘
        ▲                      ▲
  data/rules.txt        data/lists/*.txt (downloaded daily)
```

The engine knows nothing about networks, TLS or caches. Backends know nothing about lists or rules. They meet only through `Before`, `After`, `Failed`. Two interchangeable backends, chosen by `backend:` in the YAML config: `dnsproxy` (AdGuard dnsproxy v0.86.0, Apache-2.0; built in Phase 1) and `native` (own code on miekg/dns v1.1.73; Phase 4). Master wants to try both.

Default blocklists (about 249k entries): HaGeZi Multi PRO, HaGeZi Pop-Up Ads, ABPindo `domain.txt`. Blocked answers are a null IP with a 10 s TTL.

## Repository map

```
cmd/hecate/               main.go, check.go, run.go, log.go, rule.go, lists.go    [DONE, 92% cover]
backend/lib/config/          strict YAML loader, defaults, validation, Warnings()    [DONE, 96% cover]
backend/engine/          filter engine (ACL, matcher, rules, CNAME, special)     [DONE, 97% cover]
backend/service/lists/           list download, parser, cache, scheduler                 [DONE, 87% cover]
backend/service/qlog/            JSONL query log                                         [DONE, 89% cover]
backend/service/selfcheck/       self-test (SafeSearch, upstream, sample domains)        [DONE, 93% cover]
backend/core/         Backend interface, registry                             [DONE, 100% cover]
  dnsproxybe/             AdGuard dnsproxy v0.86.0 adapter                        [DONE, 95% cover]
  conformance/            shared backend conformance suite                        [DONE, 81% cover]
  native/                 native miekg/dns backend (Phase 4)                      [doc.go only]
configs/hecate.example.yaml reference config (identical to docs/architecture.md section 10)
docs/                     architecture.md roadmap.md research.md phase-{0,1,2}-report.md (Indonesian)
.agents/                  this pack          .claude/  Claude Code only (tracked; settings.local.json ignored)
```

Planned engine files (`docs/architecture.md` section 4): `engine.go acl.go matcher.go rules.go response.go cname.go special.go`.

## Success criteria (docs/architecture.md section 1)

| Code | Criterion | Measured in |
|---|---|---|
| S1 | Ad domains on the lists answer `0.0.0.0`; adblock.turtlecute.org and canyoublockit.com before/after; first target 90% of test hosts blocked | Phase 3 |
| S2 | Play Store redirect trigger found via the query log and stopped (10 retries without that redirect) | Phase 3 |
| S3 | SafeSearch not forced: `www.google.com` never chains to `forcesafesearch.google.com` | Phase 2 and 3 |
| S4 | 7 days running without crash or manual restart, stable memory, cache-miss p95 < 150 ms | Phase 6, on the VPS (moved from Phase 5: the home PC is switched off at night) |
| S5 | `hecate rule add allow <domain>` takes effect within 10 s without restart | Phase 1 |

Not goals of version 1: web UI, HTTPS content inspection, APK, DNS-over-QUIC, DNSSEC validation, per-client profiles, parental control, ECS.

## Where the truth lives

`docs/architecture.md` (design, decisions D1-D16, risks R1-R10, items to verify in section 18), `docs/roadmap.md` (phases, "done when" criteria, decision gates), `docs/research.md` (sources), `docs/phase-0-report.md` (Phase 0 evidence and 13 rulings). They are in Indonesian. If this pack and the docs disagree about design, the docs win; for live status, `state/STATE.md` wins.
