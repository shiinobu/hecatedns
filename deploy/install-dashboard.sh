#!/bin/sh
# Run as root on the VPS from the extracted install bundle: sh deploy/install-dashboard.sh
# Installs the new binary, the renewal user, the operations wrapper, a certificate that also covers *.<base domain> and
# the dashboard. It does not restart hecate (the DNS server) and it does not touch the firewall.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
bundle=$(cd "$here/.." && pwd)
cfg=/etc/hecate/hecate.yaml
etc=/etc/systemd/system
renew=$etc/hecate-renew.service
certs=/var/lib/hecate/lego/certificates
redact='s/[a-z0-9]\{16,\}\./<label>./g'

fail() {
	echo "RESULT: FAILED, $1"
	exit 1
}

[ "$(id -u)" -eq 0 ] || fail "run as root"
for f in "$bundle/hecate" "$bundle/dashboard.block" "$bundle/key.sha256" "$bundle/settings.env" "$cfg" /opt/hecate/hecate "$renew"; do
	test -r "$f" || fail "missing $f"
done
. "$bundle/settings.env"
[ -n "${BASE_DOMAIN:-}" ] || fail "settings.env has no BASE_DOMAIN"
grep -q '^dashboard:' "$cfg" && fail "the config already has a dashboard section"

echo "1/6 the renewal user"
if grep -q '^User=hecate-acme$' "$renew"; then
	echo "already done"
else
	sh "$here/users/acme.sh"
fi

echo "2/6 the binary (the running hecate keeps its old file until it restarts)"
test -e /opt/hecate/hecate.prev || cp -p /opt/hecate/hecate /opt/hecate/hecate.prev
install -m 0755 -o root -g root "$bundle/hecate" /opt/hecate/hecate.new
mv /opt/hecate/hecate.new /opt/hecate/hecate

echo "3/6 the renew unit and the operations wrapper"
. "$here/lib/renew-domains.sh"
test -e "$renew.before-wildcard" || cp -p "$renew" "$renew.before-wildcard"
normalize_renew_domains "$renew" "$BASE_DOMAIN"
grep -q -- " --domains \*\.$BASE_DOMAIN --domains " "$renew" || fail "the renew unit could not be rewritten"
grep -q ' renew --days 30$' "$renew" || fail "the renew unit lost its renew command"
systemctl daemon-reload
sh "$here/ops/install.sh"

echo "4/6 a trial run on the Let's Encrypt staging CA, then the real certificate"
systemctl start hecate-renew-test.service || fail "the staging run failed: $(journalctl -u hecate-renew-test.service -n 15 --no-pager | sed "$redact")"
newcrt=$certs/_.$BASE_DOMAIN.crt
newkey=$certs/_.$BASE_DOMAIN.key
covers_wildcard() {
	test -s "$newcrt" && openssl x509 -in "$newcrt" -noout -ext subjectAltName | grep -q "DNS:\*\.$BASE_DOMAIN"
}
if ! covers_wildcard; then
	sed 's| renew --days 30$| run|' "$renew" >"$etc/hecate-renew-issue.service"
	systemctl daemon-reload
	if ! systemctl start hecate-renew-issue.service; then
		journalctl -u hecate-renew-issue.service -n 15 --no-pager | sed "$redact"
		rm -f "$etc/hecate-renew-issue.service"
		systemctl daemon-reload
		fail "the certificate run failed, nothing was switched"
	fi
	rm -f "$etc/hecate-renew-issue.service"
	systemctl daemon-reload
fi
covers_wildcard || fail "the new certificate does not cover *.$BASE_DOMAIN"
uncovered=$(awk -f "$here/lib/dot-names.awk" "$cfg" | while read -r name; do
	openssl x509 -in "$newcrt" -noout -checkhost "$name" | grep -q 'does match' || echo x
done | wc -l)
[ "$uncovered" -eq 0 ] || fail "the new certificate does not cover $uncovered of the current DoT names, nothing was switched"
echo "switching the configured certificate paths to the new files (the DNS server reloads them by itself)"
for field in cert_file key_file; do
	old=$(awk -v field="$field" -f "$here/lib/dot-field.awk" "$cfg")
	[ -n "$old" ] || fail "the configuration has no dot.$field"
	case $field in
	cert_file) new=$newcrt ;;
	*) new=$newkey ;;
	esac
	if [ "$old" != "$new" ] && [ ! -L "$old" ]; then
		test -e "$old.before-wildcard" || cp -p "$old" "$old.before-wildcard"
		ln -s "$new" "$old.link"
		chown -h hecate-acme:hecate "$old.link"
		mv -T "$old.link" "$old"
	fi
done

echo "5/6 the key hash and the configuration"
install -d -m 0750 -o hecate -g hecate /var/lib/hecate/dashboard
install -m 0640 -o hecate -g hecate "$bundle/key.sha256" /var/lib/hecate/dashboard/key.sha256
test -e "$cfg.before-dashboard" || cp -p "$cfg" "$cfg.before-dashboard"
trap 'echo "If this stopped before RESULT: OK, the old configuration is in $cfg.before-dashboard (cp -p it back)"' EXIT
{
	echo
	cat "$bundle/dashboard.block"
} >>"$cfg"
checkerr=$(mktemp)
if ! /opt/hecate/hecate check -config "$cfg" >/dev/null 2>"$checkerr"; then
	cp -p "$cfg.before-dashboard" "$cfg"
	sed "$redact" "$checkerr"
	rm -f "$checkerr"
	fail "the configuration check failed, the old configuration is back"
fi
rm -f "$checkerr"

echo "6/6 the dashboard service"
install -m 0644 "$here/hecate-dashboard.service" "$etc/hecate-dashboard.service"
systemd-analyze verify "$etc/hecate-dashboard.service"
systemctl daemon-reload
systemctl enable --now hecate-dashboard.service
sleep 3
if ! systemctl is-active --quiet hecate-dashboard.service; then
	journalctl -u hecate-dashboard.service -n 20 --no-pager | sed "$redact"
	fail "the dashboard did not start"
fi
ss -ltn | grep -c ':443 ' | sed 's/^/listeners on port 443: /'
trap - EXIT
echo "RESULT: OK, the dashboard runs. Next: load the firewall with the safety net, then open it from the phone."
