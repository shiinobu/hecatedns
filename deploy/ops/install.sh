#!/bin/sh
set -eu
src=$(cd "$(dirname "$0")" && pwd)
renew=/etc/systemd/system/hecate-renew.service
test -r "$renew" || { echo "missing $renew"; exit 1; }
install -d -m 0755 /opt/hecate/ops
install -m 0755 -o root -g root "$src/run.sh" /opt/hecate/ops/run.sh
install -m 0755 -o root -g root "$src/../health.sh" /opt/hecate/ops/health.sh
install -m 0644 "$src/hecate-ops.conf" /etc/tmpfiles.d/hecate-ops.conf
systemd-tmpfiles --create /etc/tmpfiles.d/hecate-ops.conf
install -m 0644 "$src/hecate-op@.service" /etc/systemd/system/hecate-op@.service
install -m 0644 "$src/hecate-op@.path" /etc/systemd/system/hecate-op@.path
sed 's/--days 30/--days 90/' "$renew" > /etc/systemd/system/hecate-renew-force.service
sed -e 's|--path /var/lib/hecate/lego|--path /tmp/lego-staging|' \
	-e 's|--accept-tos|--server https://acme-staging-v02.api.letsencrypt.org/directory --accept-tos|' \
	-e 's| renew --days 30$| run|' "$renew" > /etc/systemd/system/hecate-renew-test.service
grep -q -- '--days 90' /etc/systemd/system/hecate-renew-force.service || { echo "force unit not derived"; exit 1; }
grep -q 'acme-staging' /etc/systemd/system/hecate-renew-test.service || { echo "test unit not derived"; exit 1; }
grep -q ' run$' /etc/systemd/system/hecate-renew-test.service || { echo "test unit still renews"; exit 1; }
systemd-analyze verify /etc/systemd/system/hecate-renew-force.service /etc/systemd/system/hecate-renew-test.service /etc/systemd/system/hecate-op@health.service
systemctl daemon-reload
for action in health backup renew-test renew renew-force; do
	systemctl enable --now "hecate-op@$action.path"
done
sh /opt/hecate/ops/run.sh backup --list
for action in health backup renew-test renew renew-force; do
	printf '%s: %s\n' "hecate-op@$action.path" "$(systemctl is-active "hecate-op@$action.path")"
done
ls -ld /run/hecate-ops/req /var/lib/hecate-ops/result
echo "RESULT: OK, the dashboard operations are installed"
