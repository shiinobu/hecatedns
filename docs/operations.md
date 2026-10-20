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

The server code is tested (generated certificates, attacker-style load on loopback), but the files in `deploy/` were written on Windows and first ran on Ubuntu 24.04 on 2026-10-20; the header of each file says what that run showed. Do the steps in order, keep a second SSH session open while you change the firewall, and treat each "check" line as required. The design and the numbers behind it are in [architecture.md](architecture.md), section 13.1.

What you need: a small Linux VPS with a public IPv4 address and root SSH access, and a domain you control whose DNS is hosted where `lego` can change it through an API for the DNS-01 challenge (the examples use Cloudflare, free plan). Only the `native` backend serves DoT.

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

Put `<label>.<base>` in `dot.names`. `lego` names its files after the first `--domains` entry; with `*.<base>` first (step 3) they are `_.<base>.crt` and `_.<base>.key`, which is what the certificate paths in the example use. Plain DNS stays off (`listen: []`), and `allowed_clients` is everyone because the server name is the key.

### 3. DNS records and the certificate

Create one record that points at the VPS address, **DNS only** (grey cloud, not proxied): `A *.<base>`. The bare `A <base>` is not needed. A proxy does not carry port 853, and a proxied wildcard answers with the proxy's addresses: every name you rotate to would then fail and a phone in strict Private DNS mode would lose its internet (see "When something is wrong", section 10). The wildcard record is what makes the secret label resolve: the phone looks the name up through its ordinary resolver before it connects.

Create an API token for that one zone with the permissions Zone / Zone / Read and Zone / DNS / Edit. Write `/etc/hecate/cloudflare.env` (owner root, mode `0600`) with two lines, `CF_DNS_API_TOKEN=...` and `LEGO_EMAIL=...`. Type the token without leaving it in the shell history (`read -s`). Request the first certificate **as the `hecate` user**, once against the staging CA to prove the method, then for real (drop the `--server` line):

```
sudo -u hecate env CF_DNS_API_TOKEN="$token" /opt/hecate/lego --path /var/lib/hecate/lego \
  --server https://acme-staging-v02.api.letsencrypt.org/directory \
  --email "$email" --accept-tos --dns cloudflare --dns.resolvers 1.1.1.1:53 \
  --domains '*.<base>' run
```

Do not list the bare name next to its wildcard: Let's Encrypt refuses an order that names a domain together with a wildcard that already covers it ("redundant with a wildcard"). The install script of section 9 rewrites the renewal unit this way for you.

`lego` creates and removes the challenge TXT records itself and waits until they are visible; expect a minute or two. Another DNS provider needs its own `--dns` code and token variables (see the lego documentation).

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

`deploy/nftables.conf` drops everything except SSH (22), DoT (853) and the dashboard (443), limits new connections and open connections per source (IPv4 address or IPv6 `/64`) and limits ping, while neighbour discovery and the ICMP errors IPv6 needs are not limited. It replaces only its own table `hecate`. Adjust the SSH rules first if your own address never changes (see the header of the file). Then, with a second SSH session open:

```
nft -c -f /root/nftables.conf
systemd-run --on-active=5m nft flush ruleset
nft -f /root/nftables.conf
```

Open a NEW SSH login from another terminal. Only when that works, stop the safety timer that `systemd-run` printed (`systemctl stop run-....timer`; name it with `--unit=` to find it easily) and make the rules permanent in your distribution's way (for example copy the file to `/etc/nftables.conf`, keep the old one as a backup, and `systemctl enable nftables`). From a different host, scan the VPS: only 22 and 853 may answer (and 443 once the dashboard of section 9 runs), port 53 must be closed.

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
- Renewal: `hecate-renew.timer` runs `lego renew --days 30` daily, as the user `hecate-acme` once section 9 is installed (the Cloudflare token is then out of reach of the DNS and dashboard processes). `deploy/post-renew.sh` runs after every certificate run and sets mode 0640 on the files (lego writes 0600) and touches them, so the readers pick them up. After a renewal the server reloads the files within `dot.reload_every` (default 10 minutes) and logs "the DoT certificate was reloaded"; a reloaded certificate that no longer covers a served name is refused and the old one stays. Test it with `systemctl start hecate-renew.service` and `journalctl -u hecate-renew`, or with the Renew test button of the dashboard (staging CA, nothing changes in production). From 14 days before expiry the server logs an hourly warning.
- Rotating the secret name: use Rotate and Remove old in the dashboard (section 9); no restart is needed. By hand: generate a new label, put it in `dot.names` and in the phone's Private DNS field, restart hecate. The wildcard certificate already covers it.
- Backup: `/opt/hecate/backup.sh` writes a tar of `hecate.yaml`, `rules.txt` and, when they exist, `dot-names.txt` and the dashboard key hash to `/var/backups/hecate` (14 kept, mode 0600). The archive contains the secret names, so treat it as a secret. The certificate key, the ACME account and `cloudflare.env` are not saved; after a restore request a new certificate. The Backup button of the dashboard runs the same script.
- Limits to know: 64 established DoT connections and 8 per source (a phone needs one or two), 512 connections waiting for a handshake (4 s deadline), 1,000 queries per connection, 200 queries per second per client with a burst of 500. The firewall caps are 60 new and 16 open connections per source.

### 8. What is proven and what is not

Seen on 2026-10-20 (Ubuntu 24.04 on a VPS with its own public IPv4 address, an Android phone on mobile data and on Wi-Fi): the Android strict-mode handshake and ALPN, a real Let's Encrypt wildcard certificate through DNS-01 on Cloudflare, the nftables rules (after one fix: a `ct count` set cannot have a timeout), the systemd units, the lego flags and file order, and a reboot that brings the service and the firewall back by themselves.

Seen on 2026-10-21: the dashboard installed from a bundle, the wildcard certificate requested with only `*.<base>` (after Let's Encrypt refused the bare name together with its wildcard on the staging CA), the 443 firewall rules loaded with the safety timer and made permanent, the names file switched on with one restart of the DNS server, a rotation from the dashboard that worked once the DNS record was DNS only, and the Control screen used from the phone.

Not proven yet: a real certificate renewal with the new domain list and the post-renew step (the certificate is valid until 2027-01-17 and renews at 30 days left), the seven-day stability run (restarted on 2026-10-21), and a full external port scan (only a connection test of a few ports was made). See [testing.md](testing.md).

## Dashboard on the VPS

The dashboard is a second unit, `hecate-dashboard`, of the same binary. Its design, limits and threat model are in [architecture.md](architecture.md), section 18. Everything below that changes the VPS is run by you as root; the scripts print a `RESULT:` line and never print a host name, a label or a key.

### 9. Install from a bundle

On the PC (a shell with Go and `sh`, from the repository root; the DNS record `A *.<base>` must exist, DNS only):

```
sh scripts/make-bundle.sh <base>
```

This builds the Linux binary and writes `data/bundle.tgz` (git-ignored) with the binary, `deploy/`, the config block of the dashboard, the SHA-256 of the access key and the base name. The access key (`data/tls/dashboard-key.txt`) and the secret host label (`data/tls/dashboard-label.txt`) are made once and stay on the PC; open them in an editor and never paste them into a chat, a ticket or a command line. The address of the dashboard is `https://<label>.<base>`.

Copy the bundle, unpack it and run the installer on the VPS:

```
scp data/bundle.tgz root@<ip>:/root/
ssh root@<ip> 'rm -rf /root/hecate-install && mkdir /root/hecate-install && tar -xzf /root/bundle.tgz -C /root/hecate-install && sh /root/hecate-install/deploy/install-dashboard.sh'
```

`install-dashboard.sh` does six steps and refuses to run twice (it stops when the configuration already has a `dashboard:` section). It does not restart `hecate` and it does not touch the firewall: 1 the renewal user `hecate-acme` (migrates the lego directory); 2 the new binary, copied to a temporary name and moved over `/opt/hecate/hecate` (the running DNS server keeps its old file until it restarts; the old binary is kept as `hecate.prev`); 3 the renewal unit with `*.<base>` as its first domain and the operations wrapper (`deploy/ops/`: the root-owned `run.sh` and the `hecate-op@` path units); 4 a trial certificate run on the Let's Encrypt staging CA, then the real certificate, then the configured certificate paths become symlinks to the new files (the DNS server reloads them by itself); 5 the key hash and the configuration block; 6 the dashboard unit. It ends with `RESULT: OK, the dashboard runs`. If it fails at the certificate run, check the Cloudflare token scope for the zone and the lego error text.

Then open the port with the safety net of step 5 (`nft -c -f`, a second SSH session, a timer that flushes the rules after 5 minutes), test a NEW SSH login, open the dashboard from the phone on mobile data, and only then make the rules permanent and stop the timer.

### Names file and Rotate

```
ssh root@<ip> 'sh /root/hecate-install/deploy/install-rotation.sh'
```

moves the DoT names from the configuration into `/var/lib/hecate/dot-names.txt` (the file the dashboard edits), restarts `hecate` once, checks every name with a real TLS handshake, and restarts the dashboard. If a check fails it restores the old configuration, restarts again, and as a last resort puts back the previous binary. The restart resets any stability run in progress, so choose the moment.

To change the name your phone uses, open the Connection screen:

1. Run the network test for the domain, then press Rotate. A new `<random>.<base>` appears.
2. Type it into Settings, Network, Private DNS on the phone and check that the internet works on Wi-Fi and on mobile data. Until then the old name keeps working.
3. Press Remove old on the name marked Older.
4. Only after that delete any old DNS records for names you no longer use.

Rotate can answer `not_reachable`: the name resolves, but not to this server (see section 10). At most 4 names can exist in the file; remove one before adding another.

### Update the binary

`install-dashboard.sh` cannot be run again. To ship a new release of the dashboard, build a new bundle, copy it, and swap the binary; only the dashboard restarts, the DNS server and its stability run are untouched:

```
scp data/bundle.tgz root@<ip>:/root/
ssh root@<ip> 'rm -rf /root/hecate-install && mkdir /root/hecate-install && tar -xzf /root/bundle.tgz -C /root/hecate-install && cp -p /opt/hecate/hecate /opt/hecate/hecate.prev && install -m 0755 -o root -g root /root/hecate-install/hecate /opt/hecate/hecate.new && mv /opt/hecate/hecate.new /opt/hecate/hecate && systemctl restart hecate-dashboard && sleep 2 && systemctl is-active hecate-dashboard && sha256sum /opt/hecate/hecate | cut -c1-16'
```

Run the two commands one at a time and wait for the copy to finish; if the copy fails, the second command would install the previous bundle. Compare the printed fingerprint with `tar -xzOf data/bundle.tgz ./hecate | sha256sum | cut -c1-16` on the PC. Roll back with `cp -p /opt/hecate/hecate.prev /opt/hecate/hecate.new && mv /opt/hecate/hecate.new /opt/hecate/hecate && systemctl restart hecate-dashboard`. The DNS server runs the new binary only after its own next restart. The phone needs no cache clearing: the dashboard files are revalidated on every load.

### 10. When something is wrong

- **The whole phone has no internet after typing a new Private DNS name.** Strict mode is fail-closed: if the name cannot be resolved or connected, nothing works. First get the phone back: set Private DNS to the old name (if it is still listed) or to Automatic. Then, from the VPS, run `dig +short @1.1.1.1 <name>`: the answer must be only the server's address. Cloudflare addresses mean the wildcard record is proxied; make it DNS only. A stale negative answer for the domain in the router or the carrier's resolver has caused the same symptom; a fresh Rotate gives a name that was never cached. Never judge from the server log alone: the log shows only what reached the server.
- **The dashboard does not open.** Check, in this order: the unit (`systemctl is-active hecate-dashboard`), the DNS record (`dig +short @1.1.1.1 <label>.<base>` must be the server address), the 443 rules and the safety timer, the address (the exact secret host name), and the key (a wrong key gives 401 and then 429 after five tries a minute). The dashboard answers only to its own names; any other host name is closed without a body.
- **After an update the tabs look grey or do not switch.** The browser mixed a new page with old cached styles and scripts, which releases before 2026-10-21 allowed for up to a day. Clear "cached images and files" for the site once; later releases are revalidated on every load.
- **The icon in the browser tab did not change.** Browsers cache icons for a long time; it changes after a while.
- **Rotate says `not_served`.** The DNS server did not accept the new name within 6 seconds; nothing was changed. Check `journalctl -u hecate` for certificate coverage warnings.
- **A button of the Control screen says cooldown.** The wrapper limits repeats (health 10 s, backup 60 s, renewal test and renewal 1 h, forced renewal 48 h); it is not an error.
