# 90 - Hand the project back to Claude Code (paste into Claude)

Claude does not read `.agents/` by itself, and `/ecc:resume-session` only reads `~/.claude/session-data`. Paste this as the first message of a new Claude Code session opened in `C:\Users\Administrator\Documents\Vscode\xdns`.

---

Master kembali ke Claude. Agent lain (Gemini 4 Argon) mengerjakan xdns selama Claude terkena batas pemakaian. Kamu tidak punya riwayat itu; ambil alih lewat disk, jangan lewat ingatan.

1. Baca `.agents/state/STATE.md`, 60 baris terakhir `.agents/state/WORKLOG.md`, task file fase aktif (`.agents/state/PHASE-N-TASKS.md`), `.agents/notes/open-questions.md` dan `.agents/notes/do-not-retry.md`.
2. Verifikasi, jangan percaya catatan: `git log --oneline -15`, `git status --short`, lalu `git log <HEAD lama di STATE.md>..HEAD --stat` untuk semua commit agent lain. Jalankan gate lengkap (`gofmt -l .`, `go vet ./...`, `go build ./...`, `go test ./... -cover -count=1`, `go mod verify`, `go mod tidy -diff`, race dengan `$env:CGO_ENABLED='1'; go test -race ./... -count=1`, `govulncheck`). Laporkan apa yang merah.
3. Tinjau kerja agent lain secukupnya: baca diff-nya, periksa bahwa tes memang ditulis lebih dulu (lihat WORKLOG), bahwa `docs/` tidak diubah di tengah fase, dan bahwa tidak ada push/remote/perubahan sistem. Perbaiki penyimpangan atau laporkan ke Master.
4. Pertahankan aturan Master: panggil "Master", jawab Bahasa Indonesia singkat, tugas dulu dokumen belakangan, satu review per fase (Opus `xdns-reviewer` bila tersedia), delegasi: Sonnet utama dan agent Opus `xdns-core` untuk bagian sulit.
5. Perbarui memori proyek bila ada fakta baru yang tidak tercatat di repo, lalu lanjutkan dari "Next" di STATE.md setelah Master bilang "lanjut". Jangan memulai fase baru tanpa "lanjut".

---
