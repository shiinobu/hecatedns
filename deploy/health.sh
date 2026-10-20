#!/bin/sh
set -u

LOG_DIR=${HECATE_LOG_DIR:-/var/lib/hecate/logs}
CONFIG=${HECATE_CONFIG:-/etc/hecate/hecate.yaml}
TODAY=${HECATE_TODAY:-$(date +%F)}
SECTIONS=${HEALTH_SECTIONS:-"service resources firewall dot certificate queries warns ssh"}

red() {
	sed -E 's/[a-z0-9]{16,}\.[a-z0-9.-]+/<name>/g'
}

latest_query_log() {
	ls -t "$LOG_DIR"/queries-*.jsonl 2>/dev/null | head -n 1
}

section_service() {
	echo "--- service"
	echo "now=$(date '+%F %T %Z %z') $(uptime -p)"
	systemctl show hecate -p ActiveState -p SubState -p ActiveEnterTimestamp -p NRestarts -p MainPID 2>&1
	for unit in nftables chrony ssh hecate-renew.timer unattended-upgrades; do
		printf '%s: %s / %s\n' "$unit" "$(systemctl is-active "$unit" 2>&1)" "$(systemctl is-enabled "$unit" 2>&1)"
	done
	systemctl list-timers hecate-renew.timer --no-pager 2>&1 | head -n 3
}

section_resources() {
	echo "--- resources"
	free -m | head -n 2
	df -h /var/lib/hecate | tail -n 1
	journalctl -k --since today --no-pager 2>/dev/null | grep -i -E 'oom|out of memory|killed process' | tail -n 5
}

section_firewall() {
	echo "--- firewall counters on drop rules"
	nft list chain inet hecate input 2>&1 | grep counter
}

section_dot() {
	echo "--- DoT connections now (count per remote ip)"
	ss -tn state established '( sport = :853 )' 2>/dev/null | awk 'NR > 1 { print $4 }' | sed -E 's/:[0-9]+$//' | sort | uniq -c
}

section_certificate() {
	echo "--- certificate"
	cert=$(awk '/^[[:space:]]*cert_file:/ { print $2; exit }' "$CONFIG" 2>/dev/null | tr -d "\"'")
	if [ -z "$cert" ] || [ ! -r "$cert" ]; then
		echo "certificate file not readable"
		return
	fi
	end=$(openssl x509 -enddate -noout -in "$cert" | cut -d= -f2)
	left=$(( ($(date -d "$end" +%s) - $(date +%s)) / 86400 ))
	echo "certificate days left: $left (expires $end)"
}

client_table() {
	awk -F'"' '
	function seconds(ts) {
		return substr(ts, 12, 2) * 3600 + substr(ts, 15, 2) * 60 + substr(ts, 18, 2)
	}
	{
		ts = ""; client = ""
		for (i = 1; i <= NF; i++) {
			if ($i == "ts") ts = $(i + 2)
			if ($i == "client") client = $(i + 2)
		}
		if (ts == "" || client == "") next
		at = seconds(ts)
		count[client]++
		if (!(client in first)) first[client] = substr(ts, 12, 8)
		else if (at - prev[client] > gap[client]) gap[client] = at - prev[client]
		last[client] = substr(ts, 12, 8)
		prev[client] = at
	}
	END {
		for (c in count) {
			printf "%d %s first=%s last=%s longest_gap=%dh%02dm\n", count[c], c, first[c], last[c], gap[c] / 3600, (gap[c] % 3600) / 60
		}
	}' "$1" | sort -rn | head -n 12
}

latency_summary() {
	awk -F'"' '
	{
		action = ""; cached = 0; ms = ""
		for (i = 1; i <= NF; i++) {
			if ($i == "action") action = $(i + 2)
			if ($i == "cached" && $(i + 1) ~ /true/) cached = 1
			if ($i == "ms") { ms = $(i + 1); gsub(/[^0-9]/, "", ms) }
		}
		if (action == "pass" && !cached && ms != "") print ms
	}' "$1" | sort -n | awk '
	{ v[NR] = $1 }
	END {
		if (NR == 0) { print "uncached passed queries: none"; exit }
		p95 = int(NR * 0.95)
		if (p95 < NR * 0.95) p95++
		if (p95 < 1) p95 = 1
		printf "uncached passed queries: n=%d p50=%dms p95=%dms max=%dms\n", NR, v[int((NR + 1) / 2)], v[p95], v[NR]
	}'
}

section_queries() {
	echo "--- queries"
	file=$(latest_query_log)
	if [ -z "$file" ]; then
		echo "no query log"
		return
	fi
	echo "file=$(basename "$file") lines=$(wc -l < "$file")"
	echo "per client: count client first last longest_gap"
	client_table "$file"
	latency_summary "$file"
}

warn_table() {
	known=$(awk -F'"' '{ for (i = 1; i <= NF; i++) if ($i == "client") print $(i + 2) }' "$1" | sort -u | tr '\n' ' ')
	grep -F "$TODAY" "$LOG_DIR/hecate.log" 2>/dev/null | grep -E 'level=WARN| WARN ' | red | awk -v known="$known" '
	BEGIN {
		n = split(known, list, " ")
		for (i = 1; i <= n; i++) phone[list[i]] = 1
	}
	function address(line,   s) {
		if (!match(line, /(client|remote|addr)="?\[?[0-9a-fA-F:.]+/)) return ""
		s = substr(line, RSTART, RLENGTH)
		sub(/^[a-z]+="?\[?/, "", s)
		if (s ~ /^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+:[0-9]+$/) sub(/:[0-9]+$/, "", s)
		return s
	}
	function reason(line,   s) {
		if (match(line, /msg="[^"]*"/)) return substr(line, RSTART + 5, RLENGTH - 6)
		if (match(line, /msg=[^ ]+/)) return substr(line, RSTART + 4, RLENGTH - 4)
		return "other"
	}
	{
		who = (address($0) in phone) ? "known phone" : "others"
		total[reason($0) " | " who]++
	}
	END {
		for (k in total) printf "%d %s\n", total[k], k
	}' | sort -rn | head -n 20
}

section_warns() {
	echo "--- warnings today ($TODAY), count by reason and by source"
	file=$(latest_query_log)
	if [ -z "$file" ]; then
		echo "no query log, cannot tell the phone from others"
		return
	fi
	table=$(warn_table "$file")
	if [ -z "$table" ]; then
		echo "none"
	else
		echo "$table"
	fi
}

section_ssh() {
	echo "--- ssh failed logins today (count only)"
	journalctl -u ssh --since today --no-pager 2>/dev/null | grep -c -i -E 'failed|invalid user'
}

for section in $SECTIONS; do
	"section_$section"
done
