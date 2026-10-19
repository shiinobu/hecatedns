# Laporan Fase 0 — Fondasi

*2026-10-18 | Status: selesai, sudah ditinjau, semua temuan penting diperbaiki*

## Hasil

Kerangka proyek Go yang bisa dibangun dan diuji: repo git (`main`), modul `hecate` dengan dependensi terkunci, pemuat konfigurasi YAML yang ketat dengan nilai bawaan dan validasi, serta perintah `hecate check`. Paket lain (`engine`, `lists`, `qlog`, `selfcheck`, `backend/*`) baru berupa `doc.go` kosong; isinya dikerjakan mulai Fase 1.

| Tugas | Hasil | Commit |
|---|---|---|
| 1. Repo, modul, kerangka | Sembilan paket kosong, dependensi terkunci, `.gitattributes` (LF) | `509c5d1`, `66e62ee` |
| 2. Pemuat konfigurasi | Dekode ketat (kunci asing/ganda ditolak), BOM dan CRLF, jalur relatif | `2328395` |
| 3. Validasi dan peringatan | Semua masalah dilaporkan sekaligus, `Warnings()` | `6e01377` |
| 4. CLI `check` dan README | `hecate check -config <berkas>` | `ec61aa5` |
| Perbaikan hasil tinjauan | Tiga commit `fix:` (lihat di bawah) | `11f32b5`, `936ec3c`, `8811ec1` |
| Peningkatan keamanan | Go 1.26.9 dan `x/net` v0.60.0 | `2a20cd5` |

## Bukti verifikasi (keadaan akhir)

- `gofmt -l .` kosong; `go vet ./...`, `go build ./...`, `go mod verify`, `go mod tidy -diff` bersih.
- `go test ./... -cover -count=1`: **92 pengujian lulus, 0 gagal**; cakupan `internal/config` 96,2%, `cmd/hecate` 97,3% (target ≥ 80%).
- `govulncheck ./...`: sebelum perbaikan **6 kerentanan pustaka standar terdampak** (GO-2026-6603, 6607, 6611, 6612, 6613, 6617: `net/http`, `crypto/tls`, `x/net`); sesudah Go 1.26.9 dan `x/net` v0.60.0 **0 terdampak**. Sisa satu kerentanan di modul yang di-require tetapi tidak dipanggil kode kita.
- Perintah nyata diuji: contoh valid lolos; salah ketik kunci, `localhost:5300` sebagai `listen`, dan mode blokir salah ditolak dengan pesan jelas; berkas kosong lolos dengan peringatan.

## Tinjauan

Peninjau berkonteks baru (model Fable) memeriksa `509c5d1..5c635a4`: **0 Critical, 4 Important**, beberapa Minor. Kelima fokus tinjauan rencana terbukti berfungsi pada program nyata, dan empat mutasi uji (dekode ketat dimatikan, cek `bootstrap` dimatikan, `Warnings` dikosongkan, direktori dasar salah) semuanya tertangkap tes.

| Temuan Important | Perbaikan (tes ditulis dulu, gagal, lalu lulus) |
|---|---|
| Hanya dokumen YAML pertama yang dibaca; bagian setelah `---` diabaikan diam-diam | Dokumen kedua ditolak: `TestLoad_RejectsMultipleDocuments` |
| `hecate check lain.yaml` mengabaikan argumen dan memvalidasi berkas bawaan | Argumen tak terduga ditolak (kode 2): dua baris baru di `TestRun` |
| `allowed_clients` kosong diterima (berisiko jadi resolver terbuka) | Wajib minimal satu CIDR; untuk mengizinkan semua tulis `0.0.0.0/0` dan `::/0` |
| Nama sumber daftar menjadi nama berkas tanpa dicek (`../../evil`, `CON`, `a:b`, `Hagezi`/`hagezi`) | Nama dibatasi huruf/angka/`-`/`_`, maks 64, bukan nama perangkat Windows, unik tanpa beda huruf |

## Keputusan yang saya ambil (Rulings)

1. Tanpa git worktree: repo baru, kerja langsung di `main` (D16 di `architecture.md`). Risiko: tidak ada.
2. `/.superpowers/` ditambahkan ke `.gitignore` (ruang kerja sementara). Risiko: satu baris.
3. Commit dasar Tugas 1 dicatat sebagai commit akar (HEAD belum ada saat mulai). Risiko: hanya rentang di ledger.
4. `.gitattributes` (`* text=auto eol=lf`) ditambahkan, karena `autocrlf` di Windows membuat `gofmt -l` mendaftar semua berkas. Risiko: satu berkas.
5. Tugas 2: pernyataan jalur absolut dipisah menjadi tes sendiri dan ditambah `TestDefault_ReturnsFreshSlices`. Risiko: dua tes kecil.
6. Tugas 3: `Warnings` hanya menghitung sumber yang aktif dan berjenis `block`; ada subtes "hanya sumber nonaktif". Risiko: satu kondisi.
7. Tugas 4: `TestRun` ditambah kasus jalur bawaan `hecate.yaml` dan flag tak dikenal. Risiko: dua baris tabel.
8. Upstream atau bootstrap yang sama dengan alamat `listen` hecate tetap diterima (perlu pengetahuan saat berjalan; ditangani di Fase 1). Risiko: loop DNS bila salah konfigurasi.
9. `allowed_clients: 0.0.0.0/0` dengan `listen` publik tidak memberi peringatan (pilihan sengaja; pengerasan di Fase 6). Risiko: resolver terbuka bila salah konfigurasi.
10. Jalur berawalan `/` tanpa drive di Windows dihitung dari direktori konfigurasi. Risiko: jalur mengejutkan untuk masukan aneh.
11. Perpindahan dokumen ke `docs/` oleh Master diterima; rujukan di README, rencana, dan dokumen sudah diperbaiki. Risiko: tidak ada setelah perbaikan.
12. Nama host untuk unduhan daftar yang lewat resolver sistem (bisa berputar ke hecate) dipindah ke desain Fase 2. Risiko: pembaruan daftar gagal.
13. Direktif `go` dinaikkan dari 1.26.8 ke 1.26.9, karena 1.26.8 terdampak kerentanan yang dipanggil kode (`dnsproxy` v0.86.0 hanya butuh ≥ 1.26.8). Risiko: satu unduhan toolchain lagi.

## Minor yang ditunda

- `rules.file` kosong dan `log.dir` kosong (saat log aktif) lolos validasi → diperketat di Fase 1 (tugas 10).
- `/hecate.yaml` belum ada di `.gitignore` → ditambahkan di Fase 1 (tugas 10).
- Hanya `check` yang mencetak `Warnings()`; `run` harus mencetaknya juga, dan `enabled:` yang dihilangkan kini berarti nonaktif → diubah menjadi aktif di Fase 1 (tugas 10).
- Batas longgar: port 0, `listen` ganda, TTL sangat besar, `update_interval` 1 ms → dibatasi sebelum konversi ke `uint32` di Fase 1.
- Belum ada tes yang mengunci "nilai bawaan sama dengan contoh" atau salah ketik kunci bersarang; pemangkasan BOM eksplisit redundan dengan `yaml.v3` (tidak berbahaya).
- Satu kerentanan di modul yang di-require tetapi tidak dipanggil → periksa ulang setelah Fase 1 memakai `dnsproxy` sungguhan.
- Baris status di `architecture.md` dan `roadmap.md` sempat mendahului selesainya tinjauan → sudah dikoreksi di pembaruan dokumen ini.

## Serah terima ke Fase 1

- `dnsproxy` tidak punya ACL klien, jadi ACL `allowed_clients` ada di engine (`internal/engine/acl.go`) dan menjadi langkah pertama `Before`; perubahan sudah tertulis di `architecture.md` §5.3 dan §6 serta `roadmap.md` Fase 1.
- Tugas Fase 1 diturunkan langsung dari `roadmap.md` (tanpa dokumen rencana terpisah); aturan kerja Master: tugas dulu, dokumen di akhir, peninjauan agent sekali per fase.
- Lingkungan: UDP 53 di PC masih dipegang ICS; pengembangan memakai `127.0.0.1:5300`.
