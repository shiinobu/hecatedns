---
name: hecate-go-tdd
description: Test-first patterns for the hecate Go repository - table-driven tests, fake DNS upstream on loopback, httptest for list downloads, fuzz targets, atomic-snapshot and concurrency tests under the race detector, coverage and benchmark commands. Use whenever writing or fixing code in hecate.
---

# HecateDNS Go TDD patterns

Applies to every task. Cycle: RED (test fails for the right reason) -> GREEN (minimum code) -> refactor. Existing style to imitate: `internal/config/config_test.go`, `cmd/hecate/main_test.go`. The type and function names below are illustrations; use the names your implementation defines.

## Table-driven test

```go
func TestDomainSet_Match(t *testing.T) {
	set := mustBuild(t, "example.com", "ads.tracker.net")
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{"exact", "example.com", true},
		{"subdomain", "a.b.example.com", true},
		{"similar suffix is not a subdomain", "notexample.com", false},
		{"parent of an entry", "tracker.net", false},
		{"uppercase", "WWW.Example.COM", true},
		{"trailing dot", "example.com.", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := set.Match(tt.query); got != tt.want {
				t.Errorf("Match(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}
```

Helpers call `t.Helper()`. Files go in `t.TempDir()` with mode `0o600`. Compare structs with `reflect.DeepEqual` or field by field with a clear message. Name tests `TestType_Behavior`.

## Fake DNS upstream on loopback (no internet, random port)

```go
func startFakeUpstream(t *testing.T, h dns.HandlerFunc) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	started := make(chan struct{})
	srv := &dns.Server{PacketConn: pc, Handler: h, NotifyStartedFunc: func() { close(started) }}
	go func() { _ = srv.ActivateAndServe() }()
	<-started
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String()
}
```

Query it with `dns.Client{}.Exchange(msg, addr)` where `msg := new(dns.Msg); msg.SetQuestion(dns.Fqdn("ads.example."), dns.TypeA)` (miekg/dns v1.1.73, v1 API).

## List download with `httptest`

Serve fixtures from `httptest.NewTLSServer` (lists must be `https://`); cover: normal body, `ETag`/304, body over the size cap, slow server vs the 30 s timeout (use a short test timeout knob), redirect, 500 error keeps the old cache, atomic write leaves no partial file.

## Fuzz a parser (must never panic)

```go
func FuzzParseRuleLine(f *testing.F) {
	for _, seed := range []string{"block example.com", "allow a.b", "# c", "", "block", "block  *.x.com", "\xff\xfe"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, line string) {
		_, _, _ = parseRuleLine(line)
	})
}
```

Run briefly while developing: `go test ./internal/engine -run xxx -fuzz FuzzParseRuleLine -fuzztime 20s`. Commit any interesting failing input that fuzzing writes under `testdata/fuzz`.

## Atomic snapshot read while swapping (run under `-race`)

```go
func TestSnapshot_SwapWhileReading(t *testing.T) {
	var cur atomic.Pointer[DomainSet]
	cur.Store(mustBuild(t, "a.com"))
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = cur.Load().Match("x.a.com")
				}
			}
		}()
	}
	for i := 0; i < 200; i++ {
		cur.Store(mustBuild(t, "a.com", "b.com"))
	}
	close(stop)
	wg.Wait()
}
```

For the engine: spawn many goroutines calling `Before`/`After` with a shared `*Engine` while rules reload; assert decisions stay correct and exactly one log record per query is written.

## Commands

```
go test ./internal/engine/... -count=1                 # package loop
go test ./internal/engine/... -cover -count=1          # coverage (>= 80% on logic packages)
go test ./internal/engine/... -coverprofile="$env:TEMP\c.out"; go tool cover -func "$env:TEMP\c.out"   # find uncovered code (PowerShell)
go test ./internal/engine/... -bench . -benchmem -run xxx
```

Race (per command): `$env:CGO_ENABLED='1'; go test -race ./internal/engine/... -count=1; Remove-Item Env:CGO_ENABLED`.

## Test-quality checks

- A new test must have been seen failing. A test that cannot fail proves nothing.
- Hostile inputs for anything that parses external data: empty, one huge line, binary bytes, invalid UTF-8, odd Unicode, IDN/punycode, trailing dots, mixed case, IPv4-mapped IPv6, zero or multiple DNS questions, truncated messages.
- Time-dependent code (retention, rotation, reload by mtime, polling) takes a clock or interval parameter so tests do not sleep long.
- Tests never depend on the internet, on a fixed port, or on files outside `t.TempDir()` and `testdata/`.
- Weak-test probe: break the code in a scratch copy of the repo and confirm a test goes red.
