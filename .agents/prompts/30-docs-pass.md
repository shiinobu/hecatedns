# 30 - Documentation pass and phase close (the only time `docs/` is edited)

Paste everything between the lines after the review fixes are done and the gates are green.

---

Semua tugas fase dan perbaikan hasil tinjauan sudah selesai. Sekarang lakukan **satu kali pass dokumentasi** dan tutup fase, persis seperti `.agents/workflows/close-phase.md`.

1. Read `.agents/rules/02-workflow.md`, `.agents/state/STATE.md` (the list "Docs to write at phase end"), the review report, the WORKLOG entries of this phase, and `docs/phase-0-report.md` as the model for the report.
2. Verify first, then write: re-run `.agents/workflows/gates.md` on the current HEAD and re-check each "Selesai bila" criterion of the phase on the real binary. Only evidence-backed statements go into documents. Label anything unverified as unverified.
3. Update `docs/architecture.md` (status line, version, section 18 verified items, the stale "no gcc / no `-race`" lines in sections 12 and 15, any design change such as the registry-cycle ruling and the dnsproxy `Handler` being an interface), `docs/roadmap.md` (status, ticks, change-history row), `README.md` (status and commands), `docs/research.md` only if new researched facts exist. Language: Indonesian, matching the existing text.
4. Create `docs/phase-<N>-report.md` (hasil, tabel tugas dan commit, bukti verifikasi dengan angka dan perintah, tabel temuan tinjauan -> tes perbaikan, keputusan bernomor beserta risikonya, minor yang ditunda, serah terima ke fase berikutnya).
5. One commit: `docs: update documents after phase <N> review`. `gofmt -l .` must stay empty.
6. Create `.agents/state/PHASE-<N+1>-TASKS.md` from `docs/roadmap.md`, update `.agents/state/STATE.md` (phase status, HEAD, gates, gate questions for Master) and append a WORKLOG summary.
7. Report to Master in Indonesian (<= 12 lines) with the path of the phase report and the next gate's questions if any. Then STOP and wait for "lanjut".

---
