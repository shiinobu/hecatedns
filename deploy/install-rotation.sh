#!/bin/sh
# Run as root on the VPS after install-dashboard.sh: sh deploy/install-rotation.sh
# Moves the DoT names from the configuration into the names file that the dashboard edits (Rotate, Remove old) and
# restarts hecate once so the server reads that file. Every name is checked with a real TLS handshake afterwards; on a
# failure the old configuration is restored and hecate is restarted again. Names are never printed.
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

restore() {
	cp -p "$cfg.before-rotation" "$cfg"
	systemctl restart hecate.service
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
waited=0
until systemctl is-active --quiet hecate.service; do
	waited=$((waited + 1))
	[ "$waited" -le 20 ] || {
		restore
		rm -f "$namesfile"
		fail "hecate did not come back, the old configuration is restored"
	}
	sleep 1
done
sleep 2

bad=0
while read -r name; do
	openssl s_client -connect 127.0.0.1:853 -servername "$name" -verify_hostname "$name" -verify_return_error </dev/null >/dev/null 2>&1 || bad=$((bad + 1))
done <"$work/names"
if [ "$bad" -ne 0 ]; then
	restore
	rm -f "$namesfile"
	fail "$bad of $count names failed a TLS handshake, the old configuration is restored"
fi
echo "RESULT: OK, $count names are served from $namesfile and the dashboard can rotate them"
