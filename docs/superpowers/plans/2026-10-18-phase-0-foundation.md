# xdns Fase 0 — Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (native, single agent, as chosen in `roadmap.md`) to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A buildable, tested Go skeleton for xdns: git repo, pinned module graph, strict config loader and validator, and an `xdns check` command.

**Architecture:** One Go module `xdns`. `internal/config` owns the YAML schema (defaults, strict decoding, path resolution, validation). `cmd/xdns` is a thin dispatcher whose `check` subcommand loads and validates a config; other subcommands are stubs until later phases. Every other package from `architecture.md` §4 exists only as an empty `doc.go` so the layout is fixed from day one.

**Tech Stack:** Go 1.26.8, `gopkg.in/yaml.v3`, stdlib `testing`. `github.com/miekg/dns` and `github.com/AdguardTeam/dnsproxy` are pinned now (blank imports) and used for real from Phase 1.

**Spec:** `architecture.md` (§2 D1/D9/D13/D15/D16, §4, §10) and `roadmap.md` (Fase 0).

## Global Constraints

- Module path `xdns`; `go 1.26.8` in `go.mod` (`dnsproxy` v0.86.0 requires it; local Go is 1.26.4 and `GOTOOLCHAIN=auto` downloads 1.26.8).
- Pinned versions: `github.com/miekg/dns v1.1.73`, `github.com/AdguardTeam/dnsproxy v0.86.0`, `gopkg.in/yaml.v3 v3.0.1`.
- YAML decoding is strict (`KnownFields(true)`): unknown keys are errors.
- Defaults: `listen` = `127.0.0.1:5300`; `allowed_clients` = `127.0.0.0/8`, `::1/128`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`; `upstreams` = `https://1.1.1.1/dns-query`, `https://8.8.8.8/dns-query`; `bootstrap` = `1.1.1.1`, `8.8.8.8`; `backend` = `dnsproxy`; `block` = `null_ip` with TTL `10s`; `cache` = enabled, 16 MB, min `0s`, max `1h`; `special_names` both `true`; `lists.update_interval` = `24h` with no sources; `rules` = `data/rules.txt`, poll `2s`; `log` = enabled, `data/logs`, 7 days; `data_dir` = `data`.
- Relative paths (`data_dir`, `rules.file`, `log.dir`) resolve against the directory of the config file.
- Errors wrapped with `%w`, never swallowed; no package-level mutable state; `Load` returns `Config` by value.
- Files typically 200–400 lines (800 max), functions < 50 lines, nesting ≤ 4, no magic numbers (named constants).
- `internal/config` test coverage ≥ 80%.
- Conventional commits (`chore:`, `feat:`, `test:`, `docs:`), no co-author trailer. `gofmt` and `go vet` clean. The HackHub-only overrides in `.claude/rules` do not apply.

## Review Focus

1. Config saved by Windows Notepad (UTF-8 BOM, CRLF line endings) must load. → Task 2.
2. A typo'd key (`bakend:`) or a repeated key must fail loudly, never be ignored. → Task 2.
3. `listen` written as a hostname (`localhost:5300`) is rejected with a message naming `listen[0]`. → Task 3.
4. A hostname upstream with empty `bootstrap` is rejected (resolving it through the system resolver could loop back into xdns). → Task 3.
5. An empty config file loads with defaults, and `check` warns that no blocklist source is enabled (otherwise Master may think blocking is on). → Tasks 3 and 4.

---

### Task 1: Repository, module, and skeleton

**Files:**
- Create: `.gitignore`, `go.mod`, `go.sum`, `cmd/xdns/main.go` (placeholder `func main() {}`)
- Create: `doc.go` (package comment only) in `internal/config`, `internal/engine`, `internal/lists`, `internal/qlog`, `internal/selfcheck`, `internal/backend`, `internal/backend/dnsproxybe`, `internal/backend/native`, `internal/backend/conformance`
- Existing, to be committed: `architecture.md`, `roadmap.md`, `research.md`, this plan

**Interfaces:**
- Produces: module `xdns` with the nine empty packages above.

- [ ] **Step 1: Initialise git.** `git init -b main` in the project root. If `git config user.name` / `user.email` are unset, set them repo-local (email `nightwishees@gmail.com`; name from `git config --global user.name`, else `nightwishees`).
- [ ] **Step 2: Write `.gitignore`** with these lines: `/data/`, `/bin/`, `/.claude/`, `*.exe`, `*.test`, `*.out`, `*.pem`, `*.key`.
- [ ] **Step 3: Create the module.** Run `go mod init xdns` then `go get go@1.26.8`. Expected: `go.mod` contains `go 1.26.8` (first run prints `go: downloading go1.26.8`).
- [ ] **Step 4: Create the nine `doc.go` files**, each a one-sentence package comment taken from `architecture.md` §4 (e.g. `// Package engine implements the transport-agnostic filtering engine.`). `dnsproxybe/doc.go` additionally has `import _ "github.com/AdguardTeam/dnsproxy/proxy"` and `native/doc.go` has `import _ "github.com/miekg/dns"`, each with a comment saying the blank import pins the dependency until the package gets real code. Create the placeholder `cmd/xdns/main.go`.
- [ ] **Step 5: Pin dependencies.** Run `go get github.com/AdguardTeam/dnsproxy@v0.86.0 github.com/miekg/dns@v1.1.73` then `go mod tidy`. Expected: `go list -m github.com/AdguardTeam/dnsproxy github.com/miekg/dns` prints `v0.86.0` and `v1.1.73`.
- [ ] **Step 6: Verify the skeleton.** Run `go build ./...`, `go vet ./...`, `go mod verify`, and `gofmt -l .`. Expected: no errors; `gofmt -l .` prints nothing; `go mod verify` prints `all modules verified`.
- [ ] **Step 7: Commit.** `git add -A` then `git commit -m "chore: initial commit with design docs and module skeleton"`. Expected: `git status --short` is empty.

---

### Task 2: Strict config loading with defaults and path resolution

**Files:**
- Create: `internal/config/config.go`, `internal/config/config_test.go`, `configs/xdns.example.yaml`
- Modify: `go.mod`, `go.sum` (adds `gopkg.in/yaml.v3 v3.0.1`)

**Interfaces:**
- Consumes: nothing.
- Produces (package `config`):

```go
const (
    BackendDNSProxy = "dnsproxy"; BackendNative = "native"
    ModeNullIP = "null_ip"; ModeNXDomain = "nxdomain"; ModeNoData = "nodata"; ModeRefused = "refused"
    KindBlock = "block"; KindAllow = "allow"
    FormatDomains = "domains"; FormatHosts = "hosts"
)

type Config struct {
    Backend string `yaml:"backend"`; Listen, AllowedClients, Upstreams, Bootstrap []string // yaml: listen, allowed_clients, upstreams, bootstrap
    Cache Cache; Block Block; SpecialNames SpecialNames; Lists Lists; Rules Rules; Log Log // yaml: cache, block, special_names, lists, rules, log
    DataDir string `yaml:"data_dir"`
}
type Cache struct { Enabled bool; SizeMB int; MinTTL, MaxTTL time.Duration }          // yaml: enabled, size_mb, min_ttl, max_ttl
type Block struct { Mode string; TTL time.Duration }                                  // yaml: mode, ttl
type SpecialNames struct { FirefoxCanary, DDR bool }                                  // yaml: firefox_canary, ddr
type Lists struct { UpdateInterval time.Duration; Sources []Source }                  // yaml: update_interval, sources
type Source struct { Name, URL, Kind, Format string; Enabled bool }                   // yaml: name, url, kind, format, enabled
type Rules struct { File string; PollInterval time.Duration }                         // yaml: file, poll_interval
type Log struct { Enabled bool; Dir string; RetentionDays int }                       // yaml: enabled, dir, retention_days

func Default() Config                  // fresh slices on every call
func Load(path string) (Config, error) // read, strip BOM, strict-decode over Default(), resolve paths
```

- [ ] **Step 1: Write `configs/xdns.example.yaml`** exactly as in `architecture.md` §10 (three sources: `hagezi-pro`, `hagezi-popups`, `abpindo`).
- [ ] **Step 2: Write the failing tests** in `internal/config/config_test.go` (each writes its YAML to `t.TempDir()`):
  - `TestLoad_ExampleConfig`: `Load("../../configs/xdns.example.yaml")` → no error; `Backend == "dnsproxy"`; `len(Lists.Sources) == 3`; `Lists.Sources[0].Name == "hagezi-pro"`; `Block.TTL == 10*time.Second`.
  - `TestLoad_DefaultsForMissingKeys`: file `backend: native\n` → `Backend == "native"`; `Listen == []string{"127.0.0.1:5300"}`; `Block.Mode == "null_ip"`; `Block.TTL == 10*time.Second`; `Cache.SizeMB == 16`; `Lists.UpdateInterval == 24*time.Hour`; `Rules.PollInterval == 2*time.Second`; `Log.RetentionDays == 7`.
  - `TestLoad_UserListReplacesDefault`: `listen: ["192.168.1.10:53"]` → `Listen` has exactly that one element.
  - `TestLoad_EmptyFileUsesDefaults`: empty file → `Backend == "dnsproxy"` and no error.
  - `TestLoad_BOMAndCRLF`: bytes `"\xef\xbb\xbfbackend: native\r\nlisten:\r\n  - 127.0.0.1:5300\r\n"` → no error, `Backend == "native"`.
  - `TestLoad_RejectsUnknownKey`: `bakend: native\n` → error text contains `bakend`.
  - `TestLoad_RejectsDuplicateKey`: `backend: native\nbackend: dnsproxy\n` → non-nil error.
  - `TestLoad_ResolvesRelativePaths`: config in dir `D` with defaults → `DataDir == filepath.Join(D,"data")`, `Rules.File == filepath.Join(D,"data","rules.txt")`, `Log.Dir == filepath.Join(D,"data","logs")`; a config with an absolute `data_dir` keeps it unchanged.
  - `TestLoad_MissingFile`: `errors.Is(err, os.ErrNotExist)`.
  - `TestLoad_InvalidYAML`: `backend: [unclosed` → error text contains the file path.
- [ ] **Step 3: Run the tests to verify they fail.** Run `go test ./internal/config/ -v`. Expected: FAIL (build error: `Load` undefined).
- [ ] **Step 4: Implement `config.go`.** Add `gopkg.in/yaml.v3@v3.0.1` with `go get`. `Load` reads the file, trims a UTF-8 BOM, decodes with `yaml.NewDecoder(...).KnownFields(true)` into `Default()` (treat `io.EOF` from an empty document as "no overrides"), resolves relative paths against `filepath.Dir(path)` on its local copy, and wraps errors as `fmt.Errorf("config %s: %w", path, err)`. Durations rely on yaml.v3 decoding strings like `10s` into `time.Duration`.
- [ ] **Step 5: Run the tests to verify they pass.** Run `go test ./internal/config/ -v`. Expected: PASS for all.
- [ ] **Step 6: Commit.** `git add configs internal/config go.mod go.sum` then `git commit -m "feat: add strict YAML config loader with defaults"`.

---

### Task 3: Validation and warnings

**Files:**
- Create: `internal/config/validate.go`, `internal/config/validate_test.go`
- Modify: `internal/config/config.go` (`Load` calls `Validate` after path resolution)

**Interfaces:**
- Consumes: Task 2 `Config`, `Default()`, constants.
- Produces: `func (c Config) Validate() error` (all problems joined with `errors.Join`, each prefixed by its field path such as `listen[0]` or `lists.sources[1].url`); `func (c Config) Warnings() []string`.

- [ ] **Step 1: Write the failing tests** in `validate_test.go`:
  - `TestValidate_Rejects` (table: name, mutation of `Default()`, wanted substring):
    unknown backend `foo` → `backend`; `Listen=nil` → `listen`; `["localhost:5300"]` → `listen[0]`; `["127.0.0.1"]` → `listen[0]`; `AllowedClients=["10.0.0.0/33"]` → `allowed_clients[0]`; `Upstreams=nil` → `upstreams`; `["ftp://1.1.1.1"]` → `upstreams[0]`; `Upstreams=["https://dns.google/dns-query"]` with `Bootstrap=nil` → `bootstrap`; `Bootstrap=["dns.google"]` → `bootstrap[0]`; `Block.Mode="weird"` → `block.mode`; `Block.TTL=-1s` → `block.ttl`; cache enabled with `SizeMB=0` → `cache.size_mb`; `MinTTL=2h, MaxTTL=1h` → `cache.min_ttl`; source URL `http://x/y.txt` → `lists.sources[0].url`; source kind `x` → `lists.sources[0].kind`; source format `adblock` → `lists.sources[0].format`; two sources with the same name → `duplicate`; empty source name → `lists.sources[0].name`; one source and `UpdateInterval=0` → `lists.update_interval`; `Rules.PollInterval=0` → `rules.poll_interval`; log enabled with `RetentionDays=0` → `log.retention_days`; `DataDir=""` → `data_dir`.
  - `TestValidate_Accepts`: `Default()`; `Listen=["[::1]:5300"]` with `AllowedClients=["::1/128"]`; each upstream form `1.1.1.1`, `1.1.1.1:53`, `udp://1.1.1.1`, `tcp://1.1.1.1:53`, `tls://1.1.1.1:853`, `https://1.1.1.1/dns-query`; hostname upstream with `Bootstrap=["1.1.1.1"]`.
  - `TestValidate_ReportsAllErrors`: bad backend and bad block mode together → error text contains both `backend` and `block.mode`.
  - `TestWarnings`: `Default().Warnings()` has exactly one entry containing `daftar`; the example config has none.
  - `TestLoad_InvalidConfigFails`: file `backend: nope\n` → `Load` error contains `backend`.
- [ ] **Step 2: Run the tests to verify they fail.** Run `go test ./internal/config/ -v`. Expected: FAIL (`Validate` undefined).
- [ ] **Step 3: Implement `Validate` and `Warnings` in `validate.go`.** Split into one helper per section (`validateListen`, `validateClients`, `validateUpstreams`, `validateBlock`, `validateCache`, `validateLists`, `validateRules`, `validateLog`) to stay under 50 lines each. Use `netip.ParseAddrPort` / `netip.ParsePrefix` / `netip.ParseAddr` and `net/url`. An upstream is valid as a bare IP, `ip:port`, or a URL with scheme `udp|tcp|tls|https` and non-empty host; a host that is not an IP literal requires non-empty `Bootstrap`. List sources must be `https`, kind in `block|allow`, format in `domains|hosts`, names unique and non-empty. `Warnings` returns one Indonesian message when no enabled source exists. Wire `Validate` into `Load` after path resolution.
- [ ] **Step 4: Run the tests to verify they pass.** Run `go test ./internal/config/ -cover`. Expected: PASS, coverage ≥ 80%.
- [ ] **Step 5: Commit.** `git add internal/config` then `git commit -m "feat: validate config and report warnings"`.

---

### Task 4: CLI dispatcher, `check`, and README

**Files:**
- Modify: `cmd/xdns/main.go`
- Create: `cmd/xdns/check.go`, `cmd/xdns/main_test.go`, `README.md`

**Interfaces:**
- Consumes: `config.Load(path string) (Config, error)`, `Config.Warnings() []string`.
- Produces:

```go
const ( exitOK = 0; exitError = 1; exitUsage = 2 )
func run(args []string, stdout, stderr io.Writer) int      // dispatch on args[0]
func runCheck(args []string, stdout, stderr io.Writer) int // flag -config (default "xdns.yaml")
```

- [ ] **Step 1: Write the failing tests** in `main_test.go` (`TestRun_Table`, using `bytes.Buffer` for both writers):
  - no args → exit 2, stderr contains `penggunaan`;
  - `bogus` → exit 2, stderr contains `bogus`;
  - each of `run`, `log`, `rule`, `lists` → exit 2, stderr contains `belum diimplementasi`;
  - `check -config ../../configs/xdns.example.yaml` → exit 0, stdout contains `konfigurasi valid` and `backend: dnsproxy`;
  - `check -config <path to missing file>` → exit 1, stderr contains that path;
  - `check -config <file "backend: nope">` → exit 1, stderr contains `backend`;
  - `check -config <empty file>` → exit 0, stdout contains `peringatan`;
  - `check -config <a directory>` → exit 1.
- [ ] **Step 2: Run the tests to verify they fail.** Run `go test ./cmd/xdns/ -v`. Expected: FAIL (`run` undefined).
- [ ] **Step 3: Implement `run` and `runCheck`.** `main` calls `os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))`. `runCheck` parses its own `flag.FlagSet`, loads the config, and prints lines `konfigurasi valid: <path>`, `backend: <name>`, `listen: <comma list>`, `upstreams: <n>`, `daftar aktif: <n>`, then one `peringatan: <text>` line per warning. Errors go to stderr, exit 1.
- [ ] **Step 4: Run the tests to verify they pass.** Run `go test ./cmd/xdns/ -v`. Expected: PASS.
- [ ] **Step 5: Smoke-test the real command.** Run `go run ./cmd/xdns check -config configs/xdns.example.yaml`. Expected: prints `konfigurasi valid`, `backend: dnsproxy`, `daftar aktif: 3`, exit code 0.
- [ ] **Step 6: Write `README.md`** (Indonesian, short): what xdns is, current status (Fase 0), how to build/test/check (`go build ./...`, `go test ./... -cover`, `go run ./cmd/xdns check -config configs/xdns.example.yaml`), and pointers to `architecture.md`, `roadmap.md`, `research.md`.
- [ ] **Step 7: Commit.** `git add cmd README.md` then `git commit -m "feat: add xdns check command and README"`.

---

### Task 5: Phase close — verification and review

**Files:**
- Modify: `architecture.md` (§10: add one sentence that omitted keys take the defaults in `config.Default()` and that lists have no default), `roadmap.md` (change-log row)

- [ ] **Step 1: Full verification.** Run `gofmt -l .` (prints nothing), `go vet ./...` (silent), `go test ./... -cover` (all PASS; `internal/config` ≥ 80%), `go build ./...`, `go mod verify`.
- [ ] **Step 2: Update the docs.** Add the §10 sentence and a `roadmap.md` change-log row dated 2026-10-18 ("Fase 0 selesai: `yaml.v3` dipasang di Task 2, bukan Task 1; nilai bawaan `config.Default()`"). Commit with `docs: record phase 0 decisions`.
- [ ] **Step 3: Review.** Run the `code-reviewer` agent over the whole Phase 0 diff. Fix CRITICAL and HIGH findings (new commit per fix, `fix:`), then repeat Step 1.
- [ ] **Step 4: Report.** Update the project memory, then give Master a short summary: what exists, verification output, review result, and the Phase 1 next step.
