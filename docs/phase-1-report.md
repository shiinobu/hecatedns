# Laporan Fase 1 — Engine inti + backend dnsproxy

*2026-10-18 | Status: selesai, sudah ditinjau, semua temuan Important diperbaiki kecuali satu yang sengaja diterima untuk jaringan rumah (I1, lihat bawah)*

## Hasil

hecate sekarang benar-benar berjalan: `hecate run` mendengarkan di `127.0.0.1:5300` (UDP dan TCP), meneruskan query ke hulu DoH lewat dnsproxy v0.86.0, dan memblokir nama yang cocok dengan aturan kustom (`hecate rule`). Daftar blokir (HaGeZi, ABPindo) **belum dimuat**: itu pekerjaan Fase 2, jadi saat ini hanya aturan kustom yang memblokir. Semua kriteria "selesai bila" Fase 1 lulus pada binary asli.

| Tugas | Hasil | Commit |
|---|---|---|
| P1-01 Spike dnsproxy | Fakta terverifikasi dengan file:baris di `.agents/notes/dnsproxy-api-facts.md`; arsitektur §5.3 berlaku | (catatan, tanpa kode) |
| P1-02 `engine/matcher` | `DomainSet` cocok-sufiks pada batas label, `Matcher` dengan tukar atomik, `ParseEntry`/`NormalizeName` (lipat huruf hanya ASCII) | `b4fe654` |
| P1-03 `engine/rules` | Parser `block`/`allow <domain>` dengan nomor baris, batas 4096 byte per baris dan 4 MiB per berkas, muat ulang lewat polling waktu-ubah + ukuran | `b030ecf` |
| P1-04 `engine/response` | Empat mode blokir, SOA sintetis, jawaban dikompresi | `1797d12` |
| P1-05 `engine/acl` | ACL fail-closed (IPv4-mapped, zona IPv6, batas CIDR) | `ae1f353` |
| P1-06 `engine/engine` | `Before`/`After`/`Failed`, prioritas, satu catatan per query, panic ditangani, daftar sebagai snapshot atomik | `261fe49` (+ seam `9c5815e`) |
| P1-07 `qlog` | Log JSONL harian, antrean terbatas (buang + hitung), retensi, sanitasi, pembaca + filter | `f3452f2`, `7777774` |
| P1-08 `backend` + `dnsproxybe` | `Registry` dibuat pemanggil (siklus impor Q1 selesai), adapter satu handler dengan `recover` | `a03f1a1` |
| P1-09 Suite kesesuaian | 12 kasus per backend dengan hulu palsu di loopback | `b5cc616` |
| P1-10 CLI | `hecate run`, `hecate log`, `hecate rule add|remove|list` | `78a1310`, `45b14eb`, `4403a3c` |
| P1-11 Pengerasan konfigurasi | `enabled:` yang dihilangkan = aktif, `rules.file`/`log.dir` wajib, `listen` unik dan port ≠ 0, TTL dibatasi, `/hecate.yaml` diabaikan git | `eeac3ed` |
| Tambahan | `scripts/dnsquery.ps1` (nslookup Windows mengabaikan `-port`) | `f825312` |
| Perbaikan hasil tinjauan | Tujuh commit `fix:`/`test:` | `3dbeaa1`, `c8e351b`, `d35f0ae`, `e9fb444`, `fc7a1d3`, `599a526`, `b626fee` |

Pembagian kerja model: sesi utama Sonnet 5.5 mengerjakan semua yang rutin; agent Opus `hecate-core` mengerjakan engine dan adapter dnsproxy dalam satu putaran; agent Opus `hecate-reviewer` melakukan satu tinjauan berkonteks baru. 26 commit sejak `4b546fc`, sekitar 10.200 baris Go termasuk tes.

## Bukti verifikasi (keadaan akhir, setelah perbaikan tinjauan)

- `gofmt -l .` kosong; `go vet ./...`, `go build ./...`, `go mod verify`, `go mod tidy -diff` bersih; build silang Linux (`GOOS=linux GOARCH=amd64`) berhasil.
- `go test ./... -cover -count=1`: **225 pengujian tingkat atas dan 377 subtes lulus, 0 gagal**. Cakupan: `engine` 97,1%, `backend` 100%, `dnsproxybe` 95,4%, `config` 97,3%, `qlog` 89,1%, `cmd/hecate` 92,4%, `conformance` 81,9% (target ≥ 80%).
- `CGO_ENABLED=1 go test -race ./... -count=2`: lulus (detektor race berfungsi lewat w64devkit). Uji fuzz `FuzzParseRuleLine`, `FuzzACLAllows`, `FuzzSanitize` tanpa kegagalan.
- `govulncheck ./...`: **0 terdampak**; satu advisori di modul yang di-require tetapi tidak dipanggil (GO-2026-5932, `x/crypto/openpgp`, belum ada perbaikan).
- Mutasi di salinan scratch: puluhan mutasi pada engine, ACL, rules, response, qlog, config, CLI, adapter, dan suite kesesuaian; hampir semuanya tertangkap tes, sisanya ekuivalen atau menjadi tes baru (lihat tinjauan).
- Binary asli (hulu DoH sungguhan):
  - `example.com` dijawab A+AAAA lewat DoH dalam 67–80 ms.
  - `hecate rule add block contoh.test`: `www.contoh.test` menjawab `0.0.0.0` setelah **0,88 detik** (AAAA `::`, HTTPS NOERROR kosong); `rule add allow` berlaku setelah **1,93 detik** (kriteria S5: ≤ 10 detik).
  - Instance dengan `allowed_clients: [10.0.0.0/8]` menjawab **REFUSED** dan mencatat `BLOK … acl`.
  - `hecate log` menampilkan query lolos dan terblokir; alasan allow tampil sebagai `allow:allow contoh.test`.

## Tinjauan

Peninjau berkonteks baru (Opus, `hecate-reviewer`) memeriksa `4b546fc..HEAD` dan **menjalankan binary dengan beban ala penyerang**: **0 Critical, 4 Important, 12 Minor**. Laporan lengkap: `.agents/state/reviews/phase-1-review.md`.

| Temuan Important | Penanganan |
|---|---|
| **I1** 256 koneksi TCP menganggur dari satu host dapat memenuhi seluruh slot `MaxGoroutines` dan menghentikan UDP juga; ACL tidak menahannya karena ia berjalan per request | **Diterima untuk LAN rumah (Fase 1–5), wajib diperbaiki sebelum Fase 6.** Arah perbaikan: `proxy.Proxy` kedua khusus TCP dengan kolam sendiri, atau batas koneksi per IP di depan dnsproxy (`notes/open-questions.md` Q4) |
| **I2** Setiap penolakan ACL dicatat; satu pengirim UDP menulis 72 MB log dalam 5 detik | Batas harian `log.max_mb_per_day` (bawaan 256, wajib ≥ 1): catatan melewati batas dibuang dan dihitung, peringatan sekali sehari, ukuran berkas yang sudah ada ikut dihitung (`e9fb444`) |
| **I3** Opcode selain QUERY (STATUS, NOTIFY, UPDATE) diteruskan ke hulu melewati semua aturan: nama yang diblokir mendapat jawaban asli | Engine menjawab FORMERR/NOTIMP sendiri setelah ACL, alasan `invalid`, satu catatan; kasus kesesuaian baru (`c8e351b`) |
| **I4** `TestBindFailureEndsRunAndReleasesListeners` gagal 100 dari 300 kali di PC ini (rentang port UDP terkecuali Hyper-V/WinNAT) | Port TCP dipilih dari port yang UDP-nya bebas; 300 dari 300 lulus (`3dbeaa1`) |

Minor yang diperbaiki: catatan `GALAT` palsu untuk PTR privat (M1), panic di tahap filter kini REFUSED, bukan lolos tanpa filter (M3), teks bantuan (M4), `rule list` mengikuti batas baris server (M5), pemotongan berkas aturan tidak lagi tertelan batas isu (M6), jawaban dikompresi dan REFUSED ACL tanpa RA (M7), `max_ttl: 0` berarti tanpa batas (M8), `rule add` menolak hasil di atas 4 MiB (M9), `dnsquery.ps1` tidak lagi menggantung (M10), celah tes (M11), petunjuk punycode dan tes regresi timer tengah malam (M12).

## Keputusan yang saya ambil (Rulings)

1. Prioritas: aturan kustom `allow` > kustom `block` > daftar `allow` > daftar `block`. Langkah bernomor di arsitektur §6 membiarkan daftar `allow` mengalahkan aturan `block` milik pemilik; itu tidak dipakai.
2. Registri backend dibuat oleh pemanggil (`cmd/hecate`, suite kesesuaian) lewat `backend.Registry`, bukan di paket `backend` (menyelesaikan siklus impor Q1).
3. Adapter dnsproxy adalah satu `proxy.Handler`: `Before` → `p.Resolve` → `After`. dnsproxy menyimpan jawaban hulu ke cache **sebelum** `After`, jadi pemeriksaan CNAME Fase 2 akan berjalan juga pada cache hit.
4. `recover` di setiap tahap handler. Panic sebelum `Before` selesai → REFUSED (status ACL tidak diketahui); panic di dalam `Resolve` → SERVFAIL; panic setelah `Resolve` → jawaban dipertahankan. Engine sendiri gagal-buka kecuali pada ACL.
5. `MaxGoroutines` 256, batas waktu hulu 5 detik, `RefuseAny`, deduplikasi request kembar diaktifkan eksplisit (bawaan kode dnsproxy mati walau komentarnya bilang aktif); mode hulu `load_balance` (acak berbobot dengan fail-over, **bukan** "berurutan").
6. Engine menjawab sendiri request cacat dan opcode selain QUERY (I3); `Responder` memberi FORMERR bila pertanyaan ≠ 1.
7. `rule add` menggantikan aturan lama untuk domain yang sama dan menjaga komentar, baris rusak, BOM, dan CRLF; penulisan atomik lewat berkas sementara + rename (dicoba ulang singkat untuk pembaca Windows).
8. Log: berkas per hari lokal, retensi N hari termasuk hari ini, pembersihan saat start, saat rotasi, dan sesaat setelah tengah malam; hanya berkas `queries-YYYYMMDD.jsonl` biasa yang pernah dihapus; karakter kontrol/bidi diganti `?`; ada batas ukuran harian.
9. Konfigurasi: `enabled:` kosong (YAML null) berarti aktif; kunci gabung `<<` ditolak di dalam sumber daftar; TTL maksimum 2147483647 detik.
10. `scripts/dnsquery.ps1` ditambahkan (sedikit di luar daftar tugas) karena `nslookup -port` di Windows menerima opsi tetapi mengabaikannya; kriteria roadmap diganti.

## Minor yang ditunda

- Pembaruan serentak `hecate rule` tanpa kunci bisa menghilangkan satu pembaruan (jarang; Q5).
- Jawaban dnsproxy untuk opcode ganjil sudah ditangani engine, tetapi **query yang dijawab dnsproxy sebelum handler** (ANY → NOTIMP, jumlah pertanyaan ≠ 1 → FORMERR, ARPA privat dari klien non-privat dan deteksi rekursi → NXDOMAIN) tidak lewat ACL dan tidak dicatat; jawabannya tidak lebih besar dari request. "Satu catatan per query" berlaku untuk query yang sampai ke engine.
- Ctrl+C pada konsol Windows asli belum diuji langsung (hanya jalur pembatalan konteks); perilaku disk penuh belum diuji.
- Mode berkas `0600`/`0700` tidak ditegakkan di Windows (mewarisi ACL folder induk).
- Pemuat daftar, pemeriksaan CNAME, nama khusus, `selfcheck`, dan backend `native` memang belum ada (Fase 2 dan 4).

## Serah terima ke Fase 2

- Engine siap menerima daftar: `engine.NewListSet(allow, block []NamedSet)` + `Engine.SwapLists`; `After` tinggal diperluas dengan pemeriksaan CNAME; `Before` tinggal diberi langkah nama khusus. Paket `lists` hanya perlu membangun `DomainSet` per daftar (memakai `engine.ParseEntry`) dan memanggil `SwapLists`.
- `hecate run` mencetak catatan "daftar blokir belum dimuat" sampai pemuat daftar ada; hapus catatan itu di Fase 2. `hecate lists` masih stub.
- Kriteria Fase 2 yang menyentuh Fase 1: `hecate check` harus melakukan uji mandiri (SafeSearch, hulu, domain contoh) dengan klien DNS yang memakai port apa pun (jangan pakai `nslookup`).
- Sebelum Fase 6: perbaiki I1 (kolam TCP), tambahkan pembatas laju per IP (paket `ratelimit` dnsproxy atau milik sendiri), dan tinjau ulang SO_REUSEPORT di Linux/Android (dnsproxy mengaktifkannya, sehingga instance kedua bisa memakai port yang sama tanpa galat).
