/^dot:/ { in_dot = 1; next }
in_dot && /^[^ \t#]/ { in_dot = 0; in_names = 0 }
in_dot && /^  names:/ {
	line = $0
	sub(/[ \t]*#.*/, "", line)
	in_names = 0
	start = index(line, "[")
	if (start > 0) {
		stop = index(line, "]")
		inner = substr(line, start + 1, stop - start - 1)
		count = split(inner, parts, ",")
		for (i = 1; i <= count; i++) {
			gsub(/[ \t"']/, "", parts[i])
			if (parts[i] != "") print parts[i]
		}
	} else {
		in_names = 1
	}
	next
}
in_dot && in_names && /^    - / {
	value = $0
	sub(/^    - */, "", value)
	sub(/[ \t]+#.*$/, "", value)
	gsub(/["']/, "", value)
	print value
	next
}
in_dot && in_names && !/^[ \t]*(#.*)?$/ { in_names = 0 }
