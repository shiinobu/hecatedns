# HecateDNS

Server DNS pribadi yang memblokir iklan di browser (pop-up, redirect, click-redirect) tanpa memaksa Google SafeSearch dan tanpa aplikasi VPN. Dipakai sendiri, non-komersial. Dulu bernama xdns (diganti 2026-10-19); biner dan perintahnya sekarang `hecate`.

**Status:** Fase 0 (fondasi), Fase 1 (engine inti + backend dnsproxy), Fase 2 (daftar blokir, pemeriksaan rantai CNAME, nama khusus, uji mandiri), dan Fase 3 (tes di HP nyata lewat Wi-Fi rumah: `adblock.turtlecute.org` 95–97%, lihat `docs/test-report.md`) selesai. Fase 4 (backend kedua `native`, dipilih dengan `backend: native`; satu suite tes untuk kedua backend) selesai, termasuk tes HP (97%). Fase 5 (stabilisasi: review penuh Fase 4 dan perbaikannya, Ctrl+C anggun, peringatan hulu gagal beruntun, log berbatas ukuran, jalan otomatis saat boot lewat Task Scheduler) selesai; uji 7 hari dipindah ke Fase 6 di VPS karena PC ini dimatikan tiap malam. Server rumah memakai `native`. Server mengunduh dan memuat ketiga daftar bawaan (HaGeZi Multi PRO, HaGeZi Pop-Up Ads, ABPindo — ±250 ribu domain), memblokir domain iklan baik secara langsung maupun lewat rantai CNAME, menjawab nama khusus (`use-application-dns.net` NXDOMAIN, `_dns.resolver.arpa` NODATA), serta memverifikasi SafeSearch dan daftar blokir lewat `hecate check`. Lihat `docs/roadmap.md` dan laporan fase di `docs/` (`phase-1-report.md` sampai `phase-5-report.md`).

## Perintah

```
go build ./...                                                          # bangun semua paket
go test ./... -cover                                                    # jalankan tes
go run ./cmd/hecate lists update -config hecate.yaml                    # unduh/perbarui semua daftar blokir aktif
go run ./cmd/hecate lists status -config hecate.yaml                    # tampilkan status cache daftar blokir
go run ./cmd/hecate check -config configs/hecate.example.yaml           # validasi konfigurasi + uji mandiri (hulu, SafeSearch, sampel daftar)
go run ./cmd/hecate run   -config hecate.yaml                           # jalankan server (Ctrl+C untuk berhenti)
go run ./cmd/hecate rule add block contoh.com -config hecate.yaml       # blokir contoh.com dan semua subdomainnya
go run ./cmd/hecate rule add allow baik.contoh.com -config hecate.yaml  # kecualikan (allow mengalahkan block)
go run ./cmd/hecate rule list   -config hecate.yaml                     # tampilkan aturan
go run ./cmd/hecate rule remove contoh.com -config hecate.yaml          # hapus aturan
go run ./cmd/hecate log -since 10m -blocked -config hecate.yaml         # baca log query
go run ./scripts/compare -a 127.0.0.1:5301 -b 127.0.0.1:5302 -log-a LOG_A -log-b LOG_B  # bandingkan keputusan dua server (backend berbeda)
powershell -File scripts/autostart.ps1 install                          # PowerShell Administrator: jalan otomatis saat boot (Task Scheduler)
powershell -File scripts/autostart.ps1 status                           # keadaan tugas dan proses hecate
powershell -File scripts/autostart.ps1 remove                           # cabut tugas (satu-satunya rollback)
```

Salin `configs/hecate.example.yaml` menjadi `hecate.yaml` lalu sesuaikan. Kunci yang tidak ditulis memakai nilai bawaan, sedangkan kunci yang salah ketik, kunci ganda, dan lebih dari satu dokumen YAML ditolak dengan pesan galat. Server membaca ulang berkas aturan sendiri (setiap `rules.poll_interval`, bawaan 2 detik) dan memantau pembaruan cache daftar di disk, jadi `hecate rule` dan `hecate lists update` berlaku tanpa restart.

Mengetes server: `nslookup` di Windows mengabaikan `-port`, jadi pakai skrip yang disertakan:

```
powershell -File scripts/dnsquery.ps1 example.com -Port 5300
powershell -File scripts/dnsquery.ps1 www.contoh.com -Type HTTPS -Port 5300
```

## Dokumen

- `docs/architecture.md`: desain, keputusan, dan kriteria sukses
- `docs/roadmap.md`: fase kerja dan gerbang keputusan
- `docs/phase-0-report.md` sampai `docs/phase-5-report.md`, dan `docs/test-report.md`: laporan tiap fase dan tes HP
- `docs/research.md`: riset pemblokiran iklan beserta sumbernya
- `docs/superpowers/plans/`: rencana tugas Fase 0 (arsip)

