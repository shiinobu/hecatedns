normalize_renew_domains() {
	unit=$1
	base=$2
	esc=$(printf '%s' "$base" | sed 's/\./\./g')
	sed -i \
		-e "/^ExecStart=/ s| --domains [a-z0-9-]\{1,\}\.$esc | |g" \
		-e "/^ExecStart=/ s| --domains \*\.$esc | |g" \
		-e "/^ExecStart=/ s| --domains | --domains *.$base --domains |" \
		"$unit"
}
