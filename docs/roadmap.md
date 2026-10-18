# xdns — Roadmap

*Versi 1.4 | 2026-10-19 | Status: **Fase 0, Fase 1, Fase 2, dan Fase 3 selesai; Fase 4 selesai di sisi kode dan tes** (laporan: `phase-0-report.md`, `phase-1-report.md`, `phase-2-report.md`, `phase-3-report.md`, `phase-4-report.md`, `test-report.md`); tinggal tes HP dengan `backend: native` (menunggu Master), lalu Fase 5.*

Roadmap ini adalah alur kerja bertahap. Desain teknisnya ada di `architecture.md`, dasar risetnya di `research.md`. Kode `D#`, `S#`, `R#` merujuk ke tabel keputusan, kriteria sukses, dan risiko di `architecture.md`.

---

## Cara kerja

- **Kewenangan.** Master memberi kewenangan penuh (2026-10-18): saya memutuskan semua hal teknis. Saya bertanya hanya bila (a) ragu pada hal yang mahal diubah nanti, (b) di gerbang keputusan di bawah, atau (c) menyangkut uang atau risiko keamanan. Master boleh membatalkan keputusan apa pun kapan saja.
- **Satu agent.** Saya mengerjakan sendiri langsung di sesi ini. Agent tambahan hanya sebagai peninjau (kode dan keamanan) di akhir fase, sesuai aturan proyek.
- **Siklus tiap tugas.** Tulis tes dulu (merah) → implementasi minimal (hijau) → rapikan. Commit kecil dengan format `feat:`, `fix:`, `test:`, `docs:`, `refactor:`, `chore:`.
- **Urutan kerja (aturan Master, 2026-10-18).** Semua tugas satu fase dikerjakan dulu. Peninjauan oleh agent dilakukan sekali per fase, setelah semua tugas selesai dan sebelum masuk fase berikutnya. Dokumen diperbarui di akhir, setelah itu.
- **Penutup tiap fase.** `gofmt -l .` kosong, `go vet ./...`, `go test ./... -cover` hijau, `govulncheck` tanpa kerentanan yang terdampak; peninjauan kode (`code-reviewer`) dan, untuk kode yang menghadap jaringan, `security-reviewer`; perbaikan temuan Critical dan Important dengan TDD; pembaruan dokumen; ringkasan singkat untuk Master.
- **Rencana tugas.** Sejak Fase 1, tugas diturunkan langsung dari roadmap ini tanpa dokumen rencana terpisah (rencana Fase 0 disimpan sebagai arsip di `docs/superpowers/plans/`). Catatan kerja ada di ledger lokal `.superpowers/` yang tidak di-commit.
- **Perubahan desain** dicatat di bagian "Riwayat perubahan" di bawah dan di `architecture.md`.

## Ringkasan fase

| Fase | Nama | Hasil utama | Usaha (perkiraan kasar) | Biaya |
|---|---|---|---|---|
| 0 | Fondasi | Repo git, `go.mod`, kerangka paket, pemuat konfigurasi | ½–1 sesi | Rp0 |
| 1 | Engine inti + backend `dnsproxy` | Forwarder DoH jalan di `127.0.0.1:5300`, aturan kustom, log, CLI | 1–2 sesi | Rp0 |
| 2 | Daftar + CNAME + nama khusus | HaGeZi PRO/Pop-Up + ABPindo termuat, pemeriksaan CNAME, HTTPS/SVCB, `xdns check` | 1–2 sesi | Rp0 |
| 3 | **Tes di HP (Wi-Fi rumah)** | S1–S3 terukur, penyetelan | 1 sesi + waktu tes Master | Rp0 |
| 4 | Backend `native` + suite kesesuaian | Ganti backend dengan satu baris | 2 sesi | Rp0 |
| 5 | Stabilisasi | Jalan otomatis saat boot, uji 7 hari (S4) | 1 sesi + 7 hari | Rp0 |
| 6 | VPS + DoT | HP terlindungi di luar rumah lewat Private DNS | 1–2 sesi | ≤ Rp50 ribu/bulan |
| 7 | Opsional | UI web lokal, daftar tambahan, Plan B APK | menyesuaikan | menyesuaikan |

Perkiraan total sampai HP bisa dites (Fase 0–3): sekitar 4–6 sesi kerja. Sampai kedua backend siap (Fase 4): sekitar 2 sesi lagi. Ini perkiraan, bukan janji. Urutan sengaja menaruh tes di HP (Fase 3) sebelum backend kedua, supaya Master cepat melihat hasil nyata dan bisa memutuskan perlu tidaknya Plan B.

---

## Fase 0 — Fondasi

**Tujuan.** Kerangka proyek yang bisa dibangun dan diuji.

**Tugas**
1. `git init`; `.gitignore` (`data/`, `*.exe`, `*.pem`, `*.key`); commit awal berisi `architecture.md`, `roadmap.md`, `research.md`.
2. `go mod init xdns`; `go 1.26.9`; dependensi terkunci: `github.com/miekg/dns v1.1.73`, `github.com/AdguardTeam/dnsproxy v0.86.0`, `gopkg.in/yaml.v3`; `go mod tidy` dan `go mod verify`.
3. Kerangka direktori sesuai `architecture.md` §4.
4. `internal/config`: tes dulu (konfigurasi sah; kunci tak dikenal; CIDR salah; URL daftar bukan `https`; backend atau mode tak dikenal; TTL negatif), lalu pemuat ketat dan validasi.
5. `cmd/xdns`: subperintah `check` yang memuat dan memvalidasi konfigurasi; subperintah lain berupa kerangka yang mencetak "belum diimplementasi" dan keluar dengan kode ≠ 0.
6. `configs/xdns.example.yaml` (dari `architecture.md` §10) dan `README.md` singkat.

**Selesai bila.** `go build ./...`, `go vet ./...`, `gofmt -l .` (kosong), `go test ./... -cover` hijau (`config` ≥ 80%); `xdns check -config configs/xdns.example.yaml` menyatakan konfigurasi valid.

**Catatan.** Toolchain Go 1.26.8 diunduh otomatis saat build pertama (butuh internet, R6). Tidak ada instalasi lain.

---

## Fase 1 — Engine inti + backend dnsproxy

**Tujuan.** Server yang meneruskan query lewat DoH, menegakkan aturan kustom, dan mencatat log, memakai backend `dnsproxy`.

**Tugas**
1. `engine/matcher`: tes tabel (cocok persis, subdomain, label di tengah, entri satu label ditolak, huruf besar, titik akhir) → `DomainSet` dengan snapshot atomik.
2. `engine/rules`: parser `rules.txt`, prioritas `allow` > `block`, muat ulang berdasarkan waktu ubah; tes termasuk baris rusak.
3. `engine/response`: empat mode jawaban blokir dengan SOA sintetis; tes per tipe (A, AAAA, HTTPS, TXT).
4. `engine/engine`: `Before`, `After`, `Failed`, fail-open; tes konkurensi.
5. `qlog`: penulis JSONL (saluran berpenyangga, rotasi harian, retensi) dan pembaca; tes.
6. `backend`: antarmuka dan registri. `dnsproxybe`: pemetaan konfigurasi dan handler yang memanggil engine. Verifikasi dari pkg.go.dev: tanda tangan handler, upstream DoH berupa IP (`architecture.md` §18-1,2).
7. `conformance` v1 (blokir, lolos, `allow` mengalahkan `block`, ACL, log tertulis) dijalankan pada backend `dnsproxy`.
8. CLI: `run`, `log`, `rule`.
9. `engine/acl`: ACL `allowed_clients` sebagai langkah pertama `Before` (REFUSED di luar daftar); tes tabel IPv4/IPv6, batas CIDR, daftar satu CIDR. dnsproxy tidak punya ACL klien, jadi ini milik engine (`architecture.md` §5.3).
10. Penguatan konfigurasi dari tinjauan Fase 0: sumber tanpa `enabled` dianggap aktif; `rules.file` dan `log.dir` wajib terisi; `listen` unik dan port ≠ 0; TTL dibatasi sebelum konversi ke uint32; `run` mencetak `Warnings()`; tambahkan `/xdns.yaml` ke `.gitignore`.
11. Peninjauan fase (sekali, setelah semua tugas selesai): `code-reviewer` dan `security-reviewer` (ACL, penanganan masukan).

**Selesai bila.**
- `xdns run` mendengarkan di `127.0.0.1:5300` dan `scripts/dnsquery.ps1 example.com -Port 5300` menjawab lewat DoH (`nslookup` Windows menerima `-port` tetapi mengabaikannya);
- `xdns rule add block contoh.test` membuat nama itu menjawab `0.0.0.0` dalam ≤ 10 detik tanpa restart (S5);
- log memuat kedua query; klien di luar `allowed_clients` dijawab REFUSED; suite kesesuaian hijau; cakupan `engine` ≥ 80%.

---

## Fase 2 — Daftar, CNAME, nama khusus

**Tujuan.** Pemblokiran sungguhan memakai daftar HaGeZi dan ABPindo, plus pemeriksaan CNAME dan penutupan celah HTTPS/SVCB.

**Tugas**
1. `lists/parser`: format `domains` dan `hosts`; tes tabel dan fuzz; hitung baris yang ditolak.
2. `lists/fetch`: unduh HTTPS, ETag, batas waktu dan ukuran, tulis atomik, metadata; diuji dengan `httptest`.
3. `lists/manager`: muat cache saat start → segarkan di latar belakang → snapshot baru → tukar atomik; penjadwal 24 jam dengan jitter.
4. Sambungkan ke engine sebagai daftar `block`; prioritas lengkap.
5. `engine/cname`: pemeriksaan rantai CNAME di `After`; tes untuk target terblokir, target diizinkan, rantai panjang, tanpa CNAME.
6. HTTPS/SVCB → NODATA untuk nama terblokir; `engine/special` (canary Firefox, DDR) dengan saklar konfigurasi.
7. `selfcheck` dan `xdns check` (hulu terjangkau, SafeSearch, domain contoh tiap daftar → `0.0.0.0`); `xdns lists status|update`.
8. Benchmark pencarian dan memori (`architecture.md` §7).
9. Peninjauan fase (sekali, setelah semua tugas selesai).

**Selesai bila.**
- ketiga daftar termuat (± 249 ribu entri sebelum de-duplikasi) dan domain contoh tiap daftar menjawab `0.0.0.0`;
- tes CNAME dan HTTPS/SVCB hijau;
- `xdns check` LULUS, termasuk S3 (tidak ada `forcesafesearch.google.com`);
- p99 pencarian < 50 µs dan memori < 150 MB, atau angka aktual dicatat beserta rencana optimasi.

**Gerbang G1.** Setelah PC dipakai beberapa jam, saya meninjau statistik blokir di log dan menyesuaikan daftar (mis. menambah `allow` untuk salah blokir) sebelum lanjut.

---

## Fase 3 — Tes di HP (Wi-Fi rumah) — selesai 2026-10-18

**Tujuan.** Mengukur kriteria S1–S3 pada perangkat nyata dan menyetel hasilnya. **Hasil:** S1 95% lalu 97% pada `adblock.turtlecute.org` (target ≥ 90%); S3 aman dari sisi DNS; S2 lulus untuk Shopee (`s.shopee.co.id` diblokir), redirect Play Store belum pernah muncul. Alamat PC menjadi `192.168.1.99` (bukan `.10`). Lihat `phase-3-report.md` dan `test-report.md`. Teks di bawah adalah rencana awal.

**Gerbang G2 (awal fase): UDP 53 di PC ini.** Layanan Internet Connection Sharing (ICS) memegang UDP `0.0.0.0:53` (`architecture.md` §12). Android tidak bisa menentukan port DNS, jadi server harus bisa memakai UDP 53 di `192.168.1.10`. Saya memeriksa dulu apakah bind spesifik berhasil. Bila tidak, opsinya: hentikan ICS (bila Master tidak memakai Mobile Hotspot, Hyper-V, atau WSL), pakai perangkat lain untuk server rumah, atau jalankan tes lewat VPS lebih awal. **Di sini saya akan bertanya ke Master.**

**Tugas**
1. Selesaikan konflik UDP 53; ubah `listen` ke `192.168.1.10:53`; tambah aturan Windows Defender Firewall (masuk UDP dan TCP 53, profil Private).
2. Reservasi alamat PC di router agar IP-nya tetap.
3. Di HP: Wi-Fi → IP statis → DNS 1 = IP PC, DNS 2 kosong; Private DNS = Mati; periksa pengaturan Secure DNS di Chrome dan DoH di Firefox.
4. Ukur baseline di adblock.turtlecute.org dan canyoublockit.com sebelum xdns, lalu sesudahnya.
5. Picu redirect ke Play Store/Shopee → `xdns log -around` → tambah aturan atau daftar → ulangi 10 kali.
6. Pastikan query HP benar-benar masuk ke log (waspadai DNS IPv6 dari router dan DoH di browser).
7. Catat hasil S1–S3, angka latensi, dan temuan di `docs/test-report.md`.

**Selesai bila.** S1, S2, dan S3 terukur dan tercatat.

**Gerbang G2b (akhir fase).** Bila S2 gagal, saya sampaikan terus terang batas DNS, merekomendasikan pelengkap Firefox + uBlock Origin, dan membahas Plan B (`architecture.md` §16). Bila lulus, lanjut.

---

## Fase 4 — Backend native + suite kesesuaian — selesai di sisi kode 2026-10-19

**Tujuan.** Backend kedua (pendekatan 1) dengan perilaku keputusan yang sama, bisa ditukar lewat konfigurasi.

**Hasil.** Suite kesesuaian (15 kasus) hijau pada kedua backend dari satu tabel; `backend: native` cukup satu baris; 2.000 query sampel memberi keputusan engine identik (0 beda). Diubah dari rencana atas keputusan Master: **DoT sebagai hulu ditunda** (DoH, UDP, dan TCP polos ada), dan **review fase dibuat singkat** (review penuh diwajibkan di Fase 5, dan paling lambat sebelum Fase 6). Tugas 7 (tes HP dengan `native`) menunggu Master. Lihat `phase-4-report.md`. Teks di bawah adalah rencana awal.

**Tugas**
1. `native/cache`, `native/upstream` (DoH, DoT, plain, failover); tes dengan hulu palsu.
2. `native/server` (UDP dan TCP), `native/handler`, `native/ratelimit`.
3. Daftarkan di registri; `backend: native` berfungsi.
4. Jalankan `conformance` pada **kedua** backend dari satu tabel; samakan semua perbedaan.
5. Skrip pembanding: 1.000 domain sampel (dari log dan daftar) → bandingkan keputusan blokir/lolos kedua backend.
6. Peninjauan keamanan: bind, ACL, batas laju, jawaban hulu hanya diterima bila ID dan pertanyaan cocok.
7. Tes singkat dari HP dengan backend `native` (ulang S1–S2).

**Selesai bila.** Suite identik hijau di dua backend; mengganti backend hanya perlu satu baris; keputusan blokir/lolos pada sampel sama (selisih, bila ada, dijelaskan).

---

## Fase 5 — Stabilisasi

**Tujuan.** Berjalan sendiri tanpa dijaga.

**Tugas**
1. Jalan otomatis saat boot lewat Task Scheduler (hak tertinggi, mulai saat startup, restart bila gagal).
2. Shutdown rapi pada sinyal; peringatan di log bila hulu gagal beruntun.
3. Retensi dan rotasi log; ukuran `data/` terbatas.
4. Uji 7 hari: catat memori, jumlah query, galat; tanpa restart manual.

**Selesai bila.** S4 terpenuhi.

---

## Fase 6 — VPS + DoT

**Tujuan.** HP terlindungi di luar rumah (data seluler) lewat Private DNS bawaan Android.

**Gerbang G3 (awal fase).** Master menyetujui biaya dan penyedia. Saya memeriksa harga terbaru (target ≤ Rp50 ribu/bulan, wilayah dekat Indonesia) dan mengajukan satu rekomendasi.

**Tugas**
1. Pesan VPS Linux kecil; pengerasan: pengguna non-root, firewall (hanya DoT dan SSH), pembaruan otomatis.
2. Nama domain: subdomain gratis yang menunjuk ke VPS.
3. Sertifikat TLS (Let's Encrypt) dengan pembaruan otomatis.
4. Listener DoT (853) pada kedua backend; identifikasi klien lewat nama host rahasia (SNI) dan batas laju; **port 53 publik ditutup**.
5. Layanan systemd, log, cadangan konfigurasi.
6. HP: Private DNS = nama host rahasia; uji di Wi-Fi lain dan data seluler (ulang S1–S2).
7. Uji bahwa server bukan resolver terbuka (klien tanpa nama host benar ditolak).

8. **Prasyarat dari tinjauan Fase 1 (I1)**: kolam slot TCP terpisah dari UDP (satu host di LAN dapat menahan 256 koneksi TCP menganggur dan menghentikan UDP; `proxy.Proxy` kedua khusus TCP atau batas koneksi per IP di depan dnsproxy); pembatas laju per IP klien; tinjau SO_REUSEPORT di Linux/Android (dnsproxy mengaktifkannya sehingga instance kedua dapat memakai port yang sama tanpa galat).

**Selesai bila.** HP di data seluler memblokir iklan dengan kriteria yang sama dan server tidak menjawab klien yang tidak berwenang.

---

## Fase 7 — Opsional (hanya bila Master mau)

- UI web lokal: lihat log, tombol blok/izinkan (lewat alur skill ui-consistency: finding-patterns → planning → implementing → verifying).
- Daftar tambahan (NRD/DGA, DoH-bypass), sintaks adblock dasar, EDE, statistik.
- **Plan B (APK):** switch Private DNS (ringan) atau acuan BlockAds untuk filter lokal (`research.md` §9).

---

## Gerbang keputusan

| Gerbang | Kapan | Isi |
|---|---|---|
| G1 | Akhir Fase 2 | Tinjau statistik blokir di PC; sesuaikan daftar |
| G2 | Awal Fase 3 | Konflik UDP 53 / ICS; tanya Master |
| G2b | Akhir Fase 3 | S2 gagal → pelengkap Firefox + uBO, bahas Plan B |
| G3 | Awal Fase 6 | Pilih VPS, domain, dan biaya |

## Pertanyaan untuk Master (nanti, bukan sekarang)

- **Awal Fase 3:** Apakah Master memakai Mobile Hotspot, Hyper-V, atau WSL di PC ini? Bisakah Master membuka pengaturan router (reservasi IP)? Browser utama di HP apa?
- **Awal Fase 6:** persetujuan biaya dan penyedia VPS; nama subdomain yang diinginkan.

---

## Riwayat perubahan

| Tanggal | Perubahan |
|---|---|
| 2026-10-18 | Versi 1.0 dibuat. Urutan fase menaruh tes di HP (Fase 3) sebelum backend `native` (Fase 4) |
| 2026-10-18 | Fase 0 selesai. `yaml.v3` dipasang di Tugas 2 (bukan Tugas 1) karena baru dipakai di sana; nilai bawaan ada di `config.Default()`; `.gitattributes` memaksa akhir baris LF agar `gofmt` bersih di Windows |
| 2026-10-18 | Peninjauan Fase 0 oleh agent: 0 Critical, 4 Important, semuanya diperbaiki dengan TDD (dokumen YAML ganda ditolak; argumen tak terduga pada `check` ditolak; `allowed_clients` wajib terisi; nama sumber daftar harus aman sebagai nama berkas). `govulncheck` menemukan 6 kerentanan pustaka standar, diperbaiki dengan Go 1.26.9 dan x/net v0.60.0 (0 terdampak). ACL dipindah ke engine karena dnsproxy tidak punya ACL klien. Aturan baru dari Master: tugas dulu dan dokumen di akhir; peninjauan agent sekali per fase sebelum fase berikutnya. Master memindahkan dokumen ke `docs/` |
| 2026-10-18 | Fase 1 selesai (laporan: `phase-1-report.md`). Tinjauan oleh agent Opus berkonteks baru yang menjalankan binary dengan beban ala penyerang: 0 Critical, 4 Important (I2, I3, I4 diperbaiki dengan TDD; I1, kolam TCP bersama UDP, diterima untuk LAN dan menjadi prasyarat Fase 6), 12 Minor. Perubahan desain yang tercatat: prioritas aturan kustom di atas daftar; engine menjawab FORMERR/NOTIMP sendiri; batas harian log `log.max_mb_per_day`; `enabled:` yang dihilangkan berarti aktif; `nslookup -port` diganti `scripts/dnsquery.ps1` |
| 2026-10-18 | Fase 3 selesai (laporan: `phase-3-report.md`, `test-report.md`). Tidak ada perubahan kode Go. G2 terjawab: ICS tidak perlu dihentikan (bind khusus `192.168.1.99:53` berhasil), dua aturan firewall sempit, PC dipesan di router pada `192.168.1.99`, HP memakai `192.168.1.150`. S1: 95% lalu 97% (126 lalu 128 dari 132) pada `adblock.turtlecute.org`; `canyoublockit.com` eXtreme terblokir semua menurut pengamatan Master. S2: pemicu redirect Shopee adalah `s.shopee.co.id` (diblokir); Play Store belum pernah muncul. Aturan kustom ditambah: `ad.doubleclick.net`, `adservice.google.com`, `s.shopee.co.id`. `AGENTS.md` ditambahkan di root |
| 2026-10-18 | Fase 2 selesai (laporan: `phase-2-report.md`). Ketiga daftar bawaan diunduh dan termuat (`hagezi-pro` 202.297, `hagezi-popups` 47.668, `abpindo` 124 — total 250.089 entri); pemeriksaan rantai CNAME di `After` (termasuk pada cache hit); nama khusus (`use-application-dns.net` NXDOMAIN, `_dns.resolver.arpa` NODATA); `selfcheck` + `xdns check` (termasuk S3 SafeSearch dan deteksi loop Q3) + `xdns lists status|update`. Benchmark 250 ribu entri: rata-rata 215–250 ns/op (0 alokasi), p99 2,01 µs (< 50 µs), memori proses hidup WorkingSet 71,74–74,20 MiB (< 150 MB). Tinjauan berkonteks baru: 0 Critical, 2 Important (I1 failover bootstrap DNS pada pengunduh daftar, I2 pemilihan sampel per daftar di `selfcheck`), 8 Minor — seluruhnya diperbaiki dengan TDD |
| 2026-10-19 | Fase 4 selesai di sisi kode dan tes (laporan: `phase-4-report.md`); versi 1.4. Backend `native` (cache, hulu DoH/UDP/TCP dengan failover, server UDP+TCP, handler, pembatas laju) lulus suite 15 kasus yang sama dengan `dnsproxy`; 2.000 query sampel: 0 beda keputusan. Atas keputusan Master: DoT sebagai hulu ditunda, review fase dibuat singkat (review penuh wajib di Fase 5 dan paling lambat sebelum Fase 6). Temuan: satu datagram UDP besar menghentikan loop UDP miekg di Windows (diperbaiki, jadi kasus suite). Tes HP dengan `native` menunggu Master |

