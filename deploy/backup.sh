#!/bin/sh
set -eu
umask 077
dest=/var/backups/hecate
keep=14
mkdir -p "$dest"
stamp=$(date +%Y%m%d-%H%M%S)
tar -czf "$dest/hecate-$stamp.tar.gz" -C / etc/hecate/hecate.yaml var/lib/hecate/rules.txt
ls -1t "$dest"/hecate-*.tar.gz | tail -n +$((keep + 1)) | xargs -r rm --
echo "$dest/hecate-$stamp.tar.gz"
