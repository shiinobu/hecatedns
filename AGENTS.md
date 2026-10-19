# AGENTS.md

HecateDNS is a DNS-only browser ad blocker in Go (binary `hecate`). This file is a short guide for anyone, human or AI agent, who changes the code.

## Layout

`cmd/hecate` is the entry point. Everything else is under `backend/`: `controller` (CLI), `core` (backend contract, `dnsproxy` adapter, `native` server), `engine` (the filter), `service` (lists, query log, self-check), `lib` (config, operational log) and `test` (conformance suite, source-rule gates). Imports point downward only; see [docs/architecture.md](docs/architecture.md), section 4.

## Rules

- Source code carries **no comments** (Go, tests and PowerShell). `backend/test/nocomment` fails the build otherwise. Put the reasoning in `docs/`.
- Write the test first; keep coverage at 80% or more. Unit tests stay beside the code, black-box tests go in `backend/test/`.
- Files stay under 800 lines, functions under 50 lines, nesting at most 4 levels. Wrap errors with context and never swallow them.
- Lists, rules, configs and DNS messages are untrusted input.
- Do not bind port 53 in development (use `127.0.0.1:5300`), do not change system settings, and do not push without the owner's approval.

## Before you finish

```
gofmt -l .
go vet ./...
go build ./...
go test ./... -cover -count=1
CGO_ENABLED=1 go test -race ./... -count=1
go mod verify
go mod tidy -diff
govulncheck ./...
go run ./cmd/hecate check -config configs/hecate.example.yaml
```

Docs are in English and live in `docs/`.
