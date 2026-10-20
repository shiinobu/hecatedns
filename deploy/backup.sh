#!/bin/sh
# The archives hold hecate.yaml and dot-names.txt, which name the secret DoT server names: treat every archive as a secret.
# The certificate key, the ACME account and cloudflare.env are not saved; after a restore, request a new certificate.
set -eu
umask 077
dest=/var/backups/hecate
keep=14
mkdir -p "$dest"
stamp=$(date +%Y%m%d-%H%M%S)
files="etc/hecate/hecate.yaml var/lib/hecate/rules.txt"
for optional in var/lib/hecate/dot-names.txt var/lib/hecate/dashboard/key.sha256 var/lib/hecate/lifetime-counts.json; do
  if [ -f "/$optional" ]; then
    files="$files $optional"
  fi
done
tar -czf "$dest/hecate-$stamp.tar.gz" -C / $files
ls -1t "$dest"/hecate-*.tar.gz | tail -n +$((keep + 1)) | xargs -r rm --
echo "$dest/hecate-$stamp.tar.gz"
