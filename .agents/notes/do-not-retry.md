# Do not retry (things that failed or were retracted, with the reason)

| Do not | Because | Do instead |
|---|---|---|
| Say "DNS blocks about 85-95% of ads" | Said early without a source, retracted. The only data is AdGuard's own (vendor; US news sites: DNS saved ~74% of the bandwidth an extension saved) | Give the honest limit (`rules/00-project-brief.md`); no percentage |
| Assume dnsproxy enforces `allowed_clients` | It has no client ACL | The engine enforces the ACL (first step of `Before`) |
| Use Go 1.26.8 | 6 stdlib/x-net vulnerabilities are reachable from our code | Go 1.26.9 + `golang.org/x/net` v0.60.0 (already in `go.mod`) |
| Use `go test -race` without enabling cgo | `-race requires cgo; enable cgo by setting CGO_ENABLED=1` | `CGO_ENABLED=1` for that one command (`rules/06-environment.md`) |
| Set `CGO_ENABLED=1` permanently | Breaks the Linux cross-build needed in Phase 6 | Per-command only |
| Add w64devkit's `bin` to PATH | Its `find`, `sort`, `sh` would shadow Windows tools | Leave PATH alone; `go env CC` already points at gcc |
| Use the `gh` CLI | Not installed | GitHub REST API with curl |
| Use the Exa MCP | Free limit used up | Plain web search/fetch |
| Expect uBlock Origin on Chrome for Android | Chrome for Android has no extension support | Firefox for Android + uBlock Origin as the documented complement |
| Rely on memory for dnsproxy / miekg/dns signatures | dnsproxy is 0.x, docs partly TODO | Read the real source (`workflows/spike-dnsproxy.md`) |
| Edit a Claude memory file with a partial string replace | The harness had re-serialized the frontmatter; the edit failed | (Claude-only note) read first, then rewrite the whole file |
| Create a worktree or branch for phase work | Single-person local repo; D16 trunk-based | Commit on `main` |
| Write the Phase N plan as a separate document | Master wants tasks first, docs last | Use `state/PHASE-N-TASKS.md` and the roadmap |
| Run the superpowers `task-start` script before the first commit | Printed `fatal: ambiguous argument 'HEAD'` | (Claude-only, cosmetic) not needed outside Claude |
| Review per task, or review from the diff only | Master wants one review per phase; the Phase 0 review found its 4 Important issues by running the binary and mutating code | One fresh-context review per phase that may read surrounding code and run things |
| Make the reviewer edit files | A reviewer reports; the implementer fixes | Reviewer is read-only |
| Pass `localhost:5300` as a `listen` value | Config rejects hostnames in `listen` | Use an IP: `127.0.0.1:5300` |
| Push or create a remote "as a backup" | Outward-facing; needs Master's explicit yes | Ask Master; the repo is local only for now |

Unresolved, do not guess: which domain triggers the Play Store redirect on Master's phone (needs the Phase 3 query log).
