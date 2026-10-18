# Laporan Fase 2 — Daftar blokir, rantai CNAME, nama khusus, dan uji mandiri

*2026-10-18 | Status: selesai, sudah ditinjau dengan konteks baru, seluruh temuan Important dan Minor diperbaiki dengan TDD*

## Hasil

xdns kini memblokir iklan secara penuh memakai ketiga daftar bawaan (HaGeZi Multi PRO, HaGeZi Pop-Up Ads, dan ABPindo — total **250.089 entri** sebelum de-duplikasi), memeriksa rantai CNAME pada jawaban hulu maupun cache hit (`Engine.After`), menangani nama khusus (`use-application-dns.net` → NXDOMAIN, `_dns.resolver.arpa` → NODATA), serta menyediakan subperintah `xdns lists status|update` dan uji mandiri `xdns check` (termasuk verifikasi SafeSearch S3 dan deteksi loop konfigurasi Q3). Seluruh empat kriteria "selesai bila" Fase 2 lulus pada binary asli dan suite pengujian.

| Tugas | Hasil | Commit |
|---|---|---|
| P2-01 `lists/parser` | Parser format `domains` dan `hosts`, batas 4.096 byte/baris, 64 MiB/berkas, 1.000.000 domain unik, fuzz target `FuzzParseLine` | `74a646f` |
| P2-02 `lists/fetch` | Pengunduh HTTPS saja, `ETag` / `If-None-Match` (`304 Not Modified`), batas waktu 30 detik, penulisan atomik `<nama>.txt` + `<nama>.meta.json`, resolusi nama host lewat `bootstrap` | `8c31c85` |
| P2-03 `lists/manager` | Muat cache disk saat start, segarkan berkala (24 jam + jitter hingga 10%), polling cap berkas 5 detik, tukar snapshot atomik `SwapLists` | `084b377` |
| P2-04 Integrasi `xdns run` | `lists.Manager` dipasang ke `xdns run`; catatan stub Fase 1 dihapus | `a7701ab` |
| P2-05 `engine/cname` | Pemeriksaan rantai CNAME di `Engine.After` (`ReasonCNAME`, `Via: target`), kasus kesesuaian termasuk cache hit | `8c34703` |
| P2-06 HTTPS/SVCB & nama khusus | `use-application-dns.net` → NXDOMAIN, `_dns.resolver.arpa` → NODATA (`engine/special.go`); kasus kesesuaian HTTPS (65) dan SVCB (64) pada blokir langsung dan CNAME | `cb99a20` |
| P2-07 `selfcheck`, `xdns check`, `xdns lists` | `Config.LoopWarnings()` (Q3), `internal/selfcheck` (hulu, S3 SafeSearch, sampel tiap daftar), CLI `xdns lists status|update` dan `xdns check` | `af34c50` |
| P2-08 Benchmark 250k entri | `BenchmarkListSet_Lookup250k` dan `TestListSet_Budget250k` (p99 dan memori pada 250.000 entri) | `a264c54` |
| P2-09 Gates & perbaikan tinjauan | Uji binary langsung dengan 3 daftar nyata; tinjauan berkonteks baru; 3 commit perbaikan TDD (`I1`, `I2`, `M1`–`M8`) | `e5c18e5`, `d3483b7`, `7fd1ac0` |

## Bukti verifikasi (keadaan akhir, setelah perbaikan tinjauan)

- `gofmt -l .` kosong; `go vet ./...`, `go build ./...`, `go mod verify`, `go mod tidy -diff` bersih; build silang Linux (`GOOS=linux GOARCH=amd64 go build ./...`) berhasil.
- `go test ./... -cover -count=1`: semua paket lulus (`ok`). Cakupan per paket:
  - `xdns/cmd/xdns`: **92,2%**
  - `xdns/internal/backend`: **100,0%**
  - `xdns/internal/backend/conformance`: **81,0%**
  - `xdns/internal/backend/dnsproxybe`: **95,4%**
  - `xdns/internal/config`: **95,5%**
  - `xdns/internal/engine`: **96,8%**
  - `xdns/internal/lists`: **86,7%**
  - `xdns/internal/qlog`: **89,1%**
  - `xdns/internal/selfcheck`: **92,5%**
- `CGO_ENABLED=1 go test -race ./... -count=1`: semua paket lulus tanpa data race. Uji fuzz `FuzzParseLine` (15 detik, 2,2 juta eksekusi, 0 kegagalan).
- `govulncheck ./...`: **0 kerentanan terdampak** (satu advisori modul tidak dipanggil kode kita: GO-2026-5932).
- Benchmark & anggaran sumber daya (`internal/engine/matcher_test.go` + pengukuran proses hidup):
  - `BenchmarkListSet_Lookup250k-12`: **214,9–250,1 ns/op** (~0,25 µs/op), **0 B/op**, **0 allocs/op**.
  - Persentil latensi pencarian 250.000 entri: **p50 = 0 s**, **p95 = 1,04–1,07 µs**, **p99 = 2,01 µs** (target: `< 50 µs`).
  - Memori heap `DomainSet` (250.000 entri): `heap_inuse_delta` = **16,15–16,29 MiB**, `Sys` runtime Go = **38,15–42,64 MiB**.
  - Memori proses `xdns.exe run` hidup dengan ketiga daftar nyata termuat (250.089 entri): **WorkingSet = 71,74–74,20 MiB**, **PrivateBytes = 103,35–105,25 MiB** (target: `< 150 MB`).
- Verifikasi binary asli (dengan 3 daftar nyata diunduh dari internet):
  - `xdns lists update`: `hagezi-pro` **202.297 domain** (ditolak 0), `hagezi-popups` **47.668 domain** (ditolak 0; 113 unik terhadap PRO), `abpindo` **124 domain** (ditolak 0) — total **250.089 entri**. Pemanggilan kedua `xdns lists update` menerima `304 Not Modified` (`belum berubah`).
  - `xdns check`: memverifikasi kedua hulu DoH (`1.1.1.1` dan `8.8.8.8`), mencetak `safe_search (S3): LULUS (www.google.com tidak diarahkan ke forcesafesearch.google.com)`, memverifikasi sampel domain tiap daftar menjawab `0.0.0.0`, dan keluar dengan `uji mandiri: LULUS` (kode 0).
  - `xdns run` + `scripts/dnsquery.ps1`:
    - `telemetry.001-studio.com A` → `0.0.0.0` TTL 10 (`BLOK … list:hagezi-pro`)
    - `h4g3z1-popupads.web.app A` → `0.0.0.0` TTL 10 (`BLOK … list:hagezi-popups`)
    - `accesstrade.co.id A` → `0.0.0.0` TTL 10, `AAAA` → `::` (`BLOK … list:abpindo`)
    - `use-application-dns.net A` → `NXDOMAIN` + SOA (`BLOK … special:firefox_canary`)
    - `_dns.resolver.arpa SVCB` → `NOERROR` kosong + SOA (`BLOK … special:ddr`)
    - `www.google.com A` → `NOERROR` (8 rekaman A asli Google lewat DoH)

## Tinjauan

Peninjau berkonteks baru (`phase-reviewer`) memeriksa `8c10920..HEAD` dan menguji binary asli beserta eksperimen mutasi: **0 Critical, 2 Important, 8 Minor**. Laporan lengkap: `.agents/state/reviews/phase-2-review.md`.

| Temuan | Penanganan |
|---|---|
| **I1 (Important)** `newBootstrapResolver` di `internal/lists/fetch.go` memakai `d.DialContext(ctx, "udp", addr)` yang selalu berhasil secara lokal pada `addrs[0]` tanpa mengirim paket, sehingga tidak pernah berpindah ke `addrs[1]` bila `addrs[0]` mati/diam | `Fetcher` kini me-resolve nama host daftar lewat `miekg/dns` (pertukaran paket DNS UDP nyata dengan fallback TCP bila terpotong) secara berurutan di sepanjang `Bootstrap`; diuji dengan soket UDP blackhole di `addrs[0]` dan server DNS palsu di `addrs[1]` (`e5c18e5`) |
| **I2 (Important)** `selfcheck` hanya membaca baris pertama dengan `bufio.Scanner` (batas 64 KiB), mengabaikan galat `rules.Reload()`, gagal bila entri pertama di-allow pengguna, dan untuk `hagezi-popups` memilih domain yang sudah ada di `hagezi-pro` tanpa memeriksa `dec.Source == src.Name` | `selfcheck` memeriksa galat `rules.Reload()`, menghapus `bufio.Scanner`, dan memakai `DomainSet.Find` untuk memilih sampel terkecil secara leksikografis per daftar yang tidak dikecualikan aturan/daftar `allow` dan memiliki `dec.Source == src.Name` (`d3483b7`) |
| **M1 (Minor)** `lists.Parse` mengalokasikan peta `seen` + slice `domains` lalu membangun ulang peta kedua di `engine.NewDomainSet` | Ditambahkan `engine.DomainSetBuilder` sehingga `lists.Parse` membangun `DomainSet` dalam satu lintasan tanpa alokasi ganda (`d3483b7`) |
| **M2 (Minor)** `Fetcher` mengkloning `http.DefaultTransport` tiap `Fetch()`, dan `Manager.Run()` saat start mem-parse ulang 250 ribu domain dari disk saat menerima `304 Not Modified` | `Fetcher` menyimpan dan memakai ulang `*http.Transport` bawaan (`e5c18e5`); `Manager.Refresh` meneruskan `*engine.DomainSet` di memori lewat `FetchWithCached` bila cap berkas disk tidak berubah (`7fd1ac0`) |
| **M3 (Minor)** `len(via) >= maxRedirects` (5) menolak pada redirect ke-5 padahal pesan galat menyebut melebihi 5 redirect | Diubah menjadi `len(via) > maxRedirects` dan diuji bahwa 5 redirect HTTPS berhasil sedangkan redirect ke-6 ditolak (`e5c18e5`) |
| **M4 (Minor)** `ReloadCache` tidak mencatat galat `statList` dan `Refresh` memanggil `publishLocked` saat semua unduhan gagal | `ReloadCache` mencatat galat `statList` dan `Refresh` hanya memanggil `publishLocked` bila ada pembaruan (`7fd1ac0`) |
| **M5 (Minor)** `LoopWarnings` melewatkan kombinasi listen `[::]:port` dengan hulu `127.0.0.1:port`, hulu `0.0.0.0:port`, dan nama host `localhost` | `LoopWarnings` diperluas mencakup kombinasi wildcard/loopback IPv4/IPv6 pada port yang sama serta URL `localhost` (`7fd1ac0`) |
| **M6 (Minor)** `failOpen` selalu mencatat `"stage":"before"` walau dipanggil dari `inspectAnswer` (`After`) | `failOpen` menerima parameter `stage` (`"before"` atau `"after"`) (`7fd1ac0`) |
| **M7 (Minor)** Komentar penanda handover `// agy wrote this` di baris 1 di atas `package` ikut terbaca `go doc` | Dipertahankan sesuai instruksi eksplisit Master untuk penanda handover ke Claude, tetapi dipindahkan ke bawah baris `package` agar `go doc` tetap bersih (`7fd1ac0`) |
| **M8 (Minor)** Tiga mutasi bertahan pada salinan scratch (perubahan berkas ukuran sama di `ReloadCache`, subdomain `forcesafesearch.google.com`, dan `.meta.json` yatim tanpa `.txt`) | Ditambahkan tes unit untuk ketiga skenario tersebut (`e5c18e5`, `d3483b7`, `7fd1ac0`) |

## Keputusan yang diambil (Rulings)

1. **Peta string standar Go (`map[string]struct{}`) dipertahankan untuk `DomainSet`.** Dengan 250.000 entri, delta heap hanya ~16,2 MiB (memori proses hidup ~72–74 MiB WorkingSet, jauh di bawah batas 150 MB) dan latensi pencarian p99 2,01 µs (rata-rata ~215–250 ns/op, 0 alokasi, jauh di bawah batas 50 µs), sehingga penggantian ke peta hash 64-bit tidak diperlukan.
2. **Resolusi nama host sumber daftar memakai `bootstrap` secara langsung lewat `miekg/dns`.** Agar unduhan daftar tidak pernah berputar lewat `xdns` sendiri (Fase 0 ruling 12) dan tidak bergantung pada perilaku `net.Resolver` terhadap soket UDP lokal, `Fetcher` me-resolve nama host URL daftar ke alamat IP memakai server `bootstrap` secara berurutan sebelum membuka koneksi TLS.
3. **Koordinasi `xdns lists update` dengan server yang berjalan memakai polling cap berkas 5 detik.** Selain jadwal unduh latar belakang 24 jam (+ jitter hingga 10%), `lists.Manager` memeriksa ukuran + waktu ubah berkas `<nama>.txt` setiap 5 detik sehingga `xdns lists update` langsung diterapkan server tanpa restart (sesuai D11).
4. **Berkas cache daftar yang belum ada pada `xdns check` menghasilkan peringatan, bukan kegagalan keras.** Dengan demikian `xdns check -config configs/xdns.example.yaml` lulus pada pohon git bersih tanpa perlu mengunduh daftar terlebih dahulu, sedangkan setiap berkas cache yang sudah ada di disk wajib valid, tidak kosong, dan terverifikasi memblokir domain sampelnya sendiri.
5. **Nama khusus selalu mengembalikan rcode spesifik protokol.** `use-application-dns.net` selalu dijawab `NXDOMAIN` + SOA sintetis dan `_dns.resolver.arpa` selalu dijawab `NOERROR` kosong (NODATA) + SOA sintetis terlepas dari `block.mode`, karena Firefox DoH canary secara khusus memeriksa `NXDOMAIN` dan klien DDR memeriksa `NODATA`.
6. **Deteksi loop konfigurasi (Q3) diterapkan lewat `Config.LoopWarnings()` dan `selfcheck`.** Alamat `upstreams` atau `bootstrap` yang mengarah ke alamat `listen` sendiri menghasilkan peringatan di `Warnings()` (dicetak saat `xdns run` dan `xdns check`) dan menggagalkan `selfcheck.Run` (`xdns check` keluar dengan kode 1).

## Catatan & hal yang ditunda ke fase berikutnya

- Pada unduhan pertama `hagezi-pro` (~4,5 MB) dari `cdn.jsdelivr.net` saat koneksi dingin, unduhan sempat terkena batas waktu 30 detik satu kali lalu berhasil dalam 13,0 detik pada percobaan berikutnya; bila jaringan rumah sering lambat ke jsDelivr, `lists.Manager` akan mempertahankan cache lama dan menyegarkan pada siklus berikutnya.
- Perilaku nyata Firefox dan Chrome di HP RedMagic 8S Pro terhadap jawaban `use-application-dns.net` (NXDOMAIN) dan `_dns.resolver.arpa` (NODATA), serta reaksi HP terhadap `0.0.0.0`, akan diuji langsung di Fase 3 (`architecture.md` §18 butir 3 dan 6).
- Temuan Fase 1 I1 (kolam slot TCP bersama UDP di dnsproxy) tetap menjadi prasyarat wajib sebelum Fase 6 (VPS publik).

## Serah terima ke Fase 3

- **Gerbang G1 (opsional sebelum/selama pemakaian di PC):** setelah `xdns lists update` dan `xdns run` dipakai beberapa jam di PC, tinjau `xdns log -blocked` dan tambahkan `xdns rule add allow <domain>` bila ada salah blokir.
- **Gerbang G2 (awal Fase 3 — wajib tanya Master):** Android tidak bisa memilih port DNS, sehingga xdns perlu mendengarkan di `192.168.1.10:53` (UDP dan TCP). Saat ini layanan Windows `SharedAccess` (ICS) memegang `0.0.0.0:53` UDP. Sebelum mengubah layanan atau firewall, tanyakan kepada Master:
  1. Apakah Master memakai Mobile Hotspot, Hyper-V, atau WSL di PC ini?
  2. Bisakah Master membuka pengaturan router Wi-Fi (untuk reservasi IP DHCP PC)?
  3. Browser apa yang dipakai di HP RedMagic 8S Pro?
