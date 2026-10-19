# 21 - Fix the review findings (after prompt 20; implementer session)

Paste everything between the lines. The implementer may be a new session: it needs the review file, not the old conversation.

---

Peninjauan fase sudah selesai. Laporannya ada di `.agents/state/reviews/phase-<PHASE>-review.md`. Perbaiki temuan **Critical** dan **Important**; temuan **Minor** hanya bila sepele (sisanya masuk daftar "ditunda" untuk laporan fase).

1. Read `.agents/rules/` (all), `.agents/state/STATE.md`, and the review report completely. Check the claim of each finding yourself before fixing: reproduce it with the failing input from the report. If a finding is wrong or already handled, write the evidence in the WORKLOG instead of changing code.
2. For each real finding, in order of severity: write a test that fails (RED, show the decisive failing line), make the minimum fix (GREEN), run the package tests, commit as `fix: <what> ` (one commit per finding, no co-author trailer). Use the `hecate-go-tdd` skill. Look for the same mistake in sibling code and cover it too.
3. After all fixes: run `.agents/workflows/gates.md` completely (race and `govulncheck` included). Everything must be green; a new vulnerability means a toolchain/module bump (record ids).
4. Update the WORKLOG (finding -> fix test name -> commit id), tick the review line in `.agents/state/PHASE-<PHASE>-TASKS.md`, and add the deferred Minor items to the "Docs to write at phase end" list in `.agents/state/STATE.md`.
5. Do not edit `docs/` yet. Report to Master in Indonesian (<= 10 lines): temuan yang diperbaiki, yang ditolak beserta alasannya, hasil gate. Then the next step is prompt 30 (documentation pass) when Master says "lanjut".

---
