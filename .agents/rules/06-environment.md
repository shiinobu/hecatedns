---
trigger: always_on
description: Master's PC - Windows 11, shells, Go toolchain, quality-gate commands, race detector via w64devkit, ports, and tool gaps.
---

# Environment (measured 2026-10-18)

## Machine

- Windows 11 Pro. Two shells: **PowerShell** (Windows PowerShell 5.1 - no `&&`/`||`, no ternary; chain with `;` and `if ($?) { ... }`) and **Git Bash** (POSIX syntax, `/c/Users/...` paths). Use each shell's own syntax.
- Project root: `C:\Users\Administrator\Documents\Vscode\xdns`. LAN IP of this PC `192.168.1.99`, gateway `192.168.1.1`. A virtual adapter `172.25.144.1` exists (probably Hyper-V/WSL).
- Ports: **UDP 0.0.0.0:53 is held by Internet Connection Sharing** (service `SharedAccess`); UDP 5353 by mDNS. `5300` and `853` are free. Development port: `127.0.0.1:5300`. Android cannot set a DNS port, so the phone test (Phase 3) needs UDP 53 on `192.168.1.99` - that is decision gate G2; do not stop ICS or touch the firewall without Master.
- Tools present: Go 1.26.4 (the module needs 1.26.9, fetched automatically through `GOTOOLCHAIN=auto`, needs internet the first time), git 2.54.0, 7-Zip, curl, Python, Node. **No `gh` CLI** (use the GitHub REST API with curl if ever needed). The Exa MCP free limit is used up; use normal web search/fetch.
- Source of dnsproxy for reading: `C:\Users\Administrator\go\pkg\mod\github.com\!adguard!team\dnsproxy@v0.86.0\` (uppercase letters in module paths are stored as `!` + lowercase). miekg/dns: `...\pkg\mod\github.com\miekg\dns@v1.1.73\`. `go doc github.com/AdguardTeam/dnsproxy/proxy.Config` also works from inside the repo.

## Quality gates (run from the project root)

```
gofmt -l .                         # must print nothing
go vet ./...
go build ./...
go test ./... -cover -count=1
go mod verify                      # "all modules verified"
go mod tidy -diff                  # must print nothing
go run golang.org/x/vuln/cmd/govulncheck@latest ./...   # 0 vulnerabilities AFFECTING the code
go run ./cmd/xdns check -config configs/xdns.example.yaml
```

Baseline at HEAD `4b546fc`: `go test ./... -cover -count=1` prints two `ok` lines (`xdns/cmd/xdns` 97.3%, `xdns/internal/config` 96.2%), 92 tests pass. `govulncheck` shows 0 affected (one vulnerability in a required module that our code does not call).

## Race detector (works since 2026-10-18)

w64devkit v2.10.1 (gcc 16.2.0) is extracted at `C:\Users\Administrator\tools\w64devkit` and `go env CC` points at its `gcc.exe`. PATH was NOT changed. `go test -race` needs cgo, and `CGO_ENABLED` stays 0 by default, so enable it for that one command only:

```
PowerShell:  $env:CGO_ENABLED='1'; go test -race ./... -count=1; Remove-Item Env:CGO_ENABLED
Git Bash:    CGO_ENABLED=1 go test -race ./... -count=1
```

**Never** set `CGO_ENABLED=1` permanently (`go env -w` or system env): it is forced on for cross-compiles too and breaks the Linux build for the VPS (Phase 6). A plain `GOOS=linux GOARCH=amd64 go build ./...` (cgo off) must keep working.

## Practical notes

- Git emits LF/CRLF warnings on Windows; `.gitattributes` (`* text=auto eol=lf`) keeps files LF. Run `gofmt -l .` to catch drift.
- **Windows `nslookup` ignores `-port`** (verified 2026-10-18 on this PC: `nslookup -port=5300 example.com 127.0.0.1` printed "No response from server" while xdns answered fine on 5300; the server never saw the query, so nslookup was asking UDP 53). `Resolve-DnsName` has no port option either. To query xdns on `127.0.0.1:5300` use `scripts/dnsquery.ps1 example.com -Port 5300` (A/AAAA/CNAME and rcode). On port 53 (phone test, Phase 3) plain `nslookup` works. In PowerShell 5.1 nslookup output that goes to stderr is wrapped as a `NativeCommandError` record, so judge by the printed text.
- When a Windows path is passed to a Go test (`../../configs/xdns.example.yaml`) keep it relative; tests run with the package directory as cwd.
- Scratch location for temporary work: a temp directory outside the repo (for example `$env:TEMP\xdns-scratch`). Delete what you create there when done.
- `data/` (runtime state), `/xdns.yaml` (to be added to `.gitignore` in Phase 1), `*.exe`, `*.pem`, `*.key` must never be committed.
