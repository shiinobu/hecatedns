#!/bin/sh
set -u
umask 022

REQ_DIR=${HECATE_OPS_REQ:-/run/hecate-ops/req}
RES_DIR=${HECATE_OPS_RESULT:-/var/lib/hecate-ops/result}
OPS_HOME=${HECATE_OPS_HOME:-/opt/hecate/ops}
BACKUP_DIR=${HECATE_BACKUP_DIR:-/var/backups/hecate}
BACKUP_CMD=${HECATE_BACKUP_CMD:-/opt/hecate/backup.sh}
MAX_OUTPUT=65536
KEEP_BACKUPS=14

action=${1:-}
case "$action" in
health) cooldown=10; limit=120 ;;
backup) cooldown=60; limit=120 ;;
renew-test) cooldown=3600; limit=900 ;;
renew) cooldown=3600; limit=900 ;;
renew-force) cooldown=172800; limit=900 ;;
*)
	echo "hecate-op: unknown action" >&2
	exit 2
	;;
esac

log() {
	if command -v logger >/dev/null 2>&1; then
		logger -t hecate-op "action=$action $*"
	else
		echo "hecate-op: action=$action $*" >&2
	fi
}

stamp() {
	date -u +%Y-%m-%dT%H:%M:%SZ
}

red() {
	sed -E 's/[a-z0-9]{16,}\.[a-z0-9.-]+/<name>/g'
}

publish() {
	tmp=$(mktemp "$RES_DIR/.tmp.XXXXXX") || exit 1
	cat > "$tmp"
	chmod 0644 "$tmp"
	mv -f -- "$tmp" "$RES_DIR/$1"
}

write_backup_list() {
	for f in "$BACKUP_DIR"/hecate-*.tar.gz; do
		[ -f "$f" ] || continue
		printf '%s %s %s\n' "$(stat -c %Y "$f")" "$(stat -c %s "$f")" "$(basename "$f")"
	done | sort -rn | head -n "$KEEP_BACKUPS" | publish backups.list
}

unit_run() {
	since=$(date '+%F %T')
	systemctl start --wait "$1"
	code=$?
	journalctl -u "$1" -o cat --no-pager --since "$since" 2>/dev/null | tail -n 80
	return "$code"
}

run_command() {
	key=$(echo "$action" | tr 'a-z-' 'A-Z_')
	override=$(printenv "HECATE_OPS_CMD_$key" 2>/dev/null) || override=
	if [ -n "$override" ]; then
		sh -c "$override"
		return $?
	fi
	case "$action" in
	health) sh "$OPS_HOME/health.sh" ;;
	backup) "$BACKUP_CMD" ;;
	renew-test) unit_run hecate-renew-test.service ;;
	renew) unit_run hecate-renew.service ;;
	renew-force) unit_run hecate-renew-force.service ;;
	esac
}

too_soon() {
	finished=$(sed -n -e 's/^finished=//p' -e '/^---$/q' "$RES_DIR/$action.result" 2>/dev/null | head -n 1)
	[ -n "$finished" ] || return 1
	before=$(date -u -d "$finished" +%s 2>/dev/null) || return 1
	[ $(($(date +%s) - before)) -lt "$cooldown" ]
}

if [ "${2:-}" = "--exec" ]; then
	run_command
	exit $?
fi

if [ "$action" = backup ] && [ "${2:-}" = "--list" ]; then
	write_backup_list
	exit 0
fi

rm -f -- "$REQ_DIR/$action"
if too_soon; then
	log "skipped, it ran too recently"
	exit 0
fi

started=$(stamp)
printf 'status=running\nstarted=%s\n---\n' "$started" | publish "$action.result"
log "started"
out=$(mktemp "$RES_DIR/.out.XXXXXX") || exit 1
trap 'rm -f -- "$out"' EXIT
timeout "$limit" sh "$0" "$action" --exec > "$out" 2>&1
code=$?
if [ "$action" = backup ]; then
	write_backup_list
fi
status=ok
[ "$code" -eq 0 ] || status=failed
{
	printf 'status=%s\nstarted=%s\nfinished=%s\nexit=%s\n---\n' "$status" "$started" "$(stamp)" "$code"
	tr -d '\000' < "$out" | red | head -c "$MAX_OUTPUT"
} | publish "$action.result"
log "finished status=$status exit=$code"
exit 0
