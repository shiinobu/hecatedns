# Open questions, blockers and unverified items

Add new questions at the bottom with a number. When one is answered, move it to the "Answered" section with the answer and the date.

## For the implementing agent to resolve (technical)

**Q1. Registry import cycle (found 2026-10-18 while writing the pack).** `docs/architecture.md` section 5.2 puts `Deps`, `Factory` and `var registry = map[string]Factory{"dnsproxy": dnsproxybe.New, "native": native.New}` in package `backend`. But `dnsproxybe.New` takes a `backend.Deps`, so `dnsproxybe` imports `backend` while `backend` imports `dnsproxybe`: a Go import cycle. Likely fix: keep `Backend`, `Deps`, `Factory` in `backend`, and build the name -> factory map in `cmd/xdns` (or a tiny package such as `internal/backend/registry` that imports all backends and `backend`). Decide in P1-08, record the ruling in `state/WORKLOG.md`, and update `docs/architecture.md` sections 4 and 5.2 in the docs pass.

**Q2. dnsproxy integration points (docs section 18, items 1-2).** Exact signature of `proxy.Config.RequestHandler` (type `Handler`); how a handler supplies a response and stops dnsproxy from resolving; where `After` can hook in (the docs say a `BeforeRequestHandler` hook was not found - check `middleware.go`, `requesthandler.go`, `proxy.go`, `dnscontext.go`); whether a DoH upstream given as an IP (`https://1.1.1.1/dns-query`) needs a bootstrap; how the dnsproxy cache treats answers that `After` replaced. Answers go to `notes/dnsproxy-api-facts.md`. If `After` cannot replace a cached answer cleanly, record the consequence for the CNAME check (Phase 2) and for the design.

**Q3. Run-time loop detection.** An upstream/bootstrap equal to xdns's own listen address is accepted by config validation. Decide whether `run` should refuse it (Phase 1 task P1-10).

## For Master (do not ask early; ask at the gate)

- **G2, start of Phase 3:** Is Mobile Hotspot, Hyper-V or WSL used on this PC (ICS holds UDP 53 for them)? Can Master open the router settings (DHCP address reservation)? Which browser is used on the phone?
- **G3, start of Phase 6:** VPS provider and cost approval (check current prices first), subdomain name.
- Whether to push the repo anywhere as a backup: needs Master's explicit yes.

## Unverified (never state as fact)

- `docs/architecture.md` section 18 items 3-8: canary/DDR behavior in Firefox and Chrome on the phone, RedMagic reaction to `0.0.0.0` vs NODATA vs NXDOMAIN, whether EDE (RFC 8914) helps, real memory/latency with about 249k entries (targets: < 150 MB, p99 < 50 microseconds), the ICS / UDP 53 resolution.
- `docs/research.md` section 8 (research gaps and unverified claims).
- Which domain triggers the Play Store redirect on Master's phone.
- Real quota cost of Opus vs Sonnet in Claude (measured with `/usage`; Claude-side only).
- Whether Antigravity loads `.agents/rules` etc. exactly as described in the pack README (third-party sources only).
- One vulnerability in a required module that our code does not call: re-run `govulncheck` after Phase 1 uses dnsproxy for real.

## Known stale documentation (fix in the docs pass, not before)

- `docs/architecture.md` line ~391 (section 12) and ~444 (section 15): "no gcc / `go test -race` unavailable" is no longer true.
- `README.md` status line says Phase 0 only.

## Answered

- 2026-10-18: Commit `.agents/`? Master said yes, and also asked for `.claude/` ("commit sekalian .claude"). Done in two commits; `.claude/settings.local.json` stays gitignored (local setting, turns the ECC guard off on this PC only). Master may ask to track it too.
- Still open from Master: a root `AGENTS.md` pointer to `.agents/` (offered, not created).
