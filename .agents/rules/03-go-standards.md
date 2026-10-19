---
trigger: always_on
description: Go code, test, error-handling and file-size standards for the hecate repository, derived from the existing Phase 0 code.
---

# Go standards for HecateDNS

Go 1.26.9 (module `go` directive). Dependencies: `github.com/AdguardTeam/dnsproxy v0.86.0`, `github.com/miekg/dns v1.1.73` (v1 API), `gopkg.in/yaml.v3`. Add no others without need.

## Style (match the existing code in `internal/config` and `cmd/hecate`)

- `gofmt` clean. Idiomatic Go. Doc comment on every exported symbol and on every package (`// Package engine ...`), in English. Comments explain why, not what.
- Named constants for thresholds, TTLs, limits and string enums (see `config.ModeNullIP`, `defaultBlockTTL`). No magic numbers.
- User-facing CLI text is Indonesian (`penggunaan: hecate <perintah> [opsi]`, `belum diimplementasi`). Log fields and error strings for developers are English.
- Exit codes live in constants (`exitOK=0`, `exitError=1`, `exitUsage=2`). Commands take `io.Writer` for stdout/stderr so tests can capture output.
- The HackHub-era rules found in `.claude/rules` (zero comments in `src/`, `trace()`, `tsc`/esbuild, `docs/implementation-rules.md`) do NOT apply here.

## Size and structure

- Files 200-400 lines typical, 800 as a soft ceiling (split by responsibility, not by type). Functions under 50 lines. Nesting at most 4 levels: use early returns.
- High cohesion, low coupling. `engine` must not import `backend`, `lists` or `qlog`; `lists` and `qlog` feed the engine through small interfaces defined in `engine`. Beware import cycles (see `notes/open-questions.md`, registry cycle).
- No global mutable state. Shared state is an immutable snapshot swapped atomically (`atomic.Pointer[T]`); never mutate a published snapshot. Prefer returning new values over mutating arguments (do not mutate `q.Req` or the upstream answer; copy first).

## Errors

- Handle every error explicitly. Wrap with context: `fmt.Errorf("load rules %s: %w", path, err)`. Log once, at the layer that handles it. Never swallow an error silently.
- Never panic on external input (DNS messages, list lines, rule lines, YAML, flags). A malformed input is an error value or a skipped line with a counted warning.
- Validate at boundaries: config load, list parsing, rule parsing, DNS question validation, CLI arguments. Fail fast with a clear message; report all config problems at once (see `config.Validate`).
- Policy: engine errors fail OPEN (forward the query); the client ACL fails CLOSED (REFUSED). Details in `04-engine-contract.md`.

## Tests (TDD is mandatory)

- Standard `testing` package only. Table-driven tests with `t.Run`. Names like `TestLoad_RejectsMultipleDocuments`, `TestRun`. Helpers call `t.Helper()`. Use `t.TempDir()` for files and `0o600` for written files.
- Tests must not need the internet or fixed ports: use `httptest`, fake upstreams on `127.0.0.1:0`, random ports.
- Fuzz tests for parsers (list parser, rule parser). Benchmarks for the matcher (`-bench . -benchmem`).
- Concurrency code gets a test that hammers the real code path from many goroutines, run under `-race` (see `06-environment.md`).
- Coverage: at least 80% per package that holds logic; `engine` at least 80% is a Phase 1 "done when" criterion. Check with `go test ./... -cover -count=1`.
- Weak test check: when a test looks too easy, break the code on purpose (in a scratch copy, not the working tree) and see whether the test notices.
- See the `hecate-go-tdd` skill for ready patterns.

## Security hygiene before every commit

No hardcoded secrets; all external input validated; error messages to DNS clients never carry internal detail; log files are not world-readable by design choice; no new network exposure without Master's yes. Full threat model: `05-security-and-input.md`.
