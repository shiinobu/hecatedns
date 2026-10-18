# Laporan Fase 4 — Backend `native` dan suite kesesuaian

*2026-10-19 | Claude (Sonnet 5.5) | Status: selesai di sisi kode dan tes. Tinggal tes HP dengan `backend: native`, yang menunggu Master.*

## Hasil singkat

Backend kedua, `native`, sudah ada dan bisa dipilih dengan satu baris (`backend: native`). Ia memakai mesin keputusan yang sama dengan `dnsproxy`, jadi aturan, daftar, dan log tidak berubah. Suite kesesuaian yang sama (15 kasus) hijau pada kedua backend, dan 2.000 query sampel menghasilkan keputusan blokir/lolos yang **identik** (0 beda).

## Keputusan Master untuk fase ini

| Keputusan | Akibat |
|---|---|
| DoT sebagai hulu ditunda | `tls://` ditolak saat start dengan pesan jelas. DoH, UDP, dan TCP polos ada. DoT sebagai *listener* (Fase 6) tidak terpengaruh |
| Review fase dibuat singkat | Satu review berkonteks baru, model Sonnet, cakupan sempit (lihat bagian Review). **Review penuh wajib di Fase 5, dan paling lambat sebelum server dibuka ke internet (Fase 6)** |
| Kerja sampai ±01.00 | Tes HP dengan `native` (tugas P4-09) tidak dikerjakan karena butuh Master di depan HP |

## Yang dibangun

Paket `internal/backend/native` (cakupan tes 93,1%); rincian per berkas ada di `architecture.md` §5.4.

| Tugas | Hasil |
|---|---|
| P4-01 cache | LRU berbatas byte, TTL dijepit, cache negatif, kunci termasuk bit DO, menyimpan jawaban hulu sebelum `After` |
| P4-02 hulu | Kolam dengan pilihan acak berbobot dan failover; plain UDP/TCP, DoH dengan resolver bootstrap; jawaban diterima hanya bila QR, ID, opcode, dan pertanyaan cocok |
| P4-03 server | UDP dan TCP pada tiap `listen`; batas koneksi TCP sendiri (64); batas query berjalan (256); kegagalan bind dikembalikan seketika; shutdown rapi |
| P4-04 handler, laju | Batas laju per IP (100/detik, burst 200); pemulihan panik per tahap seperti adapter dnsproxy; ANY dijawab NOTIMP; ukuran UDP mengikuti EDNS klien |
| P4-05 registri | `native` terdaftar di `cmd/xdns`; `xdns run` dan `xdns check` bekerja dengan `backend: native` (dicoba nyata, lihat di bawah) |
| P4-06 kesesuaian | Satu tabel, dua backend; 15 kasus lulus di keduanya |
| P4-07 pembanding | `scripts/compare`: lihat bagian Perbandingan |
| P4-08 keamanan | Lihat bagian Keamanan |
| P4-10 gate | Semua hijau (bagian Gate); review singkat |

## Bukti

**Suite kesesuaian.** `internal/backend/conformance` menjalankan 15 kasus pada `dnsproxy` dan `native` dari satu daftar `constructors()`, tanpa pengecualian per backend. Satu kasus baru ditambahkan di fase ini (datagram UDP besar atau rusak tidak menghentikan server, lihat Temuan).

**Perbandingan 1.000 domain** (`scripts/compare`, dua server pada port 5301 dan 5302 dengan konfigurasi, daftar, dan aturan yang sama). Sampel: 600 nama dari log query asli (HP dan PC) dan 400 dari ketiga daftar blokir (sebagian dengan awalan `www.`). Tiap nama ditanyakan A dan HTTPS, jadi 2.000 query per server. Keputusan dibaca dari log query kedua server.

| | `dnsproxy` | `native` |
|---|---|---|
| Query | 2.000 | 2.000 |
| Diblokir | 1.732 | 1.732 |
| Keputusan engine beda | 0 | |
| Bentuk jawaban beda | 1 | |
| Catatan log hilang | 0 | |

Satu perbedaan bentuk jawaban: `api.x.com` HTTPS, `dnsproxy` mendapat record dan `native` mendapat NOERROR kosong. Diulang tiga kali pada kedua server, keduanya menjawab NOERROR kosong; jawaban HTTPS di hulu berubah di antara dua pertanyaan. Jadi ini variasi hulu, bukan perilaku backend. Sampel memuat banyak nama yang diblokir (86%) karena separuh lebih berasal dari daftar dan log HP.

**Uji nyata `native`.** Dijalankan di `127.0.0.1:5300` dengan DoH ke 1.1.1.1 dan daftar asli: `example.com` lolos, `ad.doubleclick.net` dan `s.shopee.co.id` menjawab `0.0.0.0` (TTL 10), `use-application-dns.net` NXDOMAIN, HTTPS dan AAAA dijawab. `xdns check` dengan `backend: native` lulus.

**Gate** (2026-10-19): `gofmt -l .` kosong; `go vet ./...` dan `go build ./...` bersih; `go test ./... -cover -count=1` semua `ok` (`native` 93,1%, `conformance` 80,9%, `dnsproxybe` 95,4%, `engine` 96,8%, `cmd/xdns` 92,2%); `CGO_ENABLED=1 go test -race ./...` semua `ok`; `go mod verify` bersih; `go mod tidy -diff` tanpa selisih; `govulncheck` 0 terdampak (satu advisori pada modul yang tidak dipanggil); `xdns check -config configs/xdns.example.yaml` lulus. Server asli di `192.168.1.99:53` (dnsproxy) tidak disentuh dan tetap melayani HP.

## Temuan

1. **Satu datagram UDP besar menghentikan listener UDP di Windows.** Saat tes, mengirim 5.000 byte ke port UDP membuat `ReadFromUDP` gagal dengan `WSAEMSGSIZE`; miekg menganggap galat itu fatal dan keluar dari loop, sehingga semua query UDP berhenti sampai restart. Dari host mana pun di LAN, cukup satu paket. Perbaikan: `tolerantReader` menganggap galat itu sementara. Kasusnya dijadikan tes suite kesesuaian; `dnsproxy` lulus kasus yang sama (9.000 byte), jadi hanya `native` yang perlu perbaikan.
2. Helper tes yang saya tulis sempat gagal secara acak di Windows (soket UDP yang ditutup langsung dipakai ulang di port yang sama). Hanya tes, sudah diperbaiki.
3. Perkiraan ukuran cache hanya memakai panjang paket, sehingga cache berisi record kecil bisa memakai memori beberapa kali lipat dari `size_mb`. Diperbaiki dengan biaya tetap per record.
4. Review singkat menemukan lima hal Important (kunci cache, penyapuan tabel laju, slot hulu tanpa batas waktu, TCP per alamat, balapan shutdown) dan enam Minor; semuanya diperbaiki atau dicatat dengan alasan. Lihat bagian Review singkat.

## Keamanan (P4-08)

| Area | Keadaan |
|---|---|
| ACL | Dijalankan engine untuk kedua backend; kasus suite menjamin REFUSED dan tidak ada query ke hulu |
| Bind | Soket diikat di awal pada alamat `listen` saja; galat dikembalikan seketika |
| Batas laju | Per IP, tabel dibatasi 4.096; klien yang kena batas dibuang tanpa jawaban |
| Sumber daya | 256 query hulu (64 per klien), 64 koneksi TCP (8 per alamat), tiap koneksi maks 100 query, batas baca 5 detik dan menganggur 10 detik, batas waktu 11 detik per query |
| Pesan terlalu besar/rusak | Buffer baca UDP 4.096; kegagalan baca UDP tidak menghentikan server; pesan dengan jumlah pertanyaan atau record tidak wajar diabaikan; jawaban UDP dipotong ke ukuran EDNS klien (512–1232) dengan bit TC |
| Jawaban hulu | Diterima hanya bila QR, ID acak, opcode, dan pertanyaan cocok; DoH memeriksa sertifikat, tidak mengikuti redirect, tidak memakai proksi lingkungan, badan dibatasi, tipe konten diperiksa |
| Privasi hulu | Query ke hulu dibangun baru: cookie, client subnet, dan padding klien tidak diteruskan |
| Cache | Hanya jawaban hulu yang tervalidasi dan cocok dengan pertanyaannya; tidak ada pengecekan bailiwick isi jawaban (hulu DoH dipercaya) |
| Panik | Dipulihkan per tahap; sebelum engine meloloskan: REFUSED |

**Risiko yang diterima sampai Fase 6:** (a) belum ada pemeriksaan ACL saat accept TCP, jadi satu host di luar ACL bisa memegang 8 koneksi selama batas baca 5 detik (UDP dan koneksi host lain tetap jalan); (b) tidak ada deduplikasi query identik yang sedang berjalan; (c) reverse lookup alamat privat dari klien non-privat diteruskan ke hulu (dnsproxy menjawab NXDOMAIN); (d) pembatas laju memakai alamat IPv6 penuh. Semuanya tidak berdampak untuk satu HP di LAN rumah.

## Review singkat

Satu agent berkonteks baru (model Sonnet, hanya baca, cakupan `internal/backend/native` tanpa tes, sekitar 17 panggilan alat). Ia tidak menjalankan tes. Hasilnya saya nilai satu per satu terhadap kode; hasil: 0 Critical, 5 Important, 6 Minor. Semua yang saya anggap benar sudah diperbaiki dengan tes lebih dulu (commit `fix: harden the native backend after review`):

| Temuan | Tingkat | Keputusan |
|---|---|---|
| Kunci cache tanpa bit CD: jawaban yang diambil dengan validasi dimatikan bisa disajikan ke klien yang meminta validasi | Important | Diperbaiki (bit CD masuk kunci) |
| Tabel pembatas laju penuh disapu penuh di setiap paket dari alamat baru, serial di bawah kunci global | Important | Diperbaiki (paling sering sekali per detik) |
| Slot "query berjalan" dipegang sepanjang query tanpa batas waktu; hulu yang diam menghabiskan 256 slot, termasuk untuk cache hit dan jawaban blokir | Important | Diperbaiki (hanya pertukaran ke hulu yang memakai slot, batas per klien 64, batas waktu 11 detik, pembatalan saat shutdown) |
| Batas TCP global 64 tanpa batas per alamat | Important | Diperbaiki (8 per alamat); pemeriksaan ACL saat accept belum ada (dicatat) |
| `ShutdownContext` sebelum server sempat mulai tidak menghentikannya: `Run` bisa menggantung dan soket tetap terbuka | Important | Diperbaiki (soket ikut ditutup; tes pembatalan seketika) |
| Hanya `WSAEMSGSIZE` yang dianggap sementara; galat baca UDP lain mengakhiri server | Minor | Diperbaiki (semua galat baca kecuali soket ditutup, dengan jeda 10 ms) |
| Pesan rusak dijawab FORMERR oleh miekg sebelum ACL, batas laju, dan log | Minor | Diperbaiki (diabaikan; berbeda dari dnsproxy, dicatat) |
| Hulu yang menjawab SERVFAIL atau REFUSED langsung diteruskan | Minor | Diperbaiki (pindah ke hulu berikutnya; bila semua begitu, jawaban hulu itu diteruskan) |
| `After` yang memblokir tanpa jawaban membuka jawaban asli | Minor | Diperbaiki (SERVFAIL) |
| Query yang dibuang (batas laju, penuh) tidak masuk log query | Minor | Sengaja; dihitung dan diperingatkan di log server paling sering sekali per 10 detik |
| Kunci pembatas laju IPv6 memakai alamat penuh, bukan /64 (bagian dari temuan batas laju) | Important untuk Fase 6 | Ditunda: LAN rumah memakai IPv4; dicatat untuk Fase 6 |

Yang tidak bisa diverifikasi reviewer: perilaku galat UDP Windows selain `WSAEMSGSIZE`, dan server DoH yang memantulkan ID bukan nol (pemeriksaan ID ketat, sama seperti RFC 8484). Ini review singkat atas permintaan Master, bukan pengganti review penuh `xdns-reviewer`, yang masih terutang.

## Belum terverifikasi

- Tes HP dengan `backend: native` (S1, S2): menunggu Master. Tidak ada yang perlu diubah di HP; cukup ganti `backend: native` di `xdns.yaml`, mulai ulang server, tunggu sekitar satu menit atau nyalakan mode pesawat sebentar.
- Beban nyata dan lama (7 hari) pada `native`; Fase 5.
- DoT sebagai hulu (ditunda). IPv6 hulu belum dicoba.
- Hal Fase 3 yang masih terbuka tetap terbuka: Play Store, Firefox di HP, jalur DNS IPv6 router, EDE, baseline lengkap.

## Serah terima ke Fase 5

- Review penuh (xdns-reviewer, mencakup Fase 4) menjadi tugas pertama Fase 5.
- Sebelum Fase 6: batas koneksi TCP per IP untuk kedua backend, pembatas laju untuk `dnsproxy`, deduplikasi query, dan perilaku ARPA privat (`architecture.md` §5.4).
- Server rumah masih memakai `dnsproxy`. Beralih ke `native` hanya setelah tes HP lulus.
