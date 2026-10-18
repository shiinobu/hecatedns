# xdns — Laporan tes di HP (Fase 3)

*2026-10-18 | Status: S1 dan S3 terukur dan lulus; S2 lulus untuk redirect Shopee, redirect Play Store belum pernah muncul | Penguji: Master (HP) dan Claude (log, PC)*

Semua angka di bawah berasal dari tangkapan layar Master, log kueri xdns (`data/logs/queries-20261018.jsonl`), dan perintah yang dijalankan pada 2026-10-18. Yang hanya berupa pengamatan Master atau dugaan ditandai begitu.

## 1. Perangkat dan pengaturan

| Bagian | Nilai |
|---|---|
| PC server | Windows 11 Pro, Wi-Fi satu jaringan dengan HP. Alamat `192.168.1.99`, dipesan di router ZTE F663NV3a (menu Network-LAN-DHCP Binding; sebelumnya DHCP `192.168.1.10`), diverifikasi dengan `Get-NetIPAddress` setelah `ipconfig /release` dan `/renew` |
| xdns | `bin\xdns.exe` dibangun dari repo (kode terakhir `7fd1ac0`), backend `dnsproxy`, mendengarkan `udp` dan `tcp` `192.168.1.99:53`; `allowed_clients` hanya `127.0.0.0/8`, `::1/128`, `192.168.1.0/24`; tiga daftar bawaan termuat (250.089 entri) |
| UDP 53 | Layanan ICS (`SharedAccess`, svchost) memegang `0.0.0.0:53` UDP. Bind khusus ke `192.168.1.99:53` (UDP dan TCP) tetap berhasil, jadi ICS, Hyper-V, dan WSL tidak disentuh |
| Firewall | Dua aturan masuk (grup `xdns`): UDP 53 dan TCP 53, hanya ke `192.168.1.99`, hanya dari `LocalSubnet`, hanya untuk `bin\xdns.exe`, profil Public dan Private. Jaringan Wi-Fi di PC berkategori Public, jadi rencana awal "profil Private saja" tidak berlaku. Cabut dengan `Remove-NetFirewallRule -Group xdns` |
| HP | RedMagic 8S Pro, Chrome. Wi-Fi IP statis `192.168.1.150`, gateway `192.168.1.1`, DNS 1 `192.168.1.99`, DNS 2 kosong; DNS Pribadi dan "DNS aman" Chrome dimatikan (dikerjakan Master sesuai panduan; tidak ada pencarian host DoH di log, lihat bagian 4) |
| Keluarga | Perangkat lain di Wi-Fi yang sama tidak diubah. Log hanya memuat dua klien: HP (`192.168.1.150`) dan PC (`192.168.1.99`) |

## 2. Hasil kriteria

### S1 — iklan di daftar terblokir

| Tes | Hasil |
|---|---|
| `adblock.turtlecute.org`, dengan xdns | **95%** (126 dari 132 terblokir, 6 lolos) |
| Sama, setelah dua aturan kustom (`block ad.doubleclick.net`, `block adservice.google.com`) | **97%** (128 dari 132, 4 lolos); naik tepat 2 |
| Sama, tanpa xdns (HP kembali ke DHCP) | 5%. Angka sementara: tangkapan layar diambil saat tes masih berjalan (jumlah total kosong) |
| `canyoublockit.com`, tingkat eXtreme, dengan xdns | Pengamatan Master (tanpa tangkapan layar): popup, redirect, dan iklan video awal terblokir semua |
| Sama, tanpa xdns | Pengamatan Master: semua iklan dan redirect muncul, layar HP penuh iklan |

Target awal (≥ 90%) terpenuhi. Tingkat Simple di `canyoublockit.com` tidak dipakai karena iklannya dihosting di situsnya sendiri dan tidak bisa diblokir DNS. Empat domain yang masih lolos pada tes terakhir, dugaan dari log dan bukan bukti: `www.googletagmanager.com`, `deploy.mopinion.com`, `cdn.iubenda.com`, `cdn.fluidplayer.com`. Master memutuskan membiarkannya karena memblokirnya berisiko merusak situs yang sah.

Tangkapan layar ada di folder Unduhan Master dan tidak disalin ke repo.

### S2 — redirect Play Store / Shopee

- **Shopee: lulus.** Master membuka X (Twitter) yang banyak tautan iklan dan terjadi redirect ke aplikasi Shopee. Log HP pada 23:31:32–39 menunjukkan urutan: kueri X, lalu `cdn.acegimg.com`, `google.com`, **`s.shopee.co.id`** (host tautan pendek Shopee), lalu aplikasi Shopee terbuka. Setelah `xdns rule add block s.shopee.co.id`, tautan yang sama berakhir di "This site can't be reached" dan Shopee tidak terbuka. Log mencatat 5 kueri `s.shopee.co.id` dari HP setelah aturan dipasang (23:35:23, 23:37:02 tiga kali, 23:38:03), semuanya terblokir, tidak ada yang lolos. Itu sekitar 3 kali tekan, **belum 10 percobaan** seperti syarat awal; Master memutuskan cukup.
- **Play Store: tidak direproduksi.** Master tidak menemukan redirect ke Play Store, dan log tidak memuat kueri host Play Store yang mencurigakan. Dicatat sebagai "belum pernah muncul", bukan "sudah diblokir".
- **Konsekuensi yang disetujui Master:** tautan Shopee berbentuk `s.shopee.co.id/...` (tautan bagi atau afiliasi) tidak lagi terbuka di HP Master. `shopee.co.id` dan `api.shopee.co.id` tetap terjawab, jadi aplikasi Shopee yang dibuka manual tetap bekerja. Pembatalan: `xdns rule remove block s.shopee.co.id`.
- Gerbang G2b tidak terpicu: pemicunya domain terpisah, bukan skrip dari domain situs konten sendiri.

### S3 — SafeSearch tidak dipaksa

Dari sisi DNS: HP tidak pernah menanyakan `forcesafesearch.google.com` (0 kueri), dan `www.google.com` dijawab normal oleh hulu (3 kueri, semuanya lolos). `xdns check` juga lulus uji S3. Belum dilihat langsung pada pengaturan SafeSearch di HP.

## 3. Latensi dan statistik (log HP, 2026-10-18 23:17–23:40)

| Ukuran | Nilai |
|---|---|
| Total kueri dari HP | 1.040 (724 diblokir = 69,6%, 316 lolos). Persentase blokir tinggi karena jendela ini didominasi tes `adblock.turtlecute.org`; bukan angka penjelajahan biasa |
| Blokir per sumber | `hagezi-pro` 711; aturan kustom 13 (`s.shopee.co.id` 9, `ad.doubleclick.net` 2, `adservice.google.com` 2) |
| Jawaban dari hulu (tanpa cache), n = 262 | median 31 ms, p95 123 ms, maks 419 ms |
| Jawaban dari cache | 54 kueri |
| Jawaban blokir | dicatat 0 ms (diputuskan di dalam proses, tanpa hulu) |

Latensi hulu adalah DoH ke `1.1.1.1` dan `8.8.8.8`, dari PC ke internet; bukan jaringan HP ke PC. Catatan: jam di HP berformat 12 jam, jadi tangkapan layar "11:19" sama dengan 23:19 di log.

## 4. Temuan

1. **Konflik ICS terselesaikan tanpa menyentuh ICS.** Bind khusus ke alamat Wi-Fi PC berhasil walau ICS memegang UDP `0.0.0.0:53` (UDP dan TCP diuji).
2. **Apeks `doubleclick.net` dan host Google iklan tidak ada di HaGeZi PRO.** Daftar hanya memuat subdomain tertentu (mis. `g.doubleclick.net` di daftar pop-up), jadi `ad.doubleclick.net` dan `adservice.google.com` ditambahkan sebagai aturan kustom (keputusan Master).
3. **Chrome tidak menanyakan `use-application-dns.net` maupun `_dns.resolver.arpa`** (0 kueri keduanya). Reaksi Firefox terhadap nama khusus itu belum diuji; Chrome tidak memakainya.
4. **Chrome tidak melewati xdns:** tidak ada pencarian host DoH/DoT (`dns.google`, `cloudflare-dns.com`, `one.one.one.one`, dan sejenisnya) dari HP, dan pemblokiran efektif 95–97%.
5. **Jawaban `0.0.0.0` pada Chrome HP:** tidak ada gangguan yang teramati; permintaan ke domain terblokir gagal cepat ("This site can't be reached"). Tidak dibandingkan dengan NODATA atau NXDOMAIN.
6. **Aplikasi biasa tetap jalan:** WhatsApp, Instagram, X, Facebook, dan Shopee (dibuka manual) lolos; hanya host pelacak mereka yang diblokir (mis. `ubt.tracking.shopee.co.id`, `www.google-analytics.com`).

## 5. Belum terverifikasi

- Redirect ke Play Store (belum pernah terjadi).
- Firefox di HP; DNS IPv6 dari router sebagai jalur lolos (tidak diperiksa langsung; bukti tidak langsung: blokir efektif 95–97%).
- EDE (RFC 8914) pada jawaban blokir.
- Baseline "tanpa xdns" yang lengkap (tes sempat difoto sebelum selesai).
- xdns berjalan sebagai proses tersembunyi biasa, belum sebagai layanan: berhenti bila PC mati atau tidur, dan HP kehilangan DNS karena DNS 2 sengaja kosong. Jalan otomatis saat boot adalah Fase 5.
