---
name: hecate-block-semantics
description: Domain knowledge for hecate filtering decisions - suffix matching, name normalization, list and rule parsing, rule priority, blocked-answer shapes per mode, CNAME and HTTPS/SVCB handling, special names, SafeSearch rule. Use when implementing or reviewing the matcher, rules, lists, response builder, CNAME check or special names.
---

# HecateDNS blocking semantics

Source of truth: `docs/architecture.md` sections 6, 7, 8, 11 and decisions D3-D7. This is the condensed rule set.

## Name handling

- Normalize before matching: lowercase, remove the trailing dot. IDN names arrive as punycode on the wire; do not re-encode.
- **Suffix match:** an entry `example.com` blocks `example.com` and every subdomain (`a.b.example.com`). It does NOT block `notexample.com` (match on label boundaries) and does not block the parent `com`.
- Reject entries that are IP addresses or a single label (`com`), so one broken list line cannot block a whole TLD. Labels are 1-63 characters, total <= 253, characters `a-z 0-9 - _` only. Strip a leading `*.` from list entries.
- The matcher is a map of domains with a walk up the labels (check `a.b.example.com`, `b.example.com`, `example.com`; stop before the bare TLD). Snapshots are immutable and swapped atomically; lists and custom rules are separate snapshots.

## Priority (highest first)

custom `allow` > custom `block` > list `allow` > list `block`. A matched `allow` returns `Pass` with reason `allow` and skips blocking, including for CNAME targets.

## Formats

- Rules file `data/rules.txt`: lines `block <domain>` / `allow <domain>`; `#` starts a comment; a bad line is skipped and reported with its line number; a missing file means no rules. Same suffix semantics as lists. Reload when the file's mtime changes (poll every `rules.poll_interval`, default 2 s); `hecate rule add` writes atomically.
- Lists, format `domains`: one domain per line. Format `hosts`: `0.0.0.0 domain` (also `127.0.0.1`). Comments start with `#`, `!` or `;` and may trail a line. Count and report rejected lines; a bad line never aborts the load. The three default lists contain only plain lowercase domains (checked 2026-10-18: no `*.`, no spaces). Full adblock syntax (`||domain^$opts`) is NOT supported in v1.

## Blocked answers (copy ID + question, set RA, keep RD; TTL = `block.ttl`, default 10 s)

| Mode | A | AAAA | Other types (TXT, HTTPS 65, ...) |
|---|---|---|---|
| `null_ip` (default) | `0.0.0.0` | `::` | NOERROR, empty answer, synthetic SOA |
| `nxdomain` | NXDOMAIN + SOA | same | same |
| `nodata` | NOERROR empty + SOA | same | same |
| `refused` | REFUSED | same | same |

The synthetic SOA uses the same TTL and minimum as `block.ttl` so negative caching in clients is short. Special case: ACL violations are REFUSED regardless of mode.

## CNAME check (Phase 2, in `After`)

For every CNAME in the answer section: normalize its target; if allow-listed, continue; if it matches a block entry, return `Block` for the ORIGINAL question with reason `cname` and `Via` = the target. This catches CNAME-cloaked trackers. It must also run on cache hits, so the cache must hold the upstream answer from BEFORE `After`.

## HTTPS/SVCB (type 65) and special names

- HTTPS/SVCB queries for a blocked name get NODATA (decision D7; the reasoning is in `docs/research.md` sections 4.4-4.5). With `null_ip` this follows from "other types -> NODATA".
- `_dns.resolver.arpa` -> NODATA (blocks DDR auto-upgrade to the router's/ISP's encrypted DNS) when `special_names.ddr` is on.
- `use-application-dns.net` -> NXDOMAIN (Firefox DoH canary) when `special_names.firefox_canary` is on. Real behavior in Firefox/Chrome on the phone is still UNVERIFIED (architecture section 18 item 3).

## SafeSearch rule (success criterion S3)

hecate never rewrites `www.google.com`. The default upstreams are NOT the "family/filtering" variants, and the upstream is encrypted so the ISP/router cannot hijack answers. `hecate check` (self-check, Phase 2) resolves `www.google.com` through the configured upstream and warns if the chain contains `forcesafesearch.google.com`. Limit: if SafeSearch is locked by the Google account or a network proxy, DNS cannot undo it.

## What to be careful about

- Never block by substring or regex: only by label-boundary suffix.
- Never let a blocked answer carry upstream data. Never mutate the incoming request or the upstream response; copy first.
- A cached negative answer in the phone/browser means a rule change can take up to the block TTL (10 s) to be visible; do not "fix" this by raising the TTL.
- Allow-list entries are for fixing false positives: `hecate rule add allow <domain>` must work in <= 10 s with no restart (S5).
