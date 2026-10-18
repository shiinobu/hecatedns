# xdns

Server DNS pribadi yang memblokir iklan di browser (pop-up, redirect, click-redirect) tanpa memaksa Google SafeSearch dan tanpa aplikasi VPN. Dipakai sendiri, non-komersial.

**Status:** Fase 0 (fondasi), Fase 1 (engine inti + backend dnsproxy), dan Fase 2 (daftar blokir, pemeriksaan rantai CNAME, nama khusus, uji mandiri) selesai. Server mengunduh dan memuat ketiga daftar bawaan (HaGeZi Multi PRO, HaGeZi Pop-Up Ads, ABPindo — ±250 ribu domain), memblokir domain iklan baik secara langsung maupun lewat rantai CNAME, menjawab nama khusus (`use-application-dns.net` NXDOMAIN, `_dns.resolver.arpa` NODATA), serta memverifikasi SafeSearch dan daftar blokir lewat `xdns check`. Lihat `docs/roadmap.md`, `docs/phase-1-report.md`, dan `docs/phase-2-report.md`.

## Perintah

```
go build ./...                                              # bangun semua paket
go test ./... -cover                                        # jalankan tes
go run ./cmd/xdns lists update -config xdns.yaml            # unduh/perbarui semua daftar blokir aktif
go run ./cmd/xdns lists status -config xdns.yaml            # tampilkan status cache daftar blokir
go run ./cmd/xdns check -config configs/xdns.example.yaml   # validasi konfigurasi + uji mandiri (hulu, SafeSearch, sampel daftar)
go run ./cmd/xdns run   -config xdns.yaml                   # jalankan server (Ctrl+C untuk berhenti)
go run ./cmd/xdns rule add block contoh.com -config xdns.yaml     # blokir contoh.com dan semua subdomainnya
go run ./cmd/xdns rule add allow baik.contoh.com -config xdns.yaml # kecualikan (allow mengalahkan block)
go run ./cmd/xdns rule list   -config xdns.yaml             # tampilkan aturan
go run ./cmd/xdns rule remove contoh.com -config xdns.yaml  # hapus aturan
go run ./cmd/xdns log -since 10m -blocked -config xdns.yaml # baca log query
```

Salin `configs/xdns.example.yaml` menjadi `xdns.yaml` lalu sesuaikan. Kunci yang tidak ditulis memakai nilai bawaan, sedangkan kunci yang salah ketik, kunci ganda, dan lebih dari satu dokumen YAML ditolak dengan pesan galat. Server membaca ulang berkas aturan sendiri (setiap `rules.poll_interval`, bawaan 2 detik) dan memantau pembaruan cache daftar di disk, jadi `xdns rule` dan `xdns lists update` berlaku tanpa restart.

Mengetes server: `nslookup` di Windows mengabaikan `-port`, jadi pakai skrip yang disertakan:

```
powershell -File scripts/dnsquery.ps1 example.com -Port 5300
powershell -File scripts/dnsquery.ps1 www.contoh.com -Type HTTPS -Port 5300
```

## Dokumen

- `docs/architecture.md`: desain, keputusan, dan kriteria sukses
- `docs/roadmap.md`: fase kerja dan gerbang keputusan
- `docs/phase-0-report.md`, `docs/phase-1-report.md`, `docs/phase-2-report.md`: laporan tiap fase
- `docs/research.md`: riset pemblokiran iklan beserta sumbernya
- `docs/superpowers/plans/`: rencana tugas Fase 0 (arsip)

