// Package native implements the xdns Backend with its own server, upstream
// client and cache on top of miekg/dns.
package native

// The blank import pins github.com/miekg/dns in go.mod until this package
// gets real code in Phase 4.
import _ "github.com/miekg/dns"
