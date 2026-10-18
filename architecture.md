# xdns — Arsitektur

*Versi 1.0 | 2026-10-18 | Status: **disetujui Master, Fase 0 selesai** | Penulis: Claude (keputusan desain didelegasikan penuh oleh Master)*

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
| S4 | Stabil | 7 hari berjalan tanpa crash atau restart manual, memori stabil, latensi query lolos (cache miss) p95 < 150 ms (target awal) | Fase 5 |
| S5 | Salah blokir mudah diperbaiki | Satu perintah `xdns rule add allow <domain>` berlaku ≤ 10 detik tanpa restart | Fase 1 |

---

## 2. Keputusan desain

Semua ditetapkan Claude atas kewenangan penuh dari Master (2026-10-18). Master boleh membatalkan salah satunya kapan saja.

| # | Keputusan | Pilihan | Alasan |
|---|---|---|---|
| D1 | Bahasa | Go, `go 1.26.8` di `go.mod` | Satu binary, ekosistem DNS matang, sudah terpasang (1.26.4; toolchain naik otomatis karena `dnsproxy` v0.86.0 mensyaratkan 1.26.8) |
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
├── architecture.md  roadmap.md  research.md   # dokumen (akar proyek)
├── test-report.md                              # hasil tes HP, dibuat di Fase 3
├── go.mod  go.sum  .gitignore  README.md
├── cmd/xdns/
│   └── main.go                 # titik masuk; subperintah: run, check, log, rule, lists
├── internal/
│   ├── config/                 # skema YAML, pemuat ketat, validasi
│   ├── engine/                 # filter tanpa jaringan
│   │   ├── engine.go           #   Engine, Query, Decision; Before/After/Failed
│   │   ├── matcher.go          #   DomainSet (cocok sufiks) + snapshot atomik
│   │   ├── rules.go            #   aturan kustom: parser + muat ulang
│   │   ├── response.go         #   pembentuk jawaban blokir (4 mode)
│   │   ├── cname.go            #   pemeriksaan rantai CNAME
│   │   └── special.go          #   nama khusus (canary, DDR)
│   ├── lists/                  # sumber daftar: unduh, parser, cache disk, penjadwal
│   ├── qlog/                   # log query: penulis JSONL, rotasi, pembaca
│   ├── backend/
│   │   ├── backend.go          #   antarmuka Backend, Deps, registri
│   │   ├── dnsproxybe/         #   adapter AdGuard dnsproxy (pendekatan 4)
│   │   ├── native/             #   server + upstream + cache sendiri (pendekatan 1)
│   │   └── conformance/        #   suite tes yang sama untuk kedua backend
│   └── selfcheck/              # uji mandiri: SafeSearch, hulu, domain contoh
├── configs/xdns.example.yaml
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
    Reason  string   // "rule" | "list" | "cname" | "special"
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
- selalu menulis tepat satu catatan log per query (di `Before` bila diblokir, di `After` atau `Failed` bila diteruskan);
- bila belum ada daftar yang termuat, `Before` meloloskan query (fail-open) dan mencatat peringatan satu kali.

### 5.2 Backend

```go
package backend

type Backend interface {
    Name() string
    Run(ctx context.Context) error // blok sampai ctx selesai atau galat fatal
}

type Deps struct { Cfg config.Config; Engine *engine.Engine; Log *slog.Logger }
type Factory func(Deps) (Backend, error)

var registry = map[string]Factory{ "dnsproxy": dnsproxybe.New, "native": native.New }
```

`main.go` memilih backend dari `backend:` pada konfigurasi atau opsi `-backend`. Nama tak dikenal = galat saat start.

### 5.3 Adapter `dnsproxy` (pendekatan 4)

Alur satu query: handler kustom dnsproxy (`proxy.Config.RequestHandler`) memanggil `Before`. Bila `Block`, jawaban sintetis diisikan ke konteks respons. Bila `Pass`, dnsproxy melakukan resolusi bawaannya (upstream + cache), lalu `After` memeriksa jawaban dan boleh menggantinya dengan jawaban blokir. Pemetaan konfigurasi: `listen`, `upstreams`, `bootstrap`, `cache`, `allowed_clients` → `proxy.Config`.

Tipe pesan DNS yang dipakai dnsproxy sama dengan engine (`github.com/miekg/dns`), jadi tidak ada konversi. Tanda tangan persis handler dan perilaku cache terhadap jawaban yang diubah `After` diverifikasi dari pkg.go.dev v0.86.0 saat implementasi (dokumentasinya masih memuat TODO; hook `BeforeRequestHandler` tidak ditemukan).

### 5.4 Backend `native` (pendekatan 1)

| Berkas | Isi |
|---|---|
| `server.go` | Listener UDP dan TCP lewat `dns.Server`; DoT ditambahkan di Fase 6 (DoH hanya bila Master meminta) |
| `acl.go` | Pemeriksaan `allowed_clients` (CIDR); di luar daftar dijawab REFUSED |
| `upstream.go` | Klien DoH (`net/http`, HTTP/2, `application/dns-message`), DoT dan plain lewat `dns.Client`; failover berurutan dengan penanda gagal sementara |
| `cache.go` | LRU berbatas ukuran; kunci (nama, tipe, kelas, bit DO); TTL dijepit `min_ttl`/`max_ttl`; cache negatif memakai SOA minimum |
| `handler.go` | `Before` → cache → upstream → `After`; menulis `Failed` bila semua hulu gagal |
| `ratelimit.go` | Pembatas per IP klien (token bucket) |

Cache menyimpan jawaban hulu *sebelum* `After`, sama seperti dnsproxy, sehingga pemeriksaan CNAME tetap berjalan pada setiap cache hit.

### 5.5 Daftar blokir (`internal/lists`)

Lihat §7.

### 5.6 Log query (`internal/qlog`)

Lihat §9.

---

## 6. Alur satu query

`Before`:
1. **Validasi.** Harus satu pertanyaan dengan opcode QUERY; selain itu `Pass` (backend yang menjawab FORMERR/NOTIMP).
2. **Normalisasi.** Huruf kecil, buang titik akhir. Nama IDN dianggap sudah dalam punycode (bentuk kawat DNS).
3. **Nama khusus** (bila diaktifkan): `use-application-dns.net` → blokir NXDOMAIN; `_dns.resolver.arpa` → NODATA.
4. **Izinkan?** Cocok aturan `allow` kustom, lalu daftar berjenis `allow` → `Pass` (alasan `allow`).
5. **Blokir?** Cocok aturan `block` kustom, lalu daftar berjenis `block` → `Block` dengan `Source` terisi.
6. Selain itu `Pass`.

Backend meneruskan query yang `Pass` ke cache/hulu. Lalu `After`:
1. **Jawaban tidak ada atau rcode bukan NOERROR:** catat dan kembalikan apa adanya.
2. **Rantai CNAME:** untuk setiap CNAME di bagian jawaban, normalisasi targetnya. Bila target dikecualikan `allow`, lanjut. Bila cocok blokir, kembalikan `Block` untuk pertanyaan asli (alasan `cname`, `Via` = target).
3. Selain itu catat sebagai lolos dengan hulu, rcode, dan latensi.

**Jawaban blokir** (`response.go`), semuanya menyalin ID dan bagian pertanyaan, mengisi RA, dan menjaga bit RD:

| Mode | Perilaku |
|---|---|
| `null_ip` (bawaan) | A → `0.0.0.0`, AAAA → `::` (TTL `block.ttl`); tipe lain, termasuk HTTPS/SVCB (65), → NOERROR kosong + SOA sintetis |
| `nxdomain` | NXDOMAIN + SOA sintetis |
| `nodata` | NOERROR kosong + SOA sintetis |
| `refused` | REFUSED |

SOA sintetis memakai TTL dan minimum sama dengan `block.ttl` agar cache negatif klien singkat (aturan baru cepat berlaku).

---

## 7. Daftar blokir

| Nama | URL | Format | Entri (2026-10-18) |
|---|---|---|---|
| `hagezi-pro` | `https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/wildcard/pro-onlydomains.txt` | `domains` | 201.729 |
| `hagezi-popups` | `https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/wildcard/popupads-onlydomains.txt` | `domains` | 47.268 |
| `abpindo` | `https://raw.githubusercontent.com/ABPindo/indonesianadblockrules/master/subscriptions/domain.txt` | `domains` | ±125 (kecil, tetapi lokal Indonesia) |

Ketiganya sudah saya unduh dan periksa: satu domain per baris, komentar berawalan `#`, tanpa spasi pada baris data. Header HaGeZi menyatakan `Expires: 8 hours`; ABPindo `4 days`.

**Format yang didukung versi 1:** `domains` (satu domain per baris) dan `hosts` (`0.0.0.0 domain`). Sintaks adblock penuh (`||domain^$opsi`) tidak didukung; masuk Fase 7 bila diperlukan.

**Parser** (daftar dianggap masukan tak tepercaya):
- abaikan baris kosong dan komentar (`#`, `!`, `;`), buang komentar di ujung baris;
- huruf kecil; buang awalan `*.`;
- validasi sintaks nama: label 1–63 karakter, total ≤ 253, hanya `a-z 0-9 - _`;
- tolak alamat IP dan entri satu label (agar satu baris rusak seperti `com` tidak memblokir seluruh TLD);
- hitung dan laporkan baris yang ditolak; jangan berhenti karena satu baris buruk.

**Pembaruan:**
- saat start: muat salinan cache di `data/lists/<nama>.txt` lebih dulu (cepat), lalu segarkan di latar belakang;
- interval `lists.update_interval` (bawaan 24 jam) dengan sedikit jitter; permintaan `If-None-Match`/`If-Modified-Since` bila ada metadata;
- hanya `https://`; batas waktu 30 detik; batas ukuran 64 MB per daftar;
- tulis ke berkas sementara lalu `rename` (atomik); simpan metadata (`etag`, `fetched_at`, `count`, `rejected`) di `data/lists/<nama>.meta.json`;
- bila unduhan gagal atau berkas rusak: pertahankan salinan lama dan catat galat;
- setelah ada perubahan, bangun snapshot baru lalu tukar atomik (`atomic.Pointer`); snapshot lama tidak pernah diubah.

**Dua snapshot terpisah** (daftar dan aturan kustom) agar perubahan aturan tidak membangun ulang ratusan ribu entri.

**Anggaran sumber daya** (target awal, diukur di Fase 2): sekitar 250 ribu entri, memori proses < 150 MB, pencarian p99 < 50 µs. Bila terlampaui, ganti peta string dengan peta hash 64-bit.

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

- rotasi harian; retensi `log.retention_days` (bawaan 7); berkas lama dihapus saat start dan tiap tengah malam;
- penulisan lewat saluran berpenyangga: bila penuh, catatan dibuang dan penghitung `dropped` dicatat; query tidak pernah menunggu disk;
- cincin memori 5.000 catatan terakhir disiapkan untuk UI di Fase 7;
- log berisi riwayat domain yang dibuka; berada hanya di PC ini dan dapat dimatikan (`log.enabled: false`).

---

## 10. Konfigurasi

`configs/xdns.example.yaml`:

```yaml
backend: dnsproxy                # dnsproxy | native

listen:
  - 127.0.0.1:5300               # pengembangan; ganti ke 192.168.1.10:53 di Fase 3

allowed_clients:                 # CIDR; di luar ini dijawab REFUSED
  - 127.0.0.0/8
  - "::1/128"
  - 10.0.0.0/8
  - 172.16.0.0/12
  - 192.168.0.0/16

upstreams:                       # dicoba berurutan
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

data_dir: data
```

Validasi saat start (kunci tak dikenal, CIDR tak sah, URL daftar bukan `https`, `mode` tak dikenal, backend tak dikenal, TTL negatif → galat dengan pesan jelas dan keluar kode ≠ 0). Jalur relatif dihitung dari lokasi berkas konfigurasi. Kunci yang tidak ditulis memakai nilai bawaan yang sama dengan contoh ini (`config.Default()`); daftar blokir tidak punya nilai bawaan, jadi tanpa sumber daftar yang aktif `xdns check` memberi peringatan.

---

## 11. SafeSearch

xdns **tidak pernah** menulis ulang `www.google.com`. Syarat agar hasilnya benar:
- hulu bukan resolver yang memaksa SafeSearch (karena itu varian "family" Cloudflare/AdGuard dilarang sebagai bawaan);
- hulu terenkripsi supaya ISP atau router tidak membajak jawaban DNS biasa;
- tidak ada aturan atau daftar yang memetakan Google ke `forcesafesearch.google.com`.

`xdns check` melakukan uji mandiri: resolve `www.google.com` lewat hulu yang dikonfigurasi dan memastikan rantai CNAME tidak memuat `forcesafesearch.google.com`; bila ada, cetak peringatan yang menyebut hulu mana. Batas: bila SafeSearch dikunci lewat pengaturan akun Google atau proxy jaringan, DNS tidak dapat membatalkannya.

---

## 12. Lingkungan PC ini (fakta terukur 2026-10-18)

- Windows 11 Pro; Go 1.26.4 (`GOTOOLCHAIN=auto`); git 2.54.0; **tidak ada gcc**, jadi `go test -race` tidak tersedia lokal.
- IP LAN PC `192.168.1.10`, gateway `192.168.1.1`; ada adapter virtual `172.25.144.1` (kemungkinan Hyper-V/WSL).
- **UDP `0.0.0.0:53` sedang dipegang layanan Internet Connection Sharing (ICS / `SharedAccess`)**; TCP 53 bebas. Android tidak bisa menentukan port DNS di pengaturan Wi-Fi, jadi tes dari HP memerlukan UDP 53 di IP LAN PC.
- UDP 5353 dipakai banyak proses (mDNS). Port 5300 dan 853 bebas.

Konsekuensi: pengembangan di `127.0.0.1:5300`; keputusan soal ICS diambil di Fase 3 (opsi: hentikan ICS bila Master tidak memakai Mobile Hotspot/Hyper-V/WSL, pakai perangkat lain untuk server rumah, atau uji pengikatan alamat dengan reuse) [Inferensi: Windows kemungkinan menolak bind spesifik ke port 53 selama ada bind wildcard dari proses lain; diuji, bukan diasumsikan].

---

## 13. Keamanan

Ancaman utama: (1) resolver terbuka disalahgunakan (amplifikasi DDoS) setelah dipasang di VPS; (2) daftar atau berkas konfigurasi berisi masukan berbahaya; (3) kebocoran riwayat penelusuran lewat log; (4) hulu atau unduhan daftar dimanipulasi.

| Kontrol | Penerapan |
|---|---|
| Batasi klien | `allowed_clients` bawaan hanya loopback dan rentang privat; selain itu REFUSED |
| Bind spesifik | Dengarkan IP tertentu, bukan `0.0.0.0`, kecuali ada alasan |
| Batas laju | Per IP klien (native: `ratelimit.go`; dnsproxy: opsi rate limit bawaannya); wajib aktif sebelum Fase 6 |
| Akses publik (Fase 6) | DoT di port 853 dengan sertifikat valid; identifikasi klien lewat nama host rahasia pada SNI. Desain rincinya ditinjau ulang di awal Fase 6 (Android Private DNS tidak mengirim header autentikasi) |
| Daftar tak tepercaya | HTTPS saja, batas ukuran dan waktu, validasi tiap baris, tidak pernah dieksekusi |
| Rahasia | Kunci privat TLS di `data/tls/` (izin ketat, di-.gitignore); tidak ada rahasia di repo atau konfigurasi contoh |
| Rantai pasok | Versi modul dikunci; `go mod verify`; `GOFLAGS=-mod=readonly` pada build |
| Pesan galat | Log server boleh rinci; jawaban DNS ke klien tidak pernah memuat detail internal |
| Hak akses | Berjalan sebagai pengguna biasa (di Linux: pengguna khusus, bukan root, dengan kapabilitas bind port rendah) |

Sebelum setiap commit: periksa daftar keamanan di `.claude/rules/ecc/common/security.md`, dan jalankan agent `security-reviewer` untuk kode yang menghadap jaringan (Fase 1, 4, 6).

---

## 14. Penanganan galat dan ketahanan

- **Fail-open:** bila engine belum siap atau terjadi galat internal, query diteruskan (DNS tetap hidup) dan galat dicatat. Pengecualian: pelanggaran ACL tetap REFUSED.
- **Hulu gagal:** coba hulu berikutnya; bila semua gagal, SERVFAIL ke klien dan `Failed` mencatatnya.
- **Daftar:** unduhan gagal → pakai cache lama; cache rusak → abaikan berkas itu, pakai yang lain, catat galat.
- **Aturan:** baris salah dilewati; berkas hilang = tanpa aturan, bukan galat fatal.
- **Log:** disk penuh atau saluran penuh tidak boleh memblokir query.
- **Shutdown:** `ctx` dibatalkan saat Ctrl+C; listener ditutup rapi dan log dikosongkan.
- Tidak ada galat yang ditelan diam-diam: setiap galat dibungkus konteks (`fmt.Errorf("...: %w", err)`) dan dicatat sekali pada lapisan yang menanganinya.

---

## 15. Pengujian

Proyek ini memakai TDD: tes ditulis dulu (merah), implementasi minimal (hijau), lalu rapikan.

| Lapisan | Isi | Target |
|---|---|---|
| Unit | `engine` (pencocokan, prioritas, jawaban blokir, CNAME, nama khusus), `lists` (parser), `qlog`, `config`, `native` (cache, ACL, upstream dengan hulu palsu) | Cakupan ≥ 80% |
| Fuzz | Parser daftar dan parser aturan (`go test -fuzz`) | Tanpa panic |
| Benchmark | Pencarian domain pada ±250 ribu entri; memori proses | p99 < 50 µs; < 150 MB |
| Kesesuaian | `internal/backend/conformance`: hulu palsu (`miekg/dns` di loopback) + backend yang diuji di port acak. Kasus: A/AAAA/HTTPS terblokir, lolos, `allow` mengalahkan `block`, CNAME terblokir, ACL REFUSED, log tertulis. Dijalankan untuk **kedua** backend dari satu tabel konstruktor | Hasil identik |
| Integrasi manual | `nslookup`/`dig` dari PC, lalu uji dari HP (Fase 3) | Kriteria S1–S3 |

Perintah verifikasi tiap fase: `gofmt -l .` (kosong), `go vet ./...`, `go test ./... -cover`. Detektor race (`-race`) memerlukan cgo/gcc yang belum ada di PC ini; dijalankan bila gcc terpasang atau di Linux (VPS/WSL). Tanpa itu, kode konkuren dijaga lewat snapshot tak berubah, tidak ada state global, dan tes beban sederhana.

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
| R6 | Toolchain Go 1.26.8 diunduh otomatis (butuh internet) | Dicatat di Fase 0 |
| R7 | PC mati → HP tanpa DNS saat DNS Wi-Fi hanya menunjuk PC | Diterima untuk tes; DNS kedua sengaja dikosongkan agar pengukuran tidak bocor ke DNS lain. Solusi tetap: VPS (Fase 6) |
| R8 | Sumber daftar mati atau berubah format | Cache disk; validasi baris; peringatan di `lists status` |
| R9 | Domain iklan terus berganti | Pembaruan harian; daftar baru (NRD/DGA) dipertimbangkan di Fase 7 |
| R10 | Pemicu redirect ternyata skrip domain situs sendiri | Dikatakan terus terang; pelengkap Firefox + uBO; Plan B |

---

## 18. Hal yang diverifikasi saat implementasi

Belum dipastikan; dibuktikan lewat tes dan bukan diasumsikan:
1. Tanda tangan `proxy.Config.RequestHandler` dan perilaku cache dnsproxy pada jawaban yang diubah `After` (pkg.go.dev v0.86.0).
2. Apakah dnsproxy menerima upstream DoH berupa IP (`https://1.1.1.1/dns-query`) tanpa bootstrap tambahan.
3. Perilaku `use-application-dns.net` (NXDOMAIN) dan `_dns.resolver.arpa` (NODATA) pada Firefox dan Chrome di HP.
4. Format baris pada pembaruan daftar di masa depan. Ketiga daftar saat ini sudah diperiksa (2026-10-18): semua baris data berupa domain polos huruf kecil, tanpa `*.` dan tanpa spasi.
5. Memori dan latensi pencarian dengan daftar penuh (angka aktual dicatat).
6. Perilaku HP RedMagic terhadap jawaban `0.0.0.0` dibanding NODATA/NXDOMAIN.
7. Apakah EDE (RFC 8914) "Filtered" pada jawaban blokir bermanfaat; ditambahkan hanya bila terbukti tidak mengganggu klien.
8. Penyelesaian konflik ICS pada UDP 53 (Fase 3).
