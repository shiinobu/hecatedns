/^dot:/ { in_dot = 1; next }
in_dot && /^[^ \t#]/ { in_dot = 0 }
in_dot && $1 == field ":" { print $2; exit }
