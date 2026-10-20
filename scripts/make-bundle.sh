#!/bin/sh
# Builds the install bundle for the VPS as data/bundle.tgz (data/ is git-ignored). Run it from the repository on the PC:
#   sh scripts/make-bundle.sh <base domain, for example hecate.my.id>
# The dashboard access key and the secret dashboard host label are made once and kept in data/tls; the key itself never
# goes into the bundle, only its SHA-256. This script prints paths and sizes, never a key or a host name.
set -eu
base=${1:?usage: sh scripts/make-bundle.sh <base domain>}
root=$(cd "$(dirname "$0")/.." && pwd)
out=$root/data/bundle
keydir=$root/data/tls
cd "$root"
rm -rf "$out" "$root/data/bundle.tgz"
mkdir -p "$out" "$keydir"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$out/hecate" ./cmd/hecate
cp -r deploy "$out/deploy"
if [ ! -e "$keydir/dashboard-key.txt" ]; then
	go run ./cmd/hecate dashboard genkey -out "$keydir/dashboard-key.txt" -hash-out "$keydir/dashboard-key.sha256" >/dev/null
fi
go run ./cmd/hecate dashboard hashkey <"$keydir/dashboard-key.txt" >"$out/key.sha256"
if [ ! -e "$keydir/dashboard-label.txt" ]; then
	LC_ALL=C tr -dc 'a-z0-9' </dev/urandom | head -c 20 >"$keydir/dashboard-label.txt"
fi
label=$(cat "$keydir/dashboard-label.txt")
[ "${#label}" -eq 20 ] || {
	echo "the dashboard label file is not 20 characters"
	exit 1
}
cat >"$out/dashboard.block" <<EOF
dashboard:
  enabled: true
  listen: ["0.0.0.0:443", "[::]:443"]
  names:
    - $label.$base
  probe_domains: [$base]
  certs: []
  key_hash_file: /var/lib/hecate/dashboard/key.sha256
  secret_file: /var/lib/hecate/dashboard/cookie.secret
  session_days: 30
  ops:
    request_dir: /run/hecate-ops/req
    result_dir: /var/lib/hecate-ops/result
EOF
printf 'BASE_DOMAIN=%s\n' "$base" >"$out/settings.env"
tar -czf "$root/data/bundle.tgz" -C "$out" .
echo "bundle: data/bundle.tgz ($(wc -c <"$root/data/bundle.tgz") bytes)"
echo "key file (stays on this PC): data/tls/dashboard-key.txt"
