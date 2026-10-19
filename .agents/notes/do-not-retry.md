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
| Push or create a remote "as a backup" | Outward-facing; needs Master's explicit yes | Ask Master; `origin` exists and was pushed twice on 2026-10-19 with his yes, but every push still needs his explicit yes |
| Rely on Task Scheduler "restart on failure" (`-RestartCount`, `-RestartInterval`) to bring hecate back | Tested 2026-10-19: a process killed with `Stop-Process`, and a task that exits with code 1 (restart count 3, interval 1 min), were not restarted (1 run in 215 s) | Boot trigger with repetition every minute and `MultipleInstances IgnoreNew` (`scripts/autostart.ps1`) |
| `[TimeSpan]::MaxValue` as the repetition duration of a trigger | Task Scheduler rejects it (`0x80041318`, `P99999999DT23H59M59S` out of range) | `New-TimeSpan -Days 365` (repetition restarts at every boot) |
| `Set-ScheduledTask -Settings` with a freshly built settings set to drop restart settings | Rejected: "task XML is missing a required element (Count)" | Change only the trigger with `Set-ScheduledTask -Trigger`, or `remove` then `install` |
| `$PSScriptRoot` in a `param()` default of a script on Windows PowerShell 5.1 | Empty there; `Split-Path` fails | Compute it in the script body |
| Backslash paths in Master's `!` shell commands | The `!` shell is Git Bash and eats the backslash (`scripts\autostart.ps1` became `scriptsautostart.ps1`) | Use forward slashes: `powershell -File scripts/autostart.ps1 install` |
| Run PowerShell commands that touch `scripts/autostart.ps1` from the agent | The harness refused them ("Unauthorized Persistence") even for a parse check, although Master asked for P5-02 | Master runs the install himself (`!`); do not work around it |
| Trust a context for cancellation of a miekg/dns exchange | miekg obeys the deadline of a context but not its cancellation; shutdown waited out the upstream timeout | Close the connection on cancel (`context.AfterFunc(ctx, conn.Close)`) |
| Use the request context's deadline inside a net/http dial function | Since Go 1.23 net/http dials with a context that has lost the request deadline | Give the dial its own budget with `context.WithTimeout` |
| Pick a free test port by trying 100 random ports on Windows | Windows excludes 100-port blocks of TCP ports (Hyper-V/WinNAT); 1 flake in 25 runs | Try up to 1,000 ports |
| Replace `bin\hecate.exe` while the scheduled task is running, or stop hecate with `Stop-ScheduledTask` alone | A running exe is locked, and the trigger repeats every minute, so hecate is started again within a minute | `Disable-ScheduledTask`, `Stop-ScheduledTask`, copy, `Enable-ScheduledTask`, `Start-ScheduledTask` |
| Rename the project folder, or move Claude Code's memory and sessions, from inside a running Claude Code session | The session process and VS Code hold the folder (a rename fails with "in use"), and Claude Code keys memory, transcripts and temp by the folder path | Commit the in-repo rename first, then Master runs `hecate-migrate.ps1` (elevated) with Claude Code and VS Code closed; open the new folder afterwards |

Unresolved, do not guess: which domain triggers the Play Store redirect on Master's phone (needs the Phase 3 query log).
