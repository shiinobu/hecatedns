#!/bin/sh
# One-time migration, run as root from the install bundle: the certificate renewal gets its own user, hecate-acme, so a
# compromised DNS server or dashboard cannot read the Cloudflare token or the ACME account.
# Rollback: restore hecate-renew.service.before-acme-user, then chown -R hecate:hecate /var/lib/hecate/lego and daemon-reload.
set -eu
src=$(cd "$(dirname "$0")/.." && pwd)
etc=/etc/systemd/system
unit=$etc/hecate-renew.service
lego=/var/lib/hecate/lego
certs=$lego/certificates
[ "$(id -u)" -eq 0 ] || { echo "run as root"; exit 1; }
getent group hecate >/dev/null || { echo "no group hecate"; exit 1; }
test -d "$certs" || { echo "no certificate directory $certs"; exit 1; }
test -r "$unit" || { echo "missing $unit"; exit 1; }
getent passwd hecate-acme >/dev/null || useradd --system --gid hecate --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin hecate-acme
systemctl stop hecate-renew.timer 2>/dev/null || true
install -m 0755 -o root -g root "$src/post-renew.sh" /opt/hecate/post-renew.sh
test -e "$unit.before-acme-user" || cp -p "$unit" "$unit.before-acme-user"
sed -i -e 's/^User=hecate$/User=hecate-acme/' -e '/^StateDirectory=hecate$/d' -e 's/^UMask=0077$/UMask=0027/' "$unit"
for line in 'ExecStartPost=/opt/hecate/post-renew.sh' 'ReadWritePaths=/var/lib/hecate/lego' 'ProtectProc=invisible' 'ProcSubset=pid'; do
	grep -qxF "$line" "$unit" || sed -i "s|^NoNewPrivileges=true\$|&\n$line|" "$unit"
done
grep -q '^User=hecate-acme$' "$unit" || { echo "the renew unit does not run as hecate-acme"; exit 1; }
chown -R hecate-acme:hecate "$lego"
find "$lego" -type d -exec chmod 0750 {} +
find "$lego" -type f -exec chmod 0640 {} +
chmod -R go-rwx "$lego/accounts"
systemd-analyze verify "$unit"
systemctl daemon-reload
if ! systemctl start hecate-renew.service; then
	journalctl -u hecate-renew.service -n 20 --no-pager | sed 's/[a-z0-9]\{16,\}\./<label>./g'
	echo "RESULT: FAILED, the renewal did not run as hecate-acme"
	exit 1
fi
systemctl enable --now hecate-renew.timer
bad=0
for f in "$certs"/*.crt "$certs"/*.key; do
	runuser -u hecate -- test -r "$f" || { echo "hecate cannot read a certificate file"; bad=1; }
done
runuser -u hecate -- sh -c "ls '$lego/accounts'" >/dev/null 2>&1 && { echo "hecate can read the ACME accounts"; bad=1; }
runuser -u hecate -- sh -c "test -w '$lego'" && { echo "hecate can write to the lego directory"; bad=1; }
runuser -u hecate-acme -- sh -c "test -w '$lego' && test -r /etc/hecate/cloudflare.env" && { echo "hecate-acme can read cloudflare.env itself"; bad=1; }
ls -ld "$lego" "$certs" "$lego/accounts"
[ "$bad" -eq 0 ] || { echo "RESULT: FAILED, see above"; exit 1; }
echo "RESULT: OK, the renewal runs as hecate-acme and hecate can still read the certificate"
