---
trigger: always_on
description: Threat model and input-handling rules for hecate - ACL, untrusted lists and configs, paths, resource limits, logs, and the pre-commit security checklist.
---

# Security and input handling

Main threats (`docs/architecture.md` section 13): (1) an open resolver abused for DDoS amplification once on a VPS; (2) hostile content in blocklists, rules or config; (3) browsing history leaking through the query log; (4) a manipulated upstream or list download.

## Rules that are never optional

- **Client ACL fails closed.** `allowed_clients` default is loopback + private ranges; anything else gets REFUSED. An empty list is a config error. Check IPv4-mapped IPv6 (`::ffff:10.0.0.1` must be treated as `10.0.0.1`), CIDR boundaries (first and last address of a range), port stripping, zone IDs.
- **Bind narrowly.** Listen on a specific IP, not `0.0.0.0`, unless there is a reason. Development uses `127.0.0.1:5300`.
- **Lists and rules are untrusted input.** Never executed, never trusted. Per line: strip comments, lowercase, validate syntax (labels 1-63 chars, total <= 253, only `a-z 0-9 - _`), reject IP addresses and single-label entries (a bad line like `com` must not block a whole TLD), count and report rejected lines, never stop for one bad line. Cap line length and file size so a huge or binary file cannot exhaust memory.
- **List downloads:** `https://` only, 30 s timeout, 64 MB cap per list, redirects limited, write to a temp file then atomic `rename`, keep metadata in `<name>.meta.json`. On any failure keep the old copy.
- **Paths:** list source names become file names, so they are restricted to `[A-Za-z0-9_-]{1,64}`, start alphanumeric, are not Windows device names (CON, PRN, AUX, NUL, COM1-9, LPT1-9) and are unique case-insensitively (already enforced in `config.Validate`). Never build a path from DNS data or list content. Watch for `..`, drive letters, trailing dots/spaces on Windows.
- **Resource limits everywhere:** bounded log buffer (drop + counter), bounded cache size, bounded goroutines, bounded message sizes. No unbounded growth from attacker-controlled input (query names, list size, number of clients).
- **Query log privacy:** the log is a history of visited domains. It stays on this PC, has retention (default 7 days) and can be disabled (`log.enabled: false`). Sanitize logged strings against log injection (newlines, control characters). Do not log it to a shared location.
- **Errors:** server logs may be detailed; DNS responses to clients never carry internal detail.
- **Secrets:** no secrets in the repo, example config or tests. TLS private keys (Phase 6) live in `data/tls/`, mode restricted, gitignored (`*.pem`, `*.key`, `/data/`).
- **Supply chain:** pinned module versions, `go mod verify`, `go mod tidy -diff`, `govulncheck` with 0 affected vulnerabilities at every phase end (Go toolchain 1.26.9 and `golang.org/x/net` v0.60.0 were required for exactly this).
- **Amplification:** never recurse for unauthorized clients; rate limiting per client IP is mandatory before any public deployment (Phase 6). Public port 53 stays closed; Phase 6 uses DoT on 853 with a secret SNI hostname.

## Pre-commit checklist

- [ ] No hardcoded secrets or personal data
- [ ] All external input validated and size-bounded
- [ ] ACL, parsers and file paths covered by tests with hostile inputs
- [ ] No panic path on malformed DNS messages or list/rule lines
- [ ] Error messages to clients leak nothing internal
- [ ] No new network exposure beyond what the task states

## Security review focus at phase end (give this to the reviewer)

ACL bypasses; malformed DNS messages; list and rule parsing (huge lines, huge files, odd Unicode, IDN, trailing dots, case); download limits and redirects; path traversal and Windows device names; file permissions; log injection; unbounded memory/goroutine growth; open-resolver or amplification risk; secrets in logs/config.
