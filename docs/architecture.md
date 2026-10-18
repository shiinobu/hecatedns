# xdns — Arsitektur

*Versi 1.5 | 2026-10-19 | Status: Fase 0 sampai Fase 5 selesai. Backend `native` lulus tes HP dan menjadi backend server rumah; xdns jalan otomatis saat boot lewat Task Scheduler. Uji 7 hari (S4) dipindah ke Fase 6 di VPS atas keputusan Master (lihat `phase-1-report.md` sampai `phase-5-report.md`, `test-report.md`) | Penulis: Claude & Gemini 4 Argon (keputusan desain didelegasikan penuh oleh Master)*

Dokumen ini adalah spesifikasi desain xdns. Urutan kerja ada di `roadmap.md`. Dasar riset dan sumbernya ada di `research.md` (rujukan "riset §N" di bawah menunjuk ke sana).

---

## 1. Tujuan, batas, dan kriteria sukses

**Tujuan.** Server DNS pribadi (non-komersial) yang memblokir iklan di browser HP Android Master (RedMagic 8S Pro): pop-up, pop-under, auto-redirect, dan click-redirect, termasuk redirect ke Play Store atau aplikasi Shopee. Server tidak memaksa Google SafeSearch dan tidak memakai aplikasi VPN di HP.

**Batas yang jujur** (riset §2–3). DNS tidak bisa memblokir 100% iklan browser. Yang tidak terjangkau:
- iklan yang disajikan dari domain yang sama dengan kontennya;
- skrip pemicu redirect dari domain situs itu sendiri;
- overlay klik yang membuka jendela dari skrip situs;
- kotak iklan kosong yang tertinggal.

Pelengkap terdokumentasi: Firefox + uBlock Origin. Plan B: APK (§16).

**Bukan tujuan versi 1:** UI web, pemeriksaan isi HTTPS, APK, DNS-over-QUIC, validasi DNSSEC, profil per klien, kontrol orang tua.

**Kriteria sukses**

| Kode | Kriteria | Cara ukur | Diukur di |
|---|---|---|---|
| S1 | Domain iklan pada daftar terblokir | `xdns check`: domain contoh tiap daftar dijawab `0.0.0.0`. Hasil adblock.turtlecute.org dan canyoublockit.com dicatat sebelum dan sesudah. Target awal: ≥ 90% host uji terblokir (disesuaikan setelah baseline) | Fase 3 |
| S2 | Pemicu redirect Play Store ditemukan dan dihentikan | Log query di sekitar waktu kejadian → aturan atau daftar → 10 percobaan ulang tanpa redirect yang sama | Fase 3 |
| S3 | SafeSearch tidak dipaksa oleh DNS | `www.google.com` tidak berantai ke `forcesafesearch.google.com`; self-check lulus | Fase 2 dan 3 |
| S4 | Stabil | 7 hari berjalan tanpa crash atau restart manual, memori stabil, latensi query lolos (cache miss) p95 < 150 ms (target awal) | Fase 6, di VPS (dipindah dari Fase 5 pada 2026-10-19: PC ini dimatikan tiap malam, jadi 7 hari tanpa putus tidak bisa dicapai di sini) |
| S5 | Salah blokir mudah diperbaiki | Satu perintah `xdns rule add allow <domain>` berlaku ≤ 10 detik tanpa restart | Fase 1 |

---

## 2. Keputusan desain

Semua ditetapkan Claude atas kewenangan penuh dari Master (2026-10-18). Master boleh membatalkan salah satunya kapan saja.

| # | Keputusan | Pilihan | Alasan |
|---|---|---|---|
| D1 | Bahasa | Go, `go 1.26.9` di `go.mod` | Satu binary, ekosistem DNS matang, sudah terpasang (1.26.4; toolchain naik otomatis). `dnsproxy` v0.86.0 mensyaratkan ≥ 1.26.8; dipakai 1.26.9 karena 1.26.8 terdampak enam kerentanan pustaka standar (GO-2026-6603, 6607, 6611, 6612, 6613, 6617) |
| D2 | Backend | Dua, bisa ditukar lewat konfigurasi: `dnsproxy` (AdGuard dnsproxy v0.86.0, Apache-2.0) dan `native` (kode sendiri di atas `miekg/dns` v1.1.73) | Permintaan Master untuk mencoba keduanya. Engine bersama membuat hasil keputusan sama dan bisa dipakai ulang di Plan B |
| D3 | Pencocokan domain | Peta domain, cocok sufiks (domain dan semua subdomainnya), snapshot tak berubah yang ditukar atomik | README HaGeZi: kelima format memblokir domain yang sama, dan aturan induk mencocokkan subdomain. Sederhana, cepat, aman konkuren |
| D4 | Daftar bawaan | HaGeZi Multi PRO, HaGeZi Pop-Up Ads, ABPindo `domain.txt` | Riset §4.1. PRO direkomendasikan penulisnya; ULTIMATE berisiko salah blokir |
| D5 | Jawaban terblokir | `null_ip` (A → `0.0.0.0`, AAAA → `::`), TTL 10 detik; tipe lain NODATA dengan SOA sintetis. Mode bisa diganti | Riset §4.3 |
| D6 | CNAME | Periksa setiap CNAME pada jawaban hulu; blokir bila ada target yang cocok daftar (kecuali diizinkan) | Riset §4.2 |
| D7 | HTTPS/SVCB dan nama khusus | Tipe 65 pada nama terblokir → NODATA. `_dns.resolver.arpa` → NODATA. `use-application-dns.net` → NXDOMAIN (opsional, dapat dimatikan) | Riset §4.4–4.5. Perilaku canary diverifikasi saat implementasi |
| D8 | Hulu | DoH ke Cloudflare `1.1.1.1`, lalu Google `8.8.8.8` (bukan varian penyaring/"family") | Terenkripsi sehingga ISP tidak ikut campur; tidak memaksa SafeSearch. Latensi diukur di Fase 3 |
| D9 | Konfigurasi | YAML ketat (`gopkg.in/yaml.v3`, kunci tak dikenal = galat) | Mudah dibaca; sudah ada di graf dependensi dnsproxy |
| D10 | Log query | JSONL harian, retensi 7 hari | Alur "reproduksi → baca log → blokir" (riset §3) |
| D11 | Koordinasi CLI dan server | Lewat file (aturan, cache daftar) dengan polling waktu ubah; tanpa soket admin | Sederhana, tanpa port tambahan, tidak menambah permukaan serangan |
| D12 | Pengujian | `testing` standar, tabel, fuzz untuk parser, suite kesesuaian untuk kedua backend, TDD | Aturan proyek: tulis tes dulu, cakupan ≥ 80% |
| D13 | Port saat pengembangan | `127.0.0.1:5300` | Port 5353 dipakai banyak proses (mDNS); UDP 53 dipegang ICS (§12) |
| D14 | UI | Tidak ada UI web di versi 1; log dan aturan lewat CLI | YAGNI; UI web masuk Fase 7 bila Master mau |
| D15 | Standar kode | Go idiomatik (`gofmt`, `go vet`, doc comment untuk simbol ekspor). Aturan khusus HackHub di `.claude/rules` tidak berlaku | Proyek ini bukan mod HackHub |
| D16 | Git | Repo lokal, trunk-based di `main`, commit konvensional | Proyek pribadi satu orang |

---

## 3. Gambaran besar

```
 Klien (HP / PC)
      │  DNS (UDP/TCP 53; nanti DoT 853)
      ▼
┌──────────────────────────────────────────────────────┐
│ BACKEND   backend: dnsproxy | native                 │
│   listener · daftar klien yang diizinkan (ACL)       │
│   cache · upstream terenkripsi (DoH/DoT)             │
└───────┬───────────────────────────────▲──────────────┘
   Before(q)                       After(q, jawaban)
        ▼                               │
┌──────────────────────────────────────────────────────┐
│ ENGINE FILTER  (satu, dipakai bersama)               │
│   aturan kustom · daftar blokir · cek CNAME          │
│   jawaban blokir · nama khusus · log query           │
└──────────────────────────────────────────────────────┘
        ▲                   ▲
  data/rules.txt     data/lists/*.txt  (diunduh berkala)
```

Prinsip: engine tidak tahu apa pun tentang jaringan, TLS, atau cache. Backend tidak tahu apa pun tentang daftar atau aturan. Keduanya hanya bertemu lewat tiga pemanggilan (`Before`, `After`, `Failed`).

---

## 4. Struktur proyek

```
xdns/
├── docs/                                       # dokumen proyek
│   ├── architecture.md  roadmap.md  research.md
│   ├── test-report.md                          # hasil tes HP, dibuat di Fase 3
│   └── superpowers/plans/                      # rencana tugas Fase 0 (arsip)
├── go.mod  go.sum  .gitignore  .gitattributes  README.md
├── cmd/xdns/
│   └── main.go                 # titik masuk; subperintah: run, check, log, rule, lists
├── internal/
│   ├── config/                 # skema YAML, pemuat ketat, validasi
│   ├── engine/                 # filter tanpa jaringan
│   │   ├── engine.go           #   Engine, Query, Decision; Before/After/Failed
│   │   ├── acl.go              #   ACL klien (allowed_clients), langkah pertama Before
│   │   ├── matcher.go          #   DomainSet (cocok sufiks) + snapshot atomik
│   │   ├── rules.go            #   aturan kustom: parser + muat ulang
│   │   ├── response.go         #   pembentuk jawaban blokir (4 mode)
│   │   ├── cname.go            #   pemeriksaan rantai CNAME
│   │   ├── special.go          #   nama khusus (canary, DDR)
│   │   └── health.go           #   peringatan bila hulu gagal beruntun (kedua backend)
│   ├── lists/                  # sumber daftar: unduh, parser, cache disk, penjadwal
│   ├── qlog/                   # log query: penulis JSONL, rotasi, pembaca
│   ├── runlog/                 # log operasional xdns.log: berkas berbatas ukuran + Tee ke layar
│   ├── backend/
│   │   ├── backend.go          #   antarmuka Backend, Deps, registri
│   │   ├── dnsproxybe/         #   adapter AdGuard dnsproxy (pendekatan 4)
│   │   ├── native/             #   server + upstream + cache sendiri (pendekatan 1)
│   │   └── conformance/        #   suite tes yang sama untuk kedua backend
│   └── selfcheck/              # uji mandiri: SafeSearch, hulu, domain contoh
├── configs/xdns.example.yaml
├── scripts/                    # dnsquery.ps1, compare (pembanding backend), autostart.ps1 (Task Scheduler)
├── testdata/                   # daftar contoh, fixture
└── data/                       # data runtime, di-.gitignore
    ├── rules.txt  lists/  logs/  tls/
```

Aturan ukuran: berkas 200–400 baris tipikal (batas lunak 800), fungsi di bawah 50 baris, nesting maksimal 4 tingkat. Paket `engine` tidak boleh mengimpor `backend`, `lists`, atau `qlog` secara terbalik: `lists` dan `qlog` memberi data ke engine lewat antarmuka kecil yang didefinisikan di `engine`.

---

## 5. Komponen dan kontrak

### 5.1 Engine

```go
package engine

type Query struct {
    Req    *dns.Msg    // pesan dari miekg/dns v1
    Client netip.Addr
    Proto  string      // "udp" | "tcp" | "tls" | "https"
}

type Verdict uint8
const ( Pass Verdict = iota; Block )

type Decision struct {
    Verdict Verdict
    Resp    *dns.Msg // terisi bila Block
    Reason  string   // "rule" | "list" | "cname" | "special" | "acl" | "invalid"
    Source  string   // nama daftar atau baris aturan yang cocok
    Via     string   // target CNAME yang cocok (bila Reason == "cname")
}

type Meta struct { Upstream string; Latency time.Duration; Cached bool }

func (e *Engine) Before(ctx context.Context, q Query) Decision
func (e *Engine) After(ctx context.Context, q Query, resp *dns.Msg, m Meta) Decision
func (e *Engine) Failed(ctx context.Context, q Query, err error)
```

Jaminan:
- aman dipakai bersamaan oleh banyak goroutine;
- tidak melakukan I/O jaringan;
- tidak mengubah `q.Req` maupun `resp` yang diterima (membuat salinan bila perlu);
- selalu menulis tepat satu catatan log per query yang sampai ke engine (di `Before` bila diblokir, di `After` atau `Failed` bila diteruskan). Beberapa jawaban diberikan dnsproxy sebelum handler dan tidak lewat engine (lihat §5.3);
- bila belum ada daftar yang termuat, `Before` meloloskan query (fail-open) dan mencatat peringatan satu kali;
- panic di dalam engine ditangkap: gagal-buka (`Pass`) untuk keputusan filter, gagal-tutup (REFUSED) untuk pemeriksaan ACL, dan hanya mengorbankan catatan log bila panic terjadi saat menulisnya.

### 5.2 Backend

```go
package backend

type Backend interface {
    Name() string
    Run(ctx context.Context) error // blok sampai ctx selesai atau galat fatal
}

type Deps struct { Cfg config.Config; Engine *engine.Engine; Log *slog.Logger }
type Factory func(Deps) (Backend, error)

type Registry map[string]Factory   // dibuat oleh pemanggil; paket backend tidak mengimpor implementasinya

// Opsional: backend yang dapat melaporkan alamat dengarnya (tes memakai port acak, `run` mencetak alamat).
type Bound interface { Ready() <-chan struct{}; Addr(network string) net.Addr }
```

`cmd/xdns` (`defaultRegistry`) dan suite kesesuaian membangun `Registry` berisi `dnsproxy` (dan `native` mulai Fase 4); dengan begitu tidak ada siklus impor. `main.go` memilih backend dari `backend:` pada konfigurasi. Nama tak dikenal = galat saat start yang menyebut nama yang dikenal. Sebuah backend hanya dijalankan sekali (`Run` kedua adalah galat).

### 5.3 Adapter `dnsproxy` (pendekatan 4)

Alur satu query: `proxy.Config.RequestHandler` adalah *interface* `Handler` (bukan fungsi); adapter adalah satu handler pembungkus yang memanggil `Before`; bila `Block`, jawaban sintetis diisikan ke `dctx.Res`. Bila `Pass`, ia memanggil `p.Resolve(dctx)` (upstream + cache), lalu `After` memeriksa `dctx.Res` dan boleh menggantinya dengan jawaban blokir. Pemetaan konfigurasi: `listen`, `upstreams`, `bootstrap`, `cache` → `proxy.Config`. `allowed_clients` tidak dipetakan: dnsproxy tidak punya ACL klien, jadi engine menegakkannya sendiri (§6 langkah 1) dan kedua backend berperilaku sama.

Tipe pesan DNS yang dipakai dnsproxy sama dengan engine (`github.com/miekg/dns`), jadi tidak ada konversi. Fakta yang diverifikasi dari kode v0.86.0 (dengan nomor baris) ada di `.agents/notes/dnsproxy-api-facts.md`; yang penting bagi desain:

- **Cache menyimpan jawaban hulu (dalam bentuk terkemas) sebelum `After`**, jadi jawaban blokir hasil `After` tidak ikut tersimpan dan pemeriksaan CNAME berjalan pada setiap cache hit.
- Upstream DoH berupa IP (`https://1.1.1.1/dns-query`) tidak perlu bootstrap.
- Mode hulu `load_balance`: pilihan acak berbobot dengan fail-over (bukan "berurutan"); `MaxGoroutines` 256, batas waktu hulu 5 detik, `RefuseAny` dan deduplikasi request kembar diaktifkan eksplisit (bawaan kodenya mati).
- Paket `proxy` tidak memakai `recover()`; adapter memasangnya sendiri per tahap (filter, `Resolve`, jawaban). Panic sebelum `Before` selesai → REFUSED (status ACL tidak diketahui); panic di `Resolve` → SERVFAIL; panic setelahnya → jawaban dipertahankan.
- `p.Resolve` mengembalikan `upstream.ErrNoUpstreams` untuk PTR alamat privat (tidak ada hulu privat) dan dnsproxy sudah mengisi NXDOMAIN; adapter mencatatnya sebagai query yang terjawab, bukan kegagalan.
- **dnsproxy menjawab sebagian query sebelum handler kita**, tanpa ACL dan tanpa catatan log: ANY → NOTIMP, jumlah pertanyaan ≠ 1 → FORMERR, ARPA privat dari klien non-privat dan deteksi rekursi → NXDOMAIN, paket yang ditandai respons dibuang. Jawabannya tidak pernah lebih besar dari request. Opcode selain QUERY *tidak* ditolak dnsproxy (diteruskan ke hulu), sehingga engine menjawabnya sendiri (§6 langkah 2).
- **Setiap koneksi TCP memegang satu slot `MaxGoroutines` selama hidupnya** (menganggur 10 detik, diperpanjang tiap request), dan UDP memakai semaphore yang sama. Satu host di LAN dapat menahan 256 koneksi dan menghentikan layanan; diterima untuk jaringan rumah, **wajib diperbaiki sebelum Fase 6** (kolam TCP sendiri atau batas koneksi per IP; roadmap Fase 6 tugas 8).
- Listener dnsproxy memakai `SO_REUSEPORT` di Linux/Android, sehingga instance kedua bisa mengikat port yang sama tanpa galat; di Windows bind kedua gagal.

### 5.4 Backend `native` (pendekatan 1)

Dibangun di Fase 4 dan diperbaiki setelah review penuh di Fase 5 (paket `internal/backend/native`, cakupan tes 93,8%). Sejak 2026-10-19 backend ini yang melayani server rumah.

| Berkas | Isi |
|---|---|
| `native.go` | Pabrik `New` dan `Run` (satu kali pakai); mengikat soket lebih dulu sehingga galat bind kembali seketika; implementasi `backend.Bound` |
| `server.go` | Listener UDP dan TCP lewat `dns.Server` pada setiap alamat `listen`; `acceptMsg` meneruskan opcode selain QUERY ke engine (supaya dijawab NOTIMP *dan* dicatat) dan mengabaikan paket berbit respons serta pesan yang *headernya* menyebut jumlah pertanyaan atau record yang tidak wajar (tanpa jawaban dan tanpa log; dnsproxy menjawab FORMERR). Pesan dengan header wajar tetapi badan rusak tidak sampai ke handler: miekg menjawab FORMERR sendiri sebelum batas laju, ACL, dan log, dan jawaban itu tidak pernah lebih besar dari request (dijaga tes, `28e0769`); batas koneksi TCP sendiri (`limitListener`: 64 total, 8 per alamat klien); `tolerantReader` agar kegagalan baca UDP (di Windows: datagram lebih besar dari buffer) tidak menghentikan server, kecuali soket memang ditutup; shutdown menutup soket juga supaya server yang dibatalkan sebelum sempat mulai tidak jalan terus |
| `handler.go` | Batas laju → `Before` → cache → hulu → `After`; `Failed` bila semua hulu gagal; ANY dijawab NOTIMP; pemulihan panik per tahap (sebelum engine meloloskan: REFUSED; di cache atau hulu: SERVFAIL; sesudah jawaban ada: jawaban dipertahankan); hanya pertukaran ke hulu yang memakai slot (256 total, 64 per klien; di atas batas dijawab SERVFAIL dan dicatat lewat `Failed`), jadi cache hit dan jawaban blokir tetap jalan saat hulu lambat; tiap query punya batas waktu 11 detik dan dibatalkan saat shutdown; ukuran UDP mengikuti EDNS klien (512–1232) |
| `upstream.go`, `upstream_plain.go`, `upstream_doh.go`, `upstream_parse.go`, `bootstrap.go` | Kolam hulu: pilihan acak berbobot 1/srtt dengan failover ke yang lain; plain UDP (diulang lewat TCP bila terpotong) dan TCP; DoH (POST RFC 8484, ID 0, HTTP/2, tanpa proksi lingkungan, tanpa redirect, badan ≤ 65535 byte, tipe `application/dns-message`); nama host DoH di-resolve lewat resolver bootstrap saat koneksi baru dibuat, dengan anggaran waktu sendiri yang dibagi: tiap server bootstrap dan tiap alamat hasilnya mendapat sebagian dari sisa waktu, jadi satu server yang diam tidak menghabiskan seluruh batas waktu (net/http membuang batas waktu request saat dial, sejak Go 1.23). Query ke hulu UDP/TCP polos menutup koneksinya saat konteks dibatalkan, karena miekg hanya menghormati batas waktu, bukan pembatalan. Jawaban diterima hanya bila bit QR, ID, opcode, dan pertanyaan cocok. Hulu yang menjawab SERVFAIL atau REFUSED dianggap gagal dan hulu berikutnya ditanya; bila semua begitu, jawaban hulu itu yang diteruskan. **DoT sebagai hulu belum ada** (`tls://` ditolak dengan pesan jelas); `udp://` dan `tcp://` harus berupa IP |
| `cache.go` | LRU berbatas byte (ukuran paket + biaya tetap per entri dan per record); kunci (nama huruf kecil, tipe, kelas, bit DO, bit CD); TTL dijepit `min_ttl`/`max_ttl`; cache negatif (NXDOMAIN, NODATA) memakai min(TTL SOA, minimum SOA); hanya NOERROR dan NXDOMAIN tanpa TC yang disimpan; TTL tersaji = sisa umur entri; OPT hulu dibuang |
| `ratelimit.go` | Token bucket per IP klien (200/detik, burst 500; dinaikkan dari 100/200 setelah tes HP: halaman tes iklan mengirim ±255 kueri dalam setengah detik), tabel dibatasi 4096 klien (tabel penuh disapu paling sering sekali per detik; klien baru yang tidak muat dilayani tanpa dilacak, artinya pada tabel penuh sumber baru tidak dibatasi sama sekali: terukur 1.200 dari 1.200 query dijawab, temuan I2, ditunda ke Fase 6); klien yang kena batas dibuang tanpa jawaban (jawaban ke alamat palsu = amplifier). Query yang dibuang tidak masuk log query |

Cache menyimpan jawaban hulu *sebelum* `After`, sama seperti dnsproxy, sehingga pemeriksaan CNAME tetap berjalan pada setiap cache hit. Query ke hulu dibangun baru dari pertanyaan klien (RD, CD, EDNS 1232 dengan bit DO bila klien memintanya): cookie, client subnet, dan padding klien tidak ikut keluar.

**Perbedaan yang diketahui dengan adapter `dnsproxy`** (semuanya tanpa dampak untuk klien privat di LAN):
- dnsproxy menjawab ANY, jumlah pertanyaan ≠ 1, dan deteksi rekursi *sebelum* handler (tanpa ACL dan tanpa log). `native` mengabaikan paket yang headernya menyebut jumlah pertanyaan atau record tidak wajar di `acceptMsg` (tanpa jawaban, tanpa log; koreksi 2026-10-19: versi sebelumnya menulis FORMERR), tetapi menjawab ANY setelah `Before`, sehingga ACL berlaku dan query tercatat.
- **Sudah sama sejak Fase 5 (`dcc5347`):** reverse lookup (PTR, SOA, NS) untuk nama di zona RFC 6303 (10/8, 172.16/12, 192.168/16, 127/8, 169.254/16, fc00::/7, fe80::/10) dijawab lokal dengan NXDOMAIN dan SOA oleh kedua backend, untuk semua klien, memakai fungsi golibs yang sama (`netutil.IsLocallyServed`). Review penuh menunjukkan `native` sebelumnya meneruskannya ke Cloudflare/Google, termasuk dari klien privat (misalnya `nslookup 192.168.1.99` di Windows); catatan lama "baru relevan di Fase 6" keliru.
- `native` tidak mendeduplikasi query identik yang sedang berjalan (`PendingRequests` milik dnsproxy).
- Slot TCP: `native` punya batas koneksi TCP sendiri (64 total, 8 per alamat klien) dan slot hulu sendiri, sehingga masalah Q4 tidak berlaku. Belum ada pemeriksaan ACL saat accept. Koreksi 2026-10-19: batas baca 5 detik hanya berlaku sampai query pertama; sesudahnya berlaku batas menganggur 10 detik dan 100 query per koneksi, jadi satu host di luar ACL yang mengirim satu query tiap 9 detik bisa mempertahankan satu koneksi sekitar 16,5 menit dan membukanya lagi (terukur reviewer; 8 koneksi per alamat). Tidak berbahaya hari ini (firewall `LocalSubnet` dan ACL /24); Fase 6: periksa ACL saat accept atau tutup koneksi setelah REFUSED.
- Pesan dengan header wajar tetapi badan rusak dijawab FORMERR oleh miekg sebelum batas laju, ACL, dan log (tidak pernah lebih besar dari request, jadi bukan penguat; dijaga tes). Hanya relevan bila UDP 53 dibuka ke internet.
- Kunci IPv6 pada pembatas laju, batas TCP per alamat, dan gerbang hulu per klien memakai alamat penuh, bukan /64; ketiganya diperbaiki bersama di Fase 6, bersama temuan I2 (tabel pembatas penuh).

### 5.5 Daftar blokir (`internal/lists`)

Lihat §7.

### 5.6 Log query (`internal/qlog`)

Lihat §9.

---

## 6. Alur satu query

`Before`:
1. **ACL.** Klien di luar `allowed_clients` → `Block` dengan jawaban REFUSED (alasan `acl`), tanpa mencocokkan daftar. `allowed_clients` wajib berisi minimal satu CIDR (validasi konfigurasi), jadi daftar kosong tidak pernah berarti "izinkan semua".
2. **Validasi.** Harus satu pertanyaan dengan opcode QUERY. Selain itu engine sendiri menjawab FORMERR (bukan satu pertanyaan) atau NOTIMP (opcode lain) dengan alasan `invalid`, dan mencatatnya; dnsproxy meneruskan opcode ganjil ke hulu melewati semua aturan, jadi ini tidak boleh diserahkan ke backend. Langkah ACL tetap lebih dulu.
3. **Normalisasi.** Huruf kecil, buang titik akhir. Nama IDN dianggap sudah dalam punycode (bentuk kawat DNS).
4. **Nama khusus** (bila diaktifkan): `use-application-dns.net` → blokir NXDOMAIN; `_dns.resolver.arpa` → NODATA.
5. **Aturan kustom** (milik pemilik, selalu menang atas daftar): cocok `allow` → `Pass` (alasan `allow`); lalu cocok `block` → `Block` (alasan `rule`). Satu nama dapat memuat keduanya; entri yang lebih spesifik menang di dalam satu himpunan.
6. **Daftar:** cocok daftar berjenis `allow` → `Pass` (alasan `allow`); lalu daftar berjenis `block` → `Block` dengan `Source` terisi.

Urutan prioritas: aturan kustom `allow` > aturan kustom `block` > daftar `allow` > daftar `block`. Pencocokan memakai sufiks pada batas label: `contoh.com` juga mencocokkan `a.b.contoh.com`, bukan `kontoh.com`.
7. Selain itu `Pass`.

Backend meneruskan query yang `Pass` ke cache/hulu. Lalu `After`:
1. **Jawaban tidak ada atau rcode bukan NOERROR:** catat dan kembalikan apa adanya.
2. **Rantai CNAME:** untuk setiap CNAME di bagian jawaban, normalisasi targetnya. Bila target dikecualikan `allow`, lanjut. Bila cocok blokir, kembalikan `Block` untuk pertanyaan asli (alasan `cname`, `Via` = target).
3. Selain itu catat sebagai lolos dengan hulu, rcode, dan latensi.

**Jawaban blokir** (`response.go`), semuanya menyalin ID dan bagian pertanyaan, mengisi RA, menjaga bit RD, meniru OPT bila request memilikinya, dan dikompresi (tanpa kompresi jawaban untuk nama panjang bisa dua kali ukuran request). Penolakan ACL (REFUSED) tidak mengisi RA karena klien itu tidak boleh memakai rekursi:

| Mode | Perilaku |
|---|---|
| `null_ip` (bawaan) | A → `0.0.0.0`, AAAA → `::` (TTL `block.ttl`); tipe lain, termasuk HTTPS/SVCB (65), → NOERROR kosong + SOA sintetis |
| `nxdomain` | NXDOMAIN + SOA sintetis |
| `nodata` | NOERROR kosong + SOA sintetis |
| `refused` | REFUSED |

SOA sintetis memakai TTL dan minimum sama dengan `block.ttl` agar cache negatif klien singkat (aturan baru cepat berlaku).

---

## 7. Daftar blokir

| Nama | URL | Format | Entri (diunduh 2026-10-18 malam) |
|---|---|---|---|
| `hagezi-pro` | `https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/wildcard/pro-onlydomains.txt` | `domains` | 202.297 (ditolak 0) |
| `hagezi-popups` | `https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/wildcard/popupads-onlydomains.txt` | `domains` | 47.668 (ditolak 0; 113 unik terhadap PRO) |
| `abpindo` | `https://raw.githubusercontent.com/ABPindo/indonesianadblockrules/master/subscriptions/domain.txt` | `domains` | 124 (ditolak 0; lokal Indonesia) |

Ketiganya sudah diunduh dan diverifikasi pada binary asli (total **250.089 entri** sebelum de-duplikasi): satu domain per baris, komentar berawalan `#`, tanpa spasi pada baris data. Header HaGeZi menyatakan `Expires: 8 hours`; ABPindo `4 days`.

**Nama sumber** menjadi nama berkas (`data/lists/<nama>.txt`), jadi dibatasi: 1–64 karakter huruf, angka, `-`, atau `_`, diawali huruf atau angka, bukan nama perangkat Windows (CON, PRN, AUX, NUL, COM1–COM9, LPT1–LPT9), dan unik tanpa membedakan huruf besar-kecil. Sumber tanpa `enabled` dianggap aktif (diterapkan di Fase 1).

**Format yang didukung versi 1:** `domains` (satu domain per baris) dan `hosts` (`0.0.0.0 domain` atau `127.0.0.1 domain`, baris bawaan `localhost` diabaikan). Sintaks adblock penuh (`||domain^$opsi`) tidak didukung; masuk Fase 7 bila diperlukan.

**Parser** (daftar dianggap masukan tak tepercaya):
- abaikan baris kosong dan komentar (`#`, `!`, `;`), buang komentar di ujung baris, dan kupas UTF-8 BOM di baris pertama;
- huruf kecil ASCII; buang awalan `*.` dan titik akhir;
- validasi sintaks nama lewat `engine.ParseEntry`: label 1–63 karakter, total ≤ 253, hanya `a-z 0-9 - _`;
- tolak alamat IP dan entri satu label (agar satu baris rusak seperti `com` tidak memblokir seluruh TLD);
- batas keras: panjang baris < 4.096 byte (baris lebih panjang dikuras tanpa alokasi besar dan dihitung sebagai ditolak), aliran terdekode ≤ 64 MiB (`ErrListTooLarge`), dan jumlah domain unik ≤ 1.000.000 per daftar (`ErrTooManyEntries`); daftar dengan 0 domain valid ditolak (`ErrEmptyList`) agar halaman galat HTML tidak pernah menghapus perlindungan;
- bangun `engine.DomainSet` sekali jalan lewat `engine.DomainSetBuilder` tanpa peta atau slice perantara ganda.

**Pembaruan:**
- saat start: muat salinan cache di `data/lists/<nama>.txt` lebih dulu (cepat), lalu segarkan di latar belakang;
- interval `lists.update_interval` (bawaan 24 jam) dengan jitter hingga +10%, ditambah polling ukuran + waktu ubah berkas cache setiap 5 detik agar `xdns lists update` langsung dimuat server yang sedang berjalan tanpa restart (D11);
- permintaan `If-None-Match` dikirim hanya bila `<nama>.txt` dan `<nama>.meta.json` keduanya ada; saat server membalas `304 Not Modified` dan cap berkas disk tidak berubah, `Manager` memakai kembali `*engine.DomainSet` yang sudah ada di memori tanpa mem-parse ulang berkas dari disk;
- hanya `https://` (termasuk pada hingga 5 redirect; redirect ke-6 atau redirect ke `http://` ditolak); batas waktu 30 detik; batas ukuran 64 MiB per daftar;
- nama host URL daftar di-resolve memakai server `bootstrap` secara berurutan lewat `miekg/dns` (UDP port 53 dengan fallback TCP bila terpotong) sehingga unduhan daftar tidak pernah berputar lewat `xdns` sendiri dan tetap berpindah ke bootstrap kedua bila bootstrap pertama diam/mati;
- tulis ke berkas sementara lalu `rename` (atomik dengan coba-ulang singkat di Windows); simpan metadata (`etag`, `fetched_at`, `count`, `rejected`) di `data/lists/<nama>.meta.json`;
- bila unduhan gagal atau berkas rusak: pertahankan salinan lama dan catat galat;
- setelah ada perubahan, bangun snapshot baru lalu tukar atomik (`atomic.Pointer`); snapshot lama tidak pernah diubah.

**Dua snapshot terpisah** (daftar dan aturan kustom) agar perubahan aturan tidak membangun ulang ratusan ribu entri.

**Anggaran sumber daya** (terukur di Fase 2 pada 250.000–250.089 entri): memori heap `DomainSet` bertambah **16,15–16,29 MiB** (`Sys` runtime Go 38–43 MiB; proses `xdns run` hidup: WorkingSet **71,74–74,20 MiB**, Private **103,35–105,25 MiB**, di bawah batas 150 MB), latensi pencarian rata-rata **215–250 ns/op** (0 alokasi), **p95 = 1,04–1,07 µs**, **p99 = 2,01 µs** (jauh di bawah batas 50 µs). Peta string standar Go (`map[string]struct{}`) sudah sangat memadai tanpa perlu diganti peta hash 64-bit.

---

## 8. Aturan kustom dan CLI

`data/rules.txt`:

```
# komentar
block ads.contoh-iklan.com
allow cdn.situs-yang-rusak.com
```

Semantik sama dengan daftar: berlaku untuk domain dan semua subdomainnya. Urutan prioritas: `allow` kustom → `block` kustom → daftar `allow` → daftar `block`. Baris yang salah dilewati dengan catatan nomor barisnya, tidak menggagalkan muat ulang. Server memeriksa waktu ubah berkas setiap 2 detik (`rules.poll_interval`).

Subperintah (`xdns <perintah>`):

| Perintah | Fungsi |
|---|---|
| `run [-config f] [-backend dnsproxy\|native]` | Jalankan server |
| `check [-config f]` | Validasi konfigurasi, muat daftar dari cache, uji mandiri (§11), cetak LULUS/GAGAL |
| `log [-since 10m] [-grep teks] [-blocked] [-around 14:32 -window 30s]` | Baca log query terformat |
| `rule add block\|allow <domain>` / `rule remove <domain>` / `rule list` | Kelola `rules.txt` secara atomik |
| `lists status` / `lists update` | Lihat status atau paksa unduh ulang daftar |

Alur pelacakan redirect: picu redirect di HP → catat jam → `xdns log -around 14:32 -window 30s` → tentukan domain pemicu → `xdns rule add block <domain>` → ulangi.

---

## 9. Log query

`data/logs/queries-YYYYMMDD.jsonl`, satu JSON per baris, waktu lokal dengan offset:

```json
{"ts":"2026-10-18T14:32:07.412+07:00","client":"192.168.1.23","proto":"udp","qname":"ads.contoh.com","qtype":"A","action":"block","reason":"list","source":"hagezi-popups","rcode":"NOERROR","ms":0}
{"ts":"...","client":"192.168.1.23","proto":"udp","qname":"www.contoh.com","qtype":"A","action":"pass","upstream":"https://1.1.1.1/dns-query","rcode":"NOERROR","ms":38,"cached":false}
{"ts":"...","qname":"smetrics.situs.com","action":"block","reason":"cname","source":"hagezi-pro","via":"x.pelacak.net"}
```

- **Log operasional** (peringatan dan galat server, bukan query): ditulis ke layar *dan*, bila `log.enabled`, ke `<log.dir>/xdns.log` (`internal/runlog`), supaya tugas terjadwal tanpa konsol tetap meninggalkan jejak. Berkas dibatasi 5 MB ditambah satu cadangan `xdns.log.1`, jadi paling banyak sekitar 10 MB. Gagal menulis berkas hanya menjadi peringatan di layar, tidak pernah menghentikan server. Galat yang mengakhiri server (misalnya gagal mengikat `192.168.1.99:53` karena Wi-Fi belum siap saat boot) ikut ditulis ke berkas ini sebelum ditutup; **galat memuat konfigurasi tidak bisa**, karena folder log berasal dari konfigurasi itu, jadi jalankan `xdns check` setelah mengubah `xdns.yaml`. Bila rotasi gagal (di Windows, program lain yang membuka `xdns.log`, misalnya `Get-Content -Wait`, menghalangi penggantian nama), baris tetap ditambahkan ke berkas sekarang sampai dua kali batas, lalu dibuang sampai rotasi berhasil lagi; berkas yang gagal dibuka ulang dibuka lagi pada penulisan berikutnya;
- rotasi harian menurut tanggal lokal; retensi `log.retention_days` (bawaan 7, hari ini ikut dihitung); hanya berkas biasa `queries-YYYYMMDD.jsonl` yang pernah dihapus, saat start, saat rotasi, dan sesaat setelah tengah malam;
- penulisan lewat saluran berpenyangga: bila penuh, catatan dibuang dan penghitung `dropped` dicatat; query tidak pernah menunggu disk;
- **disk penuh** (diperbaiki di Fase 5, `73647aa`): `bufio.Writer` menyimpan galat pertamanya, sehingga satu saat disk penuh dulu mengakhiri log untuk sisa hari itu dan catatan yang hilang tetap dihitung tertulis. Sekarang penyangga di-reset setelah tulis atau flush gagal, catatan yang belum terkonfirmasi dihitung `dropped`, dan catatan berikutnya mulai di baris baru (tulis yang gagal bisa meninggalkan setengah baris). Diuji dengan disk palsu yang penuh lalu lega; belum diuji pada drive penuh yang asli;
- **batas ukuran harian** `log.max_mb_per_day` (bawaan 256, minimal 1): catatan yang melewati batas dibuang dan dihitung, peringatan satu kali per hari, ukuran berkas yang sudah ada ikut dihitung setelah restart. Tanpa batas, satu pengirim UDP bisa menulis sekitar 14 MB/detik lewat penolakan ACL yang dicatat satu per query;
- nama dan galat disanitasi (karakter kontrol dan bidi diganti `?`), baris di atas 64 KiB dilewati pembaca; `xdns log` membaca log yang sedang ditulis server (di Windows ukuran berkas yang terbuka dilaporkan 0, jangan jadikan patokan);
- cincin memori 5.000 catatan terakhir disiapkan untuk UI di Fase 7;
- log berisi riwayat domain yang dibuka; berada hanya di PC ini dan dapat dimatikan (`log.enabled: false`).

---

## 10. Konfigurasi

`configs/xdns.example.yaml`:

```yaml
backend: dnsproxy                # dnsproxy | native

listen:
  - 127.0.0.1:5300               # pengembangan; tes HP (Fase 3): ganti ke alamat LAN PC, mis. 192.168.1.99:53

allowed_clients:                 # CIDR; di luar ini dijawab REFUSED
  - 127.0.0.0/8
  - "::1/128"
  - 10.0.0.0/8
  - 172.16.0.0/12
  - 192.168.0.0/16

upstreams:                       # satu dipilih acak berbobot, lainnya cadangan bila gagal
  - https://1.1.1.1/dns-query
  - https://8.8.8.8/dns-query
bootstrap: [1.1.1.1, 8.8.8.8]    # untuk upstream berupa nama host

cache:
  enabled: true
  size_mb: 16
  min_ttl: 0s
  max_ttl: 1h

block:
  mode: null_ip                  # null_ip | nxdomain | nodata | refused
  ttl: 10s

special_names:
  firefox_canary: true           # use-application-dns.net -> NXDOMAIN
  ddr: true                      # _dns.resolver.arpa -> NODATA

lists:
  update_interval: 24h
  sources:
    - { name: hagezi-pro,    kind: block, format: domains, enabled: true,
        url: "https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/wildcard/pro-onlydomains.txt" }
    - { name: hagezi-popups, kind: block, format: domains, enabled: true,
        url: "https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/wildcard/popupads-onlydomains.txt" }
    - { name: abpindo,       kind: block, format: domains, enabled: true,
        url: "https://raw.githubusercontent.com/ABPindo/indonesianadblockrules/master/subscriptions/domain.txt" }

rules:
  file: data/rules.txt
  poll_interval: 2s

log:
  enabled: true
  dir: data/logs
  retention_days: 7
  max_mb_per_day: 256            # batas ukuran berkas log per hari; sisanya dibuang dan dihitung

data_dir: data
```

Validasi saat start (kunci tak dikenal atau berulang, lebih dari satu dokumen YAML, `allowed_clients` kosong (untuk mengizinkan semua tulis `0.0.0.0/0` dan `::/0`), CIDR tak sah, `listen` berupa nama host, upstream bernama host tanpa `bootstrap`, URL daftar bukan `https`, nama sumber tak aman sebagai nama berkas atau ganda, `mode` tak dikenal, backend tak dikenal, TTL negatif → galat dengan pesan jelas dan keluar kode ≠ 0; semua masalah dilaporkan sekaligus). Jalur relatif dihitung dari lokasi berkas konfigurasi. Aturan yang ditambahkan di Fase 1 dan Fase 2: `enabled:` yang dihilangkan atau kosong (YAML null) berarti aktif; `rules.file` dan `log.dir` (bila log aktif) wajib terisi; `listen` unik dan port ≠ 0; TTL dibatasi 2147483647 detik sebelum konversi ke `uint32`; `cache.max_ttl: 0` berarti tanpa batas atas; `log.max_mb_per_day` minimal 1; kunci gabung YAML `<<` ditolak di dalam sumber daftar; `LoopWarnings()` (bagian dari `Warnings()`) memperingatkan bila `upstreams` atau `bootstrap` mengarah ke alamat `listen` sendiri (termasuk kombinasi wildcard/loopback dan `localhost`), dan `xdns check` menggagalkan uji mandiri bila ditemukan loop itu. Kunci yang tidak ditulis memakai nilai bawaan yang sama dengan contoh ini (`config.Default()`); daftar blokir tidak punya nilai bawaan, jadi tanpa sumber daftar yang aktif `xdns check` memberi peringatan.

---

## 11. SafeSearch

xdns **tidak pernah** menulis ulang `www.google.com`. Syarat agar hasilnya benar:
- hulu bukan resolver yang memaksa SafeSearch (karena itu varian "family" Cloudflare/AdGuard dilarang sebagai bawaan);
- hulu terenkripsi supaya ISP atau router tidak membajak jawaban DNS biasa;
- tidak ada aturan atau daftar yang memetakan Google ke `forcesafesearch.google.com`.

`xdns check` (`internal/selfcheck`) melakukan uji mandiri: resolve `www.google.com` (tipe A) lewat setiap hulu yang dikonfigurasi dan memastikan minimal satu hulu menjawab rekaman A dengan NOERROR serta rantai CNAME pada setiap hulu tidak memuat `forcesafesearch.google.com` (atau subdomainnya); bila ada yang memaksa SafeSearch, uji mandiri gagal dan menyebut hulu mana. Selain itu `selfcheck` memuat daftar blokir yang sudah ter-cache di disk dan memverifikasi bahwa satu domain contoh milik masing-masing daftar benar-benar diblokir oleh engine (serta memberi peringatan bila berkas cache belum diunduh lewat `xdns lists update`). Batas: bila SafeSearch dikunci lewat pengaturan akun Google atau proxy jaringan, DNS tidak dapat membatalkannya.

---

## 12. Lingkungan PC ini (fakta terukur 2026-10-18)

- Windows 11 Pro; Go 1.26.4 terpasang, modul memakai 1.26.9 lewat `GOTOOLCHAIN=auto`; git 2.54.0; gcc tersedia lewat w64devkit sehingga `go test -race` berjalan (set `CGO_ENABLED=1` per perintah, jangan global).
- IP LAN PC `192.168.1.99` (dipesan di router ZTE F663NV3a pada 2026-10-18; sebelumnya DHCP `192.168.1.10`), gateway `192.168.1.1`; HP memakai IP statis `192.168.1.150`. Adapter virtual `172.25.144.1` adalah Hyper-V "Default Switch" (Hyper-V aktif dan WSL2 terpasang). Wi-Fi di PC berkategori Public.
- **UDP `0.0.0.0:53` dipegang layanan Internet Connection Sharing (ICS / `SharedAccess`, svchost)**; TCP 53 bebas. Android tidak bisa menentukan port DNS di pengaturan Wi-Fi, jadi tes dari HP memerlukan UDP 53 di IP LAN PC. **Terverifikasi di Fase 3:** bind khusus ke `192.168.1.99:53` (UDP dan TCP) berhasil walau ICS memegang wildcard, jadi ICS tidak perlu dihentikan. Dua aturan firewall masuk (grup `xdns`, UDP dan TCP 53, hanya ke `192.168.1.99` dari `LocalSubnet`, program `bin\xdns.exe`).
- UDP 5353 dipakai banyak proses (mDNS). Port 5300 dan 853 bebas.
- **`nslookup` Windows menerima `-port` tetapi mengabaikannya** (selalu memakai port 53); pakai `scripts/dnsquery.ps1 <nama> -Port <port>` atau klien miekg/dns.
- Rentang port yang dikecualikan Windows (Hyper-V/WinNAT, mis. UDP 49674-49973) membuat bind gagal dengan WSAEACCES; tes tidak boleh mengandaikan port TCP dan UDP yang sama selalu sama-sama bebas.
- **Jalan otomatis saat boot (Fase 5):** `scripts/autostart.ps1 install` (PowerShell Administrator) membuat tugas Task Scheduler `xdns`: pemicu startup dengan tunda 30 detik, diulang tiap 1 menit selama 365 hari sejak tiap boot, akun sekarang lewat S4U (jalan tanpa login, hak biasa: bukan SYSTEM dan bukan "highest"), tersembunyi, tanpa batas waktu, `MultipleInstances = IgnoreNew`. Ulangan itu hanya menghidupkan xdns bila belum jalan, penting karena Wi-Fi bisa belum memberi `192.168.1.99` saat boot dan xdns keluar dengan galat. **Pengaturan "restart bila gagal" Task Scheduler tidak dipakai: diuji, tugas yang keluar dengan kode 1 atau dimatikan paksa tidak dihidupkan lagi.** Pemicu berulang diuji dengan tugas percobaan (hidup lagi tepat 1 menit sesudah keluar kode 1). Perintah lain: `status` dan `remove` (satu-satunya rollback; ikut menghentikan prosesnya). `remove` dan shutdown Windows mengakhiri xdns dengan `TerminateProcess`, tanpa jalur shutdown anggun (log query tetap aman karena penulis mengosongkan penyangga tiap antrean menganggur); tugas bersifat tersembunyi, jadi tidak tampak di antarmuka Task Scheduler tetapi `status` dan `remove` bekerja dengan nama `xdns`; pemicu berulang berakhir 365 hari sesudah tiap boot. Aturan firewall terikat ke `bin\xdns.exe`, jadi bangun ulang ke jalur yang sama; karena pemicunya berulang tiap menit, `Stop-ScheduledTask` saja tidak mematikan xdns untuk seterusnya (menit berikutnya hidup lagi). Untuk mengganti binary (berkas yang sedang berjalan terkunci): `Disable-ScheduledTask`, `Stop-ScheduledTask`, salin berkasnya, lalu `Enable-ScheduledTask` dan `Start-ScheduledTask`. Untuk mematikannya sampai Master menyalakan lagi: `Disable-ScheduledTask` lalu hentikan prosesnya, atau `remove`. (Belum diketahui apakah pemicu yang diganti pada 2026-10-19 sudah berulang sebelum boot berikutnya; pada boot berikutnya ia diharapkan berulang, belum diuji lewat boot sungguhan.)
- Mode berkas `0600`/`0700` tidak ditegakkan di Windows (berkas mewarisi ACL folder induk); mengganti nama berkas yang sedang dibuka gagal (Go membuka tanpa `FILE_SHARE_DELETE`), karena itu penulisan atomik `xdns rule` mencoba ulang sebentar.

Konsekuensi: pengembangan di `127.0.0.1:5300`; keputusan soal ICS diambil di Fase 3 (opsi: hentikan ICS bila Master tidak memakai Mobile Hotspot/Hyper-V/WSL, pakai perangkat lain untuk server rumah, atau uji pengikatan alamat dengan reuse) [Inferensi: Windows kemungkinan menolak bind spesifik ke port 53 selama ada bind wildcard dari proses lain; diuji, bukan diasumsikan].

---

## 13. Keamanan

Ancaman utama: (1) resolver terbuka disalahgunakan (amplifikasi DDoS) setelah dipasang di VPS; (2) daftar atau berkas konfigurasi berisi masukan berbahaya; (3) kebocoran riwayat penelusuran lewat log; (4) hulu atau unduhan daftar dimanipulasi.

| Kontrol | Penerapan |
|---|---|
| Batasi klien | `allowed_clients` bawaan hanya loopback dan rentang privat; selain itu REFUSED |
| Bind spesifik | Dengarkan IP tertentu, bukan `0.0.0.0`, kecuali ada alasan |
| Batas laju | Per IP klien. `native`: `ratelimit.go`, aktif (200/detik, burst 500). `dnsproxy`: paket middleware `ratelimit` yang terpisah, bukan opsi `proxy.Config`, belum dipasang; wajib aktif sebelum Fase 6 |
| Habisnya slot TCP | `dnsproxy`: satu koneksi TCP menahan satu slot `MaxGoroutines` bersama UDP (§5.3); diterima di LAN rumah, wajib diperbaiki sebelum Fase 6. `native`: batas koneksi TCP sendiri (64 total, 8 per alamat klien) dan batas query hulu sendiri (256 total, 64 per klien) |
| Datagram UDP terlalu besar | Di Windows, satu datagram lebih besar dari buffer baca menghentikan loop server miekg (galat `WSAEMSGSIZE` dianggap fatal). `native` menanganinya (`tolerantReader`); kasusnya ada di suite kesesuaian dan lulus di kedua backend |
| Pertumbuhan log | Batas ukuran harian `log.max_mb_per_day` (§9); penolakan ACL dicatat satu per query |
| Berkas aturan | Maks 4 MiB dan 4096 byte per baris; kelebihan diabaikan dan dilaporkan sebagai masalah berkas, tidak pernah diam-diam |
| Akses publik (Fase 6) | DoT di port 853 dengan sertifikat valid; identifikasi klien lewat nama host rahasia pada SNI. Desain rincinya ditinjau ulang di awal Fase 6 (Android Private DNS tidak mengirim header autentikasi) |
| Daftar tak tepercaya | HTTPS saja, batas ukuran dan waktu, validasi tiap baris, tidak pernah dieksekusi |
| Rahasia | Kunci privat TLS di `data/tls/` (izin ketat, di-.gitignore); tidak ada rahasia di repo atau konfigurasi contoh |
| Rantai pasok | Versi modul dikunci; `go mod verify`; `GOFLAGS=-mod=readonly` pada build |
| Pesan galat | Log server boleh rinci; jawaban DNS ke klien tidak pernah memuat detail internal |
| Hak akses | Berjalan sebagai pengguna biasa (di Linux: pengguna khusus, bukan root, dengan kapabilitas bind port rendah) |

Sebelum setiap commit: periksa daftar keamanan di `.claude/rules/ecc/common/security.md`, dan jalankan agent `security-reviewer` untuk kode yang menghadap jaringan (Fase 1, 4, 6). Tinjauan Fase 1 dilakukan oleh agent `xdns-reviewer` yang menjalankan binary dengan beban ala penyerang (`.agents/state/reviews/phase-1-review.md`).

---

## 14. Penanganan galat dan ketahanan

- **Fail-open:** bila engine belum siap atau terjadi galat internal, query diteruskan (DNS tetap hidup) dan galat dicatat. Pengecualian: pelanggaran ACL tetap REFUSED, juga bila yang panic adalah pemeriksaan ACL atau handler sebelum `Before` selesai.
- **Hulu gagal:** coba hulu berikutnya; bila semua gagal, SERVFAIL ke klien dan `Failed` mencatatnya.
- **Hulu gagal beruntun (Fase 5, `internal/engine/health.go`):** engine menghitung query yang diteruskan dan gagal berturut-turut; hitungannya sama untuk kedua backend karena keduanya melapor lewat `After` dan `Failed`. Lima berturut-turut memicu satu peringatan per menit (jumlah dan galat terakhir); jawaban pertama sesudahnya yang menyebut hulu mencatat pemulihan. Cache hit, jawaban lokal, dan pembatalan oleh klien (`context.Canceled`) diabaikan.
- **Daftar:** unduhan gagal → pakai cache lama; cache rusak → abaikan berkas itu, pakai yang lain, catat galat.
- **Aturan:** baris salah dilewati; berkas hilang = tanpa aturan, bukan galat fatal.
- **Log:** disk penuh, saluran penuh, atau batas ukuran harian tidak boleh memblokir query; catatan yang tidak tertulis dihitung.
- **Shutdown:** `ctx` dibatalkan saat Ctrl+C; listener ditutup rapi dan log dikosongkan. Diverifikasi di Fase 5 pada konsol Windows asli untuk kedua backend (kode keluar 0, "xdns berhenti", log query ditutup, port UDP dilepas), dan query yang tertahan di hulu polos yang diam dibatalkan dengan menutup koneksinya, tidak menunggu batas waktu.
- Tidak ada galat yang ditelan diam-diam: setiap galat dibungkus konteks (`fmt.Errorf("...: %w", err)`) dan dicatat sekali pada lapisan yang menanganinya.

---

## 15. Pengujian

Proyek ini memakai TDD: tes ditulis dulu (merah), implementasi minimal (hijau), lalu rapikan.

| Lapisan | Isi | Target |
|---|---|---|
| Unit | `engine` (pencocokan, prioritas, jawaban blokir, CNAME, nama khusus), `lists` (parser), `qlog`, `config`, `native` (cache, ACL, upstream dengan hulu palsu) | Cakupan ≥ 80% |
| Fuzz | Parser daftar dan parser aturan (`go test -fuzz`) | Tanpa panic |
| Benchmark | Pencarian domain pada ±250 ribu entri; memori proses | p99 < 50 µs; < 150 MB |
| Kesesuaian | `internal/backend/conformance`: hulu palsu (`miekg/dns` di loopback) + backend yang diuji di port acak. Kasus: A/AAAA/HTTPS terblokir, lolos, `allow` mengalahkan `block`, CNAME terblokir, ACL REFUSED (tanpa bit RA), log tertulis, datagram UDP besar atau rusak tidak menghentikan server, reverse lookup alamat privat dijawab lokal (16 kasus sejak Fase 5). Dijalankan untuk **kedua** backend dari satu tabel konstruktor | Hasil identik |
| Integrasi manual | `scripts/dnsquery.ps1` (atau `dig`) dari PC, lalu uji dari HP (Fase 3) | Kriteria S1–S3 |

Perintah verifikasi tiap fase: `gofmt -l .` (kosong), `go vet ./...`, `go test ./... -cover`, dan pemindaian kerentanan `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` (yang terdampak harus 0). Detektor race (`CGO_ENABLED=1 go test -race ./...`) berjalan di PC ini lewat w64devkit. Kode konkuren dijaga lewat snapshot tak berubah, tanpa state global, dan tes beban. Mutasi sengaja pada salinan scratch dipakai untuk memastikan tes benar-benar menangkap perubahan perilaku.

---

## 16. Plan B dan di luar cakupan

**Plan B: APK.** Dipertimbangkan bila setelah penyetelan di Fase 3 kriteria S2 gagal atau Master ingin pemblokiran lebih kuat. Tiga bentuknya dan perbandingannya ada di `research.md` §9: filter DNS lokal (daya blokir sama dengan DNS), filter DNS + HTTPS (lebih kuat, berat, berisiko), dan switch Private DNS (ringan). Aplikasi open source BlockAds (GPL-3.0) sudah melakukan dua bentuk pertama dan bisa jadi acuan. Karena engine adalah paket Go murni tanpa ketergantungan jaringan, ia dapat dikompilasi untuk Android di kemudian hari [Inferensi; BlockAds memakai pola Go + gomobile].

**Di luar cakupan (YAGNI):** UI web (Fase 7 bila diminta), sintaks adblock penuh, DoQ, validasi DNSSEC, profil per klien, kontrol orang tua, ECS (dimatikan demi privasi), pengaturan DHCP.

---

## 17. Risiko dan mitigasi

| # | Risiko | Mitigasi |
|---|---|---|
| R1 | API `dnsproxy` masih 0.x dan bisa berubah | Kunci versi; adapter tipis; backend `native` sebagai cadangan |
| R2 | ICS memegang UDP 53 di PC ini | Pengembangan di port 5300; keputusan di Fase 3; opsi perangkat lain |
| R3 | Salah blokir (daftar terlalu agresif) | Mulai dari PRO; `rule add allow` berlaku ≤ 10 detik; log menunjukkan sumbernya |
| R4 | HP lolos dari DNS kita (DNS IPv6 dari router, DoH browser, Private DNS aktif) | Periksa log apakah query HP masuk; cek setelan Secure DNS Chrome dan DoH Firefox; matikan Private DNS saat tes |
| R5 | Dua backend berperilaku beda | Suite kesesuaian yang sama; perbandingan keputusan pada sampel domain |
| R6 | Toolchain Go 1.26.9 diunduh otomatis (butuh internet) | Dicatat di Fase 0; `govulncheck` tiap penutup fase |
| R7 | PC mati → HP tanpa DNS saat DNS Wi-Fi hanya menunjuk PC | Diterima untuk tes; DNS kedua sengaja dikosongkan agar pengukuran tidak bocor ke DNS lain. Solusi tetap: VPS (Fase 6) |
| R8 | Sumber daftar mati atau berubah format | Cache disk; validasi baris; peringatan di `lists status` |
| R9 | Domain iklan terus berganti | Pembaruan harian; daftar baru (NRD/DGA) dipertimbangkan di Fase 7 |
| R10 | Pemicu redirect ternyata skrip domain situs sendiri | Dikatakan terus terang; pelengkap Firefox + uBO; Plan B |
| R11 | xdns tidak naik setelah boot (Wi-Fi belum memberi `192.168.1.99`, xdns keluar dengan galat) dan "restart bila gagal" Task Scheduler tidak bekerja | Pemicu berulang tiap menit dengan `IgnoreNew` (§12). Diuji dengan tugas percobaan; boot sungguhan belum diuji |
| R12 | Server rumah dipakai keluarga dan teman (`allowed_clients` tetap `192.168.1.0/24`, keputusan Master) | Batas per IP (laju, koneksi TCP, gerbang hulu) lebih penting daripada untuk satu HP; celahnya: tabel pembatas laju yang penuh melepas sumber baru (I2, Fase 6) |

---

## 18. Hal yang diverifikasi saat implementasi

Belum dipastikan; dibuktikan lewat tes dan bukan diasumsikan:
1. **Terverifikasi di Fase 1:** tanda tangan `proxy.Config.RequestHandler` dan perilaku cache dnsproxy (§5.3, `.agents/notes/dnsproxy-api-facts.md`).
2. **Terverifikasi di Fase 1:** upstream DoH berupa IP tanpa bootstrap (jawaban DoH nyata dari `https://1.1.1.1/dns-query` pada binary).
3. **Sebagian terverifikasi di Fase 3:** server menjawab NXDOMAIN dan NODATA dengan SOA sintetis pada kueri dari PC; **Chrome di HP tidak pernah menanyakan** `use-application-dns.net` maupun `_dns.resolver.arpa` (0 kueri), jadi tidak ada reaksi yang bisa diamati. Reaksi Firefox di HP belum diuji.
4. **Terverifikasi di Fase 2:** ketiga daftar bawaan diunduh dan di-parse pada binary asli (`hagezi-pro` 202.297 domain, `hagezi-popups` 47.668 domain, `abpindo` 124 domain — total 250.089 entri sebelum de-duplikasi, 0 baris ditolak).
5. **Terverifikasi di Fase 2:** memori dan latensi pencarian dengan daftar penuh (250.000–250.089 entri): `heap_inuse_delta` = 16,15–16,29 MiB, memori proses hidup WorkingSet = 71,74–74,20 MiB / Private = 103,35–105,25 MiB (< 150 MB), rata-rata = 215–250 ns/op (0 alokasi), p95 = 1,04–1,07 µs, p99 = 2,01 µs (< 50 µs).
6. **Sebagian terverifikasi di Fase 3 (Chrome saja):** jawaban `0.0.0.0` pada Chrome HP RedMagic tidak menimbulkan gangguan yang teramati; permintaan ke domain terblokir gagal cepat ("This site can't be reached"). Belum dibandingkan dengan NODATA atau NXDOMAIN, dan Firefox belum diuji (`test-report.md` bagian 4).
7. Apakah EDE (RFC 8914) "Filtered" pada jawaban blokir bermanfaat; ditambahkan hanya bila terbukti tidak mengganggu klien. Belum diuji (Fase 3 tidak mencakupnya).
8. **Terverifikasi di Fase 3:** konflik ICS pada UDP 53 terselesaikan tanpa menyentuh ICS; bind khusus `192.168.1.99:53` UDP dan TCP berhasil (bagian 12).
9. **Terverifikasi di Fase 3:** pada HP nyata, `adblock.turtlecute.org` 95% (126/132) lalu 97% (128/132) dengan dua aturan kustom; Chrome tidak melewati xdns (tidak ada pencarian host DoH). Redirect Shopee dilacak ke `s.shopee.co.id` dan diblokir; redirect Play Store belum pernah muncul. DNS IPv6 router sebagai jalur lolos belum diperiksa langsung (`test-report.md`).
10. **Terverifikasi di Fase 4:** suite kesesuaian (15 kasus) hijau pada `dnsproxy` dan `native` dari satu tabel; 2.000 query (1.000 nama, A dan HTTPS) memberi keputusan engine yang sama pada kedua backend (1.732 diblokir di masing-masing, 0 beda keputusan, 1 beda bentuk jawaban akibat hulu). `native` melayani DoH nyata ke 1.1.1.1 (`phase-4-report.md`).
11. **Temuan Fase 4:** satu datagram UDP lebih besar dari buffer menghentikan loop UDP miekg di Windows; diperbaiki di `native` dan dijadikan kasus suite.
12. **Terverifikasi di Fase 5:** tes HP dengan `backend: native`: `adblock.turtlecute.org` 97%, 484 query HP dalam ±2,5 menit (322 diblokir daftar, 6 aturan kustom), hulu p50 29 ms / p95 88 ms, proses 41 MB (`test-report.md` bagian 5). Satu query terbuang oleh pembatas laju lama (100/detik) karena halaman tes mengirim ±255 query dalam setengah detik; dinaikkan ke 200/detik, burst 500.
13. **Terverifikasi di Fase 5:** Ctrl+C anggun pada konsol asli untuk kedua backend; peringatan hulu gagal beruntun; log query pulih sesudah disk penuh (disk palsu); log operasional berbatas ukuran; tugas Task Scheduler terpasang dan menjalankan xdns tersembunyi (sesi 0) dengan DNS menjawab dan `xdns.log` bertambah.
14. **Belum diverifikasi (sesudah Fase 5):** boot PC sungguhan dengan tugas Task Scheduler (termasuk Wi-Fi yang telat); disk penuh pada drive asli; DoH dan hulu IPv6 nyata di bawah beban lama; galat baca UDP Windows selain `WSAEMSGSIZE`; ketepatan perkiraan memori cache di bawah beban nyata; uji 7 hari S4 (Fase 6, VPS); DoT sebagai hulu (ditunda); Firefox di HP, jalur DNS IPv6 router, EDE.

