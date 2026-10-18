---
description: Run the full xdns quality-gate set (format, vet, build, tests with coverage, race, module checks, govulncheck, config check) and report results honestly.
---

# /gates - full quality gates

Run from the project root. Use PowerShell or Git Bash syntax as appropriate; do not chain with `&&` in Windows PowerShell 5.1. Capture the key lines of each result; never claim a gate passed without running it.

| # | Command | Pass means |
|---|---|---|
| 1 | `gofmt -l .` | prints nothing |
| 2 | `go vet ./...` | no output, exit 0 |
| 3 | `go build ./...` | no output, exit 0 |
| 4 | `go test ./... -cover -count=1` | every package `ok`; note coverage per package |
| 5 | race (below) | every package `ok`, no `WARNING: DATA RACE` |
| 6 | `go mod verify` | `all modules verified` |
| 7 | `go mod tidy -diff` | prints nothing |
| 8 | `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` | 0 vulnerabilities affecting the code (`No vulnerabilities found`, or only ones in required modules that are not called) |
| 9 | `go run ./cmd/xdns check -config configs/xdns.example.yaml` | exit 0 |
| 10 | `GOOS=linux GOARCH=amd64 go build ./...` (cgo off) | exit 0 (keeps the Phase 6 cross-build healthy) |

Race command (cgo for this command only; never permanent):

```
PowerShell:  $env:CGO_ENABLED='1'; go test -race ./... -count=1; Remove-Item Env:CGO_ENABLED
Git Bash:    CGO_ENABLED=1 go test -race ./... -count=1
```

Cross-build in PowerShell: `$env:GOOS='linux'; $env:GOARCH='amd64'; go build ./...; Remove-Item Env:GOOS; Remove-Item Env:GOARCH`.

Notes
- `govulncheck@latest` needs internet and may download tools the first time.
- If a gate fails: report the exact failing output, fix with RED -> GREEN where behavior is involved, then re-run the whole set once.
- A new vulnerability in a called stdlib/module path usually means a Go toolchain or module bump (as in Phase 0: Go 1.26.9, x/net v0.60.0). Record the ids and the fix.

Report format (Indonesian, short): a table of the 10 rows with LULUS/GAGAL, coverage numbers, and for any failure the first decisive lines of output. Put long output in `state/WORKLOG.md` or a file and give the path.
