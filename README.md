# xdns

Server DNS pribadi yang memblokir iklan di browser (pop-up, redirect, click-redirect) tanpa memaksa Google SafeSearch dan tanpa aplikasi VPN. Dipakai sendiri, non-komersial.

**Status:** Fase 0 (fondasi) selesai. Server belum berjalan; yang tersedia saat ini adalah `xdns check` untuk memvalidasi konfigurasi. Berikutnya Fase 1 (engine filter + backend dnsproxy), lihat `docs/roadmap.md`.

## Perintah

```
go build ./...                                              # bangun semua paket
go test ./... -cover                                        # jalankan tes
go run ./cmd/xdns check -config configs/xdns.example.yaml   # validasi konfigurasi
```

Salin `configs/xdns.example.yaml` menjadi `xdns.yaml` lalu sesuaikan. Kunci yang tidak ditulis memakai nilai bawaan, sedangkan kunci yang salah ketik, kunci ganda, dan lebih dari satu dokumen YAML ditolak dengan pesan galat.

## Dokumen

- `docs/architecture.md`: desain, keputusan, dan kriteria sukses
- `docs/roadmap.md`: fase kerja dan gerbang keputusan
- `docs/research.md`: riset pemblokiran iklan beserta sumbernya
- `docs/superpowers/plans/`: rencana tugas Fase 0 (arsip)
