# Phase 3 - Phone test on home Wi-Fi: task checklist

Derived from `docs/roadmap.md` Phase 3 and `docs/phase-2-report.md` ("Serah terima ke Fase 3"). Tick a box only when the task is verified and recorded.

**Do not start until Master says "lanjut".** Phase 2 is closed; `STATE.md` says so.

**IMPORTANT BOUNDARY:** Never stop ICS (`SharedAccess`), never edit Windows Defender Firewall rules, and never bind `192.168.1.10:53` without Master's explicit approval at Decision Gate G2.

Phase 3 is done when ALL of these hold on the real RedMagic 8S Pro phone:
- **S1:** Ad domains on the lists answer `0.0.0.0`; `adblock.turtlecute.org` and `canyoublockit.com` measured before and after xdns (initial target >= 90% of test hosts blocked, adjusted after baseline).
- **S2:** Play Store / Shopee redirect trigger domain identified via `xdns log -around`, blocked by rule or list, and verified across 10 retries without that redirect (or Gate G2b triggered honestly if the redirect script is served from the content site's own domain).
- **S3:** SafeSearch not forced on the phone (`www.google.com` does not chain to `forcesafesearch.google.com`).
- Results, latency numbers, and findings recorded in `docs/test-report.md`.

## Decision Gate G2 (ask Master at the start of Phase 3)

Before touching UDP 53 or firewall settings:
1. Check non-destructively whether `192.168.1.10:53` UDP can be bound while ICS (`SharedAccess`) holds `0.0.0.0:53` UDP.
2. Ask Master:
   - Does Master use Mobile Hotspot, Hyper-V, or WSL on this PC (which depend on `SharedAccess`/ICS)?
   - Can Master access the Wi-Fi router settings (for DHCP IP reservation of `192.168.1.10`)?
   - Which browser is used on the RedMagic 8S Pro phone?

## Tasks

- [ ] **P3-01 Gate G2 & UDP 53 resolution.** Test whether `192.168.1.10:53` UDP bind succeeds; ask Master the Gate G2 questions; with Master's explicit yes, resolve the UDP 53 conflict, set `listen: ["192.168.1.10:53"]` in local `xdns.yaml` (gitignored), and add the Windows Defender Firewall rule (inbound UDP/TCP 53, Private profile only).
- [ ] **P3-02 Router IP reservation.** Guide Master to reserve `192.168.1.10` for this PC in the router's DHCP settings so the LAN IP never drifts.
- [ ] **P3-03 Phone Wi-Fi & browser DNS setup.** Guide Master on the RedMagic 8S Pro: Wi-Fi -> Static IP -> DNS 1 = `192.168.1.10`, DNS 2 = empty; Android Private DNS = Off; check Chrome Secure DNS and Firefox DoH settings (plus verify `use-application-dns.net` NXDOMAIN and `_dns.resolver.arpa` NODATA in the query log).
- [ ] **P3-04 S1 baseline & after-xdns measurement.** Record scores on `adblock.turtlecute.org` and `canyoublockit.com` on the phone before enabling xdns and after enabling xdns.
- [ ] **P3-05 Verify phone queries hit the log (R4).** Confirm phone queries (`192.168.1.x`) appear in `xdns log` and are not bypassing xdns via IPv6 router DNS or browser DoH; check S3 SafeSearch from the phone.
- [ ] **P3-06 S2 Play Store / Shopee redirect hunting.** Reproduce the redirect on the phone -> note timestamp -> run `xdns log -around <HH:MM> -window 30s` -> identify trigger domain -> `xdns rule add block <domain>` -> test 10 retries. (If the redirect is same-origin script, trigger Gate G2b honestly.)
- [ ] **P3-07 Phase gates & test report.** Run `/gates`, write `docs/test-report.md` with S1–S3 measurements and latency numbers, run `/review-phase` if code changed, and `/close-phase`.

## Review / close-out

- [ ] `docs/test-report.md` written, `docs/architecture.md` §18 items 3, 6, 8 updated, `docs/phase-3-report.md` written, `state/STATE.md` set to "Phase 3 done, waiting for lanjut", `state/PHASE-4-TASKS.md` created.
