# Research: blocking browser ads, and what it means for HecateDNS

Written 2026-10-18 from about 45 sources (some only from search summaries, marked). Overall confidence: **medium**.

Scope: ads in the **browser** on an Android phone, focused on pop-ups, auto-redirects, click-redirects and redirects to the Play Store or a shopping app. Main question: *can DNS block browser ads completely, and what methods exist?*

Labels: **[Fact]** has a source; **[Vendor]** a seller's claim about its own product; **[Inference]** my own conclusion; **[Unverified]** not checked.

---

## Executive summary

1. **No single method blocks 100% of browser ads.** The most complete method is a content blocker inside the browser (uBlock Origin on Firefox for Android). DNS is simpler and works for every app, but it only sees domain names.
2. **DNS vendors admit the limit.** AdGuard, which sells both a DNS blocker and an extension, writes that DNS "will not replace browser-based blockers" and cannot handle same-domain ads ([AdGuard DNS blog](https://adguard-dns.io/en/blog/dns-content-blocking-at-scale.html)).
3. **Correction:** an earlier "85-95%" figure had **no source** and should be ignored. The only quantitative data found (vendor, US news sites) shows DNS saving about **74%** of the bandwidth that a browser extension saves, leaving more tracker domains (489 vs 364 of a baseline 829). No independent study compares them, and none covers pop-ups or redirects specifically.
4. **For pop-ups and redirects, DNS works only when the trigger is a third-party domain already on a list.** A Play Store redirect usually passes through click-tracking domains, and blocking one link stops the chain ([Malwarebytes](https://www.malwarebytes.com/blog/news/2014/02/ad2store-redirections-the-latest-annoyance-for-mobile-users), 2014). A script served from the site's own domain, or a click overlay calling `window.open` on a real click, is out of reach for DNS.
5. **Findings that shaped the design:** query log and custom rules must be in the first version; check the CNAME chain; handle HTTPS/SVCB (type 65) answers on blocked domains; a dedicated Pop-Up Ads list (HaGeZi) and an Indonesian list (ABPindo) exist; and a reusable Go library exists (AdGuard `dnsproxy`, Apache-2.0).

---

## 1. Map of blocking methods

| Method | How it works | Can | Cannot | Root? |
|---|---|---|---|---|
| **DNS sinkhole** (Pi-hole, AdGuard Home, Blocky, NextDNS, hecate) | Blocked domains answer `0.0.0.0`/NXDOMAIN | All third-party ad/tracker domains, every app, nothing to install on the phone (via Private DNS) | Same-domain ads, URL paths, hiding ad boxes, pop-up scripts from the site's own domain | No |
| **In-browser content blocker** (uBlock Origin, Brave Shields) | URL/context rules, cosmetic filtering, scriptlets | Nearly all of the above plus `popup`/`popunder` options and element hiding | Only inside that browser | No |
| **Browser built-ins** (Chrome pop-ups and redirects, notification blocking) | Block windows opened without a gesture | Pop-ups without a click | Pop-unders riding on a real click | No |
| **Local filter via VpnService** (RethinkDNS, personalDNSfilter, NetGuard, AdGuard non-root) | An app intercepts DNS (optionally HTTPS) | Like DNS, system-wide, without a server | Like DNS; HTTPS only if its CA is trusted | No |
| **HTTPS inspection** (MITM with a private CA) | Decrypt, then filter | Same-domain ads in apps that trust the CA | Apps that trust only the system store | Root for most apps |
| **Root** (hosts file, CA in the system store) | Change the system | Equivalent to DNS; opens MITM for more apps | Still not "complete" | Yes |

**[Unverified]** here: a home-network MITM proxy (mitmproxy, Privoxy, Squid; likely the same CA limits as section 5) and modified clients such as ReVanced (outside "ads in the browser").

---

## 2. DNS versus an in-browser blocker

**Technical reasons** (uBlock Origin's author, "Why uBlock Origin works so much better than Pi-hole"): early request cancellation in the browser, full URL matching, context-aware rules (request type and party), redirects to neutral resources, cosmetic filtering, scriptlet injection, HTML filtering (Firefox only), pop-up handling (`$popup,third-party`) and tracking-parameter removal. DNS filtering is blunt in comparison, but its strength is covering every device on the network ([uBO wiki](https://github.com/gorhill/uBlock/wiki/About-%22Why-uBlock-Origin-works-so-much-better-than-Pi%E2%80%91hole-does%3F%22)). **[Fact]** uBO's syntax has `popup`, `popunder`, `redirect`, `removeparam`, `csp` and `##+js(...)` scriptlets ([syntax](https://github.com/gorhill/uBlock/wiki/Static-filter-syntax)). **[Fact]**

**The only numbers, from a vendor** (AdGuard report 2025-12-17; Chrome; 119 US news sites, each loaded three times; [report](https://adguard.com/en/blog/adguard-tracker-traffic-ad-report.html)). **[Vendor]**

| Measure | No blocking | AdGuard DNS | Browser extension |
|---|---|---|---|
| Requests per page | 299 | not reported | 145 (-51%) |
| Bandwidth saved (119 sites) | - | 198.52 MB | 267.42 MB |
| Average load time | over 11 s | 7.4 s | 6.2 s |
| Unique tracker domains | 829 | 489 (-41%) | 364 (-56%) |

My arithmetic: DNS bandwidth saving is about 74% of the extension's (198.52 / 267.42). **[Inference]** Caveats: one browser, US news sites (not Indonesian or streaming sites full of pop-ups), both products from the same vendor, nothing specific to pop-ups or redirects. The vendor concludes extensions "generally give better results" and recommends using both.

Other data: a 2017 mobile study found the best DNS-based list reduced third-party requests by only about 25% ([paper](https://eprints.cs.univie.ac.at/6446/1/block_me_if_you_can.pdf); old, from search summaries). One Pi-hole forum user found uBO added "very little or nothing" ([thread](https://discourse.pi-hole.net/t/poor-performance-compared-to-ublock/3603); anecdote). A Pi-hole advocacy site argues that ad-blocker tests misjudge DNS by checking HTML elements ([piholekiller.com](https://piholekiller.com/why-pihole-fails); partisan).

**Conclusion.** There is no independent percentage. DNS blocks most cross-domain requests, fewer than an in-browser blocker, and does not clean up empty ad boxes.

---

## 3. Pop-ups, pop-unders, click-redirects and store redirects

**Mechanics.** A pop-under calls `window.open()` and sends the window behind. Browsers allow `window.open` only from a user-interaction event, so pop-under scripts attach a click listener to the whole document and ride on a real click ([Wikipedia](https://en.wikipedia.org/wiki/Pop-under_ad)). Chrome's built-in blocker stops only pop-ups *without* a gesture ([no-popunder](https://github.com/nimitbhargava/no-popunder)). Transparent overlays and hover ads are part of the page HTML, so a pop-up blocker cannot recognise them. **[Fact]**

**Store redirect chains.** Malwarebytes (2014) describes ads that start a long redirect chain with no user interaction: a tracking domain sends an HTTP 302 to a store URL; on Android that looks like `market://details?id=...` with a `referrer` parameter. Blocking one domain in the chain prevents the redirect. The article is 12 years old; the pattern is probably still valid, the domains are not. **[Fact, dated]**

**What Chrome already limits.** Chrome Android does not launch apps through `intent://` without a user gesture ([docs](https://developer.chrome.com/docs/android/intents)); cross-origin iframes may navigate the top frame only with a gesture ([Chromium](https://groups.google.com/a/chromium.org/g/Blink-dev/c/Xi8-y4ySjA4/m/D80epeAMAAAJ)); "tab-under" navigations are cancelled under several simultaneous conditions. **[Fact]**

**What escapes both the browser and DNS.** **[Inference]** A script loaded in the main page can change that page's own `location` without a gesture; that is not an iframe navigation or a tab-under. Only removing the script stops it, for example by blocking the domain that loads it. A "tap anywhere, then jump to the store" pattern probably uses a click overlay or a document listener riding on a real gesture.

**Arms race.** Search summaries say ad networks sell anti-blocker features ("domain rotation", "anti-adblock"). I did not open the originals. **[Vendor, unverified]** Static lists will always lag, hence daily updates.

**Indonesia.** ABPindo is an EasyList addition for Indonesian and Malay ads (over-promotion, gambling, adult, fake update/download buttons); its DNS variants contain only ad-server and third-party lists to limit false positives ([repo](https://github.com/ABPindo/indonesianadblockrules)). **[Fact]** Indonesian media articles give only generic Chrome advice (site settings, notifications, "draw over other apps"). **[Fact, generic]**

---

## 4. DNS techniques that add strength

**4.1 Block lists.**

| List | Content | Note |
|---|---|---|
| HaGeZi Multi LIGHT / NORMAL / **PRO** / PRO++ / ULTIMATE | Increasing aggressiveness | PRO is the author's recommendation; ULTIMATE may break sites |
| HaGeZi **Pop-Up Ads** | Pop-up networks | Its own list (about 47,000 entries) |
| HaGeZi NRD/DGA | Newly registered / generated domains | Large, memory-hungry, false positives; for fast-changing domains |
| HaGeZi DoH/VPN/Tor/Proxy bypass | Domains used to bypass DNS | Needs outbound 53/853 blocked at the router to be effective |
| **ABPindo** `domain.txt` | Indonesian and Malaysian ads | Domain-only, DNS friendly |
| StevenBlack hosts | Classic hosts file | MIT, active |

HaGeZi builds daily (11:00 UTC) in several formats and recommends pairing DNS lists with an in-browser blocker ([repo](https://github.com/hagezi/dns-blocklists)). **[Fact]**

**4.2 CNAME uncloaking.** Trackers hide as first-party subdomains through CNAME records, and extensions on Chrome/Safari cannot see CNAMEs, but a DNS resolver can: NextDNS applies its lists to **every intermediate CNAME** ([NextDNS](https://medium.com/nextdns/nextdns-added-cname-uncloaking-support-becomes-the-first-cross-platform-solution-to-the-problem-e3f437f84342)); AdGuard keeps a list of over 6,000 cloaked trackers ([AdGuard](https://adguard.com/en/blog/cname-tracking.html)); a study found 0.58% of the Alexa top 300K used CNAME cloaking in January 2020 ([APNIC](https://blog.apnic.net/2020/08/04/characterizing-cname-cloaking-based-tracking/)). AdGuard warns that some trackers point at microsites or redirect targets, so blocking them can change what the user sees. uBO can uncloak only on Firefox. **[Fact]** **[Inference]** If that holds on Firefox for Android, hecate's CNAME check fills a real gap on the phone.

**4.3 Shape of a block answer.**

| Mode | Behaviour | Note |
|---|---|---|
| NULL IP (`0.0.0.0` / `::`) | Clients usually do not try to connect | Pi-hole's default and recommendation ([docs](https://docs.pi-hole.net/ftldns/blockingmode/)) |
| NXDOMAIN | Clients may retry more often | An informal Android test on AdGuard Home saw more repeated queries ([PR #743](https://github.com/AdguardTeam/AdGuardHome/pull/743)) |
| NODATA | Slightly better client acceptance than NXDOMAIN | A Chromium engineer proposed NODATA + SOA TTL + EDE (RFC 8914) as an honest, cacheable answer |
| REFUSED | Most "honest", but old devices may fall back to another DNS | |

Pi-hole uses a 2-second TTL so exceptions apply quickly and prefers NULL for IoT compatibility. One user reported NXDOMAIN making Android Chrome/Firefox "hang" (anecdote). **[Inference]** NULL IP is the sensible starting point on the target phone; measure on the device.

**4.4 HTTPS/SVCB (type 65) and DDR.** HTTPS/SVCB records can carry `ipv4hint`/`ipv6hint`, letting a client connect without A/AAAA answers; Pi-hole users report this defeats DNS blocking, and the discussed fix is NODATA for that type. `_dns.resolver.arpa` (DDR) can steer clients to the provider's DoH and bypass local blocking ([thread](https://discourse.pi-hole.net/t/are-svcb-queries-sent-through-pi-hole-of-blocking-concern/59983), [RFC 9460](https://www.iana.org/go/rfc9460)). **[Fact]** For hecate: blocked names get NODATA for type 65 too, and `_dns.resolver.arpa` answers NODATA unless DDR is implemented. **[Inference]**

**4.5 Ways around our DNS.** Chrome's automatic Secure DNS upgrades to DoH only if the current provider is on Chrome's built-in list and can fall back to plain DNS ([Chromium blog, 2020](https://blog.chromium.org/2020/09/a-safer-and-more-private-browsing.html); dated). Firefox for Android "Default Protection" enables DoH where partners exist and stands down when the network says so ([Mozilla](https://support.mozilla.org/fi/kb/configure-dns-over-https-protection-levels-firefox-android); summary only). **[Unverified]** the Firefox canary domain and bypass through router IPv6 DNS. To test on the phone: check Secure DNS in Chrome and DoH in Firefox, then confirm in hecate's log that the phone's queries arrive.

**4.6 Testing.** [adblock.turtlecute.org](https://adblock.turtlecute.org/) (128 ad/tracker hosts; for DNS filters it only scores the host category) and [canyoublockit.com](https://canyoublockit.com/results/). The best comparison is hecate's own query log. **[Fact]**

---

## 5. Android

- **Chrome for Android does not support desktop extensions**, so uBlock Origin cannot be installed there ([uBO Lite](https://ublockorigin.com/lite/)). **[Fact]**
- **Firefox for Android runs full uBlock Origin**, which blocks pop-ups through its built-in lists ([AMO](https://addons.mozilla.org/addon/ublock-origin/)). **[Fact]**
- Chromium-based browsers with extensions: Kiwi stopped in January 2025; Quetta, Lemur and Yandex support Chrome extensions but uBO is not confirmed ([MakeUseOf](https://www.makeuseof.com/android-browsers-that-run-extensions-chrome-keeps-leaving-out/)). **[Fact]**
- **Brave** has built-in Shields and broad uBO-syntax compatibility, but community threads report pop-ups slipping through even in Aggressive mode on Android while Firefox + uBO blocks the same sites. User reports only. **[Fact, anecdote]**
- **HTTPS inspection:** a CA in the user store is enough for browsers and some apps; other apps trust only the system store, which needs root; since Android 7, apps targeting API 24+ ignore user CAs unless they opt in ([AdGuard](https://adguard.com/kb/de/adguard-for-android/features/rooted/)). One 2025 write-up says Android 14 makes adding a system CA harder even with root (single source, unverified). **[Fact]**
- Root is not needed for DNS blocking and does not make blocking "complete". **[Inference]**
- Local filter apps (VpnService) are actively maintained: RethinkDNS (Apache-2.0), personalDNSfilter (GPL-2.0), Intra (Apache-2.0); DNS66 is archived. **[Fact]**

---

## 6. Reusable code

| Candidate | License | Status (2026-10-18) | Fit |
|---|---|---|---|
| [AdGuard `dnsproxy`](https://github.com/AdguardTeam/dnsproxy) | Apache-2.0 | Go, active | Network layer: UDP/TCP/DoT/DoH/DoQ listeners, DoH/DoT/DoQ/DNSCrypt upstreams, cache, rate limit |
| [AdGuard `urlfilter`](https://github.com/AdguardTeam/urlfilter) | **GPL-3.0** | Go, active | Adblock-style rule engine for DNS (used by AdGuard Home) |
| [Blocky](https://github.com/0xERR0R/blocky) | Apache-2.0 | Go, active | Design reference or fork base |
| [`miekg/dns`](https://github.com/miekg/dns) | BSD-3 | v1.1.73 | Low-level DNS message library |
| CoreDNS | Apache-2.0 | active | Plugin-based DNS server |

`dnsproxy` can be imported as a Go library (`proxy.Config`, `proxy.New`, `Start/Shutdown`); `Config.RequestHandler` accepts a custom handler, which is where the blocking logic sits. List licenses: HaGeZi GPL-3.0 (downloaded at run time, not linked), StevenBlack MIT, ABPindo unspecified, EasyList dual-licensed.

This opened a fourth build option: **own filter code on top of `dnsproxy`**, with transport and cache from a tested library. Trade-off: dependence on a library API that is still changing (v0.x). The project ended up with two backends sharing one engine: the `dnsproxy` adapter and a `native` implementation.

---

## 7. Implications and recommendation

**Honest expectation:** hecate removes most cross-domain ads (including known pop-up networks and click trackers), will not remove 100% of browser ads, and does not clean up empty ad boxes.

**Version 1 should have:** a query log and custom allow/block rules with live reload (without a log the redirect trigger is guesswork); HaGeZi PRO + Pop-Up Ads + ABPindo with daily updates (PRO, not ULTIMATE); a CNAME-chain check; NULL IP for A/AAAA and NODATA for HTTPS/SVCB on blocked names; encrypted upstreams.

**Complement that closes the DNS gap:** Firefox with uBlock Origin on the phone, the only documented way to stop pop-unders that ride on a real click and scripts from a site's own domain.

---

## 8. Gaps and unverified claims

- No independent study compares DNS and extensions with a percentage; the one quantitative source is a vendor, on US news sites, with no pop-up or redirect data.
- Not verified: Vivaldi/Opera/Samsung Internet built-in blockers; Brave Android's current state; the Android 14 system-CA claim; current Chrome/Firefox DoH behaviour (sources from 2020); HTTPS-record handling in AdGuard Home; ad-network domain rotation claims; the Firefox canary domain; IPv6 bypass.
- Some sources are search summaries only (Chrome Android, Firefox DoH, Brave, ad networks, the 2017 study, one Pi-hole thread). Partisan: piholekiller.com. Anecdotal: Reddit and forums.

---

## 9. If an APK is built (an on/off switch, not a browser)

| Form | How it works | Blocking power vs a DNS server | VPN icon | Effort |
|---|---|---|---|---|
| **1. Local DNS filter** | VpnService intercepts DNS; same domain lists | **Same** (no server, no hosting cost) | Yes | Medium |
| **2. DNS + HTTPS filter** | VpnService + user-space TCP/IP stack + MITM with a private CA; CSS and scripts injected | **More** (hides ad boxes, pop-up scripts, URL rules), but only in browsers that trust the CA | Yes | Heavy |
| **3. Private DNS switch** | A small app/tile that toggles the Private DNS setting to hecate | **Same** (still a DNS server) | No | Light |

**Facts.** **BlockAds** (open source) already does forms 1 and 2 without root: Kotlin app, Go tunnel (gomobile), GPL-3.0; DNS filtering with a trie; HTTPS filtering with a user-space TCP/IP stack (gVisor netstack), intercepting only chosen browsers, injecting cosmetic CSS and scripts (EasyList `##+js(...)`, AdGuard `#%#//scriptlet(...)`), with 284 excluded banking/payment domains; apps with certificate pinning are not intercepted; Quick Settings tile and widget for on/off ([repo](https://github.com/pass-with-high-score/blockads-android); I have not audited the code). **AdGuard for Android** (closed source) follows the same pattern, and Android 11 and up requires manual certificate installation. Private DNS switches already exist on F-Droid (DNS Toggle, Private DNS Quick Tile); they need the `WRITE_SECURE_SETTINGS` permission granted once through `adb`, with no root. **[Fact]**

**Inference.** Even form 2 is **not "full"**: quality follows externally maintained filter lists, ads stitched into video streams (for example YouTube) stay out of reach, and non-browser apps that distrust user CAs are not filtered. Form 2 also adds risk, since the app holds a CA private key that can read all of the browser's HTTPS, and it needs a large subsystem (TCP/IP stack, per-host TLS, HTTP/2, QUIC handling, bank exclusions). Forms 1 and 3 reuse hecate's DNS engine.

**Unverified:** pop-up handling in BlockAds, QUIC handling in all forms, behaviour on the target phone's OS (adb permission, battery saving), and the quality and security of BlockAds' code (the project is about 8 months old).
