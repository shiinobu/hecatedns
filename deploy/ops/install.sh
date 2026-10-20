#!/bin/sh
set -eu
src=$(cd "$(dirname "$0")" && pwd)
etc=/etc/systemd/system
renew=$etc/hecate-renew.service
force=$etc/hecate-renew-force.service
staging=$etc/hecate-renew-test.service
actions="health backup renew-test renew renew-force"
test -r "$renew" || { echo "missing $renew"; exit 1; }
install -d -m 0755 -o root -g root /opt/hecate/ops
install -m 0755 -o root -g root "$src/run.sh" /opt/hecate/ops/run.sh
install -m 0755 -o root -g root "$src/../health.sh" /opt/hecate/ops/health.sh
install -m 0644 "$src/hecate-ops.conf" /etc/tmpfiles.d/hecate-ops.conf
systemd-tmpfiles --create /etc/tmpfiles.d/hecate-ops.conf
install -m 0644 "$src/hecate-op@.service" $etc/hecate-op@.service
install -m 0644 "$src/hecate-op@.path" $etc/hecate-op@.path
sed 's/--days 30/--days 90/' "$renew" > "$force"
sed -e 's|--path /var/lib/hecate/lego|--path /tmp/lego-staging|' \
	-e 's|--accept-tos|--server https://acme-staging-v02.api.letsencrypt.org/directory --accept-tos|' \
	-e 's| renew --days 30$| run|' \
	-e '/^ExecStartPost=/d' -e '/^ReadWritePaths=/d' \
	-e 's|^\[Service\]$|[Service]\nInaccessiblePaths=/var/lib/hecate/lego|' "$renew" > "$staging"
grep -q -- '--days 90' "$force" || { echo "force unit not derived"; exit 1; }
grep -q -- '--days 30' "$force" && { echo "force unit still renews at 30 days"; exit 1; }
grep -q -- '--path /tmp/lego-staging' "$staging" || { echo "test unit does not use the staging directory"; exit 1; }
grep -q -- '--path /var/lib/hecate/lego' "$staging" && { echo "test unit still points at the production directory"; exit 1; }
grep -q 'acme-staging' "$staging" || { echo "test unit does not use the staging CA"; exit 1; }
grep -q '^ExecStart=.* run$' "$staging" || { echo "test unit does not end in run"; exit 1; }
grep -q '^InaccessiblePaths=/var/lib/hecate/lego$' "$staging" || { echo "test unit lacks the production lock"; exit 1; }
grep -q -e '^ExecStartPost=' -e '^ReadWritePaths=' "$staging" && { echo "test unit still has a production hook or write path"; exit 1; }
grep -q '^ExecStartPost=/opt/hecate/post-renew.sh$' "$force" || { echo "force unit lost the post-renew hook"; exit 1; }
systemd-analyze verify "$force" "$staging" $etc/hecate-op@health.service $etc/hecate-op@health.path
systemctl daemon-reload
for action in $actions; do
	systemctl enable --now "hecate-op@$action.path"
done
sh /opt/hecate/ops/run.sh backup --list
bad=0
for action in $actions; do
	state=$(systemctl is-active "hecate-op@$action.path" || true)
	printf '%s: %s\n' "hecate-op@$action.path" "$state"
	[ "$state" = active ] || bad=1
done
ls -ld /run/hecate-ops/req /var/lib/hecate-ops/result /opt/hecate/ops
[ "$bad" -eq 0 ] || { echo "RESULT: FAILED, a path unit is not active"; exit 1; }
echo "RESULT: OK, the dashboard operations are installed"
