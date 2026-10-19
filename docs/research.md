# Riset: Pemblokiran Iklan di Browser — Metode, Batas, dan Dampaknya ke HecateDNS

*Dibuat: 2026-10-18 | Sumber dibaca: ±45 (sebagian hanya ringkasan hasil pencarian, ditandai) | Keyakinan keseluruhan: **Sedang***

Cakupan: iklan di **browser** pada HP Android (RedMagic 8S Pro), dengan fokus pop-up, auto-redirect, click-redirect, dan redirect ke Play Store / aplikasi Shopee. Pertanyaan utama Master: *apakah DNS bisa memblokir iklan di browser secara penuh, dan metode apa saja yang ada?*

Label yang dipakai: **[Fakta]** ada sumbernya, **[Vendor]** klaim penjual produk, **[Inferensi]** kesimpulan saya sendiri, **[Belum terverifikasi]** tidak sempat/tidak bisa dicek.

---

## Ringkasan eksekutif

1. **Tidak ada metode tunggal yang memblokir 100% iklan di browser.** Metode paling lengkap untuk iklan di browser adalah pemblokir konten di dalam browser (uBlock Origin di Firefox Android). DNS adalah lapisan yang lebih sederhana, bekerja untuk semua aplikasi, tetapi hanya melihat nama domain.
2. **Vendor DNS sendiri mengakui batas ini.** AdGuard (menjual pemblokir DNS dan ekstensi) menulis bahwa DNS "tidak akan menggantikan pemblokir berbasis browser" dan tidak bisa menangani iklan satu-domain ([AdGuard DNS blog](https://adguard-dns.io/en/blog/dns-content-blocking-at-scale.html)).
3. **Koreksi dari saya:** angka "85–95%" yang saya sebut sebelumnya **tidak punya sumber** dan sebaiknya diabaikan. Satu-satunya data kuantitatif yang saya temukan (vendor, situs berita AS) menunjukkan DNS menghemat sekitar **74%** bandwidth dibanding ekstensi browser, dan menyisakan lebih banyak domain pelacak (489 vs 364 dari baseline 829). Tidak ada studi independen yang membandingkan keduanya, dan tidak ada data khusus pop-up/redirect.
4. **Untuk pop-up dan redirect, DNS efektif hanya jika pemicunya domain pihak ketiga yang sudah masuk daftar.** Redirect ke Play Store biasanya melewati rantai domain pelacak klik. Memblokir satu mata rantai menghentikan seluruhnya ([Malwarebytes](https://www.malwarebytes.com/blog/news/2014/02/ad2store-redirections-the-latest-annoyance-for-mobile-users), 2014). Jika pemicunya skrip dari domain situs itu sendiri atau overlay klik yang membuka `window.open` dengan klik asli, DNS tidak bisa menghentikannya.
5. **Temuan yang mengubah desain hecate:** log query dan aturan kustom harus ada di versi pertama; server sebaiknya memeriksa rantai CNAME; jawaban untuk query tipe HTTPS/SVCB (type 65) pada domain terblokir harus ditangani; ada daftar khusus **Pop-Up Ads** (HaGeZi) dan daftar domain Indonesia (ABPindo) yang siap pakai; dan ada pustaka Go yang bisa dipakai ulang (AdGuard `dnsproxy`, Apache-2.0).

---

## 1. Peta metode pemblokiran iklan

| Metode | Cara kerja | Yang bisa | Yang tidak bisa | Root? |
|---|---|---|---|---|
| **DNS sinkhole** (Pi-hole, AdGuard Home, Blocky, NextDNS, hecate) | Domain terblokir dijawab `0.0.0.0`/NXDOMAIN | Semua domain iklan/pelacak pihak ketiga, semua aplikasi di jaringan, tanpa pasang apa pun di HP (via Private DNS) | Iklan satu-domain, URL-path, menyembunyikan kotak iklan, skrip pop-up dari domain situs | Tidak |
| **Pemblokir konten di browser** (uBlock Origin, Brave Shields, dll.) | Aturan URL/konteks, cosmetic filtering, scriptlet | Hampir semua yang di atas, plus opsi `popup`/`popunder`, hide elemen, scriptlet | Hanya di browser itu; tidak untuk aplikasi lain | Tidak |
| **Pengaturan bawaan browser** (Chrome: Pop-ups and redirects, blok notifikasi) | Blokir jendela tanpa gestur | Pop-up tanpa klik | Pop-under yang menumpang klik asli | Tidak |
| **Filter lokal via VpnService** (RethinkDNS, personalDNSfilter, NetGuard, AdGuard non-root) | Aplikasi mencegat DNS (dan opsional HTTPS) | Seperti DNS, sistem-wide, tanpa server | Seperti DNS; HTTPS hanya jika sertifikat dipercaya | Tidak |
| **Pemeriksaan isi HTTPS** (MITM dengan CA buatan) | Dekripsi lalu filter | Iklan satu-domain pada aplikasi yang percaya CA | Aplikasi yang hanya percaya system store | Root untuk sebagian besar aplikasi |
| **Root** (hosts file, CA ke system store) | Ubah sistem | Setara DNS; membuka MITM untuk lebih banyak aplikasi | Tidak menjadikan "penuh" | Ya |

**[Belum terverifikasi dalam riset ini]:** proxy MITM di jaringan rumah (mitmproxy/Privoxy/Squid) dan klien termodifikasi (mis. ReVanced). Keduanya tidak saya riset; proxy MITM kemungkinan terkena batasan CA yang sama (bagian 5), dan klien termodifikasi untuk aplikasi tertentu di luar cakupan "iklan di browser".

---

## 2. DNS vs pemblokir di browser: seberapa jauh bedanya?

### Alasan teknis (pembuat uBlock Origin)
Halaman wiki uBO tentang "Why uBlock Origin works so much better than Pi-hole" menyebut: pembatalan request lebih awal di dalam browser, pencocokan URL penuh (teks/wildcard/regex), aturan sadar konteks (jenis request dan pihak), pengalihan ke sumber daya "netral", cosmetic filtering, injeksi scriptlet, filter HTML (hanya Firefox/Tor Browser), penanganan pop-up (`$popup,third-party`), dan pembuangan parameter pelacak (`$removeparam`). Kesimpulannya: filter DNS "tumpul" dibanding ekstensi, tetapi kekuatan DNS adalah mencakup semua perangkat di jaringan ([uBO wiki](https://github.com/gorhill/uBlock/wiki/About-%22Why-uBlock-Origin-works-so-much-better-than-Pi%E2%80%91hole-does%3F%22)). **[Fakta]**

Sintaks filter uBO punya opsi `popup`, `popunder` ("memblokir popunder tempat halaman asli dialihkan ke iklan"), `redirect`, `removeparam`, `csp`, dan injeksi scriptlet `##+js(...)` ([uBO static filter syntax](https://github.com/gorhill/uBlock/wiki/Static-filter-syntax)). **[Fakta]**

### Angka (satu-satunya yang ada, dan berasal dari vendor)
Laporan AdGuard (terbit 2025-12-17; uji 2025-03-13; Chrome; 119 situs berita/penerbit AS; masing-masing dimuat tiga kali) ([AdGuard report](https://adguard.com/en/blog/adguard-tracker-traffic-ad-report.html)). **[Vendor]**

| Ukuran | Tanpa blokir | AdGuard DNS | Ekstensi browser |
|---|---|---|---|
| Request per halaman | 299 | tidak dilaporkan | 145 (−51%) |
| Bandwidth dihemat (total 119 situs) | – | 198,52 MB (1,67 MB/situs) | 267,42 MB (>2,2 MB/situs) |
| Waktu muat rata-rata | >11 detik | 7,4 detik | 6,2 detik |
| Domain pelacak unik | 829 | 489 (−41%) | 364 (−56%) |
| Perusahaan pelacak | 233 | 107 | 73 |

Hitungan saya: penghematan bandwidth DNS ≈ 74% dari penghematan ekstensi (198,52 / 267,42). **[Inferensi]**
Catatan metodologi: satu browser, situs berita AS (bukan situs Indonesia, bukan situs streaming/unduhan yang banyak pop-up), kedua produk milik vendor yang sama, dan tidak ada ukuran khusus pop-up/redirect. Vendor menyimpulkan ekstensi "umumnya memberi hasil lebih baik", DNS lebih efisien hanya pada segelintir situs, dan merekomendasikan menggabungkan keduanya.

### Data lain
- Studi mobile 2017 "Block Me If You Can": daftar berbasis DNS terbaik (MoaAB) hanya mengurangi request ke pihak ketiga sekitar 25% ([paper](https://eprints.cs.univie.ac.at/6446/1/block_me_if_you_can.pdf)). Sudah tua; hanya dari ringkasan pencarian. **[Fakta, usang]**
- Satu pengguna forum Pi-hole membandingkan sebelum/sesudah dan menemukan blokir tambahan uBO "sangat kecil atau nol" ([forum Pi-hole](https://discourse.pi-hole.net/t/poor-performance-compared-to-ublock/3603)). Anekdot lama, satu pengaturan. **[Fakta, anekdot]**
- Situs advokasi Pi-hole berargumen tes ad-blocker sering salah menilai DNS karena mengecek elemen HTML, bukan apakah iklan benar-benar dimuat ([piholekiller.com](https://piholekiller.com/why-pihole-fails)). Sumber partisan. **[Fakta, partisan]**

### Kesimpulan bagian 2
Tidak ada studi independen dengan persentase. DNS memblokir sebagian besar permintaan lintas-domain, tetapi lebih sedikit daripada pemblokir di browser, dan tidak membersihkan kotak iklan kosong yang tertinggal.

---

## 3. Pop-up, pop-under, click-redirect, dan redirect ke Play Store

### Mekanisme
- Pop-under memakai `window.open()` lalu mengirim jendela ke belakang. Browser modern hanya mengizinkan `window.open` dari event interaksi pengguna, sehingga skrip pop-under **memasang listener klik pada seluruh dokumen** dan menumpang klik asli pengguna ([Wikipedia](https://en.wikipedia.org/wiki/Pop-under_ad)). **[Fakta]**
- Pemblokir bawaan Chrome hanya menghentikan pop-up *tanpa* gestur; pop-under menumpang klik asli sehingga terlihat seperti dibuka pengguna ([README no-popunder](https://github.com/nimitbhargava/no-popunder)). Penulisnya menyimpulkan perlu alat yang menarget teknik, bukan daftar domain. **[Fakta]**
- Satu skrip pop-under dianalisis menggunakan trik mousedown/mouseup dan elemen PDF base64 untuk mengembalikan fokus; penulis menyebut memblokir skrip berdasarkan nama "sangat bocor" ([Alex Rhodes, 2017](https://alexrhodes.io/blog/post/15/)). **[Fakta, usang]**
- Overlay transparan dan hover ad adalah bagian dari HTML halaman sehingga pemblokir pop-up tidak bisa mengenalinya ([Wikipedia](https://en.wikipedia.org/wiki/Pop-under_ad)). **[Fakta]**

### Rantai redirect ke toko aplikasi
Malwarebytes (2014) menjelaskan iklan yang memulai rantai redirect panjang tanpa interaksi pengguna. Satu domain pelacak mengirim HTTP 302 ke URL toko; di Android bentuknya `market://details?id=...` dengan parameter `referrer` untuk pelacakan kampanye. Memblokir domain mata rantai itu mencegah redirect. Pengujinya memakai pemblokir pop-up bawaan dan redirect tetap terjadi (di iOS). Artikel ini 12 tahun; pola umumnya masih relevan tetapi domain dan detail hampir pasti sudah berbeda ([Malwarebytes](https://www.malwarebytes.com/blog/news/2014/02/ad2store-redirections-the-latest-annoyance-for-mobile-users)). **[Fakta, usang]**

### Apa yang sudah dibatasi Chrome
- Chrome Android tidak meluncurkan aplikasi lewat `intent://` tanpa gestur pengguna, termasuk dari timer JavaScript ([Chrome docs](https://developer.chrome.com/docs/android/intents)). Halaman itu tidak membahas `market://`. **[Fakta]**
- Iframe lintas-origin hanya boleh menavigasi frame teratas saat ada gestur pengguna ([Chromium intent-to-deprecate](https://groups.google.com/a/chromium.org/g/Blink-dev/c/Xi8-y4ySjA4/m/D80epeAMAAAJ), [uji framebusting](https://chromium.googlesource.com/chromium/src/+/lkgr/content/browser/framebusting_browsertest.cc)). Celah lewat header CSP sandbox ditambal 2022. **[Fakta]**
- Pemblokir "tab-under" membatalkan navigasi hanya jika beberapa syarat terpenuhi bersamaan (tab membuka popup sejak gestur terakhir, navigasi dari renderer tanpa gestur, tab di latar belakang, lintas-origin); awalnya hanya di Android. **[Fakta]**

### Yang lolos dari browser dan DNS
- **[Inferensi]** Skrip yang dimuat langsung di halaman utama dapat mengubah `location` halaman itu sendiri tanpa gestur; itu bukan navigasi iframe dan bukan tab-under, sehingga tidak tercakup pembatasan di atas. Yang menghentikannya adalah menghilangkan skrip tersebut, misalnya memblokir domain pemuat skripnya (DNS atau uBO).
- **[Inferensi]** Pola "ketuk di mana saja lalu pindah ke Play Store" kemungkinan memakai overlay klik atau listener dokumen yang menumpang gestur asli.

### Perlombaan senjata
Ringkasan hasil pencarian menyebut jaringan iklan menjual fitur pelewat pemblokir, termasuk "Dynamic AdBlock Domain Rotation" (ExoClick) dan "Anti-Adblock" (Adcash, klaim pemulihan 98% oleh vendor sendiri). Saya tidak membuka halaman aslinya. **[Vendor, belum terverifikasi]** Artinya daftar blokir statis akan selalu tertinggal; itulah alasan daftar harian dan daftar domain baru (bagian 4.1).

### Konteks Indonesia
- **ABPindo** adalah filter tambahan EasyList untuk iklan berbahasa Indonesia dan Malaysia: iklan promosi berlebihan, judi, dewasa, dan iklan yang menyamar sebagai tombol update/stream/unduh ([repo](https://github.com/ABPindo/indonesianadblockrules), aktif; terakhir diperbarui 2026-10-18). Versi untuk DNS (`domain.txt`, `hosts.txt`, `aghome.txt`) hanya memuat `adservers.txt` dan `thirdparty.txt` demi menekan false positive. **[Fakta]**
- Artikel media Indonesia ([Bisnis](https://teknologi.bisnis.com/read/20231225/280/1726980/cara-blokir-iklan-pop-up-google-chrome-di-ponsel-android), [CNBC Indonesia](https://www.cnbcindonesia.com/tech/20260506180611-37-732875/cara-blokir-iklan-di-hp-android-tanpa-aplikasi-tambahan-mudah)) hanya memberi saran umum: Chrome → Setelan Situs → Pop-up dan pengalihan, blok notifikasi situs, cek izin "tampil di atas aplikasi lain", dan hapus aplikasi asing. Tidak ada yang spesifik tentang redirect ke Play Store. **[Fakta, generik]**

### Penyebab kasus Master
**[Belum terverifikasi]** Saya belum tahu domain mana yang memicu redirect ke Play Store di HP Master. Cara mengukurnya: picu redirect itu, catat jam, baca log query DNS di sekitar jam tersebut, lalu blokir domain pemicunya.

---

## 4. Teknik DNS yang menambah kekuatan (relevan untuk HecateDNS)

### 4.1 Daftar blokir
| Daftar | Isi | Catatan |
|---|---|---|
| HaGeZi Multi LIGHT / NORMAL / **PRO** / PRO++ / ULTIMATE | Tingkat agresif dari iklan/pelacak sampai phishing/malware | PRO direkomendasikan penulisnya; risiko false positive rendah-sedang; ULTIMATE bisa merusak situs/aplikasi |
| HaGeZi **Pop-Up Ads** | Jaringan pop-up "dari mengganggu sampai berbahaya" | Ada daftar sendiri (±47 ribu entri pada 2026-10-18) |
| HaGeZi NRD/DGA | Domain yang baru terdaftar / dihasilkan algoritma | Besar, boros memori, bisa false positive; relevan untuk domain yang berganti cepat |
| HaGeZi DoH/VPN/Tor/Proxy Bypass | Domain/IP yang dipakai untuk melewati DNS | Perlu juga memblokir keluar TCP/UDP 53 dan 853 di router agar efektif |
| **ABPindo** `domain.txt` | Iklan lokal Indonesia/Malaysia | Format domain-saja, ramah DNS |
| StevenBlack hosts | Hosts klasik | Lisensi MIT, aktif |

Detail HaGeZi (2026-10-18): jumlah entri LIGHT 62.932, NORMAL 165.192, PRO 201.729, Pop-Up Ads 47.268 (berubah tiap build). Format: Adblock, DNSMasq, Wildcard Asterisk, Wildcard Domains, RPZ. Pembaruan harian 11:00 UTC (GitHub/jsDelivr/GitLab/Codeberg), mirror build setiap ±4–8 jam. Contoh URL: `https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/wildcard/pro-onlydomains.txt` dan `.../wildcard/popupads-onlydomains.txt`. Penulisnya sendiri menyarankan memasangkan daftar DNS dengan pemblokir konten di browser ([HaGeZi](https://github.com/hagezi/dns-blocklists)). **[Fakta]**

### 4.2 CNAME uncloaking
Pelacak/iklan menyamar sebagai subdomain situs lewat rekaman CNAME, dan ekstensi di Chrome/Safari tidak bisa melihat CNAME. Resolver DNS bisa: NextDNS menerapkan daftar blokir ke **setiap CNAME perantara** selain nama yang ditanyakan; daftarnya perlu dicocokkan secara wildcard ([NextDNS](https://medium.com/nextdns/nextdns-added-cname-uncloaking-support-becomes-the-first-cross-platform-solution-to-the-problem-e3f437f84342), [daftar CNAME NextDNS](https://github.com/nextdns/cname-cloaking-blocklist)). AdGuard menyusun daftar CNAME tersembunyi (>6.000 entri pada 2021) ([AdGuard](https://adguard.com/en/blog/cname-tracking.html), [repo](https://github.com/AdguardTeam/cname-trackers/)). Penelitian: 1.739 situs (0,58%) dari Alexa 300K memakai pelacakan CNAME-cloaking per Januari 2020, dan naik tiap tahun ([APNIC](https://blog.apnic.net/2020/08/04/characterizing-cname-cloaking-based-tracking/), [paper](http://www.fukuda-lab.org/publications/2021/DMF_tnsm2021.pdf)). NextDNS juga mengonfirmasi CNAME cloaking bisa dipakai untuk menyajikan iklan. **[Fakta]**
Peringatan AdGuard: sebagian CNAME-tracker mengarah ke mikrosite/halaman tujuan atau dipakai untuk redirect ke sumber akhir, sehingga memblokirnya mengubah pengalaman pengguna. **[Fakta]**
uBO hanya bisa uncloak CNAME di Firefox (API DNS browser); sumber 2019 menyebut hanya Firefox desktop ([NextDNS](https://medium.com/nextdns/nextdns-added-cname-uncloaking-support-becomes-the-first-cross-platform-solution-to-the-problem-e3f437f84342)). **[Inferensi]** Jika berlaku juga di Firefox Android, pemeriksaan CNAME di hecate mengisi celah nyata di HP.

### 4.3 Bentuk jawaban untuk domain terblokir
| Mode | Perilaku | Catatan |
|---|---|---|
| NULL IP (`0.0.0.0` / `::`) | Klien biasanya tidak mencoba terhubung | Default dan rekomendasi Pi-hole; default AdGuard Home untuk aturan gaya adblock ([Pi-hole docs](https://docs.pi-hole.net/ftldns/blockingmode/), [AGH #8095](https://github.com/AdguardTeam/AdGuardHome/issues/8095)) |
| NXDOMAIN | Klien bisa mengulang query lebih sering | Eksperimen Pi-hole; uji tidak ilmiah di Android pada AdGuard Home juga menunjukkan query berulang lebih banyak daripada NULL ([PR #743](https://github.com/AdguardTeam/AdGuardHome/pull/743)) |
| NODATA | Penerimaan klien sedikit lebih baik dari NXDOMAIN | Insinyur Chromium mengusulkan NODATA + TTL via SOA + EDE (RFC 8914) sebagai jawaban yang jujur dan bisa di-cache ([forum Pi-hole](https://discourse.pi-hole.net/t/consider-making-nodata-the-default-blocking-strategy/46080)) |
| REFUSED | Paling "jujur", tetapi perangkat lama bisa pindah ke DNS cadangan | ([AdGuard DNS blog](https://adguard-dns.io/en/blog/dns-content-blocking-at-scale.html)) |

Catatan: Pi-hole memakai TTL 2 detik untuk jawaban terblokir agar pengecualian (allowlist) cepat berlaku, dan memilih NULL demi kompatibilitas perangkat IoT yang akan "membombardir" server jika menerima NODATA ([forum Pi-hole](https://discourse.pi-hole.net/t/consider-making-nodata-the-default-blocking-strategy/46080)). Ada laporan satu pengguna bahwa NXDOMAIN membuat Chrome/Firefox Android "menggantung" dan NULL menyembuhkannya; anekdot satu orang. **[Fakta, anekdot]**
**[Inferensi]** Untuk HP Android Master, NULL IP adalah titik awal yang paling masuk akal; ukur langsung di RedMagic.

### 4.4 Rekaman HTTPS/SVCB (type 65) dan DDR
Rekaman HTTPS/SVCB bisa membawa `ipv4hint`/`ipv6hint`, sehingga klien dapat tersambung tanpa jawaban A/AAAA; pengguna Pi-hole melaporkan ini mengakali pemblokiran DNS, dan solusi yang dibahas adalah menjawab NODATA untuk query tipe itu. `_dns.resolver.arpa` (DDR) dapat mengarahkan klien ke DoH milik penyedia dan melewati pemblokiran lokal ([forum Pi-hole](https://discourse.pi-hole.net/t/are-svcb-queries-sent-through-pi-hole-of-blocking-concern/59983), [usulan fitur](https://discourse.pi-hole.net/t/implement-dns-resolver-arpa-as-a-special-domain-or-add-block-svcb-as-a-configuration-option/77099), [RFC 9460](https://www.iana.org/go/rfc9460)). **[Fakta]**
Penanganan di AdGuard Home **[Belum terverifikasi]**. **[Inferensi]** Untuk hecate: domain terblokir harus mendapat NODATA juga untuk tipe 65, dan `_dns.resolver.arpa` dijawab NODATA kecuali kita memang mengimplementasikan DDR.

### 4.5 Jalan keluar dari DNS kita
- Chrome "Secure DNS" mode otomatis hanya meng-upgrade ke DoH jika penyedia DNS saat ini ada di daftar bawaan Chrome, dan bisa jatuh kembali ke DNS biasa ([Chromium blog 2020](https://blog.chromium.org/2020/09/a-safer-and-more-private-browsing.html), [9to5Google](https://9to5google.com/2020/09/02/chrome-secure-dns-android/)). **[Fakta, usang]**
- Firefox Android: "Default Protection" mengaktifkan DoH jika tersedia di wilayah dengan mitra, dan mati bila jaringan memberi tahu Firefox untuk tidak memakainya ([Mozilla](https://support.mozilla.org/fi/kb/configure-dns-over-https-protection-levels-firefox-android)). **[Fakta, hanya ringkasan]**
- **[Belum terverifikasi]** Domain "canary" Firefox (`use-application-dns.net`) dan bypass lewat IPv6 DNS dari router tidak saya verifikasi di sini.
- Cara menguji di HP: periksa setelan Secure DNS di Chrome dan DoH di Firefox, lalu cocokkan dengan log hecate apakah query HP benar-benar masuk.

### 4.6 Menguji hasil
Situs uji siap pakai: [adblock.turtlecute.org](https://adblock.turtlecute.org/) (128 host iklan/pelacak; untuk filter DNS hanya menilai kategori host) dan [canyoublockit.com](https://canyoublockit.com/results/). Pembanding terbaik tetap log query hecate sendiri. **[Fakta]**

---

## 5. Android (RedMagic 8S Pro)

- **Chrome Android tidak mendukung ekstensi desktop**, jadi uBO tidak bisa dipasang di sana ([uBlock Origin Lite](https://ublockorigin.com/lite/), [ringkasan pencarian](https://ublockorigin.com/chrome/)). Tidak ada pengumuman 2026 bahwa ini akan berubah (sumber: situs uBO dan blog pihak ketiga; mungkin bias). **[Fakta, kuat sebagian]**
- **Firefox Android menjalankan uBlock Origin penuh.** Listing Mozilla menunjukkan versi 1.72.2 (8 Juli 2026), Android 115 ke atas, dan uBO memblokir pop-up lewat daftar bawaan (EasyList, EasyPrivacy, Peter Lowe, daftar URL berbahaya) ([AMO](https://addons.mozilla.org/addon/ublock-origin/)). **[Fakta]**
- **Browser Chromium dengan ekstensi:** Kiwi berhenti Januari 2025 dan tidak lagi menerima patch keamanan; Quetta, Lemur, dan Yandex mendukung ekstensi Chrome tetapi artikel tidak memastikan uBO ([MakeUseOf, 2026-04-22](https://www.makeuseof.com/android-browsers-that-run-extensions-chrome-keeps-leaving-out/)). **[Fakta]**
- **Brave** punya Shields bawaan dan menyatakan kompatibel luas dengan sintaks uBO ([Brave support](https://support.brave.app/hc/ja-jp/articles/6449369961741)), tetapi beberapa thread komunitas melaporkan pop-up lolos meski mode Aggressive di Android, sementara Firefox + uBO memblokir situs yang sama ([thread 1](https://community.brave.app/t/popup-ads-bypassing-shields-even-with-aggressive-mode-android/656651), [thread 2](https://community.brave.app/t/brave-shields-unable-to-block-popup-new-tab-ads/581182)). Hanya laporan pengguna; tidak ada uji independen. **[Fakta, anekdot]**
- **Pemeriksaan HTTPS (MITM):** CA di user store cukup untuk browser dan sebagian aplikasi; aplikasi lain hanya percaya system store, dan menaruh CA di system store memerlukan root (modul Magisk `adguardcert`). Sejak Android 7 aplikasi yang menarget API 24+ tidak mempercayai CA pengguna kecuali memilih ikut serta ([AdGuard: perangkat root](https://adguard.com/kb/de/adguard-for-android/features/rooted/), [AdGuard: Nougat](https://adguard.com/en/blog/android-nougat-release-and-what-does-it-mean-for-adguard-users/), [adguardcert](https://github.com/AdguardTeam/adguardcert), [blog JeroenHD](https://blog.jeroenhd.nl/article/android-7-nougat-and-certificate-authorities)). Satu tulisan pentest 2025 menyebut Android 14 mempersulit penambahan system CA bahkan dengan root; sumber tunggal. **[Fakta; klaim Android 14 belum terverifikasi]**
- **Root** tidak diperlukan untuk pemblokiran DNS, dan tidak menjadikan pemblokiran "penuh". **[Inferensi]**
- **Aplikasi filter lokal (VpnService)** sudah ada dan aktif dipelihara: RethinkDNS (Kotlin, Apache-2.0, diperbarui 2026-10-17), personalDNSfilter (Java, GPL-2.0, 2026-10-17), Intra (Apache-2.0, 2026-09-16); DNS66 sudah diarsipkan (Maret 2026) ([GitHub API](https://github.com/celzero/rethink-app)). **[Fakta]**

---

## 6. Fondasi kode yang bisa dipakai ulang (Research & Reuse)

| Kandidat | Lisensi | Status (2026-10-18) | Cocok untuk |
|---|---|---|---|
| [AdGuard `dnsproxy`](https://github.com/AdguardTeam/dnsproxy) | Apache-2.0 | Go, 3,4 ribu bintang, aktif (2026-10-15) | Lapisan jaringan: listener UDP/TCP/DoT/DoH/DoQ, upstream DoH/DoT/DoQ/DNSCrypt, cache, rate limit, auth Basic untuk DoH |
| [AdGuard `urlfilter`](https://github.com/AdguardTeam/urlfilter) | **GPL-3.0** | Go, 116 bintang, aktif (2026-10-06) | Mesin aturan gaya adblock untuk DNS (dipakai AdGuard Home) |
| [Blocky](https://github.com/0xERR0R/blocky) | Apache-2.0 | Go, 7 ribu bintang, aktif | Acuan desain atau bahan fork |
| [`miekg/dns`](https://github.com/miekg/dns) | BSD-3 | v1.1.73 (2026-08-19), aktif; v2 pra-1.0 di `codeberg.org/miekg/dns` (v0.6.118) | Pustaka paket DNS tingkat rendah |
| CoreDNS | Apache-2.0 | aktif | Server DNS berbasis plugin |

Fakta penting tentang `dnsproxy`: bisa diimpor sebagai pustaka Go (`proxy.Config`, `proxy.New`, `Proxy.Start/Shutdown`); `Config.RequestHandler` memungkinkan handler kustom (kita menaruh logika blokir di sana); cache (`CacheEnabled`, `CacheSizeBytes`, `CacheMinTTL/MaxTTL`, optimistic); DoT lewat `TLSListenAddr`, DoH lewat `HTTPConfig` (termasuk `Userinfo` untuk Basic Auth), DoQ lewat `QUICListenAddr`. Halaman dokumentasi menampilkan versi v0.86.0 dan menandainya bukan yang terbaru; dokumentasinya masih memuat beberapa TODO. Saya tidak menemukan hook `BeforeRequestHandler`/`ResponseHandler` di dokumentasi ([pkg.go.dev](https://pkg.go.dev/github.com/AdguardTeam/dnsproxy/proxy)). **[Fakta; detail versi perlu dicek ulang saat menulis spec]**

Lisensi daftar: HaGeZi GPL-3.0 (hanya diunduh saat berjalan, tidak ditautkan ke kode), StevenBlack MIT, ABPindo `NOASSERTION`, EasyList berlisensi ganda.

**Dampak ke tiga pendekatan yang diajukan sebelumnya:** muncul opsi keempat, yaitu **kode filter sendiri di atas `dnsproxy`**. Pendekatan ini tetap "buatan sendiri" (mesin blokir, daftar, log, CNAME, SafeSearch) tetapi transport DoT/DoH/cache memakai pustaka yang sudah teruji. Kompromi: ketergantungan pada API pustaka yang masih berubah (v0.x), dan tidak mempelajari lapisan paket DNS dari dasar.

---

## 7. Dampak ke HecateDNS dan rekomendasi

**Ekspektasi yang jujur:** hecate akan mengurangi sebagian besar iklan lintas-domain (termasuk jaringan pop-up dan pelacak klik yang sudah dikenal daftar), tidak akan menghapus 100% iklan di browser, dan tidak membersihkan kotak iklan kosong.

**Versi pertama sebaiknya sudah punya:**
1. **Log query** (waktu, klien, nama, tipe, aksi, daftar/aturan yang cocok, rantai CNAME) dan **aturan blokir/izin kustom** dengan muat ulang tanpa restart. Alasannya: tanpa log, kita menebak pemicu redirect Master.
2. **Daftar:** HaGeZi PRO + Pop-Up Ads + ABPindo `domain.txt`, pembaruan otomatis harian. Mulai dari PRO, bukan ULTIMATE.
3. **Pemeriksaan rantai CNAME** pada jawaban hulu.
4. **Jawaban terblokir:** NULL IP untuk A/AAAA; NODATA untuk HTTPS/SVCB pada nama terblokir; uji di RedMagic.
5. **Hulu terenkripsi** (DoH/DoT) supaya DNS ISP tidak ikut campur.

**Pelengkap yang menutup celah DNS:** Firefox + uBlock Origin di HP. Hanya itu yang terdokumentasi bisa menghentikan pop-under yang menumpang klik asli dan skrip dari domain situs sendiri.

**Langkah tes berikutnya:**
- Ukur baseline dengan turtlecute / canyoublockit sebelum dan sesudah hecate.
- Picu redirect Play Store, catat jam, baca log.
- Cek setelan Secure DNS di Chrome dan DoH di Firefox saat tes di rumah.

**Keputusan terbuka:** pendekatan pembangunan (sekarang empat opsi; lihat bagian 6).

---

## 8. Celah riset dan klaim yang belum terverifikasi

- Tidak ada studi independen yang membandingkan DNS dan ekstensi dengan persentase; satu-satunya data kuantitatif berasal dari vendor, situs berita AS, tanpa data pop-up/redirect.
- Pemicu redirect Play Store di HP Master belum diketahui.
- Tiga query Exa kena batas gratis (perbandingan efektivitas, sumber otoritatif CA pengguna, README HaGeZi) dan diganti WebSearch/WebFetch.
- Sebagian sumber hanya ringkasan hasil pencarian (ditandai): ringkasan Chrome Android, Firefox DoH, Brave, jaringan iklan, studi 2017, forum Pi-hole 3603, GeoEdge.
- Belum diverifikasi: blocker bawaan Vivaldi/Opera/Samsung Internet; kondisi terbaru Brave Android; klaim system CA di Android 14; perilaku DoH Chrome/Firefox saat ini (sumber 2020); penanganan HTTPS-record di AdGuard Home; API `dnsproxy` selain `RequestHandler`; klaim rotasi domain jaringan iklan; proxy MITM jaringan; klien termodifikasi; domain canary Firefox; bypass IPv6.
- Sumber partisan: piholekiller.com. Sumber anekdot: Reddit, forum.

---

## 9. Tambahan: jika dibuat APK (switch on/off, bukan browser)

Pertanyaan Master: apakah APK bisa "full block" atau sama saja dengan DNS? Jawabannya tergantung isi APK. Ada tiga bentuk:

| Bentuk APK | Cara kerja | Daya blokir dibanding server DNS | Ikon VPN | Beban pembuatan |
|---|---|---|---|---|
| **1. Filter DNS lokal** | VpnService mencegat DNS; daftar domain yang sama | **Sama** (hanya tanpa server, jadi tanpa biaya hosting) | Ya | Sedang |
| **2. Filter DNS + HTTPS** | VpnService + tumpukan TCP/IP di ruang pengguna + MITM dengan sertifikat CA buatan sendiri; CSS dan skrip disuntik ke halaman | **Lebih banyak** (sembunyikan kotak iklan, skrip penahan pop-up, aturan URL), tetapi hanya di browser yang mempercayai CA | Ya | Berat |
| **3. Switch Private DNS** | Aplikasi/tile kecil yang menyalakan dan mematikan setelan Private DNS ke server hecate | **Sama** (ini tetap DNS server) | Tidak | Ringan |

**Fakta**
- **BlockAds** adalah aplikasi Android open source yang sudah melakukan bentuk 1 dan 2 tanpa root: Kotlin untuk aplikasi, Go untuk tunnel (gomobile), GPL-3.0, 2.192 bintang, dibuat 2026-02-10, rilis v7.0.0 pada 2026-10-14, commit terakhir 2026-10-18. Filter DNS memakai trie; pemfilteran HTTPS memakai tumpukan TCP/IP ruang pengguna (gVisor netstack via tun2socks), hanya mencegat aliran dari browser terpilih, menyuntik CSS kosmetik dan skrip (sintaks EasyList `##+js(...)` dan AdGuard `#%#//scriptlet(...)`), dan punya daftar pengecualian 284 domain (bank/pembayaran). Aplikasi yang memakai certificate pinning tidak dicegat. Ada Quick Settings tile dan widget untuk on/off. README tidak menyebut penanganan pop-up. Butuh JDK 17+, Android SDK 36, minSdk 24 untuk membangun ([repo](https://github.com/pass-with-high-score/blockads-android)). **[Fakta; belum saya audit kodenya]**
- **AdGuard untuk Android** (aplikasi tertutup) memakai pola yang sama: CA di user store cukup untuk menyaring HTTPS di browser berbasis Chromium; sebagian besar aplikasi lain hanya percaya system store, yang memerlukan root. Android 11 ke atas mewajibkan pemasangan sertifikat manual. Penyaringan HTTP/3 (QUIC) baru ditambahkan sebagai fitur beta sejak versi 4.2 ([AdGuard KB](https://adguard.com/kb/adguard-for-android/features/settings/), [AdGuard 4.2](https://adguard.com/fc/en/blog/adguard-v4-2-for-android.html)). **[Fakta]**
- Sumber sekunder lemah menyebut CA pengguna dipercaya langsung oleh browser Chromium, oleh Firefox hanya setelah mengaktifkan toggle tersembunyi, dan tidak oleh aplikasi native dengan certificate pinning ([Lantern corpus](https://corpus.lantern.io/findings/2026-patterniha-mitm-domainfronting__cert-pinning-limits-native-app-fronting/)). **[Fakta, sumber lemah]**
- **Switch Private DNS** sudah ada di F-Droid: [DNS Toggle](https://f-droid.org/packages/com.ericlowry.dnstoggle/) dan [Private DNS Quick Tile](https://f-droid.org/bo/packages/com.jpwolfso.privdnsqt). Keduanya membutuhkan izin `WRITE_SECURE_SETTINGS` yang diberikan sekali lewat `adb shell pm grant <paket> android.permission.WRITE_SECURE_SETTINGS` dari PC (atau root/Shizuku), tanpa root. **[Fakta; nama kunci setelan tidak saya verifikasi]**

**Inferensi**
- Bentuk 2 pun **tidak "full"**: kualitasnya mengikuti daftar filter dan skrip yang dirawat pihak lain (EasyList, AdGuard); iklan yang disisipkan server ke dalam aliran video (mis. YouTube) tetap di luar jangkauan; dan aplikasi selain browser yang tidak percaya CA pengguna tidak tersaring.
- Bentuk 2 menambah risiko keamanan: aplikasi memegang kunci privat CA yang bisa membaca seluruh HTTPS browser. Master harus memasang sertifikat itu secara manual dan mempercayai kode aplikasinya (membuat sendiri atau memakai BlockAds).
- Bentuk 2 membutuhkan subsistem besar yang tidak ada di rencana "sederhana": tumpukan TCP/IP, TLS per-host, HTTP/2, penanganan QUIC, dan pengecualian aplikasi bank. Bentuk 1 dan 3 memakai mesin blokir DNS yang sama dengan hecate.

**Belum terverifikasi:** penanganan pop-up di BlockAds, penanganan QUIC di semua bentuk, perilaku di RedMagic OS (izin `adb`, penghematan baterai), dan kualitas serta keamanan kode BlockAds (proyek berumur sekitar 8 bulan).

---

## Sumber

**Efektivitas DNS vs browser**
1. [AdGuard Web Performance Report 2025](https://adguard.com/en/blog/adguard-tracker-traffic-ad-report.html) — uji DNS vs ekstensi, vendor.
2. [uBO wiki: kenapa uBO lebih baik dari Pi-hole](https://github.com/gorhill/uBlock/wiki/About-%22Why-uBlock-Origin-works-so-much-better-than-Pi%E2%80%91hole-does%3F%22) — daftar alasan teknis.
3. [uBO static filter syntax](https://github.com/gorhill/uBlock/wiki/Static-filter-syntax) — opsi `popup`, `popunder`, scriptlet.
4. [AdGuard DNS blog: DNS content blocking](https://adguard-dns.io/en/blog/dns-content-blocking-at-scale.html) — kelebihan/kekurangan DNS.
5. [Block Me If You Can (2017)](https://eprints.cs.univie.ac.at/6446/1/block_me_if_you_can.pdf) — studi alat pemblokir mobile.
6. [Forum Pi-hole: performa vs uBlock](https://discourse.pi-hole.net/t/poor-performance-compared-to-ublock/3603) — anekdot.
7. [piholekiller.com](https://piholekiller.com/why-pihole-fails) — sumber partisan.
8. [adblock.turtlecute.org](https://adblock.turtlecute.org/), [canyoublockit.com](https://canyoublockit.com/results/) — alat uji.

**CNAME**
9. [NextDNS CNAME cloaking blocklist](https://github.com/nextdns/cname-cloaking-blocklist)
10. [NextDNS: uncloaking](https://medium.com/nextdns/nextdns-added-cname-uncloaking-support-becomes-the-first-cross-platform-solution-to-the-problem-e3f437f84342)
11. [NextDNS: CNAME cloaking](https://medium.com/nextdns/cname-cloaking-the-dangerous-disguise-of-third-party-trackers-195205dc522a)
12. [AdGuard: scan CNAME tracker](https://adguard.com/en/blog/cname-tracking.html)
13. [AdguardTeam/cname-trackers](https://github.com/AdguardTeam/cname-trackers/)
14. [APNIC: CNAME cloaking](https://blog.apnic.net/2020/08/04/characterizing-cname-cloaking-based-tracking/)
15. [Dao & Fukuda paper](http://www.fukuda-lab.org/publications/2021/DMF_tnsm2021.pdf)
16. [Brave: CNAME](https://brave.com/privacy-updates/6-cname-trickery/)

**Pop-up / redirect**
17. [Wikipedia: Pop-up ad](https://en.wikipedia.org/wiki/Pop-under_ad)
18. [no-popunder](https://github.com/nimitbhargava/no-popunder)
19. [Alex Rhodes: Pop-under JavaScript](https://alexrhodes.io/blog/post/15/)
20. [Malwarebytes: Ad2Store](https://www.malwarebytes.com/blog/news/2014/02/ad2store-redirections-the-latest-annoyance-for-mobile-users)
21. [Chrome Android intents](https://developer.chrome.com/docs/android/intents)
22. [Chrome Help: pop-up](https://support.google.com/chrome/answer/95472)
23. [Chromium: cross-origin top navigation](https://groups.google.com/a/chromium.org/g/Blink-dev/c/Xi8-y4ySjA4/m/D80epeAMAAAJ), [framebusting test](https://chromium.googlesource.com/chromium/src/+/lkgr/content/browser/framebusting_browsertest.cc)
24. [GeoEdge: Auto Redirects](https://site.geoedge.com/downloads/documents/Auto_Redirects.pdf) — hanya cuplikan.
25. Media Indonesia: [Bisnis](https://teknologi.bisnis.com/read/20231225/280/1726980/cara-blokir-iklan-pop-up-google-chrome-di-ponsel-android), [CNBC Indonesia](https://www.cnbcindonesia.com/tech/20260506180611-37-732875/cara-blokir-iklan-di-hp-android-tanpa-aplikasi-tambahan-mudah)

**Respons blokir / HTTPS record / bypass**
26. [Pi-hole: blocking mode](https://docs.pi-hole.net/ftldns/blockingmode/)
27. [Forum Pi-hole: NODATA default](https://discourse.pi-hole.net/t/consider-making-nodata-the-default-blocking-strategy/46080)
28. [AdGuard Home PR #743](https://github.com/AdguardTeam/AdGuardHome/pull/743), [issue #8095](https://github.com/AdguardTeam/AdGuardHome/issues/8095)
29. [r/Adguard: blocking mode](https://www.reddit.com/r/Adguard/comments/1jw07tw/adguard_home_dns_best_blocking_mode/) — anekdot.
30. [Forum Pi-hole: SVCB](https://discourse.pi-hole.net/t/are-svcb-queries-sent-through-pi-hole-of-blocking-concern/59983), [DDR](https://discourse.pi-hole.net/t/implement-dns-resolver-arpa-as-a-special-domain-or-add-block-svcb-as-a-configuration-option/77099), [RFC 9460](https://www.iana.org/go/rfc9460)
31. [Chromium blog: Secure DNS](https://blog.chromium.org/2020/09/a-safer-and-more-private-browsing.html), [9to5Google](https://9to5google.com/2020/09/02/chrome-secure-dns-android/), [Mozilla: DoH Android](https://support.mozilla.org/fi/kb/configure-dns-over-https-protection-levels-firefox-android)

**Daftar blokir**
32. [HaGeZi dns-blocklists](https://github.com/hagezi/dns-blocklists)
33. [ABPindo](https://github.com/ABPindo/indonesianadblockrules)
34. [StevenBlack/hosts](https://github.com/StevenBlack/hosts) — hanya metadata repo.

**Android**
35. [uBlock Origin Lite](https://ublockorigin.com/lite/), [uBO di Chrome](https://ublockorigin.com/chrome/) — hanya ringkasan pencarian.
36. [Mozilla AMO: uBlock Origin](https://addons.mozilla.org/addon/ublock-origin/) — hanya ringkasan pencarian.
37. [MakeUseOf: browser Android dengan ekstensi](https://www.makeuseof.com/android-browsers-that-run-extensions-chrome-keeps-leaving-out/)
38. [Brave support](https://support.brave.app/hc/ja-jp/articles/6449369961741), komunitas [1](https://community.brave.app/t/popup-ads-bypassing-shields-even-with-aggressive-mode-android/656651), [2](https://community.brave.app/t/brave-shields-unable-to-block-popup-new-tab-ads/581182)
39. [AdGuard: root & CA](https://adguard.com/kb/de/adguard-for-android/features/rooted/), [Nougat](https://adguard.com/en/blog/android-nougat-release-and-what-does-it-mean-for-adguard-users/), [adguardcert](https://github.com/AdguardTeam/adguardcert)
40. [JeroenHD: Android 7 dan CA](https://blog.jeroenhd.nl/article/android-7-nougat-and-certificate-authorities), [OWASP MASTG 0286](https://mas.owasp.org/MASTG/tests/android/MASVS-NETWORK/MASTG-TEST-0286/)
41. [RethinkDNS](https://github.com/celzero/rethink-app), [personalDNSfilter](https://github.com/IngoZenz/personaldnsfilter), [Intra](https://github.com/Jigsaw-Code/Intra), [DNS66](https://github.com/julian-klode/dns66) — metadata repo.

**Pustaka**
42. [AdGuard dnsproxy](https://github.com/AdguardTeam/dnsproxy), [pkg.go.dev](https://pkg.go.dev/github.com/AdguardTeam/dnsproxy/proxy)
43. [AdGuard urlfilter](https://github.com/AdguardTeam/urlfilter)
44. [Blocky](https://github.com/0xERR0R/blocky)
45. [miekg/dns](https://github.com/miekg/dns), proxy Go: `github.com/miekg/dns` v1.1.73, `codeberg.org/miekg/dns` v0.6.118

---

## Metodologi

Sekitar 20 pencarian web (WebSearch, mode standar dan extended; Exa dengan batas gratis tercapai pada 3 query), sekitar 12 halaman dibaca penuh via WebFetch (AdGuard report, uBO wiki x2, HaGeZi x2, Malwarebytes, Chrome intents, dnsproxy x2, ABPindo, MakeUseOf), dan beberapa panggilan API GitHub/proxy modul Go untuk status repo dan lisensi. Sub-pertanyaan: (1) seberapa jauh DNS tertinggal dari pemblokir browser; (2) metode di browser Android saat ini; (3) kemampuan dan batas filter sistem, HTTPS, dan root; (4) mekanisme pop-up/redirect dan penghentinya; (5) teknik DNS yang memperkuat hecate; (6) pustaka yang bisa dipakai ulang. Sumber di web diperlakukan sebagai data; tidak ada instruksi dari sumber yang dijalankan.
