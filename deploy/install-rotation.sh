#!/bin/sh
# Run as root on the VPS after install-dashboard.sh: sh deploy/install-rotation.sh
# Moves the DoT names from the configuration into the names file that the dashboard edits (Rotate, Remove old) and
# restarts hecate once so the server reads that file, then restarts the dashboard so it sees the names file too. Every
# name is checked with a real TLS handshake afterwards. On a failure the old configuration is restored, hecate is
# restarted again and checked again, and if that does not help either the previous binary (hecate.prev) is put back.
# Names are never printed.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
lib=$here/lib
cfg=/etc/hecate/hecate.yaml
namesfile=/var/lib/hecate/dot-names.txt
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() {
	echo "RESULT: FAILED, $1"
	exit 1
}

wait_active() {
	waited=0
	until systemctl is-active --quiet hecate.service; do
		waited=$((waited + 1))
		[ "$waited" -le 20 ] || return 1
		sleep 1
	done
	sleep 2
}

failed_handshakes() {
	bad=0
	while read -r name; do
		openssl s_client -connect 127.0.0.1:853 -servername "$name" -verify_hostname "$name" -verify_return_error </dev/null >/dev/null 2>&1 || bad=$((bad + 1))
	done <"$work/names"
	echo "$bad"
}

serving() {
	wait_active && [ "$(failed_handshakes)" -eq 0 ]
}

restore() {
	cp -p "$cfg.before-rotation" "$cfg"
	rm -f "$namesfile"
	systemctl restart hecate.service
	if serving; then
		echo "the old configuration is back and every name answers"
		return 0
	fi
	if test -r /opt/hecate/hecate.prev; then
		cp -p /opt/hecate/hecate.prev /opt/hecate/hecate.new
		mv /opt/hecate/hecate.new /opt/hecate/hecate
		systemctl restart hecate.service
		if serving; then
			echo "the previous binary had to be put back as well"
			return 0
		fi
	fi
	echo "WARNING: DoT does not answer after the rollback, look at the server now"
	return 1
}

[ "$(id -u)" -eq 0 ] || fail "run as root"
test -r "$cfg" || fail "missing $cfg"
grep -q '^  names_file:' "$cfg" && fail "the configuration already uses a names file"
test -e "$namesfile" && fail "$namesfile already exists"
awk -f "$lib/dot-names.awk" "$cfg" >"$work/names"
[ -s "$work/names" ] || fail "no DoT names were found in the configuration"
count=$(wc -l <"$work/names")

stamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)
while read -r name; do
	printf '%s %s\n' "$name" "$stamp"
done <"$work/names" >"$work/names.file"
test -e "$cfg.before-rotation" || cp -p "$cfg" "$cfg.before-rotation"
awk -v file="$namesfile" -f "$lib/dot-rewrite.awk" "$cfg.before-rotation" >"$work/hecate.yaml"
grep -q "^  names_file: $namesfile\$" "$work/hecate.yaml" || fail "the configuration could not be rewritten"

install -m 0640 -o hecate -g hecate "$work/names.file" "$namesfile"
cat "$work/hecate.yaml" >"$cfg"
if ! /opt/hecate/hecate check -config "$cfg" >/dev/null 2>"$work/check.err"; then
	sed 's/[a-z0-9]\{16,\}\./<label>./g' "$work/check.err"
	cp -p "$cfg.before-rotation" "$cfg"
	rm -f "$namesfile"
	fail "the configuration check failed, the old configuration is back and hecate was not restarted"
fi

echo "restarting hecate once ($count names)"
systemctl restart hecate.service
if ! wait_active; then
	restore || true
	fail "hecate did not come back, the rollback was tried"
fi
bad=$(failed_handshakes)
if [ "$bad" -ne 0 ]; then
	restore || true
	fail "$bad of $count names failed a TLS handshake, the rollback was tried"
fi

systemctl try-restart hecate-dashboard.service
sleep 2
if systemctl is-enabled --quiet hecate-dashboard.service && ! systemctl is-active --quiet hecate-dashboard.service; then
	fail "hecate serves the names file, but the dashboard did not come back: systemctl status hecate-dashboard"
fi
echo "RESULT: OK, $count names are served from $namesfile and the dashboard can rotate them"
