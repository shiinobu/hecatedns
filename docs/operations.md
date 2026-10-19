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

## Linux VPS with DNS-over-TLS

The server code is tested (generated certificates, attacker-style load on loopback), but the files in `deploy/` were written on Windows and have **never run on Linux**: every file says so in its header. Do the steps in order, keep a second SSH session open while you change the firewall, and treat each "check" line as required. The design and the numbers behind it are in [architecture.md](architecture.md), section 13.1.

What you need: a small Linux VPS with a public IPv4 address and root SSH access, and a domain you control whose DNS can be changed through an API for the DNS-01 challenge (the examples use DuckDNS). Only the `native` backend serves DoT.

### 1. Build and install

```
GOOS=linux GOARCH=amd64 go build -o hecate ./cmd/hecate
GOOS=linux GOARCH=amd64 GOBIN="$PWD/linux-bin" go install github.com/go-acme/lego/v4/cmd/lego@v4.35.2
```

On the VPS (as root):

```
useradd --system --home-dir /var/lib/hecate --shell /usr/sbin/nologin hecate
install -d -m 0755 /opt/hecate
install -d -m 0750 -o root -g hecate /etc/hecate
install -m 0755 hecate lego /opt/hecate/
install -d -m 0700 -o hecate -g hecate /var/lib/hecate
```

Copy `deploy/hecate.service`, `deploy/hecate-renew.service` and `deploy/hecate-renew.timer` to `/etc/systemd/system/`, and `deploy/backup.sh` to `/opt/hecate/`.

### 2. Configuration and the secret name

Copy `configs/hecate.vps.example.yaml` to `/etc/hecate/hecate.yaml` (`chown root:hecate`, mode `0640`) and replace `example.org` with your base name. Make the secret label long and random:

```
tr -dc a-z0-9 </dev/urandom | head -c 20
```

Put `<label>.<base>` in `dot.names`. The certificate paths in the example match what `lego --path /var/lib/hecate/lego` writes for a first domain `<base>`. Plain DNS stays off (`listen: []`), and `allowed_clients` is everyone because the server name is the key.

### 3. The certificate

Write `/etc/hecate/duckdns.env` (owner root, mode `0600`) with two lines, `DUCKDNS_TOKEN=...` and `LEGO_EMAIL=...`. Type the token without leaving it in the shell history (`read -s`). Request the first certificate **as the `hecate` user**, once against the staging CA to prove the method, then for real (drop the `--server` line):

```
sudo -u hecate env DUCKDNS_TOKEN="$token" /opt/hecate/lego --path /var/lib/hecate/lego \
  --server https://acme-staging-v02.api.letsencrypt.org/directory \
  --email "$email" --accept-tos --dns duckdns --dns.resolvers 1.1.1.1:53 --dns.propagation-wait 30s \
  --domains <base> --domains '*.<base>' run
```

The base name and the wildcard challenges run one after the other and take about 2.5 minutes. Point the DuckDNS name at the VPS address before the real run if you also want the plain name to resolve.

Check: `sudo -u hecate /opt/hecate/hecate check -config /etc/hecate/hecate.yaml` must print `dot certificate: PASSED` with a date. It loads the certificate pair and fails if it is expired, does not match its key or does not cover every `dot.names` entry; it never prints the names.

### 4. The service

```
systemd-analyze verify /etc/systemd/system/hecate.service
systemctl daemon-reload
systemctl enable --now hecate hecate-renew.timer
journalctl -u hecate -e
```

Check that the service user can read the configuration (`sudo -u hecate cat /etc/hecate/hecate.yaml`) and that the start-up output shows a `listening: tls` line (only the first DoT address is shown, so with both `0.0.0.0:853` and `[::]:853` test both families with step 6). A `[::]:853` entry alone serves IPv6 only; `hecate check` warns about it. A broken or expired certificate makes hecate exit; the unit restarts it every 30 seconds, at most 5 times in 5 minutes.

### 5. The firewall (the step that can lock you out)

`deploy/nftables.conf` drops everything except SSH (22) and DoT (853), limits new connections and open connections per source (IPv4 address or IPv6 `/64`) and limits ping, while neighbour discovery and the ICMP errors IPv6 needs are not limited. It replaces only its own table `hecate`. Adjust the SSH rules first if your own address never changes (see the header of the file). Then, with a second SSH session open:

```
nft -c -f /root/nftables.conf
systemd-run --on-active=5m nft flush ruleset
nft -f /root/nftables.conf
```

Open a NEW SSH login from another terminal. Only when that works, stop the safety timer that `systemd-run` printed (`systemctl stop run-....timer`) and make the rules permanent in your distribution's way (for example `/etc/nftables.conf` and `systemctl enable nftables`). From a different host, scan the VPS: only 22 and 853 may answer, port 53 must be closed.

### 6. Tests from outside

```
openssl s_client -connect <ip>:853 -servername <label>.<base> -alpn dot </dev/null
openssl s_client -connect <ip>:853 -servername wrong.<base> </dev/null
kdig @<ip> +tls-hostname=<label>.<base> example.com
```

The first handshake must complete with the real certificate and ALPN `dot`; the second must be refused with an alert and no answer; the third (from knot-dnsutils) must return an answer. A client with no server name, or the bare IP, must be refused too. Then open Android Settings, Network, Private DNS, "Private DNS provider hostname", and enter `<label>.<base>`. Repeat the ad-test page on mobile data and on another Wi-Fi.

### 7. Day to day on the VPS

- Logs: `journalctl -u hecate`, the operational log and the query log in `/var/lib/hecate/logs/`. "dropping queries" warnings come with a reason and the client address; refusals by a cap, unknown server names and failed handshakes appear there, scanner noise included, at most one line per reason every 10 seconds.
- Rules and lists: run the same commands as at home as the service user, with `-config /etc/hecate/hecate.yaml` (for example `sudo -u hecate /opt/hecate/hecate rule add block example.com -config /etc/hecate/hecate.yaml`). The server picks changes up without a restart.
- Renewal: `hecate-renew.timer` runs `lego renew --days 30` daily. After a renewal the server reloads the files within `dot.reload_every` (default 10 minutes) and logs "the DoT certificate was reloaded". Test it with `systemctl start hecate-renew.service` and `journalctl -u hecate-renew`. From 14 days before expiry the server logs an hourly warning.
- Rotating the secret name: generate a new label, put it in `dot.names` and in the phone's Private DNS field, restart hecate. The wildcard certificate already covers it.
- Backup: `/opt/hecate/backup.sh` writes a tar of `hecate.yaml` and `rules.txt` to `/var/backups/hecate` (14 kept, mode 0600). The archive contains the secret names, so treat it as a secret. The certificate key, the ACME account and `duckdns.env` are not saved; after a restore request a new certificate.
- Limits to know: 64 established DoT connections and 8 per source (a phone needs one or two), 512 connections waiting for a handshake (4 s deadline), 1,000 queries per connection, 200 queries per second per client with a burst of 500. The firewall caps are 60 new and 16 open connections per source.

### 8. What is not proven yet

The Android strict-mode handshake and ALPN against this server, a real Let's Encrypt certificate and renewal, the behaviour of a mobile carrier's NAT, the nftables syntax and semantics (`ct count` with a masked key, dynamic sets), the systemd options, the lego flags and file order, the seven-day stability run, and an external port scan. See [testing.md](testing.md).
