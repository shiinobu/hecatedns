# Laporan Fase 3 — Tes di HP (Wi-Fi rumah)

*2026-10-18 | Status: selesai. Kriteria S1 dan S3 lulus; S2 lulus untuk Shopee dan redirect Play Store belum pernah muncul. Tidak ada perubahan kode Go di fase ini, jadi tidak ada tinjauan agent (lihat bagian Tinjauan)*

## Hasil

xdns berjalan di `192.168.1.99:53` pada PC Master dan HP RedMagic 8S Pro memakainya sebagai DNS Wi-Fi. Pada `adblock.turtlecute.org` skor naik ke **95%** (126 dari 132 terblokir) dan **97%** setelah dua aturan kustom; pada `canyoublockit.com` tingkat eXtreme Master mengamati semua iklan, popup, dan redirect terblokir (tanpa xdns, layar penuh iklan). Redirect ke Shopee dari iklan di X dilacak ke `s.shopee.co.id`, diblokir, dan tidak terbuka lagi. Detail ukuran dan latensi ada di `test-report.md`.

| Tugas | Hasil | Bukti |
|---|---|---|
| P3-01 Gerbang G2 dan UDP 53 | Bind khusus `192.168.1.99:53` (UDP dan TCP) berhasil walau ICS memegang `0.0.0.0:53`; ICS tidak disentuh. Dua aturan firewall sempit (opsi A, disetujui Master) | `test-report.md` bagian 1 |
| P3-02 Reservasi IP router | Master memesan PC di ZTE F663NV3a; alamat PC berganti dari `192.168.1.10` ke `192.168.1.99` setelah `ipconfig /release` dan `/renew` | `Get-NetIPAddress` |
| P3-03 Pengaturan DNS di HP | IP statis `192.168.1.150`, DNS 1 `192.168.1.99`, DNS 2 kosong, DNS Pribadi dan "DNS aman" Chrome mati | log HP muncul sejak 23:17:12 |
| P3-04 Pengukuran S1 | 95% dengan xdns; 97% setelah dua aturan; baseline tanpa xdns 5% (sementara) | tangkapan layar Master; `test-report.md` bagian 2 |
| P3-05 Kueri HP masuk log | Kueri HP (A dan HTTPS) masuk log; tidak ada pencarian host DoH; S3 aman dari sisi DNS. DNS IPv6 router dan Firefox tidak diuji | `data/logs/queries-20261018.jsonl` |
| P3-06 Pelacakan redirect | Shopee: `s.shopee.co.id` diblokir, tes ulang lulus. Play Store: tidak direproduksi | log 23:31:32–39 dan 23:35–23:38 |
| P3-07 Gerbang dan laporan | Gerbang hijau, laporan ini dan `test-report.md` | bagian di bawah |

## Bukti verifikasi

Dijalankan pada 2026-10-18 di HEAD `1e740df`, lalu tidak ada perubahan kode Go sampai penutupan fase (`git diff --name-only 3112e50..HEAD -- '*.go'` kosong, 0 berkas):

- `gofmt -l .` kosong; `go vet ./...` dan `go build ./...` bersih.
- `go test ./... -cover -count=1`: semua `ok` (cakupan `cmd/xdns` 91,9%, `backend` 100,0%, `conformance` 81,0%, `dnsproxybe` 95,4%, `config` 95,5%, `engine` 96,8%, `lists` 86,7%, `qlog` 89,1%, `selfcheck` 92,5%).
- `CGO_ENABLED=1 go test -race ./... -count=1`: semua `ok`.
- `go mod verify`: semua modul terverifikasi; `go mod tidy -diff`: tidak ada perubahan.
- `govulncheck ./...` (dipasang di fase ini ke `GOPATH\bin`, PATH tidak diubah): 0 kerentanan terdampak, 1 advisori pada modul yang dipakai tetapi tidak dipanggil kode kita. Ini mengonfirmasi klaim Gemini di akhir Fase 2.
- `xdns check -config configs/xdns.example.yaml`: `uji mandiri: LULUS`, termasuk S3 SafeSearch.
- Uji langsung dari PC lewat `scripts/dnsquery.ps1 -Server 192.168.1.99 -Port 53`: entri daftar nyata dijawab `0.0.0.0` ttl 10; `www.example.com` dan `www.google.com` normal; `use-application-dns.net` NXDOMAIN; `_dns.resolver.arpa` (SVCB) NODATA.

Gemini 4 Argon menutup Fase 2 sendiri; hasilnya diverifikasi ulang oleh Claude di awal fase ini (gerbang, race, `xdns check`, dan pembacaan perbaikan failover bootstrap di `internal/lists/fetch.go`) tanpa temuan.

## Tinjauan

Tidak dilakukan. Aturan proyek menugaskan satu tinjauan agent per fase untuk kode yang berubah; di Fase 3 tidak ada berkas Go yang berubah (hanya konfigurasi lokal `xdns.yaml` yang diabaikan git, aturan firewall, `AGENTS.md`, dan catatan `.agents/`). Tinjauan berikutnya terjadi di Fase 4 (backend `native`).

## Keputusan yang diambil (Rulings)

1. **Aturan firewall sempit (opsi A), bukan mengubah kategori Wi-Fi ke Private.** Alasan: jaringan Wi-Fi di PC berkategori Public; mengubahnya membuka PC lebih luas. Risiko: aturan terikat pada jalur `bin\xdns.exe`; memindahkan atau membangun di jalur lain memerlukan aturan baru. Pencabutan: `Remove-NetFirewallRule -Group xdns`.
2. **Alamat PC `192.168.1.99`, bukan `.10`.** Master meminta angka lebih tinggi supaya tidak bentrok dengan perangkat keluarga; dipesan di router.
3. **`AGENTS.md` di root repo** sebagai penunjuk ke `.agents/README.md` (permintaan Master).
4. **Aturan kustom `block ad.doubleclick.net` dan `block adservice.google.com`** (Master: "full power"). Disimpan di `data/rules.txt`, yang tidak ikut git.
5. **Aturan `block s.shopee.co.id`.** Mematikan redirect Shopee dari iklan di X; sebagai akibat, tautan Shopee berbentuk `s.shopee.co.id/...` tidak terbuka di HP Master. Bisa dibatalkan satu perintah.
6. **Empat domain yang masih lolos di tes (`googletagmanager`, `mopinion`, `iubenda`, `fluidplayer`) dibiarkan** (keputusan Master) karena risiko merusak situs sah.
7. **Riwayat git `.agents/`:** 11 commit `chore` lokal digabung menjadi satu (`8a51cfb`) atas permintaan Master; repo belum punya remote dan isi pohon identik sebelum dan sesudah penggabungan.

## Catatan & hal yang ditunda

- **Q4 (kolam TCP bersama UDP)** tetap wajib diperbaiki sebelum Fase 6. Sekarang xdns mendengarkan di alamat LAN, tetapi `allowed_clients` membatasi ke `192.168.1.0/24`; ACL berjalan per permintaan, jadi perangkat mana pun di Wi-Fi rumah masih bisa membuka banyak koneksi TCP.
- Pembatasan `allowed_clients` ke IP HP saja (`192.168.1.150/32`) belum dilakukan; ditawarkan ke Master, belum dijawab.
- Baseline "tanpa xdns" yang lengkap, Firefox, DNS IPv6 dari router, dan EDE belum diuji (daftar di `test-report.md` bagian 5).
- xdns masih berjalan sebagai proses tersembunyi biasa; Fase 5 memasang jalan otomatis saat boot.
- Berkas lama yang masih menyebut `192.168.1.10` (`docs/phase-2-report.md` dan `docs/superpowers/plans/`) sengaja tidak diubah karena mencatat keadaan saat itu; nilai terkini ada di `architecture.md` §12.

## Serah terima ke Fase 4

- Kode Go tidak berubah sejak `7fd1ac0`; semua gerbang hijau di sana.
- Backend `dnsproxy` terbukti pada HP nyata. Fase 4 membangun backend `native` (`miekg/dns`) dan menjalankan suite `conformance` yang sama pada kedua backend; tes HP dengan `native` mengulang S1–S2 memakai pengaturan yang sama (HP `192.168.1.150` → PC `192.168.1.99`).
- Untuk uji HP dengan `native`: hentikan xdns yang berjalan (`Stop-Process -Name xdns`), ganti `backend: native` di `xdns.yaml` lokal, jalankan ulang; aturan firewall sudah ada selama jalur `bin\xdns.exe` sama.
- `xdns.yaml` lokal (diabaikan git): `listen: 192.168.1.99:53`, `allowed_clients` `127.0.0.0/8`, `::1/128`, `192.168.1.0/24`.
