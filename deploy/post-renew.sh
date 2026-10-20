#!/bin/sh
# Runs after every certificate run as the renewal user. lego writes the key and the certificate with mode 0600, but
# the DNS server and the dashboard run as another user of the same group and must read them. A changed mode alone does
# not change the modification time, and the readers skip a file stamp they already rejected, so the file is touched too.
# The unit fails when a file is still not mode 0640 afterwards (for example a root-owned file from a manual run).
set -eu
dir=${HECATE_CERT_DIR:-/var/lib/hecate/lego/certificates}
find "$dir" -type f ! -perm 0640 -exec sh -c 'chmod 0640 "$1" && touch "$1"' sh {} \; || true
if find "$dir" -type f ! -perm 0640 | grep -q .; then
	echo "some certificate files are still not mode 0640" >&2
	exit 1
fi
