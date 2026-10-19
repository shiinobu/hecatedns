# Operations

## Run in development

```
go build -o bin/hecate.exe ./cmd/hecate
bin/hecate.exe check -config configs/hecate.example.yaml
bin/hecate.exe run   -config hecate.yaml
```

Copy `configs/hecate.example.yaml` to `hecate.yaml` (git-ignored) and adjust it. The development port is `127.0.0.1:5300`. Query it with:

```
powershell -File scripts/windows/dnsquery.ps1 example.com -Port 5300
powershell -File scripts/windows/dnsquery.ps1 www.example.com -Type HTTPS -Port 5300
```

Windows `nslookup` accepts `-port` but ignores it, which is why this script builds a plain UDP query by hand. Parameters: `-Name` (position 0), `-Type` (A, AAAA, CNAME, TXT, MX, HTTPS, SVCB, NS, SOA), `-Server` (default `127.0.0.1`), `-Port` (default 5300), `-TimeoutSeconds`. It prints the response code, the A/AAAA/CNAME records and the number of other records.

## Day-to-day commands

```
hecate lists update -config hecate.yaml            download or refresh all enabled lists
hecate lists status -config hecate.yaml            show the list cache status
hecate rule add block example.com -config hecate.yaml
hecate rule add allow ok.example.com -config hecate.yaml
hecate rule list   -config hecate.yaml
hecate rule remove example.com -config hecate.yaml
hecate log -since 10m -blocked -config hecate.yaml
```

The server picks up rule and list changes without a restart. Always run `hecate check` after editing the config.

## Windows: start at boot (Task Scheduler)

`scripts/windows/autostart.ps1` installs, removes or inspects a scheduled task named `hecate`. Run it from an elevated PowerShell:

```
powershell -File scripts/windows/autostart.ps1 install
powershell -File scripts/windows/autostart.ps1 status
powershell -File scripts/windows/autostart.ps1 remove
```

- **install** creates the task: it starts 30 seconds after boot without a login, as the current account with ordinary rights (not SYSTEM, not "highest"), hidden, with no time limit. The trigger repeats every minute for 365 days from each boot; because the task never starts a second copy, the repeat only revives hecate if it is not running (for example when Wi-Fi had not yet assigned the LAN address and hecate exited with an error). It changes Windows settings.
- **remove** stops and deletes the task; this is the only rollback needed.
- **status** shows the task state and the hecate process.

The task runs `bin\hecate.exe run -config hecate.yaml` from the project folder. Firewall rules are bound to the path `bin\hecate.exe`, so rebuild to the same path and do not move it. The operational log is `<log.dir>\hecate.log` (up to 5 MB plus one backup).

Because the trigger repeats, `Stop-ScheduledTask` alone does not keep hecate down, and a running executable cannot be overwritten. To deploy a new binary:

```
Disable-ScheduledTask -TaskName hecate
Stop-ScheduledTask   -TaskName hecate
# copy the new bin\hecate.exe
Enable-ScheduledTask -TaskName hecate
Start-ScheduledTask  -TaskName hecate
```

To stop the server until you start it again, run `Disable-ScheduledTask` and stop the process, or use `remove`. Firewall rules (group `hecate`) are removed with `Remove-NetFirewallRule -Group hecate`.

Notes: never bind `0.0.0.0:53` and never stop the Windows ICS service; bind the specific LAN address. A phone whose only DNS is the PC loses name resolution while the server is off (DNS 2 is empty on purpose), so switch it back to automatic DNS when the server is stopped.

## Linux / VPS

Planned: systemd service, DoT on 853, Let's Encrypt, hardening.
