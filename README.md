# xdns

Server DNS pribadi yang memblokir iklan di browser (pop-up, redirect, click-redirect) tanpa memaksa Google SafeSearch dan tanpa aplikasi VPN. Dipakai sendiri, non-komersial.

**Status:** Fase 0 (fondasi). Server belum berjalan; yang tersedia saat ini adalah `xdns check` untuk memvalidasi konfigurasi. Tahap berikutnya ada di `roadmap.md`.

## Perintah

```
go build ./...                                              # bangun semua paket
go test ./... -cover                                        # jalankan tes
go run ./cmd/xdns check -config configs/xdns.example.yaml   # validasi konfigurasi
```

Salin `configs/xdns.example.yaml` menjadi `xdns.yaml` lalu sesuaikan. Kunci yang tidak ditulis memakai nilai bawaan, sedangkan kunci yang salah ketik ditolak dengan pesan galat.

## Dokumen

- `architecture.md`: desain, keputusan, dan kriteria sukses
- `roadmap.md`: fase kerja dan gerbang keputusan
- `research.md`: riset pemblokiran iklan beserta sumbernya
- `docs/superpowers/plans/`: rencana tugas rinci per fase
