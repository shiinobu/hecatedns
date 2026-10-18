# Laporan Fase 5 — Stabilisasi

*2026-10-19 | Claude (Sonnet 5.5; review penuh Fase 4 oleh agent Opus `xdns-reviewer`) | Status: selesai. Uji 7 hari (S4) dipindah ke Fase 6 di VPS atas keputusan Master. Satu hal masih terbuka: tugas boot belum dicoba lewat boot PC yang sebenarnya.*

## Hasil singkat

Backend `native` sudah lulus tes di HP (turtlecute 97%, "semua aman") dan sekarang menjadi backend server rumah. Review penuh Fase 4, yang terutang dari fase lalu, selesai: 0 Critical, 2 Important, 7 Minor; semua yang wajib diperbaiki dengan tes lebih dulu, dan dua temuan ternyata menyentuh bug nyata di kode kami sendiri. xdns kini mati dengan rapi pada Ctrl+C, memperingatkan bila hulu gagal beruntun, tidak kehilangan log query setelah disk penuh, menulis log operasional berbatas ukuran, dan jalan otomatis saat PC menyala lewat Task Scheduler.

## Keputusan Master untuk fase ini

| Keputusan | Akibat |
|---|---|
| `allowed_clients` tetap `192.168.1.0/24` | Keluarga dan teman di Wi-Fi rumah ikut memakai server. Batas per IP (laju, koneksi TCP, gerbang hulu) jadi lebih penting daripada untuk satu HP |
| Server rumah memakai `native` | Rollback satu baris: `backend: dnsproxy` di `xdns.yaml`, lalu restart |
| Batas laju `native` dinaikkan ke 200/detik, burst 500 | Halaman tes iklan mengirim ±255 query dalam setengah detik dan kehilangan satu |
| Uji 7 hari (P5-05, S4) dipindah ke Fase 6 di VPS | PC dimatikan tiap malam, jadi 7 hari tanpa putus tidak mungkin di sini. Fase 5 berakhir pada P5-00 sampai P5-04 dan P5-06 |
| Hemat kuota Opus | Satu agent Opus untuk review penuh; sisanya dikerjakan langsung. Review akhir fase ini memakai Sonnet |

## Yang dikerjakan

| Tugas | Hasil | Commit |
|---|---|---|
| P5-00 review penuh Fase 4 | 0 Critical, 2 Important, 7 Minor; laporan `.agents/state/reviews/phase-4-review.md` | `e10a006` (I1), `1db1d1d` (M1), `ed9e3d4` (M3, M4), `dcc5347` (ARPA privat), `28e0769` (M2) |
| P5-01 tes HP dengan `native` | 97%; 484 query HP dalam ±2,5 menit; hulu p50 29 ms, p95 88 ms; proses 41 MB (`test-report.md` bagian 5) | `70a484c`, `4361b64` (batas laju) |
| P5-02 jalan saat boot | Tugas Task Scheduler `xdns` dipasang dan diuji (bagian Bukti) | `scripts/autostart.ps1`, di commit penutup fase |
| P5-03 shutdown dan peringatan | Ctrl+C pada konsol asli di kedua backend; peringatan setelah 5 kegagalan beruntun | `1bd4396` |
| P5-04 batas disk | Audit; bug log query setelah disk penuh diperbaiki; log operasional berbatas ukuran | `73647aa`, `0ecb371` |
| P5-05 uji 7 hari | Dipindah ke Fase 6 (VPS) | — |
| Review akhir fase | 0 Critical, 1 Important, 5 Minor; I1, M1, M2 diperbaiki | `4b0754a`, `6b58d4b`, `9e81576` |
| P5-06 gate, dokumen, laporan | Dokumen ini; binary baru dipasang di server rumah (PID baru lewat tugas, hash terpasang = hash hasil bangun) | commit penutup fase |

## Bukti

**Gate** (2026-10-19, dijalankan penuh dua kali: pada `28e0769` sebelum review akhir fase, dan lagi pada kode final sesudah tiga perbaikan review, HEAD `9e81576`; angka di bawah dari jalan kedua; semua sepuluh LULUS pada kedua jalan):

| # | Perintah | Hasil |
|---|---|---|
| 1 | `gofmt -l .` | kosong |
| 2-3 | `go vet ./...`, `go build ./...` | bersih |
| 4 | `go test ./... -cover -count=1` | semua `ok`: `cmd/xdns` 92,9%, `backend` 100%, `conformance` 80,1%, `dnsproxybe` 95,4%, `native` 93,8%, `config` 95,5%, `engine` 97,2%, `lists` 86,7%, `qlog` 90,9%, `runlog` 96,0%, `selfcheck` 92,5%, `scripts/compare` 0% (tanpa tes) |
| 5 | `CGO_ENABLED=1 go test -race ./... -count=1` | semua `ok`, 0 balapan |
| 6-7 | `go mod verify`, `go mod tidy -diff` | bersih (golibs kini persyaratan langsung) |
| 8 | `govulncheck ./...` | 0 terdampak; 1 advisori pada modul yang tidak dipanggil |
| 9 | `xdns check -config configs/xdns.example.yaml` | lulus |
| 10 | cross-build `GOOS=linux GOARCH=amd64` | bersih |

**Ctrl+C anggun pada konsol Windows asli, kedua backend.** Proses dijalankan di jendela konsol baru, lalu `GenerateConsoleCtrlEvent` dikirim dari skrip pembantu: kode keluar 0, pesan "xdns berhenti", log query ditutup, port UDP dilepas.

**Peringatan hulu gagal beruntun.** Sembilan tes baru di `internal/engine/health_test.go`: lima kegagalan beruntun memicu satu peringatan per menit; jawaban hulu pertama sesudahnya mencatat pemulihan; cache hit, jawaban lokal, dan `context.Canceled` diabaikan; bebas balapan. Kedua backend memanggil `Failed` dan `After`, jadi satu penghitung melayani keduanya.

**Log query setelah disk penuh.** Tes dengan disk palsu (`writer_disk_test.go`) merah dulu (catatan tidak pernah sampai ke disk lagi, hitungan salah, baris terpotong menyatu dengan catatan berikutnya), hijau sesudah perbaikan. Tidak diuji pada drive penuh yang asli (butuh disk virtual sementara, perubahan sistem).

**Tugas Task Scheduler (P5-02).**
- Terpasang sesuai rancangan: pemicu startup dengan tunda 30 detik, S4U, hak terbatas, tersembunyi, tanpa batas waktu.
- Setelah `Start-ScheduledTask`: proses xdns di sesi 0 (tanpa jendela), memegang `192.168.1.99:53`; `example.com` menjawab; `ad.doubleclick.net` dan `s.shopee.co.id` menjawab `0.0.0.0`; `data\logs\xdns.log` bertambah dari 827 ke 1.654 byte.
- **Percobaan restart gagal:** (1) proses dimatikan dengan `Stop-Process`, tugas tidak menghidupkannya lagi dalam 120 detik; (2) tugas percobaan yang keluar dengan kode 1 dan "restart tiap 1 menit, 3 kali" hanya jalan sekali dalam 215 detik (seharusnya empat). Pengaturan restart-bila-gagal Task Scheduler tidak dapat diandalkan di sini.
- **Pemicu berulang tiap 1 menit** (dengan `IgnoreNew`) menggantikannya. Tugas percobaan dengan pemicu itu keluar dengan kode 1 dan dijalankan lagi tepat satu menit kemudian (01:31:27, 01:32:28). Pemicu itu dipasang ke tugas `xdns` yang asli tanpa menghentikan prosesnya.
- Batas durasi pengulangan: nilai terbesar (`TimeSpan.MaxValue`) ditolak Task Scheduler (`0x80041318`), jadi dipakai 365 hari; hitungannya mulai lagi pada tiap boot.

## Temuan dan bug yang ditemukan

1. **Tenggat bootstrap habis di server pertama (I1).** Server bootstrap dan hulu DoH berbagi satu tenggat; bila server bootstrap pertama diam, yang kedua tidak pernah ditanya. Percobaan perbaikan pertama gagal karena net/http membuang tenggat request saat dial (sejak Go 1.23). Diperbaiki dengan anggaran waktu sendiri di fungsi dial yang dibagi per server dan per alamat.
2. **Query ke hulu polos tidak dibatalkan saat shutdown.** Ditemukan tes server baru yang ditambahkan karena mutasi `stopQueries` lolos semua tes: miekg/dns menghormati tenggat sebuah konteks tetapi bukan pembatalannya, jadi shutdown dengan hulu yang diam menunggu seluruh batas waktu. Sekarang koneksi ditutup saat konteks dibatalkan (`context.AfterFunc`).
3. **Reverse lookup alamat privat diteruskan ke hulu oleh `native`**, untuk semua klien, termasuk `nslookup 192.168.1.99` dari Windows. Alasan "baru relevan di Fase 6" di dokumen lama keliru. Kini kedua backend menjawab zona RFC 6303 secara lokal.
4. **RA pada jawaban REFUSED.** `native` selalu mengisi bit RA, bertentangan dengan engine dan dnsproxy. Diperbaiki; suite kesesuaian memeriksanya.
5. **Log query mati sampai tengah malam setelah satu momen disk penuh.** `bufio.Writer` menyimpan galat pertamanya dan menolak semuanya sesudah itu, sementara catatan yang hilang tetap dihitung tertulis.
6. **Tes yang kadang gagal (1 dari 25).** Windows mengecualikan blok 100 port TCP (Hyper-V/WinNAT); 100 percobaan port bisa jatuh pada dua blok bersebelahan. Pembantu tes kini mencoba sampai 1.000 port.
7. **Skrip `autostart.ps1`.** `$PSScriptRoot` kosong di dalam blok `param` pada Windows PowerShell 5.1 (perhitungan `-Root` dipindah ke badan skrip); `Set-ScheduledTask` menolak pengaturan restart yang dihapus, jadi tugas yang sudah terpasang masih mencatat `RestartCount=999` (tidak berpengaruh, terbukti di atas).
8. **Satu kegagalan `-race` yang belum terjelaskan** pada `go test -race ./internal/backend/native -count=2` (6,0 detik), tidak pernah terulang dalam 40+ jalan sesudahnya (putaran paket dan seluruh suite). Satu-satunya asersi yang peka waktu dan ditambahkan di fase ini (waktu shutdown) dilonggarkan dari 2 ke 4 detik. Dalam gate hari ini, dua jalan penuh (biasa dan race) lulus.

## Review penuh Fase 4 (P5-00)

Oleh `xdns-reviewer` (Opus, konteks baru, hanya baca; sekitar 16 menit), rentang `7fd1ac0..4c7220e`; laporan lengkap dengan tabel status perbaikan ada di `.agents/state/reviews/phase-4-review.md`.

| Temuan | Tingkat | Perbaikan |
|---|---|---|
| I1 tenggat bootstrap | Important | `e10a006`: `TestDoH_HostnameStillWorksWhenTheFirstBootstrapServerIsDead`, `TestShareOfDeadline` |
| I2 tabel pembatas laju penuh melepas sumber baru | Important (Minor di LAN rumah) | Ditunda ke Fase 6 |
| M1 RA pada REFUSED | Minor | `1db1d1d`: asersi di kasus suite `aclRefuses` |
| M2 pesan dengan badan rusak dijawab FORMERR oleh miekg | Minor | Tes penjaga `28e0769` (jawaban tidak lebih besar dari request); dokumen dikoreksi |
| M3 `serve` 61 baris | Minor | `ed9e3d4`: `awaitStop`, `shutdownServers` |
| M4 dua mutasi lolos semua tes | Minor | `ed9e3d4`: kedua mutasi kini tertangkap (mutasi diulang) |
| M5 klaim dokumen salah (FORMERR, "5 detik") | Minor | Dokumen dikoreksi di commit ini |
| M6, M7 | Minor, opsional | Terbuka (Fase 6, tugas 10) |
| ARPA privat | Temuan tambahan | `dcc5347`: kasus suite `privateReverse` merah di `native` dulu |
| Slot TCP sesudah REFUSED | Temuan tambahan | Ditunda ke Fase 6 |

## Review akhir fase (hemat)

Oleh `xdns-reviewer` yang dijalankan di model Sonnet (konteks baru, hanya baca, sekitar 8 menit, tiga percobaan di salinan scratch), rentang `4c7220e..28e0769` ditambah `scripts/autostart.ps1`. Master meminta kuota Opus dihemat, jadi ini review hemat, bukan review penuh; review penuh yang menyerang pendengar DoT dijadwalkan sebelum server dibuka (Fase 6, tugas P6-13). Laporan: `.agents/state/reviews/phase-5-review.md`. Hasil: **0 Critical, 1 Important, 5 Minor**.

| Temuan | Tingkat | Keputusan |
|---|---|---|
| I1 galat yang mengakhiri server tidak masuk `xdns.log` (tugas tersembunyi yang gagal mengikat alamat saat boot tidak meninggalkan jejak sama sekali) | Important | Diperbaiki `4b0754a`, tes merah dulu |
| M1 rotasi `runlog` gagal: pegangan tertutup membuat log mati sampai restart; `Get-Content -Wait` pada `xdns.log` membuat semua baris hilang | Minor | Diperbaiki `6b58d4b`, empat tes merah dulu (cakupan `runlog` 86,0% jadi 96,0%) |
| M2 `qlog` menghitung catatan yang sudah di-flush sendiri oleh bufio sebagai dibuang (330 di disk, `Written()` 0) | Minor | Diperbaiki `9e81576`, tes merah dulu |
| M3 peringatan hulu: koneksi yang naik turun membanjiri log; `errBusy` (batas lokal) terhitung kegagalan hulu; hulu yang menjawab SERVFAIL tidak pernah memicu peringatan | Minor | Ditunda: (a) bertabrakan dengan aturan yang sengaja diuji ("outage baru diumumkan seketika"), banjir dibatasi berkas 5 MB; (b)(c) butuh engine membedakan batas lokal dari kegagalan hulu |
| M4 celah pembaruan hilang antara `failed` dan `answered` | Minor | Ditunda bersama M3 (struct yang sama); dampaknya paling banyak satu baris peringatan basi |
| M5 skrip: `-Root` relatif, penghentian paksa saat `remove`, jendela 365 hari, tugas tersembunyi | Minor | `-Root` dibuat absolut (cek sintaks lulus); sisanya dicatat di `architecture.md` §12 |

Yang dinyatakan benar oleh reviewer: penghitung kegagalan tidak terpicu atau terhapus oleh cache hit, jawaban lokal, ANY, atau `context.Canceled`; pemulihan `qlog` tidak menggandakan, menghilangkan tanpa hitung, atau menggabung baris; `Tee` tidak pernah menghentikan server; semua perbaikan review Fase 4 di `native` sesuai tesnya; alur `install`, `remove`, dan jalan dua kali pada skrip tidak punya kesalahan logika. Yang tidak bisa diverifikasi reviewer: skrip tidak dijalankan (bukti ada di bagian Bukti di atas), `dnsproxybe` belum dipastikan membungkus `context.Canceled` dengan `%w` saat shutdown (paling banyak satu baris peringatan palsu pada backend itu), dan disk penuh pada NTFS asli.

## Keputusan bernomor

1. **Uji 7 hari di VPS, bukan di PC** (Master). Risiko: S4 baru terukur di Fase 6; sampai itu, kestabilan jangka panjang `native` hanya terbukti lewat satu sesi tes HP dan sekitar satu jam jalan tersembunyi di rumah sejak 00:29.
2. **Tugas boot memakai hak biasa dan pemicu berulang.** Risiko: pemicu berulang membuka kembali xdns paling lama satu menit setelah mati, bukan seketika; dan boot sungguhan belum diuji.
3. **Log operasional tanpa kunci konfigurasi baru** (5 MB + satu cadangan, mengikuti `log.dir` dan `log.enabled`). Risiko: bila `log.enabled: false`, tugas tersembunyi tidak meninggalkan jejak sama sekali.
4. **Peringatan hulu gagal beruntun ada di engine**, bukan di tiap backend: satu penghitung untuk dua backend.
5. **Aturan reverse lookup privat disalin dari dnsproxy** lewat fungsi golibs yang sama, demi kesamaan perilaku dan privasi.
6. **Pekerjaan dilakukan di worktree git sementara** selagi reviewer membaca repo (tes merah tidak boleh merusak gate si reviewer), lalu di-fast-forward ke `main`.

## Belum terverifikasi

- Boot PC sungguhan dengan tugas Task Scheduler, termasuk Wi-Fi yang terlambat memberi `192.168.1.99`.
- Disk penuh pada drive asli (hanya disk palsu).
- Perilaku DoH nyata dan hulu IPv6 dalam beban lama; galat baca UDP Windows selain `WSAEMSGSIZE`; ketepatan perkiraan memori cache di bawah beban nyata.
- HP belum dites ulang dengan binary Fase 5 (yang berubah untuk HP hanya jawaban PTR).
- Uji 7 hari (S4): Fase 6.
- Dari Fase 3 yang masih terbuka: Play Store, Firefox di HP, jalur DNS IPv6 router, EDE, baseline lengkap.

## Ditunda

I2 dan kunci IPv6 /64; slot TCP sesudah REFUSED; batas TCP per IP dan pembatas laju untuk `dnsproxy`; deduplikasi query untuk `native`; DoT sebagai hulu; M6; M7; dari review akhir fase M3 dan M4 (peringatan hulu yang naik turun, `errBusy` yang terhitung kegagalan hulu, satu struct untuk penghitung dan jeda). Daftar dan urutannya ada di `roadmap.md` Fase 6 tugas 8-10 dan `.agents/state/PHASE-6-TASKS.md`.

## Serah terima ke Fase 6

- Mulai dari gerbang G3: pilih VPS, biaya, dan subdomain; tidak ada yang disewa tanpa persetujuan Master.
- Sebelum server dibuka ke internet: seluruh utang di bagian "Ditunda" yang menyangkut backend yang dipakai di VPS, lalu review penuh yang menyerang pendengar DoT.
- Setelah PC menyala berikutnya, periksa bahwa xdns jalan sendiri (`powershell -File scripts/autostart.ps1 status`).
