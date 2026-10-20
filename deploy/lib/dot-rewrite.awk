/^dot:/ { in_dot = 1; print; next }
in_dot && /^[^ \t#]/ { in_dot = 0; skipping = 0 }
in_dot && /^  names:/ {
	print "  names_file: " file
	line = $0
	sub(/[ \t]*#.*/, "", line)
	skipping = (index(line, "[") == 0)
	next
}
in_dot && skipping && /^    - / { next }
in_dot && skipping && /^[ \t]*(#.*)?$/ { print; next }
in_dot && skipping { skipping = 0 }
{ print }
